package application_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
		RequestID: "request-success",
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

	allRecords, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
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
	if state.Version != 5 || state.ActiveTurn != nil {
		t.Fatalf("replayed state = %#v", state)
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

func TestFinalAssistantMessageSecretIsRedactedInResultAndDomainEvent(t *testing.T) {
	store := newTurnMemoryStore(t)
	expectedRequest := engine.ModelRequest{
		SessionID: "session-1",
		TurnID:    "turn-1",
		ItemID:    "item-1",
		Input:     "inspect repository",
	}
	model, err := testkit.NewScriptedModel(expectedRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
		{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "found DATABASE_URL=postgres://localhost/app and API_KEY=sup3rSecretValue123 in the .env file"}},
		{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID,
		RequestID: "request-redact-final",
		Input:     expectedRequest.Input,
		Sink:      &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if strings.Contains(result.Text, "sup3rSecretValue123") {
		t.Fatalf("RunTurnResult.Text leaked the secret: %q", result.Text)
	}
	if !strings.Contains(result.Text, "[redacted]") {
		t.Fatalf("RunTurnResult.Text = %q, want a [redacted] marker", result.Text)
	}
	if !strings.Contains(result.Text, "DATABASE_URL=postgres://localhost/app") {
		t.Fatalf("RunTurnResult.Text = %q, want unrelated content preserved", result.Text)
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var domainText string
	found := false
	for _, record := range records {
		if event, ok := record.Event.(domain.AssistantMessageCompleted); ok {
			domainText = event.Text
			found = true
		}
	}
	if !found {
		t.Fatal("no domain.AssistantMessageCompleted event found in the durable stream")
	}
	if strings.Contains(domainText, "sup3rSecretValue123") {
		t.Fatalf("persisted domain.AssistantMessageCompleted.Text leaked the secret: %q", domainText)
	}
	if !strings.Contains(domainText, "[redacted]") {
		t.Fatalf("persisted domain.AssistantMessageCompleted.Text = %q, want a [redacted] marker", domainText)
	}
}

func TestRunTurnRequestIdentityAdmitsThreeEvents(t *testing.T) {
	store := newTurnMemoryStore(t)
	recordingStore := &turnRecordingStore{EventStore: store}
	model := &repeatingSuccessModel{text: "done"}
	identity := validTurnRequestIdentity()
	config := application.DefaultConfig()
	config.RequestIdentity = &identity
	service := newTurnServiceWithConfig(t, recordingStore, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-identity", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Status != domain.TurnStatusCompleted || result.Text != "done" || !result.TerminalCommitted {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if got, want := turnEventTypes(result.Records), []string{
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventModelRequestRecorded,
		domain.EventAssistantMessageCompleted,
		domain.EventTurnCompleted,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("result record types = %v, want %v", got, want)
	}
	recorded, ok := result.Records[2].Event.(domain.ModelRequestRecorded)
	if !ok || recorded.AdapterFamily != identity.AdapterFamily || recorded.ModelID != identity.ModelID || recorded.EndpointID != identity.EndpointID {
		t.Fatalf("request recorded = %#v", result.Records[2].Event)
	}
	if !reflect.DeepEqual(recorded.Messages, []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: "inspect"}}) {
		t.Fatalf("request messages = %#v", recorded.Messages)
	}
	requests := recordingStore.AppendRequests()
	if len(requests) != 3 || len(requests[1].Events) != 3 || len(requests[2].Events) != 2 {
		t.Fatalf("append requests = %#v, want create plus 3-event admission and 2-event terminal", requests)
	}
}

func TestRunTurnPrependsObservedUsageBeforeTerminal(t *testing.T) {
	store := newTurnMemoryStore(t)
	recordingStore := &turnRecordingStore{EventStore: store}
	inner := &repeatingSuccessModel{text: "done"}
	usage := &engine.TokenUsage{InputTokens: 4, OutputTokens: 6, CachedInputTokens: 1}
	model := &observingModel{inner: inner, stats: engine.AttemptStats{Usage: usage, FinishReason: "stop", ProviderRequestID: "req-usage", LatencyMs: 21}}
	identity := validTurnRequestIdentity()
	config := application.DefaultConfig()
	config.RequestIdentity = &identity
	service := newTurnServiceWithConfig(t, recordingStore, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-usage", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Status != domain.TurnStatusCompleted || result.Text != "done" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if got, want := turnEventTypes(result.Records), []string{
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventModelRequestRecorded,
		domain.EventModelUsageRecorded,
		domain.EventAssistantMessageCompleted,
		domain.EventTurnCompleted,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("result record types = %v, want %v", got, want)
	}
	if _, ok := itemTerminalRecord(result.Records).Event.(domain.AssistantMessageCompleted); !ok {
		t.Fatalf("item terminal = %#v, want completed by type", itemTerminalRecord(result.Records))
	}
	gotUsage, ok := result.Records[3].Event.(domain.ModelUsageRecorded)
	if !ok || gotUsage.InputTokens != 4 || gotUsage.OutputTokens != 6 || gotUsage.CachedInputTokens != 1 || gotUsage.FinishReason != "stop" || gotUsage.ProviderRequestID != "req-usage" || gotUsage.LatencyMs != 21 {
		t.Fatalf("usage = %#v", result.Records[3].Event)
	}
	requests := recordingStore.AppendRequests()
	if len(requests) != 3 || len(requests[1].Events) != 3 || len(requests[2].Events) != 3 {
		t.Fatalf("append requests = %#v, want usage prepended onto the terminal batch", requests)
	}
	if requests[2].Events[0].Event.EventType() != domain.EventModelUsageRecorded {
		t.Fatalf("terminal batch[0] = %#v, want usage", requests[2].Events[0])
	}
}

func TestRunTurnObservedStatsWithoutIdentityDoNotPersistUsage(t *testing.T) {
	store := newTurnMemoryStore(t)
	inner := &repeatingSuccessModel{text: "done"}
	model := &observingModel{inner: inner, stats: engine.AttemptStats{
		Usage:        &engine.TokenUsage{InputTokens: 4, OutputTokens: 6},
		FinishReason: "stop",
		LatencyMs:    21,
	}}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-scripted-stats", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Status != domain.TurnStatusCompleted || result.Text != "done" || !result.TerminalCommitted {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if got, want := turnEventTypes(result.Records), []string{
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventAssistantMessageCompleted,
		domain.EventTurnCompleted,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("result record types = %v, want %v", got, want)
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

	first, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-first", Input: "first", Sink: &testkit.RecordingSink{}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-second", Input: "second", Sink: &testkit.RecordingSink{}})
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
	if state.Version != 9 || state.ActiveTurn != nil {
		t.Fatalf("session = %#v", state)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := domain.Replay(records)
	if err != nil || legacy.Version != 9 || legacy.ActiveTurn != nil {
		t.Fatalf("pinned compact history = %#v, %v", legacy, err)
	}
}

