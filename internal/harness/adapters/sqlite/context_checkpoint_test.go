package sqlite

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// validCheckpoint builds a ContextCompactionCompleted event whose Checkpoint
// correctly covers the given canonical records from D0, mirroring what the
// real orchestrator (context_orchestrator.go) always produces.
func validCheckpoint(t *testing.T, id string, records []domain.RecordedEvent, through uint64) domain.ContextCompactionCompleted {
	t.Helper()
	covered := make([]domain.RecordedEvent, 0, len(records))
	for _, record := range records {
		if record.Sequence > through {
			break
		}
		covered = append(covered, record)
	}
	digest, count, err := contextengine.ComputeSourceDigest(covered)
	if err != nil {
		t.Fatalf("ComputeSourceDigest: %v", err)
	}
	return domain.ContextCompactionCompleted{
		ID: domain.ContextCompactionID(id + "-compaction"),
		Checkpoint: domain.ContextCheckpointRecord{
			ID:                id,
			Kind:              domain.ContextCheckpointKindRollingSummary,
			SourceSchema:      "och_source_v1",
			CoveredEventCount: count,
			ThroughSequence:   through,
			SourceDigestHex:   hex.EncodeToString(digest[:]),
			Summary:           "summary text",
		},
	}
}

func readEvents(t *testing.T, store *Store, sessionID string) []domain.RecordedEvent {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(),
		"SELECT payload FROM events WHERE session_id = ? ORDER BY sequence", sessionID)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	var records []domain.RecordedEvent
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan payload: %v", err)
		}
		record, err := domain.UnmarshalRecordedEvent(payload)
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	return records
}

