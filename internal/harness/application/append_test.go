package application

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestBuildAppendIntentOwnsBatchMetadata(t *testing.T) {
	clock := &countingClock{value: time.Date(2026, 8, 13, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))}
	ids := &intentIDs{}
	events := []domain.UncommittedEvent{{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}}
	admission := &CommandAdmission{RunTurnRequestID: "request-1", TurnID: "turn-1", ItemID: "item-1"}
	intent, err := BuildAppendIntent(clock, ids, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", admission, events)
	if err != nil {
		t.Fatal(err)
	}
	if clock.calls != 1 || ids.appendCalls != 1 || ids.eventCalls != 1 {
		t.Fatalf("clock=%d append=%d event=%d", clock.calls, ids.appendCalls, ids.eventCalls)
	}
	if len(intent.Request.Events) != 1 || intent.Request.Events[0].OccurredAt.Location() != time.UTC {
		t.Fatalf("intent = %#v", intent)
	}
	events[0].Event = domain.SessionCreated{WorkspaceRoot: "/tampered"}
	admission.TurnID = "turn-tampered"
	if got := intent.Request.Events[0].Event.(domain.SessionCreated).WorkspaceRoot; got != "/workspace" {
		t.Fatalf("intent event = %q", got)
	}
	if intent.Request.Admission.TurnID != "turn-1" {
		t.Fatalf("intent admission = %#v", intent.Request.Admission)
	}
}

func TestBuildAppendIntentValidatesBatchBoundsAndIDFailures(t *testing.T) {
	base := []domain.UncommittedEvent{{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}}
	for _, events := range [][]domain.UncommittedEvent{nil, make([]domain.UncommittedEvent, 65)} {
		if len(events) == 65 {
			for i := range events {
				events[i] = base[0]
			}
		}
		ids := &intentIDs{}
		if _, err := BuildAppendIntent(&countingClock{value: time.Now().UTC()}, ids, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", nil, events); !IsCategory(err, CategoryValidation) || ids.appendCalls != 0 || ids.eventCalls != 0 {
			t.Fatalf("events=%d err=%v append=%d events=%d", len(events), err, ids.appendCalls, ids.eventCalls)
		}
	}
	ids := &intentIDs{appendErr: context.DeadlineExceeded}
	if _, err := BuildAppendIntent(&countingClock{value: time.Now().UTC()}, ids, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", nil, base); !IsCategory(err, CategoryInternal) || ids.eventCalls != 0 {
		t.Fatalf("append ID failure = %v events=%d", err, ids.eventCalls)
	}
	ids = &intentIDs{eventErrAt: 2}
	if _, err := BuildAppendIntent(&countingClock{value: time.Now().UTC()}, ids, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", nil, append(base, base[0])); !IsCategory(err, CategoryInternal) || ids.appendCalls != 1 || ids.eventCalls != 2 {
		t.Fatalf("event ID failure calls append=%d event=%d", ids.appendCalls, ids.eventCalls)
	}
}

func TestCommitAppendIntentRejectsNilAndCanceledContextBeforeStore(t *testing.T) {
	intent, err := BuildAppendIntent(&countingClock{value: time.Now().UTC()}, &intentIDs{}, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", nil, []domain.UncommittedEvent{{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}})
	if err != nil {
		t.Fatal(err)
	}
	var nilContext context.Context
	for _, test := range []struct {
		ctx      context.Context
		category ErrorCategory
		code     string
	}{{ctx: nilContext, category: CategoryValidation, code: "invalid_context"}, {ctx: canceledContext(), category: CategoryCanceled, code: "canceled"}} {
		store := &receiptSpy{}
		_, _, err := CommitAppendIntent(test.ctx, store, domain.Session{}, intent)
		if store.calls != 0 || !IsCategory(err, test.category) {
			t.Fatalf("err=%v calls=%d", err, store.calls)
		}
		var applicationErr *Error
		if !errors.As(err, &applicationErr) || applicationErr.Code != test.code {
			t.Fatalf("error = %v, want %s/%s", err, test.category, test.code)
		}
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// This is the v2 replacement map for the removed v1 appendAndApply tests:
// immutable caller events and one batch timestamp are proved here; receipt
// metadata and compact application are proved by the Commit tests below.
func TestBuildAppendIntentUsesOneTimestampAndRejectsOverflowBeforeStore(t *testing.T) {
	clock := &countingClock{value: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)}
	ids := &intentIDs{}
	intent, err := BuildAppendIntent(clock, ids, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", nil, []domain.UncommittedEvent{
		{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}},
		{Event: domain.SessionClosed{}},
	})
	if err != nil || len(intent.Request.Events) != 2 || intent.Request.Events[0].OccurredAt != intent.Request.Events[1].OccurredAt || ids.appendCalls != 1 || ids.eventCalls != 2 {
		t.Fatalf("BuildAppendIntent() = %#v, %v; append=%d event=%d", intent, err, ids.appendCalls, ids.eventCalls)
	}
	store := &receiptSpy{}
	if _, err := BuildAppendIntent(clock, ids, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", math.MaxUint64, "command-1", nil, []domain.UncommittedEvent{{Event: domain.SessionClosed{}}}); err == nil {
		t.Fatal("BuildAppendIntent() accepted overflowing sequence range")
	}
	if store.calls != 0 {
		t.Fatalf("overflow attempted Append %d times", store.calls)
	}
}

