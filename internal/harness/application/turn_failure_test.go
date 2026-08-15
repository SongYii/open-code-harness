package application_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestRunTurnModelOutputFailureCommitsStableFailedPair(t *testing.T) {
	providerFailure := errors.New("secret provider prose")
	closeFailure := errors.New("close transport prose")
	tests := []struct {
		name           string
		config         testkit.ScriptedModelConfig
		maxBytes       int
		category       application.ErrorCategory
		code           string
		message        string
		wantCause      error
		wantClose      bool
		wantCloseCause bool
		wantDeltas     int
	}{
		{name: "startup", config: testkit.ScriptedModelConfig{StartupError: providerFailure}, category: application.CategoryModel, code: "model_startup", message: "model failed before streaming", wantCause: providerFailure},
		{name: "startup returned stream", config: testkit.ScriptedModelConfig{StartupError: providerFailure, ReturnStreamOnStartupError: true, CloseError: closeFailure}, category: application.CategoryModel, code: "model_startup", message: "model failed before streaming", wantCause: providerFailure, wantClose: true, wantCloseCause: true},
		{name: "failure before delta", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: providerFailure}}}, category: application.CategoryModel, code: "model_stream", message: "model stream failed", wantCause: providerFailure, wantClose: true},
		{name: "failure after delta", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "partial"}}, {Err: providerFailure}}}, category: application.CategoryModel, code: "model_stream", message: "model stream failed", wantCause: providerFailure, wantClose: true, wantDeltas: 1},
		{name: "event and error", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "ignored"}, Err: providerFailure}}}, category: application.CategoryModel, code: "model_stream", message: "model stream failed", wantCause: providerFailure, wantClose: true},
		{name: "premature EOF", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: io.EOF}}, CloseError: closeFailure}, category: application.CategoryModel, code: "invalid_stream", message: "model stream violated contract", wantClose: true, wantCloseCause: true},
		{name: "unknown event", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: "provider.unknown"}}}}, category: application.CategoryModel, code: "invalid_stream", message: "model stream violated contract", wantClose: true},
		{name: "empty delta", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta}}}}, category: application.CategoryModel, code: "invalid_stream", message: "model stream violated contract", wantClose: true},
		{name: "invalid UTF-8", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: string([]byte{0xff})}}}}, category: application.CategoryModel, code: "invalid_stream", message: "model stream violated contract", wantClose: true},
		{name: "output limit", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "12"}}}, CloseError: closeFailure}, maxBytes: 1, category: application.CategoryOutputLimit, code: "output_limit", message: "assistant output exceeded limit", wantCause: engine.ErrAssistantOutputLimit, wantClose: true, wantCloseCause: true},
		{name: "close only", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}}}, CloseError: closeFailure}, category: application.CategoryModel, code: "model_stream", message: "model stream failed", wantCause: closeFailure, wantClose: true},
		{name: "primary and close", config: testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: providerFailure}}, CloseError: closeFailure}, category: application.CategoryModel, code: "model_stream", message: "model stream failed", wantCause: providerFailure, wantClose: true, wantCloseCause: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTurnMemoryStore(t)
			expected := engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"}
			model, err := testkit.NewScriptedModel(expected, test.config)
			if err != nil {
				t.Fatal(err)
			}
			config := application.DefaultConfig()
			if test.maxBytes != 0 {
				config.MaxAssistantBytes = test.maxBytes
			}
			service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
			created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			sink := &testkit.RecordingSink{}
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-model-failure", Input: expected.Input, Sink: sink})
			assertRunTurnError(t, err, test.category, test.code, true)
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Fatalf("error %v does not preserve %v", err, test.wantCause)
			}
			if test.wantCloseCause && !errors.Is(err, closeFailure) {
				t.Fatalf("error %v does not preserve close cause", err)
			}
			if result.Status != domain.TurnStatusFailed || result.Text != "" || !result.TerminalCommitted || result.DeliveryWarning != nil || len(result.Records) != 4 {
				t.Fatalf("result = %#v", result)
			}
			if got := model.CloseCalls(); (got == 1) != test.wantClose {
				t.Fatalf("Close calls = %d, want close=%t", got, test.wantClose)
			}
			assertFailedPair(t, store, result, test.code, test.message)
			wantRuntime := []engine.RuntimeEventType{}
			if test.name != "startup" && test.name != "startup returned stream" {
				wantRuntime = append(wantRuntime, engine.RuntimeModelStreamStarted)
			}
			for range test.wantDeltas {
				wantRuntime = append(wantRuntime, engine.RuntimeModelTextDelta)
			}
			wantRuntime = append(wantRuntime, engine.RuntimeAppendCompleted, engine.RuntimeModelStreamFailed)
			if got := runtimeEventTypes(sink.Delivered()); !reflect.DeepEqual(got, wantRuntime) {
				t.Fatalf("runtime types = %v, want %v", got, wantRuntime)
			}
		})
	}
}

func TestRunTurnProviderAuthPersistsClassifiedFailureCode(t *testing.T) {
	store := newTurnMemoryStore(t)
	failure := &engine.ProviderFailure{Class: engine.FailureClassAuth, Code: "provider_auth", SafeMessage: "Authorization: Bearer sk-secret"}
	model, err := testkit.NewScriptedModel(
		engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"},
		testkit.ScriptedModelConfig{StartupError: &engine.Error{Code: engine.CodeModelStartup, Cause: failure}},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-provider-auth", Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryModel, string(engine.CodeModelStartup), true)
	var classified *engine.ProviderFailure
	if !errors.As(err, &classified) || classified.Code != "provider_auth" {
		t.Fatalf("error = %v, want ProviderFailure provider_auth", err)
	}
	if strings.Contains(err.Error(), "Authorization") || strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("returned error rendered secret text: %v", err)
	}
	assertFailedPair(t, store, result, "provider_auth", "provider rejected credentials")
}

