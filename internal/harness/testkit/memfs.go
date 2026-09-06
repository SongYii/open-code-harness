package testkit

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

type memNode struct {
	dir     bool
	data    []byte
	symlink string
	version tools.FileVersion
}

// MemFS is an in-memory FileSystem. It models symlink children so List can
// skip targets that leave the workspace without touching the host.
type MemFS struct {
	mu        sync.Mutex
	workspace string
	nodes     map[string]*memNode
	sequence  uint64
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
	mem.nodes[abs] = &memNode{data: append([]byte(nil), data...), version: mem.nextVersionLocked()}
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

func (mem *MemFS) Read(ctx context.Context, abs string, limit int) (tools.FileRead, error) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tools.FileRead{}, err
	}
	if limit < 0 {
		return tools.FileRead{}, fs.ErrInvalid
	}
	final, err := mem.followLocked(abs)
	if err != nil {
		return tools.FileRead{}, err
	}
	if !mem.insideLocked(final) {
		return tools.FileRead{}, tools.ErrOutOfScope
	}
	node, ok := mem.nodes[final]
	if !ok {
		return tools.FileRead{}, fs.ErrNotExist
	}
	if node.dir {
		return tools.FileRead{}, &tools.Error{Code: tools.CodeFilesystemNotRegularFile}
	}
	data := node.data
	truncated := len(data) > limit
	if truncated {
		data = data[:limit+1]
	}
	end := 0
	for offset := 0; offset < len(data); {
		if truncated && len(data) < len(node.data) && !utf8.FullRune(data[offset:]) {
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
	return tools.FileRead{Data: append([]byte(nil), data[:end]...), Truncated: truncated, Version: node.version}, nil
}

func (mem *MemFS) Write(ctx context.Context, abs string, data []byte, guard tools.MutationGuard) (tools.MutationResult, error) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tools.MutationResult{}, err
	}
	final, _, err := mem.mutationTargetLocked(abs, guard)
	if err != nil {
		return tools.MutationResult{}, err
	}
	if !utf8.Valid(data) {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFilesystemNotText}
	}
	return mem.publishLocked(final, data, guard), nil
}

func (mem *MemFS) Edit(ctx context.Context, abs string, old, replacement []byte, replaceAll bool, guard tools.MutationGuard) (tools.MutationResult, error) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tools.MutationResult{}, err
	}
	if guard.Kind != tools.GuardReplaceIfVersion || len(old) == 0 {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeInvalidArgs}
	}
	final, node, err := mem.mutationTargetLocked(abs, guard)
	if err != nil {
		return tools.MutationResult{}, err
	}
	if !utf8.Valid(old) || !utf8.Valid(replacement) {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFilesystemNotText}
	}
	if len(node.data) > tools.MaxEditFileBytes {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFilesystemTooLarge}
	}
	if !utf8.Valid(node.data) {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFilesystemNotText}
	}
	crlf := bytes.Count(node.data, []byte("\r\n"))
	lf := bytes.Count(node.data, []byte("\n")) - crlf
	data := bytes.ReplaceAll(node.data, []byte("\r\n"), []byte("\n"))
	old = bytes.ReplaceAll(old, []byte("\r\n"), []byte("\n"))
	replacement = bytes.ReplaceAll(replacement, []byte("\r\n"), []byte("\n"))
	count := bytes.Count(data, old)
	if count == 0 {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFilesystemEditNotFound}
	}
	if count > 1 && !replaceAll {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFilesystemAmbiguousEdit}
	}
	replacements := 1
	if replaceAll {
		replacements = count
	}
	restoreCRLF := crlf > lf
	if !memEditResultWithinLimit(data, old, replacement, replacements, restoreCRLF) {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFilesystemTooLarge}
	}
	data = bytes.Replace(data, old, replacement, replacements)
	if restoreCRLF {
		data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	}
	return mem.publishLocked(final, data, guard), nil
}

func memEditResultWithinLimit(data, old, replacement []byte, count int, restoreCRLF bool) bool {
	intermediate, ok := memReplacedSize(len(data), len(old), len(replacement), count, tools.MaxEditFileBytes)
	if !ok {
		return false
	}
	if !restoreCRLF {
		return true
	}
	newlines, ok := memReplacedSize(
		bytes.Count(data, []byte("\n")),
		bytes.Count(old, []byte("\n")),
		bytes.Count(replacement, []byte("\n")),
		count,
		tools.MaxEditFileBytes,
	)
	return ok && newlines <= tools.MaxEditFileBytes-intermediate
}

func memReplacedSize(base, old, replacement, count, limit int) (int, bool) {
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

func (mem *MemFS) mutationTargetLocked(abs string, guard tools.MutationGuard) (string, *memNode, error) {
	if err := guard.Validate(); err != nil {
		return "", nil, err
	}
	final, err := mem.followLocked(abs)
	if err != nil {
		return "", nil, err
	}
	if !mem.insideLocked(final) {
		return "", nil, tools.ErrOutOfScope
	}
	node := mem.nodes[final]
	if node != nil && node.dir {
		return "", nil, &tools.Error{Code: tools.CodeFilesystemNotRegularFile}
	}
	if guard.Kind == tools.GuardCreateIfAbsent {
		if node != nil {
			return "", nil, fs.ErrExist
		}
	} else if node == nil || node.version != guard.Version {
		return "", nil, &tools.Error{Code: tools.CodeFilesystemStaleVersion}
	}
	parent := path.Dir(final)
	if parentNode, ok := mem.nodes[parent]; !ok || !parentNode.dir {
		return "", nil, fs.ErrNotExist
	}
	return final, node, nil
}

func (mem *MemFS) publishLocked(abs string, data []byte, guard tools.MutationGuard) tools.MutationResult {
	version := mem.nextVersionLocked()
	mem.nodes[abs] = &memNode{data: append([]byte(nil), data...), version: version}
	operation := tools.MutationUpdate
	if guard.Kind == tools.GuardCreateIfAbsent {
		operation = tools.MutationCreate
	}
	return tools.MutationResult{Version: version, Operation: operation}
}

func (mem *MemFS) nextVersionLocked() tools.FileVersion {
	mem.sequence++
	return tools.FileVersion(fmt.Sprintf("mem:%d", mem.sequence))
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
