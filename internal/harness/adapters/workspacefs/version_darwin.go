//go:build darwin

package workspacefs

import (
	"os"
	"syscall"
)

// identityFields is the darwin layout of the same facts version_linux.go
// documents: device, inode, size, nanosecond mtime and ctime, and mode.
//
// The field names differ (Mtimespec/Ctimespec rather than Mtim/Ctim), which
// is the only reason this file exists separately.
func identityFields(info os.FileInfo) []uint64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fallbackIdentityFields(info)
	}
	return []uint64{
		uint64(stat.Dev),
		stat.Ino,
		uint64(stat.Size),
		uint64(stat.Mtimespec.Sec), uint64(stat.Mtimespec.Nsec),
		uint64(stat.Ctimespec.Sec), uint64(stat.Ctimespec.Nsec),
		uint64(stat.Mode),
	}
}
