package workspacefs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// filePublisher owns the final atomic namespace operation after staging closes.
type filePublisher interface {
	Publish(staged, destination string, create bool) error
}

type osPublisher struct{}

func (osPublisher) Publish(staged, destination string, create bool) error {
	if create {
		return os.Link(staged, destination)
	}
	return os.Rename(staged, destination)
}

type targetLock struct {
	mu    sync.Mutex
	users int
}

func (files *FileSystem) lockTarget(abs string) func() {
	files.mu.Lock()
	lock := files.locks[abs]
	if lock == nil {
		lock = &targetLock{}
		files.locks[abs] = lock
	}
	lock.users++
	files.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		files.mu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(files.locks, abs)
		}
		files.mu.Unlock()
	}
}

func staleError() error { return &tools.Error{Code: tools.CodeFilesystemStaleVersion} }

func (files *FileSystem) Write(ctx context.Context, abs string, data []byte, guard tools.MutationGuard) (tools.MutationResult, error) {
	return files.mutate(ctx, abs, guard, func(_ *os.File, _ os.FileInfo) ([]byte, error) {
		if !utf8.Valid(data) {
			return nil, &tools.Error{Code: tools.CodeFilesystemNotText}
		}
		return data, nil
	})
}

func (files *FileSystem) Edit(ctx context.Context, abs string, old, replacement []byte, replaceAll bool, guard tools.MutationGuard) (tools.MutationResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.MutationResult{}, err
	}
	if guard.Kind != tools.GuardReplaceIfVersion || len(old) == 0 {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeInvalidArgs}
	}
	return files.mutate(ctx, abs, guard, func(file *os.File, info os.FileInfo) ([]byte, error) {
		if !utf8.Valid(old) || !utf8.Valid(replacement) {
			return nil, &tools.Error{Code: tools.CodeFilesystemNotText}
		}
		if info.Size() > tools.MaxEditFileBytes {
			return nil, &tools.Error{Code: tools.CodeFilesystemTooLarge}
		}
		read, err := readDescriptor(ctx, file, info, tools.MaxEditFileBytes)
		if err != nil {
			return nil, err
		}
		if read.Truncated {
			return nil, &tools.Error{Code: tools.CodeFilesystemTooLarge}
		}
		crlf := bytes.Count(read.Data, []byte("\r\n"))
		lf := bytes.Count(read.Data, []byte("\n")) - crlf
		data := normalizeNewlines(read.Data)
		old = normalizeNewlines(old)
		replacement = normalizeNewlines(replacement)
		count := bytes.Count(data, old)
		if count == 0 {
			return nil, &tools.Error{Code: tools.CodeEditNoMatch}
		}
		if count > 1 && !replaceAll {
			return nil, &tools.Error{Code: tools.CodeEditAmbiguous}
		}
		replacements := 1
		if replaceAll {
			replacements = count
		}
		restoreCRLF := crlf > lf
		if !editResultWithinLimit(data, old, replacement, replacements, restoreCRLF) {
			return nil, &tools.Error{Code: tools.CodeFilesystemTooLarge}
		}
		data = bytes.Replace(data, old, replacement, replacements)
		if restoreCRLF {
			data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
		}
		return data, nil
	})
}

func normalizeNewlines(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

func editResultWithinLimit(data, old, replacement []byte, count int, restoreCRLF bool) bool {
	intermediate, ok := replacedSize(len(data), len(old), len(replacement), count, tools.MaxEditFileBytes)
	if !ok {
		return false
	}
	if !restoreCRLF {
		return true
	}
	newlines, ok := replacedSize(
		bytes.Count(data, []byte("\n")),
		bytes.Count(old, []byte("\n")),
		bytes.Count(replacement, []byte("\n")),
		count,
		tools.MaxEditFileBytes,
	)
	return ok && newlines <= tools.MaxEditFileBytes-intermediate
}

// replacedSize computes base-count*old+count*replacement without overflowing.
// For shrinking replacements, every counted non-overlapping match proves the
// subtraction is bounded by base. For expanding replacements, division checks
// the remaining budget before multiplication.
func replacedSize(base, old, replacement, count, limit int) (int, bool) {
	if base < 0 || old < 0 || replacement < 0 || count < 0 || limit < 0 {
		return 0, false
	}
	if replacement <= old {
		shrink := old - replacement
		if shrink > 0 && count > base/shrink {
			return 0, false
		}
		result := base - count*shrink
		return result, result <= limit
	}
	if base > limit || count > (limit-base)/(replacement-old) {
		return 0, false
	}
	return base + count*(replacement-old), true
}

// mutate serializes this adapter's mutations. Revalidation narrows but cannot
// eliminate the final check-to-rename race with an uncooperative external writer.
func (files *FileSystem) mutate(ctx context.Context, abs string, guard tools.MutationGuard, content func(*os.File, os.FileInfo) ([]byte, error)) (tools.MutationResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.MutationResult{}, err
	}
	if err := guard.Validate(); err != nil {
		return tools.MutationResult{}, err
	}
	resolved, err := files.jail(abs)
	if err != nil {
		return tools.MutationResult{}, err
	}
	unlock := files.lockTarget(resolved)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return tools.MutationResult{}, err
	}
	checked, err := files.jail(abs)
	if err != nil {
		return tools.MutationResult{}, err
	}
	if checked != resolved {
		return tools.MutationResult{}, staleError()
	}
	info, err := files.checkGuard(resolved, guard)
	if err != nil {
		return tools.MutationResult{}, err
	}
	var file *os.File
	mode := os.FileMode(0o600)
	if info != nil {
		file, info, err = files.openRegular(resolved)
		if err != nil {
			return tools.MutationResult{}, err
		}
		defer file.Close()
		if versionOf(info) != guard.Version {
			return tools.MutationResult{}, staleError()
		}
		mode = info.Mode().Perm() | info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky)
	}
	data, err := content(file, info)
	if err != nil {
		return tools.MutationResult{}, err
	}
	return files.publish(ctx, abs, resolved, data, mode, guard)
}

