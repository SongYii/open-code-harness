package porttest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// SeedListTree writes the locked list_dir fixture: README.md, src/foo.go,
// and src/nested/bar.go.
func SeedListTree(put func(rel string, data []byte)) {
	put("README.md", []byte("hi"))
	put("src/foo.go", []byte("package src"))
	put("src/nested/bar.go", []byte("package nested"))
}

// FileSystemListDepthAndCap pins depth 1 vs 2 and the 256-entry cap.
// The caller must SeedListTree first. put is used only to add cap files.
func FileSystemListDepthAndCap(t *testing.T, files tools.FileSystem, workspace string, put func(rel string, data []byte)) {
	t.Helper()
	ctx := context.Background()
	root, err := files.Resolve(ctx, workspace, ".")
	if err != nil {
		t.Fatalf("Resolve(.) error = %v", err)
	}

	got1, truncated, err := files.List(ctx, root, 1, tools.MaxListDirEntries)
	if err != nil || truncated {
		t.Fatalf("depth 1 error=%v truncated=%t", err, truncated)
	}
	if !reflect.DeepEqual(got1, []string{"README.md", "src"}) {
		t.Fatalf("depth 1 = %#v", got1)
	}

	got2, truncated, err := files.List(ctx, root, 2, tools.MaxListDirEntries)
	if err != nil || truncated {
		t.Fatalf("depth 2 error=%v truncated=%t", err, truncated)
	}
	if !reflect.DeepEqual(got2, []string{"README.md", "src", "src/foo.go", "src/nested"}) {
		t.Fatalf("depth 2 = %#v", got2)
	}

	if _, _, err := files.List(ctx, root, 0, tools.MaxListDirEntries); err == nil {
		t.Fatal("depth 0: expected error")
	}
	if _, _, err := files.List(ctx, root, 3, tools.MaxListDirEntries); err == nil {
		t.Fatal("depth 3: expected error")
	}

	for i := 0; i < 300; i++ {
		put(fmt.Sprintf("f-%03d.txt", i), []byte("x"))
	}
	gotCap, truncated, err := files.List(ctx, root, 1, tools.MaxListDirEntries)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(gotCap) != tools.MaxListDirEntries {
		t.Fatalf("cap = %d truncated=%t, want %d true", len(gotCap), truncated, tools.MaxListDirEntries)
	}
	copied := append([]string(nil), gotCap...)
	copied[0] = "mutated"
	if gotCap[0] == "mutated" {
		t.Fatal("List() returned a live backing slice")
	}
}

// FileSystemReadWriteJail pins Read limits and Write prefix refusal.
func FileSystemReadWriteJail(t *testing.T, files tools.FileSystem, workspace string) {
	t.Helper()
	ctx := context.Background()
	abs, err := files.Resolve(ctx, workspace, "note.txt")
	if err != nil {
		t.Fatalf("Resolve(note.txt) = %v", err)
	}
	if err := files.Write(ctx, abs, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, truncated, err := files.Read(ctx, abs, 64)
	if err != nil || truncated || string(data) != "hello" {
		t.Fatalf("Read() = %q truncated=%t err=%v", data, truncated, err)
	}
	data, truncated, err = files.Read(ctx, abs, 2)
	if err != nil || !truncated || string(data) != "he" {
		t.Fatalf("Read(limit=2) = %q truncated=%t err=%v", data, truncated, err)
	}
	data, truncated, err = files.Read(ctx, abs, 0)
	if err != nil || !truncated || len(data) != 0 {
		t.Fatalf("Read(limit=0) = %q truncated=%t err=%v", data, truncated, err)
	}
	if _, _, err := files.Read(ctx, abs, -1); err == nil {
		t.Fatal("Read(limit=-1): expected error")
	}

	raw := []byte{0xff, 0xfe, 'x'}
	if err := files.Write(ctx, abs, raw); err != nil {
		t.Fatal(err)
	}
	data, truncated, err = files.Read(ctx, abs, 64)
	if err != nil || truncated || !reflect.DeepEqual(data, raw) {
		t.Fatalf("Read(invalid UTF-8) = %q truncated=%t err=%v", data, truncated, err)
	}

	if err := files.Write(ctx, "/etc/passwd", []byte("no")); err != tools.ErrOutOfScope && !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("Write outside error = %v", err)
	}
	if _, _, err := files.Read(ctx, "/etc/passwd", 8); err != tools.ErrOutOfScope && !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("Read outside error = %v", err)
	}

	missing, err := files.Resolve(ctx, workspace, "missing.txt")
	if err != nil {
		t.Fatalf("Resolve(missing) = %v", err)
	}
	if _, _, err := files.Read(ctx, missing, 8); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing Read error = %v", err)
	}
}
