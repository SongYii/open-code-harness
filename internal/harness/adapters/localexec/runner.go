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

// DefaultMaxBytes is the combined stdout+stderr cap when MaxBytes is unset.
const DefaultMaxBytes = 64 << 10

const (
	// DefaultMemoryHighBytes is the soft memory ceiling (memory.high, Linux
	// only) used when no explicit configuration is given.
	DefaultMemoryHighBytes uint64 = 512 << 20 // 512 MiB
	// DefaultMemoryHeadroomBytes is added to DefaultMemoryHighBytes to form
	// the hard kernel OOM boundary (memory.max = high + headroom); 256 MiB
	// is Grok Build's own documented default for this headroom.
	DefaultMemoryHeadroomBytes uint64 = 256 << 20 // 256 MiB
	// DefaultCPUPeriodMicros and DefaultCPUQuotaMicros configure cpu.max
	// (Linux only) as "100000 100000": one full core's worth of scheduled
	// time per 100ms period, cgroup v2's own stated default period. A
	// single-threaded command never trips this; a command fanning out
	// across multiple cores is throttled to roughly one core's aggregate
	// worth of CPU time (design doc §3).
	DefaultCPUPeriodMicros uint64 = 100000
	DefaultCPUQuotaMicros  uint64 = 100000
)

// Runner executes argv commands inside a workspace jail.
type Runner struct {
	workspace         string
	enforcement       Enforcement
	bwrapAvailable    bool
	bwrapReason       string
	cgroup            *cgroupQuota
	cgroupReason      string
	cpuReason         string
	seatbeltAvailable bool
	seatbeltReason    string
	rlimitMu          sync.Mutex
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
	bwrapAvailable, bwrapReason := probeBwrap()
	enforcement := Enforcement{
		Filesystem: EnforcementNone,
		Network:    EnforcementNone,
		Memory:     EnforcementNone,
		CPU:        EnforcementNone,
	}
	if bwrapAvailable {
		// --unshare-net denies all network access outright (design §3.2);
		// the read-only host with only the workspace rebound read-write
		// gives the same guarantee for filesystem writes.
		enforcement.Filesystem = EnforcementFull
		enforcement.Network = EnforcementFull
	}
	cgroup, cgroupReason, cpuReason := newCgroupQuota(DefaultMemoryHighBytes, DefaultMemoryHighBytes+DefaultMemoryHeadroomBytes, DefaultCPUPeriodMicros, DefaultCPUQuotaMicros)
	if cgroup != nil {
		enforcement.Memory = EnforcementFull
		// cpu delegation fails independently of memory (CPU quota
		// design §3): a cpu.max write failure never undoes the memory
		// quota that already succeeded.
		if cpuReason == "" {
			enforcement.CPU = EnforcementFull
		}
	}
	seatbeltAvailable, seatbeltReason := probeSeatbelt()
	if seatbeltAvailable {
		enforcement.Filesystem = EnforcementFull
		enforcement.Network = EnforcementFull
	}
	if level := rlimitEnforcementLevel(); level != EnforcementNone {
		enforcement.Memory = level
	}
	if level := cpuRlimitEnforcementLevel(); level != EnforcementNone {
		enforcement.CPU = level
	}
	return &Runner{
		workspace:         real,
		enforcement:       enforcement,
		bwrapAvailable:    bwrapAvailable,
		bwrapReason:       bwrapReason,
		cgroup:            cgroup,
		cgroupReason:      cgroupReason,
		cpuReason:         cpuReason,
		seatbeltAvailable: seatbeltAvailable,
		seatbeltReason:    seatbeltReason,
	}, nil
}

// Close releases the memory-quota cgroup and stops its monitor goroutine,
// if one was created. It is a no-op when no quota is active. Callers that
// construct many short-lived Runners (tests included) should call this to
// avoid leaking a cgroup directory and a goroutine per Runner.
func (runner *Runner) Close() error {
	runner.cgroup.close()
	return nil
}

// Enforcement reports, per effect, how completely this Runner confines or
// bounds the commands it runs.
func (runner *Runner) Enforcement() Enforcement {
	return runner.enforcement
}

func (runner *Runner) Run(ctx context.Context, spec tools.CommandSpec) (tools.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.CommandResult{}, err
	}
	maxBytes := spec.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	cmd, tmp, err := runner.buildConfinedCommand(spec)
	if err != nil {
		return tools.CommandResult{}, err
	}
	defer os.RemoveAll(tmp)

	out := newCapBuffer(maxBytes)
	cmd.Stdout = out
	cmd.Stderr = out

	restoreRlimit := beginRlimitBracket(&runner.rlimitMu, DefaultMemoryHighBytes+DefaultMemoryHeadroomBytes)
	startErr := cmd.Start()
	restoreRlimit()
	if startErr != nil {
		return tools.CommandResult{}, startErr
	}
	_ = runner.cgroup.addProcess(cmd.Process.Pid)
	resourceLimited := runner.cgroup.register(cmd.Process.Pid)
	defer runner.cgroup.unregister(cmd.Process.Pid)

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
		result := finish(waitErr, out, false, runner.throttled())
		// Nothing else in this select pre-empted normal completion, so a
		// process terminated by SIGXCPU here was killed by the kernel's
		// own RLIMIT_CPU enforcement (Darwin only; always false
		// elsewhere — CPU quota design §4).
		if isCPUResourceLimitExit(waitErr) {
			result.ResourceLimited = true
		}
		return result, nil
	case <-ctx.Done():
		kill()
		<-done
		return tools.CommandResult{}, ctx.Err()
	case <-timeout:
		kill()
		waitErr := <-done
		return finish(waitErr, out, true, runner.throttled()), nil
	case <-out.Overflow():
		kill()
		waitErr := <-done
		result := finish(waitErr, out, false, runner.throttled())
		result.Truncated = true
		return result, nil
	case <-resourceLimited:
		kill()
		waitErr := <-done
		result := finish(waitErr, out, false, runner.throttled())
		result.ResourceLimited = true
		return result, nil
	}
}

// throttled reads the cgroup's cpu.stat one final time before the caller's
// deferred cleanup tears the cgroup down for this invocation, reporting
// whether the kernel's cpu.max controller measurably throttled this
// command at any point during its run (CPU quota design §3). A read error
// (no quota active, or the cpu controller was never delegated) is treated
// as "not throttled", never as a hard failure.
func (runner *Runner) throttled() bool {
	count, err := runner.cgroup.readThrottledCount()
	return err == nil && count > 0
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

func finish(waitErr error, out *capBuffer, timedOut, throttled bool) tools.CommandResult {
	return tools.CommandResult{
		ExitCode:  exitCode(waitErr),
		Output:    out.String(),
		Truncated: out.Truncated(),
		TimedOut:  timedOut,
		Throttled: throttled,
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
