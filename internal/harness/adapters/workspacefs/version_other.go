//go:build !linux && !darwin

package workspacefs

import "os"

// identityFields on every other platform uses only what os.FileInfo exposes
// portably: size, mode, and nanosecond modification time.
//
// This exists so the package cross-compiles — Windows in particular — and it
// makes no runtime claim. Without device and inode it cannot notice a file
// being replaced by a different one of identical size, mode, and timestamp,
// and the contract documents that the guarantee covers linux and darwin. A
// platform that wants the real guarantee needs its own file here, not a
// weaker version silently standing in for one.
func identityFields(info os.FileInfo) []uint64 {
	return fallbackIdentityFields(info)
}
