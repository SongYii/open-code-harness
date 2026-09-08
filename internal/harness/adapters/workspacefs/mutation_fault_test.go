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

// This file is in the package rather than beside it because it replaces the
// private publisher with production-shaped failure and interference decorators.
// That keeps the tested commit point identical to production's link/rename path.

func newFaultFS(t *testing.T) (*FileSystem, string) {
	t.Helper()
	root := t.TempDir()
	files, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return files, root
}

var errPublication = errors.New("injected pre-publication failure")

type failingPublisher struct{}

func (failingPublisher) Publish(staged, destination string, create bool) error {
	return errPublication
}

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

	files.publisher = failingPublisher{}
	_, err = files.Write(ctx, target, []byte("replacement"), tools.MutationGuard{
		Kind: tools.GuardReplaceIfVersion, Version: read.Version,
	})
	if !errors.Is(err, errPublication) {
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

	files.publisher = failingPublisher{}
	if _, err := files.Write(ctx, target, []byte("never"), tools.MutationGuard{
		Kind: tools.GuardCreateIfAbsent,
	}); !errors.Is(err, errPublication) {
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

	files.publisher = failingPublisher{}
	if _, err := files.Edit(ctx, target, []byte("beta"), []byte("BETA"), false, tools.MutationGuard{
		Kind: tools.GuardReplaceIfVersion, Version: read.Version,
	}); !errors.Is(err, errPublication) {
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

func TestDefaultPublisherIsProductionPublisher(t *testing.T) {
	files, _ := newFaultFS(t)
	if _, ok := files.publisher.(osPublisher); !ok {
		t.Fatalf("default publisher = %T, want osPublisher", files.publisher)
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

type postPublicationMutator struct {
	inPlace bool
}

func (publisher postPublicationMutator) Publish(staged, destination string, create bool) error {
	if err := (osPublisher{}).Publish(staged, destination, create); err != nil {
		return err
	}
	if publisher.inPlace {
		return os.WriteFile(destination, []byte("external change"), 0o600)
	}
	competing := filepath.Join(filepath.Dir(destination), "external-stage")
	if err := os.WriteFile(competing, []byte("external change"), 0o600); err != nil {
		return err
	}
	return os.Rename(competing, destination)
}

func TestMutationPostPublicationChangeReturnsStale(t *testing.T) {
	for _, test := range []struct {
		name    string
		inPlace bool
	}{
		{name: "external rename"},
		{name: "external in-place mutation", inPlace: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			files, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			abs := filepath.Join(root, "note")
			if err := os.WriteFile(abs, []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			observed, err := files.Read(context.Background(), abs, 64)
			if err != nil {
				t.Fatal(err)
			}
			guard := tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: observed.Version}
			files.publisher = postPublicationMutator{inPlace: test.inPlace}
			result, err := files.Write(context.Background(), abs, []byte("ours"), guard)
			if !tools.IsCode(err, tools.CodeFSStaleVersion) {
				t.Fatalf("Write() = %#v, %v; want zero result and fs_stale_version", result, err)
			}
			if result != (tools.MutationResult{}) {
				t.Fatalf("stale Write result = %#v, want zero value", result)
			}

			files.publisher = osPublisher{}
			if _, err := files.Write(context.Background(), abs, []byte("blind next write"), guard); !tools.IsCode(err, tools.CodeFSStaleVersion) {
				t.Fatalf("next Write() error = %v, want fs_stale_version", err)
			}
			got, err := os.ReadFile(abs)
			if err != nil || string(got) != "external change" {
				t.Fatalf("external revision = %q, %v", got, err)
			}
		})
	}
}

// interferingPublisher publishes for real and then lets another writer land
// before the verifier looks, which is the window a competing process occupies.
type interferingPublisher struct{ after func(destination string) }

func (p interferingPublisher) Publish(staged, destination string, create bool) error {
	if err := (osPublisher{}).Publish(staged, destination, create); err != nil {
		return err
	}
	p.after(destination)
	return nil
}

// TestAFailureAfterPublicationIsNotANonEvent records the one place where a
// reported failure does not mean the destination is unchanged.
//
// Every failure before the rename leaves the original byte-identical, which is
// what the fault tests above prove. Verification runs after the rename, so a
// writer that lands in that window produces an error over a destination that
// has already been replaced. Returning the error is right — the version we
// would otherwise return does not describe bytes we can vouch for, and an
// observation built on it would license a later blind overwrite — but the
// narrower promise has to be written down, or a caller will read "failed" as
// "nothing happened" and be wrong.
func TestAFailureAfterPublicationIsNotANonEvent(t *testing.T) {
	files, root := newFaultFS(t)
	ctx := context.Background()
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	read, err := files.Read(ctx, target, 1024)
	if err != nil {
		t.Fatal(err)
	}

	files.publisher = interferingPublisher{after: func(destination string) {
		if err := os.WriteFile(destination, []byte("someone-else"), 0o644); err != nil {
			t.Errorf("interfering write: %v", err)
		}
	}}
	_, writeErr := files.Write(ctx, target, []byte("ours"), tools.MutationGuard{
		Kind: tools.GuardReplaceIfVersion, Version: read.Version,
	})

	if !tools.IsCode(writeErr, tools.CodeFSStaleVersion) {
		t.Fatalf("Write = %v, want %q", writeErr, tools.CodeFSStaleVersion)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) == "original" {
		t.Fatal("the destination was unchanged; this test no longer describes the code and the contract's narrower promise should be revisited")
	}
	// The point is not which writer won. It is that "failed" here does not
	// mean "nothing happened", and the only honest recovery is to re-read.
	if string(after) != "someone-else" {
		t.Fatalf("destination = %q, want the interfering writer's bytes", after)
	}
}