func TestRunTurnContentFilterPersistsPermanentUsageWithoutFinishReason(t *testing.T) {
	store := newTurnMemoryStore(t)
	failure := &engine.ProviderFailure{Class: engine.FailureClassPermanent, Code: "provider_permanent"}
	inner, err := testkit.NewScriptedModel(
		engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"},
		testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: &engine.Error{Code: engine.CodeModelStream, Cause: failure}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	model := &observingModel{inner: inner, stats: engine.AttemptStats{
		Usage:        &engine.TokenUsage{InputTokens: 8, OutputTokens: 0},
		FinishReason: "",
		LatencyMs:    33,
	}}
	identity := validTurnRequestIdentity()
	config := application.DefaultConfig()
	config.RequestIdentity = &identity
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-content-filter", Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryModel, string(engine.CodeModelStream), true)
	if result.Status != domain.TurnStatusFailed || result.Text != "" || !result.TerminalCommitted {
		t.Fatalf("result = %#v", result)
	}
	if got, want := turnEventTypes(result.Records), []string{
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventModelRequestRecorded,
		domain.EventModelUsageRecorded,
		domain.EventAssistantMessageFailed,
		domain.EventTurnFailed,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("result record types = %v, want %v", got, want)
	}
	usage, ok := result.Records[3].Event.(domain.ModelUsageRecorded)
	if !ok || usage.FinishReason != "" || usage.LatencyMs != 33 || usage.InputTokens != 8 {
		t.Fatalf("usage = %#v, want latency with empty finish reason", result.Records[3].Event)
	}
	failed, ok := itemTerminalRecord(result.Records).Event.(domain.AssistantMessageFailed)
	if !ok || failed.Code != "provider_permanent" || failed.Message != "provider rejected the request" {
		t.Fatalf("item terminal = %#v, want provider_permanent by type", itemTerminalRecord(result.Records))
	}
}

func TestRunTurnRunnerEngineCodeRemainsAuthorityOverProviderApplicationError(t *testing.T) {
	injected := &application.Error{Category: application.CategoryCanceled, Code: "provider_injected", TerminalCommitted: true}
	providerCause := errors.Join(errors.New("provider startup failed"), injected)
	store := newTurnMemoryStore(t)
	model, err := testkit.NewScriptedModel(
		engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"},
		testkit.ScriptedModelConfig{StartupError: providerCause},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-engine-authority", Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryModel, string(engine.CodeModelStartup), true)
	if !errors.Is(err, injected) {
		t.Fatalf("error = %v, want raw provider cause inspectable", err)
	}
	assertFailedPair(t, store, result, string(engine.CodeModelStartup), "model failed before streaming")
}

func TestRunTurnRunnerDeliveryCodeRemainsAuthorityOverSinkApplicationError(t *testing.T) {
	injected := &application.Error{Category: application.CategoryModel, Code: "sink_injected", TerminalCommitted: true}
	store := newTurnMemoryStore(t)
	service := newTurnService(t, store, testkit.NewSequenceIDs(), &repeatingSuccessModel{text: "delta"})
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID,
		RequestID: "request-delivery-authority",
		Input:     "inspect",
		Sink:      &testkit.RecordingSink{FailOrdinal: 1, Failure: injected},
	})
	assertRunTurnError(t, err, application.CategoryDelivery, "runtime_delivery_failed", true)
	if !errors.Is(err, injected) {
		t.Fatalf("error = %v, want raw sink cause inspectable", err)
	}
	assertInterruptedPair(t, store, result, domain.InterruptionDeliveryFailed)
}