func TestRunTurnResultRecordsAreDefensive(t *testing.T) {
	store := newTurnMemoryStore(t)
	service := newTurnService(t, store, testkit.NewSequenceIDs(), &repeatingSuccessModel{text: "original"})
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-defensive", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil {
		t.Fatal(err)
	}
	result.Records[0].Event = domain.TurnStarted{TurnID: "turn-mutated", Input: "mutated"}
	result.Records[2].Event = domain.AssistantMessageCompleted{TurnID: result.TurnID, ItemID: result.ItemID, Text: "mutated"}

	reloaded, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
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
		{name: "empty request ID", request: application.RunTurnRequest{SessionID: "session-1", Input: "inspect", Sink: &testkit.RecordingSink{}}},
		{name: "padded request ID", request: application.RunTurnRequest{SessionID: "session-1", RequestID: " request-invalid", Input: "inspect", Sink: &testkit.RecordingSink{}}},
		{name: "invalid session", request: application.RunTurnRequest{SessionID: " session-1", RequestID: "request-invalid", Input: "inspect", Sink: &testkit.RecordingSink{}}},
		{name: "blank input", request: application.RunTurnRequest{SessionID: "session-1", RequestID: "request-invalid", Input: " \t\n", Sink: &testkit.RecordingSink{}}},
		{name: "invalid UTF-8 input", request: application.RunTurnRequest{SessionID: "session-1", RequestID: "request-invalid", Input: string([]byte{0xff}), Sink: &testkit.RecordingSink{}}},
		{name: "nil sink", request: application.RunTurnRequest{SessionID: "session-1", RequestID: "request-invalid", Input: "inspect"}},
		{name: "typed nil sink", request: application.RunTurnRequest{SessionID: "session-1", RequestID: "request-invalid", Input: "inspect", Sink: typedNilSink}},
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

