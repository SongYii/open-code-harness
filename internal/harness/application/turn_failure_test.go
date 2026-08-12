package application_test

import (
	"context"
	"errors"
	"io"
	"reflect"
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
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: expected.Input, Sink: sink})
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
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
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
		result, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: sink})
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
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{FailOrdinal: ordinal, Failure: cause}})
			assertRunTurnError(t, err, application.CategoryDelivery, "runtime_delivery_failed", true)
			if !errors.Is(err, cause) {
				t.Fatalf("error %v does not preserve sink cause", err)
			}
			if !errors.Is(err, closeCause) {
				t.Fatalf("error %v does not preserve close cause", err)
			}
			assertInterruptedPair(t, store, result, domain.InterruptionDeliveryFailed)
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
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
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
		name     string
		wrap     func(application.EventStore) application.EventStore
		category application.ErrorCategory
		code     string
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
			category: application.CategoryInternal,
			code:     "store_contract_violation",
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
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
			assertRunTurnError(t, err, test.category, test.code, false)
			if !errors.Is(err, executionCause) {
				t.Fatalf("error = %v, want execution cause", err)
			}
			if test.category == application.CategoryConflict && !application.IsVersionConflict(err) {
				t.Fatalf("error = %v, want conflict cause", err)
			}
			if test.category == application.CategoryInternal && !domain.IsCode(err, domain.CodeInvalidEvent) {
				t.Fatalf("error = %v, want Apply cause", err)
			}
			assertRunningBoundary(t, base, result)
		})
	}
}

func TestRunTurnCompletedAppendCancellationFallsBackOnceToInterrupted(t *testing.T) {
	base := newTurnMemoryStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	appendCause := errors.New("success append canceled")
	store := &cancelCompletedAppendStore{EventStore: base, cancel: cancel, failure: appendCause}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), &repeatingSuccessModel{text: "done"})
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryCanceled, "canceled", true)
	if !errors.Is(err, appendCause) || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want append and cancellation causes", err)
	}
	if store.CompletedCalls() != 1 || store.InterruptedCalls() != 1 {
		t.Fatalf("completed/interrupted calls = %d/%d", store.CompletedCalls(), store.InterruptedCalls())
	}
	assertInterruptedPair(t, base, result, domain.InterruptionCallerCanceled)
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
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
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
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: sink})
	assertRunTurnError(t, err, application.CategoryCanceled, "canceled", true)
	assertInterruptedPair(t, base, result, domain.InterruptionCallerCanceled)
	if !store.SawDetachedBoundedContext() {
		t.Fatal("interruption append did not receive a live bounded context detached from caller cancellation")
	}
	if len(model.Calls()) != 0 || len(sink.Attempts()) != 0 || result.DeliveryWarning != context.Canceled {
		t.Fatalf("post-admission cancellation: model=%#v sink=%#v warning=%v", model.Calls(), sink.Attempts(), result.DeliveryWarning)
	}
}