func TestRunTurnEmptySuccessfulOutputIsCompleted(t *testing.T) {
	store := newTurnMemoryStore(t)
	model, err := testkit.NewScriptedModel(engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"}, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}}}})
	if err != nil {
		t.Fatal(err)
	}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-empty-output", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Status != domain.TurnStatusCompleted || result.Text != "" || !result.TerminalCommitted {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestRunTurnCancellationDuringScriptedStepCommitsInterruptedPair(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	closeCause := errors.New("cancel close failed")
	model, err := testkit.NewScriptedModel(engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"}, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Entered: entered, Release: release, Event: engine.StreamEvent{Type: engine.StreamEventCompleted}}}, CloseError: closeCause})
	if err != nil {
		t.Fatal(err)
	}
	store := newTurnMemoryStore(t)
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
	done := make(chan outcome, 1)
	go func() {
		result, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-step-cancel", Input: "inspect", Sink: sink})
		done <- outcome{result, runErr}
	}()
	<-entered
	cancel()
	close(release)
	got := <-done
	assertRunTurnError(t, got.err, application.CategoryCanceled, "canceled", true)
	if !errors.Is(got.err, closeCause) {
		t.Fatalf("error = %v, want close cause", got.err)
	}
	if got.result.DeliveryWarning != context.Canceled {
		t.Fatalf("delivery warning = %v, want caller cancellation", got.result.DeliveryWarning)
	}
	if attempts := sink.Attempts(); len(attempts) != 1 || attempts[0].Type != engine.RuntimeModelStreamStarted {
		t.Fatalf("runtime attempts = %#v, want started only", attempts)
	}
	assertInterruptedPair(t, store, got.result, domain.InterruptionCallerCanceled)
}

func TestCancellationWinnerCompletedBeatsLateCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := newTurnMemoryStore(t)
	ids := testkit.NewSequenceIDs()
	created, err := newTurnService(t, base, ids, &repeatingSuccessModel{text: "seed"}).CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store := &cancelOnCompletedAppendStore{EventStore: base, cancel: cancel}
	model := &repeatingSuccessModel{text: "done"}
	service := newTurnService(t, store, ids, model)
	result, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-late-cancel", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if result.Status != domain.TurnStatusCompleted || !result.TerminalCommitted || len(model.Calls()) != 1 {
		t.Fatalf("result=%#v err=%v calls=%d", result, runErr, len(model.Calls()))
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
	if err != nil || turnEventTypes(records)[len(turnEventTypes(records))-2] != domain.EventAssistantMessageCompleted {
		t.Fatalf("records=%v err=%v", turnEventTypes(records), err)
	}
}

type cancelOnCompletedAppendStore struct {
	application.EventStore
	cancel context.CancelFunc
}

func (store *cancelOnCompletedAppendStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	if len(request.Events) > 0 && request.Events[0].Event.EventType() == domain.EventAssistantMessageCompleted {
		store.cancel()
	}
	return store.EventStore.Append(ctx, request)
}

func TestRunTurnDeliveryFailureBeforeTerminalCommitIsDurablyInterrupted(t *testing.T) {
	for _, ordinal := range []uint64{1, 2} {
		t.Run(map[uint64]string{1: "started", 2: "delta"}[ordinal], func(t *testing.T) {
			store := newTurnMemoryStore(t)
			closeCause := errors.New("delivery close failed")
			model, modelErr := testkit.NewScriptedModel(
				engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"},
				testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "delta"}}, {Event: engine.StreamEvent{Type: engine.StreamEventCompleted}}}, CloseError: closeCause},
			)
			if modelErr != nil {
				t.Fatal(modelErr)
			}
			service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
			created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			cause := errors.New("sink offline")
			sink := &durableObservingSink{
				RecordingSink: testkit.RecordingSink{FailOrdinal: ordinal, Failure: cause},
				store:         store,
				sessionID:     created.SessionID,
			}
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-delivery-failure", Input: "inspect", Sink: sink})
			assertRunTurnError(t, err, application.CategoryDelivery, "runtime_delivery_failed", true)
			if !errors.Is(err, cause) {
				t.Fatalf("error %v does not preserve sink cause", err)
			}
			if !errors.Is(err, closeCause) {
				t.Fatalf("error %v does not preserve close cause", err)
			}
			assertInterruptedPair(t, store, result, domain.InterruptionDeliveryFailed)
			attempts, delivered := sink.Attempts(), sink.Delivered()
			wantAttempts := []engine.RuntimeEventType{engine.RuntimeModelStreamStarted, engine.RuntimeAppendCompleted, engine.RuntimeModelStreamInterrupted}
			wantDelivered := []engine.RuntimeEventType{engine.RuntimeAppendCompleted, engine.RuntimeModelStreamInterrupted}
			if ordinal == 2 {
				wantAttempts = []engine.RuntimeEventType{engine.RuntimeModelStreamStarted, engine.RuntimeModelTextDelta, engine.RuntimeAppendCompleted, engine.RuntimeModelStreamInterrupted}
				wantDelivered = []engine.RuntimeEventType{engine.RuntimeModelStreamStarted, engine.RuntimeAppendCompleted, engine.RuntimeModelStreamInterrupted}
			}
			if got := runtimeEventTypes(attempts); !reflect.DeepEqual(got, wantAttempts) {
				t.Fatalf("attempt types = %v, want %v", got, wantAttempts)
			}
			if got := runtimeEventTypes(delivered); !reflect.DeepEqual(got, wantDelivered) {
				t.Fatalf("delivered types = %v, want %v", got, wantDelivered)
			}
			commandID := result.Records[0].CommandID
			for index, event := range attempts {
				if event.Ordinal != uint64(index+1) || event.SessionID != result.SessionID || event.TurnID != result.TurnID || event.ItemID != result.ItemID || event.CommandID != commandID {
					t.Fatalf("attempt[%d] correlation/ordinal = %#v", index, event)
				}
				if event.Type == engine.RuntimeModelStreamInterrupted && event.Code != domain.InterruptionDeliveryFailed {
					t.Fatalf("interrupted payload = %#v", event)
				}
			}
			if !sink.ObservedDurableTerminalBeforeSignals() {
				t.Fatal("terminal runtime delivery was not observed after the durable interrupted pair")
			}
		})
	}
}

func TestRunTurnTerminalPersistenceFailurePreservesRunningBoundaryAndCauses(t *testing.T) {
	executionCause := errors.New("provider failed")
	appendCause := errors.New("terminal store failed")
	base := newTurnMemoryStore(t)
	store := &failTerminalStore{EventStore: base, failure: appendCause}
	model, err := testkit.NewScriptedModel(engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"}, testkit.ScriptedModelConfig{StartupError: executionCause})
	if err != nil {
		t.Fatal(err)
	}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-terminal-failure", Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryPersistence, "append_failed", false)
	if !errors.Is(err, executionCause) || !errors.Is(err, appendCause) {
		t.Fatalf("error = %v, want both causes", err)
	}
	if result.Status != domain.TurnStatusRunning || result.TerminalCommitted || len(result.Records) != 2 {
		t.Fatalf("result = %#v", result)
	}
	if store.TerminalCalls() != 1 {
		t.Fatalf("terminal append calls = %d, want 1", store.TerminalCalls())
	}
	assertRunningBoundary(t, base, result)
}

func TestRunTurnTerminalCleanupFailureCategoriesPreserveExecutionCause(t *testing.T) {
	executionCause := errors.New("provider failed before terminalization")
	tests := []struct {
		name      string
		wrap      func(application.EventStore) application.EventStore
		category  application.ErrorCategory
		code      string
		committed bool
	}{
		{
			name: "conflict",
			wrap: func(store application.EventStore) application.EventStore {
				return &failTerminalStore{EventStore: store, failure: &application.VersionConflictError{SessionID: "session-1", ExpectedVersion: 3, ActualVersion: 4}}
			},
			category: application.CategoryConflict,
			code:     "version_conflict",
		},
		{
			name: "apply contract violation",
			wrap: func(store application.EventStore) application.EventStore {
				return &invalidTerminalReturnStore{EventStore: store}
			},
			category:  application.CategoryInternal,
			code:      "store_contract_violation",
			committed: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := newTurnMemoryStore(t)
			model, err := testkit.NewScriptedModel(
				engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"},
				testkit.ScriptedModelConfig{StartupError: executionCause},
			)
			if err != nil {
				t.Fatal(err)
			}
			service := newTurnService(t, test.wrap(base), testkit.NewSequenceIDs(), model)
			created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-cleanup-failure", Input: "inspect", Sink: &testkit.RecordingSink{}})
			assertRunTurnError(t, err, test.category, test.code, false)
			if !errors.Is(err, executionCause) {
				t.Fatalf("error = %v, want execution cause", err)
			}
			if test.category == application.CategoryConflict && !application.IsVersionConflict(err) {
				t.Fatalf("error = %v, want conflict cause", err)
			}
			if test.committed {
				durable, loadErr := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				state, replayErr := domain.Replay(durable)
				if replayErr != nil {
					t.Fatal(replayErr)
				}
				if len(durable) != 5 || state.ActiveTurn != nil {
					t.Fatalf("malformed receipt durable state = %#v", state)
				}
				return
			}
			assertRunningBoundary(t, base, result)
		})
	}
}

