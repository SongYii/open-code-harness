package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"

	sqlite3 "modernc.org/sqlite/lib"
)

var testTime = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

func appendRequest(appendID domain.AppendID, sessionID domain.SessionID, expected uint64, commandID domain.CommandID, events ...domain.Event) application.AppendRequest {
	request := application.AppendRequest{
		AppendID:        appendID,
		SessionID:       sessionID,
		ExpectedVersion: expected,
		CommandID:       commandID,
		Authority:       application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1},
		Events:          make([]application.ProposedEvent, len(events)),
	}
	for i, event := range events {
		request.Events[i] = application.ProposedEvent{
			ID:            domain.EventID(fmt.Sprintf("event-%s-%d", appendID, i)),
			SchemaVersion: 1,
			OccurredAt:    testTime,
			Event:         event,
		}
	}
	return request
}

func admission(request application.AppendRequest, requestID string, turnID domain.TurnID, itemID domain.ItemID) application.AppendRequest {
	digest, err := application.DigestRunTurnRequestV1(request.SessionID, "input")
	if err != nil {
		panic(err)
	}
	request.Admission = &application.CommandAdmission{
		RunTurnRequestID: domain.RunTurnRequestID(requestID),
		RequestDigest:    digest,
		TurnID:           turnID,
		ItemID:           itemID,
	}
	return request
}

func mustAppend(t *testing.T, store *Store, request application.AppendRequest) application.CommitReceipt {
	t.Helper()
	receipt, err := store.Append(context.Background(), request)
	if err != nil {
		t.Fatalf("Append(%s) error = %v", request.AppendID, err)
	}
	return receipt
}

func tableCount(t *testing.T, store *Store, table string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestAppendCommitsAtomicBatch(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	request := appendRequest("append-1", "session-1", 0, "command-1",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"})

	receipt := mustAppend(t, store, request)
	if receipt.CommitPosition != 1 || receipt.FirstSequence != 1 || receipt.LastSequence != 3 {
		t.Fatalf("receipt = %+v, want position 1 sequences 1..3", receipt)
	}
	if got := tableCount(t, store, "events"); got != 3 {
		t.Fatalf("events rows = %d, want 3", got)
	}
	if got := tableCount(t, store, "event_appends"); got != 1 {
		t.Fatalf("event_appends rows = %d, want 1", got)
	}
	var version uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT version FROM event_streams WHERE session_id = 'session-1'").Scan(&version); err != nil {
		t.Fatalf("read stream version: %v", err)
	}
	if version != 3 {
		t.Fatalf("stream version = %d, want 3", version)
	}
	var head uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&head); err != nil {
		t.Fatalf("read head position: %v", err)
	}
	if head != 1 {
		t.Fatalf("head_commit_position = %d, want 1", head)
	}

	rows, err := store.db.QueryContext(context.Background(),
		"SELECT payload FROM events WHERE session_id = 'session-1' ORDER BY sequence")
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	order := 0
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan payload: %v", err)
		}
		record, err := domain.UnmarshalRecordedEvent(payload)
		if err != nil {
			t.Fatalf("unmarshal record: %v", err)
		}
		order++
		if record.Sequence != uint64(order) {
			t.Fatalf("record sequence = %d, want %d", record.Sequence, order)
		}
		if record.SessionID != domain.SessionID("session-1") || record.CommandID != domain.CommandID("command-1") {
			t.Fatalf("record identity = %s/%s", record.SessionID, record.CommandID)
		}
		if !record.OccurredAt.Equal(testTime) {
			t.Fatalf("record occurred_at = %v, want %v", record.OccurredAt, testTime)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate events: %v", err)
	}

	var turnCount int
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM domain_identities WHERE identity_kind = 'turn'").Scan(&turnCount); err != nil {
		t.Fatalf("count turn identities: %v", err)
	}
	if turnCount != 1 {
		t.Fatalf("turn identities = %d, want 1", turnCount)
	}

	var status string
	var activeTurn sql.NullString
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT status, active_turn_id FROM session_heads WHERE session_id = 'session-1'").Scan(&status, &activeTurn); err != nil {
		t.Fatalf("read session head: %v", err)
	}
	if status != "idle" || activeTurn.Valid {
		t.Fatalf("session head = %s/%v, want idle with no active turn", status, activeTurn)
	}
}

