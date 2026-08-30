package eventstoretest

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func testSessionHeadCatalog(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	requests := []application.AppendRequest{
		v2Append("append-catalog-idle", "session-catalog-idle", 0, "command-catalog-idle", domain.SessionCreated{WorkspaceRoot: "/catalog"}),
		v2Append("append-catalog-running", "session-catalog-running", 0, "command-catalog-running", domain.SessionCreated{WorkspaceRoot: "/catalog"}, domain.TurnStarted{TurnID: "turn-catalog", Input: "hello"}),
		v2Append("append-catalog-closed", "session-catalog-closed", 0, "command-catalog-closed", domain.SessionCreated{WorkspaceRoot: "/catalog"}, domain.SessionClosed{}),
		v2Append("append-catalog-deleted", "session-catalog-deleted", 0, "command-catalog-deleted", domain.SessionCreated{WorkspaceRoot: "/catalog"}, domain.SessionDeleted{}),
		v2Append("append-catalog-foreign", "session-catalog-foreign", 0, "command-catalog-foreign", domain.SessionCreated{WorkspaceRoot: "/foreign"}),
	}
	for _, request := range requests {
		if _, err := h.Store.Append(context.Background(), request); err != nil {
			t.Fatalf("Append(%q) error = %v", request.SessionID, err)
		}
	}
	first, err := h.Store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/catalog", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := []domain.SessionID{"session-catalog-closed", "session-catalog-running"}
	if got := headIDs(first.Sessions); !reflect.DeepEqual(got, wantFirst) || first.NextCursor == "" {
		t.Fatalf("first page = %#v, want IDs %v and cursor", first, wantFirst)
	}
	if first.Sessions[0].Status != application.SessionHeadStatusClosed || first.Sessions[1].Status != application.SessionHeadStatusRunning {
		t.Fatalf("first statuses = %q/%q", first.Sessions[0].Status, first.Sessions[1].Status)
	}
	for _, head := range first.Sessions {
		if head.WorkspaceRoot != "/catalog" || head.UpdatedAt.IsZero() || head.UpdatedAt.Location() != time.UTC {
			t.Fatalf("invalid head = %#v", head)
		}
	}
	second, err := h.Store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/catalog", Cursor: first.NextCursor, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	wantSecond := []domain.SessionID{"session-catalog-idle"}
	if got := headIDs(second.Sessions); !reflect.DeepEqual(got, wantSecond) || second.NextCursor != "" {
		t.Fatalf("second page = %#v, want IDs %v without cursor", second, wantSecond)
	}
	first.Sessions[0].WorkspaceRoot = "/mutated"
	again, err := h.Store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/catalog", Limit: 2})
	if err != nil || again.Sessions[0].WorkspaceRoot != "/catalog" {
		t.Fatalf("catalog result aliases storage: %#v, %v", again, err)
	}

	invalidCursors := []string{
		"%%%",
		strings.Repeat("a", 513),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":1,"s":"session-catalog-idle","extra":true}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":1}`)),
		base64.URLEncoding.EncodeToString([]byte(`{"v":1,"p":1,"s":"session-catalog-idle"}`)),
		"eyJ2IjoxLCJwIjoxLCJzIjoic2Vzc2lvbi1jYXRhbG9nLWlkbGUifR",
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":18446744073709551615,"s":"session-catalog-idle"}`)),
	}
	for _, request := range append([]application.ListSessionHeadsRequest{
		{WorkspaceRoot: "/catalog", Limit: 0},
		{WorkspaceRoot: "/catalog", Limit: 257},
		{WorkspaceRoot: "/catalog/.", Limit: 2},
	}, catalogCursorRequests(invalidCursors)...) {
		if _, err := h.Store.ListSessionHeads(context.Background(), request); err == nil {
			t.Fatalf("ListSessionHeads(%#v) succeeded", request)
		} else {
			requireCode(t, err, application.StoreCodeInvalidRead)
		}
	}
}

func catalogCursorRequests(cursors []string) []application.ListSessionHeadsRequest {
	requests := make([]application.ListSessionHeadsRequest, len(cursors))
	for index, cursor := range cursors {
		requests[index] = application.ListSessionHeadsRequest{WorkspaceRoot: "/catalog", Cursor: cursor, Limit: 2}
	}
	return requests
}

func headIDs(heads []application.SessionHead) []domain.SessionID {
	ids := make([]domain.SessionID, len(heads))
	for index := range heads {
		ids[index] = heads[index].SessionID
	}
	return ids
}

var v2Time = time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

