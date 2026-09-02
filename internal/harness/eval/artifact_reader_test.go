package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// collectedHappyAttempt runs a real Attempt end to end and collects its
// evidence, returning everything an ArtifactReader test needs.
func collectedHappyAttempt(t *testing.T) (directories AttemptRootDirectories, outcome Outcome, manifest EvidenceManifest) {
	t.Helper()
	directories, execution, scenario := runHappyAttempt(t)
	outcome, manifest, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{})
	if err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	return directories, outcome, manifest
}

func TestNewArtifactReaderHappyPath(t *testing.T) {
	directories, outcome, manifest := collectedHappyAttempt(t)

	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	if reader.Outcome().AttemptID != outcome.AttemptID {
		t.Fatalf("Outcome().AttemptID = %q, want %q", reader.Outcome().AttemptID, outcome.AttemptID)
	}
	if len(reader.Manifest().Entries) != len(manifest.Entries) {
		t.Fatalf("Manifest() has %d entries, want %d", len(reader.Manifest().Entries), len(manifest.Entries))
	}
	transcriptEntries := reader.Entries("transcript")
	if len(transcriptEntries) != 1 {
		t.Fatalf("Entries(transcript) = %d, want 1", len(transcriptEntries))
	}
	data, err := reader.ReadEntry(transcriptEntries[0].Path)
	if err != nil {
		t.Fatalf("ReadEntry(transcript): %v", err)
	}
	if len(data) == 0 {
		t.Fatal("transcript entry read back empty")
	}
}

func TestNewArtifactReaderRejectsOutcomeManifestDigestMismatch(t *testing.T) {
	directories, outcome, _ := collectedHappyAttempt(t)

	// Overwrite the published outcome.json with a different, but still
	// well-formed, Outcome -- bypassing PublishOutcome's write-once guard
	// directly, the way an out-of-band write or corruption might.
	tampered := outcome
	tampered.Message = "tampered"
	raw := marshal(t, tampered)
	if err := os.WriteFile(filepath.Join(directories.Root, outcomeFilename), raw, 0o600); err != nil {
		t.Fatalf("overwrite outcome.json: %v", err)
	}

	_, err := NewArtifactReader(directories)
	if err == nil {
		t.Fatal("NewArtifactReader() error = nil, want a manifest/outcome digest mismatch")
	}
	if !errors.Is(err, errArtifactReaderMismatch) {
		t.Fatalf("NewArtifactReader() error = %v, want wrapping errArtifactReaderMismatch", err)
	}
}

func TestArtifactReaderReadEntryRejectsTamperedFile(t *testing.T) {
	directories, _, manifest := collectedHappyAttempt(t)

	var transcriptPath string
	for _, entry := range manifest.Entries {
		if entry.Role == "transcript" {
			transcriptPath = entry.Path
		}
	}
	if transcriptPath == "" {
		t.Fatal("no transcript entry in manifest")
	}
	fullPath := filepath.Join(directories.Evidence, transcriptPath)
	if err := os.WriteFile(fullPath, []byte("tampered content, different length and bytes"), 0o600); err != nil {
		t.Fatalf("tamper with transcript file: %v", err)
	}

	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	_, err = reader.ReadEntry(transcriptPath)
	if err == nil {
		t.Fatal("ReadEntry() error = nil, want a mismatch after tampering")
	}
	if !errors.Is(err, errArtifactReaderMismatch) {
		t.Fatalf("ReadEntry() error = %v, want wrapping errArtifactReaderMismatch", err)
	}
}

func TestArtifactReaderReadEntryRejectsSymlinkSwap(t *testing.T) {
	directories, _, manifest := collectedHappyAttempt(t)

	var transcriptPath string
	for _, entry := range manifest.Entries {
		if entry.Role == "transcript" {
			transcriptPath = entry.Path
		}
	}
	fullPath := filepath.Join(directories.Evidence, transcriptPath)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	if err := os.WriteFile(elsewhere, []byte("attacker controlled"), 0o600); err != nil {
		t.Fatalf("write elsewhere: %v", err)
	}
	if err := os.Remove(fullPath); err != nil {
		t.Fatalf("remove original: %v", err)
	}
	if err := os.Symlink(elsewhere, fullPath); err != nil {
		t.Fatalf("swap in a symlink: %v", err)
	}

	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	_, err = reader.ReadEntry(transcriptPath)
	if err == nil {
		t.Fatal("ReadEntry() error = nil, want a refusal after a post-collection symlink swap")
	}
	if !errors.Is(err, errArtifactReaderMismatch) {
		t.Fatalf("ReadEntry() error = %v, want wrapping errArtifactReaderMismatch", err)
	}
}