func TestRunTurnRequestLookupPrecedesIDsAndModel(t *testing.T) {
	for _, test := range []struct {
		lookup application.CommandRequestLookup
		code   string
	}{
		{lookup: application.CommandRequestLookup{Kind: application.CommandRequestLookupIdentityMismatch}, code: "command_identity_mismatch"},
	} {
		base := activeTurnStore(t)
		store := &lookupTurnStore{EventStore: base, lookup: test.lookup}
		ids := &turnIDs{}
		model := &repeatingSuccessModel{text: "unused"}
		service := newTurnService(t, store, ids, model)
		result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: "session-preflight", RequestID: "request-known", Input: "inspect", Sink: &testkit.RecordingSink{}})
		var appErr *application.Error
		if !application.IsCategory(err, application.CategoryConflict) || !errors.As(err, &appErr) || appErr.Code != test.code || !reflect.DeepEqual(result, application.RunTurnResult{}) || ids.TotalCalls() != 0 || len(model.Calls()) != 0 || store.reads != 0 || store.appends != 0 {
			t.Fatalf("lookup=%#v result=%#v err=%v ids=%d model=%v reads=%d appends=%d", test.lookup, result, err, ids.TotalCalls(), model.Calls(), store.reads, store.appends)
		}
	}
}

func TestRunTurnRejectsImpossibleUnknownLookupCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := activeTurnStore(t)
	unknown, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, MayHaveCommitted: true})
	if err != nil {
		t.Fatal(err)
	}
	store := &lookupTurnStore{EventStore: base, lookupErr: unknown, cancel: cancel}
	ids := &turnIDs{}
	model := &repeatingSuccessModel{text: "unused"}
	service := newTurnService(t, store, ids, model)
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: "session-preflight", RequestID: "request-lookup-unknown", Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryInternal, "store_contract_violation", false)
	if !reflect.DeepEqual(result, application.RunTurnResult{}) || ids.TotalCalls() != 0 || len(model.Calls()) != 0 || store.reads != 0 || store.appends != 0 {
		t.Fatalf("impossible lookup result=%#v err=%v IDs=%d model=%v reads=%d appends=%d", result, err, ids.TotalCalls(), model.Calls(), store.reads, store.appends)
	}
}

func TestRunTurnCanceledAfterReadStopsBeforeIDsAndModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := activeTurnStore(t)
	store := &cancelAfterReadStore{EventStore: base, cancel: cancel}
	ids := &turnIDs{}
	model := &repeatingSuccessModel{text: "unused"}
	service := newTurnService(t, store, ids, model)
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: "session-preflight", RequestID: "request-cancel-read", Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryCanceled, "canceled", false)
	if !reflect.DeepEqual(result, application.RunTurnResult{}) || len(ids.Calls()) != 0 || len(model.Calls()) != 0 {
		t.Fatalf("post-read cancel result=%#v ids=%v model=%v", result, ids.Calls(), model.Calls())
	}
}