func TestAppendSessionHeadTracksWorkspaceRunningAndDeleted(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-open", "session-h", 0, "command-h",
		domain.SessionCreated{WorkspaceRoot: "/w/."},
		domain.TurnStarted{TurnID: "turn-h", Input: "hi"}))

	var workspaceRoot string
	var status string
	var activeTurn sql.NullString
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT workspace_root, status, active_turn_id FROM session_heads WHERE session_id = 'session-h'").Scan(&workspaceRoot, &status, &activeTurn); err != nil {
		t.Fatalf("read session head: %v", err)
	}
	if workspaceRoot != "/w" || status != "running" || !activeTurn.Valid || activeTurn.String != "turn-h" {
		t.Fatalf("session head = %s/%s/%v, want /w/running/turn-h", workspaceRoot, status, activeTurn)
	}

	mustAppend(t, store, appendRequest("append-stop", "session-h", 2, "command-stop",
		domain.TurnCompleted{TurnID: "turn-h"}))
	mustAppend(t, store, appendRequest("append-delete", "session-h", 3, "command-delete",
		domain.SessionDeleted{}))
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT workspace_root, status, active_turn_id FROM session_heads WHERE session_id = 'session-h'").Scan(&workspaceRoot, &status, &activeTurn); err != nil {
		t.Fatalf("read deleted session head: %v", err)
	}
	if workspaceRoot != "/w" || status != "deleted" || activeTurn.Valid {
		t.Fatalf("deleted session head = %s/%s/%v, want /w/deleted/NULL", workspaceRoot, status, activeTurn)
	}
}

func TestAppendExactRetryAfterAdvance(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	first := appendRequest("append-a", "session-r", 0, "command-a", domain.SessionCreated{WorkspaceRoot: "/w"})
	receiptA := mustAppend(t, store, first)

	second := appendRequest("append-b", "session-r", 1, "command-b", domain.TurnStarted{TurnID: "turn-r", Input: "x"})
	mustAppend(t, store, second)

	// Exact retry returns the original receipt even though the stream moved.
	retry, err := store.Append(context.Background(), first)
	if err != nil {
		t.Fatalf("exact retry error = %v", err)
	}
	if retry != receiptA {
		t.Fatalf("retry receipt = %+v, want original %+v", retry, receiptA)
	}
	if got := tableCount(t, store, "event_appends"); got != 2 {
		t.Fatalf("event_appends rows = %d, want 2", got)
	}
	if got := tableCount(t, store, "events"); got != 2 {
		t.Fatalf("events rows = %d, want 2", got)
	}

	// Same AppendID with a different digest never commits.
	mutated := appendRequest("append-a", "session-r", 0, "command-a", domain.SessionCreated{WorkspaceRoot: "/different"})
	_, err = store.Append(context.Background(), mutated)
	requireStoreCode(t, err, application.StoreCodeAppendIdentityMismatch)
	if got := tableCount(t, store, "event_appends"); got != 2 {
		t.Fatalf("event_appends rows after mismatch = %d, want 2", got)
	}
}

func TestAppendVersionConflictRollsBackEveryIndex(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	request := appendRequest("append-stale", "session-cas", 5, "command-stale",
		domain.SessionCreated{WorkspaceRoot: "/w"})

	_, err := store.Append(context.Background(), request)
	requireStoreCode(t, err, application.StoreCodeVersionConflict)
	var storeErr *application.StoreError
	if !errors.As(err, &storeErr) || storeErr.ActualVersion != 0 || storeErr.ExpectedVersion != 5 {
		t.Fatalf("error = %v, want version conflict 5 vs 0", err)
	}
	for _, table := range []string{"events", "event_appends", "event_streams", "command_requests", "domain_identities", "session_heads"} {
		if got := tableCount(t, store, table); got != 0 {
			t.Fatalf("%s rows = %d after rolled-back append, want 0", table, got)
		}
	}
	var head uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&head); err != nil {
		t.Fatalf("read head: %v", err)
	}
	if head != 0 {
		t.Fatalf("head_commit_position = %d, want 0", head)
	}
}

