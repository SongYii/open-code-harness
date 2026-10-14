//go:build unix

package localexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const stdioProcessSupported = true
const (
	stdioEOFGrace  = 5 * time.Second
	stdioTermGrace = 3 * time.Second
	stdioKillGrace = 5 * time.Second
)

func stopStdioProcess(cmd *exec.Cmd, waited <-chan struct{}, monitor *resourceMonitor, override time.Duration) error {
	pid := cmd.Process.Pid
	if monitor == nil && !allowUnmonitoredGroupSignals {
		return ErrProcessTeardownUnproven
	}
	eofGrace, termGrace, killGrace := stdioEOFGrace, stdioTermGrace, stdioKillGrace
	if override > 0 {
		eofGrace, termGrace, killGrace = override, override, override
	}
	gone := func(grace time.Duration) bool {
		if monitor == nil {
			return stdioGroupGone(pid, waited, grace)
		}
		deadline := time.Now().Add(grace)
		for {
			select {
			case <-waited:
				if quiet, err := monitor.groupQuiescent(); err == nil && quiet {
					return true
				}
			default:
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	signal := func(sig syscall.Signal) error {
		var err error
		if monitor != nil {
			err = monitor.signal(sig)
		} else {
			err = syscall.Kill(-pid, sig)
		}
		if err != nil && !errors.Is(err, syscall.ESRCH) {
			// The original Go handle is coordinated with Wait. A fallback may
			// stop our own leader, never an unverified/reused group.
			if leaderErr := cmd.Process.Signal(sig); leaderErr != nil && !errors.Is(leaderErr, os.ErrProcessDone) {
				err = errors.Join(err, leaderErr)
			}
		}
		return err
	}
	if gone(eofGrace) {
		return nil
	}
	var signalErr error
	if err := signal(syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		signalErr = fmt.Errorf("localexec: terminate group: %w", err)
	}
	if gone(termGrace) {
		return signalErr
	}
	if err := signal(syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		signalErr = errors.Join(signalErr, fmt.Errorf("localexec: kill group: %w", err))
	}
	if gone(killGrace) {
		return signalErr
	}
	return errors.Join(signalErr, fmt.Errorf("%w: group %d", ErrProcessTeardownUnproven, pid))
}

// A missing group alone is insufficient: the leader must be gone AND our
// single Wait must have finished. Never infer reaping merely from a signal.
func stdioGroupGone(pid int, waited <-chan struct{}, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waited:
			if errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				return true
			}
		default:
		}
		select {
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}