func testAtomicAppendAndCAS(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	first := v2Append("append-cas-1", "session-cas", 0, "command-cas-1", domain.SessionCreated{WorkspaceRoot: "/cas"})
	receipt, err := h.Store.Append(context.Background(), first)
	if err != nil || receipt.FirstSequence != 1 || receipt.LastSequence != 1 || receipt.CommitPosition == 0 {
		t.Fatalf("first Append() = (%#v, %v)", receipt, err)
	}
	loser := v2Append("append-cas-2", "session-cas", 0, "command-cas-2", domain.SessionCreated{WorkspaceRoot: "/other"})
	if _, err := h.Store.Append(context.Background(), loser); err == nil {
		t.Fatal("stale Append() succeeded")
	} else {
		requireCode(t, err, application.StoreCodeVersionConflict)
	}
	page := readAll(t, h.Store, "session-cas")
	if len(page) != 1 || page[0].ID != first.Events[0].ID {
		t.Fatalf("stream after conflict = %#v, want only winner", page)
	}

	duplicate := v2Append("append-duplicate", "session-other", 0, "command-duplicate", domain.SessionCreated{WorkspaceRoot: "/other"})
	duplicate.Events[0].ID = first.Events[0].ID
	if _, err := h.Store.Append(context.Background(), duplicate); err == nil {
		t.Fatal("global duplicate EventID succeeded")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	if got := readAll(t, h.Store, "session-other"); len(got) != 0 {
		t.Fatalf("duplicate EventID leaked batch: %#v", got)
	}
	sameBatch := v2Append("append-same-batch-event", "session-same-batch-event", 0, "command-same-batch-event", domain.SessionCreated{WorkspaceRoot: "/same"}, domain.TurnStarted{TurnID: "turn-same", Input: "same"})
	sameBatch.Events[1].ID = sameBatch.Events[0].ID
	if _, err := h.Store.Append(context.Background(), sameBatch); err == nil {
		t.Fatal("same-batch duplicate EventID succeeded")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	assertAppendAbsent(t, h, sameBatch, 0)
}

func testProposedMetadataPreservation(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	base := time.Date(2027, 3, 4, 5, 6, 7, 890, time.UTC)
	request := v2Append("append-metadata", "session-metadata", 0, "command-metadata", domain.SessionCreated{WorkspaceRoot: "/metadata"}, domain.TurnStarted{TurnID: "turn-metadata", Input: "distinct input"})
	request.Events[0].ID, request.Events[0].OccurredAt = "event-metadata-created", base
	request.Events[1].ID, request.Events[1].OccurredAt = "event-metadata-turn", base.Add(37*time.Nanosecond)
	receipt, err := h.Store.Append(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.FirstSequence != 1 || receipt.LastSequence != 2 || receipt.CommitPosition == 0 {
		t.Fatalf("receipt = %#v", receipt)
	}
	page, err := h.Store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: request.SessionID, Limit: 2})
	if err != nil || len(page.Records) != len(request.Events) {
		t.Fatalf("ReadStream = (%#v, %v)", page, err)
	}
	for i, proposed := range request.Events {
		record := page.Records[i]
		if record.Sequence != uint64(i+1) || record.ID != proposed.ID || record.SchemaVersion != int(proposed.SchemaVersion) || record.OccurredAt != proposed.OccurredAt || record.CommandID != request.CommandID || record.SessionID != request.SessionID || !reflect.DeepEqual(record.Event, proposed.Event) {
			t.Fatalf("record %d = %#v, want preserved proposed metadata %#v", i, record, proposed)
		}
	}
}

func testExactReceiptRetry(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	request := v2Append("append-retry", "session-retry", 0, "command-retry", domain.SessionCreated{WorkspaceRoot: "/retry"})
	first, err := h.Store.Append(context.Background(), request)
	if err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-retry-advance", "session-retry", 1, "command-retry-advance", domain.TurnStarted{TurnID: "turn-retry", Input: "advance"})); err != nil {
		t.Fatalf("advance Append() error = %v", err)
	}
	h.RotateAuthority(application.WriterAuthority{RuntimeID: "runtime-successor", FencingToken: 2})
	retried, err := h.Store.Append(context.Background(), request)
	if err != nil || retried != first {
		t.Fatalf("exact retry = (%#v, %v), want %#v", retried, err, first)
	}
	changed := request
	changed.Events = append([]application.ProposedEvent(nil), request.Events...)
	changed.Events[0].ID = "event-retry-changed"
	if _, err := h.Store.Append(context.Background(), changed); err == nil {
		t.Fatal("same AppendID different immutable request succeeded")
	} else {
		requireCode(t, err, application.StoreCodeAppendIdentityMismatch)
	}
}