func TestRunTurnCancellationAtCompletedTerminalEntryCommitsOnlyInterruption(t *testing.T) {
	base := newTurnMemoryStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cause := errors.New("completed append did not commit")
	store := &cancelCompletedAppendStore{EventStore: base, cancel: cancel, failure: cause}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), &repeatingSuccessModel{text: "done"})
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryCanceled, "canceled", true)
	assertInterruptedPair(t, base, result, domain.InterruptionCallerCanceled)
	durable, loadErr := base.Load(context.Background(), created.SessionID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if got := turnEventTypes(durable); !reflect.DeepEqual(got, []string{
		domain.EventSessionCreated,
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventAssistantMessageInterrupted,
		domain.EventTurnInterrupted,
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
	serviceForSeed := newTurnService(t, base, testkit.NewSequenceIDs(), &repeatingSuccessModel{})
	created, err := serviceForSeed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store := &mutateRunAppendStore{EventStore: base, targetType: domain.EventTurnStarted, mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
		records[1].Sequence++
		return records
	}}
	model := &repeatingSuccessModel{text: "unused"}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
	assertRunTurnError(t, err, application.CategoryInternal, "store_contract_violation", false)
	if !reflect.DeepEqual(result, application.RunTurnResult{}) || len(model.Calls()) != 0 {
		t.Fatalf("malformed admission result = %#v, model calls = %#v", result, model.Calls())
	}
}

func TestRunTurnRejectsMalformedTerminalAppendReturnsWithoutSuccessSignal(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]domain.RecordedEvent) []domain.RecordedEvent
	}{
		{name: "count", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent { return records[:1] }},
		{name: "sequence gap", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent { records[1].Sequence++; return records }},
		{name: "wrong session", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			records[0].SessionID = "session-other"
			return records
		}},
		{name: "wrong command", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			records[0].CommandID = "command-other"
			return records
		}},
		{name: "schema", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			records[0].SchemaVersion = 2
			return records
		}},
		{name: "event ID", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			records[0].ID = " invalid"
			return records
		}},
		{name: "non UTC", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			records[0].OccurredAt = records[0].OccurredAt.In(time.FixedZone("offset", 3600))
			return records
		}},
		{name: "timestamp mismatch", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			records[1].OccurredAt = records[1].OccurredAt.Add(time.Nanosecond)
			return records
		}},
		{name: "changed type", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			records[0].Event = domain.SessionClosed{}
			return records
		}},
		{name: "changed payload", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			event := records[0].Event.(domain.AssistantMessageCompleted)
			event.Text = "changed"
			records[0].Event = event
			return records
		}},
		{name: "changed order", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			records[0].Event, records[1].Event = records[1].Event, records[0].Event
			return records
		}},
		{name: "apply failure", mutate: func(records []domain.RecordedEvent) []domain.RecordedEvent {
			occurredAt := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			records[0].OccurredAt, records[1].OccurredAt = occurredAt, occurredAt
			return records
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := newTurnMemoryStore(t)
			seed := newTurnService(t, base, testkit.NewSequenceIDs(), &repeatingSuccessModel{})
			created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			store := &mutateRunAppendStore{EventStore: base, targetType: domain.EventAssistantMessageCompleted, mutate: test.mutate}
			sink := &testkit.RecordingSink{}
			service := newTurnService(t, store, testkit.NewSequenceIDs(), &repeatingSuccessModel{text: "done"})
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: sink})
			assertRunTurnError(t, err, application.CategoryInternal, "store_contract_violation", false)
			if result.Status != domain.TurnStatusRunning || result.TerminalCommitted || len(result.Records) != 2 {
				t.Fatalf("result = %#v", result)
			}
			if got := runtimeEventTypes(sink.Attempts()); !reflect.DeepEqual(got, []engine.RuntimeEventType{engine.RuntimeModelStreamStarted, engine.RuntimeModelTextDelta}) {
				t.Fatalf("runtime attempts = %v, want no terminal success signal", got)
			}
			if store.TargetCalls() != 1 {
				t.Fatalf("terminal append calls = %d, want no retry", store.TargetCalls())
			}
		})
	}
}

func TestRunTurnConcurrentSameSessionHasOneAdmissionWinner(t *testing.T) {
	base := newTurnMemoryStore(t)
	seed := newTurnService(t, base, testkit.NewSequenceIDs(), &repeatingSuccessModel{})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store := newTwoLoadBarrierStore(base, created.SessionID)
	model := &repeatingSuccessModel{text: "done"}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for _, input := range []string{"first", "second"} {
		input := input
		go func() {
			result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: input, Sink: &testkit.RecordingSink{}})
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

func TestRunTurnAndCloseSessionRaceHasOneCASWinner(t *testing.T) {
	base := newTurnMemoryStore(t)
	seed := newTurnService(t, base, testkit.NewSequenceIDs(), &repeatingSuccessModel{})
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	store := newTwoLoadBarrierStore(base, created.SessionID)
	model := &repeatingSuccessModel{text: "done"}
	service := newTurnService(t, store, testkit.NewSequenceIDs(), model)
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
		result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
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
			result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: sessionID, Input: "inspect", Sink: &testkit.RecordingSink{}})
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
	durable, err := store.Load(context.Background(), result.SessionID)
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
	item := state.Turns[result.TurnID].Items[result.ItemID]
	if item.Status == domain.ItemStatusCompleted {
		t.Fatalf("partial output persisted as completed: %#v", item)
	}
}

