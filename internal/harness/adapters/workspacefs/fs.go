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
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// FileSystem is a host-backed workspace jail. Tests must use t.TempDir().
type FileSystem struct {
	root      string
	mu        sync.Mutex
	locks     map[string]*targetLock
	publisher filePublisher
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
	return &FileSystem{root: real, locks: make(map[string]*targetLock), publisher: osPublisher{}}, nil
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
	file, info, err := files.openRegular(resolved)
	if err != nil {
		return tools.FileRead{}, err
	}
	defer file.Close()
	return readDescriptor(ctx, file, info, limit)
}

// openRegular checks the opened descriptor against the jailed path identity.
func (files *FileSystem) openRegular(abs string) (*os.File, os.FileInfo, error) {
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, nil, err
	}
	if err := regularError(info); err != nil {
		return nil, nil, err
	}
	file, err := os.Open(abs)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err == nil {
		err = regularError(opened)
	}
	if err == nil && !os.SameFile(info, opened) {
		err = staleError()
	}
	resolved, jailErr := files.jail(abs)
	if err == nil && jailErr != nil {
		err = jailErr
	}
	if err == nil && resolved != abs {
		err = tools.ErrOutOfScope
	}
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, opened, nil
}

func regularError(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return &tools.Error{Code: tools.CodeFilesystemNotRegularFile}
	}
	return nil
}

func readDescriptor(ctx context.Context, file *os.File, before os.FileInfo, limit int) (tools.FileRead, error) {
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return tools.FileRead{}, err
	}
	after, err := file.Stat()
	if err != nil {
		return tools.FileRead{}, err
	}
	if err := ctx.Err(); err != nil {
		return tools.FileRead{}, err
	}
	version := versionOf(before)
	if version != versionOf(after) {
		return tools.FileRead{}, staleError()
	}
	truncated := len(data) > limit
	// A bounded read can end inside a valid UTF-8 rune. Validate every complete
	// rune and return only complete runes within the requested byte limit.
	end := 0
	for offset := 0; offset < len(data); {
		if truncated && int64(len(data)) < before.Size() && !utf8.FullRune(data[offset:]) {
			break
		}
		r, n := utf8.DecodeRune(data[offset:])
		if r == utf8.RuneError && n == 1 {
			return tools.FileRead{}, &tools.Error{Code: tools.CodeFilesystemNotText}
		}
		offset += n
		if offset <= limit {
			end = offset
		}
	}
	return tools.FileRead{Data: data[:end], Truncated: truncated, Version: version}, nil
}

func versionOf(info os.FileInfo) tools.FileVersion {
	fields := versionFields(info)
	encoded := make([]byte, len(fields)*8)
	for i, field := range fields {
		binary.BigEndian.PutUint64(encoded[i*8:], field)
	}
	sum := sha256.Sum256(encoded)
	return tools.FileVersion("sha256:" + hex.EncodeToString(sum[:]))
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
