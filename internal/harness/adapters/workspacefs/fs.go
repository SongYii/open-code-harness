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

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// FileSystem is a host-backed workspace jail. Tests must use t.TempDir().
type FileSystem struct {
	root string
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

func (files *FileSystem) Read(ctx context.Context, abs string, limit int) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if limit < 0 {
		return nil, false, fs.ErrInvalid
	}
	resolved, err := files.jail(abs)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, false, err
	}
	if info.IsDir() {
		return nil, false, fs.ErrInvalid
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
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

func (files *FileSystem) Write(ctx context.Context, abs string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved, err := files.jail(abs)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(resolved); statErr == nil && info.IsDir() {
		return fs.ErrInvalid
	}
	file, err := os.OpenFile(resolved, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
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
