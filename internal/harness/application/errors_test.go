package application

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestApplicationErrorMethodsAreNilSafe(t *testing.T) {
	var typedNil *Error
	if got := typedNil.Error(); got != "<nil>" {
		t.Fatalf("typed nil Error() = %q, want <nil>", got)
	}
	if cause := typedNil.Unwrap(); cause != nil {
		t.Fatalf("typed nil Unwrap() = %v, want nil", cause)
	}
}

func TestIsCategoryTraversesNestedJoinsAfterTypedNilBranches(t *testing.T) {
	var typedNil *Error
	want := &Error{Category: CategoryPersistence, Code: "append_failed"}
	err := errors.Join(
		fmt.Errorf("typed nil: %w", typedNil),
		errors.Join(errors.New("ordinary"), fmt.Errorf("later: %w", want)),
	)
	if !IsCategory(err, CategoryPersistence) {
		t.Fatal("IsCategory() did not find a matching later sibling")
	}
	if IsCategory(err, CategoryModel) {
		t.Fatal("IsCategory() found an absent category")
	}
}

func TestVersionConflictMatcherIsNilSafeAcrossCompleteErrorTree(t *testing.T) {
	var typedNil *VersionConflictError
	conflict := &VersionConflictError{SessionID: "session-1", ExpectedVersion: 1, ActualVersion: 2}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil},
		{name: "direct typed nil", err: typedNil},
		{name: "typed nil only join", err: errors.Join(typedNil, errors.New("ordinary"))},
		{name: "later sibling", err: errors.Join(typedNil, fmt.Errorf("wrapped: %w", conflict)), want: true},
		{name: "nested join", err: fmt.Errorf("outer: %w", errors.Join(errors.New("ordinary"), conflict)), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsVersionConflict(test.err); got != test.want {
				t.Fatalf("IsVersionConflict() = %t, want %t", got, test.want)
			}
		})
	}
	if got := typedNil.Error(); got != "<nil>" {
		t.Fatalf("typed nil VersionConflictError.Error() = %q, want <nil>", got)
	}
}

func TestAppendAndApplyPreservesDomainApplyCause(t *testing.T) {
	state := applyForAppendTest(t, domain.Session{}, 1, time.Date(2026, 8, 12, 4, 0, 0, 0, time.UTC), domain.SessionCreated{WorkspaceRoot: "/workspace"})
	state = applyForAppendTest(t, state, 2, time.Date(2026, 8, 12, 4, 1, 0, 0, time.UTC), domain.TurnStarted{TurnID: "turn-1", Input: "hello"})
	event := domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}
	store := &appendResultStore{records: []domain.RecordedEvent{{
		SchemaVersion: 1, ID: "event-3", CommandID: "command-append", SessionID: "session-1", Sequence: 3,
		OccurredAt: time.Date(2026, 8, 12, 3, 59, 0, 0, time.UTC), Event: event,
	}}}
	service := &Service{store: store}
	_, _, err := service.appendAndApply(context.Background(), "session-1", state, []domain.UncommittedEvent{{Event: event}}, "command-append")
	if !IsCategory(err, CategoryInternal) || !domain.IsCode(err, domain.CodeInvalidEvent) {
		t.Fatalf("appendAndApply() error = %v, want internal error preserving invalid_event apply cause", err)
	}
}

func TestAppendAndApplyRejectsOverflowBeforeCallingStore(t *testing.T) {
	store := &appendResultStore{}
	service := &Service{store: store}
	_, _, err := service.appendAndApply(context.Background(), "session-1", domain.Session{ID: "session-1", Version: math.MaxUint64}, []domain.UncommittedEvent{{
		Event: domain.SessionClosed{},
	}}, "command-overflow")
	if !IsCategory(err, CategoryInternal) || store.calls != 0 {
		t.Fatalf("appendAndApply() = error %v, store calls %d; want internal rejection before Store", err, store.calls)
	}
}

func TestAppendAndApplyRequiresOneExactBatchTimestamp(t *testing.T) {
	state := applyForAppendTest(t, domain.Session{}, 1, time.Date(2026, 8, 12, 4, 0, 0, 0, time.UTC), domain.SessionCreated{WorkspaceRoot: "/workspace"})
	events := []domain.UncommittedEvent{
		{Event: domain.TurnStarted{TurnID: "turn-1", Input: "hello"}},
		{Event: domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}},
	}
	store := &appendResultStore{records: []domain.RecordedEvent{
		{SchemaVersion: 1, ID: "event-2", CommandID: "command-batch", SessionID: "session-1", Sequence: 2, OccurredAt: time.Date(2026, 8, 12, 4, 1, 0, 0, time.UTC), Event: events[0].Event},
		{SchemaVersion: 1, ID: "event-3", CommandID: "command-batch", SessionID: "session-1", Sequence: 3, OccurredAt: time.Date(2026, 8, 12, 4, 1, 1, 0, time.UTC), Event: events[1].Event},
	}}
	service := &Service{store: store}
	_, _, err := service.appendAndApply(context.Background(), "session-1", state, events, "command-batch")
	if !IsCategory(err, CategoryInternal) {
		t.Fatalf("appendAndApply() error = %v, want store contract violation", err)
	}
}

func applyForAppendTest(t *testing.T, state domain.Session, sequence uint64, occurredAt time.Time, event domain.Event) domain.Session {
	t.Helper()
	next, err := domain.Apply(state, domain.RecordedEvent{
		SchemaVersion: 1, ID: domain.EventID(fmt.Sprintf("event-seed-%d", sequence)), CommandID: "command-seed",
		SessionID: "session-1", Sequence: sequence, OccurredAt: occurredAt, Event: event,
	})
	if err != nil {
		t.Fatalf("seed Apply() error = %v", err)
	}
	return next
}

type appendResultStore struct {
	records []domain.RecordedEvent
	calls   int
}

func (*appendResultStore) Load(context.Context, domain.SessionID) ([]domain.RecordedEvent, error) {
	return nil, nil
}

func (store *appendResultStore) Append(context.Context, AppendRequest) ([]domain.RecordedEvent, error) {
	store.calls++
	return domain.CloneRecordedEvents(store.records)
}
