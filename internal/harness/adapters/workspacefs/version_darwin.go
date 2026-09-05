package workspacefs

import (
	"os"
	"syscall"
)

func versionFields(info os.FileInfo) []uint64 {
	stat := info.Sys().(*syscall.Stat_t)
	return []uint64{uint64(stat.Dev), uint64(stat.Ino), uint64(stat.Size), uint64(stat.Mtimespec.Sec)*1e9 + uint64(stat.Mtimespec.Nsec), uint64(stat.Ctimespec.Sec)*1e9 + uint64(stat.Ctimespec.Nsec)}
}
