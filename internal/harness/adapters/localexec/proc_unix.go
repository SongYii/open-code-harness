//go:build unix

package localexec

import (
	"errors"
	"syscall"
	"time"
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}

func waitForCommandGroup(pid int, m *resourceMonitor, grace time.Duration) error {
	deadline := time.Now().Add(max(grace, 0))
	for {
		if m != nil {
			gone, err := m.groupQuiescent()
			if err != nil {
				return errors.Join(ErrProcessTeardownUnproven, err)
			}
			if gone {
				return nil
			}
		} else if errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrProcessTeardownUnproven
		}
		time.Sleep(5 * time.Millisecond)
	}
}
