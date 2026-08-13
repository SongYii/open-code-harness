package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/application/eventstoretest"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestEventStoreContract(t *testing.T) {
	eventstoretest.Run(t, func(t *testing.T) eventstoretest.Harness {
		expectedOccurredAt := time.Date(2026, 8, 12, 1, 2, 3, 123456789, time.FixedZone("contract", 8*60*60))
		store, err := NewEventStore(
			testkit.FixedClock{Time: expectedOccurredAt},
			testkit.NewSequenceIDs(),
		)
		if err != nil {
			t.Fatal(err)
		}
		return eventstoretest.Harness{
			Store:              store,
			ExpectedOccurredAt: expectedOccurredAt,
			FailNextLoad:       store.FailNextLoad,
			FailNextAppend:     store.FailNextAppend,
		}
	})
}

func TestNewEventStoreRejectsNilDependenciesWithoutPanic(t *testing.T) {
	validClock := &countingClock{now: validTime()}
	validIDs := &countingIDs{}
	var typedNilClock *countingClock
	var typedNilIDs *countingIDs
	tests := []struct {
		name  string
		clock application.Clock
		ids   application.IDGenerator
	}{
		{name: "nil clock", ids: validIDs},
		{name: "typed nil clock", clock: typedNilClock, ids: validIDs},
		{name: "nil IDs", clock: validClock},
		{name: "typed nil IDs", clock: validClock, ids: typedNilIDs},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				store, err := NewEventStore(test.clock, test.ids)
				if err == nil || store != nil {
					t.Fatalf("NewEventStore() = (%#v, %v), want nil store and error", store, err)
				}
			}()
			if panicked {
				t.Fatal("NewEventStore() panicked for a nil dependency")
			}
		})
	}
}

func TestAppendRejectsEarlyWithoutReadingSources(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*EventStore)
		ctx     func() context.Context
		request application.AppendRequest
	}{
		{
			name: "canceled context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			request: validCreateRequest("session-early-canceled", "command-early-canceled"),
		},
		{name: "invalid Session ID", request: validCreateRequest(" session-early", "command-early-session")},
		{name: "invalid Command ID", request: validCreateRequest("session-early-command", "command-early ")},
		{
			name:    "empty Events",
			request: application.AppendRequest{SessionID: "session-early-empty", CommandID: "command-early-empty"},
		},
		{
			name: "unknown event",
			request: application.AppendRequest{
				SessionID: "session-early-unknown", CommandID: "command-early-unknown",
				Events: []domain.Event{unknownEvent{}},
			},
		},
		{
			name: "malformed event",
			request: application.AppendRequest{
				SessionID: "session-early-malformed", CommandID: "command-early-malformed",
				Events: []domain.Event{domain.SessionCreated{}},
			},
		},
		{
			name: "version conflict",
			prepare: func(store *EventStore) {
				if _, err := store.Append(context.Background(), validCreateRequest("session-early-conflict", "command-early-seed")); err != nil {
					t.Fatal(err)
				}
			},
			request: validCreateRequest("session-early-conflict", "command-early-conflict"),
		},
		{
			name: "injected append fault",
			prepare: func(store *EventStore) {
				store.FailNextAppend("session-early-fault", errors.New("injected"))
			},
			request: validCreateRequest("session-early-fault", "command-early-fault"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &countingClock{now: validTime()}
			ids := &countingIDs{}
			store := mustStore(t, clock, ids)
			if test.prepare != nil {
				test.prepare(store)
				clock.reset()
				ids.resetEventCalls()
			}
			ctx := context.Background()
			if test.ctx != nil {
				ctx = test.ctx()
			}
			if _, err := store.Append(ctx, test.request); err == nil {
				t.Fatal("Append() error = nil, want rejection")
			}
			if got := clock.callCount(); got != 0 {
				t.Fatalf("Clock.Now() calls = %d, want 0", got)
			}
			if got := ids.eventCallCount(); got != 0 {
				t.Fatalf("NewEventID() calls = %d, want 0", got)
			}
		})
	}
}

