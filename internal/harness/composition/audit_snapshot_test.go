package composition_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestVerifyAuditSnapshotMatchesAppendedEvents(t *testing.T) {
	_, sessionID, store := seedExportSession(t, domain.SessionCreated{WorkspaceRoot: "/workspace"}, domain.SessionClosed{})
	auditDirectory := t.TempDir()
	if _, err := store.ExportOnce(context.Background(), sqlite.ExportConfig{Directory: auditDirectory}); err != nil {
		t.Fatalf("ExportOnce: %v", err)
	}

	snapshot, err := composition.VerifyAuditSnapshot(auditDirectory)
	if err != nil {
		t.Fatalf("VerifyAuditSnapshot: %v", err)
	}
	if snapshot.HeadCommitPosition == 0 {
		t.Fatal("HeadCommitPosition = 0, want a positive commit position")
	}
	if snapshot.HeadAuditDigest == "" {
		t.Fatal("HeadAuditDigest is empty")
	}
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("Sessions = %d, want 1", len(snapshot.Sessions))
	}
	session := snapshot.Sessions[0]
	if session.SessionID != string(sessionID) {
		t.Fatalf("SessionID = %q, want %q", session.SessionID, sessionID)
	}
	if len(session.Events) != 2 {
		t.Fatalf("Events = %d, want 2 (one per appended SessionCreated)", len(session.Events))
	}
	for index, record := range session.Events {
		if record.Sequence != uint64(index+1) {
			t.Fatalf("Events[%d].Sequence = %d, want %d", index, record.Sequence, index+1)
		}
	}
	if _, ok := session.Events[0].Event.(domain.SessionCreated); !ok {
		t.Fatalf("Events[0].Event = %T, want domain.SessionCreated", session.Events[0].Event)
	}
	if _, ok := session.Events[1].Event.(domain.SessionClosed); !ok {
		t.Fatalf("Events[1].Event = %T, want domain.SessionClosed", session.Events[1].Event)
	}
}

func TestVerifyAuditSnapshotRequiresNoLiveDatabase(t *testing.T) {
	_, _, store := seedExportSession(t, domain.SessionCreated{WorkspaceRoot: "/workspace"})
	auditDirectory := t.TempDir()
	if _, err := store.ExportOnce(context.Background(), sqlite.ExportConfig{Directory: auditDirectory}); err != nil {
		t.Fatalf("ExportOnce: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := composition.VerifyAuditSnapshot(auditDirectory); err != nil {
		t.Fatalf("VerifyAuditSnapshot after the writer closed: %v", err)
	}
}

func TestVerifyAuditSnapshotFailsClosedOnMissingDirectory(t *testing.T) {
	if _, err := composition.VerifyAuditSnapshot(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("VerifyAuditSnapshot accepted a missing replica directory")
	}
}
