//go:build !unix

package localexec

import "syscall"

// sysProcAttrSetsProcessGroup has no process-group concept to assert on a
// platform where sysProcAttr itself is nil; the confined path is only claimed
// on unix, matching Run's own platform support.
func sysProcAttrSetsProcessGroup(attr *syscall.SysProcAttr) bool {
	return attr == nil
}
