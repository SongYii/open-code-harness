package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const evaluationSessionID = domain.SessionID("session-export")

// seededEvaluationDatabase seeds a real database with two Turns, releases
// the writer lease, and closes the store, leaving a database on disk an
// evaluation open can target cold.
func seededEvaluationDatabase(t *testing.T) string {
	t.Helper()
	config := tempStoreConfig(t)
	store := openStore(t, config)
	seedAppends(t, store, 2)
	if err := store.ReleaseLease(context.Background()); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return config.Path
}

func TestInspectEvaluationStoreHappyPath(t *testing.T) {
	path := seededEvaluationDatabase(t)

	inspection, err := InspectEvaluationStore(context.Background(), path, evaluationSessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}
	if inspection.SessionID != string(evaluationSessionID) {
		t.Fatalf("SessionID = %q, want %q", inspection.SessionID, evaluationSessionID)
	}
	if inspection.SessionHeadSequence == 0 {
		t.Fatal("SessionHeadSequence = 0, want positive")
	}
	if inspection.StoreHeadCommitPosition == 0 {
		t.Fatal("StoreHeadCommitPosition = 0, want positive")
	}
	if inspection.SessionHeadAppendCommitPosition == 0 || inspection.SessionHeadAppendCommitPosition > inspection.StoreHeadCommitPosition {
		t.Fatalf("SessionHeadAppendCommitPosition = %d, want in (0, %d]", inspection.SessionHeadAppendCommitPosition, inspection.StoreHeadCommitPosition)
	}
	if !inspection.Terminal.Open {
		t.Fatal("Terminal.Open = false, want true (an active session)")
	}
	if inspection.Terminal.Running {
		t.Fatal("Terminal.Running = true, want false (no Turn left active by seedAppends)")
	}
}

func TestInspectEvaluationStoreCreatesNoDestination(t *testing.T) {
	path := seededEvaluationDatabase(t)
	before, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if _, err := InspectEvaluationStore(context.Background(), path, evaluationSessionID); err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}
	after, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("InspectEvaluationStore created or removed a filesystem entry: before=%d after=%d", len(before), len(after))
	}
}

func TestInspectEvaluationStoreRefusesLiveLease(t *testing.T) {
	config := tempStoreConfig(t)
	store := openStore(t, config) // lease held for the whole test; not released.
	seedAppends(t, store, 1)

	_, err := InspectEvaluationStore(context.Background(), config.Path, evaluationSessionID)
	if err == nil {
		t.Fatal("InspectEvaluationStore succeeded while the writer lease is live")
	}
	var leaseErr *ErrEvaluationLeaseLive
	if !errors.As(err, &leaseErr) {
		t.Fatalf("InspectEvaluationStore error = %v (%T), want *ErrEvaluationLeaseLive", err, err)
	}
	if leaseErr.Owner != config.RuntimeID {
		t.Fatalf("ErrEvaluationLeaseLive.Owner = %q, want %q", leaseErr.Owner, config.RuntimeID)
	}
}

func TestInspectEvaluationStoreDetectsHeadsCorruption(t *testing.T) {
	config := tempStoreConfig(t)
	store := openStore(t, config)
	seedAppends(t, store, 1)
	if _, err := store.db.ExecContext(context.Background(),
		"UPDATE session_heads SET status = 'closed' WHERE session_id = ?", string(evaluationSessionID)); err != nil {
		t.Fatalf("tamper session_heads: %v", err)
	}
	if err := store.ReleaseLease(context.Background()); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := InspectEvaluationStore(context.Background(), config.Path, evaluationSessionID); err == nil {
		t.Fatal("InspectEvaluationStore accepted a session_heads row that disagrees with canonical replay")
	}
}

func TestInspectEvaluationStoreRejectsUnknownSession(t *testing.T) {
	path := seededEvaluationDatabase(t)
	if _, err := InspectEvaluationStore(context.Background(), path, "session-does-not-exist"); err == nil {
		t.Fatal("InspectEvaluationStore accepted an unknown session")
	}
}

