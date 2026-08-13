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
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestRunTurnPersistsExactAssistantMessage(t *testing.T) {
	store := newTurnMemoryStore(t)
	recordingStore := &turnRecordingStore{EventStore: store}
	expectedRequest := engine.ModelRequest{
		SessionID: "session-1",
		TurnID:    "turn-1",
		ItemID:    "item-1",
		Input:     "inspect repository",
	}
	model, err := testkit.NewScriptedModel(expectedRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
		{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "你"}},
		{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "好\n"}},
		{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service := newTurnService(t, recordingStore, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	sink := &testkit.RecordingSink{}

	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID,
		Input:     expectedRequest.Input,
		Sink:      sink,
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.SessionID != expectedRequest.SessionID || result.TurnID != expectedRequest.TurnID || result.ItemID != expectedRequest.ItemID ||
		result.Status != domain.TurnStatusCompleted || result.Text != "你好\n" || !result.TerminalCommitted ||
		result.DeliveryWarning != nil {
		t.Fatalf("RunTurn() result = %#v", result)
	}
	if got := model.Calls(); !reflect.DeepEqual(got, []engine.ModelRequest{expectedRequest}) {
		t.Fatalf("model calls = %#v, want %#v", got, []engine.ModelRequest{expectedRequest})
	}

	allRecords, err := store.Load(context.Background(), created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := turnEventTypes(allRecords), []string{
		domain.EventSessionCreated,
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventAssistantMessageCompleted,
		domain.EventTurnCompleted,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("durable event types = %v, want %v", got, want)
	}
	if got, want := turnEventTypes(result.Records), turnEventTypes(allRecords[1:]); !reflect.DeepEqual(got, want) {
		t.Fatalf("result record types = %v, want %v", got, want)
	}
	if len(result.Records) != 4 {
		t.Fatalf("result records = %d, want 4", len(result.Records))
	}
	commandID := result.Records[0].CommandID
	for index, record := range result.Records {
		if record.Sequence != uint64(index+2) || record.CommandID != commandID || record.SessionID != created.SessionID {
			t.Fatalf("result record[%d] = %#v", index, record)
		}
	}
	if result.Records[0].OccurredAt != result.Records[1].OccurredAt || result.Records[2].OccurredAt != result.Records[3].OccurredAt {
		t.Fatalf("atomic batch timestamps differ: %#v", result.Records)
	}
	requests := recordingStore.AppendRequests()
	if len(requests) != 3 || len(requests[1].Events) != 2 || len(requests[2].Events) != 2 {
		t.Fatalf("append requests = %#v, want create plus two atomic pairs", requests)
	}
	if requests[1].CommandID != commandID || requests[2].CommandID != commandID {
		t.Fatalf("RunTurn command IDs = %q, %q, want %q", requests[1].CommandID, requests[2].CommandID, commandID)
	}

	state, err := domain.Replay(allRecords)
	if err != nil {
		t.Fatal(err)
	}
	turn := state.Turns[result.TurnID]
	item := turn.Items[result.ItemID]
	payload, ok := item.Payload.(domain.AssistantMessagePayload)
	if !ok || state.Version != 5 || turn.Status != domain.TurnStatusCompleted || item.Status != domain.ItemStatusCompleted || payload.Text != result.Text {
		t.Fatalf("replayed state = %#v, item = %#v", state, item)
	}

	runtimeEvents := sink.Delivered()
	wantRuntime := []engine.RuntimeEventType{
		engine.RuntimeModelStreamStarted,
		engine.RuntimeModelTextDelta,
		engine.RuntimeModelTextDelta,
		engine.RuntimeAppendCompleted,
		engine.RuntimeModelStreamCompleted,
	}
	if got := runtimeEventTypes(runtimeEvents); !reflect.DeepEqual(got, wantRuntime) {
		t.Fatalf("runtime event types = %v, want %v", got, wantRuntime)
	}
	for index, event := range runtimeEvents {
		if event.Ordinal != uint64(index+1) || event.SessionID != result.SessionID || event.TurnID != result.TurnID ||
			event.ItemID != result.ItemID || event.CommandID != commandID {
			t.Fatalf("runtime event[%d] = %#v", index, event)
		}
	}
	if runtimeEvents[1].Text != "你" || runtimeEvents[2].Text != "好\n" {
		t.Fatalf("runtime deltas = %q, %q", runtimeEvents[1].Text, runtimeEvents[2].Text)
	}
}

func TestRunTurnSequentialTurnsHaveDistinctIdentityAndOrder(t *testing.T) {
	store := newTurnMemoryStore(t)
	model := &repeatingSuccessModel{text: "answer"}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "first", Sink: &testkit.RecordingSink{}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "second", Sink: &testkit.RecordingSink{}})
	if err != nil {
		t.Fatal(err)
	}
	if first.TurnID == second.TurnID || first.ItemID == second.ItemID || first.Records[0].CommandID == second.Records[0].CommandID {
		t.Fatalf("identities were reused: first %#v second %#v", first, second)
	}
	for _, result := range []application.RunTurnResult{first, second} {
		if result.Records[0].CommandID != result.Records[1].CommandID || result.Records[0].CommandID != result.Records[2].CommandID || result.Records[0].CommandID != result.Records[3].CommandID {
			t.Fatalf("RunTurn command lineage split: %#v", result.Records)
		}
	}
	if got, want := model.Calls(), []engine.ModelRequest{
		{SessionID: created.SessionID, TurnID: first.TurnID, ItemID: first.ItemID, Input: "first"},
		{SessionID: created.SessionID, TurnID: second.TurnID, ItemID: second.ItemID, Input: "second"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("model calls = %#v, want %#v", got, want)
	}
	state, err := service.LoadSession(context.Background(), created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 9 || !reflect.DeepEqual(state.TurnOrder, []domain.TurnID{first.TurnID, second.TurnID}) {
		t.Fatalf("session = %#v", state)
	}
	for _, result := range []application.RunTurnResult{first, second} {
		turn := state.Turns[result.TurnID]
		if turn.Status != domain.TurnStatusCompleted || !reflect.DeepEqual(turn.ItemOrder, []domain.ItemID{result.ItemID}) ||
			turn.Items[result.ItemID].Status != domain.ItemStatusCompleted {
			t.Fatalf("turn %q = %#v", result.TurnID, turn)
		}
	}
}

func TestRunTurnResultRecordsAreDefensive(t *testing.T) {
	store := newTurnMemoryStore(t)
	service := newTurnService(t, store, testkit.NewSequenceIDs(), &repeatingSuccessModel{text: "original"})
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil {
		t.Fatal(err)
	}
	result.Records[0].Event = domain.TurnStarted{TurnID: "turn-mutated", Input: "mutated"}
	result.Records[2].Event = domain.AssistantMessageCompleted{TurnID: result.TurnID, ItemID: result.ItemID, Text: "mutated"}

	reloaded, err := store.Load(context.Background(), created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := reloaded[1].Event, (domain.TurnStarted{TurnID: result.TurnID, Input: "inspect"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh admission event = %#v, want %#v", got, want)
	}
	if got, want := reloaded[3].Event, (domain.AssistantMessageCompleted{TurnID: result.TurnID, ItemID: result.ItemID, Text: "original"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh terminal event = %#v, want %#v", got, want)
	}
}

func TestRunTurnValidatesRequestBeforeStoreAndIDs(t *testing.T) {
	var typedNilSink *nilTurnSink
	tests := []struct {
		name    string
		request application.RunTurnRequest
	}{
		{name: "invalid session", request: application.RunTurnRequest{SessionID: " session-1", Input: "inspect", Sink: &testkit.RecordingSink{}}},
		{name: "blank input", request: application.RunTurnRequest{SessionID: "session-1", Input: " \t\n", Sink: &testkit.RecordingSink{}}},
		{name: "invalid UTF-8 input", request: application.RunTurnRequest{SessionID: "session-1", Input: string([]byte{0xff}), Sink: &testkit.RecordingSink{}}},
		{name: "nil sink", request: application.RunTurnRequest{SessionID: "session-1", Input: "inspect"}},
		{name: "typed nil sink", request: application.RunTurnRequest{SessionID: "session-1", Input: "inspect", Sink: typedNilSink}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &turnCountingStore{}
			ids := &turnIDs{}
			model := &repeatingSuccessModel{text: "unused"}
			service := newTurnService(t, store, ids, model)
			result, err := service.RunTurn(context.Background(), test.request)
			assertRunTurnError(t, err, application.CategoryValidation, "invalid_request", false)
			if !reflect.DeepEqual(result, application.RunTurnResult{}) || store.LoadCalls() != 0 || store.AppendCalls() != 0 || len(ids.Calls()) != 0 || len(model.Calls()) != 0 {
				t.Fatalf("rejected request side effects: result %#v, load %d append %d IDs %v model %v", result, store.LoadCalls(), store.AppendCalls(), ids.Calls(), model.Calls())
			}
		})
	}
}

func TestRunTurnRejectsNilContextBeforeStoreAndIDs(t *testing.T) {
	var nilContext context.Context
	var typedNilContext *nilTurnContext
	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "nil", ctx: nilContext},
		{name: "typed nil", ctx: typedNilContext},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &turnCountingStore{}
			ids := &turnIDs{}
			model := &repeatingSuccessModel{text: "unused"}
			service := newTurnService(t, store, ids, model)
			result, err := service.RunTurn(test.ctx, application.RunTurnRequest{SessionID: "session-1", Input: "inspect", Sink: &testkit.RecordingSink{}})
			assertRunTurnError(t, err, application.CategoryValidation, "invalid_context", false)
			if !reflect.DeepEqual(result, application.RunTurnResult{}) || store.LoadCalls() != 0 || store.AppendCalls() != 0 || len(ids.Calls()) != 0 || len(model.Calls()) != 0 {
				t.Fatalf("nil context side effects: result %#v, load %d append %d IDs %v model %v", result, store.LoadCalls(), store.AppendCalls(), ids.Calls(), model.Calls())
			}
		})
	}
}

