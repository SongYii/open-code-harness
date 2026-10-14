package localexec

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"

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
	// A child that exits while a detached descendant retains a pipe must
	// not keep Wait blocked forever. This bounds pipe drain, not CPU/RSS.
	cmd.WaitDelay = 500 * time.Millisecond
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
	monitor    *resourceMonitor
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

// StartBracket is retained for source compatibility. It no longer changes
// parent-process limits. Call Register immediately after Cmd().Start, or use
// StdioProcess.Start, to attach the same external monitor used by Run.
func (c *ConfinedCommand) StartBracket() func() { return func() {} }

// Register enrolls a started process in the runner's resource quota, the same
// enrollment Run performs for its own child. It is separate from
// NewConfinedCommand because the pid does not exist until the caller starts
// the process.
//
// Register must follow raw Cmd().Start immediately, before concurrent Wait.
// Darwin monitor setup errors are returned and the group is terminated.
// Missing Linux cgroup delegation retains the explicit none capability.
// Start starts and enrolls a prepared command as one serialized operation.
// The caller still owns stdio and the single Wait. Prefer this to raw
// Cmd().Start + Register so monitor setup errors cannot be forgotten.
func (c *ConfinedCommand) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errConfinedClosed
	}
	if err := c.cmd.Start(); err != nil {
		return err
	}
	if err := c.registerLocked(c.cmd.Process.Pid); err != nil {
		_ = killProcessGroup(c.cmd.Process.Pid)
		_ = c.cmd.Wait()
		return err
	}
	return nil
}

func (c *ConfinedCommand) Register(pid int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.registerLocked(pid)
}

func (c *ConfinedCommand) registerLocked(pid int) error {
	if c.closed {
		return errConfinedClosed
	}
	if c.cmd.Process == nil || pid <= 0 || pid != c.cmd.Process.Pid {
		return errors.New("localexec: registration must name this command's started process")
	}
	// A caller registering an already waited command must not enroll a
	// new process that has since reused its PID. There is no live resource.
	if c.cmd.ProcessState != nil {
		return nil
	}
	if c.registered != 0 {
		if c.registered == pid {
			return nil
		}
		return errors.New("localexec: command already registered")
	}
	monitor, err := c.runner.startResourceMonitor(pid)
	if err != nil {
		_ = killProcessGroup(pid)
		return err
	}
	c.monitor = monitor
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
	c.monitor.close()
	_, monitorErr := c.monitor.status()
	if c.registered != 0 {
		c.runner.cgroup.unregister(c.registered)
		c.registered = 0
	}
	if c.tempDir == "" {
		return monitorErr
	}
	return errors.Join(monitorErr, os.RemoveAll(c.tempDir))
}
