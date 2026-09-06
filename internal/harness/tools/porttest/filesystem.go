package porttest

import (
	"bytes"
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
	created, err := files.Write(ctx, abs, []byte("hello"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent})
	if err != nil {
		t.Fatal(err)
	}
	if created.Operation != tools.MutationCreate || created.Version == "" {
		t.Fatalf("create = %+v, want a create carrying a version", created)
	}
	if _, err := files.Write(ctx, abs, []byte("lost"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second create = %v, want fs.ErrExist", err)
	}
	read, err := files.Read(ctx, abs, 64)
	if err != nil || read.Truncated || string(read.Data) != "hello" {
		t.Fatalf("Read() = %+v err=%v", read, err)
	}
	if read.Version == "" {
		t.Fatal("Read returned no version; every observation must be guardable")
	}
	clipped, err := files.Read(ctx, abs, 2)
	if err != nil || !clipped.Truncated || string(clipped.Data) != "he" {
		t.Fatalf("Read(limit=2) = %+v err=%v", clipped, err)
	}
	empty, err := files.Read(ctx, abs, 0)
	if err != nil || !empty.Truncated || len(empty.Data) != 0 {
		t.Fatalf("Read(limit=0) = %+v err=%v", empty, err)
	}
	if _, err := files.Read(ctx, abs, -1); err == nil {
		t.Fatal("Read(limit=-1): expected error")
	}

	// A guarded overwrite succeeds; replaying the same guard afterwards does
	// not, which is the lost-update refusal this port exists for.
	replaced, err := files.Write(ctx, abs, []byte("world"), tools.MutationGuard{
		Kind: tools.GuardReplaceIfVersion, Version: read.Version,
	})
	if err != nil || replaced.Operation != tools.MutationUpdate {
		t.Fatalf("guarded replace = %+v err=%v", replaced, err)
	}
	if _, err := files.Write(ctx, abs, []byte("lost"), tools.MutationGuard{
		Kind: tools.GuardReplaceIfVersion, Version: read.Version,
	}); !tools.IsCode(err, tools.CodeFSStaleVersion) {
		t.Fatalf("replayed guard = %v, want %q", err, tools.CodeFSStaleVersion)
	}

	for i, test := range []struct {
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
			expansionAbs, err := files.Resolve(ctx, workspace, fmt.Sprintf("expansion-%d.txt", i))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := files.Write(ctx, expansionAbs, test.before, tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); err != nil {
				t.Fatal(err)
			}
			observed, err := files.Read(ctx, expansionAbs, tools.MaxEditFileBytes)
			if err != nil || observed.Truncated {
				t.Fatal(observed, err)
			}
			_, err = files.Edit(ctx, expansionAbs, test.old, test.replacement, true, tools.MutationGuard{
				Kind: tools.GuardReplaceIfVersion, Version: observed.Version,
			})
			if !tools.IsCode(err, tools.CodeFSTooLarge) {
				t.Fatalf("Edit() error = %v, want fs_too_large", err)
			}
			after, err := files.Read(ctx, expansionAbs, tools.MaxEditFileBytes)
			if err != nil || after.Truncated || !bytes.Equal(after.Data, test.before) || after.Version != observed.Version {
				t.Fatalf("rejected edit changed observation: before=%#v after=%#v err=%v", observed, after, err)
			}
		})
	}

	// Non-text is refused rather than returned. A literal edit over arbitrary
	// bytes would corrupt the file it claims to be editing, so the refusal
	// belongs at the read that would otherwise license one.
	rawPath, err := files.Resolve(ctx, workspace, "raw.bin")
	if err != nil {
		t.Fatalf("Resolve(raw.bin) = %v", err)
	}
	if _, err := files.Write(ctx, rawPath, []byte{0xff, 0xfe, 'x'}, tools.MutationGuard{
		Kind: tools.GuardCreateIfAbsent,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Read(ctx, rawPath, 64); !tools.IsCode(err, tools.CodeFSNotText) {
		t.Fatalf("Read(invalid UTF-8) = %v, want %q", err, tools.CodeFSNotText)
	}

	if _, err := files.Write(ctx, "/etc/passwd", []byte("no"), tools.MutationGuard{
		Kind: tools.GuardCreateIfAbsent,
	}); err != tools.ErrOutOfScope && !tools.IsCode(err, tools.CodeScopeDenied) {
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