func TestRunTurnReplayAndEligibilityPrecedeRunIDs(t *testing.T) {
	tests := []struct {
		name     string
		store    func(*testing.T) application.EventStore
		category application.ErrorCategory
		code     string
	}{
		{name: "missing", store: func(*testing.T) application.EventStore { return &turnCountingStore{} }, category: application.CategoryValidation, code: "session_not_found"},
		{name: "corrupt", store: corruptTurnStore, category: application.CategoryInternal, code: "store_contract_violation"},
		{name: "closed", store: closedTurnStore, category: application.CategoryValidation, code: "domain_rejected"},
		{name: "already running", store: runningTurnStore, category: application.CategoryValidation, code: "domain_rejected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ids := &turnIDs{turnID: "turn-new", itemID: "item-new", commandID: "command-new"}
			model := &repeatingSuccessModel{text: "unused"}
			service := newTurnService(t, test.store(t), ids, model)
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: "session-preflight", Input: "inspect", Sink: &testkit.RecordingSink{}})
			assertRunTurnError(t, err, test.category, test.code, false)
			if !reflect.DeepEqual(result, application.RunTurnResult{}) || len(ids.Calls()) != 0 || len(model.Calls()) != 0 {
				t.Fatalf("preflight side effects: result %#v IDs %v model %v", result, ids.Calls(), model.Calls())
			}
		})
	}
}

