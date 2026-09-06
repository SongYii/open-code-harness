package porttest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
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

// FileSystemGuardedMutation exercises guards and edits through the final port.
func FileSystemGuardedMutation(t *testing.T, files tools.FileSystem, workspace string, put func(string, []byte)) {
	t.Helper()
	ctx := context.Background()
	abs, err := files.Resolve(ctx, workspace, "edit.txt")
	if err != nil {
		t.Fatal(err)
	}
	created, err := files.Write(ctx, abs, []byte("a\r\na\r\n"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent})
	if err != nil || created.Operation != tools.MutationCreate || created.Version == "" {
		t.Fatal(created, err)
	}
	if _, err := files.Write(ctx, abs, []byte("lost"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	guard := tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: created.Version}
	if _, err := files.Edit(ctx, abs, []byte("missing"), []byte("x"), false, guard); !tools.IsCode(err, tools.CodeEditNoMatch) {
		t.Fatal(err)
	}
	if _, err := files.Edit(ctx, abs, []byte("a"), []byte("x"), false, guard); !tools.IsCode(err, tools.CodeEditAmbiguous) {
		t.Fatal(err)
	}
	result, err := files.Edit(ctx, abs, []byte("a\n"), []byte("b\n"), true, guard)
	if err != nil || result.Version == guard.Version || result.Operation != tools.MutationUpdate {
		t.Fatal(result, err)
	}
	read, err := files.Read(ctx, abs, 64)
	if err != nil || string(read.Data) != "b\r\nb\r\n" || read.Version != result.Version {
		t.Fatal(read, err)
	}
	if _, err := files.Edit(ctx, abs, []byte("missing"), []byte("x"), false, guard); !tools.IsCode(err, tools.CodeFilesystemStaleVersion) {
		t.Fatal(err)
	}
	guard.Version = read.Version
	put("edit.txt", []byte("b\r\nb\r\n"))
	if _, err := files.Write(ctx, abs, []byte("lost"), guard); !tools.IsCode(err, tools.CodeFilesystemStaleVersion) {
		t.Fatal(err)
	}
	put("edit.txt", []byte{0xff})
	if _, err := files.Read(ctx, abs, 64); !tools.IsCode(err, tools.CodeFilesystemNotText) {
		t.Fatal(err)
	}
	put("edit.txt", []byte("a\xe2"))
	if _, err := files.Read(ctx, abs, 1); !tools.IsCode(err, tools.CodeFilesystemNotText) {
		t.Fatalf("incomplete rune at EOF: %v", err)
	}
	put("edit.txt", []byte("a🙂z"))
	read, err = files.Read(ctx, abs, 2)
	if err != nil || !read.Truncated || string(read.Data) != "a" {
		t.Fatalf("split rune: %#v, %v", read, err)
	}
	put("edit.txt", []byte(strings.Repeat("x", tools.MaxEditFileBytes+1)))
	read, err = files.Read(ctx, abs, 8)
	if err != nil || !read.Truncated {
		t.Fatal(read, err)
	}
	guard.Version = read.Version
	if _, err := files.Edit(ctx, abs, []byte("x"), []byte("y"), true, guard); !tools.IsCode(err, tools.CodeFilesystemTooLarge) {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		before      []byte
		old         []byte
		replacement []byte
	}{
		{
			name:        "replace all expansion",
			before:      bytes.Repeat([]byte("a"), 33),
			old:         []byte("a"),
			replacement: bytes.Repeat([]byte("b"), 32_768),
		},
		{
			name:        "dominant CRLF restoration expansion",
			before:      bytes.Repeat([]byte("a\r\n"), 270_000),
			old:         []byte("a"),
			replacement: []byte("\n"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			put("expansion.txt", test.before)
			expansionAbs, err := files.Resolve(ctx, workspace, "expansion.txt")
			if err != nil {
				t.Fatal(err)
			}
			observed, err := files.Read(ctx, expansionAbs, tools.MaxEditFileBytes)
			if err != nil || observed.Truncated {
				t.Fatal(observed, err)
			}
			_, err = files.Edit(ctx, expansionAbs, test.old, test.replacement, true, tools.MutationGuard{
				Kind: tools.GuardReplaceIfVersion, Version: observed.Version,
			})
			if !tools.IsCode(err, tools.CodeFilesystemTooLarge) {
				t.Fatalf("Edit() error = %v, want fs_too_large", err)
			}
			after, err := files.Read(ctx, expansionAbs, tools.MaxEditFileBytes)
			if err != nil || after.Truncated || !bytes.Equal(after.Data, test.before) || after.Version != observed.Version {
				t.Fatalf("rejected edit changed observation: before=%#v after=%#v err=%v", observed, after, err)
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := files.Read(canceled, abs, 8); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := files.Write(canceled, abs, []byte("lost"), guard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := files.Edit(canceled, abs, []byte("x"), []byte("y"), true, guard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
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
	if _, err := files.Write(ctx, abs, []byte("hello"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); err != nil {
		t.Fatal(err)
	}
	read, err := files.Read(ctx, abs, 64)
	if err != nil || read.Truncated || string(read.Data) != "hello" || read.Version == "" {
		t.Fatalf("Read() = %#v err=%v", read, err)
	}
	version := read.Version
	read, err = files.Read(ctx, abs, 2)
	if err != nil || !read.Truncated || string(read.Data) != "he" || read.Version != version {
		t.Fatalf("Read(limit=2) = %#v err=%v", read, err)
	}
	read, err = files.Read(ctx, abs, 0)
	if err != nil || !read.Truncated || len(read.Data) != 0 {
		t.Fatalf("Read(limit=0) = %#v err=%v", read, err)
	}
	if _, err := files.Read(ctx, abs, -1); err == nil {
		t.Fatal("Read(limit=-1): expected error")
	}

	raw := []byte{0xff, 0xfe, 'x'}
	if _, err := files.Write(ctx, abs, raw, tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: version}); !tools.IsCode(err, tools.CodeFilesystemNotText) {
		t.Fatalf("Write(invalid UTF-8) error=%v", err)
	}

	if _, err := files.Write(ctx, "/etc/passwd", []byte("no"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); err != tools.ErrOutOfScope && !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("Write outside error = %v", err)
	}
	if _, err := files.Read(ctx, "/etc/passwd", 8); err != tools.ErrOutOfScope && !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("Read outside error = %v", err)
	}

	missing, err := files.Resolve(ctx, workspace, "missing.txt")
	if err != nil {
		t.Fatalf("Resolve(missing) = %v", err)
	}
	if _, err := files.Read(ctx, missing, 8); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing Read error = %v", err)
	}
}
