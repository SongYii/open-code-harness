package workspacefs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// stagingDirName is the private sibling directory replacements are built in.
//
// It has to be a sibling of the target rather than a process temp directory,
// because publication is a link or a rename and those only work within one
// filesystem. It is created 0700 and removed after each mutation, so it is
// never a durable thing an agent's own list_dir or a user's version control
// would trip over.
const stagingDirName = ".och-stage"

// fallbackIdentityFields is what every platform can report portably.
//
// It is the whole story on platforms without a syscall.Stat_t layout here,
// and the shared tail of the story on the ones that have one.
func fallbackIdentityFields(info os.FileInfo) []uint64 {
	return []uint64{
		uint64(info.Size()),
		uint64(info.Mode()),
		uint64(info.ModTime().UnixNano()),
	}
}

// versionOf hashes a canonical binary encoding of the identity fields.
//
// Hashing rather than concatenating keeps the token opaque, so no consumer
// can start depending on an inode number or a timestamp it happened to be
// able to parse out. Fixed-width big-endian encoding keeps the digest stable
// for the same facts, which is what makes a version comparable at all.
func versionOf(info os.FileInfo) tools.FileVersion {
	fields := identityFields(info)
	buf := make([]byte, 0, 8*len(fields))
	for _, field := range fields {
		buf = binary.BigEndian.AppendUint64(buf, field)
	}
	sum := sha256.Sum256(buf)
	return tools.FileVersion("sha256:" + hex.EncodeToString(sum[:]))
}

// pathLock serializes mutations of one target within this process.
//
// It is not a substitute for the guard. The guard is what defends against
// writers this process does not control — another tool, the user's editor,
// a second `och`. This lock only stops one process from racing itself
// between the check and the publication, where the guard alone would leave a
// window.
type pathLock struct {
	mu   sync.Mutex
	refs int
}

// lockPath returns a release function for the given resolved path.
//
// Entries are reference-counted and dropped at zero, so a long-lived
// FileSystem does not accumulate one mutex per file ever touched.
func (files *FileSystem) lockPath(path string) func() {
	files.locksOnce.Do(func() { files.locks = make(map[string]*pathLock) })

	files.locksMu.Lock()
	entry, ok := files.locks[path]
	if !ok {
		entry = &pathLock{}
		files.locks[path] = entry
	}
	entry.refs++
	files.locksMu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		files.locksMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(files.locks, path)
		}
		files.locksMu.Unlock()
	}
}

func fsError(code tools.ErrorCode) error { return &tools.Error{Code: code} }

// Write replaces or creates a file, but only under a guard that holds.
func (files *FileSystem) Write(ctx context.Context, abs string, data []byte, guard tools.MutationGuard) (tools.MutationResult, error) {
	target, release, err := files.beginMutation(ctx, abs, guard)
	if err != nil {
		return tools.MutationResult{}, err
	}
	defer release()

	info, err := files.checkGuard(target, guard)
	if err != nil {
		return tools.MutationResult{}, err
	}
	return files.publish(target, data, info)
}

// Edit applies one bounded literal replacement under the same guard.
//
// The guard is checked before the literal is looked for, and the ordering is
// load-bearing: a stale caller whose literal happens to be absent would
// otherwise be told "your text is not there" and sent looking for text, when
// the real answer is that the file changed underneath it and needs re-reading.
func (files *FileSystem) Edit(ctx context.Context, abs string, oldString, newString []byte, replaceAll bool, guard tools.MutationGuard) (tools.MutationResult, error) {
	target, release, err := files.beginMutation(ctx, abs, guard)
	if err != nil {
		return tools.MutationResult{}, err
	}
	defer release()

	info, err := files.checkGuard(target, guard)
	if err != nil {
		return tools.MutationResult{}, err
	}
	if info == nil {
		// A create guard held, meaning nothing is there. There is no text to
		// edit, and inventing a file whose content is the replacement would
		// be a different operation than the one asked for.
		return tools.MutationResult{}, fsError(tools.CodeFSEditNotFound)
	}

	current, err := readBoundedText(target)
	if err != nil {
		return tools.MutationResult{}, err
	}
	edited, err := applyLiteralEdit(current, string(oldString), string(newString), replaceAll)
	if err != nil {
		return tools.MutationResult{}, err
	}
	return files.publish(target, []byte(edited), info)
}

// beginMutation performs the checks every mutation shares, in the order that
// keeps a malformed or out-of-jail request from reaching a real file, and
// returns the target held under its own lock.
func (files *FileSystem) beginMutation(ctx context.Context, abs string, guard tools.MutationGuard) (string, func(), error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if err := guard.Validate(); err != nil {
		return "", nil, err
	}
	target, err := files.jail(abs)
	if err != nil {
		return "", nil, err
	}

	release := files.lockPath(target)
	// Re-jail under the lock. Between the first resolution and here, the path
	// could have become a symlink pointing out of the workspace.
	if _, err := files.jail(target); err != nil {
		release()
		return "", nil, err
	}
	return target, release, nil
}