func TestRunTurnValidatesEveryGeneratedIDBeforeAdmission(t *testing.T) {
	sourceCause := errors.New("run ID source failed")
	tests := []struct {
		name      string
		ids       *turnIDs
		code      string
		wantCalls []string
	}{
		{name: "turn source", ids: &turnIDs{turnErr: sourceCause}, code: "id_generation_failed", wantCalls: []string{"turn"}},
		{name: "item source", ids: &turnIDs{turnID: "turn-1", itemErr: sourceCause}, code: "id_generation_failed", wantCalls: []string{"turn", "item"}},
		{name: "command source", ids: &turnIDs{turnID: "turn-1", itemID: "item-1", commandErr: sourceCause}, code: "id_generation_failed", wantCalls: []string{"turn", "item", "command"}},
		{name: "invalid turn", ids: &turnIDs{turnID: " turn-1", itemID: "item-1", commandID: "command-1"}, code: "id_generator_contract_violation", wantCalls: []string{"turn"}},
		{name: "invalid item", ids: &turnIDs{turnID: "turn-1", itemID: " item-1", commandID: "command-1"}, code: "id_generator_contract_violation", wantCalls: []string{"turn", "item"}},
		{name: "invalid command", ids: &turnIDs{turnID: "turn-1", itemID: "item-1", commandID: " command-1"}, code: "id_generator_contract_violation", wantCalls: []string{"turn", "item", "command"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := activeTurnStore(t)
			store := &turnRecordingStore{EventStore: base}
			model := &repeatingSuccessModel{text: "unused"}
			service := newTurnService(t, store, test.ids, model)
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: "session-preflight", Input: "inspect", Sink: &testkit.RecordingSink{}})
			assertRunTurnError(t, err, application.CategoryInternal, test.code, false)
			if !reflect.DeepEqual(result, application.RunTurnResult{}) || len(store.AppendRequests()) != 0 || len(model.Calls()) != 0 {
				t.Fatalf("ID rejection side effects: result %#v appends %#v model %#v", result, store.AppendRequests(), model.Calls())
			}
			if got := test.ids.Calls(); !reflect.DeepEqual(got, test.wantCalls) {
				t.Fatalf("ID calls = %v, want %v", got, test.wantCalls)
			}
			if test.code == "id_generation_failed" && !errors.Is(err, sourceCause) {
				t.Fatalf("RunTurn() error = %v, want source cause", err)
			}
		})
	}
}

