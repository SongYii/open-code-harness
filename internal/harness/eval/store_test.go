package eval

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishAndReadAttempt(t *testing.T) {
	directory := t.TempDir()
	attempt := validAttempt(t)
	if err := PublishAttempt(directory, attempt); err != nil {
		t.Fatalf("PublishAttempt: %v", err)
	}
	got, err := ReadAttempt(directory)
	if err != nil {
		t.Fatalf("ReadAttempt: %v", err)
	}
	if got != attempt {
		t.Fatalf("ReadAttempt = %+v, want %+v", got, attempt)
	}
}

func TestPublishAttemptIsImmutable(t *testing.T) {
	directory := t.TempDir()
	attempt := validAttempt(t)
	if err := PublishAttempt(directory, attempt); err != nil {
		t.Fatalf("PublishAttempt: %v", err)
	}
	other := attempt
	other.RepetitionIndex = 1
	if err := PublishAttempt(directory, other); !errors.Is(err, errAlreadyPublished) {
		t.Fatalf("second PublishAttempt error = %v, want wrapping errAlreadyPublished", err)
	}
	got, err := ReadAttempt(directory)
	if err != nil {
		t.Fatalf("ReadAttempt: %v", err)
	}
	if got != attempt {
		t.Fatalf("attempt.json changed after a rejected second publish: got %+v, want original %+v", got, attempt)
	}
}

func TestPublishDocumentLeavesNoTempFileBehind(t *testing.T) {
	directory := t.TempDir()
	if err := PublishAttempt(directory, validAttempt(t)); err != nil {
		t.Fatalf("PublishAttempt: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != attemptFilename {
			t.Fatalf("unexpected leftover directory entry after publish: %q", entry.Name())
		}
	}
}

func TestPublishAndReadOutcome(t *testing.T) {
	directory := t.TempDir()
	outcome := validOutcome(t, mustAttemptID(t))
	if err := PublishOutcome(directory, outcome); err != nil {
		t.Fatalf("PublishOutcome: %v", err)
	}
	got, err := ReadOutcome(directory)
	if err != nil {
		t.Fatalf("ReadOutcome: %v", err)
	}
	if !got.StartedAt.Equal(outcome.StartedAt) || !got.EndedAt.Equal(outcome.EndedAt) {
		t.Fatalf("ReadOutcome timestamps = %+v, want %+v", got, outcome)
	}
	if got.Status != outcome.Status || got.Code != outcome.Code {
		t.Fatalf("ReadOutcome = %+v, want %+v", got, outcome)
	}
}

func TestPublishOutcomeIsAtMostOnce(t *testing.T) {
	directory := t.TempDir()
	attemptID := mustAttemptID(t)
	if err := PublishOutcome(directory, validOutcome(t, attemptID)); err != nil {
		t.Fatalf("PublishOutcome: %v", err)
	}
	if err := PublishOutcome(directory, validOutcome(t, attemptID)); !errors.Is(err, errAlreadyPublished) {
		t.Fatalf("second PublishOutcome error = %v, want wrapping errAlreadyPublished", err)
	}
}

func TestPublishEvidenceManifestCreatesEvidenceDirectory(t *testing.T) {
	directory := t.TempDir()
	manifest := validEvidenceManifest(t, mustAttemptID(t))
	if err := PublishEvidenceManifest(directory, manifest); err != nil {
		t.Fatalf("PublishEvidenceManifest: %v", err)
	}
	info, err := os.Stat(filepath.Join(directory, evidenceDirectoryName, manifestFilename))
	if err != nil {
		t.Fatalf("Stat manifest.json: %v", err)
	}
	if info.IsDir() {
		t.Fatal("manifest.json is a directory")
	}
	got, err := ReadEvidenceManifest(directory)
	if err != nil {
		t.Fatalf("ReadEvidenceManifest: %v", err)
	}
	if got.FileCount != manifest.FileCount || got.TotalBytes != manifest.TotalBytes {
		t.Fatalf("ReadEvidenceManifest = %+v, want %+v", got, manifest)
	}
}

func TestPublishEvidenceManifestIsCommitMarker(t *testing.T) {
	directory := t.TempDir()
	if _, err := ReadEvidenceManifest(directory); err == nil {
		t.Fatal("ReadEvidenceManifest succeeded before any manifest was published")
	}
	if err := PublishEvidenceManifest(directory, validEvidenceManifest(t, mustAttemptID(t))); err != nil {
		t.Fatalf("PublishEvidenceManifest: %v", err)
	}
	if _, err := ReadEvidenceManifest(directory); err != nil {
		t.Fatalf("ReadEvidenceManifest after publish: %v", err)
	}
}

func TestPublishScoreNeverReplacesAnotherScore(t *testing.T) {
	directory := t.TempDir()
	attemptID := mustAttemptID(t)
	first := validScore(t, attemptID)
	second := validScore(t, attemptID)
	if err := PublishScore(directory, first); err != nil {
		t.Fatalf("PublishScore(first): %v", err)
	}
	if err := PublishScore(directory, second); err != nil {
		t.Fatalf("PublishScore(second): %v", err)
	}
	scores, err := ReadScores(directory)
	if err != nil {
		t.Fatalf("ReadScores: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("ReadScores returned %d scores, want 2", len(scores))
	}

	// Republishing the exact same Score ID must fail rather than overwrite.
	if err := PublishScore(directory, first); !errors.Is(err, errAlreadyPublished) {
		t.Fatalf("republishing the same Score ID error = %v, want wrapping errAlreadyPublished", err)
	}
	scores, err = ReadScores(directory)
	if err != nil {
		t.Fatalf("ReadScores: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("ReadScores returned %d scores after a rejected republish, want 2", len(scores))
	}
}

func TestReadScoresOnAttemptWithNoScoresYet(t *testing.T) {
	directory := t.TempDir()
	scores, err := ReadScores(directory)
	if err != nil {
		t.Fatalf("ReadScores: %v", err)
	}
	if len(scores) != 0 {
		t.Fatalf("ReadScores returned %d scores, want 0", len(scores))
	}
}

func TestPublishRejectsInvalidDocument(t *testing.T) {
	directory := t.TempDir()
	invalid := validAttempt(t)
	invalid.ID = ""
	if err := PublishAttempt(directory, invalid); err == nil {
		t.Fatal("PublishAttempt accepted an invalid Attempt")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("PublishAttempt left %d entries behind after a validation failure, want 0", len(entries))
	}
}