func TestRunTurnTerminalUnknownDoesNotFallbackAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := newTurnMemoryStore(t)
	ids := testkit.NewSequenceIDs()
	seed := newTurnService(t, base, ids, &repeatingSuccessModel{text: "done"})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store := &terminalUnknownStore{EventStore: base, cancel: cancel}
	model := &repeatingSuccessModel{text: "done"}
	service := newTurnService(t, store, ids, model)
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-terminal-unknown", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if result.Status != domain.TurnStatusCompleted || !result.TerminalCommitted || len(model.Calls()) != 1 {
		t.Fatalf("late cancel replaced completed winner: result=%#v err=%v calls=%d", result, err, len(model.Calls()))
	}
	requests := store.AppendRequests()
	if store.terminalCalls != 1 || len(requests) < 2 {
		t.Fatalf("terminal calls=%d requests=%#v", store.terminalCalls, requests)
	}
	admission, terminal := requests[0], requests[1]
	if len(admission.Events) != 2 || len(terminal.Events) != 2 || admission.CommandID != terminal.CommandID || terminal.AppendID == "" || terminal.AppendID != store.terminalAppendID || !reflect.DeepEqual(terminal.Events, store.terminalEvents) {
		t.Fatalf("terminal identity was not stable: admission=%#v terminal=%#v", admission, terminal)
	}
	for _, request := range requests[1:] {
		if request.Events[0].Event.EventType() != domain.EventAssistantMessageCompleted {
			t.Fatalf("unexpected post-unknown append %#v", request)
		}
	}
	records, readErr := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
	if readErr != nil || len(records) != 5 || turnEventTypes(records)[3] != domain.EventAssistantMessageCompleted {
		t.Fatalf("durable unknown outcome=%#v error=%v", records, readErr)
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
			result, err := service.RunTurn(test.ctx, application.RunTurnRequest{SessionID: "session-1", RequestID: "request-context", Input: "inspect", Sink: &testkit.RecordingSink{}})
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
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: "session-preflight", RequestID: "request-preflight", Input: "inspect", Sink: &testkit.RecordingSink{}})
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
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: "session-preflight", RequestID: "request-recording", Input: "inspect", Sink: &testkit.RecordingSink{}})
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
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: "session-preflight", RequestID: "request-append-failure", Input: "inspect", Sink: &testkit.RecordingSink{}})
			assertRunTurnError(t, err, test.category, test.code, false)
			if !reflect.DeepEqual(result, application.RunTurnResult{}) || len(model.Calls()) != 0 {
				t.Fatalf("admission failure = result %#v model %#v", result, model.Calls())
			}
			if got := store.AppendCalls(); got != 1 {
				t.Fatalf("admission Append() calls = %d, want exactly one without retry", got)
			}
			records, loadErr := application.ReadWholeStreamPinned(context.Background(), base, "session-preflight", 256)
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
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: "session-preflight", RequestID: "request-canceled", Input: "inspect", Sink: &testkit.RecordingSink{}})
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
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-delivery", Input: "inspect", Sink: sink})
			assertRunTurnError(t, err, application.CategoryDelivery, "runtime_delivery_failed", true)
			if result.Status != domain.TurnStatusCompleted || result.Text != "done" || !result.TerminalCommitted ||
				result.DeliveryWarning != cause || !errors.Is(err, cause) {
				t.Fatalf("post-commit delivery result = %#v, error = %v", result, err)
			}
			state, loadErr := service.LoadSession(context.Background(), created.SessionID)
			if loadErr != nil || state.ActiveTurn != nil || state.Version != 5 {
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
		result, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-terminal-cancel", Input: "inspect", Sink: sink})
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
	if err != nil || state.Version != 5 || state.ActiveTurn != nil {
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
	committed, err := application.ReadWholeStreamPinned(context.Background(), store, sessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 5 || !reflect.DeepEqual(result.Records, committed[1:]) {
		t.Fatalf("error result records = %#v, want exact committed records %#v", result.Records, committed)
	}

	result.Records[0].Event = domain.TurnStarted{TurnID: "turn-mutated", Input: "mutated"}
	result.Records[2].Event = domain.AssistantMessageCompleted{TurnID: result.TurnID, ItemID: result.ItemID, Text: "mutated"}
	fresh, err := application.ReadWholeStreamPinned(context.Background(), store, sessionID, 256)
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
	if state.Version != 5 || state.ActiveTurn != nil {
		t.Fatalf("fresh replay after result mutation = %#v", state)
	}
}

func newTurnMemoryStore(t *testing.T) *memory.EventStore {
	t.Helper()
	store, err := memory.NewEventStore(v2Authority)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type observingModel struct {
	inner engine.Model
	stats engine.AttemptStats
}

func (model *observingModel) Stream(ctx context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	stream, err := model.inner.Stream(ctx, request)
	if stream == nil {
		return nil, err
	}
	return &observingModelStream{ModelStream: stream, stats: model.stats}, err
}

type observingModelStream struct {
	engine.ModelStream
	stats engine.AttemptStats
}

func (stream *observingModelStream) Snapshot() engine.AttemptStats {
	return stream.stats
}

func itemTerminalRecord(records []domain.RecordedEvent) domain.RecordedEvent {
	for _, record := range records {
		switch record.Event.(type) {
		case domain.AssistantMessageCompleted, domain.AssistantMessageFailed, domain.AssistantMessageInterrupted:
			return record
		}
	}
	return domain.RecordedEvent{}
}

func newTurnService(t *testing.T, store application.EventStore, ids application.IDGenerator, model engine.Model) *application.Service {
	t.Helper()
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, ids, testkit.FixedClock{Time: time.Date(2026, 8, 12, 6, 7, 8, 9, time.UTC)}, runner, v2Authority, application.DefaultConfig())
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
		seedV2Event(t, store, "session-preflight", uint64(index), domain.CommandID("command-seed-"+string(rune('1'+index))), event)
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

func (store *turnRecordingStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	store.mu.Lock()
	cloned := request
	cloned.Events = append([]application.ProposedEvent(nil), request.Events...)
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
		result[index].Events = append([]application.ProposedEvent(nil), request.Events...)
	}
	return result
}

type turnCountingStore struct {
	mu          sync.Mutex
	records     []domain.RecordedEvent
	loadCalls   int
	appendCalls int
}

func (*turnCountingStore) ListSessionHeads(context.Context, application.ListSessionHeadsRequest) (application.SessionHeadPage, error) {
	return application.SessionHeadPage{}, nil
}

type lookupTurnStore struct {
	application.EventStore
	lookup    application.CommandRequestLookup
	lookupErr error
	cancel    context.CancelFunc
	reads     int
	appends   int
}

func (store *lookupTurnStore) FindCommandRequest(context.Context, application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	if store.cancel != nil {
		store.cancel()
	}
	return store.lookup, store.lookupErr
}
func (store *lookupTurnStore) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	store.reads++
	return store.EventStore.ReadStream(ctx, request)
}
func (store *lookupTurnStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	store.appends++
	return store.EventStore.Append(ctx, request)
}

type cancelAfterReadStore struct {
	application.EventStore
	cancel context.CancelFunc
}

func (store *cancelAfterReadStore) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	page, err := store.EventStore.ReadStream(ctx, request)
	store.cancel()
	return page, err
}