func TestRunTurnTerminalCleanupTimeoutIsPersistenceNotCallerCancellation(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	model, err := testkit.NewScriptedModel(
		engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"},
		testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Entered: entered, Release: release, Event: engine.StreamEvent{Type: engine.StreamEventCompleted}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	base := newTurnMemoryStore(t)
	store := &blockingTerminalCleanupStore{EventStore: base}
	config := application.DefaultConfig()
	config.TerminalCommitTimeout = time.Millisecond
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-race-cancel", Input: "inspect", Sink: &testkit.RecordingSink{}})
		done <- outcome{result, runErr}
	}()
	<-entered
	cancel()
	close(release)
	got := <-done
	assertRunTurnError(t, got.err, application.CategoryPersistence, "append_failed", false)
	if !errors.Is(got.err, context.Canceled) || !errors.Is(got.err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want caller cancellation and cleanup deadline causes", got.err)
	}
	if !store.SawLiveBoundedContext() {
		t.Fatal("terminal cleanup Store did not receive a live bounded context detached from the caller")
	}
	assertRunningBoundary(t, base, got.result)
}

func TestRunTurnCompletedAppendCancellationDoesNotInventInterruption(t *testing.T) {
	base := newTurnMemoryStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	appendCause := errors.New("success append canceled")
	store := &cancelCompletedAppendStore{EventStore: base, cancel: cancel, failure: appendCause}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), &repeatingSuccessModel{text: "done"})
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-completed-cancel", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if store.InterruptedCalls() != 0 {
		t.Fatalf("late cancel invented interruption: completed/interrupted=%d/%d err=%v", store.CompletedCalls(), store.InterruptedCalls(), err)
	}
	if result.Status != domain.TurnStatusRunning || result.TerminalCommitted {
		t.Fatalf("result=%#v", result)
	}
	assertRunningBoundary(t, base, result)
}

func TestRunTurnCompletedPersistenceFailureDoesNotInventSecondOutcome(t *testing.T) {
	for _, failure := range []error{errors.New("terminal unavailable"), &application.VersionConflictError{SessionID: "session-1", ExpectedVersion: 3, ActualVersion: 4}} {
		t.Run(failure.Error(), func(t *testing.T) {
			base := newTurnMemoryStore(t)
			store := &failTerminalStore{EventStore: base, failure: failure}
			service := newTurnService(t, store, testkit.NewSequenceIDs(), &repeatingSuccessModel{text: "done"})
			created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-completed-persist", Input: "inspect", Sink: &testkit.RecordingSink{}})
			category, code := application.CategoryPersistence, "append_failed"
			if application.IsVersionConflict(failure) {
				category, code = application.CategoryConflict, "version_conflict"
			}
			assertRunTurnError(t, err, category, code, false)
			if store.TerminalCalls() != 1 {
				t.Fatalf("terminal append calls = %d, want no retry/fallback", store.TerminalCalls())
			}
			assertRunningBoundary(t, base, result)
		})
	}
}

func TestRunTurnCancellationImmediatelyAfterAdmissionUsesBoundedDetachedCleanup(t *testing.T) {
	base := newTurnMemoryStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	store := &cancelAfterAdmissionStore{EventStore: base, cancel: cancel}
	config := application.DefaultConfig()
	config.TerminalCommitTimeout = 2 * time.Second
	model := &repeatingSuccessModel{text: "unused"}
	sink := &testkit.RecordingSink{}
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-after-admission", Input: "inspect", Sink: sink})
	assertRunTurnError(t, err, application.CategoryCanceled, "canceled", true)
	assertInterruptedPair(t, base, result, domain.InterruptionCallerCanceled)
	if !store.SawDetachedBoundedContext() {
		t.Fatal("interruption append did not receive a live bounded context detached from caller cancellation")
	}
	if len(model.Calls()) != 0 || len(sink.Attempts()) != 0 || result.DeliveryWarning != context.Canceled {
		t.Fatalf("post-admission cancellation: model=%#v sink=%#v warning=%v", model.Calls(), sink.Attempts(), result.DeliveryWarning)
	}
}

func TestRunTurnCancellationAtCompletedTerminalEntryDoesNotInventInterruption(t *testing.T) {
	base := newTurnMemoryStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cause := errors.New("completed append did not commit")
	store := &cancelCompletedAppendStore{EventStore: base, cancel: cancel, failure: cause}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), &repeatingSuccessModel{text: "done"})
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-terminal-entry", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if store.InterruptedCalls() != 0 {
		t.Fatalf("late cancel invented interruption err=%v", err)
	}
	assertRunningBoundary(t, base, result)
	durable, loadErr := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if got := turnEventTypes(durable); !reflect.DeepEqual(got, []string{
		domain.EventSessionCreated,
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
	}) {
		t.Fatalf("durable terminal race types = %v", got)
	}
}