func TestOpenEvaluationExportHappyPath(t *testing.T) {
	path := seededEvaluationDatabase(t)
	inspection, err := InspectEvaluationStore(context.Background(), path, evaluationSessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}

	export, err := OpenEvaluationExport(context.Background(), inspection)
	if err != nil {
		t.Fatalf("OpenEvaluationExport: %v", err)
	}
	defer export.Close()

	auditDirectory := filepath.Join(t.TempDir(), "audit")
	verified, err := export.RegenerateAudit(context.Background(), auditDirectory)
	if err != nil {
		t.Fatalf("RegenerateAudit: %v", err)
	}
	if verified.HeadCommitPosition != inspection.StoreHeadCommitPosition {
		t.Fatalf("verified.HeadCommitPosition = %d, want %d", verified.HeadCommitPosition, inspection.StoreHeadCommitPosition)
	}
	if verified.HeadCommitPosition < inspection.SessionHeadAppendCommitPosition {
		t.Fatalf("regenerated audit head %d does not include the session-head append at %d",
			verified.HeadCommitPosition, inspection.SessionHeadAppendCommitPosition)
	}
	if len(verified.Sessions) != 1 || verified.Sessions[0].SessionID != string(evaluationSessionID) {
		t.Fatalf("verified.Sessions = %+v, want exactly one entry for %q", verified.Sessions, evaluationSessionID)
	}
}

func TestOpenEvaluationExportNeverCopiesLiveAuditDirectory(t *testing.T) {
	// RegenerateAudit must write into the caller's empty destination, not
	// mirror config.AuditDirectory from a live writer -- there is no such
	// directory anywhere in this test, and generation must still succeed
	// purely from database append records.
	path := seededEvaluationDatabase(t)
	inspection, err := InspectEvaluationStore(context.Background(), path, evaluationSessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}
	export, err := OpenEvaluationExport(context.Background(), inspection)
	if err != nil {
		t.Fatalf("OpenEvaluationExport: %v", err)
	}
	defer export.Close()

	destination := filepath.Join(t.TempDir(), "regenerated")
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination unexpectedly exists before RegenerateAudit")
	}
	if _, err := export.RegenerateAudit(context.Background(), destination); err != nil {
		t.Fatalf("RegenerateAudit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "manifests")); err != nil {
		t.Fatalf("RegenerateAudit did not populate %s: %v", destination, err)
	}
}

