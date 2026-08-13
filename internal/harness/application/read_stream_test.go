package application

import (
	"context"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestReadWholeStreamPinnedKeepsFirstHead(t *testing.T) {
	first := []domain.RecordedEvent{readRecord(1), readRecord(2)}
	store := &pagingSpy{pages: []StreamPage{
		{Records: first, HeadVersion: 3, NextAfterSequence: 2},
		{Records: []domain.RecordedEvent{readRecord(3)}, HeadVersion: 3, NextAfterSequence: 3, End: true},
	}}
	got, err := ReadWholeStreamPinned(context.Background(), store, "session-1", 2)
	if err != nil || len(got) != 3 {
		t.Fatalf("ReadWholeStreamPinned() = %d records, %v", len(got), err)
	}
	if len(store.requests) != 2 || store.requests[1].HeadVersion == nil || *store.requests[1].HeadVersion != 3 {
		t.Fatalf("requests = %#v", store.requests)
	}
	got[0].Event = domain.SessionCreated{WorkspaceRoot: "/tampered"}
	if root := first[0].Event.(domain.SessionCreated).WorkspaceRoot; root != "/workspace" {
		t.Fatalf("store record mutated to %q", root)
	}
}

func TestReadWholeStreamPinnedRejectsChangingHead(t *testing.T) {
	store := &pagingSpy{pages: []StreamPage{
		{Records: []domain.RecordedEvent{readRecord(1)}, HeadVersion: 2, NextAfterSequence: 1},
		{Records: []domain.RecordedEvent{readRecord(2)}, HeadVersion: 3, NextAfterSequence: 2},
	}}
	_, err := ReadWholeStreamPinned(context.Background(), store, "session-1", 1)
	assertStoreContractViolation(t, err)
}

func TestReadWholeStreamPinnedRejectsMutatedRequestHead(t *testing.T) {
	store := &headMutatingSpy{}
	_, err := ReadWholeStreamPinned(context.Background(), store, "session-1", 1)
	assertStoreContractViolation(t, err)
	if store.calls != 2 {
		t.Fatalf("read calls = %d, want 2", store.calls)
	}
}

func TestReadWholeStreamPinnedRejectsInvalidRecordContractAndInputs(t *testing.T) {
	for name, mutate := range map[string]func(*domain.RecordedEvent){
		"schema":     func(record *domain.RecordedEvent) { record.SchemaVersion = 2 },
		"event id":   func(record *domain.RecordedEvent) { record.ID = " bad" },
		"command id": func(record *domain.RecordedEvent) { record.CommandID = " bad" },
		"timestamp": func(record *domain.RecordedEvent) {
			record.OccurredAt = time.Date(2026, 8, 13, 0, 0, 0, 0, time.FixedZone("offset", 3600))
		},
		"payload":        func(record *domain.RecordedEvent) { record.Event = unknownReadEvent{} },
		"wrong session":  func(record *domain.RecordedEvent) { record.SessionID = "session-2" },
		"wrong sequence": func(record *domain.RecordedEvent) { record.Sequence = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			record := readRecord(1)
			mutate(&record)
			_, err := ReadWholeStreamPinned(context.Background(), &pagingSpy{pages: []StreamPage{{Records: []domain.RecordedEvent{record}, HeadVersion: 1, NextAfterSequence: 1, End: true}}}, "session-1", 1)
			assertStoreContractViolation(t, err)
		})
	}
	for name, page := range map[string]StreamPage{
		"too many": {Records: []domain.RecordedEvent{readRecord(1), readRecord(2)}, HeadVersion: 2, NextAfterSequence: 2, End: true},
		"cursor":   {Records: []domain.RecordedEvent{readRecord(1)}, HeadVersion: 1, NextAfterSequence: 0, End: true},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadWholeStreamPinned(context.Background(), &pagingSpy{pages: []StreamPage{page}}, "session-1", 1)
			assertStoreContractViolation(t, err)
		})
	}
	var nilContext context.Context
	for _, limit := range []uint32{0, 257} {
		_, err := ReadWholeStreamPinned(context.Background(), &pagingSpy{}, "session-1", limit)
		if !IsCategory(err, CategoryValidation) {
			t.Fatalf("limit %d error = %v", limit, err)
		}
	}
	_, err := ReadWholeStreamPinned(nilContext, &pagingSpy{}, "session-1", 1)
	if !IsCategory(err, CategoryValidation) {
		t.Fatalf("nil context error = %v", err)
	}
}

func TestReadWholeStreamPinnedRejectsPrematureEndAndNoProgress(t *testing.T) {
	for name, page := range map[string]StreamPage{
		"premature end": {Records: []domain.RecordedEvent{readRecord(1)}, HeadVersion: 2, NextAfterSequence: 1, End: true},
		"no progress":   {HeadVersion: 1, NextAfterSequence: 0},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadWholeStreamPinned(context.Background(), &pagingSpy{pages: []StreamPage{page}}, "session-1", 1)
			assertStoreContractViolation(t, err)
		})
	}
}

func TestReadWholeStreamPinnedRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadWholeStreamPinned(ctx, &pagingSpy{}, "session-1", 1)
	if !IsCategory(err, CategoryCanceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
}

type pagingSpy struct {
	pages    []StreamPage
	requests []ReadStreamRequest
}

type headMutatingSpy struct{ calls int }

func (store *headMutatingSpy) ReadStream(_ context.Context, request ReadStreamRequest) (StreamPage, error) {
	store.calls++
	if store.calls == 1 {
		return StreamPage{Records: []domain.RecordedEvent{readRecord(1)}, HeadVersion: 2, NextAfterSequence: 1}, nil
	}
	if request.HeadVersion == nil {
		return StreamPage{}, nil
	}
	*request.HeadVersion = 3
	return StreamPage{Records: []domain.RecordedEvent{readRecord(2)}, HeadVersion: 3, NextAfterSequence: 2}, nil
}
func (*headMutatingSpy) Append(context.Context, AppendRequestV2) (CommitReceipt, error) {
	return CommitReceipt{}, nil
}
func (*headMutatingSpy) ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error) {
	return AppendResolution{}, nil
}
func (*headMutatingSpy) FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error) {
	return CommandRequestLookup{}, nil
}

type unknownReadEvent struct{}

func (unknownReadEvent) EventType() string { return "unknown" }

func (store *pagingSpy) ReadStream(_ context.Context, request ReadStreamRequest) (StreamPage, error) {
	store.requests = append(store.requests, request)
	if len(store.pages) == 0 {
		return StreamPage{HeadVersion: 0, End: true}, nil
	}
	page := store.pages[0]
	store.pages = store.pages[1:]
	return page, nil
}

func (*pagingSpy) Append(context.Context, AppendRequestV2) (CommitReceipt, error) {
	return CommitReceipt{}, nil
}
func (*pagingSpy) ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error) {
	return AppendResolution{}, nil
}
func (*pagingSpy) FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error) {
	return CommandRequestLookup{}, nil
}

func readRecord(sequence uint64) domain.RecordedEvent {
	event := domain.Event(domain.SessionCreated{WorkspaceRoot: "/workspace"})
	if sequence > 1 {
		event = domain.SessionClosed{}
	}
	return domain.RecordedEvent{SchemaVersion: 1, ID: domain.EventID("event-" + string(rune('0'+sequence))), CommandID: "command-1", SessionID: "session-1", Sequence: sequence, OccurredAt: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), Event: event}
}

func assertStoreContractViolation(t *testing.T, err error) {
	t.Helper()
	if !IsCategory(err, CategoryInternal) {
		t.Fatalf("error = %v, want internal store contract violation", err)
	}
}
