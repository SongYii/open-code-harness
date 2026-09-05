package workspacefs

import (
	"os"
	"syscall"
)

func versionFields(info os.FileInfo) []uint64 {
	stat := info.Sys().(*syscall.Stat_t)
	return []uint64{uint64(stat.Dev), uint64(stat.Ino), uint64(stat.Size), uint64(stat.Mtim.Sec)*1e9 + uint64(stat.Mtim.Nsec), uint64(stat.Ctim.Sec)*1e9 + uint64(stat.Ctim.Nsec)}
}