func testPinnedPagination(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	if _, err := h.Store.Append(context.Background(), v2Append("append-page-1", "session-page", 0, "command-page-1", domain.SessionCreated{WorkspaceRoot: "/page"}, domain.TurnStarted{TurnID: "turn-page", Input: "next"}, domain.TurnCompleted{TurnID: "turn-page"})); err != nil {
		t.Fatal(err)
	}
	first, err := h.Store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-page", Limit: 1})
	if err != nil || len(first.Records) != 1 || first.HeadVersion != 3 || first.NextAfterSequence != 1 || first.End {
		t.Fatalf("first page = (%#v, %v)", first, err)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-page-2", "session-page", 3, "command-page-2", domain.TurnStarted{TurnID: "turn-page-2", Input: "later"})); err != nil {
		t.Fatal(err)
	}
	second, err := h.Store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-page", AfterSequence: first.NextAfterSequence, HeadVersion: &first.HeadVersion, Limit: 1})
	if err != nil || len(second.Records) != 1 || second.Records[0].Sequence != 2 || second.HeadVersion != 3 || second.NextAfterSequence != 2 || second.End {
		t.Fatalf("pinned second page = (%#v, %v)", second, err)
	}
	third, err := h.Store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-page", AfterSequence: second.NextAfterSequence, HeadVersion: &first.HeadVersion, Limit: 1})
	if err != nil || len(third.Records) != 1 || third.Records[0].Sequence != 3 || third.HeadVersion != 3 || third.NextAfterSequence != 3 || !third.End {
		t.Fatalf("pinned third page = (%#v, %v)", third, err)
	}
	for _, req := range []application.ReadStreamRequest{{SessionID: "session-page", Limit: 0}, {SessionID: "session-page", Limit: 257}, {SessionID: "session-page", AfterSequence: 5, Limit: 1}, {SessionID: "session-page", AfterSequence: 2, HeadVersion: ptr(uint64(1)), Limit: 1}} {
		if _, err := h.Store.ReadStream(context.Background(), req); err == nil {
			t.Fatalf("ReadStream(%#v) succeeded", req)
		} else {
			requireCode(t, err, application.StoreCodeInvalidRead)
		}
	}
}

func testAdmissionIdentity(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	if _, err := h.Store.Append(context.Background(), v2Append("append-admission-session", "session-admission", 0, "command-admission-session", domain.SessionCreated{WorkspaceRoot: "/admission"})); err != nil {
		t.Fatal(err)
	}
	request := v2Append("append-admission", "session-admission", 1, "command-admission", domain.TurnStarted{TurnID: "turn-admission", Input: "hello"}, domain.AssistantMessageStarted{TurnID: "turn-admission", ItemID: "item-admission"})
	digest, err := application.DigestRunTurnRequestV1("session-admission", "hello")
	if err != nil {
		t.Fatal(err)
	}
	request.Admission = &application.CommandAdmission{RunTurnRequestID: "request-admission", RequestDigest: digest, TurnID: "turn-admission", ItemID: "item-admission"}
	if _, err := h.Store.Append(context.Background(), request); err != nil {
		t.Fatalf("admission Append() error = %v", err)
	}
	missingStart := v2Append("append-admission-missing-start", "session-admission", 3, "command-admission-missing-start", domain.TurnCompleted{TurnID: "turn-admission"})
	missingStart.Admission = &application.CommandAdmission{RunTurnRequestID: "request-admission-missing-start", RequestDigest: digest, TurnID: "turn-admission-missing", ItemID: "item-admission-missing"}
	if _, err := h.Store.Append(context.Background(), missingStart); err == nil {
		t.Fatal("admission without its start events succeeded")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	lookup, err := h.Store.FindCommandRequest(context.Background(), application.FindCommandRequestRequest{RunTurnRequestID: "request-admission", SessionID: "session-admission", RequestDigest: digest})
	if err != nil || lookup.Kind != application.CommandRequestLookupFound || lookup.Record == nil || lookup.Record.AdmissionAppendID != request.AppendID {
		t.Fatalf("FindCommandRequest() = (%#v, %v)", lookup, err)
	}
	mismatch, err := h.Store.FindCommandRequest(context.Background(), application.FindCommandRequestRequest{RunTurnRequestID: "request-admission", SessionID: "other-session", RequestDigest: digest})
	if err != nil || mismatch.Kind != application.CommandRequestLookupIdentityMismatch || mismatch.Record != nil {
		t.Fatalf("private mismatch = (%#v, %v)", mismatch, err)
	}
	if text := fmt.Sprint(mismatch, err); strings.Contains(text, "session-admission") {
		t.Fatalf("mismatch leaked stored identity: %s", text)
	}

	duplicate := request
	duplicate.AppendID = "append-admission-other"
	duplicate.ExpectedVersion = 0
	duplicate.SessionID = "other-session"
	duplicate.CommandID = "command-admission-other"
	duplicate.Authority = application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}
	if _, err := h.Store.Append(context.Background(), duplicate); err == nil {
		t.Fatal("global request ID reuse succeeded")
	} else {
		requireCode(t, err, application.StoreCodeCommandRequestConflict)
	}
}

