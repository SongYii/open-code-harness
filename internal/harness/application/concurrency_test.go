package application_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestConcurrentRunTurnSameSessionHasOneAtomicAdmissionWinner(t *testing.T) {
	ids := testkit.NewSequenceIDs()
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	base, err := memory.NewEventStoreV2(authority)
	if err != nil {
		t.Fatal(err)
	}
	barrier := newAcceptanceLoadBarrier(base, "session-1", 1)
	model := &acceptanceSuccessModel{text: "done"}
	service := newAcceptanceService(t, barrier, ids, model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}

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
	barrier.WaitAll()
	barrier.Release()

	successes := 0
	var first application.RunTurnResult
	for range 32 {
		got := <-done
		if got.err != nil {
			t.Fatalf("RunTurn() unexpected error = %v, result = %#v", got.err, got.result)
		}
		successes++
		if got.result.Status != domain.TurnStatusCompleted || !got.result.TerminalCommitted {
			t.Fatalf("result = %#v", got.result)
		}
		if first.TurnID == "" {
			first = got.result
		} else if got.result.TurnID != first.TurnID || got.result.ItemID != first.ItemID || got.result.Text != first.Text {
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
	if state.Version != 5 || len(state.Turns) != 1 || state.ActiveTurnID != "" {
		t.Fatalf("replayed state = %#v", state)
	}
}

func TestConcurrentRunTurnAcrossServicesReconcilesDurableAdmissionWinner(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	base, err := memory.NewEventStoreV2(authority)
	if err != nil {
		t.Fatal(err)
	}
	sharedIDs := testkit.NewSequenceIDs()
	seed := newAcceptanceService(t, base, sharedIDs, &acceptanceSuccessModel{text: "seed"})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	gate := newLookupRaceBarrier(base, 2)
	modelA, modelB := &acceptanceSuccessModel{text: "done"}, &acceptanceSuccessModel{text: "done"}
	serviceA := newAcceptanceService(t, gate, sharedIDs, modelA)
	serviceB := newAcceptanceService(t, gate, sharedIDs, modelB)
	type outcome struct{ err error }
	done := make(chan outcome, 2)
	for _, service := range []*application.Service{serviceA, serviceB} {
		go func(service *application.Service) {
			_, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-cross-process", Input: "inspect", Sink: &testkit.RecordingSink{}})
			done <- outcome{err: err}
		}(service)
	}
	<-gate.ready
	close(gate.release)
	successes, reconciliations := 0, 0
	for range 2 {
		got := <-done
		if got.err == nil {
			successes++
			continue
		}
		var appErr *application.Error
		if errors.As(got.err, &appErr) && appErr.Code == "reconciliation_required" {
			reconciliations++
			continue
		}
		t.Fatalf("cross-service result error = %v", got.err)
	}
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
	application.EventStoreV2
	mu        sync.Mutex
	remaining int
	ready     chan struct{}
	release   chan struct{}
}

func newLookupRaceBarrier(store application.EventStoreV2, callers int) *lookupRaceBarrier {
	return &lookupRaceBarrier{EventStoreV2: store, remaining: callers, ready: make(chan struct{}), release: make(chan struct{})}
}

func (store *lookupRaceBarrier) FindCommandRequest(ctx context.Context, request application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	lookup, err := store.EventStoreV2.FindCommandRequest(ctx, request)
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
	<-store.release
	return lookup, nil
}

func TestConcurrentRunTurnDifferentSessionsCompleteThirtyTwo(t *testing.T) {
	const count = 32
	ids := testkit.NewSequenceIDs()
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	store, err := memory.NewEventStoreV2(authority)
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
	var wait sync.WaitGroup
	wait.Add(count)
	for index, sessionID := range sessionIDs {
		go func() {
			defer wait.Done()
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
	wait.Wait()

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
		turn := state.Turns[got.result.TurnID]
		if state.Version != 5 || turn.Status != domain.TurnStatusCompleted || turn.Items[got.result.ItemID].Status != domain.ItemStatusCompleted {
			t.Fatalf("state[%d] = %#v", index, state)
		}
	}
	if got := len(model.Calls()); got != count {
		t.Fatalf("model calls = %d, want %d", got, count)
	}
}

type acceptanceLoadBarrier struct {
	application.EventStoreV2
	target    domain.SessionID
	mu        sync.Mutex
	remaining int
	entered   chan struct{}
	release   chan struct{}
}

func newAcceptanceLoadBarrier(store application.EventStoreV2, target domain.SessionID, parties int) *acceptanceLoadBarrier {
	return &acceptanceLoadBarrier{
		EventStoreV2: store,
		target:       target,
		remaining:    parties,
		entered:      make(chan struct{}, parties),
		release:      make(chan struct{}),
	}
}

func (barrier *acceptanceLoadBarrier) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	page, err := barrier.EventStoreV2.ReadStream(ctx, request)
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

func newAcceptanceService(t *testing.T, store application.EventStoreV2, ids application.IDGenerator, model engine.Model) *application.Service {
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
