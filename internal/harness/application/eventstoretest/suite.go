// Package eventstoretest provides a reusable behavioral contract for
// application.EventStore implementations.
package eventstoretest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type Harness struct {
	Store              application.EventStore
	ExpectedOccurredAt time.Time
	FailNextLoad       func(domain.SessionID, error)
	FailNextAppend     func(domain.SessionID, error)
}

type Factory func(*testing.T) Harness

func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("contiguous metadata and load", func(t *testing.T) { testContiguousMetadataAndLoad(t, factory(t)) })
	t.Run("compare and swap conflict", func(t *testing.T) { testCompareAndSwapConflict(t, factory(t)) })
	t.Run("atomic injected failure", func(t *testing.T) { testAtomicInjectedFailure(t, factory(t)) })
	t.Run("one-shot load failure", func(t *testing.T) { testOneShotLoadFailure(t, factory(t)) })
	t.Run("canceled append", func(t *testing.T) { testCanceledAppend(t, factory(t)) })
	t.Run("post-commit cancellation returns committed batch", func(t *testing.T) {
		testPostCommitCancellation(t, factory(t))
	})
	t.Run("post-commit barrier reports append rejection", func(t *testing.T) {
		testPostCommitBarrierReportsRejection(t)
	})
	t.Run("defensive copies", func(t *testing.T) { testDefensiveCopies(t, factory(t)) })
}

func testPostCommitCancellation(t *testing.T, harness Harness) {
	t.Helper()
	requireHarness(t, harness)
	ctx, cancel := context.WithCancel(context.Background())
	arrived := make(chan appendArrival, 1)
	release := make(chan struct{})
	store := &postCommitBarrierStore{
		EventStore: harness.Store,
		arrived:    arrived,
		release:    release,
	}
	request := application.AppendRequest{
		SessionID: "session-contract-post-commit", CommandID: "command-contract-post-commit",
		Events: []domain.Event{domain.SessionCreated{WorkspaceRoot: "/post-commit"}},
	}

	type result struct {
		records []domain.RecordedEvent
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		records, err := store.Append(ctx, request)
		resultCh <- result{records: records, err: err}
	}()

	arrival := <-arrived
	if arrival.err != nil {
		t.Fatalf("underlying Append() error = %v", arrival.err)
	}
	cancel()
	close(release)
	got := <-resultCh
	if got.err != nil || !equalRecords(got.records, arrival.records) {
		t.Fatalf("Append() after post-commit cancellation = (%#v, %v), want exact committed records", got.records, got.err)
	}
	loaded, err := harness.Store.Load(context.Background(), request.SessionID)
	if err != nil || !equalRecords(loaded, arrival.records) {
		t.Fatalf("Load() after post-commit cancellation = (%#v, %v), want committed batch %#v", loaded, err, arrival.records)
	}
}

func testPostCommitBarrierReportsRejection(t *testing.T) {
	t.Helper()
	cause := errors.New("append rejected before commit")
	arrived := make(chan appendArrival, 1)
	release := make(chan struct{})
	store := &postCommitBarrierStore{
		EventStore: rejectingStore{err: cause},
		arrived:    arrived,
		release:    release,
	}
	resultCh := make(chan error, 1)
	go func() {
		_, err := store.Append(context.Background(), application.AppendRequest{})
		resultCh <- err
	}()

	select {
	case arrival := <-arrived:
		if !errors.Is(arrival.err, cause) || len(arrival.records) != 0 {
			t.Fatalf("arrival = %#v, want rejecting cause and no records", arrival)
		}
	case <-time.After(time.Second):
		t.Fatal("barrier did not report the rejecting Append return")
	}
	select {
	case err := <-resultCh:
		if !errors.Is(err, cause) {
			t.Fatalf("wrapper Append() error = %v, want rejecting cause", err)
		}
	case <-time.After(time.Second):
		t.Fatal("rejecting Append waited for the success-only release barrier")
	}
}

type rejectingStore struct {
	err error
}

func (store rejectingStore) Load(context.Context, domain.SessionID) ([]domain.RecordedEvent, error) {
	return nil, store.err
}

func (store rejectingStore) Append(context.Context, application.AppendRequest) ([]domain.RecordedEvent, error) {
	return nil, store.err
}

