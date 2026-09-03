package eval

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withFailingSeam overrides one of publishDocument's OS-call seams for the
// duration of one test, always restoring the original.
func withFailingSeam(t *testing.T, restore func()) {
	t.Helper()
	t.Cleanup(restore)
}

func assertOnlyStaleTempOrNothing(t *testing.T, directory, finalName string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() == finalName {
			t.Fatalf("final document %q exists after an injected publish failure", finalName)
		}
		if entry.Name() != finalName && !isEvalTempName(entry.Name()) {
			t.Fatalf("unexpected non-temp entry %q left behind after an injected publish failure", entry.Name())
		}
	}
}

func isEvalTempName(name string) bool {
	return len(name) >= len(tempNamePrefix) && name[:len(tempNamePrefix)] == tempNamePrefix
}

func TestPublishDocumentFaultAtCreateTempFile(t *testing.T) {
	directory := t.TempDir()
	injected := errors.New("injected: create temp file failure")
	original := createTempFile
	createTempFile = func(dir, pattern string) (*os.File, error) { return nil, injected }
	withFailingSeam(t, func() { createTempFile = original })

	if err := PublishAttempt(directory, validAttempt(t)); !errors.Is(err, injected) {
		t.Fatalf("PublishAttempt error = %v, want wrapping %v", err, injected)
	}
	assertOnlyStaleTempOrNothing(t, directory, attemptFilename)
}

func TestPublishDocumentFaultAtWrite(t *testing.T) {
	directory := t.TempDir()
	injected := errors.New("injected: write failure")
	original := writeTempFile
	writeTempFile = func(file *os.File, data []byte) (int, error) { return 0, injected }
	withFailingSeam(t, func() { writeTempFile = original })

	if err := PublishAttempt(directory, validAttempt(t)); !errors.Is(err, injected) {
		t.Fatalf("PublishAttempt error = %v, want wrapping %v", err, injected)
	}
	assertOnlyStaleTempOrNothing(t, directory, attemptFilename)
}

func TestPublishDocumentFaultAtSync(t *testing.T) {
	directory := t.TempDir()
	injected := errors.New("injected: sync failure")
	original := syncTempFile
	syncTempFile = func(file *os.File) error { return injected }
	withFailingSeam(t, func() { syncTempFile = original })

	if err := PublishAttempt(directory, validAttempt(t)); !errors.Is(err, injected) {
		t.Fatalf("PublishAttempt error = %v, want wrapping %v", err, injected)
	}
	assertOnlyStaleTempOrNothing(t, directory, attemptFilename)
}

func TestPublishDocumentFaultAtClose(t *testing.T) {
	directory := t.TempDir()
	injected := errors.New("injected: close failure")
	original := closeTempFile
	closeTempFile = func(file *os.File) error { return injected }
	withFailingSeam(t, func() { closeTempFile = original })

	if err := PublishAttempt(directory, validAttempt(t)); !errors.Is(err, injected) {
		t.Fatalf("PublishAttempt error = %v, want wrapping %v", err, injected)
	}
	assertOnlyStaleTempOrNothing(t, directory, attemptFilename)
}

func TestPublishDocumentFaultAtLink(t *testing.T) {
	directory := t.TempDir()
	injected := errors.New("injected: link failure")
	original := linkTempFile
	linkTempFile = func(oldpath, newpath string) error { return injected }
	withFailingSeam(t, func() { linkTempFile = original })

	if err := PublishAttempt(directory, validAttempt(t)); !errors.Is(err, injected) {
		t.Fatalf("PublishAttempt error = %v, want wrapping %v", err, injected)
	}
	assertOnlyStaleTempOrNothing(t, directory, attemptFilename)
}