func TestCommitAppendIntentReconstructsIntentMetadata(t *testing.T) {
	clock := &countingClock{value: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)}
	intent, err := BuildAppendIntent(clock, &intentIDs{}, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", nil, []domain.UncommittedEvent{{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}})
	if err != nil {
		t.Fatal(err)
	}
	store := &receiptSpy{receipt: CommitReceipt{AppendID: intent.Request.AppendID, CommitPosition: 1, FirstSequence: 1, LastSequence: 1}}
	next, records, err := CommitAppendIntent(context.Background(), store, domain.Session{}, intent)
	if err != nil || next.Version != 1 || len(records) != 1 || records[0].ID != intent.Request.Events[0].ID || store.calls != 1 {
		t.Fatalf("CommitAppendIntent() = %#v %#v %v calls=%d", next, records, err, store.calls)
	}
}

func TestCommitAppendIntentRejectsMalformedReceiptWithoutApply(t *testing.T) {
	clock := &countingClock{value: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)}
	intent, err := BuildAppendIntent(clock, &intentIDs{}, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", nil, []domain.UncommittedEvent{{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = CommitAppendIntent(context.Background(), &receiptSpy{receipt: CommitReceipt{AppendID: intent.Request.AppendID, CommitPosition: 1, FirstSequence: 2, LastSequence: 2}}, domain.Session{}, intent)
	assertStoreContractViolation(t, err)
}

func TestCommitAppendIntentRejectsChronologicallyInvalidCompactApply(t *testing.T) {
	base := time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	state, err := domain.Apply(domain.Session{}, domain.RecordedEvent{SchemaVersion: 1, ID: "event-seed-1", CommandID: "command-seed", SessionID: "session-1", Sequence: 1, OccurredAt: base, Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	state, err = domain.Apply(state, domain.RecordedEvent{SchemaVersion: 1, ID: "event-seed-2", CommandID: "command-seed", SessionID: "session-1", Sequence: 2, OccurredAt: base.Add(time.Minute), Event: domain.TurnStarted{TurnID: "turn-1", Input: "inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := BuildAppendIntent(&countingClock{value: base}, &intentIDs{}, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 2, "command-1", nil, []domain.UncommittedEvent{{Event: domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = CommitAppendIntent(context.Background(), &receiptSpy{receipt: CommitReceipt{AppendID: intent.Request.AppendID, CommitPosition: 1, FirstSequence: 3, LastSequence: 3}}, state, intent)
	if !IsCategory(err, CategoryInternal) || !domain.IsCode(err, domain.CodeInvalidEvent) {
		t.Fatalf("error = %v, want invalid compact apply", err)
	}
}

func TestCommitAppendIntentDefendsAgainstStoreRequestMutation(t *testing.T) {
	clock := &countingClock{value: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)}
	intent, err := BuildAppendIntent(clock, &intentIDs{}, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", nil, []domain.UncommittedEvent{{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}})
	if err != nil {
		t.Fatal(err)
	}
	store := &receiptSpy{receipt: CommitReceipt{AppendID: intent.Request.AppendID, CommitPosition: 1, FirstSequence: 1, LastSequence: 1}, mutate: func(request *AppendRequest) {
		request.Events[0].Event = domain.SessionCreated{WorkspaceRoot: "/tampered"}
	}}
	_, records, err := CommitAppendIntent(context.Background(), store, domain.Session{}, intent)
	if err != nil || records[0].Event.(domain.SessionCreated).WorkspaceRoot != "/workspace" || intent.Request.Events[0].Event.(domain.SessionCreated).WorkspaceRoot != "/workspace" {
		t.Fatalf("records=%#v intent=%#v err=%v", records, intent, err)
	}
}

type countingClock struct {
	value time.Time
	calls int
}

func (clock *countingClock) Now() time.Time { clock.calls++; return clock.value }

type intentIDs struct {
	appendCalls, eventCalls, eventErrAt int
	appendErr                           error
}

func (*intentIDs) NewSessionID() (domain.SessionID, error) { return "session-1", nil }
func (*intentIDs) NewTurnID() (domain.TurnID, error)       { return "turn-1", nil }
func (*intentIDs) NewItemID() (domain.ItemID, error)       { return "item-1", nil }
func (*intentIDs) NewCommandID() (domain.CommandID, error) { return "command-1", nil }
func (ids *intentIDs) NewAppendID() (domain.AppendID, error) {
	ids.appendCalls++
	return "append-1", ids.appendErr
}
func (ids *intentIDs) NewEventID() (domain.EventID, error) {
	ids.eventCalls++
	if ids.eventErrAt == ids.eventCalls {
		return "", context.DeadlineExceeded
	}
	return domain.EventID("event-" + string(rune('0'+ids.eventCalls))), nil
}

type receiptSpy struct {
	receipt CommitReceipt
	calls   int
	mutate  func(*AppendRequest)
}

func (*receiptSpy) ReadStream(context.Context, ReadStreamRequest) (StreamPage, error) {
	return StreamPage{}, nil
}
func (store *receiptSpy) Append(_ context.Context, request AppendRequest) (CommitReceipt, error) {
	store.calls++
	if store.mutate != nil {
		store.mutate(&request)
	}
	return store.receipt, nil
}
func (*receiptSpy) ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error) {
	return AppendResolution{}, nil
}
func (*receiptSpy) FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error) {
	return CommandRequestLookup{}, nil
}
