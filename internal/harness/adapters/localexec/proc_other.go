//go:build !unix

package localexec

import (
	"time"

	"os"
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr { return nil }

func killProcessGroup(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

func waitForCommandGroup(int, *resourceMonitor, time.Duration) error { return nil }