func TestPublishDocumentFaultAtLinkPreservesExistingDocument(t *testing.T) {
	// The already-exists case of the link stage is not a fault, but it
	// shares the stage: prove the prior immutable document survives
	// untouched rather than being partially overwritten.
	directory := t.TempDir()
	original := validAttempt(t)
	if err := PublishAttempt(directory, original); err != nil {
		t.Fatalf("PublishAttempt: %v", err)
	}
	other := original
	other.RepetitionIndex = 7
	if err := PublishAttempt(directory, other); !errors.Is(err, errAlreadyPublished) {
		t.Fatalf("second PublishAttempt error = %v, want wrapping errAlreadyPublished", err)
	}
	got, err := ReadAttempt(directory)
	if err != nil {
		t.Fatalf("ReadAttempt: %v", err)
	}
	if got != original {
		t.Fatalf("attempt.json changed after a rejected second publish: got %+v, want %+v", got, original)
	}
}

func TestPublishDocumentFaultAtDirectorySync(t *testing.T) {
	directory := t.TempDir()
	injected := errors.New("injected: directory sync failure")
	original := syncDirectoryFn
	syncDirectoryFn = func(string) error { return injected }
	withFailingSeam(t, func() { syncDirectoryFn = original })

	err := PublishAttempt(directory, validAttempt(t))
	if !errors.Is(err, injected) {
		t.Fatalf("PublishAttempt error = %v, want wrapping %v", err, injected)
	}
	// The rename already committed before the directory-sync stage runs, so
	// unlike the earlier stages, the document itself is durably present even
	// though PublishAttempt reported an error -- design §12 only promises
	// the rename is atomic and the sync is best-effort, not that a sync
	// failure rolls back an already-linked file.
	if _, err := ReadAttempt(directory); err != nil {
		t.Fatalf("ReadAttempt after a directory-sync-only failure: %v", err)
	}
}

func TestCleanupStaleTempFilesRemovesOnlyEvalTempNames(t *testing.T) {
	root := t.TempDir()
	staleTemp := filepath.Join(root, tempNamePrefix+"attempt.json.abc123")
	if err := os.WriteFile(staleTemp, []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	unrelated := filepath.Join(root, "not-eval-owned.tmp")
	if err := os.WriteFile(unrelated, []byte("leave me alone"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	nested := filepath.Join(root, "attempts", "attempt-1")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	nestedStale := filepath.Join(nested, tempNamePrefix+"outcome.json.def456")
	if err := os.WriteFile(nestedStale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	removed, err := CleanupStaleTempFiles(root)
	if err != nil {
		t.Fatalf("CleanupStaleTempFiles: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d files, want 2: %+v", len(removed), removed)
	}
	if _, err := os.Stat(staleTemp); !os.IsNotExist(err) {
		t.Fatalf("Stat(staleTemp) error = %v, want IsNotExist", err)
	}
	if _, err := os.Stat(nestedStale); !os.IsNotExist(err) {
		t.Fatalf("Stat(nestedStale) error = %v, want IsNotExist", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("CleanupStaleTempFiles touched an unrelated file: %v", err)
	}
}

// TestEvidenceManifestMustPublishLastForScoreability is the plan's requested
// mutation check: publishing manifest.json before a required artifact is
// present must not make the Attempt look scoreable. This package does not
// yet cross-check manifest entries against files actually on disk at
// publish time (that lands with the evidence collector, design §14) --
// this test instead pins the property one layer up: Manifest.Validate
// itself refuses a document that claims required evidence was collected
// without recording the bytes that would prove it (no sha256/byteLength),
// so a manifest cannot silently claim scoreability for evidence that was
// never actually produced.
func TestEvidenceManifestMustPublishLastForScoreability(t *testing.T) {
	manifest := validEvidenceManifest(t, mustAttemptID(t))
	manifest.Entries[0].Required = true
	manifest.Entries[0].State = EntryCollected
	manifest.Entries[0].SHA256 = "" // the "artifact never actually landed" case
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a required collected entry with no sha256, i.e. no proof the artifact exists")
	}
}
