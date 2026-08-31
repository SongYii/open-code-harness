//go:build !darwin

package localexec

import "sync"

func probeSeatbelt() (available bool, reason string) {
	return false, "Seatbelt confinement is Darwin-only"
}

func seatbeltCommandArgv(_ string, target []string) (name string, argv []string) {
	return "", target
}

func rlimitEnforcementLevel() EnforcementLevel { return EnforcementNone }

func cpuRlimitEnforcementLevel() EnforcementLevel { return EnforcementNone }

func beginRlimitBracket(_ sync.Locker, _ uint64) func() { return func() {} }

// isCPUResourceLimitExit is always false off Darwin: CPU quota is
// enforced by cgroup v2 cpu.max on Linux (a throttle, never a kill — see
// Runner.throttled), and by nothing at all on any other platform.
func isCPUResourceLimitExit(_ error) bool { return false }