func TestOpenEvaluationExportRefusesNonEmptyDestination(t *testing.T) {
	path := seededEvaluationDatabase(t)
	inspection, err := InspectEvaluationStore(context.Background(), path, evaluationSessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}
	export, err := OpenEvaluationExport(context.Background(), inspection)
	if err != nil {
		t.Fatalf("OpenEvaluationExport: %v", err)
	}
	defer export.Close()

	destination := filepath.Join(t.TempDir(), "audit")
	if err := os.MkdirAll(filepath.Join(destination, "segments"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "segments", "preexisting.jsonl"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := export.RegenerateAudit(context.Background(), destination); err == nil {
		t.Fatal("RegenerateAudit accepted a non-empty destination")
	}
}

func TestOpenEvaluationExportRefusesLiveLease(t *testing.T) {
	config := tempStoreConfig(t)
	store := openStore(t, config)
	seedAppends(t, store, 1)
	inspection, err := inspectEvaluationSession(context.Background(), store.db, config.Path, evaluationSessionID)
	if err != nil {
		t.Fatalf("inspectEvaluationSession: %v", err)
	}
	// The writer lease is still live: openStore never released it.

	_, err = OpenEvaluationExport(context.Background(), inspection)
	var leaseErr *ErrEvaluationLeaseLive
	if !errors.As(err, &leaseErr) {
		t.Fatalf("OpenEvaluationExport error = %v, want *ErrEvaluationLeaseLive", err)
	}
}

func TestOpenEvaluationExportRefusesChangedDatabase(t *testing.T) {
	config := tempStoreConfig(t)
	store := openStore(t, config)
	seedAppends(t, store, 1)
	if err := store.ReleaseLease(context.Background()); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	inspection, err := InspectEvaluationStore(context.Background(), config.Path, evaluationSessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}

	// The database changes after inspection: a fresh writer appends more.
	// seedAppends' test helper hardcodes fencing token 1 (fine for the very
	// first writer on a fresh database, but wrong for a second writer that
	// reacquired the lease after release), so this append is built directly
	// against store2's own freshly reacquired Authority instead.
	store2 := openStore(t, config)
	version := exportedHead(t, store2)
	changeRequest := application.AppendRequest{
		AppendID:        "append-changed-db",
		SessionID:       evaluationSessionID,
		ExpectedVersion: version,
		CommandID:       "command-changed-db",
		Authority:       store2.Authority(),
		Events: []application.ProposedEvent{
			{ID: "event-changed-db-1", SchemaVersion: 1, OccurredAt: testTime, Event: domain.TurnStarted{TurnID: "turn-changed-db", Input: "x"}},
			{ID: "event-changed-db-2", SchemaVersion: 1, OccurredAt: testTime, Event: domain.TurnCompleted{TurnID: "turn-changed-db"}},
		},
	}
	mustAppend(t, store2, changeRequest)
	if err := store2.ReleaseLease(context.Background()); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if err := store2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := OpenEvaluationExport(context.Background(), inspection); err == nil {
		t.Fatal("OpenEvaluationExport accepted a database that changed since inspection")
	}
}

// tableSnapshot captures every row-count and metadata fact an evaluation
// open must never change, so a snapshot taken before and after one can be
// compared directly.
type tableSnapshot struct {
	events        int
	eventAppends  int
	eventStreams  int
	sessionHeads  int
	runtimeLeases int
	exportOutbox  int
	headPosition  uint64
	headDigest    string
}

func snapshotTables(t *testing.T, path string) tableSnapshot {
	t.Helper()
	// Opened and closed as a full writer so the snapshot itself never races
	// the cold path it is meant to bound; the test sequences these opens so
	// they never overlap with a live evaluation open.
	store, err := Open(context.Background(), Config{Path: path, RuntimeID: "snapshot-runtime"})
	if err != nil {
		t.Fatalf("open for snapshot: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	snapshot := tableSnapshot{
		events:        tableCount(t, store, "events"),
		eventAppends:  tableCount(t, store, "event_appends"),
		eventStreams:  tableCount(t, store, "event_streams"),
		sessionHeads:  tableCount(t, store, "session_heads"),
		runtimeLeases: tableCount(t, store, "runtime_leases"),
		exportOutbox:  tableCount(t, store, "export_outbox"),
	}
	var digest []byte
	if err := store.db.QueryRowContext(ctx, "SELECT head_commit_position, head_audit_digest FROM store_metadata WHERE id = 1").
		Scan(&snapshot.headPosition, &digest); err != nil {
		t.Fatalf("read store_metadata: %v", err)
	}
	snapshot.headDigest = string(digest)
	if err := store.ReleaseLease(ctx); err != nil {
		t.Fatalf("release snapshot lease: %v", err)
	}
	return snapshot
}

func TestInspectAndExportEvaluationEvidenceMutateNothing(t *testing.T) {
	path := seededEvaluationDatabase(t)
	before := snapshotTables(t, path)

	inspection, err := InspectEvaluationStore(context.Background(), path, evaluationSessionID)
	if err != nil {
		t.Fatalf("InspectEvaluationStore: %v", err)
	}
	afterInspect := snapshotTables(t, path)
	if afterInspect != before {
		t.Fatalf("InspectEvaluationStore changed table state:\nbefore = %+v\nafter  = %+v", before, afterInspect)
	}

	export, err := OpenEvaluationExport(context.Background(), inspection)
	if err != nil {
		t.Fatalf("OpenEvaluationExport: %v", err)
	}
	if _, err := export.RegenerateAudit(context.Background(), filepath.Join(t.TempDir(), "audit")); err != nil {
		t.Fatalf("RegenerateAudit: %v", err)
	}
	if err := export.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	afterExport := snapshotTables(t, path)
	if afterExport != before {
		t.Fatalf("ExportEvaluationEvidence changed table state:\nbefore = %+v\nafter  = %+v", before, afterExport)
	}
}
