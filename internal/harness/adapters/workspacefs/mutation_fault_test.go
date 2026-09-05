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

var errPublication = errors.New("publication failed")

type failingPublisher struct{}

func (failingPublisher) Publish(staged, destination string, create bool) error { return errPublication }

func TestMutationPublicationFailurePreservesDestinationAndCleansStage(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "replace"}[existing], func(t *testing.T) {
			root := t.TempDir()
			files, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			files.publisher = failingPublisher{}
			abs := filepath.Join(root, "note")
			guard := tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}
			if existing {
				if err := os.WriteFile(abs, []byte("before"), 0o600); err != nil {
					t.Fatal(err)
				}
				read, err := files.Read(context.Background(), abs, 64)
				if err != nil {
					t.Fatal(err)
				}
				guard = tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: read.Version}
			}
			if _, err := files.Write(context.Background(), abs, []byte("after"), guard); !errors.Is(err, errPublication) {
				t.Fatal(err)
			}
			if existing {
				got, err := os.ReadFile(abs)
				if err != nil || string(got) != "before" {
					t.Fatal(string(got), err)
				}
			} else if _, err := os.Stat(abs); !os.IsNotExist(err) {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if existing {
				want = 1
			}
			if len(entries) != want {
				t.Fatalf("staging residue: %v", entries)
			}
		})
	}
}

// A creator that wins after guard validation must survive the link operation.
type competingPublisher struct{}

func (competingPublisher) Publish(staged, destination string, create bool) error {
	if err := os.WriteFile(destination, []byte("winner"), 0o600); err != nil {
		return err
	}
	return (osPublisher{}).Publish(staged, destination, create)
}

func TestMutationConcurrentCreatorAtPublicationPreserved(t *testing.T) {
	root := t.TempDir()
	files, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	files.publisher = competingPublisher{}
	abs := filepath.Join(root, "note")
	if _, err := files.Write(context.Background(), abs, []byte("loser"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	got, err := os.ReadFile(abs)
	if err != nil || string(got) != "winner" {
		t.Fatal(string(got), err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
}

func TestReadDescriptorRejectsChangedVersion(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "note")
	if err := os.WriteFile(abs, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(abs)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("longer after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDescriptor(context.Background(), file, before, 64); !tools.IsCode(err, tools.CodeFilesystemStaleVersion) {
		t.Fatal(err)
	}
}
