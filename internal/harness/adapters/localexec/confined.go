package localexec

import (
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// buildConfinedCommand is the construction Run and NewConfinedCommand share:
// workspace admission, argv0 resolution, the platform's confinement wrapper,
// a private temporary directory, a whitelisted child environment, and a
// process group.
//
// It returns the command unstarted and with no stdio wired, plus the
// temporary directory it created. The caller owns both — Run removes the
// directory when its one call returns, while a ConfinedCommand holds it for
// the life of a long-running process.
func (runner *Runner) buildConfinedCommand(spec tools.CommandSpec) (*exec.Cmd, string, error) {
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return nil, "", errInvalidSpec
	}
	cwd, err := runner.jailCwd(spec.Cwd)
	if err != nil {
		return nil, "", err
	}
	argv0, err := runner.resolveArgv0(spec.Argv[0], cwd)
	if err != nil {
		return nil, "", err
	}
	args := append([]string(nil), spec.Argv[1:]...)

	tmp, err := os.MkdirTemp(runner.workspace, "exec-")
	if err != nil {
		return nil, "", err
	}

	name := argv0
	runArgs := args
	switch {
	case runner.bwrapAvailable:
		name = "bwrap"
		runArgs = bwrapArgv(runner.workspace, cwd, append([]string{argv0}, args...))
	case runner.seatbeltAvailable:
		name, runArgs = seatbeltCommandArgv(runner.workspace, append([]string{argv0}, args...))
	}
	cmd := exec.Command(name, runArgs...)
	cmd.Dir = cwd
	// Never os.Environ(): a child inherits exactly these three names.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + runner.workspace,
		"TMPDIR=" + tmp,
	}
	cmd.SysProcAttr = sysProcAttr()
	return cmd, tmp, nil
}

// ConfinedCommand is a confined command prepared for a caller that owns its
// own process lifetime.
//
// Run exists for one-shot commands: it starts, captures bounded output, waits,
// and releases everything when it returns. A long-lived server subprocess —
// an MCP stdio server is the motivating case — needs the opposite shape. Its
// stdin and stdout are the protocol transport, so the caller must attach the
// pipes and call Start itself, and its confinement, temporary directory, and
// quota membership have to outlive any single call.
//
// The command is returned configured but unstarted, with no stdio wired and
// with the same workspace jail, confinement wrapper, whitelisted environment,
// and process group Run applies.
type ConfinedCommand struct {
	runner  *Runner
	cmd     *exec.Cmd
	tempDir string

	mu         sync.Mutex
	registered int
	closed     bool
}

var errConfinedClosed = errors.New("localexec: confined command is closed")

// NewConfinedCommand prepares a confined, unstarted command.
//
// Admission is identical to Run's: an empty argv, an empty or non-directory
// working directory, a working directory outside the workspace, and an argv0
// that resolves outside the workspace are all refused here rather than at
// start time.
//
// The caller must Close the result to release its temporary directory and
// quota membership. Close does not stop the process — process teardown
// belongs to whoever started it, which on this path is the caller.
func (runner *Runner) NewConfinedCommand(spec tools.CommandSpec) (*ConfinedCommand, error) {
	cmd, tempDir, err := runner.buildConfinedCommand(spec)
	if err != nil {
		return nil, err
	}
	return &ConfinedCommand{runner: runner, cmd: cmd, tempDir: tempDir}, nil
}

// Cmd returns the prepared command. The caller attaches stdio and calls Start.
func (c *ConfinedCommand) Cmd() *exec.Cmd { return c.cmd }

// TempDir returns the private temporary directory this command's TMPDIR
// points at, released by Close.
func (c *ConfinedCommand) TempDir() string { return c.tempDir }

// StartBracket applies the platform's pre-Start resource bracket and returns
// its release function. The caller must invoke the release exactly once,
// after the process has been started.
//
// Run holds this bracket around its own cmd.Start. Here the caller owns Start
// — for an MCP stdio server it is the SDK's CommandTransport.Connect that
// calls it — so the bracket is exposed rather than applied, and a caller that
// skips it gets confinement without the macOS address-space bound. That is a
// disclosed difference between the two entry points, not an oversight: the
// bracket lowers this process's own RLIMIT_AS across the fork, so it cannot
// be held on the caller's behalf across a call this package does not make.
//
// On every platform but macOS the bracket is a no-op, matching Run.
func (c *ConfinedCommand) StartBracket() func() {
	return beginRlimitBracket(&c.runner.rlimitMu, DefaultMemoryHighBytes+DefaultMemoryHeadroomBytes)
}

// Register enrolls a started process in the runner's resource quota, the same
// enrollment Run performs for its own child. It is separate from
// NewConfinedCommand because the pid does not exist until the caller starts
// the process.
//
// A quota that is unavailable on this platform is not an error: enrollment is
// best-effort in Run too, and a missing cgroup controller must not fail a
// command that is otherwise correctly confined.
func (c *ConfinedCommand) Register(pid int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errConfinedClosed
	}
	_ = c.runner.cgroup.addProcess(pid)
	c.registered = pid
	return nil
}

// Close releases the temporary directory and any quota membership. It is
// idempotent, so a supervisor may call it from both an error path and a
// deferred cleanup.
//
// It deliberately does not signal the process. Whoever started it owns
// stopping it, and doing so from here would race a caller that is already
// running its own teardown ladder.
func (c *ConfinedCommand) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.registered != 0 {
		c.runner.cgroup.unregister(c.registered)
		c.registered = 0
	}
	if c.tempDir == "" {
		return nil
	}
	return os.RemoveAll(c.tempDir)
}