type terminalUnknownStore struct {
	application.EventStore
	cancel           context.CancelFunc
	terminalCalls    int
	requests         []application.AppendRequest
	terminalAppendID domain.AppendID
	terminalEvents   []application.ProposedEvent
}

func (store *terminalUnknownStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	store.requests = append(store.requests, cloneTurnAppendRequest(request))
	receipt, err := store.EventStore.Append(ctx, request)
	if err != nil || len(request.Events) == 0 || request.Events[0].Event.EventType() != domain.EventAssistantMessageCompleted {
		return receipt, err
	}
	store.terminalCalls++
	store.terminalAppendID = request.AppendID
	store.terminalEvents = append([]application.ProposedEvent(nil), request.Events...)
	store.cancel()
	unknown, buildErr := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, SessionID: request.SessionID, MayHaveCommitted: true})
	if buildErr != nil {
		return application.CommitReceipt{}, buildErr
	}
	return application.CommitReceipt{}, unknown
}

func (store *terminalUnknownStore) AppendRequests() []application.AppendRequest {
	requests := make([]application.AppendRequest, len(store.requests))
	for index, request := range store.requests {
		requests[index] = cloneTurnAppendRequest(request)
	}
	return requests
}

func cloneTurnAppendRequest(request application.AppendRequest) application.AppendRequest {
	clone := request
	clone.Events = append([]application.ProposedEvent(nil), request.Events...)
	return clone
}

