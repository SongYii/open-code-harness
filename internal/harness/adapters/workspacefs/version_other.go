//go:build !linux && !darwin

package workspacefs

import "os"

// Compile-only fallback; mutation runtime guarantees cover Linux and Darwin.
func versionFields(info os.FileInfo) []uint64 {
	return []uint64{uint64(info.Size()), uint64(info.Mode()), uint64(info.ModTime().UnixNano())}
}