// TestMigration5CreatesContextCheckpointHeadsIdempotently exercises the
// same fresh-open/reopen shape as TestUserVersionTracksLatestMigration and
// TestReopenRunsNoMigration, specifically for migration 5: it applies once
// on a fresh database and does not reapply on reopen.
func TestMigration5CreatesContextCheckpointHeadsIdempotently(t *testing.T) {
	config := tempStoreConfig(t)
	store := openStore(t, config)
	ctx := context.Background()

	var userVersion int
	if err := store.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if userVersion != latestMigrationVersion || latestMigrationVersion < 5 {
		t.Fatalf("user_version = %d, want %d (>= 5)", userVersion, latestMigrationVersion)
	}
	if got := tableCount(t, store, "context_checkpoint_heads"); got != 0 {
		t.Fatalf("context_checkpoint_heads rows on fresh open = %d, want 0", got)
	}
	var migrationCount int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE version = 5").Scan(&migrationCount); err != nil {
		t.Fatalf("count migration 5 history: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("schema_migrations rows for version 5 = %d, want 1", migrationCount)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := openStore(t, config)
	if err := reopened.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE version = 5").Scan(&migrationCount); err != nil {
		t.Fatalf("count migration 5 history after reopen: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("schema_migrations rows for version 5 after reopen = %d, want 1 (not reapplied)", migrationCount)
	}
}

// TestUpdateContextCheckpointHeadAcceptsVerifiedCompletion covers the happy
// path: a ContextCompactionCompleted event whose checkpoint's claimed digest
// genuinely matches the canonical events it covers is accepted, the row is
// written in the SAME transaction as the event, and a later
// LoadLatestContextCheckpoint agrees with what was written.
func TestUpdateContextCheckpointHeadAcceptsVerifiedCompletion(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	sessionID := domain.SessionID("session-checkpoint-ok")
	mustAppend(t, store, appendRequest("append-open", sessionID, 0, "command-open",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"}))

	records := readEvents(t, store, string(sessionID))
	through := records[len(records)-1].Sequence
	completed := validCheckpoint(t, "checkpoint-1", records, through)

	receipt := mustAppend(t, store, appendRequest("append-checkpoint", sessionID, uint64(len(records)), "command-checkpoint",
		domain.ContextCompactionStarted{ID: completed.ID, Trigger: "manual", Strategy: domain.ContextStrategySummary, MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1"},
		completed))

	if got := tableCount(t, store, "context_checkpoint_heads"); got != 1 {
		t.Fatalf("context_checkpoint_heads rows = %d, want 1", got)
	}
	var checkpointID string
	var coveredThrough, updatedAtPosition uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT checkpoint_id, covered_through_sequence, updated_at_commit_position FROM context_checkpoint_heads WHERE session_id = ?",
		string(sessionID)).Scan(&checkpointID, &coveredThrough, &updatedAtPosition); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if checkpointID != "checkpoint-1" || coveredThrough != through || updatedAtPosition != receipt.CommitPosition {
		t.Fatalf("row = id %q through %d position %d, want checkpoint-1/%d/%d",
			checkpointID, coveredThrough, updatedAtPosition, through, receipt.CommitPosition)
	}

	lookup, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("LoadLatestContextCheckpoint: %v", err)
	}
	if lookup.Status != application.ContextCheckpointLookupFound {
		t.Fatalf("lookup status = %v, want Found", lookup.Status)
	}
	if lookup.Checkpoint.ID != "checkpoint-1" || lookup.Checkpoint.ThroughSequence != through {
		t.Fatalf("lookup checkpoint = %+v", lookup.Checkpoint)
	}
}

// TestUpdateContextCheckpointHeadRejectsMismatchedDigestAndRollsBackWholeAppend
// is the core corruption-boundary test the plan requires: a
// ContextCompactionCompleted event whose claimed SourceDigestHex does NOT
// match the canonical events it says it covers must be rejected -- and
// rejected atomically, so neither the completion event nor any other event
// in the same batch, nor the context_checkpoint_heads row, is left
// committed.
func TestUpdateContextCheckpointHeadRejectsMismatchedDigestAndRollsBackWholeAppend(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	sessionID := domain.SessionID("session-checkpoint-bad")
	mustAppend(t, store, appendRequest("append-open", sessionID, 0, "command-open",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"}))
	beforeEvents := tableCount(t, store, "events")
	beforeAppends := tableCount(t, store, "event_appends")

	records := readEvents(t, store, string(sessionID))
	through := records[len(records)-1].Sequence
	completed := validCheckpoint(t, "checkpoint-bad", records, through)
	completed.Checkpoint.SourceDigestHex = hex.EncodeToString([]byte("0123456789012345678901234567890123456789012345678901234567890123456789"[:32]))

	_, err := store.Append(context.Background(), appendRequest("append-checkpoint-bad", sessionID, uint64(len(records)), "command-checkpoint-bad",
		domain.ContextCompactionStarted{ID: completed.ID, Trigger: "manual", Strategy: domain.ContextStrategySummary, MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1"},
		completed))
	requireStoreCode(t, err, application.StoreCodeCorrupt)

	if got := tableCount(t, store, "events"); got != beforeEvents {
		t.Fatalf("events rows = %d, want %d (rejected batch must not commit)", got, beforeEvents)
	}
	if got := tableCount(t, store, "event_appends"); got != beforeAppends {
		t.Fatalf("event_appends rows = %d, want %d", got, beforeAppends)
	}
	if got := tableCount(t, store, "context_checkpoint_heads"); got != 0 {
		t.Fatalf("context_checkpoint_heads rows = %d, want 0", got)
	}

	lookup, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("LoadLatestContextCheckpoint: %v", err)
	}
	if lookup.Status != application.ContextCheckpointLookupNone {
		t.Fatalf("lookup status = %v, want None", lookup.Status)
	}
}

// TestUpdateContextCheckpointHeadRejectsBackwardCoverage covers a second
// corruption shape: a completion that claims a ThroughSequence behind the
// session's own already-recorded checkpoint head.
func TestUpdateContextCheckpointHeadRejectsBackwardCoverage(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	sessionID := domain.SessionID("session-checkpoint-backward")
	mustAppend(t, store, appendRequest("append-open", sessionID, 0, "command-open",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"},
		domain.TurnStarted{TurnID: "turn-2", Input: "hi again"},
		domain.TurnCompleted{TurnID: "turn-2"}))

	records := readEvents(t, store, string(sessionID))
	firstThrough := records[2].Sequence // through turn-1's TurnCompleted
	firstCompleted := validCheckpoint(t, "checkpoint-first", records, firstThrough)
	mustAppend(t, store, appendRequest("append-checkpoint-first", sessionID, uint64(len(records)), "command-checkpoint-first",
		domain.ContextCompactionStarted{ID: firstCompleted.ID, Trigger: "manual", Strategy: domain.ContextStrategySummary, MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1"},
		firstCompleted))

	afterFirst := readEvents(t, store, string(sessionID))
	backwardThrough := records[1].Sequence // before firstThrough: moves coverage backward
	backwardCompleted := validCheckpoint(t, "checkpoint-backward", records, backwardThrough)

	_, err := store.Append(context.Background(), appendRequest("append-checkpoint-backward", sessionID, uint64(len(afterFirst)), "command-checkpoint-backward",
		domain.ContextCompactionStarted{ID: backwardCompleted.ID, Trigger: "manual", Strategy: domain.ContextStrategySummary, MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1"},
		backwardCompleted))
	requireStoreCode(t, err, application.StoreCodeCorrupt)

	var checkpointID string
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT checkpoint_id FROM context_checkpoint_heads WHERE session_id = ?", string(sessionID)).Scan(&checkpointID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if checkpointID != "checkpoint-first" {
		t.Fatalf("checkpoint_id = %q, want checkpoint-first (backward attempt must not overwrite it)", checkpointID)
	}
}

// TestUpdateContextCheckpointHeadFaultBeforeCommitRollsBackEventAndRow uses
// the store's existing fault-injection hook (already exercised by
// append_test.go's own commit-outcome tests) to prove the checkpoint row
// and its triggering event share one atomic commit: a fault injected right
// before COMMIT must leave neither behind.
func TestUpdateContextCheckpointHeadFaultBeforeCommitRollsBackEventAndRow(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	sessionID := domain.SessionID("session-checkpoint-fault")
	mustAppend(t, store, appendRequest("append-open", sessionID, 0, "command-open",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"}))

	records := readEvents(t, store, string(sessionID))
	through := records[len(records)-1].Sequence
	completed := validCheckpoint(t, "checkpoint-fault", records, through)

	store.FailNext(faultBeforeCommit, errTestFault)
	_, err := store.Append(context.Background(), appendRequest("append-checkpoint-fault", sessionID, uint64(len(records)), "command-checkpoint-fault",
		domain.ContextCompactionStarted{ID: completed.ID, Trigger: "manual", Strategy: domain.ContextStrategySummary, MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1"},
		completed))
	if err == nil {
		t.Fatal("Append with injected pre-commit fault = nil error, want error")
	}

	if got := tableCount(t, store, "events"); got != len(records) {
		t.Fatalf("events rows = %d, want %d (faulted batch must not commit)", got, len(records))
	}
	if got := tableCount(t, store, "context_checkpoint_heads"); got != 0 {
		t.Fatalf("context_checkpoint_heads rows = %d, want 0", got)
	}
}
