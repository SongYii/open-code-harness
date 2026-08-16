package localexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// Enforcement is the sandbox claim this adapter can honestly make.
const Enforcement = "partial"

// DefaultMaxBytes is the combined stdout+stderr cap when MaxBytes is unset.
const DefaultMaxBytes = 64 << 10

// Runner executes argv commands inside a workspace jail.
type Runner struct {
	workspace string
}

var errInvalidSpec = errors.New("localexec: invalid command spec")
var errInvalidWorkspace = errors.New("localexec: invalid workspace root")

func New(workspace string) (*Runner, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, errInvalidWorkspace
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, errInvalidWorkspace
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, errInvalidWorkspace
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return nil, errInvalidWorkspace
	}
	return &Runner{workspace: real}, nil
}

func (runner *Runner) Run(ctx context.Context, spec tools.CommandSpec) (tools.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.CommandResult{}, err
	}
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return tools.CommandResult{}, errInvalidSpec
	}
	cwd, err := runner.jailCwd(spec.Cwd)
	if err != nil {
		return tools.CommandResult{}, err
	}
	argv0, err := runner.resolveArgv0(spec.Argv[0], cwd)
	if err != nil {
		return tools.CommandResult{}, err
	}
	args := append([]string(nil), spec.Argv[1:]...)
	maxBytes := spec.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	tmp, err := os.MkdirTemp(runner.workspace, "exec-")
	if err != nil {
		return tools.CommandResult{}, err
	}
	defer os.RemoveAll(tmp)

	cmd := exec.Command(argv0, args...)
	cmd.Dir = cwd
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + runner.workspace,
		"TMPDIR=" + tmp,
	}
	cmd.SysProcAttr = sysProcAttr()
	out := newCapBuffer(maxBytes)
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Start(); err != nil {
		return tools.CommandResult{}, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var timeout <-chan time.Time
	if spec.Timeout > 0 {
		timer := time.NewTimer(spec.Timeout)
		defer timer.Stop()
		timeout = timer.C
	}

	kill := func() {
		if cmd.Process != nil {
			_ = killProcessGroup(cmd.Process.Pid)
		}
	}

	select {
	case waitErr := <-done:
		return finish(waitErr, out, false), nil
	case <-ctx.Done():
		kill()
		<-done
		return tools.CommandResult{}, ctx.Err()
	case <-timeout:
		kill()
		waitErr := <-done
		return finish(waitErr, out, true), nil
	case <-out.Overflow():
		kill()
		waitErr := <-done
		result := finish(waitErr, out, false)
		result.Truncated = true
		return result, nil
	}
}

func (runner *Runner) jailCwd(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errInvalidSpec
	}
	resolved, err := evalExisting(cwd)
	if err != nil {
		return "", err
	}
	if !inside(resolved, runner.workspace) {
		return "", tools.ErrOutOfScope
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errInvalidSpec
	}
	return resolved, nil
}

func (runner *Runner) resolveArgv0(argv0, cwd string) (string, error) {
	if !strings.ContainsAny(argv0, `/\`) {
		return argv0, nil
	}
	candidate := argv0
	if !filepath.IsAbs(argv0) {
		candidate = filepath.Join(cwd, argv0)
	}
	resolved, err := evalExisting(candidate)
	if err != nil {
		return "", err
	}
	if !inside(resolved, runner.workspace) {
		return "", tools.ErrOutOfScope
	}
	return resolved, nil
}

func finish(waitErr error, out *capBuffer, timedOut bool) tools.CommandResult {
	return tools.CommandResult{
		ExitCode:  exitCode(waitErr),
		Output:    out.String(),
		Truncated: out.Truncated(),
		TimedOut:  timedOut,
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type capBuffer struct {
	mu       sync.Mutex
	max      int
	buf      []byte
	trunc    bool
	overflow chan struct{}
	once     sync.Once
}

func newCapBuffer(max int) *capBuffer {
	return &capBuffer{max: max, overflow: make(chan struct{})}
}

func (buf *capBuffer) Write(p []byte) (int, error) {
	buf.mu.Lock()
	defer buf.mu.Unlock()
	if len(buf.buf) >= buf.max {
		buf.markOverflow()
		return len(p), nil
	}
	remain := buf.max - len(buf.buf)
	if len(p) > remain {
		buf.buf = append(buf.buf, p[:remain]...)
		buf.markOverflow()
		return len(p), nil
	}
	buf.buf = append(buf.buf, p...)
	return len(p), nil
}

func (buf *capBuffer) markOverflow() {
	buf.trunc = true
	buf.once.Do(func() { close(buf.overflow) })
}

func (buf *capBuffer) Overflow() <-chan struct{} { return buf.overflow }

func (buf *capBuffer) String() string {
	buf.mu.Lock()
	defer buf.mu.Unlock()
	return string(buf.buf)
}

func (buf *capBuffer) Truncated() bool {
	buf.mu.Lock()
	defer buf.mu.Unlock()
	return buf.trunc
}

func evalExisting(path string) (string, error) {
	path = filepath.Clean(path)
	_, err := os.Lstat(path)
	if err == nil {
		real, evalErr := filepath.EvalSymlinks(path)
		if evalErr != nil {
			return "", tools.ErrOutOfScope
		}
		return real, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", tools.ErrOutOfScope
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", tools.ErrOutOfScope
	}
	realParent, err := evalExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(realParent, filepath.Base(path)), nil
}

func inside(abs, root string) bool {
	abs = filepath.Clean(abs)
	root = filepath.Clean(root)
	if abs == root {
		return true
	}
	return strings.HasPrefix(abs, root+string(filepath.Separator))
}

var _ tools.CommandRunner = (*Runner)(nil)
