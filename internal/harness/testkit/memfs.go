package testkit

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

type memNode struct {
	dir     bool
	data    []byte
	symlink string
}

// MemFS is an in-memory FileSystem. It models symlink children so List can
// skip targets that leave the workspace without touching the host.
type MemFS struct {
	mu        sync.Mutex
	workspace string
	nodes     map[string]*memNode
}

func NewMemFS(workspace string) *MemFS {
	if strings.TrimSpace(workspace) == "" {
		workspace = "/workspace"
	}
	workspace = cleanSlash(workspace)
	return &MemFS{
		workspace: workspace,
		nodes: map[string]*memNode{
			workspace: {dir: true},
		},
	}
}

func (mem *MemFS) AddFile(rel string, data []byte) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	abs := mem.joinLocked(rel)
	mem.ensureDirLocked(path.Dir(abs))
	mem.nodes[abs] = &memNode{data: append([]byte(nil), data...)}
}

func (mem *MemFS) AddDir(rel string) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	mem.ensureDirLocked(mem.joinLocked(rel))
}

func (mem *MemFS) AddSymlink(rel, target string) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	abs := mem.joinLocked(rel)
	mem.ensureDirLocked(path.Dir(abs))
	mem.nodes[abs] = &memNode{symlink: target}
}

func (mem *MemFS) Resolve(_ context.Context, workspace, requested string) (string, error) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	abs, err := mem.resolveLocked(workspace, requested, true)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func (mem *MemFS) Read(_ context.Context, abs string, limit int) ([]byte, bool, error) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	if limit < 0 {
		return nil, false, fs.ErrInvalid
	}
	final, err := mem.followLocked(abs)
	if err != nil {
		return nil, false, err
	}
	if !mem.insideLocked(final) {
		return nil, false, tools.ErrOutOfScope
	}
	node, ok := mem.nodes[final]
	if !ok {
		return nil, false, fs.ErrNotExist
	}
	if node.dir {
		return nil, false, fs.ErrInvalid
	}
	data := append([]byte(nil), node.data...)
	if limit == 0 {
		return []byte{}, len(data) > 0, nil
	}
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

func (mem *MemFS) Write(_ context.Context, abs string, data []byte) error {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	abs = cleanSlash(abs)
	if !mem.insideLocked(abs) {
		return tools.ErrOutOfScope
	}
	parent := path.Dir(abs)
	if parentNode, ok := mem.nodes[parent]; !ok || !parentNode.dir {
		return fs.ErrNotExist
	}
	mem.nodes[abs] = &memNode{data: append([]byte(nil), data...)}
	return nil
}

func (mem *MemFS) List(_ context.Context, abs string, depth, limit int) ([]string, bool, error) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	if depth < tools.DefaultListDirDepth || depth > tools.MaxListDirDepth {
		return nil, false, fs.ErrInvalid
	}
	if limit <= 0 || limit > tools.MaxListDirEntries {
		limit = tools.MaxListDirEntries
	}
	final, err := mem.followLocked(abs)
	if err != nil {
		return nil, false, err
	}
	if !mem.insideLocked(final) {
		return nil, false, tools.ErrOutOfScope
	}
	node, ok := mem.nodes[final]
	if !ok {
		return nil, false, fs.ErrNotExist
	}
	if !node.dir {
		return nil, false, fs.ErrInvalid
	}
	names := mem.collectLocked(final, "", depth)
	sort.Strings(names)
	if len(names) > limit {
		return append([]string(nil), names[:limit]...), true, nil
	}
	return append([]string(nil), names...), false, nil
}

func (mem *MemFS) resolveLocked(workspace, requested string, follow bool) (string, error) {
	scope, err := tools.CheckScopeLexical(tools.ScopeRequest{WorkspaceRoot: workspace, Requested: requested})
	if err != nil || !scope.InWorkspace {
		return "", tools.ErrOutOfScope
	}
	root := cleanSlash(workspace)
	var abs string
	if isAbs(requested) || isAbs(scope.Clean) {
		abs = scope.Clean
	} else if scope.Clean == "." {
		abs = root
	} else {
		abs = path.Join(root, scope.Clean)
	}
	if follow {
		abs, err = mem.followLocked(abs)
		if err != nil {
			return "", err
		}
	}
	if !inside(abs, root) {
		return "", tools.ErrOutOfScope
	}
	return abs, nil
}