func TestRunTurnFailureTerminalDeliveryWarningKeepsPrimaryCategory(t *testing.T) {
	providerCause := errors.New("provider unavailable")
	sinkCause := errors.New("terminal observer unavailable")
	store := newTurnMemoryStore(t)
	model, err := testkit.NewScriptedModel(
		engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"},
		testkit.ScriptedModelConfig{StartupError: providerCause},
	)
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
		RequestID: "request-malformed-admission",
		Input:     "inspect",
		Sink:      &testkit.RecordingSink{FailOrdinal: 1, Failure: sinkCause},
	})
	assertRunTurnError(t, err, application.CategoryModel, "model_startup", true)
	if result.DeliveryWarning != sinkCause || !errors.Is(err, providerCause) || !errors.Is(err, sinkCause) {
		t.Fatalf("result = %#v, err = %v, want both causes and delivery warning", result, err)
	}
	assertFailedPair(t, store, result, "model_startup", "model failed before streaming")
}

func TestRunTurnRejectsMalformedAdmissionAppendBeforeModel(t *testing.T) {
	base := newTurnMemoryStore(t)
	ids := testkit.NewSequenceIDs()
	serviceForSeed := newTurnService(t, base, ids, &repeatingSuccessModel{})
	created, err := serviceForSeed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store := &corruptAdmissionReceiptStore{EventStore: base}
	model := &repeatingSuccessModel{text: "unused"}
	service := newTurnService(t, store, ids, model)
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-malformed-terminal", Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryInternal, "store_contract_violation", false)
	if !reflect.DeepEqual(result, application.RunTurnResult{}) || len(model.Calls()) != 0 {
		t.Fatalf("malformed admission result = %#v, model calls = %#v", result, model.Calls())
	}
}

func TestRunTurnRejectsMalformedTerminalAppendReturnsWithoutSuccessSignal(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*application.CommitReceipt)
	}{
		{name: "wrong append id", mutate: func(receipt *application.CommitReceipt) { receipt.AppendID = "append-other" }},
		{name: "zero position", mutate: func(receipt *application.CommitReceipt) { receipt.CommitPosition = 0 }},
		{name: "wrong first sequence", mutate: func(receipt *application.CommitReceipt) { receipt.FirstSequence++ }},
		{name: "wrong last sequence", mutate: func(receipt *application.CommitReceipt) { receipt.LastSequence-- }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := newTurnMemoryStore(t)
			ids := testkit.NewSequenceIDs()
			seed := newTurnService(t, base, ids, &repeatingSuccessModel{})
			created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			store := &corruptTerminalReceiptStore{EventStore: base, mutate: test.mutate}
			sink := &testkit.RecordingSink{}
			service := newTurnService(t, store, ids, &repeatingSuccessModel{text: "done"})
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-concurrent-session", Input: "inspect", Sink: sink})
			assertRunTurnError(t, err, application.CategoryInternal, "store_contract_violation", false)
			if result.Status != domain.TurnStatusRunning || result.TerminalCommitted || len(result.Records) != 2 {
				t.Fatalf("result = %#v", result)
			}
			if got := runtimeEventTypes(sink.Attempts()); !reflect.DeepEqual(got, []engine.RuntimeEventType{engine.RuntimeModelStreamStarted, engine.RuntimeModelTextDelta}) {
				t.Fatalf("runtime attempts = %v, want no terminal success signal", got)
			}
			if store.calls != 1 {
				t.Fatalf("terminal append calls = %d, want no retry", store.calls)
			}
		})
	}
}

type corruptTerminalReceiptStore struct {
	application.EventStore
	mutate func(*application.CommitReceipt)
	calls  int
}

func (store *corruptTerminalReceiptStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	receipt, err := store.EventStore.Append(ctx, request)
	if err != nil || len(request.Events) == 0 || request.Events[0].Event.EventType() != domain.EventAssistantMessageCompleted {
		return receipt, err
	}
	store.calls++
	store.mutate(&receipt)
	return receipt, nil
}

func TestRunTurnConcurrentSameSessionHasOneAdmissionWinner(t *testing.T) {
	base := newTurnMemoryStore(t)
	ids := testkit.NewSequenceIDs()
	seed := newTurnService(t, base, ids, &repeatingSuccessModel{})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store := newTwoLoadBarrierStore(base, created.SessionID)
	model := &repeatingSuccessModel{text: "done"}
	service := newTurnService(t, store, ids, model)
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for _, input := range []string{"first", "second"} {
		input := input
		go func() {
			result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: domain.RunTurnRequestID("request-" + input), Input: input, Sink: &testkit.RecordingSink{}})
			outcomes <- outcome{result, runErr}
		}()
	}
	store.WaitBothLoaded()
	store.Release()
	first, second := <-outcomes, <-outcomes
	winners, conflicts := 0, 0
	for _, got := range []outcome{first, second} {
		if got.err == nil && got.result.Status == domain.TurnStatusCompleted {
			winners++
		}
		if application.IsCategory(got.err, application.CategoryConflict) && reflect.DeepEqual(got.result, application.RunTurnResult{}) {
			conflicts++
		}
	}
	if winners != 1 || conflicts != 1 || len(model.Calls()) != 1 {
		t.Fatalf("winners/conflicts/model calls = %d/%d/%d; outcomes %#v %#v", winners, conflicts, len(model.Calls()), first, second)
	}
}

