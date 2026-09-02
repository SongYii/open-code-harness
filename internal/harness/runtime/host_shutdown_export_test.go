package runtime

import (
	"context"
	"os"
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

// TestShutdownIsIdempotent proves a second Shutdown call is a safe no-op:
// it must not attempt a second flush, re-release an already-released lease,
// or error.
func TestShutdownIsIdempotent(t *testing.T) {
	config := hostConfig(t)
	audit := filepath.Join(t.TempDir(), "audit")
	config.AuditDirectory = audit

	host, err := Launch(context.Background(), config)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if err := host.Shutdown(context.Background()); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	if err := host.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown: %v, want nil (idempotent)", err)
	}
}

// TestShutdownReleasesLeaseAndSucceedsDespiteBlockedFlush proves design
// §12's "export lag/failure must never block Shutdown either" extends to a
// flush that outright fails, not just one that lags: Shutdown must still
// release the lease and return successfully, and a caller must be able to
// launch a successor immediately. The flush is blocked by replacing
// AuditDirectory with a plain file, so ExportOnce's own MkdirAll fails.
func TestShutdownReleasesLeaseAndSucceedsDespiteBlockedFlush(t *testing.T) {
	config := hostConfig(t)
	blockedAuditPath := filepath.Join(t.TempDir(), "audit-blocked")
	if err := os.WriteFile(blockedAuditPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	config.AuditDirectory = blockedAuditPath

	host, err := Launch(context.Background(), config)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if err := host.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v, want nil despite a blocked flush destination", err)
	}

	// The lease must actually be free: a successor can launch immediately.
	successor, err := Launch(context.Background(), config)
	if err != nil {
		t.Fatalf("successor launch after a blocked-flush shutdown: %v", err)
	}
	if err := successor.Shutdown(context.Background()); err != nil {
		t.Fatalf("successor shutdown: %v", err)
	}
}
