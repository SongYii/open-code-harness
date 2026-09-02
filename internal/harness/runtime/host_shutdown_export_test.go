package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// TestShutdownFlushesPendingAuditExport pins the fix this file's sibling
// change makes: Shutdown must not return leaving AuditDirectory behind the
// writer's own final state. ExportInterval is set far longer than the test
// itself can run, so if Shutdown relied only on the periodic exporter's own
// next tick, this would fail.
func TestShutdownFlushesPendingAuditExport(t *testing.T) {
	audit := filepath.Join(t.TempDir(), "audit")
	config := Config{
		SQLite:            sqlite.Config{Path: filepath.Join(t.TempDir(), "host.db"), RuntimeID: "runtime-host"},
		HeartbeatInterval: 250 * time.Millisecond,
		HeartbeatDeadline: 700 * time.Millisecond,
		ExportInterval:    time.Hour,
		AuditDirectory:    audit,
	}

	host, err := Launch(context.Background(), config)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	store, err := host.Store()
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	receipt, err := store.Append(context.Background(), application.AppendRequest{
		AppendID: "append-shutdown-flush", SessionID: "session-shutdown-flush", ExpectedVersion: 0,
		CommandID: "command-shutdown-flush", Authority: store.Authority(),
		Events: []application.ProposedEvent{
			{ID: "event-shutdown-flush", SchemaVersion: 1, OccurredAt: testTime, Event: domain.SessionCreated{WorkspaceRoot: "/w"}},
		},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	if err := host.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	verified, err := sqlite.VerifyAuditReplica(audit)
	if err != nil {
		t.Fatalf("VerifyAuditReplica immediately after Shutdown: %v", err)
	}
	if verified.HeadCommitPosition != receipt.CommitPosition {
		t.Fatalf("audit head commit position = %d after Shutdown, want %d (the just-appended commit); "+
			"Shutdown did not flush the pending export", verified.HeadCommitPosition, receipt.CommitPosition)
	}
}
