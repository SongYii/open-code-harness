package workspacefs

import (
	"bytes"
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
	return files.publish(ctx, target, data, info)
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
	return files.publish(ctx, target, []byte(edited), info)
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
		// Keep the filesystem-level create conflict intact. Application maps it
		// to the bounded read-before-change recovery result.
		return nil, fs.ErrExist
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
func (files *FileSystem) publish(ctx context.Context, target string, data []byte, prior os.FileInfo) (tools.MutationResult, error) {
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

	staged, verifier, stagedInfo, err := stageBytes(staging, data, mode)
	if err != nil {
		return tools.MutationResult{}, err
	}
	defer os.Remove(staged)
	defer verifier.Close()

	if err := ctx.Err(); err != nil {
		return tools.MutationResult{}, err
	}
	create := prior == nil
	if err := files.publisher.Publish(staged, target, create); err != nil {
		return tools.MutationResult{}, err
	}
	// Removing the create staging link changes ctime, so verify only after the
	// extra link has gone. Replacement publication already consumed its path.
	if create {
		_ = os.Remove(staged)
	}
	syncDir(dir)

	version, err := verifyPublished(ctx, verifier, stagedInfo, target, data)
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
		return "", fsError(tools.CodeFSStaleVersion)
	}
	destinationBefore, err := os.Lstat(destination)
	if err != nil || !destinationBefore.Mode().IsRegular() || !os.SameFile(before, destinationBefore) || versionOf(before) != versionOf(destinationBefore) {
		return "", fsError(tools.CodeFSStaleVersion)
	}
	if _, err := verifier.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	matches, err := publishedContentMatches(verifier, expected)
	if err != nil {
		return "", err
	}
	after, err := verifier.Stat()
	if err != nil {
		return "", fsError(tools.CodeFSStaleVersion)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if versionOf(before) != versionOf(after) || !matches {
		return "", fsError(tools.CodeFSStaleVersion)
	}
	destinationAfter, err := os.Lstat(destination)
	if err != nil || !destinationAfter.Mode().IsRegular() || !os.SameFile(after, destinationAfter) || versionOf(after) != versionOf(destinationAfter) {
		return "", fsError(tools.CodeFSStaleVersion)
	}
	return versionOf(after), nil
}

// publishedContentMatches compares the staged descriptor with the caller's
// existing payload using bounded scratch space. The final one-byte probe is
// load-bearing: matching expected as a prefix is not enough to verify what was
// published.
func publishedContentMatches(verifier *os.File, expected []byte) (bool, error) {
	var chunk [32 * 1024]byte
	for offset := 0; offset < len(expected); {
		remaining := len(expected) - offset
		if remaining > len(chunk) {
			remaining = len(chunk)
		}
		n, err := io.ReadFull(verifier, chunk[:remaining])
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return false, nil
			}
			return false, err
		}
		if !bytes.Equal(chunk[:n], expected[offset:offset+n]) {
			return false, nil
		}
		offset += n
	}

	var extra [1]byte
	n, err := verifier.Read(extra[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return n == 0 && errors.Is(err, io.EOF), nil
}

func sameStagedContentMetadata(staged, published os.FileInfo) bool {
	return staged.Size() == published.Size() &&
		staged.Mode() == published.Mode() &&
		staged.ModTime().Equal(published.ModTime())
}

// stageBytes writes a complete, synced, correctly-permissioned replacement
// and returns its path. Syncing before publication is what makes the rename a
// promise about durable bytes rather than about a name.
func stageBytes(staging string, data []byte, mode fs.FileMode) (string, *os.File, os.FileInfo, error) {
	file, err := os.CreateTemp(staging, "stage-")
	if err != nil {
		return "", nil, nil, err
	}
	name := file.Name()

	var verifier *os.File
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if err == nil {
		// Open while the staged file is still owner-readable. The intended
		// mode may remove read permission, but this descriptor can still bind
		// the published inode, bytes, and returned version.
		verifier, err = os.Open(name)
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
		_ = os.Remove(name)
		return "", nil, nil, err
	}
	stagedInfo, err := verifier.Stat()
	if err != nil {
		_ = verifier.Close()
		_ = os.Remove(name)
		return "", nil, nil, err
	}
	return name, verifier, stagedInfo, nil
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

	restoreCRLF := crlfIsDominant(current)
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

	replacements := 1
	if replaceAll {
		replacements = count
	}
	if !editResultWithinLimit(normalized, wanted, replacement, replacements, restoreCRLF) {
		return "", fsError(tools.CodeFSTooLarge)
	}
	edited := strings.Replace(normalized, wanted, replacement, replacements)
	if restoreCRLF {
		edited = strings.ReplaceAll(edited, "\n", "\r\n")
	}
	return edited, nil
}

func editResultWithinLimit(data, old, replacement string, count int, restoreCRLF bool) bool {
	intermediate, ok := replacedSize(len(data), len(old), len(replacement), count, tools.MaxEditFileBytes)
	if !ok {
		return false
	}
	if !restoreCRLF {
		return true
	}
	newlines, ok := replacedSize(
		strings.Count(data, "\n"),
		strings.Count(old, "\n"),
		strings.Count(replacement, "\n"),
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