func TestSuccessfulAppendReadsClockOnceAndIDsPerEvent(t *testing.T) {
	clock := &countingClock{now: time.Date(2026, 8, 12, 9, 2, 3, 4, time.FixedZone("east", 8*60*60))}
	ids := &countingIDs{}
	store := mustStore(t, clock, ids)
	recorded, err := store.Append(context.Background(), application.AppendRequest{
		SessionID: "session-source-count", CommandID: "command-source-count",
		Events: []domain.Event{
			domain.SessionCreated{WorkspaceRoot: "/workspace"},
			domain.TurnStarted{TurnID: "turn-source-count", Input: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if got := clock.callCount(); got != 1 {
		t.Fatalf("Clock.Now() calls = %d, want 1", got)
	}
	if got := ids.eventCallCount(); got != 2 {
		t.Fatalf("NewEventID() calls = %d, want 2", got)
	}
	if len(recorded) != 2 || !recorded[0].OccurredAt.Equal(recorded[1].OccurredAt) {
		t.Fatalf("recorded timestamps = %#v, want one shared timestamp", recorded)
	}
	if recorded[0].OccurredAt.Location() != time.UTC {
		t.Fatalf("recorded timestamp location = %v, want UTC", recorded[0].OccurredAt.Location())
	}
}

func TestEventIDFailureLeavesStreamUnchangedAndPreservesCause(t *testing.T) {
	cause := &sourceError{operation: "event ID", value: 2}
	clock := &countingClock{now: validTime()}
	ids := &countingIDs{failEventAt: 2, failErr: fmt.Errorf("ID provider wrapper: %w", cause)}
	store := mustStore(t, clock, ids)
	_, err := store.Append(context.Background(), application.AppendRequest{
		SessionID: "session-id-failure", CommandID: "command-id-failure",
		Events: []domain.Event{
			domain.SessionCreated{WorkspaceRoot: "/workspace"},
			domain.TurnStarted{TurnID: "turn-id-failure", Input: "hello"},
		},
	})
	if !errors.Is(err, cause) {
		t.Fatalf("Append() error = %v, want source cause preserved", err)
	}
	var typed *sourceError
	if !errors.As(err, &typed) || typed != cause {
		t.Fatalf("errors.As() = %#v, want original source error", typed)
	}
	if !application.IsCategory(err, application.CategoryPersistence) {
		t.Fatalf("Append() category = %v, want persistence", err)
	}
	if clock.callCount() != 1 || ids.eventCallCount() != 2 {
		t.Fatalf("source calls = clock %d IDs %d, want clock 1 IDs 2", clock.callCount(), ids.eventCallCount())
	}
	assertEmptyStream(t, store, "session-id-failure")
}

func TestInvalidClockLeavesStreamUnchanged(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
	}{
		{name: "zero", now: time.Time{}},
		{name: "outside RFC3339 range", now: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &countingClock{now: test.now}
			ids := &countingIDs{}
			store := mustStore(t, clock, ids)
			_, err := store.Append(context.Background(), validCreateRequest("session-clock-failure", "command-clock-failure"))
			if err == nil || !application.IsCategory(err, application.CategoryPersistence) {
				t.Fatalf("Append() error = %v, want persistence error", err)
			}
			if clock.callCount() != 1 || ids.eventCallCount() != 0 {
				t.Fatalf("source calls = clock %d IDs %d, want clock 1 IDs 0", clock.callCount(), ids.eventCallCount())
			}
			assertEmptyStream(t, store, "session-clock-failure")
		})
	}
}

func TestEventIDsMustBeNonEmptyAndUniqueAcrossStream(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		store := mustStore(t, &countingClock{now: validTime()}, &countingIDs{eventIDs: []domain.EventID{""}})
		_, err := store.Append(context.Background(), validCreateRequest("session-empty-id", "command-empty-id"))
		if err == nil || !application.IsCategory(err, application.CategoryPersistence) {
			t.Fatalf("Append() error = %v, want persistence error", err)
		}
		assertEmptyStream(t, store, "session-empty-id")
	})

	t.Run("duplicate in batch", func(t *testing.T) {
		store := mustStore(t, &countingClock{now: validTime()}, &countingIDs{
			eventIDs: []domain.EventID{"event-duplicate", "event-duplicate"},
		})
		_, err := store.Append(context.Background(), application.AppendRequest{
			SessionID: "session-duplicate-batch", CommandID: "command-duplicate-batch",
			Events: []domain.Event{
				domain.SessionCreated{WorkspaceRoot: "/workspace"},
				domain.TurnStarted{TurnID: "turn-duplicate-batch", Input: "hello"},
			},
		})
		if err == nil || !application.IsCategory(err, application.CategoryPersistence) {
			t.Fatalf("Append() error = %v, want persistence error", err)
		}
		assertEmptyStream(t, store, "session-duplicate-batch")
	})

	t.Run("duplicate from prior batch", func(t *testing.T) {
		store := mustStore(t, &countingClock{now: validTime()}, &countingIDs{
			eventIDs: []domain.EventID{"event-existing", "event-existing"},
		})
		if _, err := store.Append(context.Background(), validCreateRequest("session-duplicate-stream", "command-duplicate-seed")); err != nil {
			t.Fatal(err)
		}
		_, err := store.Append(context.Background(), application.AppendRequest{
			SessionID: "session-duplicate-stream", ExpectedVersion: 1, CommandID: "command-duplicate-stream",
			Events: []domain.Event{domain.TurnStarted{TurnID: "turn-duplicate-stream", Input: "hello"}},
		})
		if err == nil || !application.IsCategory(err, application.CategoryPersistence) {
			t.Fatalf("Append() error = %v, want persistence error", err)
		}
		loaded, loadErr := store.Load(context.Background(), "session-duplicate-stream")
		if loadErr != nil || len(loaded) != 1 {
			t.Fatalf("Load() = (%#v, %v), want unchanged version 1", loaded, loadErr)
		}
	})
}

