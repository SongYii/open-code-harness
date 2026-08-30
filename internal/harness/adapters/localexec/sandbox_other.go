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

func beginRlimitBracket(_ sync.Locker, _ uint64) func() { return func() {} }