func TestRunTurnAtomicAdmissionFailureNeverCallsModel(t *testing.T) {
	tests := []struct {
		name     string
		failure  error
		category application.ErrorCategory
		code     string
	}{
		{name: "persistence", failure: errors.New("admission unavailable"), category: application.CategoryPersistence, code: "append_failed"},
		{name: "conflict", failure: &application.VersionConflictError{SessionID: "session-preflight", ExpectedVersion: 1, ActualVersion: 3}, category: application.CategoryConflict, code: "version_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := activeTurnStore(t)
			store := &turnFailingAppendStore{EventStore: base, failure: test.failure}
			model := &repeatingSuccessModel{text: "unused"}
			service := newTurnService(t, store, &turnIDs{turnID: "turn-1", itemID: "item-1", commandID: "command-run"}, model)
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: "session-preflight", Input: "inspect", Sink: &testkit.RecordingSink{}})
			assertRunTurnError(t, err, test.category, test.code, false)
			if !reflect.DeepEqual(result, application.RunTurnResult{}) || len(model.Calls()) != 0 {
				t.Fatalf("admission failure = result %#v model %#v", result, model.Calls())
			}
			if got := store.AppendCalls(); got != 1 {
				t.Fatalf("admission Append() calls = %d, want exactly one without retry", got)
			}
			records, loadErr := base.Load(context.Background(), "session-preflight")
			if loadErr != nil || len(records) != 1 {
				t.Fatalf("records after admission failure = (%#v, %v), want only session.created", records, loadErr)
			}
		})
	}
}

func TestRunTurnCancellationBeforeAdmissionWritesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := activeTurnStore(t)
	store := &turnRecordingStore{EventStore: base}
	ids := &turnIDs{turnID: "turn-1", itemID: "item-1", commandID: "command-run", onCommand: cancel}
	model := &repeatingSuccessModel{text: "unused"}
	service := newTurnService(t, store, ids, model)
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: "session-preflight", Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryCanceled, "canceled", false)
	if !reflect.DeepEqual(result, application.RunTurnResult{}) || len(store.AppendRequests()) != 0 || len(model.Calls()) != 0 {
		t.Fatalf("pre-admission cancel side effects: result %#v appends %#v model %#v", result, store.AppendRequests(), model.Calls())
	}
}

func TestRunTurnPostCommitSinkFailuresPreserveDurableCompletion(t *testing.T) {
	for _, failOrdinal := range []uint64{3, 4} {
		name := "append.completed"
		if failOrdinal == 4 {
			name = "model.stream.completed"
		}
		t.Run(name, func(t *testing.T) {
			store := newTurnMemoryStore(t)
			model := &repeatingSuccessModel{text: "done"}
			service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
			created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			cause := errors.New("terminal sink failed")
			sink := &testkit.RecordingSink{FailOrdinal: failOrdinal, Failure: cause}
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: sink})
			assertRunTurnError(t, err, application.CategoryDelivery, "runtime_delivery_failed", true)
			if result.Status != domain.TurnStatusCompleted || result.Text != "done" || !result.TerminalCommitted ||
				result.DeliveryWarning != cause || !errors.Is(err, cause) {
				t.Fatalf("post-commit delivery result = %#v, error = %v", result, err)
			}
			state, loadErr := service.LoadSession(context.Background(), created.SessionID)
			if loadErr != nil || state.Turns[result.TurnID].Status != domain.TurnStatusCompleted || state.Version != 5 {
				t.Fatalf("durable state = (%#v, %v)", state, loadErr)
			}
			attempts := sink.Attempts()
			if got := len(attempts); got != int(failOrdinal) {
				t.Fatalf("sink attempts = %d, want %d", got, failOrdinal)
			}
			wantFailedType := engine.RuntimeAppendCompleted
			if failOrdinal == 4 {
				wantFailedType = engine.RuntimeModelStreamCompleted
			}
			if attempts[len(attempts)-1].Type != wantFailedType || attempts[len(attempts)-1].Ordinal != failOrdinal {
				t.Fatalf("failed terminal attempt = %#v, want %s ordinal %d", attempts[len(attempts)-1], wantFailedType, failOrdinal)
			}
			assertCommittedErrorResultIsDefensive(t, store, created.SessionID, result, "inspect")
		})
	}
}