func TestRunTurnModelCompletionRacingCallerCancelCommitsExactlyOneTerminalPair(t *testing.T) {
	const iterations = 32
	for iteration := 0; iteration < iterations; iteration++ {
		entered := make(chan struct{}, 1)
		releaseModel := make(chan struct{})
		model, err := testkit.NewScriptedModel(
			engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"},
			testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Entered: entered, Release: releaseModel, Event: engine.StreamEvent{Type: engine.StreamEventCompleted}}}},
		)
		if err != nil {
			t.Fatal(err)
		}
		base := newTurnMemoryStore(t)
		store := &turnRecordingStore{EventStore: base}
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
		done := make(chan outcome, 1)
		go func() {
			result, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-cleanup-timeout", Input: "inspect", Sink: sink})
			done <- outcome{result, runErr}
		}()
		<-entered
		raceGate := make(chan struct{})
		released := make(chan struct{}, 2)
		go func() { <-raceGate; close(releaseModel); released <- struct{}{} }()
		go func() { <-raceGate; cancel(); released <- struct{}{} }()
		close(raceGate)
		<-released
		<-released
		got := <-done
		durable, loadErr := application.ReadWholeStreamPinned(context.Background(), base, created.SessionID, 256)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if len(durable) != 5 || len(got.result.Records) != 4 || !got.result.TerminalCommitted {
			t.Fatalf("iteration %d: durable=%#v result=%#v err=%v", iteration, durable, got.result, got.err)
		}
		types := turnEventTypes(durable)
		completed := reflect.DeepEqual(types[3:], []string{domain.EventAssistantMessageCompleted, domain.EventTurnCompleted})
		interrupted := reflect.DeepEqual(types[3:], []string{domain.EventAssistantMessageInterrupted, domain.EventTurnInterrupted})
		if completed == interrupted {
			t.Fatalf("iteration %d: terminal types = %v, want exactly one completed or interrupted pair", iteration, types)
		}
		if completed {
			if got.result.Status != domain.TurnStatusCompleted || (got.err != nil && !application.IsCategory(got.err, application.CategoryDelivery)) {
				t.Fatalf("iteration %d: completed result=%#v err=%v", iteration, got.result, got.err)
			}
		} else {
			assertRunTurnError(t, got.err, application.CategoryCanceled, "canceled", true)
			if got.result.Status != domain.TurnStatusInterrupted {
				t.Fatalf("iteration %d: interrupted result=%#v", iteration, got.result)
			}
		}
		requests := store.AppendRequests()
		if len(requests) != 3 && len(requests) != 4 {
			t.Fatalf("iteration %d: append attempts=%d, want create/admission/terminal with at most one authorized fallback", iteration, len(requests))
		}
		terminalAttempts := make([]string, 0, 2)
		for _, request := range requests[2:] {
			terminalAttempts = append(terminalAttempts, request.Events[0].Event.EventType())
		}
		// A canceled first complete attempt that did not publish is retried with
		// the same AppendID. That is exact resolution, not a second decision.
		exactCompletedRetry := len(requests) >= 4 && requests[2].AppendID != "" && requests[2].AppendID == requests[3].AppendID &&
			reflect.DeepEqual(terminalAttempts, []string{domain.EventAssistantMessageCompleted, domain.EventAssistantMessageCompleted})
		if len(terminalAttempts) == 2 && !exactCompletedRetry &&
			!reflect.DeepEqual(terminalAttempts, []string{domain.EventAssistantMessageCompleted, domain.EventAssistantMessageInterrupted}) {
			t.Fatalf("iteration %d: terminal attempts=%v appends=%q/%q, want interrupt fallback or exact completed retry", iteration, terminalAttempts, requests[2].AppendID, requests[3].AppendID)
		}
		if completed && !reflect.DeepEqual(terminalAttempts, []string{domain.EventAssistantMessageCompleted}) && !exactCompletedRetry {
			t.Fatalf("iteration %d: completed durable result with terminal attempts=%v", iteration, terminalAttempts)
		}
		if interrupted && !reflect.DeepEqual(terminalAttempts, []string{domain.EventAssistantMessageInterrupted}) &&
			!reflect.DeepEqual(terminalAttempts, []string{domain.EventAssistantMessageCompleted, domain.EventAssistantMessageInterrupted}) {
			t.Fatalf("iteration %d: interrupted durable result with terminal attempts=%v", iteration, terminalAttempts)
		}
		runtimeTypes := runtimeEventTypes(sink.Attempts())
		completedSignals, interruptedSignals := 0, 0
		for _, eventType := range runtimeTypes {
			if eventType == engine.RuntimeModelStreamCompleted {
				completedSignals++
			}
			if eventType == engine.RuntimeModelStreamInterrupted {
				interruptedSignals++
			}
		}
		if completedSignals > 1 || interruptedSignals > 1 || (completedSignals > 0 && interruptedSignals > 0) {
			t.Fatalf("iteration %d: runtime terminal signals=%v", iteration, runtimeTypes)
		}
	}
}

func TestRunTurnAndCloseSessionRaceHasOneCASWinner(t *testing.T) {
	base := newTurnMemoryStore(t)
	ids := testkit.NewSequenceIDs()
	seed := newTurnService(t, base, ids, &repeatingSuccessModel{})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store := newTwoLoadBarrierStore(base, created.SessionID)
	model := &repeatingSuccessModel{text: "done"}
	service := newTurnService(t, store, ids, model)
	type runOutcome struct {
		result application.RunTurnResult
		err    error
	}
	type closeOutcome struct {
		result application.CloseSessionResult
		err    error
	}
	runDone := make(chan runOutcome, 1)
	closeDone := make(chan closeOutcome, 1)
	go func() {
		result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-close-race", Input: "inspect", Sink: &testkit.RecordingSink{}})
		runDone <- runOutcome{result, runErr}
	}()
	go func() {
		result, closeErr := service.CloseSession(context.Background(), application.CloseSessionRequest{SessionID: created.SessionID})
		closeDone <- closeOutcome{result, closeErr}
	}()
	store.WaitBothLoaded()
	store.Release()
	run, closed := <-runDone, <-closeDone
	winners, conflicts := 0, 0
	if run.err == nil {
		winners++
	} else if application.IsCategory(run.err, application.CategoryConflict) {
		conflicts++
	}
	if closed.err == nil {
		winners++
	} else if application.IsCategory(closed.err, application.CategoryConflict) {
		conflicts++
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners/conflicts = %d/%d; run=(%#v,%v) close=(%#v,%v)", winners, conflicts, run.result, run.err, closed.result, closed.err)
	}
	if closed.err == nil && len(model.Calls()) != 0 {
		t.Fatalf("Close won but model calls = %#v", model.Calls())
	}
	if run.err == nil && len(model.Calls()) != 1 {
		t.Fatalf("RunTurn won but model calls = %#v", model.Calls())
	}
}

func TestRunTurnDifferentSessionsExecuteConcurrently(t *testing.T) {
	store := newTurnMemoryStore(t)
	model := newTwoStreamBarrierModel()
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	first, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/two"})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	done := make(chan outcome, 2)
	for _, sessionID := range []domain.SessionID{first.SessionID, second.SessionID} {
		sessionID := sessionID
		go func() {
			result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: sessionID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("request-%s", sessionID)), Input: "inspect", Sink: &testkit.RecordingSink{}})
			done <- outcome{result, runErr}
		}()
	}
	model.WaitBothEntered()
	model.Release()
	for range 2 {
		got := <-done
		if got.err != nil || got.result.Status != domain.TurnStatusCompleted || !got.result.TerminalCommitted {
			t.Fatalf("concurrent result = %#v, err = %v", got.result, got.err)
		}
	}
}

