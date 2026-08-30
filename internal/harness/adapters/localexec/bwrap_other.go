//go:build !linux

package localexec

// probeBwrap reports bubblewrap confinement as unavailable on every
// non-Linux platform; there is nothing to probe.
func probeBwrap() (available bool, reason string) {
	return false, "bubblewrap confinement is Linux-only"
}

// bwrapArgv is unreachable off Linux (probeBwrap always reports
// unavailable there), but returns target unchanged for symmetry.
func bwrapArgv(_, _ string, target []string) []string {
	return target
}
