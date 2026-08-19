package application_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestUnknownOutcomeResolveCommittedDoesNotCallModelTwice(t *testing.T) {
	base := newTurnMemoryStore(t)
	unknown, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, MayHaveCommitted: true})
	if err != nil {
		t.Fatal(err)
	}
	store := base
	model := &repeatingSuccessModel{text: "done"}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store.FailNext(memory.FaultAfterCommitBeforeAck, unknown)
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-unknown-committed", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Status != domain.TurnStatusCompleted || !result.TerminalCommitted || len(model.Calls()) != 1 {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, len(model.Calls()))
	}
}

func TestUnknownTerminalNotFoundExactAppendDoesNotCallModelAgain(t *testing.T) {
	base := newTurnMemoryStore(t)
	ids := testkit.NewSequenceIDs()
	seed := newTurnService(t, base, ids, &repeatingSuccessModel{text: "seed"})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store := &exactRetryAfterUnknownStore{EventStore: base}
	model := &repeatingSuccessModel{text: "done"}
	service := newTurnService(t, store, ids, model)
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-unknown-retry", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Status != domain.TurnStatusCompleted || !result.TerminalCommitted || len(model.Calls()) != 1 || store.terminalUnknowns != 1 || store.terminalRetries != 1 {
		t.Fatalf("result=%#v err=%v calls=%d unknowns=%d retries=%d", result, err, len(model.Calls()), store.terminalUnknowns, store.terminalRetries)
	}
}

func TestUnknownAdmissionCanceledAfterCommitAbandonsWithoutModel(t *testing.T) {
	base := newTurnMemoryStore(t)
	unknown, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, MayHaveCommitted: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store := &cancelOnAdmissionUnknownStore{EventStore: base, cancel: cancel, unknown: unknown}
	model := &repeatingSuccessModel{text: "unused"}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-abandoned", Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, runErr, application.CategoryCanceled, domain.InterruptionRequestAbandoned, true)
	if result.Status != domain.TurnStatusInterrupted || !result.TerminalCommitted || len(model.Calls()) != 0 {
		t.Fatalf("result=%#v calls=%d", result, len(model.Calls()))
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	types := turnEventTypes(records)
	if !reflect.DeepEqual(types[len(types)-2:], []string{domain.EventAssistantMessageInterrupted, domain.EventTurnInterrupted}) {
		t.Fatalf("records=%v", types)
	}
}

func TestUnknownOutcomeWaiterDoesNotStartSecondResolver(t *testing.T) {
	base := newTurnMemoryStore(t)
	unknown, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, MayHaveCommitted: true})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	store := &holdResolveStore{EventStore: base, started: started, release: release}
	model := &repeatingSuccessModel{text: "done"}
	ids := testkit.NewSequenceIDs()
	seed := newTurnService(t, base, ids, &repeatingSuccessModel{text: "seed"})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	service := newTurnService(t, store, ids, model)
	store.unknown = unknown
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	ownerDone := make(chan outcome, 1)
	go func() {
		result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-shared-resolver", Input: "inspect", Sink: &testkit.RecordingSink{}})
		ownerDone <- outcome{result, runErr}
	}()
	select {
	case <-started:
	case <-time.After(testRendezvousTimeout):
		t.Fatal("owner did not enter resolve")
	}
	waiterDone := make(chan outcome, 1)
	go func() {
		result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-shared-resolver", Input: "inspect", Sink: &testkit.RecordingSink{}})
		waiterDone <- outcome{result, runErr}
	}()
	// Early signal only: a short window can under-report a second resolver, so
	// it may pass spuriously but never fail spuriously. The authoritative check
	// is the post-completion count below.
	time.Sleep(20 * time.Millisecond)
	if store.resolveCalls() != 1 {
		t.Fatalf("resolver calls=%d", store.resolveCalls())
	}
	close(release)
	owner := awaitOutcome(t, ownerDone, "owner")
	waiter := awaitOutcome(t, waiterDone, "waiter")
	if owner.err != nil || waiter.err != nil || !reflect.DeepEqual(owner.result.TurnID, waiter.result.TurnID) || len(model.Calls()) != 1 {
		t.Fatalf("owner=%#v waiter=%#v calls=%d", owner, waiter, len(model.Calls()))
	}
	if store.resolveCalls() != 1 {
		t.Fatalf("resolver calls after both returned = %d, want 1", store.resolveCalls())
	}
}

