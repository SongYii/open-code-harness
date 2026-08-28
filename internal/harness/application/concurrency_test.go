package application_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestConcurrentRunTurnSameSessionHasOneAtomicAdmissionWinner(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	base, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	ids := testkit.NewSequenceIDs()
	seed := newAcceptanceService(t, base, ids, &acceptanceSuccessModel{text: "seed"})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	lookupGate := newLookupRaceBarrier(base, 32)
	model := newBlockingAcceptanceModel("done")
	service := newAcceptanceService(t, lookupGate, ids, model)

	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	start := make(chan struct{})
	done := make(chan outcome, 32)
	for index := 0; index < 32; index++ {
		go func() {
			<-start
			result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{
				SessionID: created.SessionID,
				RequestID: "request-concurrent",
				Input:     "inspect",
				Sink:      &testkit.RecordingSink{},
			})
			done <- outcome{result: result, err: runErr}
		}()
	}
	close(start)
	await(t, lookupGate.ready, "all initial request lookups")
	close(lookupGate.release)
	select {
	case <-model.started:
	case <-time.After(testRendezvousTimeout):
		t.Fatal("owner did not enter blocking model")
	}
	mismatchDone := make(chan error, 1)
	go func() {
		_, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-concurrent", Input: "changed", Sink: &testkit.RecordingSink{}})
		mismatchDone <- err
	}()
	mismatch := awaitOutcome(t, mismatchDone, "changed-input rejection")
	var mismatchErr *application.Error
	if !errors.As(mismatch, &mismatchErr) || mismatchErr.Code != "command_identity_mismatch" || len(model.Calls()) != 1 {
		t.Fatalf("changed input error=%v model=%d", mismatch, len(model.Calls()))
	}
	beforeRelease, err := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
	if err != nil || !reflect.DeepEqual(acceptanceEventTypes(beforeRelease), []string{domain.EventSessionCreated, domain.EventTurnStarted, domain.EventAssistantMessageStarted}) {
		t.Fatalf("pre-release records=%#v err=%v", beforeRelease, err)
	}
	model.releaseOnce()

	successes := 0
	var first application.RunTurnResult
	for range 32 {
		got := awaitOutcome(t, done, "same-request caller result")
		if got.err != nil {
			t.Fatalf("RunTurn() unexpected error = %v, result = %#v", got.err, got.result)
		}
		successes++
		if got.result.Status != domain.TurnStatusCompleted || !got.result.TerminalCommitted {
			t.Fatalf("result = %#v", got.result)
		}
		if first.TurnID == "" {
			first = got.result
		} else if !reflect.DeepEqual(got.result, first) {
			t.Fatalf("duplicate result = %#v, want same execution as %#v", got.result, first)
		}
	}
	if successes != 32 || len(model.Calls()) != 1 {
		t.Fatalf("successes=%d model calls=%d", successes, len(model.Calls()))
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{
		domain.EventSessionCreated,
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventAssistantMessageCompleted,
		domain.EventTurnCompleted,
	}
	if got := acceptanceEventTypes(records); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("durable event types = %v, want one complete atomic lifecycle %v", got, wantTypes)
	}
	state, err := domain.Replay(records)
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 5 || state.ActiveTurn != nil {
		t.Fatalf("replayed state = %#v", state)
	}
}