func assertFailedPair(t *testing.T, store application.EventStore, result application.RunTurnResult, code, message string) {
	t.Helper()
	if len(result.Records) != 4 {
		t.Fatalf("records = %#v", result.Records)
	}
	want := []domain.Event{
		domain.TurnStarted{TurnID: result.TurnID, Input: "inspect"},
		domain.AssistantMessageStarted{TurnID: result.TurnID, ItemID: result.ItemID},
		domain.AssistantMessageFailed{TurnID: result.TurnID, ItemID: result.ItemID, Code: code, Message: message},
		domain.TurnFailed{TurnID: result.TurnID, Code: code, Message: message},
	}
	assertExactTurnRecords(t, store, result, want)
}

func assertInterruptedPair(t *testing.T, store application.EventStore, result application.RunTurnResult, code string) {
	t.Helper()
	if result.Status != domain.TurnStatusInterrupted || result.Text != "" || !result.TerminalCommitted || len(result.Records) != 4 {
		t.Fatalf("result = %#v", result)
	}
	want := []domain.Event{
		domain.TurnStarted{TurnID: result.TurnID, Input: "inspect"},
		domain.AssistantMessageStarted{TurnID: result.TurnID, ItemID: result.ItemID},
		domain.AssistantMessageInterrupted{TurnID: result.TurnID, ItemID: result.ItemID, Code: code, Message: ""},
		domain.TurnInterrupted{TurnID: result.TurnID, Reason: code},
	}
	assertExactTurnRecords(t, store, result, want)
}

func assertExactTurnRecords(t *testing.T, store application.EventStore, result application.RunTurnResult, want []domain.Event) {
	t.Helper()
	commandID := result.Records[0].CommandID
	for index, event := range want {
		if result.Records[index].CommandID != commandID || !reflect.DeepEqual(result.Records[index].Event, event) {
			t.Fatalf("record[%d] = %#v, want %#v", index, result.Records[index], event)
		}
	}
	durable, err := application.ReadWholeStreamPinned(context.Background(), store, result.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable) != 5 || !reflect.DeepEqual(durable[1:], result.Records) {
		t.Fatalf("durable = %#v, result = %#v", durable, result.Records)
	}
	state, err := domain.Replay(durable)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveTurn != nil {
		t.Fatalf("partial output left an active turn: %#v", state)
	}
}

func assertRunningBoundary(t *testing.T, store application.EventStore, result application.RunTurnResult) {
	t.Helper()
	if result.Status != domain.TurnStatusRunning || result.TerminalCommitted || len(result.Records) != 2 || result.DeliveryWarning != nil {
		t.Fatalf("running result = %#v", result)
	}
	durable, err := application.ReadWholeStreamPinned(context.Background(), store, result.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	state, err := domain.Replay(durable)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable) != 3 || state.ActiveTurn == nil || state.ActiveTurn.ID != result.TurnID || state.ActiveTurn.ActiveItem == nil || state.ActiveTurn.ActiveItem.ID != result.ItemID {
		t.Fatalf("running durable boundary = %#v", state)
	}
}

func newTurnServiceWithConfig(t *testing.T, store application.EventStore, ids application.IDGenerator, model engine.Model, config application.Config) *application.Service {
	t.Helper()
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, ids, testkit.FixedClock{Time: time.Date(2026, 8, 12, 6, 7, 8, 9, time.UTC)}, runner, v2Authority, config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type failTerminalStore struct {
	application.EventStore
	mu            sync.Mutex
	failure       error
	terminalCalls int
}

func (store *failTerminalStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	if len(request.Events) > 0 {
		switch request.Events[0].Event.EventType() {
		case domain.EventAssistantMessageCompleted, domain.EventAssistantMessageFailed, domain.EventAssistantMessageInterrupted:
			store.mu.Lock()
			store.terminalCalls++
			store.mu.Unlock()
			return application.CommitReceipt{}, store.failure
		}
	}
	return store.EventStore.Append(ctx, request)
}

func (store *failTerminalStore) TerminalCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.terminalCalls
}

type invalidTerminalReturnStore struct {
	application.EventStore
}

func (store *invalidTerminalReturnStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	receipt, err := store.EventStore.Append(ctx, request)
	if err != nil || len(request.Events) == 0 || request.Events[0].Event.EventType() != domain.EventAssistantMessageFailed {
		return receipt, err
	}
	receipt.FirstSequence++
	receipt.LastSequence++
	return receipt, nil
}

type blockingTerminalCleanupStore struct {
	application.EventStore
	mu             sync.Mutex
	sawLiveBounded bool
}