func (files *FileSystem) checkGuard(abs string, guard tools.MutationGuard) (os.FileInfo, error) {
	info, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		if guard.Kind == tools.GuardCreateIfAbsent {
			return nil, nil
		}
		return nil, staleError()
	}
	if err != nil {
		return nil, err
	}
	if err := regularError(info); err != nil {
		return nil, err
	}
	if guard.Kind == tools.GuardCreateIfAbsent {
		return nil, fs.ErrExist
	}
	if versionOf(info) != guard.Version {
		return nil, staleError()
	}
	return info, nil
}

func (files *FileSystem) publish(ctx context.Context, requested, resolved string, data []byte, mode os.FileMode, guard tools.MutationGuard) (tools.MutationResult, error) {
	parent := filepath.Dir(resolved)
	stage, err := os.MkdirTemp(parent, ".workspacefs-*")
	if err != nil {
		return tools.MutationResult{}, err
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, "content")
	file, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return tools.MutationResult{}, err
	}
	var verifier *os.File
	_, err = io.Copy(file, bytes.NewReader(data))
	if err == nil {
		err = file.Sync()
	}
	if err == nil {
		// Open while the staging file is still owner-readable. The intended
		// destination mode may remove read permission, but this descriptor can
		// still verify the published inode without weakening that mode.
		verifier, err = os.Open(staged)
	}
	if err == nil {
		err = file.Chmod(mode)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		if verifier != nil {
			_ = verifier.Close()
		}
		return tools.MutationResult{}, err
	}
	defer verifier.Close()
	stagedInfo, err := verifier.Stat()
	if err != nil {
		return tools.MutationResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return tools.MutationResult{}, err
	}
	checked, err := files.jail(requested)
	if err != nil {
		return tools.MutationResult{}, err
	}
	if checked != resolved {
		return tools.MutationResult{}, staleError()
	}
	if _, err := files.checkGuard(resolved, guard); err != nil {
		return tools.MutationResult{}, err
	}
	create := guard.Kind == tools.GuardCreateIfAbsent
	if err := files.publisher.Publish(staged, resolved, create); err != nil {
		return tools.MutationResult{}, err
	}
	// Removing the create staging link changes ctime, so compute the published
	// version only after unlinking it. Replacement already consumed that path.
	if create {
		_ = os.Remove(staged)
	}
	if dir, err := os.Open(parent); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	version, err := verifyPublished(ctx, verifier, stagedInfo, resolved, data)
	if err != nil {
		return tools.MutationResult{}, err
	}
	operation := tools.MutationUpdate
	if create {
		operation = tools.MutationCreate
	}
	return tools.MutationResult{Version: version, Operation: operation}, nil
}

// verifyPublished binds the returned version to the staged inode and expected
// bytes. It can detect interference before and during this bounded check, but
// cannot make the subsequent return atomic with external filesystem writers.
func verifyPublished(ctx context.Context, verifier *os.File, stagedInfo os.FileInfo, destination string, expected []byte) (tools.FileVersion, error) {
	before, err := verifier.Stat()
	if err != nil || !os.SameFile(stagedInfo, before) || !sameStagedContentMetadata(stagedInfo, before) {
		return "", staleError()
	}
	destinationBefore, err := os.Lstat(destination)
	if err != nil || regularError(destinationBefore) != nil || !os.SameFile(before, destinationBefore) || versionOf(before) != versionOf(destinationBefore) {
		return "", staleError()
	}
	if _, err := verifier.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(verifier, int64(tools.MaxEditFileBytes)+1))
	if err != nil {
		return "", err
	}
	after, err := verifier.Stat()
	if err != nil {
		return "", staleError()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if versionOf(before) != versionOf(after) || !bytes.Equal(data, expected) {
		return "", staleError()
	}
	destinationAfter, err := os.Lstat(destination)
	if err != nil || regularError(destinationAfter) != nil || !os.SameFile(after, destinationAfter) || versionOf(after) != versionOf(destinationAfter) {
		return "", staleError()
	}
	return versionOf(after), nil
}

func sameStagedContentMetadata(staged, published os.FileInfo) bool {
	return staged.Size() == published.Size() &&
		staged.Mode() == published.Mode() &&
		staged.ModTime().Equal(published.ModTime())
}