// TestAppendSessionHeadCorruptionRollsBackWholeCanonicalAppend proves that a
// synchronous session_heads projection failure does not just surface
// StoreCodeCorrupt: the whole canonical append that triggered it — including
// events in the same batch that would have inserted cleanly on their own —
// rolls back completely, leaving every table exactly as it was before the
// attempt.
func TestAppendSessionHeadCorruptionRollsBackWholeCanonicalAppend(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-open", "session-corrupt", 0, "command-open",
		domain.SessionCreated{WorkspaceRoot: "/w"}))

	beforeVersion, beforeHeadStatus, beforeHeadPosition, beforeStoreHead := readSessionCorruptSnapshot(t, store)

	// The batch's first event (turn.started) would insert and project
	// cleanly on its own. The second — a duplicate session.created — is what
	// applyHeadTransition rejects, since the head's workspace root is
	// already set. Both events already have canonical event/append rows
	// written by the time updateSessionHead runs and fails.
	_, err := store.Append(context.Background(), appendRequest("append-corrupt", "session-corrupt", 1, "command-corrupt",
		domain.TurnStarted{TurnID: "turn-corrupt", Input: "hi"},
		domain.SessionCreated{WorkspaceRoot: "/w"}))
	requireStoreCode(t, err, application.StoreCodeCorrupt)

	afterVersion, afterHeadStatus, afterHeadPosition, afterStoreHead := readSessionCorruptSnapshot(t, store)
	if afterVersion != beforeVersion || afterHeadStatus != beforeHeadStatus ||
		afterHeadPosition != beforeHeadPosition || afterStoreHead != beforeStoreHead {
		t.Fatalf("session-corrupt state changed after a rolled-back corrupt append: "+
			"version %d->%d, head status %q->%q, head position %d->%d, store head %d->%d",
			beforeVersion, afterVersion, beforeHeadStatus, afterHeadStatus,
			beforeHeadPosition, afterHeadPosition, beforeStoreHead, afterStoreHead)
	}
	if got := tableCount(t, store, "event_appends"); got != 1 {
		t.Fatalf("event_appends rows = %d, want 1 (only the first, successful append)", got)
	}
	if got := tableCount(t, store, "events"); got != 1 {
		t.Fatalf("events rows = %d, want 1 (only the first, successful append); the corrupt batch's own turn.started row must not survive", got)
	}
	if got := tableCount(t, store, "event_streams"); got != 1 {
		t.Fatalf("event_streams rows = %d, want 1", got)
	}
}

// TestAppendRejectsMissingSessionHeadForExistingStream catches rebuilding an
// existing stream's derived head from only the new append. Missing derived
// state is corruption; the canonical append must remain wholly absent.
func TestAppendRejectsMissingSessionHeadForExistingStream(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-head-open", "session-head-missing", 0, "command-head-open",
		domain.SessionCreated{WorkspaceRoot: "/w"}))
	if _, err := store.db.ExecContext(context.Background(),
		"DELETE FROM session_heads WHERE session_id = 'session-head-missing'"); err != nil {
		t.Fatalf("delete derived head: %v", err)
	}

	_, err := store.Append(context.Background(), appendRequest(
		"append-head-after-missing", "session-head-missing", 1, "command-head-after-missing",
		domain.TurnStarted{TurnID: "turn-head-missing", Input: "hi"}))
	requireStoreCode(t, err, application.StoreCodeCorrupt)

	if got := tableCount(t, store, "events"); got != 1 {
		t.Fatalf("events rows after rejected append = %d, want 1", got)
	}
	if got := tableCount(t, store, "event_appends"); got != 1 {
		t.Fatalf("event_appends rows after rejected append = %d, want 1", got)
	}
	var version, storeHead uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT version FROM event_streams WHERE session_id = 'session-head-missing'").Scan(&version); err != nil {
		t.Fatalf("read stream version: %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&storeHead); err != nil {
		t.Fatalf("read store head: %v", err)
	}
	if version != 1 || storeHead != 1 || tableCount(t, store, "session_heads") != 0 {
		t.Fatalf("state after rejected append = version %d/store head %d/head rows %d, want 1/1/0",
			version, storeHead, tableCount(t, store, "session_heads"))
	}
}