func TestEventIDsAreUniqueAcrossSessions(t *testing.T) {
	store := mustStore(t, &countingClock{now: validTime()}, &countingIDs{
		eventIDs: []domain.EventID{"event-global", "event-global"},
	})
	if _, err := store.Append(context.Background(), validCreateRequest("session-global-a", "command-global-a")); err != nil {
		t.Fatalf("Append(Session A) error = %v", err)
	}
	_, err := store.Append(context.Background(), validCreateRequest("session-global-b", "command-global-b"))
	assertDuplicateEventIDError(t, err)

	loadedA, err := store.Load(context.Background(), "session-global-a")
	if err != nil || len(loadedA) != 1 || loadedA[0].ID != "event-global" {
		t.Fatalf("Load(Session A) = (%#v, %v), want original committed record", loadedA, err)
	}
	loadedB, err := store.Load(context.Background(), "session-global-b")
	if err != nil || len(loadedB) != 0 {
		t.Fatalf("Load(Session B) = (%#v, %v), want unchanged empty stream", loadedB, err)
	}
}

func TestUncommittedEventIDsCanBeReused(t *testing.T) {
	t.Run("replay failure", func(t *testing.T) {
		store := mustStore(t, &countingClock{now: validTime()}, &countingIDs{
			eventIDs: []domain.EventID{"event-replay-reusable", "event-replay-reusable"},
		})
		_, err := store.Append(context.Background(), application.AppendRequest{
			SessionID: "session-replay-uncommitted", CommandID: "command-replay-uncommitted",
			Events: []domain.Event{domain.TurnStarted{TurnID: "turn-without-session", Input: "hello"}},
		})
		if err == nil {
			t.Fatal("Append(invalid candidate) error = nil, want replay failure")
		}
		recorded, err := store.Append(context.Background(), validCreateRequest("session-replay-reuse", "command-replay-reuse"))
		if err != nil {
			t.Fatalf("Append(reused ID) error = %v", err)
		}
		if len(recorded) != 1 || recorded[0].ID != "event-replay-reusable" {
			t.Fatalf("Append(reused ID) = %#v, want reusable uncommitted ID", recorded)
		}
	})

	t.Run("late cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ids := &cancelFirstEventIDs{
			IDGenerator: testkit.NewSequenceIDs(),
			cancel:      cancel,
			eventID:     "event-canceled-reusable",
		}
		store, err := NewEventStore(testkit.FixedClock{Time: validTime()}, ids)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Append(ctx, validCreateRequest("session-canceled-uncommitted", "command-canceled-uncommitted"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Append(canceled candidate) error = %v, want context.Canceled", err)
		}
		recorded, err := store.Append(context.Background(), validCreateRequest("session-canceled-reuse", "command-canceled-reuse"))
		if err != nil {
			t.Fatalf("Append(reused canceled ID) error = %v", err)
		}
		if len(recorded) != 1 || recorded[0].ID != "event-canceled-reusable" {
			t.Fatalf("Append(reused canceled ID) = %#v, want reusable uncommitted ID", recorded)
		}
	})
}