func TestUnresolvedSessionRejectsDifferentAdmission(t *testing.T) {
	registryUnknown := &holdResolveStore{EventStore: newTurnMemoryStore(t), started: make(chan struct{}), release: make(chan struct{})}
	unknown, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, MayHaveCommitted: true})
	if err != nil {
		t.Fatal(err)
	}
	registryUnknown.unknown = unknown
	ids := testkit.NewSequenceIDs()
	seed := newTurnService(t, registryUnknown.EventStore, ids, &repeatingSuccessModel{text: "seed"})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	service := newTurnService(t, registryUnknown, ids, &repeatingSuccessModel{text: "done"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-unresolved-owner", Input: "inspect", Sink: &testkit.RecordingSink{}})
	}()
	select {
	case <-registryUnknown.started:
	case <-time.After(testRendezvousTimeout):
		t.Fatal("owner did not retain unknown")
	}
	_, otherErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-other", Input: "other", Sink: &testkit.RecordingSink{}})
	if !isUnknown(otherErr) {
		t.Fatalf("other err=%v", otherErr)
	}
	close(registryUnknown.release)
	select {
	case <-done:
	case <-time.After(testRendezvousTimeout):
		t.Fatal("owner did not finish")
	}
}

func isUnknown(err error) bool {
	var appErr *application.Error
	return errors.As(err, &appErr) && appErr.Code == "append_outcome_unknown"
}

type exactRetryAfterUnknownStore struct {
	application.EventStore
	terminalUnknowns int
	terminalRetries  int
}

func (store *exactRetryAfterUnknownStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	if len(request.Events) > 0 && request.Events[0].Event.EventType() == domain.EventAssistantMessageCompleted {
		if store.terminalUnknowns == 0 {
			store.terminalUnknowns++
			unknown, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, SessionID: request.SessionID, MayHaveCommitted: true})
			if err != nil {
				return application.CommitReceipt{}, err
			}
			return application.CommitReceipt{}, unknown
		}
		store.terminalRetries++
	}
	return store.EventStore.Append(ctx, request)
}

func (store *exactRetryAfterUnknownStore) ResolveAppend(ctx context.Context, request application.ResolveAppendRequest) (application.AppendResolution, error) {
	if store.terminalUnknowns == 1 && store.terminalRetries == 0 {
		return application.AppendResolution{Kind: application.AppendResolutionNotFound}, nil
	}
	return store.EventStore.ResolveAppend(ctx, request)
}

type cancelOnAdmissionUnknownStore struct {
	application.EventStore
	cancel  context.CancelFunc
	unknown error
}

func (store *cancelOnAdmissionUnknownStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	if request.Admission != nil {
		receipt, err := store.EventStore.Append(ctx, request)
		if err != nil {
			return receipt, err
		}
		store.cancel()
		return application.CommitReceipt{}, store.unknown
	}
	return store.EventStore.Append(ctx, request)
}

type holdResolveStore struct {
	application.EventStore
	unknown  error
	started  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	resolves int
}

func (store *holdResolveStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	if request.Admission != nil && store.unknown != nil {
		receipt, err := store.EventStore.Append(ctx, request)
		if err != nil {
			return receipt, err
		}
		return application.CommitReceipt{}, store.unknown
	}
	return store.EventStore.Append(ctx, request)
}

func (store *holdResolveStore) ResolveAppend(ctx context.Context, request application.ResolveAppendRequest) (application.AppendResolution, error) {
	store.mu.Lock()
	store.resolves++
	store.mu.Unlock()
	select {
	case store.started <- struct{}{}:
	default:
	}
	select {
	case <-store.release:
	case <-ctx.Done():
		return application.AppendResolution{}, ctx.Err()
	}
	return store.EventStore.ResolveAppend(ctx, request)
}

func (store *holdResolveStore) resolveCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.resolves
}
