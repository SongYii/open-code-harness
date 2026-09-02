package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyAuditReplicaMatchesImport(t *testing.T) {
	_, directory := replicaRoundTrip(t, 2)

	verified, err := VerifyAuditReplica(directory)
	if err != nil {
		t.Fatalf("VerifyAuditReplica: %v", err)
	}
	if verified.HeadCommitPosition != 2 {
		t.Fatalf("HeadCommitPosition = %d, want 2", verified.HeadCommitPosition)
	}
	if verified.HeadAuditDigest == "" {
		t.Fatal("HeadAuditDigest is empty")
	}

	imported, err := ImportAuditReplica(context.Background(), directory,
		Config{Path: filepath.Join(t.TempDir(), "imported.db"), RuntimeID: "runtime-import"})
	if err != nil {
		t.Fatalf("ImportAuditReplica: %v", err)
	}
	defer imported.Close()
	var headFromImport uint64
	var digestFromImport []byte
	if err := imported.db.QueryRowContext(context.Background(),
		"SELECT head_commit_position, head_audit_digest FROM store_metadata WHERE id = 1").Scan(&headFromImport, &digestFromImport); err != nil {
		t.Fatal(err)
	}
	if verified.HeadCommitPosition != headFromImport {
		t.Fatalf("VerifyAuditReplica head = %d, ImportAuditReplica landed head = %d; must agree", verified.HeadCommitPosition, headFromImport)
	}

	if len(verified.Sessions) != 1 {
		t.Fatalf("Sessions = %d, want 1", len(verified.Sessions))
	}
	session := verified.Sessions[0]
	if session.SessionID != "session-export" {
		t.Fatalf("SessionID = %q, want %q", session.SessionID, "session-export")
	}
	if session.HeadVersion != 5 {
		t.Fatalf("HeadVersion = %d, want 5", session.HeadVersion)
	}
	if len(session.Events) != 5 {
		t.Fatalf("Events = %d, want 5", len(session.Events))
	}
	for index, record := range session.Events {
		if record.Sequence != uint64(index+1) {
			t.Fatalf("Events[%d].Sequence = %d, want %d", index, record.Sequence, index+1)
		}
	}
}

func TestVerifyAuditReplicaNeverOpensADatabase(t *testing.T) {
	// VerifyAuditReplica must work against a bare replica directory with no
	// database file anywhere nearby — the whole point of design §14's
	// verify-only operation is that it needs no live SQLite connection.
	_, directory := replicaRoundTrip(t, 1)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".db" {
			t.Fatalf("replica directory unexpectedly contains a database file: %s", entry.Name())
		}
	}
	if _, err := VerifyAuditReplica(directory); err != nil {
		t.Fatalf("VerifyAuditReplica: %v", err)
	}
}

func TestVerifyAuditReplicaFailsClosedOnTamperedSegment(t *testing.T) {
	_, directory := replicaRoundTrip(t, 1)
	segments, err := os.ReadDir(filepath.Join(directory, "segments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) == 0 {
		t.Fatal("expected at least one exported segment")
	}
	segmentPath := filepath.Join(directory, "segments", segments[0].Name())
	raw, err := os.ReadFile(segmentPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), raw...)
	tampered[0] ^= 0xFF
	if err := os.WriteFile(segmentPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAuditReplica(directory); err == nil {
		t.Fatal("VerifyAuditReplica accepted a tampered segment")
	}
}
