package workspacefs_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/workspacefs"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func replaceGuard(version tools.FileVersion) tools.MutationGuard {
	return tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: version}
}

var createGuard = tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}

// TestReadThenGuardedWriteThenStaleWrite is the whole mechanism in one test.
//
// A write guarded by the version a read observed succeeds and returns a new
// version. Replaying the same guard afterwards is refused, because the file no
// longer looks like what was read — which is exactly the lost-update this
// exists to prevent, whether the intervening writer was a human, another
// process, or the agent's own earlier step.
func TestReadThenGuardedWriteThenStaleWrite(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "notes.txt")
	writeRel(t, root, "notes.txt", []byte("before"))

	read, err := files.Read(ctx, abs, 64)
	if err != nil || read.Version == "" || string(read.Data) != "before" {
		t.Fatalf("Read = %+v, %v", read, err)
	}

	updated, err := files.Write(ctx, abs, []byte("after"), replaceGuard(read.Version))
	if err != nil {
		t.Fatalf("guarded Write: %v", err)
	}
	if updated.Operation != tools.MutationUpdate {
		t.Fatalf("Operation = %q, want %q", updated.Operation, tools.MutationUpdate)
	}
	if updated.Version == read.Version {
		t.Fatal("the version did not change after a successful write")
	}
	if got := readRel(t, root, "notes.txt"); got != "after" {
		t.Fatalf("content = %q, want %q", got, "after")
	}

	_, err = files.Write(ctx, abs, []byte("lost"), replaceGuard(read.Version))
	if !tools.IsCode(err, tools.CodeFSStaleVersion) {
		t.Fatalf("stale error = %v, want %q", err, tools.CodeFSStaleVersion)
	}
	if got := readRel(t, root, "notes.txt"); got != "after" {
		t.Fatalf("a refused write changed the file: %q", got)
	}
}

// TestGuardedCreate: create-if-absent creates, and refuses once something is
// there.
func TestGuardedCreate(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "new.txt")

	created, err := files.Write(ctx, abs, []byte("first"), createGuard)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Operation != tools.MutationCreate {
		t.Fatalf("Operation = %q, want %q", created.Operation, tools.MutationCreate)
	}
	if created.Version == "" {
		t.Fatal("a successful create returned no version")
	}

	if _, err := files.Write(ctx, abs, []byte("second"), createGuard); !tools.IsCode(err, tools.CodeFSStaleVersion) {
		t.Fatalf("second create = %v, want %q", err, tools.CodeFSStaleVersion)
	}
	if got := readRel(t, root, "new.txt"); got != "first" {
		t.Fatalf("a refused create changed the file: %q", got)
	}
}