// TestAppendRejectsMissingHeadForExistingVersionZeroStream catches using
// version zero as a proxy for row absence. The schema permits a corrupt,
// pre-existing zero-version stream row, which append must not silently repair.
func TestAppendRejectsMissingHeadForExistingVersionZeroStream(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	if _, err := store.db.ExecContext(context.Background(),
		"INSERT INTO event_streams (session_id, version, created_at_commit_position, last_append_commit_position) VALUES ('session-head-zero', 0, 1, 1)"); err != nil {
		t.Fatalf("insert zero-version stream: %v", err)
	}

	_, err := store.Append(context.Background(), appendRequest(
		"append-head-zero", "session-head-zero", 0, "command-head-zero",
		domain.SessionCreated{WorkspaceRoot: "/w"}))
	requireStoreCode(t, err, application.StoreCodeCorrupt)

	var version, storeHead uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT version FROM event_streams WHERE session_id = 'session-head-zero'").Scan(&version); err != nil {
		t.Fatalf("read zero-version stream: %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&storeHead); err != nil {
		t.Fatalf("read store head: %v", err)
	}
	if version != 0 || storeHead != 0 || tableCount(t, store, "events") != 0 ||
		tableCount(t, store, "event_appends") != 0 || tableCount(t, store, "session_heads") != 0 {
		t.Fatalf("state after rejected append = version %d/store head %d/events %d/appends %d/heads %d, want 0/0/0/0/0",
			version, storeHead, tableCount(t, store, "events"), tableCount(t, store, "event_appends"), tableCount(t, store, "session_heads"))
	}
}

// TestAppendRejectsStaleSessionHeadPosition catches advancing a derived head
// whose position does not name the canonical stream's previous append.
func TestAppendRejectsStaleSessionHeadPosition(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-stale-head-open", "session-head-stale", 0, "command-stale-head-open",
		domain.SessionCreated{WorkspaceRoot: "/w"}))
	mustAppend(t, store, appendRequest("append-stale-head-run", "session-head-stale", 1, "command-stale-head-run",
		domain.TurnStarted{TurnID: "turn-head-stale", Input: "hi"}))
	if _, err := store.db.ExecContext(context.Background(),
		"UPDATE session_heads SET updated_at_commit_position = 1 WHERE session_id = 'session-head-stale'"); err != nil {
		t.Fatalf("stale derived head position: %v", err)
	}

	_, err := store.Append(context.Background(), appendRequest(
		"append-after-stale-head", "session-head-stale", 2, "command-after-stale-head",
		domain.TurnCompleted{TurnID: "turn-head-stale"}))
	requireStoreCode(t, err, application.StoreCodeCorrupt)

	var version, headPosition, storeHead uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT version FROM event_streams WHERE session_id = 'session-head-stale'").Scan(&version); err != nil {
		t.Fatalf("read stream version: %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT updated_at_commit_position FROM session_heads WHERE session_id = 'session-head-stale'").Scan(&headPosition); err != nil {
		t.Fatalf("read derived head position: %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&storeHead); err != nil {
		t.Fatalf("read store head: %v", err)
	}
	if version != 2 || headPosition != 1 || storeHead != 2 ||
		tableCount(t, store, "events") != 2 || tableCount(t, store, "event_appends") != 2 {
		t.Fatalf("state after rejected append = version %d/head position %d/store head %d/events %d/appends %d, want 2/1/2/2/2",
			version, headPosition, storeHead, tableCount(t, store, "events"), tableCount(t, store, "event_appends"))
	}
}

