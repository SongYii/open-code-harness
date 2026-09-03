//go:build unix

package eval

import (
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// acpProcessSupported is true on every build this file compiles for
// (every current unix GOOS): design §16's real ACP subprocess supervision
// is implemented here, not stubbed out.
const acpProcessSupported = true

// acpProcess is one supervised och -acp subprocess (design §16, Unix
// hosts): started in its own new process group so a later escalation
// (Task 13's SIGTERM/SIGKILL ladder) can signal every process this
// launch spawned without ever touching an unrelated one, stdin owned
// exclusively by this supervisor, stdout wired directly to the ACP wire
// protocol (never captured or bounded — a truncated frame would corrupt
// the whole connection), and stderr captured into a bounded buffer for
// evidence (design §19's own stderr bound).
type acpProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *boundedWriter

	waitOnce sync.Once
	waitDone chan struct{} // closed once cmd.Wait() has actually returned
	waitErr  error
}

// startACPProcess starts binaryPath with argv under env and workingDir in
// a new process group (Setpgid with no explicit Pgid: the child becomes
// its own group leader, so its PID and PGID are the same value). The
// caller owns the returned stdin/stdout pipes (the ACP wire) and must
// eventually call stopACPProcess or killACPProcessGroup — leaving a
// started process unreaped is never valid.
func startACPProcess(binaryPath string, argv []string, env []string, workingDir string, stderrLimit int64) (*acpProcess, error) {
	cmd := exec.Command(binaryPath, argv...)
	cmd.Env = env
	cmd.Dir = workingDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stderr := newBoundedWriter(stderrLimit)
	cmd.Stderr = stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("eval: start acp process: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("eval: start acp process: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("eval: start acp process: %w", err)
	}
	return &acpProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, waitDone: make(chan struct{})}, nil
}

// pid is the live child's process ID, equal to its process group ID by
// this package's own startACPProcess convention.
func (p *acpProcess) pid() int { return p.cmd.Process.Pid }

// wait blocks until the process exits, exactly once — sync.Once ensures
// exec.Cmd.Wait is called exactly once even if wait itself is called
// concurrently or repeatedly (this package's own waitTimeout does both:
// each call spawns its own goroutine calling wait, and a caller may call
// waitTimeout more than once, e.g. a short poll followed by a longer
// cleanup wait). Every caller — whichever goroutine actually runs
// cmd.Wait and every other one that arrives while or after it does —
// blocks on waitDone and observes the same recorded result.
func (p *acpProcess) wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		close(p.waitDone)
	})
	<-p.waitDone
	return p.waitErr
}

// waitTimeout blocks for at most timeout for the process to exit and be
// reaped. exited is true only once wait has actually returned; a false
// return leaves the process still running and still unreaped — the
// caller (Task 13's escalation ladder, or this task's own shutdown path)
// decides what happens next, this function never signals anything
// itself. Safe to call more than once, including while an earlier call's
// own spawned goroutine is still blocked in cmd.Wait(): that goroutine
// keeps running in the background and every waitTimeout call (and wait
// itself) still observes the same eventual result.
func (p *acpProcess) waitTimeout(timeout time.Duration) (exited bool, err error) {
	go p.wait() // no-ops via sync.Once if another caller already started it
	select {
	case <-p.waitDone:
		return true, p.waitErr
	case <-time.After(timeout):
		return false, nil
	}
}

// isReaped reports whether wait has already returned for this process,
// without ever blocking — design's own "reject a new launch until reap
// is proven" gate reads this rather than re-deriving process liveness
// some other way, and it must return instantly even while another
// goroutine is currently blocked inside wait() for a process that may
// never exit on its own (a hung child, before Task 13's own kill
// escalation exists to reach it).
func (p *acpProcess) isReaped() bool {
	select {
	case <-p.waitDone:
		return true
	default:
		return false
	}
}

// acpSignalTerm and acpSignalKill are the two signals the escalation
// ladder (acp_actions.go) ever sends, resolved to real platform signal
// numbers here so the ladder itself — not platform-gated — never needs
// to import syscall directly. acp_process_windows.go defines the same two
// names as inert placeholders: killProcessGroup there always refuses
// before either could matter.
const (
	acpSignalTerm      = int(syscall.SIGTERM)
	acpSignalKill      = int(syscall.SIGKILL)
	acpSignalInterrupt = int(syscall.SIGINT)
)

// killProcessGroup sends signal to every process in this launch's own
// process group — startACPProcess's own Setpgid convention means -pid()
// addresses the whole group, never just the leader, and never a
// process this launch did not itself spawn. This is the primary kill
// path design requires in place of exec.CommandContext's own parent-only
// termination, which never reaches a process the child itself spawned.
// It is idempotent in practice: signaling an already-exited group
// returns ESRCH, which callers treat as "nothing left to signal", not a
// failure worth surfacing.
func (p *acpProcess) killProcessGroup(signal int) error {
	return syscall.Kill(-p.pid(), syscall.Signal(signal))
}