func testWriterFencing(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	h.RotateAuthority(application.WriterAuthority{RuntimeID: "runtime-2", FencingToken: 2})
	request := v2Append("append-fenced", "session-fenced", 0, "command-fenced", domain.SessionCreated{WorkspaceRoot: "/fenced"})
	if _, err := h.Store.Append(context.Background(), request); err == nil {
		t.Fatal("old writer was accepted")
	} else {
		requireCode(t, err, application.StoreCodeWriterFenced)
	}
	request.Authority = application.WriterAuthority{RuntimeID: "runtime-2", FencingToken: 2}
	if _, err := h.Store.Append(context.Background(), request); err != nil {
		t.Fatalf("current writer rejected: %v", err)
	}
}

func testUnknownOutcome(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	request := v2Append("append-unknown", "session-unknown", 0, "command-unknown", domain.SessionCreated{WorkspaceRoot: "/unknown"})
	h.FailNext(FaultBeforeCommit, errors.New("before"))
	if _, err := h.Store.Append(context.Background(), request); err == nil {
		t.Fatal("before-commit fault succeeded")
	} else {
		requireCode(t, err, application.StoreCodeUnavailable)
	}
	if got := readAll(t, h.Store, "session-unknown"); len(got) != 0 {
		t.Fatalf("before-commit fault mutated stream: %#v", got)
	}
	if _, err := h.Store.Append(context.Background(), request); err != nil {
		t.Fatalf("one-shot before-commit fault persisted: %v", err)
	}
	// Start the after-commit case independently so fault exhaustion is observed
	// without conflating it with exact retry.
	request = v2Append("append-unknown-after", "session-unknown-after", 0, "command-unknown-after", domain.SessionCreated{WorkspaceRoot: "/unknown"})
	h.FailNext(FaultAfterCommitBeforeAck, errors.New("after"))
	if _, err := h.Store.Append(context.Background(), request); err == nil {
		t.Fatal("after-commit fault did not report unknown")
	} else {
		requireCode(t, err, application.StoreCodeCommitOutcomeUnknown)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-unknown-after-fresh", "session-unknown-after-fresh", 0, "command-unknown-after-fresh", domain.SessionCreated{WorkspaceRoot: "/fresh"})); err != nil {
		t.Fatalf("after-commit fault was not one-shot: %v", err)
	}
	digest, err := application.DigestAppendRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	h.FailNext(FaultResolve, errors.New("resolve"))
	if _, err := h.Store.ResolveAppend(context.Background(), application.ResolveAppendRequest{AppendID: request.AppendID, RequestDigest: digest}); err == nil {
		t.Fatal("resolve fault succeeded")
	} else {
		requireCode(t, err, application.StoreCodeUnavailable)
	}
	resolved, err := h.Store.ResolveAppend(context.Background(), application.ResolveAppendRequest{AppendID: request.AppendID, RequestDigest: digest})
	if err != nil || resolved.Kind != application.AppendResolutionCommitted || resolved.Receipt == nil {
		t.Fatalf("ResolveAppend = (%#v, %v)", resolved, err)
	}
	retried, err := h.Store.Append(context.Background(), request)
	if err != nil || retried != *resolved.Receipt {
		t.Fatalf("exact retry after unknown = (%#v, %v), want %#v", retried, err, resolved.Receipt)
	}
	if got, err := h.Store.ResolveAppend(context.Background(), application.ResolveAppendRequest{AppendID: "append-never", RequestDigest: digest}); err != nil {
		t.Fatal(err)
	} else if got.Kind != application.AppendResolutionNotFound || got.Receipt != nil {
		t.Fatalf("not found resolution = %#v", got)
	}
	wrong := digest
	wrong[0]++
	if got, err := h.Store.ResolveAppend(context.Background(), application.ResolveAppendRequest{AppendID: request.AppendID, RequestDigest: wrong}); err != nil {
		t.Fatal(err)
	} else if got.Kind != application.AppendResolutionIdentityMismatch || got.Receipt != nil {
		t.Fatalf("mismatch resolution = %#v", got)
	}
}

