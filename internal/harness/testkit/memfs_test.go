package testkit_test

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestMemFSListDepthAndCap(t *testing.T) {
	mem := testkit.NewMemFS("/workspace")
	mem.AddFile("README.md", []byte("hi"))
	mem.AddFile("src/foo.go", []byte("package src"))
	mem.AddFile("src/nested/bar.go", []byte("package nested"))

	got1, truncated, err := mem.List(context.Background(), "/workspace", 1, tools.MaxListDirEntries)
	if err != nil || truncated {
		t.Fatalf("depth 1 error=%v truncated=%t", err, truncated)
	}
	if !reflect.DeepEqual(got1, []string{"README.md", "src"}) {
		t.Fatalf("depth 1 = %#v", got1)
	}

	got2, truncated, err := mem.List(context.Background(), "/workspace", 2, tools.MaxListDirEntries)
	if err != nil || truncated {
		t.Fatalf("depth 2 error=%v truncated=%t", err, truncated)
	}
	if !reflect.DeepEqual(got2, []string{"README.md", "src", "src/foo.go", "src/nested"}) {
		t.Fatalf("depth 2 = %#v", got2)
	}

	for i := 0; i < 300; i++ {
		mem.AddFile(fmt.Sprintf("f-%03d.txt", i), []byte("x"))
	}
	gotCap, truncated, err := mem.List(context.Background(), "/workspace", 1, tools.MaxListDirEntries)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(gotCap) != tools.MaxListDirEntries {
		t.Fatalf("cap = %d truncated=%t, want %d true", len(gotCap), truncated, tools.MaxListDirEntries)
	}
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
	if err := mem.Write(context.Background(), abs, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, truncated, err := mem.Read(context.Background(), abs, 64)
	if err != nil || truncated || string(data) != "hello" {
		t.Fatalf("Read() = %q truncated=%t err=%v", data, truncated, err)
	}
	if err := mem.Write(context.Background(), "/etc/passwd", []byte("no")); err != tools.ErrOutOfScope && !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("Write outside error = %v", err)
	}
	if _, _, err := mem.Read(context.Background(), "/workspace/missing", 8); err != fs.ErrNotExist {
		t.Fatalf("missing Read error = %v", err)
	}
}
