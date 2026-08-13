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