type postCommitBarrierStore struct {
	application.EventStore
	arrived chan<- appendArrival
	release <-chan struct{}
}

type appendArrival struct {
	records []domain.RecordedEvent
	err     error
}

func (store *postCommitBarrierStore) Append(ctx context.Context, request application.AppendRequest) ([]domain.RecordedEvent, error) {
	records, err := store.EventStore.Append(ctx, request)
	store.arrived <- appendArrival{records: records, err: err}
	if err != nil {
		return nil, err
	}
	<-store.release
	return records, nil
}

func equalRecords(left, right []domain.RecordedEvent) bool {
	return reflect.DeepEqual(left, right)
}

func testContiguousMetadataAndLoad(t *testing.T, harness Harness) {
	t.Helper()
	requireHarness(t, harness)
	ctx := context.Background()
	sessionID := domain.SessionID("session-contract-metadata")
	first, err := harness.Store.Append(ctx, application.AppendRequest{
		SessionID: sessionID, CommandID: "command-contract-first",
		Events: []domain.Event{
			domain.SessionCreated{WorkspaceRoot: "/workspace"},
			domain.TurnStarted{TurnID: "turn-contract", Input: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first Append() records = %d, want 2", len(first))
	}
	assertBatchMetadata(t, first, sessionID, "command-contract-first", 1, harness.ExpectedOccurredAt)

	second, err := harness.Store.Append(ctx, application.AppendRequest{
		SessionID: sessionID, ExpectedVersion: 2, CommandID: "command-contract-second",
		Events: []domain.Event{
			domain.AssistantMessageStarted{TurnID: "turn-contract", ItemID: "item-contract"},
			domain.AssistantMessageCompleted{TurnID: "turn-contract", ItemID: "item-contract", Text: "world"},
			domain.TurnCompleted{TurnID: "turn-contract"},
		},
	})
	if err != nil {
		t.Fatalf("second Append() error = %v", err)
	}
	if len(second) != 3 {
		t.Fatalf("second Append() records = %d, want only the 3 newly recorded events", len(second))
	}
	assertBatchMetadata(t, second, sessionID, "command-contract-second", 3, harness.ExpectedOccurredAt)

	loaded, err := harness.Store.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != 5 {
		t.Fatalf("Load() records = %d, want complete stream length 5", len(loaded))
	}
	for index, record := range loaded {
		if want := uint64(index + 1); record.Sequence != want {
			t.Errorf("loaded record %d sequence = %d, want %d", index, record.Sequence, want)
		}
		if want := harness.ExpectedOccurredAt.UTC(); record.OccurredAt != want {
			t.Errorf("loaded record %d timestamp = %v, want exact injected instant %v", index, record.OccurredAt, want)
		}
	}
	absent, err := harness.Store.Load(ctx, "session-contract-absent")
	if err != nil || len(absent) != 0 {
		t.Fatalf("Load(absent) = (%#v, %v), want empty and no error", absent, err)
	}
}

func testCompareAndSwapConflict(t *testing.T, harness Harness) {
	t.Helper()
	requireHarness(t, harness)
	ctx := context.Background()
	sessionID := domain.SessionID("session-contract-conflict")
	request := application.AppendRequest{
		SessionID: sessionID, CommandID: "command-contract-winner",
		Events: []domain.Event{domain.SessionCreated{WorkspaceRoot: "/winner"}},
	}
	if _, err := harness.Store.Append(ctx, request); err != nil {
		t.Fatalf("first version-zero Append() error = %v", err)
	}
	request.CommandID = "command-contract-loser"
	request.Events = []domain.Event{domain.SessionCreated{WorkspaceRoot: "/loser"}}
	_, err := harness.Store.Append(ctx, request)
	var conflict *application.VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second version-zero Append() error = %v, want typed conflict", err)
	}
	if conflict.SessionID != sessionID || conflict.ExpectedVersion != 0 || conflict.ActualVersion != 1 {
		t.Fatalf("conflict = %#v, want session %q expected 0 actual 1", conflict, sessionID)
	}
	loaded, loadErr := harness.Store.Load(ctx, sessionID)
	if loadErr != nil || len(loaded) != 1 {
		t.Fatalf("Load() after conflict = (%#v, %v), want version 1", loaded, loadErr)
	}
}

func testAtomicInjectedFailure(t *testing.T, harness Harness) {
	t.Helper()
	requireHarness(t, harness)
	ctx := context.Background()
	sessionID := domain.SessionID("session-contract-append-fault")
	if _, err := harness.Store.Append(ctx, application.AppendRequest{
		SessionID: sessionID, CommandID: "command-contract-create",
		Events: []domain.Event{domain.SessionCreated{WorkspaceRoot: "/workspace"}},
	}); err != nil {
		t.Fatalf("seed Append() error = %v", err)
	}
	cause := errors.New("contract append fault")
	harness.FailNextAppend(sessionID, cause)
	otherID := domain.SessionID("session-contract-append-other")
	if _, err := harness.Store.Append(ctx, application.AppendRequest{
		SessionID: otherID, CommandID: "command-contract-other",
		Events: []domain.Event{domain.SessionCreated{WorkspaceRoot: "/other"}},
	}); err != nil {
		t.Fatalf("Append(other) while fault pending error = %v", err)
	}
	_, err := harness.Store.Append(ctx, application.AppendRequest{
		SessionID: sessionID, ExpectedVersion: 1, CommandID: "command-contract-faulted",
		Events: []domain.Event{
			domain.TurnStarted{TurnID: "turn-contract-fault", Input: "hello"},
			domain.TurnCompleted{TurnID: "turn-contract-fault"},
		},
	})
	if !errors.Is(err, cause) || !application.IsCategory(err, application.CategoryPersistence) {
		t.Fatalf("faulted Append() error = %v, want persistence error preserving cause", err)
	}
	loaded, loadErr := harness.Store.Load(ctx, sessionID)
	if loadErr != nil || len(loaded) != 1 || loaded[0].Sequence != 1 {
		t.Fatalf("stream after failed append = (%#v, %v), want unchanged version 1", loaded, loadErr)
	}
	if _, err := harness.Store.Append(ctx, application.AppendRequest{
		SessionID: sessionID, ExpectedVersion: 1, CommandID: "command-contract-retry",
		Events: []domain.Event{
			domain.TurnStarted{TurnID: "turn-contract-retry", Input: "hello"},
			domain.TurnCompleted{TurnID: "turn-contract-retry"},
		},
	}); err != nil {
		t.Fatalf("Append() after one-shot fault error = %v", err)
	}
}

func testOneShotLoadFailure(t *testing.T, harness Harness) {
	t.Helper()
	requireHarness(t, harness)
	ctx := context.Background()
	sessionID := domain.SessionID("session-contract-load-fault")
	if _, err := harness.Store.Append(ctx, application.AppendRequest{
		SessionID: sessionID, CommandID: "command-contract-load-seed",
		Events: []domain.Event{domain.SessionCreated{WorkspaceRoot: "/load"}},
	}); err != nil {
		t.Fatalf("seed Append() error = %v", err)
	}
	cause := errors.New("contract load fault")
	harness.FailNextLoad(sessionID, cause)
	other, err := harness.Store.Load(ctx, "session-contract-load-other")
	if err != nil || len(other) != 0 {
		t.Fatalf("Load(other) = (%#v, %v), want empty and no error", other, err)
	}
	if _, err := harness.Store.Load(ctx, sessionID); !errors.Is(err, cause) || !application.IsCategory(err, application.CategoryPersistence) {
		t.Fatalf("first Load(faulted) error = %v, want persistence error preserving cause", err)
	}
	loaded, err := harness.Store.Load(ctx, sessionID)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("second Load() = (%#v, %v), want one record", loaded, err)
	}
	loaded[0].Event = domain.SessionCreated{WorkspaceRoot: "/mutated"}
	fresh, err := harness.Store.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("fresh Load() error = %v", err)
	}
	created, ok := fresh[0].Event.(domain.SessionCreated)
	if !ok || created.WorkspaceRoot != "/load" {
		t.Fatalf("fresh Load() event = %#v, want original defensive copy", fresh[0].Event)
	}
}