func (mem *MemFS) followLocked(abs string) (string, error) {
	abs = cleanSlash(abs)
	seen := make(map[string]struct{})
	for i := 0; i < 64; i++ {
		if _, loop := seen[abs]; loop {
			return "", fs.ErrInvalid
		}
		seen[abs] = struct{}{}
		node, ok := mem.nodes[abs]
		if !ok || node.symlink == "" {
			return abs, nil
		}
		next := node.symlink
		if !isAbs(next) {
			next = path.Join(path.Dir(abs), next)
		}
		next = cleanSlash(next)
		if leftoverDotDot(next) || !mem.insideLocked(next) {
			return "", tools.ErrOutOfScope
		}
		abs = next
	}
	return "", fs.ErrInvalid
}

func (mem *MemFS) collectLocked(dir, prefix string, depth int) []string {
	children := mem.childNamesLocked(dir)
	out := make([]string, 0, len(children))
	for _, name := range children {
		childAbs := path.Join(dir, name)
		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}
		node := mem.nodes[childAbs]
		if node != nil && node.symlink != "" {
			target, err := mem.followLocked(childAbs)
			if err != nil || !mem.insideLocked(target) {
				continue
			}
			out = append(out, rel)
			if depth >= 2 {
				if targetNode, ok := mem.nodes[target]; ok && targetNode.dir {
					out = append(out, mem.collectLocked(target, rel, 1)...)
				}
			}
			continue
		}
		out = append(out, rel)
		if depth >= 2 && node != nil && node.dir {
			out = append(out, mem.collectLocked(childAbs, rel, 1)...)
		}
	}
	return out
}

func (mem *MemFS) childNamesLocked(dir string) []string {
	prefix := dir
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	names := make([]string, 0)
	seen := make(map[string]struct{})
	for abs := range mem.nodes {
		if abs == dir || !strings.HasPrefix(abs, prefix) {
			continue
		}
		rest := strings.TrimPrefix(abs, prefix)
		name, _, _ := strings.Cut(rest, "/")
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		// Only immediate recorded children or implied dirs that have descendants.
		childAbs := path.Join(dir, name)
		if _, ok := mem.nodes[childAbs]; !ok {
			mem.nodes[childAbs] = &memNode{dir: true}
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (mem *MemFS) ensureDirLocked(abs string) {
	abs = cleanSlash(abs)
	for abs != "." && abs != "/" {
		if node, ok := mem.nodes[abs]; ok {
			if !node.dir && node.symlink == "" {
				return
			}
		} else {
			mem.nodes[abs] = &memNode{dir: true}
		}
		parent := path.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	if _, ok := mem.nodes[mem.workspace]; !ok {
		mem.nodes[mem.workspace] = &memNode{dir: true}
	}
}

func (mem *MemFS) joinLocked(rel string) string {
	rel = cleanSlash(rel)
	if isAbs(rel) {
		return rel
	}
	if rel == "." {
		return mem.workspace
	}
	return path.Join(mem.workspace, rel)
}

func (mem *MemFS) insideLocked(abs string) bool {
	return inside(abs, mem.workspace)
}

func leftoverDotDot(cleaned string) bool {
	return cleaned == ".." || strings.HasPrefix(cleaned, "../")
}

func inside(abs, workspace string) bool {
	abs = cleanSlash(abs)
	workspace = cleanSlash(workspace)
	if abs == workspace {
		return true
	}
	prefix := workspace
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(abs, prefix)
}

func isAbs(value string) bool {
	normalized := strings.ReplaceAll(filepath.ToSlash(value), "\\", "/")
	return path.IsAbs(normalized) || filepath.IsAbs(value)
}

func cleanSlash(value string) string {
	return path.Clean(strings.ReplaceAll(filepath.ToSlash(value), "\\", "/"))
}

var _ tools.FileSystem = (*MemFS)(nil)
