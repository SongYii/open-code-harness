//go:build linux

package workspacefs

import (
	"os"
	"syscall"
)

// identityFields returns the platform-specific facts a version is computed
// over, in a fixed order.
//
// Device and inode are included so that replacing a file with a different one
// of identical size and timestamps still changes the version — publication
// here works by rename, so identity changing under a reader is the ordinary
// case rather than an exotic one. ctime is included because it moves on
// metadata-only changes, such as a chmod, that leave size and mtime alone.
func identityFields(info os.FileInfo) []uint64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fallbackIdentityFields(info)
	}
	return []uint64{
		uint64(stat.Dev),
		stat.Ino,
		uint64(stat.Size),
		uint64(stat.Mtim.Sec), uint64(stat.Mtim.Nsec),
		uint64(stat.Ctim.Sec), uint64(stat.Ctim.Nsec),
		uint64(stat.Mode),
	}
}