func TestFoundRunningAttachesLocalWaiterAndCancellationIsIsolated(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	base, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	ids := testkit.NewSequenceIDs()
	seed := newAcceptanceService(t, base, ids, &acceptanceSuccessModel{text: "seed"})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	observer := &foundLookupObserver{EventStore: base, found: make(chan struct{}, 4)}
	model := newBlockingAcceptanceModel("done")
	service := newAcceptanceService(t, observer, ids, model)
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	ownerDone := make(chan outcome, 1)
	request := application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-live", Input: "inspect", Sink: &testkit.RecordingSink{}}
	go func() {
		result, err := service.RunTurn(context.Background(), request)
		ownerDone <- outcome{result, err}
	}()
	await(t, model.started, "owner durable running admission")
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	canceledDone := make(chan outcome, 1)
	go func() { result, err := service.RunTurn(waiterCtx, request); canceledDone <- outcome{result, err} }()
	await(t, observer.found, "cancelable waiter found-running lookup")
	awaitRegistryLeases(t, service, request.RequestID, 2, "cancelable waiter attach")
	cancelWaiter()
	canceled := awaitOutcome(t, canceledDone, "cancelable waiter result")
	if !application.IsCategory(canceled.err, application.CategoryCanceled) {
		t.Fatalf("canceled waiter=%#v", canceled)
	}
	awaitRegistryLeases(t, service, request.RequestID, 1, "canceled waiter detach")
	select {
	case owner := <-ownerDone:
		t.Fatalf("owner returned before release: %#v", owner)
	default:
	}
	attachedDone := make(chan outcome, 1)
	go func() {
		result, err := service.RunTurn(context.Background(), request)
		attachedDone <- outcome{result, err}
	}()
	await(t, observer.found, "attached waiter found-running lookup")
	awaitRegistryLeases(t, service, request.RequestID, 2, "attached waiter lease")
	model.releaseOnce()
	owner := awaitOutcome(t, ownerDone, "owner terminal result")
	attached := awaitOutcome(t, attachedDone, "attached waiter terminal result")
	if owner.err != nil || attached.err != nil || !reflect.DeepEqual(owner.result, attached.result) || len(model.Calls()) != 1 {
		t.Fatalf("owner=%#v attached=%#v calls=%d", owner, attached, len(model.Calls()))
	}
	if records, err := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256); err != nil || len(records) != 5 {
		t.Fatalf("records=%#v err=%v", records, err)
	}
}

func awaitRegistryLeases(t *testing.T, service *application.Service, requestID domain.RunTurnRequestID, want uint32, description string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := application.ExecutionRegistrySnapshotForTest(service, requestID)
		if snapshot.Present && snapshot.Leases == want {
			return
		}
		runtime.Gosched()
	}
	snapshot := application.ExecutionRegistrySnapshotForTest(service, requestID)
	t.Fatalf("timed out waiting for %s: snapshot=%#v want leases=%d", description, snapshot, want)
}

type foundLookupObserver struct {
	application.EventStore
	found chan struct{}
}

func (store *foundLookupObserver) FindCommandRequest(ctx context.Context, request application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	lookup, err := store.EventStore.FindCommandRequest(ctx, request)
	if err == nil && lookup.Kind == application.CommandRequestLookupFound {
		select {
		case store.found <- struct{}{}:
		default:
		}
	}
	return lookup, err
}

// testRendezvousTimeout bounds goroutine rendezvous in tests. It fires only
// when a test is genuinely stuck, so it is deliberately generous: a tight
// bound turns CPU contention under -race into a false failure rather than a
// real signal. Negative assertions must not use it.
const testRendezvousTimeout = 30 * time.Second

// fatalStalled fails a rendezvous with every goroutine stack attached.
//
// A rendezvous that misses testRendezvousTimeout is not slow, it is stuck,
// and the message alone says only which channel never fired. One such stall
// has been observed twice in this package and reproduced in neither of the
// thirty-five runs that followed, so the next occurrence has to carry its own
// diagnosis or it will be lost again.
func fatalStalled(t *testing.T, description string) {
	t.Helper()
	buffer := make([]byte, 1<<20)
	buffer = buffer[:runtime.Stack(buffer, true)]
	t.Fatalf("timed out waiting for %s after %s; all goroutine stacks follow:\n%s",
		description, testRendezvousTimeout, buffer)
}

func await(t *testing.T, channel <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(testRendezvousTimeout):
		fatalStalled(t, description)
	}
}

func awaitOutcome[T any](t *testing.T, channel <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(testRendezvousTimeout):
		var zero T
		fatalStalled(t, description)
		return zero
	}
}