func assertRunningBoundary(t *testing.T, store application.EventStore, result application.RunTurnResult) {
	t.Helper()
	if result.Status != domain.TurnStatusRunning || result.TerminalCommitted || len(result.Records) != 2 || result.DeliveryWarning != nil {
		t.Fatalf("running result = %#v", result)
	}
	durable, err := store.Load(context.Background(), result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := domain.Replay(durable)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable) != 3 || state.ActiveTurnID != result.TurnID || state.Turns[result.TurnID].ActiveItemID != result.ItemID {
		t.Fatalf("running durable boundary = %#v", state)
	}
}

func newTurnServiceWithConfig(t *testing.T, store application.EventStore, ids application.IDGenerator, model engine.Model, config application.Config) *application.Service {
	t.Helper()
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, ids, runner, config)
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

func (store *failTerminalStore) Append(ctx context.Context, request application.AppendRequest) ([]domain.RecordedEvent, error) {
	if len(request.Events) > 0 {
		switch request.Events[0].EventType() {
		case domain.EventAssistantMessageCompleted, domain.EventAssistantMessageFailed, domain.EventAssistantMessageInterrupted:
			store.mu.Lock()
			store.terminalCalls++
			store.mu.Unlock()
			return nil, store.failure
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

func (store *invalidTerminalReturnStore) Append(ctx context.Context, request application.AppendRequest) ([]domain.RecordedEvent, error) {
	if len(request.Events) == 0 || request.Events[0].EventType() != domain.EventAssistantMessageFailed {
		return store.EventStore.Append(ctx, request)
	}
	occurredAt := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	records := make([]domain.RecordedEvent, len(request.Events))
	for index, event := range request.Events {
		records[index] = domain.RecordedEvent{
			SchemaVersion: 1,
			ID:            domain.EventID("event-invalid-terminal-" + string(rune('1'+index))),
			CommandID:     request.CommandID,
			SessionID:     request.SessionID,
			Sequence:      request.ExpectedVersion + uint64(index) + 1,
			OccurredAt:    occurredAt,
			Event:         event,
		}
	}
	return records, nil
}

type cancelCompletedAppendStore struct {
	application.EventStore
	mu               sync.Mutex
	cancel           context.CancelFunc
	failure          error
	completedCalls   int
	interruptedCalls int
}

func (store *cancelCompletedAppendStore) Append(ctx context.Context, request application.AppendRequest) ([]domain.RecordedEvent, error) {
	if len(request.Events) > 0 && request.Events[0].EventType() == domain.EventAssistantMessageCompleted {
		store.mu.Lock()
		store.completedCalls++
		store.mu.Unlock()
		store.cancel()
		return nil, store.failure
	}
	if len(request.Events) > 0 && request.Events[0].EventType() == domain.EventAssistantMessageInterrupted {
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

func (store *cancelAfterAdmissionStore) Append(ctx context.Context, request application.AppendRequest) ([]domain.RecordedEvent, error) {
	records, err := store.EventStore.Append(ctx, request)
	if err != nil || len(request.Events) == 0 {
		return records, err
	}
	switch request.Events[0].EventType() {
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

type mutateRunAppendStore struct {
	application.EventStore
	mu          sync.Mutex
	targetType  string
	mutate      func([]domain.RecordedEvent) []domain.RecordedEvent
	targetCalls int
}

func (store *mutateRunAppendStore) Append(ctx context.Context, request application.AppendRequest) ([]domain.RecordedEvent, error) {
	records, err := store.EventStore.Append(ctx, request)
	if err != nil || len(request.Events) == 0 || request.Events[0].EventType() != store.targetType {
		return records, err
	}
	store.mu.Lock()
	store.targetCalls++
	store.mu.Unlock()
	cloned, cloneErr := domain.CloneRecordedEvents(records)
	if cloneErr != nil {
		return nil, cloneErr
	}
	return store.mutate(cloned), nil
}

func (store *mutateRunAppendStore) TargetCalls() int {
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

func (store *twoLoadBarrierStore) Load(ctx context.Context, sessionID domain.SessionID) ([]domain.RecordedEvent, error) {
	records, err := store.EventStore.Load(ctx, sessionID)
	if err != nil || sessionID != store.sessionID {
		return records, err
	}
	store.entered <- struct{}{}
	select {
	case <-store.release:
		return records, nil
	case <-ctx.Done():
		return nil, ctx.Err()
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
