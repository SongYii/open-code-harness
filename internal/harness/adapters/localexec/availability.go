package localexec

import "runtime"

// Availability reports whether this platform's own OS-level exec
// confinement backend — bwrap on Linux, Seatbelt on macOS — is usable
// right now: the backend's binary is present and a scoped probe
// invocation actually succeeds. It delegates to whichever probe already
// exists for runtime.GOOS, the same probe Runner itself uses at
// construction. A platform with neither backend, including Windows,
// always reports unavailable: this is the accepted trade-off named in
// design doc §5 (docs/superpowers/specs/
// 2026-08-30-exec-sandboxing-resource-quotas-design.md), not a bug to fix
// by adding a Windows mechanism here.
func Availability() (available bool, reason string) {
	switch runtime.GOOS {
	case "linux":
		return probeBwrap()
	case "darwin":
		return probeSeatbelt()
	default:
		return false, "no OS-level exec sandbox backend exists for GOOS=" + runtime.GOOS
	}
}