func TestReplayFailureLeavesStreamUnchanged(t *testing.T) {
	clock := &countingClock{now: validTime()}
	ids := &countingIDs{}
	store := mustStore(t, clock, ids)
	_, err := store.Append(context.Background(), application.AppendRequest{
		SessionID: "session-replay-failure", CommandID: "command-replay-failure",
		Events: []domain.Event{
			domain.SessionCreated{WorkspaceRoot: "/workspace"},
			domain.TurnCompleted{TurnID: "turn-never-started"},
		},
	})
	if err == nil || !application.IsCategory(err, application.CategoryPersistence) {
		t.Fatalf("Append() error = %v, want persistence replay error", err)
	}
	if clock.callCount() != 1 || ids.eventCallCount() != 2 {
		t.Fatalf("source calls = clock %d IDs %d, want clock 1 IDs 2", clock.callCount(), ids.eventCallCount())
	}
	assertEmptyStream(t, store, "session-replay-failure")
}

func TestLateCancellationConsumesOpaqueIDButDoesNotCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sequence := testkit.NewSequenceIDs()
	ids := &cancelingIDs{IDGenerator: sequence, cancel: cancel}
	store, err := NewEventStore(testkit.FixedClock{Time: validTime()}, ids)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Append(ctx, validCreateRequest("session-late-cancel", "command-late-cancel"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Append() error = %v, want context.Canceled", err)
	}
	assertEmptyStream(t, store, "session-late-cancel")

	recorded, err := store.Append(context.Background(), validCreateRequest("session-late-cancel", "command-after-cancel"))
	if err != nil {
		t.Fatalf("Append() after late cancellation error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].ID != "event-2" || recorded[0].Sequence != 1 {
		t.Fatalf("Append() after late cancellation = %#v, want opaque event-2 at contiguous sequence 1", recorded)
	}
}

func TestInjectedFaultsPreserveWrappedTypedCauses(t *testing.T) {
	store := mustStore(t, &countingClock{now: validTime()}, &countingIDs{})
	appendCause := &sourceError{operation: "append", value: 7}
	store.FailNextAppend("session-fault-cause", fmt.Errorf("append fault wrapper: %w", appendCause))
	_, err := store.Append(context.Background(), validCreateRequest("session-fault-cause", "command-fault-cause"))
	assertSourceCause(t, err, appendCause)
	loadCause := &sourceError{operation: "load", value: 9}
	store.FailNextLoad("session-fault-cause", fmt.Errorf("load fault wrapper: %w", loadCause))
	_, err = store.Load(context.Background(), "session-fault-cause")
	assertSourceCause(t, err, loadCause)
}

func TestCanceledLoadDoesNotConsumeOneShotFault(t *testing.T) {
	store := mustStore(t, &countingClock{now: validTime()}, &countingIDs{})
	cause := errors.New("load fault remains")
	store.FailNextLoad("session-canceled-load", cause)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Load(ctx, "session-canceled-load"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(canceled) error = %v, want context.Canceled", err)
	}
	if _, err := store.Load(context.Background(), "session-canceled-load"); !errors.Is(err, cause) {
		t.Fatalf("next Load() error = %v, want unconsumed injected cause", err)
	}
}

func TestConcurrentSameSessionAppendHasOneWinner(t *testing.T) {
	store, err := NewEventStore(testkit.FixedClock{Time: validTime()}, testkit.NewSequenceIDs())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			<-start
			_, err := store.Append(context.Background(), validCreateRequest(
				"session-race-same", domain.CommandID(fmt.Sprintf("command-race-%d", index)),
			))
			results <- err
		}()
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case application.IsVersionConflict(err):
			conflicts++
		default:
			t.Fatalf("concurrent Append() error = %v, want nil or version conflict", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results = %d successes, %d conflicts; want 1 and 1", successes, conflicts)
	}
	loaded, err := store.Load(context.Background(), "session-race-same")
	if err != nil || len(loaded) != 1 {
		t.Fatalf("Load() after race = (%d records, %v), want version 1", len(loaded), err)
	}
}

func TestConcurrentIndependentSessionsCommit(t *testing.T) {
	store, err := NewEventStore(testkit.FixedClock{Time: validTime()}, testkit.NewSequenceIDs())
	if err != nil {
		t.Fatal(err)
	}
	const count = 32
	start := make(chan struct{})
	results := make(chan error, count)
	for index := 0; index < count; index++ {
		index := index
		go func() {
			<-start
			_, err := store.Append(context.Background(), validCreateRequest(
				domain.SessionID(fmt.Sprintf("session-independent-%d", index)),
				domain.CommandID(fmt.Sprintf("command-independent-%d", index)),
			))
			results <- err
		}()
	}
	close(start)
	for range count {
		if err := <-results; err != nil {
			t.Fatalf("independent Append() error = %v", err)
		}
	}
	for index := 0; index < count; index++ {
		sessionID := domain.SessionID(fmt.Sprintf("session-independent-%d", index))
		loaded, err := store.Load(context.Background(), sessionID)
		if err != nil || len(loaded) != 1 || loaded[0].Sequence != 1 {
			t.Fatalf("Load(%q) = (%#v, %v), want one sequence-1 record", sessionID, loaded, err)
		}
	}
}

