package localexec

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

const defaultCommandTeardownGrace = 5 * time.Second

// commandWait owns exactly one Cmd.Wait. Multiple teardown paths observe the
// same completion instead of reaping twice or waiting without a deadline.
type commandWait struct {
	done chan struct{}
	err  error
}

func waitCommand(cmd *exec.Cmd) *commandWait {
	w := &commandWait{done: make(chan struct{})}
	go func() { w.err = cmd.Wait(); close(w.done) }()
	return w
}
func (r *Runner) teardownGrace() time.Duration {
	if r.stopGrace > 0 {
		return r.stopGrace
	}
	return defaultCommandTeardownGrace
}
func (r *Runner) signalCommandGroup(cmd *exec.Cmd, m *resourceMonitor, sig syscall.Signal) error {
	if m != nil {
		return m.signal(sig)
	}
	if r.groupSignal != nil {
		return r.groupSignal(cmd.Process.Pid, sig)
	}
	return killProcessGroup(cmd.Process.Pid)
}
func (r *Runner) killLeader(cmd *exec.Cmd) error {
	if r.leaderKill != nil {
		return r.leaderKill(cmd.Process)
	}
	// Go's Process handle coordinates Signal with Wait before reaping. Do
	// not manufacture a fresh FindProcess handle from a possibly reused PID.
	return cmd.Process.Kill()
}

func (r *Runner) terminateCommand(cmd *exec.Cmd, m *resourceMonitor, w *commandWait) (result error) {
	defer func() { r.rememberUnsafe(result) }()
	deadline := time.Now().Add(r.teardownGrace())
	signalErr := r.signalCommandGroup(cmd, m, syscall.SIGKILL)
	if processGoneError(signalErr) {
		signalErr = nil
	}
	if signalErr != nil {
		if err := r.killLeader(cmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
			signalErr = errors.Join(signalErr, err)
		}
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-w.done:
		proofErr := waitForCommandGroup(cmd.Process.Pid, m, time.Until(deadline))
		if signalErr != nil {
			signalErr = errors.Join(tools.ErrCommandWaitUnproven, signalErr)
		}
		return errors.Join(signalErr, proofErr)
	case <-timer.C:
		return errors.Join(ErrProcessTeardownUnproven, signalErr)
	}
}

// Serialize admission with the permanent safety latch. Already-running calls
// retain their own lifetime; a failed teardown forbids new process starts.
func (r *Runner) startCommand(cmd *exec.Cmd) error {
	r.admissionMu.Lock()
	defer r.admissionMu.Unlock()
	if r.unsafeErr != nil {
		return r.unsafeErr
	}
	return cmd.Start()
}

func (r *Runner) rememberUnsafe(err error) {
	if !errors.Is(err, ErrProcessTeardownUnproven) {
		return
	}
	r.admissionMu.Lock()
	defer r.admissionMu.Unlock()
	if r.unsafeErr == nil {
		r.unsafeErr = err
	}
}

func unexpectedWaitError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, tools.ErrCommandOutputIncomplete) {
		return err
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return errors.Join(tools.ErrCommandOutputIncomplete, err)
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return nil
	}
	return errors.Join(tools.ErrCommandWaitUnproven, err)
}

func (r *Runner) failedStart(cmd *exec.Cmd, m *resourceMonitor, cause error) (*commandWait, error) {
	w := waitCommand(cmd)
	return w, errors.Join(tools.ErrCommandWaitUnproven, cause, r.terminateCommand(cmd, m, w))
}
