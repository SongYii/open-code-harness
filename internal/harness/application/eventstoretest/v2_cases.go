package eventstoretest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

var v2Time = time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

func testAtomicAppendAndCAS(t *testing.T, factory V2Factory) {
	h := factory(t)
	requireV2(t, h)
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
}

func testExactReceiptRetry(t *testing.T, factory V2Factory) {
	h := factory(t)
	requireV2(t, h)
	request := v2Append("append-retry", "session-retry", 0, "command-retry", domain.SessionCreated{WorkspaceRoot: "/retry"})
	first, err := h.Store.Append(context.Background(), request)
	if err != nil {
		t.Fatalf("first Append() error = %v", err)
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

func testPinnedPagination(t *testing.T, factory V2Factory) {
	h := factory(t)
	requireV2(t, h)
	if _, err := h.Store.Append(context.Background(), v2Append("append-page-1", "session-page", 0, "command-page-1", domain.SessionCreated{WorkspaceRoot: "/page"})); err != nil {
		t.Fatal(err)
	}
	first, err := h.Store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-page", Limit: 1})
	if err != nil || first.HeadVersion != 1 || first.NextAfterSequence != 1 || !first.End {
		t.Fatalf("first page = (%#v, %v)", first, err)
	}
	if _, err := h.Store.Append(context.Background(), v2Append("append-page-2", "session-page", 1, "command-page-2", domain.TurnStarted{TurnID: "turn-page", Input: "next"})); err != nil {
		t.Fatal(err)
	}
	second, err := h.Store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-page", AfterSequence: first.NextAfterSequence, HeadVersion: &first.HeadVersion, Limit: 1})
	if err != nil || len(second.Records) != 0 || second.HeadVersion != 1 || second.NextAfterSequence != 1 || !second.End {
		t.Fatalf("pinned second page = (%#v, %v)", second, err)
	}
	for _, req := range []application.ReadStreamRequest{{SessionID: "session-page", Limit: 0}, {SessionID: "session-page", Limit: 257}, {SessionID: "session-page", AfterSequence: 3, Limit: 1}, {SessionID: "session-page", AfterSequence: 2, HeadVersion: ptr(uint64(1)), Limit: 1}} {
		if _, err := h.Store.ReadStream(context.Background(), req); err == nil {
			t.Fatalf("ReadStream(%#v) succeeded", req)
		} else {
			requireCode(t, err, application.StoreCodeInvalidRead)
		}
	}
}

func testAdmissionIdentity(t *testing.T, factory V2Factory) {
	h := factory(t)
	requireV2(t, h)
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

func testWriterFencing(t *testing.T, factory V2Factory) {
	h := factory(t)
	requireV2(t, h)
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

func testUnknownOutcome(t *testing.T, factory V2Factory) {
	h := factory(t)
	requireV2(t, h)
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
	h.FailNext(FaultAfterCommitBeforeAck, errors.New("after"))
	if _, err := h.Store.Append(context.Background(), request); err == nil {
		t.Fatal("after-commit fault did not report unknown")
	} else {
		requireCode(t, err, application.StoreCodeCommitOutcomeUnknown)
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
}

func testLimitsCopiesCancellationAndCorruption(t *testing.T, factory V2Factory) {
	h := factory(t)
	requireV2(t, h)
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
	tooMany := v2Append("append-65", "session-65", 0, "command-65", domain.SessionCreated{WorkspaceRoot: "/65"})
	for len(tooMany.Events) < 65 {
		n := len(tooMany.Events)
		tooMany.Events = append(tooMany.Events, proposed(domain.EventID(fmt.Sprintf("event-65-%d", n)), domain.SessionCreated{WorkspaceRoot: fmt.Sprintf("/%d", n)}))
	}
	if _, err := h.Store.Append(context.Background(), tooMany); err == nil {
		t.Fatal("65-event append succeeded")
	} else {
		requireCode(t, err, application.StoreCodeInvalidAppend)
	}
	maxEvents := []domain.Event{domain.SessionCreated{WorkspaceRoot: "/64"}}
	for n := 0; n < 31; n++ {
		turnID := domain.TurnID(fmt.Sprintf("turn-64-%d", n))
		maxEvents = append(maxEvents, domain.TurnStarted{TurnID: turnID, Input: "ok"}, domain.TurnCompleted{TurnID: turnID})
	}
	maxEvents = append(maxEvents, domain.SessionClosed{})
	maxBatch := v2Append("append-64", "session-64", 0, "command-64", maxEvents...)
	if _, err := h.Store.Append(context.Background(), maxBatch); err != nil {
		t.Fatalf("valid 64-event append error = %v", err)
	}
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
}

func testConcurrentCommitPositions(t *testing.T, factory V2Factory) {
	h := factory(t)
	requireV2(t, h)
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
}

func v2Append(appendID domain.AppendID, sessionID domain.SessionID, expected uint64, commandID domain.CommandID, events ...domain.Event) application.AppendRequestV2 {
	request := application.AppendRequestV2{AppendID: appendID, SessionID: sessionID, ExpectedVersion: expected, CommandID: commandID, Authority: application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, Events: make([]application.ProposedEvent, len(events))}
	for i, event := range events {
		request.Events[i] = proposed(domain.EventID(fmt.Sprintf("event-%s-%d", appendID, i)), event)
	}
	return request
}

func proposed(id domain.EventID, event domain.Event) application.ProposedEvent {
	return application.ProposedEvent{ID: id, SchemaVersion: 1, OccurredAt: v2Time, Event: event}
}
func ptr(value uint64) *uint64 { return &value }

func readAll(t *testing.T, store application.EventStoreV2, sessionID domain.SessionID) []domain.RecordedEvent {
	t.Helper()
	page, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: sessionID, Limit: 256})
	if err != nil {
		t.Fatalf("ReadStream() error = %v", err)
	}
	return page.Records
}
