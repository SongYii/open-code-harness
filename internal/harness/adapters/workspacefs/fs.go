package workspacefs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// FileSystem is a host-backed workspace jail. Tests must use t.TempDir().
type FileSystem struct {
	root string

	// locks serializes mutations per target path. See lockPath: this defends
	// the window between a guard check and its publication against this
	// process racing itself; the guard itself is what defends against every
	// other writer.
	locksOnce sync.Once
	locksMu   sync.Mutex
	locks     map[string]*pathLock
}

var errInvalidRoot = errors.New("workspacefs: invalid workspace root")

func New(root string) (*FileSystem, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errInvalidRoot
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, errInvalidRoot
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, errInvalidRoot
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return nil, errInvalidRoot
	}
	return &FileSystem{root: real}, nil
}

func (files *FileSystem) Resolve(ctx context.Context, workspace, requested string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	scope, err := tools.CheckScopeLexical(tools.ScopeRequest{WorkspaceRoot: workspace, Requested: requested})
	if err != nil || !scope.InWorkspace {
		return "", tools.ErrOutOfScope
	}
	root, err := files.canonicalWorkspace(workspace)
	if err != nil {
		return "", err
	}
	candidate, err := lexicalCandidate(root, requested, scope.Clean)
	if err != nil {
		return "", err
	}
	resolved, err := evalExisting(candidate)
	if err != nil {
		return "", err
	}
	if !inside(resolved, root) {
		return "", tools.ErrOutOfScope
	}
	return resolved, nil
}

// Read observes a file: its bytes, whether they were clipped, and the version
// those particular bytes were seen at.
//
// The version is taken from the open descriptor before and after reading, and
// a change between the two is reported as stale rather than returned. Bytes
// paired with a version they were not actually read at would be worse than no
// version at all, because every guard built on them would be a false promise.
func (files *FileSystem) Read(ctx context.Context, abs string, limit int) (tools.FileRead, error) {
	if err := ctx.Err(); err != nil {
		return tools.FileRead{}, err
	}
	if limit < 0 {
		return tools.FileRead{}, fs.ErrInvalid
	}
	resolved, err := files.jail(abs)
	if err != nil {
		return tools.FileRead{}, err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return tools.FileRead{}, err
	}
	if info.IsDir() {
		return tools.FileRead{}, fs.ErrInvalid
	}
	if !info.Mode().IsRegular() {
		return tools.FileRead{}, fsError(tools.CodeFSNotRegularFile)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return tools.FileRead{}, err
	}
	defer file.Close()

	before, err := file.Stat()
	if err != nil {
		return tools.FileRead{}, err
	}

	data, truncated, err := readClipped(file, limit)
	if err != nil {
		return tools.FileRead{}, err
	}

	after, err := file.Stat()
	if err != nil {
		return tools.FileRead{}, err
	}
	version := versionOf(before)
	if version != versionOf(after) {
		return tools.FileRead{}, fsError(tools.CodeFSStaleVersion)
	}

	// A clip can land inside a multi-byte character. Dropping the partial
	// rune keeps that from being reported as the file not being text.
	if truncated {
		data = trimPartialRune(data)
	}
	if !utf8.Valid(data) {
		return tools.FileRead{}, fsError(tools.CodeFSNotText)
	}
	return tools.FileRead{Data: data, Truncated: truncated, Version: version}, nil
}

func readClipped(file *os.File, limit int) ([]byte, bool, error) {
	if limit == 0 {
		var probe [1]byte
		n, readErr := file.Read(probe[:])
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, false, readErr
		}
		return []byte{}, n > 0, nil
	}
	buf := make([]byte, limit+1)
	n, readErr := io.ReadFull(file, buf)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, false, readErr
	}
	if n > limit {
		return buf[:limit], true, nil
	}
	return buf[:n], false, nil
}

func (files *FileSystem) List(ctx context.Context, abs string, depth, limit int) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if depth < tools.DefaultListDirDepth || depth > tools.MaxListDirDepth {
		return nil, false, fs.ErrInvalid
	}
	if limit <= 0 || limit > tools.MaxListDirEntries {
		limit = tools.MaxListDirEntries
	}
	resolved, err := files.jail(abs)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, fs.ErrInvalid
	}
	names := files.collect(resolved, "", depth)
	sort.Strings(names)
	if len(names) > limit {
		return append([]string(nil), names[:limit]...), true, nil
	}
	return append([]string(nil), names...), false, nil
}

func (files *FileSystem) collect(dir, prefix string, depth int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	out := make([]string, 0, len(names))
	for _, name := range names {
		child := filepath.Join(dir, name)
		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}
		target, info, ok := files.listChild(child)
		if !ok {
			continue
		}
		out = append(out, rel)
		if depth >= 2 && info.IsDir() {
			out = append(out, files.collect(target, rel, 1)...)
		}
	}
	return out
}

func (files *FileSystem) listChild(child string) (string, os.FileInfo, bool) {
	info, err := os.Lstat(child)
	if err != nil {
		return "", nil, false
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return child, info, true
	}
	target, err := filepath.EvalSymlinks(child)
	if err != nil || !inside(target, files.root) {
		return "", nil, false
	}
	stat, err := os.Stat(target)
	if err != nil {
		return "", nil, false
	}
	return target, stat, true
}

func (files *FileSystem) jail(abs string) (string, error) {
	if strings.TrimSpace(abs) == "" {
		return "", tools.ErrOutOfScope
	}
	resolved, err := evalExisting(abs)
	if err != nil {
		return "", err
	}
	if !inside(resolved, files.root) {
		return "", tools.ErrOutOfScope
	}
	return resolved, nil
}

func (files *FileSystem) canonicalWorkspace(workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", tools.ErrOutOfScope
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return "", tools.ErrOutOfScope
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", tools.ErrOutOfScope
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", tools.ErrOutOfScope
	}
	if real != files.root {
		return "", tools.ErrOutOfScope
	}
	return files.root, nil
}

func lexicalCandidate(root, requested, cleaned string) (string, error) {
	if filepath.IsAbs(requested) || filepath.IsAbs(cleaned) {
		abs, err := filepath.Abs(requested)
		if err != nil {
			return "", tools.ErrOutOfScope
		}
		return filepath.Clean(abs), nil
	}
	if cleaned == "." {
		return root, nil
	}
	return filepath.Join(root, filepath.FromSlash(cleaned)), nil
}

func evalExisting(path string) (string, error) {
	path = filepath.Clean(path)
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			real, evalErr := filepath.EvalSymlinks(path)
			if evalErr != nil {
				return "", tools.ErrOutOfScope
			}
			return real, nil
		}
		real, evalErr := filepath.EvalSymlinks(path)
		if evalErr != nil {
			return "", tools.ErrOutOfScope
		}
		return real, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", tools.ErrOutOfScope
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", tools.ErrOutOfScope
	}
	realParent, err := evalExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(realParent, filepath.Base(path)), nil
}

func inside(abs, root string) bool {
	abs = filepath.Clean(abs)
	root = filepath.Clean(root)
	if abs == root {
		return true
	}
	return strings.HasPrefix(abs, root+string(filepath.Separator))
}

var _ tools.FileSystem = (*FileSystem)(nil)