func testLimitsCopiesCancellationAndCorruption(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Store.Append(canceled, v2Append("append-canceled", "session-canceled", 0, "command-canceled", domain.SessionCreated{WorkspaceRoot: "/canceled"})); err == nil {
		t.Fatal("canceled append succeeded")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	if _, err := h.Store.Append(nil, v2Append("append-nil", "session-nil", 0, "command-nil", domain.SessionCreated{WorkspaceRoot: "/nil"})); err == nil {
		t.Fatal("nil context append succeeded")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	tooMany := v2Append("append-65", "session-65", 0, "command-65", validBatch(65)...)
	if _, err := h.Store.Append(context.Background(), tooMany); err == nil {
		t.Fatal("65-event append succeeded")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	assertAppendAbsent(t, h, tooMany, 0)
	reuseCount := v2Append("append-65-reuse", "session-65", 0, "command-65-reuse", domain.SessionCreated{WorkspaceRoot: "/batch"}, domain.TurnStarted{TurnID: "turn-batch-0", Input: "ok"}, domain.TurnCompleted{TurnID: "turn-batch-0"})
	for i := range reuseCount.Events {
		reuseCount.Events[i].ID = tooMany.Events[i].ID
	}
	if _, err := h.Store.Append(context.Background(), reuseCount); err != nil {
		t.Fatalf("65-event rejection leaked event/turn identities: %v", err)
	}
	maxBatch := v2Append("append-64", "session-64", 0, "command-64", validBatch(64)...)
	if _, err := h.Store.Append(context.Background(), maxBatch); err != nil {
		t.Fatalf("valid 64-event append error = %v", err)
	}
	testByteLimits(t, h)
	request := v2Append("append-copy", "session-copy", 0, "command-copy", domain.SessionCreated{WorkspaceRoot: "/copy"})
	if _, err := h.Store.Append(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	page, err := h.Store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-copy", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	page.Records[0].ID = "mutated"
	again := readAll(t, h.Store, "session-copy")
	if again[0].ID != request.Events[0].ID {
		t.Fatalf("read returned aliased record: %#v", again[0])
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-history-session", "session-history", 0, "command-history-session", domain.SessionCreated{WorkspaceRoot: "/history"})); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-history-turn", "session-history", 1, "command-history-turn", domain.TurnStarted{TurnID: "turn-history", Input: "one"}, domain.TurnCompleted{TurnID: "turn-history"})); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-history-duplicate", "session-history", 3, "command-history-duplicate", domain.TurnStarted{TurnID: "turn-history", Input: "two"})); err == nil {
		t.Fatal("historical TurnID reuse succeeded")
	} else {
		requireCode(t, err, application.StoreCodeDomainIdentityConflict)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-history-item-start", "session-history", 3, "command-history-item-start", domain.TurnStarted{TurnID: "turn-item-history", Input: "item"}, domain.AssistantMessageStarted{TurnID: "turn-item-history", ItemID: "item-history"}, domain.AssistantMessageCompleted{TurnID: "turn-item-history", ItemID: "item-history", Text: "done"}, domain.TurnCompleted{TurnID: "turn-item-history"})); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-history-item-duplicate", "session-history", 7, "command-history-item-duplicate", domain.TurnStarted{TurnID: "turn-item-new", Input: "again"}, domain.AssistantMessageStarted{TurnID: "turn-item-new", ItemID: "item-history"})); err == nil {
		t.Fatal("historical ItemID reuse succeeded")
	} else {
		requireCode(t, err, application.StoreCodeDomainIdentityConflict)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-history-tool-start", "session-history", 7, "command-history-tool-start", domain.TurnStarted{TurnID: "turn-tool-history", Input: "tool"}, domain.ToolCallStarted{TurnID: "turn-tool-history", ItemID: "item-tool-history", CallID: "call-1", Name: "read_file", Arguments: `{"path":"README.md"}`, StepIndex: 1}, domain.ToolCallCompleted{TurnID: "turn-tool-history", ItemID: "item-tool-history", CallID: "call-1", Content: "ok", Truncated: false}, domain.TurnCompleted{TurnID: "turn-tool-history"})); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-history-tool-duplicate", "session-history", 11, "command-history-tool-duplicate", domain.TurnStarted{TurnID: "turn-tool-new", Input: "again"}, domain.ToolCallStarted{TurnID: "turn-tool-new", ItemID: "item-tool-history", CallID: "call-2", Name: "read_file", Arguments: `{}`, StepIndex: 1})); err == nil {
		t.Fatal("historical tool ItemID reuse succeeded")
	} else {
		requireCode(t, err, application.StoreCodeDomainIdentityConflict)
	}
	testAllIndexRollback(t, h)
}

func testConcurrentCommitPositions(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	const workers = 16
	positions := make(chan uint64, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := h.Store.Append(context.Background(), v2Append(domain.AppendID(fmt.Sprintf("append-concurrent-%d", i)), domain.SessionID(fmt.Sprintf("session-concurrent-%d", i)), 0, domain.CommandID(fmt.Sprintf("command-concurrent-%d", i)), domain.SessionCreated{WorkspaceRoot: "/concurrent"}))
			if err != nil {
				errs <- err
				return
			}
			positions <- r.CommitPosition
		}(i)
	}
	wg.Wait()
	close(positions)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Append() error = %v", err)
	}
	seen := make(map[uint64]struct{})
	for pos := range positions {
		if pos == 0 {
			t.Error("zero commit position")
		}
		if _, ok := seen[pos]; ok {
			t.Errorf("duplicate commit position %d", pos)
		}
		seen[pos] = struct{}{}
	}
	if len(seen) != workers {
		t.Fatalf("commit positions = %d, want %d", len(seen), workers)
	}
	for want := uint64(1); want <= workers; want++ {
		if _, ok := seen[want]; !ok {
			t.Errorf("commit positions omit %d: %#v", want, seen)
		}
	}
}