func TestRunTurnCancellationAfterTerminalCommitBecomesDeliveryWarning(t *testing.T) {
	base := newTurnMemoryStore(t)
	committed := make(chan struct{})
	release := make(chan struct{})
	store := &terminalBarrierStore{EventStore: base, committed: committed, release: release}
	model := &repeatingSuccessModel{}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &testkit.RecordingSink{}
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	resultChannel := make(chan outcome, 1)
	go func() {
		result, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: sink})
		resultChannel <- outcome{result: result, err: runErr}
	}()

	<-committed
	cancel()
	close(release)
	got := <-resultChannel
	assertRunTurnError(t, got.err, application.CategoryDelivery, "runtime_delivery_failed", true)
	if got.result.Status != domain.TurnStatusCompleted || got.result.Text != "" || !got.result.TerminalCommitted ||
		got.result.DeliveryWarning != context.Canceled || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("post-terminal cancellation = result %#v error %v", got.result, got.err)
	}
	if attempts := sink.Attempts(); len(attempts) != 1 || attempts[0].Type != engine.RuntimeModelStreamStarted || attempts[0].Ordinal != 1 {
		t.Fatalf("sink attempts = %#v, want only ordinal-1 started", attempts)
	}
	assertCommittedErrorResultIsDefensive(t, base, created.SessionID, got.result, "inspect")
	state, err := service.LoadSession(context.Background(), created.SessionID)
	if err != nil || state.Version != 5 || state.Turns[got.result.TurnID].Status != domain.TurnStatusCompleted {
		t.Fatalf("durable state = (%#v, %v)", state, err)
	}
}

