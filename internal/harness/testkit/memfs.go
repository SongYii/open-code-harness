package testkit

import (
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

	// version changes on every mutation. A counter rather than a digest keeps
	// the double honest about the port's contract -- versions are opaque and
	// only ever compared for equality -- while making "this changed" exact.
	version uint64
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
	// Seeding over an existing file advances its version, so a test can use
	// AddFile to stand in for a writer this harness does not control. A fixed
	// version here would make an external change invisible to the guard, and
	// a test built on that would prove the opposite of what it claimed.
	next := uint64(1)
	if existing, ok := mem.nodes[abs]; ok {
		next = existing.version + 1
	}
	mem.nodes[abs] = &memNode{data: append([]byte(nil), data...), version: next}
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

func (mem *MemFS) Read(_ context.Context, abs string, limit int) (tools.FileRead, error) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
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
		return tools.FileRead{}, fs.ErrInvalid
	}
	version := memVersion(node)
	data := append([]byte(nil), node.data...)
	truncated := false
	switch {
	case limit == 0:
		truncated = len(data) > 0
		data = []byte{}
	case len(data) > limit:
		data = data[:limit]
		truncated = true
	}
	if truncated {
		data = trimPartialRune(data)
	}
	if !utf8.Valid(data) {
		return tools.FileRead{}, &tools.Error{Code: tools.CodeFSNotText}
	}
	return tools.FileRead{Data: data, Truncated: truncated, Version: version}, nil
}

// Write mirrors the real adapter's guard semantics, because a double that
// permits what the adapter refuses would let a caller's tests pass against a
// contract the caller does not actually have.
func (mem *MemFS) Write(_ context.Context, abs string, data []byte, guard tools.MutationGuard) (tools.MutationResult, error) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	node, abs, err := mem.prepareMutationLocked(abs, guard)
	if err != nil {
		return tools.MutationResult{}, err
	}
	return mem.publishLocked(abs, node, append([]byte(nil), data...)), nil
}

// Edit is the same bounded literal replacement the real adapter performs, with
// the same guard-before-match ordering.
func (mem *MemFS) Edit(_ context.Context, abs string, oldString, newString []byte, replaceAll bool, guard tools.MutationGuard) (tools.MutationResult, error) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	node, abs, err := mem.prepareMutationLocked(abs, guard)
	if err != nil {
		return tools.MutationResult{}, err
	}
	if node == nil {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFSEditNotFound}
	}
	if len(node.data) > tools.MaxEditFileBytes {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFSTooLarge}
	}
	if !utf8.Valid(node.data) {
		return tools.MutationResult{}, &tools.Error{Code: tools.CodeFSNotText}
	}
	edited, err := memEdit(string(node.data), string(oldString), string(newString), replaceAll)
	if err != nil {
		return tools.MutationResult{}, err
	}
	return mem.publishLocked(abs, node, []byte(edited)), nil
}

// prepareMutationLocked validates the guard against current state and returns
// the existing node, or nil when a create guard held over an absent target.
func (mem *MemFS) prepareMutationLocked(abs string, guard tools.MutationGuard) (*memNode, string, error) {
	if err := guard.Validate(); err != nil {
		return nil, "", err
	}
	abs = cleanSlash(abs)
	if !mem.insideLocked(abs) {
		return nil, "", tools.ErrOutOfScope
	}
	parent := path.Dir(abs)
	if parentNode, ok := mem.nodes[parent]; !ok || !parentNode.dir {
		return nil, "", fs.ErrNotExist
	}
	node, exists := mem.nodes[abs]
	switch {
	case !exists:
		if guard.Kind == tools.GuardCreateIfAbsent {
			return nil, abs, nil
		}
		return nil, "", &tools.Error{Code: tools.CodeFSStaleVersion}
	case node.dir || node.symlink != "":
		return nil, "", &tools.Error{Code: tools.CodeFSNotRegularFile}
	case guard.Kind == tools.GuardCreateIfAbsent:
		return nil, "", &tools.Error{Code: tools.CodeFSStaleVersion}
	case memVersion(node) != guard.Version:
		return nil, "", &tools.Error{Code: tools.CodeFSStaleVersion}
	}
	return node, abs, nil
}

func (mem *MemFS) publishLocked(abs string, prior *memNode, data []byte) tools.MutationResult {
	operation := tools.MutationUpdate
	next := uint64(1)
	if prior == nil {
		operation = tools.MutationCreate
	} else {
		next = prior.version + 1
	}
	node := &memNode{data: data, version: next}
	mem.nodes[abs] = node
	return tools.MutationResult{Version: memVersion(node), Operation: operation}
}

func memVersion(node *memNode) tools.FileVersion {
	return tools.FileVersion(fmt.Sprintf("mem:%d:%d", node.version, len(node.data)))
}

func memEdit(current, oldString, newString string, replaceAll bool) (string, error) {
	if oldString == "" {
		return "", &tools.Error{Code: tools.CodeInvalidArgs}
	}
	restoreCRLF := memCRLFIsDominant(current)
	normalized := strings.ReplaceAll(current, "\r\n", "\n")
	wanted := strings.ReplaceAll(oldString, "\r\n", "\n")
	replacement := strings.ReplaceAll(newString, "\r\n", "\n")
	count := strings.Count(normalized, wanted)
	switch {
	case count == 0:
		return "", &tools.Error{Code: tools.CodeFSEditNotFound}
	case count > 1 && !replaceAll:
		return "", &tools.Error{Code: tools.CodeFSAmbiguousEdit}
	}
	replacements := 1
	if replaceAll {
		replacements = count
	}
	if !memEditResultWithinLimit(normalized, wanted, replacement, replacements, restoreCRLF) {
		return "", &tools.Error{Code: tools.CodeFSTooLarge}
	}
	edited := strings.Replace(normalized, wanted, replacement, replacements)
	if restoreCRLF {
		edited = strings.ReplaceAll(edited, "\n", "\r\n")
	}
	return edited, nil
}

func memCRLFIsDominant(text string) bool {
	total := strings.Count(text, "\n")
	windows := strings.Count(text, "\r\n")
	return windows > total-windows
}

func memEditResultWithinLimit(data, old, replacement string, count int, restoreCRLF bool) bool {
	intermediate, ok := memReplacedSize(len(data), len(old), len(replacement), count, tools.MaxEditFileBytes)
	if !ok {
		return false
	}
	if !restoreCRLF {
		return true
	}
	newlines, ok := memReplacedSize(
		strings.Count(data, "\n"),
		strings.Count(old, "\n"),
		strings.Count(replacement, "\n"),
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

// trimPartialRune drops an incomplete trailing rune left by a clip, so a cut
// in the middle of a character is not reported as the file not being text.
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
