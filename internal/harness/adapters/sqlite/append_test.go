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