func assertCommittedErrorResultIsDefensive(t *testing.T, store application.EventStore, sessionID domain.SessionID, result application.RunTurnResult, input string) {
	t.Helper()
	wantEvents := []domain.Event{
		domain.TurnStarted{TurnID: result.TurnID, Input: input},
		domain.AssistantMessageStarted{TurnID: result.TurnID, ItemID: result.ItemID},
		domain.AssistantMessageCompleted{TurnID: result.TurnID, ItemID: result.ItemID, Text: result.Text},
		domain.TurnCompleted{TurnID: result.TurnID},
	}
	if len(result.Records) != len(wantEvents) {
		t.Fatalf("error result records = %d, want %d: %#v", len(result.Records), len(wantEvents), result.Records)
	}
	commandID := result.Records[0].CommandID
	for index, want := range wantEvents {
		record := result.Records[index]
		if commandID == "" || record.Sequence != uint64(index+2) || record.SessionID != sessionID || record.CommandID != commandID || !reflect.DeepEqual(record.Event, want) {
			t.Fatalf("error result record[%d] = %#v, want event %#v with shared lineage", index, record, want)
		}
	}
	committed, err := store.Load(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 5 || !reflect.DeepEqual(result.Records, committed[1:]) {
		t.Fatalf("error result records = %#v, want exact committed records %#v", result.Records, committed)
	}

	result.Records[0].Event = domain.TurnStarted{TurnID: "turn-mutated", Input: "mutated"}
	result.Records[2].Event = domain.AssistantMessageCompleted{TurnID: result.TurnID, ItemID: result.ItemID, Text: "mutated"}
	fresh, err := store.Load(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 5 {
		t.Fatalf("fresh durable records = %d, want 5", len(fresh))
	}
	for index, want := range wantEvents {
		if !reflect.DeepEqual(fresh[index+1].Event, want) {
			t.Fatalf("fresh durable event[%d] = %#v, want %#v", index+1, fresh[index+1].Event, want)
		}
	}
	state, err := domain.Replay(fresh)
	if err != nil {
		t.Fatal(err)
	}
	turn := state.Turns[result.TurnID]
	item := turn.Items[result.ItemID]
	payload, ok := item.Payload.(domain.AssistantMessagePayload)
	if !ok || state.Version != 5 || turn.Status != domain.TurnStatusCompleted || item.Status != domain.ItemStatusCompleted || payload.Text != result.Text {
		t.Fatalf("fresh replay after result mutation = state %#v item %#v", state, item)
	}
}

func newTurnMemoryStore(t *testing.T) *memory.EventStore {
	t.Helper()
	store, err := memory.NewEventStore(testkit.FixedClock{Time: time.Date(2026, 8, 12, 6, 7, 8, 9, time.UTC)}, testkit.NewSequenceIDs())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func newTurnService(t *testing.T, store application.EventStore, ids application.IDGenerator, model engine.Model) *application.Service {
	t.Helper()
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, ids, runner, application.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func activeTurnStore(t *testing.T) *memory.EventStore {
	t.Helper()
	store := newTurnMemoryStore(t)
	seedTurnStore(t, store, []domain.Event{domain.SessionCreated{WorkspaceRoot: "/workspace"}})
	return store
}

func closedTurnStore(t *testing.T) application.EventStore {
	t.Helper()
	store := newTurnMemoryStore(t)
	seedTurnStore(t, store, []domain.Event{domain.SessionCreated{WorkspaceRoot: "/workspace"}, domain.SessionClosed{}})
	return store
}

func runningTurnStore(t *testing.T) application.EventStore {
	t.Helper()
	store := newTurnMemoryStore(t)
	seedTurnStore(t, store, []domain.Event{
		domain.SessionCreated{WorkspaceRoot: "/workspace"},
		domain.TurnStarted{TurnID: "turn-running", Input: "inspect"},
		domain.AssistantMessageStarted{TurnID: "turn-running", ItemID: "item-running"},
	})
	return store
}

func corruptTurnStore(t *testing.T) application.EventStore {
	t.Helper()
	return &turnCountingStore{records: []domain.RecordedEvent{{
		SchemaVersion: 1,
		ID:            "event-corrupt",
		CommandID:     "command-corrupt",
		SessionID:     "session-preflight",
		Sequence:      2,
		OccurredAt:    time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC),
		Event:         domain.SessionCreated{WorkspaceRoot: "/workspace"},
	}}}
}

func seedTurnStore(t *testing.T, store application.EventStore, events []domain.Event) {
	t.Helper()
	for index, event := range events {
		if _, err := store.Append(context.Background(), application.AppendRequest{
			SessionID:       "session-preflight",
			ExpectedVersion: uint64(index),
			CommandID:       domain.CommandID("command-seed-" + string(rune('1'+index))),
			Events:          []domain.Event{event},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func assertRunTurnError(t *testing.T, err error, category application.ErrorCategory, code string, terminal bool) {
	t.Helper()
	var appErr *application.Error
	if !errors.As(err, &appErr) || appErr == nil {
		t.Fatalf("error = %v, want *application.Error", err)
	}
	if appErr.Category != category || appErr.Code != code || appErr.TerminalCommitted != terminal {
		t.Fatalf("application error = %#v, want %s/%s terminal=%t", appErr, category, code, terminal)
	}
}

func turnEventTypes(records []domain.RecordedEvent) []string {
	types := make([]string, len(records))
	for index, record := range records {
		types[index] = record.Event.EventType()
	}
	return types
}

func runtimeEventTypes(events []engine.RuntimeEvent) []engine.RuntimeEventType {
	types := make([]engine.RuntimeEventType, len(events))
	for index, event := range events {
		types[index] = event.Type
	}
	return types
}

type repeatingSuccessModel struct {
	mu    sync.Mutex
	text  string
	calls []engine.ModelRequest
}

func (model *repeatingSuccessModel) Stream(_ context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	model.mu.Lock()
	model.calls = append(model.calls, request)
	model.mu.Unlock()
	steps := []engine.StreamEvent{}
	if model.text != "" {
		steps = append(steps, engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: model.text})
	}
	steps = append(steps, engine.StreamEvent{Type: engine.StreamEventCompleted})
	return &turnSuccessStream{events: steps}, nil
}

func (model *repeatingSuccessModel) Calls() []engine.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]engine.ModelRequest(nil), model.calls...)
}

type turnSuccessStream struct {
	events []engine.StreamEvent
	index  int
}

func (stream *turnSuccessStream) Next(context.Context) (engine.StreamEvent, error) {
	event := stream.events[stream.index]
	stream.index++
	return event, nil
}

func (*turnSuccessStream) Close() error { return nil }

type turnRecordingStore struct {
	application.EventStore
	mu       sync.Mutex
	requests []application.AppendRequest
}

func (store *turnRecordingStore) Append(ctx context.Context, request application.AppendRequest) ([]domain.RecordedEvent, error) {
	store.mu.Lock()
	cloned := request
	cloned.Events = append([]domain.Event(nil), request.Events...)
	store.requests = append(store.requests, cloned)
	store.mu.Unlock()
	return store.EventStore.Append(ctx, request)
}

func (store *turnRecordingStore) AppendRequests() []application.AppendRequest {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]application.AppendRequest, len(store.requests))
	for index, request := range store.requests {
		result[index] = request
		result[index].Events = append([]domain.Event(nil), request.Events...)
	}
	return result
}

type turnCountingStore struct {
	mu          sync.Mutex
	records     []domain.RecordedEvent
	loadCalls   int
	appendCalls int
}

func (store *turnCountingStore) Load(context.Context, domain.SessionID) ([]domain.RecordedEvent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.loadCalls++
	return domain.CloneRecordedEvents(store.records)
}

func (store *turnCountingStore) Append(context.Context, application.AppendRequest) ([]domain.RecordedEvent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.appendCalls++
	return nil, errors.New("unexpected append")
}

func (store *turnCountingStore) LoadCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.loadCalls
}