type unknownEvent struct{}

func (unknownEvent) EventType() string { return "test.unknown" }

type countingClock struct {
	mu    sync.Mutex
	now   time.Time
	calls int
}

func (clock *countingClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.calls++
	return clock.now
}
func (clock *countingClock) callCount() int {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.calls
}
func (clock *countingClock) reset() {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.calls = 0
}

type countingIDs struct {
	mu          sync.Mutex
	eventCalls  int
	failEventAt int
	failErr     error
	eventIDs    []domain.EventID
}

func (ids *countingIDs) NewSessionID() (domain.SessionID, error) { return "session-unused", nil }
func (ids *countingIDs) NewTurnID() (domain.TurnID, error)       { return "turn-unused", nil }
func (ids *countingIDs) NewItemID() (domain.ItemID, error)       { return "item-unused", nil }
func (ids *countingIDs) NewCommandID() (domain.CommandID, error) { return "command-unused", nil }
func (ids *countingIDs) NewAppendID() (domain.AppendID, error)   { return "append-unused", nil }
func (ids *countingIDs) NewEventID() (domain.EventID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.eventCalls++
	if ids.failEventAt != 0 && ids.eventCalls == ids.failEventAt {
		return "", ids.failErr
	}
	if ids.eventCalls <= len(ids.eventIDs) {
		return ids.eventIDs[ids.eventCalls-1], nil
	}
	return domain.EventID(fmt.Sprintf("event-counting-%d", ids.eventCalls)), nil
}
func (ids *countingIDs) eventCallCount() int {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	return ids.eventCalls
}
func (ids *countingIDs) resetEventCalls() {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.eventCalls = 0
}

type sourceError struct {
	operation string
	value     int
}

func (err *sourceError) Error() string {
	return fmt.Sprintf("%s source failed at %d", err.operation, err.value)
}

type cancelingIDs struct {
	application.IDGenerator
	cancel context.CancelFunc
}

type cancelFirstEventIDs struct {
	application.IDGenerator
	cancel  context.CancelFunc
	eventID domain.EventID
	calls   int
}

func (ids *cancelFirstEventIDs) NewEventID() (domain.EventID, error) {
	ids.calls++
	if ids.calls == 1 {
		ids.cancel()
	}
	return ids.eventID, nil
}

func (ids *cancelingIDs) NewEventID() (domain.EventID, error) {
	eventID, err := ids.IDGenerator.NewEventID()
	ids.cancel()
	return eventID, err
}

func validTime() time.Time { return time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC) }

func validCreateRequest(sessionID domain.SessionID, commandID domain.CommandID) application.AppendRequest {
	return application.AppendRequest{
		SessionID: sessionID, CommandID: commandID,
		Events: []domain.Event{domain.SessionCreated{WorkspaceRoot: "/workspace"}},
	}
}

func mustStore(t *testing.T, clock application.Clock, ids application.IDGenerator) *EventStore {
	t.Helper()
	store, err := NewEventStore(clock, ids)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func assertEmptyStream(t *testing.T, store *EventStore, sessionID domain.SessionID) {
	t.Helper()
	loaded, err := store.Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("Load() records = %d, want 0", len(loaded))
	}
}

func assertSourceCause(t *testing.T, err error, want *sourceError) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want source cause preserved", err)
	}
	var got *sourceError
	if !errors.As(err, &got) || got != want {
		t.Fatalf("errors.As() = %#v, want original source error", got)
	}
	if !application.IsCategory(err, application.CategoryPersistence) {
		t.Fatalf("error category = %v, want persistence", err)
	}
}

func assertDuplicateEventIDError(t *testing.T, err error) {
	t.Helper()
	var applicationError *application.Error
	if !errors.As(err, &applicationError) {
		t.Fatalf("Append() error = %v, want *application.Error", err)
	}
	if applicationError.Category != application.CategoryPersistence || applicationError.Code != "event_store_duplicate_event_id" {
		t.Fatalf("Append() application error = %#v, want persistence/event_store_duplicate_event_id", applicationError)
	}
	if got, want := err.Error(), "persistence/event_store_duplicate_event_id (terminal_committed=false)"; got != want {
		t.Fatalf("Append() error text = %q, want stable %q", got, want)
	}
}