func (store *blockingTerminalCleanupStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	if len(request.Events) == 0 || (request.Events[0].Event.EventType() != domain.EventAssistantMessageFailed && request.Events[0].Event.EventType() != domain.EventAssistantMessageInterrupted) {
		return store.EventStore.Append(ctx, request)
	}
	_, bounded := ctx.Deadline()
	store.mu.Lock()
	store.sawLiveBounded = ctx.Err() == nil && bounded
	store.mu.Unlock()
	<-ctx.Done()
	return application.CommitReceipt{}, ctx.Err()
}

func (store *blockingTerminalCleanupStore) SawLiveBoundedContext() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.sawLiveBounded
}

type durableObservingSink struct {
	testkit.RecordingSink
	store     application.EventStore
	sessionID domain.SessionID

	mu       sync.Mutex
	observed int
	valid    bool
}

func (sink *durableObservingSink) Emit(ctx context.Context, event engine.RuntimeEvent) error {
	if event.Type == engine.RuntimeAppendCompleted || event.Type == engine.RuntimeModelStreamInterrupted {
		records, err := application.ReadWholeStreamPinned(context.Background(), sink.store, sink.sessionID, 256)
		types := turnEventTypes(records)
		valid := err == nil && len(types) == 5 && reflect.DeepEqual(types[3:], []string{domain.EventAssistantMessageInterrupted, domain.EventTurnInterrupted})
		sink.mu.Lock()
		if sink.observed == 0 {
			sink.valid = true
		}
		sink.observed++
		sink.valid = sink.valid && valid
		sink.mu.Unlock()
	}
	return sink.RecordingSink.Emit(ctx, event)
}

func (sink *durableObservingSink) ObservedDurableTerminalBeforeSignals() bool {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.observed == 2 && sink.valid
}

type cancelCompletedAppendStore struct {
	application.EventStore
	mu               sync.Mutex
	cancel           context.CancelFunc
	failure          error
	completedCalls   int
	interruptedCalls int
}

func (store *cancelCompletedAppendStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	if len(request.Events) > 0 && request.Events[0].Event.EventType() == domain.EventAssistantMessageCompleted {
		store.mu.Lock()
		store.completedCalls++
		store.mu.Unlock()
		store.cancel()
		return application.CommitReceipt{}, store.failure
	}
	if len(request.Events) > 0 && request.Events[0].Event.EventType() == domain.EventAssistantMessageInterrupted {
		store.mu.Lock()
		store.interruptedCalls++
		store.mu.Unlock()
	}
	return store.EventStore.Append(ctx, request)
}

func (store *cancelCompletedAppendStore) CompletedCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.completedCalls
}
func (store *cancelCompletedAppendStore) InterruptedCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.interruptedCalls
}

type cancelAfterAdmissionStore struct {
	application.EventStore
	mu                 sync.Mutex
	cancel             context.CancelFunc
	sawDetachedBounded bool
}

func (store *cancelAfterAdmissionStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	records, err := store.EventStore.Append(ctx, request)
	if err != nil || len(request.Events) == 0 {
		return records, err
	}
	switch request.Events[0].Event.EventType() {
	case domain.EventTurnStarted:
		store.cancel()
	case domain.EventAssistantMessageInterrupted:
		_, bounded := ctx.Deadline()
		store.mu.Lock()
		store.sawDetachedBounded = ctx.Err() == nil && bounded
		store.mu.Unlock()
	}
	return records, nil
}

func (store *cancelAfterAdmissionStore) SawDetachedBoundedContext() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.sawDetachedBounded
}

type corruptAdmissionReceiptStore struct {
	application.EventStore
	mu          sync.Mutex
	targetCalls int
}

func (store *corruptAdmissionReceiptStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	records, err := store.EventStore.Append(ctx, request)
	if err != nil || len(request.Events) == 0 || request.Events[0].Event.EventType() != domain.EventTurnStarted {
		return records, err
	}
	store.mu.Lock()
	store.targetCalls++
	store.mu.Unlock()
	records.FirstSequence++
	records.LastSequence++
	return records, nil
}

func (store *corruptAdmissionReceiptStore) TargetCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.targetCalls
}

type twoLoadBarrierStore struct {
	application.EventStore
	sessionID domain.SessionID
	entered   chan struct{}
	release   chan struct{}
}

func newTwoLoadBarrierStore(store application.EventStore, sessionID domain.SessionID) *twoLoadBarrierStore {
	return &twoLoadBarrierStore{EventStore: store, sessionID: sessionID, entered: make(chan struct{}, 2), release: make(chan struct{})}
}

func (store *twoLoadBarrierStore) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	page, err := store.EventStore.ReadStream(ctx, request)
	if err != nil || request.SessionID != store.sessionID || request.AfterSequence != 0 {
		return page, err
	}
	store.entered <- struct{}{}
	select {
	case <-store.release:
		return page, nil
	case <-ctx.Done():
		return application.StreamPage{}, ctx.Err()
	}
}

func (store *twoLoadBarrierStore) WaitBothLoaded() { <-store.entered; <-store.entered }
func (store *twoLoadBarrierStore) Release()        { close(store.release) }

type twoStreamBarrierModel struct {
	entered chan struct{}
	release chan struct{}
}

func newTwoStreamBarrierModel() *twoStreamBarrierModel {
	return &twoStreamBarrierModel{entered: make(chan struct{}, 2), release: make(chan struct{})}
}

func (model *twoStreamBarrierModel) Stream(ctx context.Context, _ engine.ModelRequest) (engine.ModelStream, error) {
	model.entered <- struct{}{}
	select {
	case <-model.release:
		return &turnSuccessStream{events: []engine.StreamEvent{{Type: engine.StreamEventCompleted}}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (model *twoStreamBarrierModel) WaitBothEntered() { <-model.entered; <-model.entered }
func (model *twoStreamBarrierModel) Release()         { close(model.release) }
