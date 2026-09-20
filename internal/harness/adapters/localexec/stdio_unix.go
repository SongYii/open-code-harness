//go:build unix

package localexec

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

const stdioProcessSupported = true
const (
	stdioEOFGrace  = 5 * time.Second
	stdioTermGrace = 3 * time.Second
	stdioKillGrace = 5 * time.Second
)

func stopStdioProcess(pid int, waited <-chan struct{}) error {
	if stdioGroupGone(pid, waited, stdioEOFGrace) {
		return nil
	}
	var signalErr error
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		signalErr = fmt.Errorf("localexec: terminate group: %w", err)
	}
	if stdioGroupGone(pid, waited, stdioTermGrace) {
		return signalErr
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		signalErr = errors.Join(signalErr, fmt.Errorf("localexec: kill group: %w", err))
	}
	if stdioGroupGone(pid, waited, stdioKillGrace) {
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