func TestConcurrentRunTurnAcrossServicesReconcilesDurableAdmissionWinner(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	base, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	seed := newAcceptanceService(t, base, newPrefixedIDs("seed"), &acceptanceSuccessModel{text: "seed"})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	modelA, modelB := newBlockingAcceptanceModel("done"), newBlockingAcceptanceModel("done")
	serviceA := newAcceptanceService(t, base, newPrefixedIDs("runtime-a"), modelA)
	serviceB := newAcceptanceService(t, base, newPrefixedIDs("runtime-b"), modelB)
	type outcome struct{ err error }
	done := make(chan outcome, 1)
	go func() {
		_, err := serviceA.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-cross-process", Input: "inspect", Sink: &testkit.RecordingSink{}})
		done <- outcome{err: err}
	}()
	await(t, modelA.started, "cross-service durable running owner")
	_, loserErr := serviceB.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-cross-process", Input: "inspect", Sink: &testkit.RecordingSink{}})
	var appErr *application.Error
	if !errors.As(loserErr, &appErr) || appErr.Code != "reconciliation_required" {
		t.Fatalf("cross-service loser error = %v", loserErr)
	}
	modelA.releaseOnce()
	second := awaitOutcome(t, done, "cross-service winner")
	if second.err != nil {
		t.Fatalf("cross-service winner error = %v", second.err)
	}
	successes, reconciliations := 1, 1
	if successes+reconciliations != 2 || len(modelA.Calls())+len(modelB.Calls()) != 1 {
		t.Fatalf("successes=%d reconciliations=%d model calls=%d", successes, reconciliations, len(modelA.Calls())+len(modelB.Calls()))
	}
	lookup, err := base.FindCommandRequest(context.Background(), application.FindCommandRequestRequest{RunTurnRequestID: "request-cross-process", SessionID: created.SessionID, RequestDigest: mustRunTurnDigest(t, created.SessionID, "inspect")})
	if err != nil || lookup.Kind != application.CommandRequestLookupFound || lookup.Record == nil {
		t.Fatalf("admission lookup = %#v, %v", lookup, err)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
	if err != nil || countAdmissionStartPairs(records, lookup.Record.CommandID, lookup.Record.TurnID, lookup.Record.ItemID) != 1 {
		t.Fatalf("admission records = %#v, %v", records, err)
	}
}

func TestRunTurnAndDeleteSessionHaveOneCASWinner(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	base, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	seed := newAcceptanceService(t, base, newPrefixedIDs("seed-delete-race"), &acceptanceSuccessModel{text: "seed"})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}

	barrier := newAcceptanceLoadBarrier(base, created.SessionID, 2)
	model := &acceptanceSuccessModel{text: "done"}
	runService := newAcceptanceService(t, barrier, newPrefixedIDs("run-delete-race"), model)
	deleteService := newAcceptanceService(t, barrier, newPrefixedIDs("delete-race"), &acceptanceSuccessModel{text: "unused"})

	runDone := make(chan error, 1)
	deleteDone := make(chan error, 1)
	go func() {
		_, runErr := runService.RunTurn(context.Background(), application.RunTurnRequest{
			SessionID: created.SessionID,
			RequestID: "request-delete-race",
			Input:     "inspect",
			Sink:      &testkit.RecordingSink{},
		})
		runDone <- runErr
	}()
	go func() {
		deleteDone <- deleteService.DeleteSession(context.Background(), application.DeleteSessionRequest{
			SessionID: created.SessionID, WorkspaceRoot: "/workspace",
		})
	}()

	await(t, barrier.entered, "run/delete first load")
	await(t, barrier.entered, "run/delete second load")
	barrier.Release()
	runErr := awaitOutcome(t, runDone, "RunTurn/DeleteSession run outcome")
	deleteErr := awaitOutcome(t, deleteDone, "RunTurn/DeleteSession delete outcome")

	successes := 0
	if runErr == nil {
		successes++
	}
	if deleteErr == nil {
		successes++
	}
	if successes != 1 {
		t.Fatalf("RunTurn error = %v, DeleteSession error = %v; want exactly one success", runErr, deleteErr)
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	deletes, starts := 0, 0
	for _, record := range records {
		switch record.Event.EventType() {
		case domain.EventSessionDeleted:
			deletes++
		case domain.EventTurnStarted:
			starts++
		}
	}
	if deletes+starts != 1 {
		t.Fatalf("event types = %v, want exactly one delete or turn admission", acceptanceEventTypes(records))
	}
	if deletes == 1 && len(model.Calls()) != 0 {
		t.Fatalf("delete winner still called model %d times", len(model.Calls()))
	}
	if starts == 1 && len(model.Calls()) != 1 {
		t.Fatalf("turn winner model calls = %d, want 1", len(model.Calls()))
	}
}

func mustRunTurnDigest(t *testing.T, sessionID domain.SessionID, input string) application.Digest {
	t.Helper()
	digest, err := application.DigestRunTurnRequestV1(sessionID, input)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func countAdmissionStartPairs(records []domain.RecordedEvent, commandID domain.CommandID, turnID domain.TurnID, itemID domain.ItemID) int {
	count := 0
	for index := 0; index+1 < len(records); index++ {
		turn, turnOK := records[index].Event.(domain.TurnStarted)
		item, itemOK := records[index+1].Event.(domain.AssistantMessageStarted)
		if turnOK && itemOK && records[index].CommandID == commandID && records[index+1].CommandID == commandID && turn.TurnID == turnID && item.TurnID == turnID && item.ItemID == itemID {
			count++
		}
	}
	return count
}

type lookupRaceBarrier struct {
	application.EventStore
	mu        sync.Mutex
	remaining int
	ready     chan struct{}
	release   chan struct{}
}

func newLookupRaceBarrier(store application.EventStore, callers int) *lookupRaceBarrier {
	return &lookupRaceBarrier{EventStore: store, remaining: callers, ready: make(chan struct{}), release: make(chan struct{})}
}

func (store *lookupRaceBarrier) FindCommandRequest(ctx context.Context, request application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	lookup, err := store.EventStore.FindCommandRequest(ctx, request)
	if err != nil || lookup.Kind != application.CommandRequestLookupNotFound {
		return lookup, err
	}
	store.mu.Lock()
	store.remaining--
	last := store.remaining == 0
	store.mu.Unlock()
	if last {
		close(store.ready)
	}
	select {
	case <-store.release:
		return lookup, nil
	case <-ctx.Done():
		return application.CommandRequestLookup{}, ctx.Err()
	}
}

func TestConcurrentRunTurnDifferentSessionsCompleteThirtyTwo(t *testing.T) {
	const count = 32
	ids := testkit.NewSequenceIDs()
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	store, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	model := &acceptanceSuccessModel{text: "parallel"}
	service := newAcceptanceService(t, store, ids, model)
	sessionIDs := make([]domain.SessionID, count)
	for index := range count {
		created, createErr := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		sessionIDs[index] = created.SessionID
	}

	sharedSink := &testkit.RecordingSink{}
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	outcomes := make([]outcome, count)
	start := make(chan struct{})
	finished := make(chan struct{}, count)
	for index, sessionID := range sessionIDs {
		go func() {
			defer func() { finished <- struct{}{} }()
			<-start
			outcomes[index].result, outcomes[index].err = service.RunTurn(context.Background(), application.RunTurnRequest{
				SessionID: sessionID,
				RequestID: domain.RunTurnRequestID(fmt.Sprintf("request-%d", index)),
				Input:     "inspect",
				Sink:      sharedSink,
			})
		}()
	}
	close(start)
	for range count {
		await(t, finished, "different-session caller")
	}

	for index, got := range outcomes {
		if got.err != nil || got.result.Status != domain.TurnStatusCompleted || got.result.Text != "parallel" || !got.result.TerminalCommitted {
			t.Fatalf("outcome[%d] = %#v, err = %v", index, got.result, got.err)
		}
		records, loadErr := application.ReadWholeStreamPinned(context.Background(), store, sessionIDs[index], 256)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		state, replayErr := domain.Replay(records)
		if replayErr != nil {
			t.Fatal(replayErr)
		}
		if state.Version != 5 || state.ActiveTurn != nil {
			t.Fatalf("state[%d] = %#v", index, state)
		}
	}
	if got := len(model.Calls()); got != count {
		t.Fatalf("model calls = %d, want %d", got, count)
	}
}

type acceptanceLoadBarrier struct {
	application.EventStore
	target    domain.SessionID
	mu        sync.Mutex
	remaining int
	entered   chan struct{}
	release   chan struct{}
}

func newAcceptanceLoadBarrier(store application.EventStore, target domain.SessionID, parties int) *acceptanceLoadBarrier {
	return &acceptanceLoadBarrier{
		EventStore: store,
		target:     target,
		remaining:  parties,
		entered:    make(chan struct{}, parties),
		release:    make(chan struct{}),
	}
}

func (barrier *acceptanceLoadBarrier) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	page, err := barrier.EventStore.ReadStream(ctx, request)
	if err != nil || request.SessionID != barrier.target || request.AfterSequence != 0 {
		return page, err
	}
	barrier.mu.Lock()
	wait := barrier.remaining > 0
	if wait {
		barrier.remaining--
	}
	barrier.mu.Unlock()
	if !wait {
		return page, nil
	}
	barrier.entered <- struct{}{}
	select {
	case <-barrier.release:
		return page, nil
	case <-ctx.Done():
		return application.StreamPage{}, ctx.Err()
	}
}

func (barrier *acceptanceLoadBarrier) WaitAll() {
	for capacity := cap(barrier.entered); capacity > 0; capacity-- {
		<-barrier.entered
	}
}

func (barrier *acceptanceLoadBarrier) Release() { close(barrier.release) }

type acceptanceSuccessModel struct {
	mu    sync.Mutex
	text  string
	calls []engine.ModelRequest
}

type prefixedIDs struct {
	mu                                          sync.Mutex
	prefix                                      string
	session, turn, item, command, append, event uint64
}

func newPrefixedIDs(prefix string) *prefixedIDs { return &prefixedIDs{prefix: prefix} }
func (ids *prefixedIDs) next(kind string, value *uint64) string {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	*value++
	return fmt.Sprintf("%s-%s-%d", ids.prefix, kind, *value)
}
func (ids *prefixedIDs) NewSessionID() (domain.SessionID, error) {
	return domain.SessionID(ids.next("session", &ids.session)), nil
}
func (ids *prefixedIDs) NewTurnID() (domain.TurnID, error) {
	return domain.TurnID(ids.next("turn", &ids.turn)), nil
}
func (ids *prefixedIDs) NewItemID() (domain.ItemID, error) {
	return domain.ItemID(ids.next("item", &ids.item)), nil
}
func (ids *prefixedIDs) NewCommandID() (domain.CommandID, error) {
	return domain.CommandID(ids.next("command", &ids.command)), nil
}
func (ids *prefixedIDs) NewAppendID() (domain.AppendID, error) {
	return domain.AppendID(ids.next("append", &ids.append)), nil
}
func (ids *prefixedIDs) NewEventID() (domain.EventID, error) {
	return domain.EventID(ids.next("event", &ids.event)), nil
}
func (ids *prefixedIDs) NewApprovalID() (domain.ApprovalID, error) {
	return domain.ApprovalID(ids.next("approval", &ids.event)), nil
}

type blockingAcceptanceModel struct {
	*acceptanceSuccessModel
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingAcceptanceModel(text string) *blockingAcceptanceModel {
	return &blockingAcceptanceModel{acceptanceSuccessModel: &acceptanceSuccessModel{text: text}, started: make(chan struct{}), release: make(chan struct{})}
}

func (model *blockingAcceptanceModel) Stream(ctx context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	stream, err := model.acceptanceSuccessModel.Stream(ctx, request)
	if err != nil {
		return nil, err
	}
	model.once.Do(func() { close(model.started) })
	return &blockingAcceptanceStream{ModelStream: stream, release: model.release}, nil
}

func (model *blockingAcceptanceModel) releaseOnce() {
	model.once.Do(func() {})
	select {
	case <-model.release:
	default:
		close(model.release)
	}
}

type blockingAcceptanceStream struct {
	engine.ModelStream
	release <-chan struct{}
	once    sync.Once
}

func (stream *blockingAcceptanceStream) Next(ctx context.Context) (engine.StreamEvent, error) {
	blocked := false
	stream.once.Do(func() { blocked = true })
	if blocked {
		select {
		case <-stream.release:
		case <-ctx.Done():
			return engine.StreamEvent{}, ctx.Err()
		}
	}
	return stream.ModelStream.Next(ctx)
}

func (model *acceptanceSuccessModel) Stream(_ context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	model.mu.Lock()
	model.calls = append(model.calls, request)
	model.mu.Unlock()
	events := make([]engine.StreamEvent, 0, 2)
	if model.text != "" {
		events = append(events, engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: model.text})
	}
	events = append(events, engine.StreamEvent{Type: engine.StreamEventCompleted})
	return &acceptanceStream{events: events}, nil
}

func (model *acceptanceSuccessModel) Calls() []engine.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]engine.ModelRequest(nil), model.calls...)
}

type acceptanceStream struct {
	events []engine.StreamEvent
	index  int
}

func (stream *acceptanceStream) Next(context.Context) (engine.StreamEvent, error) {
	event := stream.events[stream.index]
	stream.index++
	return event, nil
}

func (*acceptanceStream) Close() error { return nil }

func newAcceptanceService(t *testing.T, store application.EventStore, ids application.IDGenerator, model engine.Model) *application.Service {
	t.Helper()
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, ids, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, application.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func acceptanceEventTypes(records []domain.RecordedEvent) []string {
	types := make([]string, len(records))
	for index, record := range records {
		types[index] = record.Event.EventType()
	}
	return types
}