func testCanceledAppend(t *testing.T, harness Harness) {
	t.Helper()
	requireHarness(t, harness)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sessionID := domain.SessionID("session-contract-canceled")
	_, err := harness.Store.Append(ctx, application.AppendRequest{
		SessionID: sessionID, CommandID: "command-contract-canceled",
		Events: []domain.Event{domain.SessionCreated{WorkspaceRoot: "/canceled"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Append(canceled) error = %v, want context.Canceled", err)
	}
	loaded, loadErr := harness.Store.Load(context.Background(), sessionID)
	if loadErr != nil || len(loaded) != 0 {
		t.Fatalf("Load() after canceled append = (%#v, %v), want empty", loaded, loadErr)
	}
}

func testDefensiveCopies(t *testing.T, harness Harness) {
	t.Helper()
	requireHarness(t, harness)
	ctx := context.Background()
	sessionID := domain.SessionID("session-contract-copies")
	source := []domain.Event{domain.SessionCreated{WorkspaceRoot: "/original"}}
	returned, err := harness.Store.Append(ctx, application.AppendRequest{
		SessionID: sessionID, CommandID: "command-contract-copies", Events: source,
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	source[0] = domain.SessionCreated{WorkspaceRoot: "/mutated-source"}
	returned[0].Sequence = 99
	returned[0].Event = domain.SessionCreated{WorkspaceRoot: "/mutated-return"}
	loaded, err := harness.Store.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("first Load() error = %v", err)
	}
	loaded[0].CommandID = "command-mutated-load"
	loaded[0].Event = domain.SessionCreated{WorkspaceRoot: "/mutated-load"}
	fresh, err := harness.Store.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("fresh Load() error = %v", err)
	}
	if len(fresh) != 1 || fresh[0].Sequence != 1 || fresh[0].CommandID != "command-contract-copies" {
		t.Fatalf("fresh Load() metadata = %#v, want original", fresh)
	}
	created, ok := fresh[0].Event.(domain.SessionCreated)
	if !ok || created.WorkspaceRoot != "/original" {
		t.Fatalf("fresh Load() event = %#v, want original", fresh[0].Event)
	}
}

func requireHarness(t *testing.T, harness Harness) {
	t.Helper()
	if harness.Store == nil || harness.ExpectedOccurredAt.IsZero() || harness.FailNextLoad == nil || harness.FailNextAppend == nil {
		t.Fatalf("incomplete EventStore harness: %#v", harness)
	}
}

func assertBatchMetadata(t *testing.T, records []domain.RecordedEvent, sessionID domain.SessionID, commandID domain.CommandID, firstSequence uint64, expectedOccurredAt time.Time) {
	t.Helper()
	ids := make(map[domain.EventID]struct{}, len(records))
	var occurredAt time.Time
	for index, record := range records {
		if record.SchemaVersion != 1 {
			t.Errorf("record %d schema version = %d, want 1", index, record.SchemaVersion)
		}
		if record.SessionID != sessionID || record.CommandID != commandID {
			t.Errorf("record %d identity = (%q, %q), want (%q, %q)", index, record.SessionID, record.CommandID, sessionID, commandID)
		}
		if want := firstSequence + uint64(index); record.Sequence != want {
			t.Errorf("record %d sequence = %d, want %d", index, record.Sequence, want)
		}
		if record.ID == "" {
			t.Errorf("record %d ID is empty", index)
		}
		if _, duplicate := ids[record.ID]; duplicate {
			t.Errorf("record %d ID %q is duplicated within batch", index, record.ID)
		}
		ids[record.ID] = struct{}{}
		if record.OccurredAt.Location() != time.UTC {
			t.Errorf("record %d timestamp location = %v, want UTC", index, record.OccurredAt.Location())
		}
		if want := expectedOccurredAt.UTC(); record.OccurredAt != want {
			t.Errorf("record %d timestamp = %v, want exact injected instant %v", index, record.OccurredAt, want)
		}
		if index == 0 {
			occurredAt = record.OccurredAt
		} else if !record.OccurredAt.Equal(occurredAt) {
			t.Errorf("record %d timestamp = %v, want shared batch timestamp %v", index, record.OccurredAt, occurredAt)
		}
	}
}
