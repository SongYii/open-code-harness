package localexec

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
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

func (r *Runner) terminateCommand(cmd *exec.Cmd, m *resourceMonitor, w *commandWait) error {
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
		return errors.Join(signalErr, proofErr)
	case <-timer.C:
		return errors.Join(ErrProcessTeardownUnproven, signalErr)
	}
}

func (r *Runner) failedStart(cmd *exec.Cmd, m *resourceMonitor, cause error) (*commandWait, error) {
	w := waitCommand(cmd)
	return w, errors.Join(cause, r.terminateCommand(cmd, m, w))
}
