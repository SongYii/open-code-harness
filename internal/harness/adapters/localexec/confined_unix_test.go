//go:build unix

package localexec

import "syscall"

// sysProcAttrSetsProcessGroup reports whether the prepared command will lead
// its own process group, which is what makes group-wide teardown possible.
func sysProcAttrSetsProcessGroup(attr *syscall.SysProcAttr) bool {
	return attr != nil && attr.Setpgid
}
