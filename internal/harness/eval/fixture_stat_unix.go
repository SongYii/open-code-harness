//go:build unix

package eval

import (
	"io/fs"
	"syscall"
)

// hardLinkCount reports the on-disk link count Lstat already observed via
// info (design §8's fixture-copy hard-link rejection). It returns 1 -- the
// ordinary case -- when the platform's FileInfo does not expose it through
// syscall.Stat_t. The Unix implementation never needs to reopen path; it is
// accepted only so both platform implementations share one signature.
func hardLinkCount(_ string, info fs.FileInfo) (uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 1, nil
	}
	return uint64(stat.Nlink), nil
}