// TestConcurrentCreatorsThroughOneFileSystemAreSerialized proves the
// per-target lock, and nothing more.
//
// It is named for what it actually exercises. An earlier version of this test
// claimed to prove that publication-by-link is what keeps two creators from
// both winning, and a mutation replacing the link with a rename did not turn
// it red — because the lock had already serialized the writers before the
// publication step could matter. The atomicity claim needs two independent
// FileSystem instances, which is the test below.
func TestConcurrentCreatorsThroughOneFileSystemAreSerialized(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "raced.txt")

	const writers = 8
	var wg sync.WaitGroup
	results := make([]error, writers)
	start := make(chan struct{})
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, results[i] = files.Write(ctx, abs, []byte(string(rune('a'+i))), createGuard)
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range results {
		switch {
		case err == nil:
			winners++
		case tools.IsCode(err, tools.CodeFSStaleVersion):
		default:
			t.Fatalf("writer %d failed with an unexpected error: %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}
	if got := readRel(t, root, "raced.txt"); len(got) != 1 {
		t.Fatalf("content = %q; a loser overwrote the winner", got)
	}
}

// TestConcurrentCreatorsAcrossFileSystemsLeaveExactlyOneWinner is the real
// atomicity proof.
//
// Two FileSystem values over the same root have separate lock registries, so
// nothing serializes them — which is also the true situation the guard exists
// for, where the other writer is a different process entirely. Both can
// observe the target as absent and both can stage a replacement; only the
// publication step decides. Linking into place fails for the loser, whereas a
// rename would let it silently overwrite the winner.
func TestConcurrentCreatorsAcrossFileSystemsLeaveExactlyOneWinner(t *testing.T) {
	_, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "raced-across.txt")

	const writers = 8
	instances := make([]*workspacefs.FileSystem, writers)
	for i := range instances {
		instance, err := workspacefs.New(root)
		if err != nil {
			t.Fatal(err)
		}
		instances[i] = instance
	}

	var wg sync.WaitGroup
	results := make([]error, writers)
	start := make(chan struct{})
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, results[i] = instances[i].Write(ctx, abs, []byte(string(rune('a'+i))), createGuard)
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range results {
		switch {
		case err == nil:
			winners++
		case tools.IsCode(err, tools.CodeFSStaleVersion):
		default:
			t.Fatalf("writer %d failed with an unexpected error: %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1; publication is not atomic", winners)
	}
	if got := readRel(t, root, "raced-across.txt"); len(got) != 1 {
		t.Fatalf("content = %q; a loser overwrote the winner", got)
	}
}

// TestWriteRefusesADirectory. A directory target is refused before anything is
// staged, rather than producing a partially-built replacement beside it.
func TestWriteRefusesADirectory(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(root, "sub")

	_, err := files.Write(ctx, abs, []byte("x"), replaceGuard("sha256:whatever"))
	if !tools.IsCode(err, tools.CodeFSNotRegularFile) && !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Write to a directory = %v, want a not-a-regular-file refusal", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("a refused write left %d entries behind, want 1", len(entries))
	}
}

// TestReadRefusesInvalidUTF8. A literal edit over arbitrary bytes would
// corrupt the file it claims to be editing, so non-text is refused at the
// boundary rather than discovered halfway through.
func TestReadRefusesInvalidUTF8(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	writeRel(t, root, "binary.bin", []byte{0xff, 0xfe, 0x00, 0x01})

	if _, err := files.Read(ctx, filepath.Join(root, "binary.bin"), 64); !tools.IsCode(err, tools.CodeFSNotText) {
		t.Fatalf("Read of invalid UTF-8 = %v, want %q", err, tools.CodeFSNotText)
	}
}

// TestCancellationIsCheckedBeforeAnyMutation.
func TestCancellationIsCheckedBeforeAnyMutation(t *testing.T) {
	files, root := newTestFS(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	abs := filepath.Join(root, "never.txt")

	if _, err := files.Write(ctx, abs, []byte("x"), createGuard); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write with a cancelled context = %v, want context.Canceled", err)
	}
	if _, err := os.Lstat(abs); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a cancelled write created the file anyway")
	}
	if _, err := files.Edit(ctx, abs, []byte("a"), []byte("b"), false, createGuard); !errors.Is(err, context.Canceled) {
		t.Fatalf("Edit with a cancelled context = %v, want context.Canceled", err)
	}
}

// TestMutationRefusesToEscapeTheJail. The jail is unchanged by this work, and
// the new entry points must not become a way around it.
func TestMutationRefusesToEscapeTheJail(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{outside, link} {
		if _, err := files.Write(ctx, target, []byte("no"), replaceGuard("sha256:x")); !errors.Is(err, tools.ErrOutOfScope) {
			t.Fatalf("Write(%q) = %v, want out-of-scope", target, err)
		}
		if _, err := files.Edit(ctx, target, []byte("outside"), []byte("no"), false, replaceGuard("sha256:x")); !errors.Is(err, tools.ErrOutOfScope) {
			t.Fatalf("Edit(%q) = %v, want out-of-scope", target, err)
		}
	}
	if data, _ := os.ReadFile(outside); string(data) != "outside" {
		t.Fatalf("the out-of-jail file was modified: %q", data)
	}
}

