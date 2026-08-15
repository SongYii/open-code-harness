package workspacefs_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/workspacefs"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
	"github.com/SongYii/open-code-harness/internal/harness/tools/porttest"
)

func TestListDepthAndCap(t *testing.T) {
	files, root := newTestFS(t)
	porttest.SeedListTree(func(rel string, data []byte) {
		writeRel(t, root, rel, data)
	})
	porttest.FileSystemListDepthAndCap(t, files, root, func(rel string, data []byte) {
		writeRel(t, root, rel, data)
	})
}

func TestReadWriteJail(t *testing.T) {
	files, root := newTestFS(t)
	porttest.FileSystemReadWriteJail(t, files, root)
}

func TestResolveSymlinkEscapeDoesNotReadOrWrite(t *testing.T) {
	files, root := newTestFS(t)
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("outside-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatal(err)
	}

	if _, err := files.Resolve(context.Background(), root, "escape"); !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("Resolve(escape) error = %v, want scope_denied", err)
	}
	if _, err := files.Resolve(context.Background(), root, "../nope"); !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("Resolve(..) error = %v, want scope_denied", err)
	}
	got, err := os.ReadFile(outsideFile)
	if err != nil || string(got) != "outside-secret" {
		t.Fatalf("outside file mutated: %q err=%v", got, err)
	}
}

func TestResolveDoesNotCreateOrTruncate(t *testing.T) {
	files, root := newTestFS(t)
	missing := filepath.Join(root, "new.txt")
	if _, err := files.Resolve(context.Background(), root, "new.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("Resolve created %s: %v", missing, err)
	}

	keep := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Resolve(context.Background(), root, "keep.txt"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(keep)
	if err != nil || string(got) != "keep" {
		t.Fatalf("Resolve truncated keep.txt: %q err=%v", got, err)
	}
}

func TestListSkipsOutOfWorkspaceDirSymlinkChildren(t *testing.T) {
	files, root := newTestFS(t)
	writeRel(t, root, "keep.txt", []byte("ok"))
	writeRel(t, root, "real/inside.go", []byte("package real"))
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "nope.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	abs, err := files.Resolve(context.Background(), root, ".")
	if err != nil {
		t.Fatal(err)
	}
	got, truncated, err := files.List(context.Background(), abs, 2, tools.MaxListDirEntries)
	if err != nil || truncated {
		t.Fatalf("List() error=%v truncated=%t", err, truncated)
	}
	want := []string{"alias", "alias/inside.go", "keep.txt", "real", "real/inside.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestResolveRejectsForeignWorkspace(t *testing.T) {
	files, _ := newTestFS(t)
	other := t.TempDir()
	if _, err := files.Resolve(context.Background(), other, "x"); !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("foreign workspace error = %v", err)
	}
}

func TestNewRejectsFileRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := workspacefs.New(path); err == nil {
		t.Fatal("expected error for file root")
	}
	if _, err := workspacefs.New(""); err == nil {
		t.Fatal("expected error for empty root")
	}
}

func TestCanceledContext(t *testing.T) {
	files, root := newTestFS(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := files.Resolve(ctx, root, "x"); err == nil {
		t.Fatal("Resolve: expected canceled")
	}
	if _, _, err := files.Read(ctx, root, 8); err == nil {
		t.Fatal("Read: expected canceled")
	}
	if err := files.Write(ctx, filepath.Join(root, "x"), []byte("x")); err == nil {
		t.Fatal("Write: expected canceled")
	}
	if _, _, err := files.List(ctx, root, 1, 8); err == nil {
		t.Fatal("List: expected canceled")
	}
}

func newTestFS(t *testing.T) (*workspacefs.FileSystem, string) {
	t.Helper()
	root := t.TempDir()
	files, err := workspacefs.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return files, root
}

func writeRel(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
