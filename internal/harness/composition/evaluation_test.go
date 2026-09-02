package composition_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestInspectEvaluationStoreHappyPath(t *testing.T) {
	path, sessionID, store := seedExportSession(t, domain.SessionCreated{WorkspaceRoot: "/workspace"}, domain.SessionClosed{})
	if err := store.ReleaseLease(context.Background()); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	inspection, err := composition.InspectEvaluationStore(context.Background(), path, sessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}
	if inspection.SessionID != string(sessionID) {
		t.Fatalf("SessionID = %q, want %q", inspection.SessionID, sessionID)
	}
	if inspection.SessionHeadSequence != 2 {
		t.Fatalf("SessionHeadSequence = %d, want 2", inspection.SessionHeadSequence)
	}
	if inspection.SessionHeadAppendCommitPosition == 0 || inspection.SessionHeadAppendCommitPosition > inspection.StoreHeadCommitPosition {
		t.Fatalf("SessionHeadAppendCommitPosition = %d, want in (0, %d]", inspection.SessionHeadAppendCommitPosition, inspection.StoreHeadCommitPosition)
	}
	if inspection.Terminal.Open {
		t.Fatal("Terminal.Open = true, want false after SessionClosed")
	}
	if inspection.Terminal.Status != "closed" {
		t.Fatalf("Terminal.Status = %q, want %q", inspection.Terminal.Status, "closed")
	}
}

func TestInspectEvaluationStoreRefusesLiveLease(t *testing.T) {
	path, sessionID, _ := seedExportSession(t, domain.SessionCreated{WorkspaceRoot: "/workspace"})
	// The store from seedExportSession is left open by t.Cleanup; its lease
	// is still live for the duration of this test.
	if _, err := composition.InspectEvaluationStore(context.Background(), path, sessionID); err == nil {
		t.Fatal("InspectEvaluationStore succeeded while the writer lease is live")
	}
}

func TestExportEvaluationEvidenceHappyPath(t *testing.T) {
	path, sessionID, store := seedExportSession(t, domain.SessionCreated{WorkspaceRoot: "/workspace"}, domain.SessionClosed{})
	if err := store.ReleaseLease(context.Background()); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	inspection, err := composition.InspectEvaluationStore(context.Background(), path, sessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}

	destinations := composition.EvaluationExportDestinations{
		TranscriptPath: filepath.Join(t.TempDir(), "transcript.jsonl"),
		AuditDirectory: filepath.Join(t.TempDir(), "audit"),
	}
	evidence, err := composition.ExportEvaluationEvidence(context.Background(), inspection, destinations)
	if err != nil {
		t.Fatalf("ExportEvaluationEvidence: %v", err)
	}
	if evidence.TranscriptDigest == "" {
		t.Fatal("TranscriptDigest is empty")
	}
	if evidence.AuditHeadDigest == "" {
		t.Fatal("AuditHeadDigest is empty")
	}
	if evidence.SessionHeadAppendCommitPosition == 0 {
		t.Fatal("SessionHeadAppendCommitPosition = 0, want positive")
	}

	transcriptBytes, err := os.ReadFile(destinations.TranscriptPath)
	if err != nil {
		t.Fatalf("ReadFile(transcript): %v", err)
	}
	assertSnapshotThenComplete(t, transcriptBytes)
	if _, err := os.Stat(filepath.Join(destinations.AuditDirectory, "manifests")); err != nil {
		t.Fatalf("ExportEvaluationEvidence did not populate the audit directory: %v", err)
	}
}

func TestExportEvaluationEvidenceRefusesLiveLease(t *testing.T) {
	path, sessionID, store := seedExportSession(t, domain.SessionCreated{WorkspaceRoot: "/workspace"})
	if err := store.ReleaseLease(context.Background()); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	inspection, err := composition.InspectEvaluationStore(context.Background(), path, sessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}

	// Reacquire the lease after inspection, before attempting export.
	relocked, err := sqlite.Open(context.Background(), sqlite.Config{Path: path, RuntimeID: "export-test"})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = relocked.Close() })

	destinations := composition.EvaluationExportDestinations{
		TranscriptPath: filepath.Join(t.TempDir(), "transcript.jsonl"),
		AuditDirectory: filepath.Join(t.TempDir(), "audit"),
	}
	if _, err := composition.ExportEvaluationEvidence(context.Background(), inspection, destinations); err == nil {
		t.Fatal("ExportEvaluationEvidence succeeded while the writer lease is live")
	}
}

func TestExportEvaluationEvidenceRequiresBothDestinations(t *testing.T) {
	path, sessionID, store := seedExportSession(t, domain.SessionCreated{WorkspaceRoot: "/workspace"})
	if err := store.ReleaseLease(context.Background()); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	inspection, err := composition.InspectEvaluationStore(context.Background(), path, sessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}
	if _, err := composition.ExportEvaluationEvidence(context.Background(), inspection, composition.EvaluationExportDestinations{
		TranscriptPath: filepath.Join(t.TempDir(), "transcript.jsonl"),
	}); err == nil {
		t.Fatal("ExportEvaluationEvidence accepted an empty AuditDirectory")
	}
}