func readSessionCorruptSnapshot(t *testing.T, store *Store) (version uint64, headStatus string, headPosition uint64, storeHead uint64) {
	t.Helper()
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT version FROM event_streams WHERE session_id = 'session-corrupt'").Scan(&version); err != nil {
		t.Fatalf("read event_streams: %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT status, updated_at_commit_position FROM session_heads WHERE session_id = 'session-corrupt'").Scan(&headStatus, &headPosition); err != nil {
		t.Fatalf("read session_heads: %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&storeHead); err != nil {
		t.Fatalf("read store_metadata: %v", err)
	}
	return version, headStatus, headPosition, storeHead
}

func TestAppendAdmissionIdentity(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	first := admission(appendRequest("append-adm", "session-adm", 0, "command-adm",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-adm", Input: "x"},
		domain.AssistantMessageStarted{TurnID: "turn-adm", ItemID: "item-adm"}),
		"request-adm", "turn-adm", "item-adm")
	mustAppend(t, store, first)

	var requestID, admissionAppend string
	err := store.db.QueryRowContext(context.Background(),
		"SELECT run_turn_request_id, admission_append_id FROM command_requests WHERE session_id = 'session-adm'").Scan(&requestID, &admissionAppend)
	if err != nil {
		t.Fatalf("read command request: %v", err)
	}
	if requestID != "request-adm" || admissionAppend != "append-adm" {
		t.Fatalf("command request = %s/%s", requestID, admissionAppend)
	}

	conflict := admission(appendRequest("append-adm2", "session-adm", 3, "command-adm2",
		domain.TurnStarted{TurnID: "turn-adm2", Input: "y"},
		domain.AssistantMessageStarted{TurnID: "turn-adm2", ItemID: "item-adm2"}),
		"request-adm", "turn-adm", "item-adm")
	_, err = store.Append(context.Background(), conflict)
	requireStoreCode(t, err, application.StoreCodeCommandRequestConflict)

	mismatch := admission(appendRequest("append-adm3", "session-adm", 3, "command-adm3",
		domain.TurnStarted{TurnID: "turn-adm3", Input: "z"},
		domain.AssistantMessageStarted{TurnID: "turn-adm3", ItemID: "item-adm3"}),
		"request-adm", "turn-adm", "item-adm")
	different, err := application.DigestRunTurnRequestV1("session-adm", "different input")
	if err != nil {
		t.Fatal(err)
	}
	mismatch.Admission.RequestDigest = different
	_, err = store.Append(context.Background(), mismatch)
	requireStoreCode(t, err, application.StoreCodeCommandIdentityMismatch)

	if got := tableCount(t, store, "command_requests"); got != 1 {
		t.Fatalf("command_requests rows = %d, want 1", got)
	}
}

func TestAppendAdmissionRequiresBatchIdentities(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	request := admission(appendRequest("append-miss", "session-miss", 0, "command-miss",
		domain.SessionCreated{WorkspaceRoot: "/w"}),
		"request-miss", "turn-miss", "item-miss")
	_, err := store.Append(context.Background(), request)
	requireStoreCode(t, err, application.StoreCodeInvalidAppend)
}

func TestAppendDomainIdentityConflict(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-dup1", "session-dup", 0, "command-dup1",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-dup", Input: "x"},
		domain.TurnCompleted{TurnID: "turn-dup"}))

	repeat := appendRequest("append-dup2", "session-dup", 3, "command-dup2",
		domain.TurnStarted{TurnID: "turn-dup", Input: "again"})
	_, err := store.Append(context.Background(), repeat)
	requireStoreCode(t, err, application.StoreCodeDomainIdentityConflict)
	var storeErr *application.StoreError
	if !errors.As(err, &storeErr) || storeErr.IdentityKind != "turn" {
		t.Fatalf("error = %v, want turn identity kind", err)
	}
	if got := tableCount(t, store, "events"); got != 3 {
		t.Fatalf("events rows = %d, want 3", got)
	}

	// In-batch duplicate identities are rejected the same way.
	inBatch := appendRequest("append-dup3", "session-dup", 3, "command-dup3",
		domain.TurnStarted{TurnID: "turn-b1", Input: "a"},
		domain.TurnStarted{TurnID: "turn-b1", Input: "b"})
	_, err = store.Append(context.Background(), inBatch)
	requireStoreCode(t, err, application.StoreCodeDomainIdentityConflict)
}