func (store *turnCountingStore) ReadStream(_ context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.loadCalls++
	records, _ := domain.CloneRecordedEvents(store.records)
	head := uint64(len(records))
	if request.HeadVersion != nil {
		head = *request.HeadVersion
	}
	if request.AfterSequence > head {
		return application.StreamPage{}, errors.New("invalid cursor")
	}
	end := request.AfterSequence + uint64(request.Limit)
	if end > head {
		end = head
	}
	return application.StreamPage{Records: records[request.AfterSequence:end], HeadVersion: head, NextAfterSequence: end, End: end == head}, nil
}

func (store *turnCountingStore) Append(context.Context, application.AppendRequest) (application.CommitReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.appendCalls++
	return application.CommitReceipt{}, errors.New("unexpected append")
}
func (*turnCountingStore) ResolveAppend(context.Context, application.ResolveAppendRequest) (application.AppendResolution, error) {
	return application.AppendResolution{Kind: application.AppendResolutionNotFound}, nil
}
func (*turnCountingStore) FindCommandRequest(context.Context, application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	return application.CommandRequestLookup{Kind: application.CommandRequestLookupNotFound}, nil
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
	appends    uint64
	events     uint64
	totalCalls int
}

func (ids *turnIDs) NewSessionID() (domain.SessionID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.totalCalls++
	return "session-unused", nil
}

func (ids *turnIDs) NewTurnID() (domain.TurnID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.calls = append(ids.calls, "turn")
	ids.totalCalls++
	return ids.turnID, ids.turnErr
}

func (ids *turnIDs) NewItemID() (domain.ItemID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.calls = append(ids.calls, "item")
	ids.totalCalls++
	return ids.itemID, ids.itemErr
}

func (ids *turnIDs) NewCommandID() (domain.CommandID, error) {
	ids.mu.Lock()
	ids.calls = append(ids.calls, "command")
	ids.totalCalls++
	callback := ids.onCommand
	id, err := ids.commandID, ids.commandErr
	ids.mu.Unlock()
	if callback != nil {
		callback()
	}
	return id, err
}

func (ids *turnIDs) NewAppendID() (domain.AppendID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.appends++
	ids.totalCalls++
	return domain.AppendID(fmt.Sprintf("append-%d", ids.appends)), nil
}
func (ids *turnIDs) NewEventID() (domain.EventID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.events++
	ids.totalCalls++
	return domain.EventID(fmt.Sprintf("event-%d", ids.events)), nil
}
func (ids *turnIDs) NewApprovalID() (domain.ApprovalID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.totalCalls++
	return "approval-1", nil
}

func (ids *turnIDs) Calls() []string {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	return append([]string(nil), ids.calls...)
}

func (ids *turnIDs) TotalCalls() int {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	return ids.totalCalls
}

type turnFailingAppendStore struct {
	application.EventStore
	mu          sync.Mutex
	failure     error
	appendCalls int
}

func (store *turnFailingAppendStore) Append(context.Context, application.AppendRequest) (application.CommitReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.appendCalls++
	return application.CommitReceipt{}, store.failure
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

func (store *terminalBarrierStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	receipt, err := store.EventStore.Append(ctx, request)
	if err != nil || len(request.Events) == 0 || request.Events[0].Event.EventType() != domain.EventAssistantMessageCompleted {
		return receipt, err
	}
	store.committed <- struct{}{}
	<-store.release
	return receipt, nil
}

type nilTurnSink struct{}

func (*nilTurnSink) Emit(context.Context, engine.RuntimeEvent) error { return nil }

type nilTurnContext struct{}

func (*nilTurnContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*nilTurnContext) Done() <-chan struct{}       { return nil }
func (*nilTurnContext) Err() error                  { return nil }
func (*nilTurnContext) Value(any) any               { return nil }