func testPublicationCancellationAndCorruptReceipts(t *testing.T, factory Factory) {
	h := factory(t)
	requireHarness(t, h)
	before := v2Append("append-cancel-before", "session-cancel-before", 0, "command-cancel-before", domain.SessionCreated{WorkspaceRoot: "/before"})
	ctx, cancel := context.WithCancel(context.Background())
	h.SetCommitHook(CommitHookBeforePublish, cancel)
	if _, err := h.Store.Append(ctx, before); err == nil {
		t.Fatal("cancellation before publication committed")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	assertAppendAbsent(t, h, before, 0)

	after := v2Append("append-cancel-after", "session-cancel-after", 0, "command-cancel-after", domain.SessionCreated{WorkspaceRoot: "/after"})
	ctx, cancel = context.WithCancel(context.Background())
	h.SetCommitHook(CommitHookAfterPublish, cancel)
	receipt, err := h.Store.Append(ctx, after)
	if err != nil {
		t.Fatalf("cancellation after publication was reported as absence: %v", err)
	}
	digest, err := application.DigestAppendRequest(after)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := h.Store.ResolveAppend(context.Background(), application.ResolveAppendRequest{AppendID: after.AppendID, RequestDigest: digest}); err != nil || resolved.Kind != application.AppendResolutionCommitted || resolved.Receipt == nil || *resolved.Receipt != receipt {
		t.Fatalf("post-publication resolution = (%#v, %v), want %#v", resolved, err, receipt)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-cancel-after-advance", "session-cancel-after", 1, "command-cancel-after-advance", domain.TurnStarted{TurnID: "turn-cancel-after", Input: "advance"})); err != nil {
		t.Fatal(err)
	}

	// The corruption seam changes the first receipt to a different but otherwise
	// plausible nonzero position/range from this same stream.
	h.CorruptReceipt(after.AppendID)
	if _, err := h.Store.Append(context.Background(), after); err == nil {
		t.Fatal("exact retry returned corrupt receipt")
	} else {
		requireCode(t, err, application.StoreCodeCorrupt)
	}
	if _, err := h.Store.ResolveAppend(context.Background(), application.ResolveAppendRequest{AppendID: after.AppendID, RequestDigest: digest}); err == nil {
		t.Fatal("resolve returned corrupt receipt")
	} else {
		requireCode(t, err, application.StoreCodeCorrupt)
	}
}

func validBatch(count int) []domain.Event {
	events := []domain.Event{domain.SessionCreated{WorkspaceRoot: "/batch"}}
	pairs := (count - 1) / 2
	for n := 0; n < pairs; n++ {
		id := domain.TurnID(fmt.Sprintf("turn-batch-%d", n))
		events = append(events, domain.TurnStarted{TurnID: id, Input: "ok"}, domain.TurnCompleted{TurnID: id})
	}
	if len(events) < count {
		events = append(events, domain.SessionClosed{})
	}
	return events
}

func testAllIndexRollback(t *testing.T, h Harness) {
	t.Helper()
	digest, err := application.DigestRunTurnRequestV1("session-rollback", "rollback")
	if err != nil {
		t.Fatal(err)
	}
	bad := v2Append("append-rollback-bad", "session-rollback", 0, "command-rollback-bad", domain.SessionCreated{WorkspaceRoot: "/rollback"}, domain.TurnStarted{TurnID: "turn-rollback", Input: "rollback"}, domain.AssistantMessageStarted{TurnID: "turn-rollback", ItemID: "item-rollback"}, domain.AssistantMessageCompleted{TurnID: "turn-rollback", ItemID: "item-rollback", Text: "done"}, domain.AssistantMessageStarted{TurnID: "turn-rollback", ItemID: "item-rollback"})
	bad.Admission = &application.CommandAdmission{RunTurnRequestID: "request-rollback", RequestDigest: digest, TurnID: "turn-rollback", ItemID: "item-rollback"}
	if _, err := h.Store.Append(context.Background(), bad); err == nil {
		t.Fatal("duplicate same-batch ItemID succeeded")
	} else {
		requireCode(t, err, application.StoreCodeDomainIdentityConflict)
	}
	assertAppendAbsent(t, h, bad, 0)
	lookup, err := h.Store.FindCommandRequest(context.Background(), application.FindCommandRequestRequest{RunTurnRequestID: "request-rollback", SessionID: "session-rollback", RequestDigest: digest})
	if err != nil || lookup.Kind != application.CommandRequestLookupNotFound || lookup.Record != nil {
		t.Fatalf("rejected admission lookup = (%#v, %v)", lookup, err)
	}
	good := v2Append("append-rollback-good", "session-rollback", 0, "command-rollback-good", domain.SessionCreated{WorkspaceRoot: "/rollback"}, domain.TurnStarted{TurnID: "turn-rollback", Input: "rollback"}, domain.AssistantMessageStarted{TurnID: "turn-rollback", ItemID: "item-rollback"})
	good.Events[0].ID, good.Events[1].ID, good.Events[2].ID = bad.Events[0].ID, bad.Events[1].ID, bad.Events[2].ID
	good.Admission = &application.CommandAdmission{RunTurnRequestID: "request-rollback", RequestDigest: digest, TurnID: "turn-rollback", ItemID: "item-rollback"}
	if _, err := h.Store.Append(context.Background(), good); err != nil {
		t.Fatalf("rejected append reserved an index: %v", err)
	}
}

func assertAppendAbsent(t *testing.T, h Harness, request application.AppendRequest, wantVersion int) {
	t.Helper()
	page, err := h.Store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: request.SessionID, Limit: 256})
	if err != nil || len(page.Records) != wantVersion {
		t.Fatalf("rejected append stream = (%#v, %v), want version %d", page, err, wantVersion)
	}
	if got, err := h.Store.ResolveAppend(context.Background(), application.ResolveAppendRequest{AppendID: request.AppendID}); err != nil || got.Kind != application.AppendResolutionNotFound || got.Receipt != nil {
		t.Fatalf("rejected append resolution = (%#v, %v)", got, err)
	}
}