func TestAppendRejectsDuplicateEventID(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	first := appendRequest("append-ev", "session-ev", 0, "command-ev", domain.SessionCreated{WorkspaceRoot: "/w"})
	mustAppend(t, store, first)

	reused := first
	reused.AppendID = "append-ev2"
	reused.ExpectedVersion = 1
	reused.CommandID = "command-ev2"
	_, err := store.Append(context.Background(), reused)
	requireStoreCode(t, err, application.StoreCodeInvalidAppend)
}

func TestAppendRejectsOverLimitBatch(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	events := make([]domain.Event, 65)
	for i := range events {
		events[i] = domain.TurnStarted{TurnID: domain.TurnID(fmt.Sprintf("turn-lim-%d", i)), Input: "x"}
	}
	request := appendRequest("append-lim", "session-lim", 0, "command-lim", events...)
	_, err := store.Append(context.Background(), request)
	requireStoreCode(t, err, application.StoreCodeInvalidAppend)
}

func TestAppendWriterFencedWhenAuthorityDisagrees(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	ctx := context.Background()

	request := appendRequest("append-fenced", "session-f", 0, "command-f", domain.SessionCreated{WorkspaceRoot: "/w"})
	request.Authority = application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 2}
	_, err := store.Append(ctx, request)
	requireStoreCode(t, err, application.StoreCodeWriterFenced)

	request.Authority = application.WriterAuthority{RuntimeID: "runtime-2", FencingToken: 1}
	_, err = store.Append(ctx, request)
	requireStoreCode(t, err, application.StoreCodeWriterFenced)

	request.Authority = store.Authority()
	mustAppend(t, store, request)
}

func TestClassifySQLiteResultCodes(t *testing.T) {
	tests := []struct {
		code int
		want application.StoreErrorCode
	}{
		{sqlite3.SQLITE_BUSY, application.StoreCodeUnavailable},
		{sqlite3.SQLITE_BUSY | (1 << 8), application.StoreCodeUnavailable},
		{sqlite3.SQLITE_LOCKED, application.StoreCodeUnavailable},
		{sqlite3.SQLITE_FULL, application.StoreCodeUnavailable},
		{sqlite3.SQLITE_IOERR, application.StoreCodeUnavailable},
		{sqlite3.SQLITE_IOERR | (3 << 8), application.StoreCodeUnavailable},
		{sqlite3.SQLITE_INTERRUPT, application.StoreCodeUnavailable},
		{sqlite3.SQLITE_READONLY, application.StoreCodeUnavailable},
		{sqlite3.SQLITE_CANTOPEN, application.StoreCodeUnavailable},
		{sqlite3.SQLITE_CORRUPT, application.StoreCodeCorrupt},
		{sqlite3.SQLITE_NOTADB, application.StoreCodeCorrupt},
		{sqlite3.SQLITE_CONSTRAINT, application.StoreCodeCorrupt},
		{sqlite3.SQLITE_MISMATCH, application.StoreCodeCorrupt},
		{sqlite3.SQLITE_INTERNAL, application.StoreCodeCorrupt},
	}
	for _, test := range tests {
		if got := classifyCode(test.code); got != test.want {
			t.Fatalf("classifyCode(%#x) = %q, want %q", test.code, got, test.want)
		}
	}
}

func requireStoreCode(t *testing.T, err error, code application.StoreErrorCode) {
	t.Helper()
	if !application.IsStoreCode(err, code) {
		t.Fatalf("error = %v, want store code %q", err, code)
	}
	var storeErr *application.StoreError
	if !errors.As(err, &storeErr) {
		t.Fatalf("error = %v, want *application.StoreError", err)
	}
	if storeErr.MayHaveCommitted != (code == application.StoreCodeCommitOutcomeUnknown) {
		t.Fatalf("MayHaveCommitted = %t for %q", storeErr.MayHaveCommitted, code)
	}
}