// TestEditUniqueMatch is the ordinary case.
func TestEditUniqueMatch(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "code.go")
	writeRel(t, root, "code.go", []byte("alpha\nbeta\ngamma\n"))

	read, err := files.Read(ctx, abs, 1024)
	if err != nil {
		t.Fatal(err)
	}
	result, err := files.Edit(ctx, abs, []byte("beta"), []byte("BETA"), false, replaceGuard(read.Version))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if result.Operation != tools.MutationUpdate {
		t.Fatalf("Operation = %q, want %q", result.Operation, tools.MutationUpdate)
	}
	if got := readRel(t, root, "code.go"); got != "alpha\nBETA\ngamma\n" {
		t.Fatalf("content = %q", got)
	}
}

// TestEditMissingAndAmbiguousAreDistinctRefusals.
//
// "the text is not there" and "the text is there several times" call for
// different next steps — reconsider the edit, versus name which one — so they
// are different codes rather than one generic failure.
func TestEditMissingAndAmbiguousAreDistinctRefusals(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "code.go")
	writeRel(t, root, "code.go", []byte("dup\ndup\nother\n"))

	read, err := files.Read(ctx, abs, 1024)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := files.Edit(ctx, abs, []byte("absent"), []byte("x"), false, replaceGuard(read.Version)); !tools.IsCode(err, tools.CodeFSEditNotFound) {
		t.Fatalf("missing literal = %v, want %q", err, tools.CodeFSEditNotFound)
	}
	if _, err := files.Edit(ctx, abs, []byte("dup"), []byte("x"), false, replaceGuard(read.Version)); !tools.IsCode(err, tools.CodeFSAmbiguousEdit) {
		t.Fatalf("ambiguous literal = %v, want %q", err, tools.CodeFSAmbiguousEdit)
	}
	if got := readRel(t, root, "code.go"); got != "dup\ndup\nother\n" {
		t.Fatalf("a refused edit changed the file: %q", got)
	}
}

// TestEditReplaceAllIsTheOnlyMatchingOption.
func TestEditReplaceAllIsTheOnlyMatchingOption(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "code.go")
	writeRel(t, root, "code.go", []byte("dup\ndup\nother\n"))

	read, err := files.Read(ctx, abs, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.Edit(ctx, abs, []byte("dup"), []byte("x"), true, replaceGuard(read.Version)); err != nil {
		t.Fatalf("replace_all Edit: %v", err)
	}
	if got := readRel(t, root, "code.go"); got != "x\nx\nother\n" {
		t.Fatalf("content = %q", got)
	}
}

// TestEditChecksTheGuardBeforeTheMatch is an ordering guarantee, not a
// nicety.
//
// If the match ran first, a stale caller whose literal happens to be absent
// would be told "your text is not there" — sending it to look for the text
// rather than to re-read a file that changed underneath it. The guard is the
// more fundamental failure and must be reported first.
func TestEditChecksTheGuardBeforeTheMatch(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "code.go")
	writeRel(t, root, "code.go", []byte("original\n"))

	read, err := files.Read(ctx, abs, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.Write(ctx, abs, []byte("rewritten\n"), replaceGuard(read.Version)); err != nil {
		t.Fatal(err)
	}

	_, err = files.Edit(ctx, abs, []byte("nowhere-in-this-file"), []byte("x"), false, replaceGuard(read.Version))
	if !tools.IsCode(err, tools.CodeFSStaleVersion) {
		t.Fatalf("Edit = %v, want the stale guard reported before the missing literal", err)
	}
}

