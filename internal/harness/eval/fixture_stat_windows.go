//go:build windows

package eval

import (
	"fmt"
	"io/fs"

	"golang.org/x/sys/windows"
)

// hardLinkCount opens path and reads
// ByHandleFileInformation.NumberOfLinks (implementation plan Task 3):
// unlike Unix's syscall.Stat_t, Windows does not expose a file's on-disk
// link count through os.FileInfo/Lstat, so this platform's implementation
// needs the path, not just the already-collected FileInfo the Unix
// implementation is satisfied with.
func hardLinkCount(path string, _ fs.FileInfo) (uint64, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("eval: hard link count: %w", err)
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("eval: hard link count: open %s: %w", path, err)
	}
	defer windows.CloseHandle(handle) //nolint:errcheck // best-effort close of a read-only handle we already used.

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return 0, fmt.Errorf("eval: hard link count: %s: %w", path, err)
	}
	return uint64(info.NumberOfLinks), nil
}
