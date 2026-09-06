package workspacefs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// This file is in the package rather than beside it, because it drives the
// private pre-publication hook. The hook exists only to make "a failure before
// publication is a non-event" testable: without it the window between a synced
// staged file and the rename is unreachable from outside, and the claim would
// rest on reading the code rather than on running it.

func newFaultFS(t *testing.T) (*FileSystem, string) {
	t.Helper()
	root := t.TempDir()
	files, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return files, root
}

var errInjected = errors.New("injected pre-publication failure")

// TestMutationFaultLeavesAnExistingDestinationByteIdentical.
func TestMutationFaultLeavesAnExistingDestinationByteIdentical(t *testing.T) {
	files, root := newFaultFS(t)
	ctx := context.Background()
	target := filepath.Join(root, "notes.txt")
	original := []byte("original content\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}

	read, err := files.Read(ctx, target, 1024)
	if err != nil {
		t.Fatal(err)
	}

	files.hooks.beforePublish = func() error { return errInjected }
	_, err = files.Write(ctx, target, []byte("replacement"), tools.MutationGuard{
		Kind: tools.GuardReplaceIfVersion, Version: read.Version,
	})
	if !errors.Is(err, errInjected) {
		t.Fatalf("Write = %v, want the injected failure", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("content = %q, want the original bytes untouched", got)
	}
	assertNoResidue(t, root, "notes.txt")
}

// TestMutationFaultLeavesACreateDestinationAbsent. A half-created file is
// worse than no file: the agent would report a failure while something exists
// at the path it named.
func TestMutationFaultLeavesACreateDestinationAbsent(t *testing.T) {
	files, root := newFaultFS(t)
	ctx := context.Background()
	target := filepath.Join(root, "new.txt")

	files.hooks.beforePublish = func() error { return errInjected }
	if _, err := files.Write(ctx, target, []byte("never"), tools.MutationGuard{
		Kind: tools.GuardCreateIfAbsent,
	}); !errors.Is(err, errInjected) {
		t.Fatalf("Write = %v, want the injected failure", err)
	}

	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat = %v; a failed create left something behind", err)
	}
	assertNoResidue(t, root)
}

// TestMutationFaultDuringAnEditLeavesTheFileIntact.
func TestMutationFaultDuringAnEditLeavesTheFileIntact(t *testing.T) {
	files, root := newFaultFS(t)
	ctx := context.Background()
	target := filepath.Join(root, "code.go")
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	read, err := files.Read(ctx, target, 1024)
	if err != nil {
		t.Fatal(err)
	}

	files.hooks.beforePublish = func() error { return errInjected }
	if _, err := files.Edit(ctx, target, []byte("beta"), []byte("BETA"), false, tools.MutationGuard{
		Kind: tools.GuardReplaceIfVersion, Version: read.Version,
	}); !errors.Is(err, errInjected) {
		t.Fatalf("Edit = %v, want the injected failure", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("content = %q, want the original bytes untouched", got)
	}
	assertNoResidue(t, root, "code.go")
}

// TestTheHookIsNilInProduction. The seam must not be reachable from anything
// but a test in this package.
func TestTheHookIsNilInProduction(t *testing.T) {
	files, _ := newFaultFS(t)
	if files.hooks.beforePublish != nil {
		t.Fatal("a freshly constructed FileSystem carries a publication hook")
	}
}

// assertNoResidue fails if anything but the named files is left in root. A
// staging directory surviving a failure would show up in the agent's own
// list_dir and in the user's version control.
func assertNoResidue(t *testing.T, root string, allowed ...string) {
	t.Helper()
	permitted := map[string]bool{}
	for _, name := range allowed {
		permitted[name] = true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !permitted[entry.Name()] {
			t.Fatalf("residue left behind: %q", entry.Name())
		}
	}
}
