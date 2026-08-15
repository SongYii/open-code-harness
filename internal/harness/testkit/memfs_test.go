package testkit_test

import (
	"context"
	"io/fs"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
	"github.com/SongYii/open-code-harness/internal/harness/tools/porttest"
)

func TestMemFSListDepthAndCap(t *testing.T) {
	mem := testkit.NewMemFS("/workspace")
	porttest.SeedListTree(mem.AddFile)
	porttest.FileSystemListDepthAndCap(t, mem, "/workspace", mem.AddFile)
}

func TestMemFSListSkipsOutOfWorkspaceSymlinkChildren(t *testing.T) {
	mem := testkit.NewMemFS("/workspace")
	mem.AddFile("keep.txt", []byte("ok"))
	mem.AddDir("real")
	mem.AddFile("real/inside.go", []byte("package real"))
	mem.AddSymlink("escape", "/etc/passwd")
	mem.AddSymlink("alias", "/workspace/real")

	got, truncated, err := mem.List(context.Background(), "/workspace", 2, tools.MaxListDirEntries)
	if err != nil || truncated {
		t.Fatalf("List() error=%v truncated=%t", err, truncated)
	}
	if !reflect.DeepEqual(got, []string{"alias", "alias/inside.go", "keep.txt", "real", "real/inside.go"}) {
		t.Fatalf("List() = %#v", got)
	}

	if _, err := mem.Resolve(context.Background(), "/workspace", "escape"); !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("Resolve(escape) error = %v, want scope_denied", err)
	}
	if _, err := mem.Resolve(context.Background(), "/workspace", "../etc/passwd"); !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("Resolve(..) error = %v, want scope_denied", err)
	}
}

func TestMemFSReadWriteResolve(t *testing.T) {
	mem := testkit.NewMemFS("/workspace")
	abs, err := mem.Resolve(context.Background(), "/workspace", "note.txt")
	if err != nil || abs != "/workspace/note.txt" {
		t.Fatalf("Resolve new file = %q, %v", abs, err)
	}
	porttest.FileSystemReadWriteJail(t, mem, "/workspace")
	if _, _, err := mem.Read(context.Background(), "/workspace/missing", 8); err != fs.ErrNotExist {
		t.Fatalf("missing Read error = %v", err)
	}
}
