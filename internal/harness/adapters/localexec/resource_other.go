//go:build !darwin

package localexec

import (
	"fmt"
	"syscall"
)

func platformResourceSampler(options Options) (groupSampler, resourceBudget, error) {
	if options.CPUTimeLimit != 0 || options.MemoryLimitBytes != 0 {
		return nil, resourceBudget{}, fmt.Errorf("%w: CPU-time/resident-memory overrides require Darwin monitoring", ErrEnforcementUnavailable)
	}
	return nil, resourceBudget{}, nil
}

func signalResourceGroup(pid int, _ uint64, _ map[processIdentity]bool, _ syscall.Signal) error {
	return killProcessGroup(pid)
}

const allowUnmonitoredGroupSignals = true

func platformProcessUsage(int) (processUsage, error) { return processUsage{}, ErrResourceMonitoring }
func platformGroupUsage(int) ([]processUsage, error) { return nil, ErrResourceMonitoring }