func testByteLimits(t *testing.T, h Harness) {
	t.Helper()
	const maxPayload = 8 * 1024 * 1024
	exactPayload := payloadEventOfSize(t, maxPayload)
	seed := v2Append("append-payload-seed", "session-payload", 0, "command-payload-seed", domain.SessionCreated{WorkspaceRoot: "/payload"}, domain.TurnStarted{TurnID: "turn-payload", Input: "payload"}, domain.AssistantMessageStarted{TurnID: "turn-payload", ItemID: "item-payload"})
	if _, err := h.Store.Append(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	exact := v2Append("append-payload-exact", "session-payload", 3, "command-payload-exact", exactPayload)
	if _, err := h.Store.Append(context.Background(), exact); err != nil {
		t.Fatalf("exact 8MiB event payload rejected: %v", err)
	}

	tooLarge := v2Append("append-payload-plus-one", "session-payload-too-large", 0, "command-payload-plus-one", payloadEventOfSize(t, maxPayload+1))
	if _, err := h.Store.Append(context.Background(), tooLarge); err == nil {
		t.Fatal("8MiB+1 event payload succeeded")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	assertAppendAbsent(t, h, tooLarge, 0)

	const maxRequest = 16 * 1024 * 1024
	exactRequest := requestOfCanonicalSize(t, "append-request-exact", "session-request-exact", maxRequest)
	if got := canonicalAppendSize(t, exactRequest); got != maxRequest {
		t.Fatalf("exact request size = %d, want %d", got, maxRequest)
	}
	if _, err := application.DigestAppendRequest(exactRequest); err != nil {
		t.Fatalf("DigestAppendRequest(exact) = %v", err)
	}
	if _, err := h.Store.Append(context.Background(), exactRequest); err != nil {
		t.Fatalf("exact 16MiB request rejected: %v", err)
	}
	overRequest := requestOfCanonicalSize(t, "append-request-plus-one", "session-request-plus-one", maxRequest+1)
	if got := canonicalAppendSize(t, overRequest); got != maxRequest+1 {
		t.Fatalf("over request size = %d, want %d", got, maxRequest+1)
	}
	if _, err := application.DigestAppendRequest(overRequest); err == nil {
		t.Fatal("DigestAppendRequest accepted 16MiB+1 request")
	}
	if _, err := h.Store.Append(context.Background(), overRequest); err == nil {
		t.Fatal("16MiB+1 request succeeded")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	assertAppendAbsent(t, h, overRequest, 0)
	// Reusing every rejected candidate identity in an accepted exact-sized
	// request catches leaked Event/Turn/Item indexes.
	reuse := v2Append("append-request-reuse", "session-request-plus-one", 0, "command-request-reuse", domain.SessionCreated{WorkspaceRoot: "/request"}, domain.TurnStarted{TurnID: "turn-request", Input: "request"}, domain.AssistantMessageStarted{TurnID: "turn-request", ItemID: "item-request-1"})
	for i := range reuse.Events {
		reuse.Events[i].ID = overRequest.Events[i].ID
	}
	if _, err := h.Store.Append(context.Background(), reuse); err != nil {
		t.Fatalf("rejected over-limit request leaked identities: %v", err)
	}
}

func payloadEventOfSize(t *testing.T, want int) domain.Event {
	t.Helper()
	event := domain.AssistantMessageCompleted{TurnID: "turn-payload", ItemID: "item-payload"}
	_, payload, err := domain.MarshalEventPayload(event)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > want {
		t.Fatalf("payload baseline %d exceeds target %d", len(payload), want)
	}
	event.Text = strings.Repeat("x", want-len(payload))
	_, payload, err = domain.MarshalEventPayload(event)
	if err != nil || len(payload) != want {
		t.Fatalf("payload size = (%d, %v), want %d", len(payload), err, want)
	}
	return event
}

func requestOfCanonicalSize(t *testing.T, appendID domain.AppendID, sessionID domain.SessionID, want int) application.AppendRequest {
	t.Helper()
	events := []domain.Event{domain.SessionCreated{WorkspaceRoot: "/request"}, domain.TurnStarted{TurnID: "turn-request", Input: "request"}, domain.AssistantMessageStarted{TurnID: "turn-request", ItemID: "item-request-1"}, domain.AssistantMessageCompleted{TurnID: "turn-request", ItemID: "item-request-1"}, domain.AssistantMessageStarted{TurnID: "turn-request", ItemID: "item-request-2"}, domain.AssistantMessageCompleted{TurnID: "turn-request", ItemID: "item-request-2"}, domain.TurnCompleted{TurnID: "turn-request"}}
	request := v2Append(appendID, sessionID, 0, domain.CommandID("command-"+string(appendID)), events...)
	base := canonicalAppendSize(t, request)
	extra := want - base
	if extra < 0 {
		t.Fatalf("request baseline %d exceeds target %d", base, want)
	}
	const payloadLimit = 8 * 1024 * 1024
	_, emptyPayload, err := domain.MarshalEventPayload(events[3])
	if err != nil {
		t.Fatal(err)
	}
	capacity := 2 * (payloadLimit - len(emptyPayload))
	if extra > capacity {
		t.Fatalf("request target needs %d payload bytes, capacity %d", extra, capacity)
	}
	firstExtra := extra
	if firstExtra > payloadLimit-len(emptyPayload) {
		firstExtra = payloadLimit - len(emptyPayload)
	}
	secondExtra := extra - firstExtra
	events[3] = domain.AssistantMessageCompleted{TurnID: "turn-request", ItemID: "item-request-1", Text: strings.Repeat("x", firstExtra)}
	events[5] = domain.AssistantMessageCompleted{TurnID: "turn-request", ItemID: "item-request-2", Text: strings.Repeat("x", secondExtra)}
	request = v2Append(appendID, sessionID, 0, domain.CommandID("command-"+string(appendID)), events...)
	return request
}

// canonicalAppendSize mirrors the public EV2-04 framing with a hand-derived
// byte count; it does not call the digest implementation to derive its want.
func canonicalAppendSize(t *testing.T, request application.AppendRequest) int {
	t.Helper()
	size := 8 + framedSize(string(request.SessionID)) + 8 + framedSize(string(request.CommandID)) + 1 + 8
	for _, event := range request.Events {
		typeName, payload, err := domain.MarshalEventPayload(event.Event)
		if err != nil {
			t.Fatal(err)
		}
		size += framedSize(string(event.ID)) + framedSize(typeName) + 8 + framedSize(event.OccurredAt.Format(time.RFC3339Nano)) + framedSizeBytes(payload)
	}
	return size
}

func framedSize(value string) int      { return 4 + len(value) }
func framedSizeBytes(value []byte) int { return 4 + len(value) }

func v2Append(appendID domain.AppendID, sessionID domain.SessionID, expected uint64, commandID domain.CommandID, events ...domain.Event) application.AppendRequest {
	request := application.AppendRequest{AppendID: appendID, SessionID: sessionID, ExpectedVersion: expected, CommandID: commandID, Authority: application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, Events: make([]application.ProposedEvent, len(events))}
	for i, event := range events {
		request.Events[i] = proposed(domain.EventID(fmt.Sprintf("event-%s-%d", appendID, i)), event)
	}
	return request
}

func proposed(id domain.EventID, event domain.Event) application.ProposedEvent {
	return application.ProposedEvent{ID: id, SchemaVersion: 1, OccurredAt: v2Time, Event: event}
}
func ptr(value uint64) *uint64 { return &value }

func readAll(t *testing.T, store application.EventStore, sessionID domain.SessionID) []domain.RecordedEvent {
	t.Helper()
	page, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: sessionID, Limit: 256})
	if err != nil {
		t.Fatalf("ReadStream() error = %v", err)
	}
	return page.Records
}