func (store *turnCountingStore) AppendCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.appendCalls
}

type turnIDs struct {
	mu         sync.Mutex
	turnID     domain.TurnID
	turnErr    error
	itemID     domain.ItemID
	itemErr    error
	commandID  domain.CommandID
	commandErr error
	onCommand  func()
	calls      []string
}

func (ids *turnIDs) NewSessionID() (domain.SessionID, error) { return "session-unused", nil }

func (ids *turnIDs) NewTurnID() (domain.TurnID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.calls = append(ids.calls, "turn")
	return ids.turnID, ids.turnErr
}

func (ids *turnIDs) NewItemID() (domain.ItemID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.calls = append(ids.calls, "item")
	return ids.itemID, ids.itemErr
}

func (ids *turnIDs) NewCommandID() (domain.CommandID, error) {
	ids.mu.Lock()
	ids.calls = append(ids.calls, "command")
	callback := ids.onCommand
	id, err := ids.commandID, ids.commandErr
	ids.mu.Unlock()
	if callback != nil {
		callback()
	}
	return id, err
}

func (*turnIDs) NewAppendID() (domain.AppendID, error) { return "append-unused", nil }

func (*turnIDs) NewEventID() (domain.EventID, error) { return "event-unused", nil }

func (ids *turnIDs) Calls() []string {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	return append([]string(nil), ids.calls...)
}

type turnFailingAppendStore struct {
	application.EventStore
	mu          sync.Mutex
	failure     error
	appendCalls int
}

func (store *turnFailingAppendStore) Append(context.Context, application.AppendRequest) ([]domain.RecordedEvent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.appendCalls++
	return nil, store.failure
}

func (store *turnFailingAppendStore) AppendCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.appendCalls
}

type terminalBarrierStore struct {
	application.EventStore
	committed chan<- struct{}
	release   <-chan struct{}
}

func (store *terminalBarrierStore) Append(ctx context.Context, request application.AppendRequest) ([]domain.RecordedEvent, error) {
	records, err := store.EventStore.Append(ctx, request)
	if err != nil || len(request.Events) == 0 || request.Events[0].EventType() != domain.EventAssistantMessageCompleted {
		return records, err
	}
	store.committed <- struct{}{}
	<-store.release
	return records, nil
}

type nilTurnSink struct{}

func (*nilTurnSink) Emit(context.Context, engine.RuntimeEvent) error { return nil }

type nilTurnContext struct{}

func (*nilTurnContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*nilTurnContext) Done() <-chan struct{}       { return nil }
func (*nilTurnContext) Err() error                  { return nil }
func (*nilTurnContext) Value(any) any               { return nil }