// TestEditPreservesTheDominantNewline. A file written on Windows must not come
// back with its line endings quietly rewritten by an edit that never claimed
// to touch them.
func TestEditPreservesTheDominantNewline(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "crlf.txt")
	writeRel(t, root, "crlf.txt", []byte("alpha\r\nbeta\r\ngamma\r\n"))

	read, err := files.Read(ctx, abs, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.Edit(ctx, abs, []byte("beta"), []byte("BETA"), false, replaceGuard(read.Version)); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	got := readRel(t, root, "crlf.txt")
	if got != "alpha\r\nBETA\r\ngamma\r\n" {
		t.Fatalf("content = %q; the dominant newline was not preserved", got)
	}
}

// TestEditMatchesAcrossLineEndingStyle. A caller that read a CRLF file and
// asks to replace a multi-line literal it saw is matching against what it was
// shown, so matching normalizes line endings even though publication does
// not.
func TestEditMatchesAcrossLineEndingStyle(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "crlf.txt")
	writeRel(t, root, "crlf.txt", []byte("alpha\r\nbeta\r\ngamma\r\n"))

	read, err := files.Read(ctx, abs, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.Edit(ctx, abs, []byte("alpha\nbeta"), []byte("ALPHA\nBETA"), false, replaceGuard(read.Version)); err != nil {
		t.Fatalf("Edit across line-ending style: %v", err)
	}
	if got := readRel(t, root, "crlf.txt"); got != "ALPHA\r\nBETA\r\ngamma\r\n" {
		t.Fatalf("content = %q", got)
	}
}

// TestPublicationPreservesMode. Publication replaces the file by rename, so
// without explicit care an executable script would come back non-executable.
func TestPublicationPreservesMode(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "script.sh")
	if err := os.WriteFile(abs, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	read, err := files.Read(ctx, abs, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.Write(ctx, abs, []byte("#!/bin/sh\necho bye\n"), replaceGuard(read.Version)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755 preserved across publication", info.Mode().Perm())
	}
}

// TestARefusedMutationLeavesNoStagingBehind. A staging directory left in the
// workspace would show up in the agent's own next list_dir and in the user's
// version control.
func TestARefusedMutationLeavesNoStagingBehind(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "code.go")
	writeRel(t, root, "code.go", []byte("body\n"))

	if _, err := files.Write(ctx, abs, []byte("x"), replaceGuard("sha256:not-the-version")); !tools.IsCode(err, tools.CodeFSStaleVersion) {
		t.Fatalf("Write = %v, want stale", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "code.go" {
			t.Fatalf("a refused mutation left %q behind", entry.Name())
		}
	}
}

// TestEditRefusesAFileOverTheBound. An edit reads the whole file into memory,
// so the bound is a memory bound; a partial edit would be worse than a
// refused one.
func TestEditRefusesAFileOverTheBound(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "big.txt")
	writeRel(t, root, "big.txt", []byte(strings.Repeat("a", tools.MaxEditFileBytes+1)))

	_, err := files.Edit(ctx, abs, []byte("a"), []byte("b"), true, replaceGuard("sha256:x"))
	if !tools.IsCode(err, tools.CodeFSTooLarge) && !tools.IsCode(err, tools.CodeFSStaleVersion) {
		t.Fatalf("Edit of an oversized file = %v, want %q", err, tools.CodeFSTooLarge)
	}
}

// TestAnInvalidGuardNeverReachesTheFile.
func TestAnInvalidGuardNeverReachesTheFile(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "guarded.txt")
	writeRel(t, root, "guarded.txt", []byte("body\n"))

	for _, guard := range []tools.MutationGuard{
		{Kind: tools.GuardReplaceIfVersion},
		{Kind: tools.GuardCreateIfAbsent, Version: "sha256:x"},
		{Kind: "invented"},
		{},
	} {
		if _, err := files.Write(ctx, abs, []byte("x"), guard); !tools.IsCode(err, tools.CodeInvalidArgs) {
			t.Fatalf("Write with guard %#v = %v, want invalid args", guard, err)
		}
	}
	if got := readRel(t, root, "guarded.txt"); got != "body\n" {
		t.Fatalf("an invalid guard changed the file: %q", got)
	}
}

func readRel(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
