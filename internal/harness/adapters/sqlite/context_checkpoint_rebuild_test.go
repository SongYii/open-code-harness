package sqlite

import (
	"context"
	"fmt"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func appendValidCheckpoint(t *testing.T, store *Store, sessionID domain.SessionID, checkpointID string) domain.ContextCompactionCompleted {
	t.Helper()
	records := readEvents(t, store, string(sessionID))
	through := records[len(records)-1].Sequence
	completed := validCheckpoint(t, checkpointID, records, through)
	mustAppend(t, store, appendRequest(domain.AppendID("append-"+checkpointID), sessionID, uint64(len(records)), domain.CommandID("command-"+checkpointID),
		domain.ContextCompactionStarted{
			ID: completed.ID, Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
			MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1",
		}, completed))
	return completed
}

// TestRebuildContextCheckpointHeadsRepairsMissingRow covers the case
// distinguishing this rebuild from RebuildAndVerifySessionHeads: a missing
// row is not automatically corruption -- a session with zero compactions
// correctly has none -- but a missing row for a session with a genuinely
// valid checkpoint under canonical events IS repaired (written), not merely
// reported.
func TestRebuildContextCheckpointHeadsRepairsMissingRow(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	sessionID := domain.SessionID("session-rebuild-missing")
	mustAppend(t, store, appendRequest("append-open", sessionID, 0, "command-open",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"}))
	appendValidCheckpoint(t, store, sessionID, "checkpoint-missing")

	if _, err := store.db.ExecContext(context.Background(),
		"DELETE FROM context_checkpoint_heads WHERE session_id = ?", string(sessionID)); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	if got := tableCount(t, store, "context_checkpoint_heads"); got != 0 {
		t.Fatalf("rows after delete = %d, want 0", got)
	}

	if err := store.RebuildAndVerifyContextCheckpointHeads(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	lookup, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("LoadLatestContextCheckpoint: %v", err)
	}
	if lookup.Status != application.ContextCheckpointLookupFound || lookup.Checkpoint.ID != "checkpoint-missing" {
		t.Fatalf("lookup after repair = %+v, want Found checkpoint-missing", lookup)
	}
}

// TestRebuildContextCheckpointHeadsNoOpWithoutAnyCompaction covers a
// session with no compactions at all: the absence of a row is correct, not
// corruption, and rebuild must not fabricate one.
func TestRebuildContextCheckpointHeadsNoOpWithoutAnyCompaction(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-plain", "session-rebuild-plain", 0, "command-plain",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"}))

	if err := store.RebuildAndVerifyContextCheckpointHeads(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := tableCount(t, store, "context_checkpoint_heads"); got != 0 {
		t.Fatalf("rows after rebuild = %d, want 0", got)
	}
}

// TestRebuildContextCheckpointHeadsDetectsDisagreeingRow tampers a stored
// row directly (bypassing the write-time hook) so it disagrees with the
// independently-derived truth: rebuild must report store_corrupt, never
// silently overwrite it.
func TestRebuildContextCheckpointHeadsDetectsDisagreeingRow(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	sessionID := domain.SessionID("session-rebuild-disagree")
	mustAppend(t, store, appendRequest("append-open", sessionID, 0, "command-open",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"}))
	appendValidCheckpoint(t, store, sessionID, "checkpoint-disagree")

	if _, err := store.db.ExecContext(context.Background(),
		"UPDATE context_checkpoint_heads SET covered_through_sequence = covered_through_sequence - 1 WHERE session_id = ?", string(sessionID)); err != nil {
		t.Fatalf("tamper row: %v", err)
	}

	err := store.RebuildAndVerifyContextCheckpointHeads(context.Background())
	if err == nil {
		t.Fatal("rebuild on disagreeing row = nil, want corruption")
	}
	requireStoreCode(t, err, application.StoreCodeCorrupt)
}

// TestRebuildContextCheckpointHeadsSpansMultiplePages proves
// derivePagedContextCheckpointHead's page-boundary arithmetic is correct,
// not merely "happens to work when everything fits in one page": it builds
// a session whose canonical stream is longer than rebuildPageLimit (512),
// with a checkpoint whose own ThroughSequence falls inside a later page,
// then confirms the rebuild still finds and repairs it correctly. This is
// the specific regression risk this task's own paging refactor introduced
// (an off-by-one at a page split would silently under- or over-cover the
// digest range).
func TestRebuildContextCheckpointHeadsSpansMultiplePages(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	sessionID := domain.SessionID("session-rebuild-paged")
	mustAppend(t, store, appendRequest("append-open", sessionID, 0, "command-open",
		domain.SessionCreated{WorkspaceRoot: "/w"}))

	const turnCount = 200 // 200 * 3 events/turn = 600 canonical events, past rebuildPageLimit (512)
	version := uint64(1)
	for i := 0; i < turnCount; i++ {
		turnID := domain.TurnID(fmt.Sprintf("turn-paged-%d", i))
		receipt := mustAppend(t, store, appendRequest(
			domain.AppendID(fmt.Sprintf("append-paged-%d", i)), sessionID, version, domain.CommandID(fmt.Sprintf("command-paged-%d", i)),
			domain.TurnStarted{TurnID: turnID, Input: "hi"},
			domain.AssistantMessageCompleted{TurnID: turnID, ItemID: domain.ItemID(fmt.Sprintf("item-paged-%d", i)), Text: "hello"},
			domain.TurnCompleted{TurnID: turnID},
		))
		version = receipt.LastSequence
	}
	// The trailing event is deliberately a source event (AssistantMessageCompleted,
	// not TurnCompleted): landing the checkpoint's own ThroughSequence boundary on
	// a record IsSourceEvent actually counts makes a page-boundary off-by-one in
	// the digest range provably observable, rather than silently absorbed by a
	// boundary record IsSourceEvent would have skipped anyway.
	mustAppend(t, store, appendRequest("append-paged-trailing", sessionID, version, "command-paged-trailing",
		domain.TurnStarted{TurnID: "turn-paged-trailing", Input: "hi"},
		domain.AssistantMessageCompleted{TurnID: "turn-paged-trailing", ItemID: "item-paged-trailing", Text: "hello"},
	))
	if got := tableCount(t, store, "events"); got <= rebuildPageLimit {
		t.Fatalf("events = %d, want > rebuildPageLimit (%d) to actually exercise a page boundary", got, rebuildPageLimit)
	}

	completed := appendValidCheckpoint(t, store, sessionID, "checkpoint-paged")
	if completed.Checkpoint.ThroughSequence <= rebuildPageLimit {
		t.Fatalf("checkpoint ThroughSequence = %d, want > rebuildPageLimit (%d)", completed.Checkpoint.ThroughSequence, rebuildPageLimit)
	}

	if _, err := store.db.ExecContext(context.Background(),
		"DELETE FROM context_checkpoint_heads WHERE session_id = ?", string(sessionID)); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	if err := store.RebuildAndVerifyContextCheckpointHeads(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	lookup, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("LoadLatestContextCheckpoint: %v", err)
	}
	if lookup.Status != application.ContextCheckpointLookupFound || lookup.Checkpoint.ID != "checkpoint-paged" ||
		lookup.Checkpoint.ThroughSequence != completed.Checkpoint.ThroughSequence {
		t.Fatalf("lookup after paged repair = %+v, want Found checkpoint-paged through %d", lookup, completed.Checkpoint.ThroughSequence)
	}
}

// TestRebuildContextCheckpointHeadsDetectsInvalidUnderlyingEvent tampers
// the canonical completion event itself (not the row) so it no longer
// independently validates: the row still points at a real event of the
// right type (satisfying context_checkpoint_heads's own foreign key), but a
// fresh rebuild finds no furthest-valid checkpoint at all, since the only
// candidate's claimed digest no longer matches canonical events. A row
// surviving that is corruption, not a silently-accepted "nothing to do."
func TestRebuildContextCheckpointHeadsDetectsInvalidUnderlyingEvent(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	sessionID := domain.SessionID("session-rebuild-invalid-event")
	mustAppend(t, store, appendRequest("append-open", sessionID, 0, "command-open",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"}))
	completed := appendValidCheckpoint(t, store, sessionID, "checkpoint-invalid-event")

	records := readEvents(t, store, string(sessionID))
	var eventID domain.EventID
	var sequence uint64
	for _, record := range records {
		if _, ok := record.Event.(domain.ContextCompactionCompleted); ok {
			eventID, sequence = record.ID, record.Sequence
		}
	}
	if eventID == "" {
		t.Fatal("no ContextCompactionCompleted event found")
	}
	tampered := completed
	tampered.Checkpoint.SourceDigestHex = "0000000000000000000000000000000000000000000000000000000000000000"[:64]
	payload, err := domain.MarshalRecordedEvent(domain.RecordedEvent{
		SessionID: sessionID, Sequence: sequence, ID: eventID, CommandID: "command-checkpoint-invalid-event",
		SchemaVersion: 1, OccurredAt: testTime, Event: tampered,
	})
	if err != nil {
		t.Fatalf("marshal tampered event: %v", err)
	}
	if _, err := store.db.ExecContext(context.Background(),
		"UPDATE events SET payload = ? WHERE session_id = ? AND event_id = ?", payload, string(sessionID), string(eventID)); err != nil {
		t.Fatalf("tamper event payload: %v", err)
	}

	err = store.RebuildAndVerifyContextCheckpointHeads(context.Background())
	if err == nil {
		t.Fatal("rebuild with a tampered underlying event but a surviving row = nil, want corruption")
	}
	requireStoreCode(t, err, application.StoreCodeCorrupt)
}