// checkGuard enforces the promise the caller made. It returns the target's
// current FileInfo, or nil when a create guard held over an absent file.
func (files *FileSystem) checkGuard(target string, guard tools.MutationGuard) (os.FileInfo, error) {
	info, err := os.Lstat(target)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		if guard.Kind == tools.GuardCreateIfAbsent {
			return nil, nil
		}
		// A replace guard over an absent file is stale rather than
		// not-found: what the caller observed is gone.
		return nil, fsError(tools.CodeFSStaleVersion)
	default:
		return nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, fsError(tools.CodeFSNotRegularFile)
	}
	if guard.Kind == tools.GuardCreateIfAbsent {
		// Something is already there, so the promise "nothing is here" is
		// false. This is the same class of answer as a stale version: the
		// state the caller expected is not the state that exists.
		return nil, fsError(tools.CodeFSStaleVersion)
	}
	if versionOf(info) != guard.Version {
		return nil, fsError(tools.CodeFSStaleVersion)
	}
	return info, nil
}

// publish stages the new bytes beside the target and moves them into place.
//
// The destination is never opened for truncation. Every failure before the
// final link or rename leaves the original exactly as it was, which is what
// makes a failed write a non-event rather than a half-written file.
func (files *FileSystem) publish(target string, data []byte, prior os.FileInfo) (tools.MutationResult, error) {
	dir := filepath.Dir(target)
	staging := filepath.Join(dir, stagingDirName)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return tools.MutationResult{}, err
	}
	defer os.Remove(staging)

	mode := fs.FileMode(0o600)
	if prior != nil {
		mode = prior.Mode().Perm()
	}

	staged, err := stageBytes(staging, data, mode)
	if err != nil {
		return tools.MutationResult{}, err
	}
	defer os.Remove(staged)

	// Everything above is reversible; everything below is not. The hook fires
	// exactly here, on that boundary.
	if files.hooks.beforePublish != nil {
		if err := files.hooks.beforePublish(); err != nil {
			return tools.MutationResult{}, err
		}
	}

	if prior == nil {
		// Link fails if the destination exists, so two creators racing past
		// the in-process lock still cannot both win.
		if err := os.Link(staged, target); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return tools.MutationResult{}, fsError(tools.CodeFSStaleVersion)
			}
			return tools.MutationResult{}, err
		}
	} else if err := os.Rename(staged, target); err != nil {
		return tools.MutationResult{}, err
	}

	syncDir(dir)

	info, err := os.Lstat(target)
	if err != nil {
		return tools.MutationResult{}, err
	}
	operation := tools.MutationUpdate
	if prior == nil {
		operation = tools.MutationCreate
	}
	return tools.MutationResult{Version: versionOf(info), Operation: operation}, nil
}

// stageBytes writes a complete, synced, correctly-permissioned replacement
// and returns its path. Syncing before publication is what makes the rename a
// promise about durable bytes rather than about a name.
func stageBytes(staging string, data []byte, mode fs.FileMode) (string, error) {
	file, err := os.CreateTemp(staging, "stage-")
	if err != nil {
		return "", err
	}
	name := file.Name()

	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// syncDir is best effort: the rename already happened, and a directory that
// cannot be opened is not a reason to report a failed write.
func syncDir(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}

// readBoundedText reads a file an edit is allowed to operate on.
//
// The bound is a memory bound: an edit holds the whole file, searches it, and
// writes a replacement. Refusing an oversized file is better than editing part
// of one.
func readBoundedText(target string) (string, error) {
	file, err := os.Open(target)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buf := make([]byte, tools.MaxEditFileBytes+1)
	n, err := io.ReadFull(file, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	if n > tools.MaxEditFileBytes {
		return "", fsError(tools.CodeFSTooLarge)
	}
	if !utf8.Valid(buf[:n]) {
		return "", fsError(tools.CodeFSNotText)
	}
	return string(buf[:n]), nil
}

// applyLiteralEdit is bounded UTF-8 literal replacement with no pattern
// language of any kind.
//
// Matching normalizes line endings because the caller is matching against
// text it was shown, while publication restores the file's own dominant
// ending — an edit that never claimed to touch line endings must not quietly
// rewrite every line of a CRLF file.
func applyLiteralEdit(current, oldString, newString string, replaceAll bool) (string, error) {
	if oldString == "" {
		return "", &tools.Error{Code: tools.CodeInvalidArgs}
	}

	crlf := crlfIsDominant(current)
	normalized := normalizeNewlines(current)
	wanted := normalizeNewlines(oldString)
	replacement := normalizeNewlines(newString)

	count := strings.Count(normalized, wanted)
	switch {
	case count == 0:
		return "", fsError(tools.CodeFSEditNotFound)
	case count > 1 && !replaceAll:
		return "", fsError(tools.CodeFSAmbiguousEdit)
	}

	limit := 1
	if replaceAll {
		limit = -1
	}
	edited := strings.Replace(normalized, wanted, replacement, limit)
	if crlf {
		edited = strings.ReplaceAll(edited, "\n", "\r\n")
	}
	return edited, nil
}

// crlfIsDominant reports whether more of this file's lines end in CRLF than
// in a bare LF.
func crlfIsDominant(text string) bool {
	total := strings.Count(text, "\n")
	windows := strings.Count(text, "\r\n")
	return windows > total-windows
}

func normalizeNewlines(text string) string {
	if !strings.Contains(text, "\r\n") {
		return text
	}
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// trimPartialRune drops an incomplete trailing rune from a clipped read.
//
// A limit cuts at a byte offset, which can land in the middle of a multi-byte
// character. Reporting that as invalid UTF-8 would be blaming the file for the
// clip, so the partial rune is dropped instead.
func trimPartialRune(data []byte) []byte {
	for len(data) > 0 {
		last, size := utf8.DecodeLastRune(data)
		if last != utf8.RuneError || size > 1 {
			return data
		}
		data = data[:len(data)-1]
	}
	return data
}
