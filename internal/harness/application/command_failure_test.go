package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestUnprovenCommandEndsTurnWithMatchingDurableAndLiveFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		cause      error
		code, text string
	}{
		{"drain", tools.ErrCommandOutputIncomplete, application.CodeExecOutputIncomplete, application.ToolTextExecOutputIncomplete},
		{"wait", tools.ErrCommandWaitUnproven, application.CodeExecWaitUnproven, application.ToolTextExecWaitUnproven},
		{"teardown", tools.ErrCommandTeardownUnproven, application.CodeExecTeardownUnproven, application.ToolTextExecTeardownUnproven},
		{"cancel_unproven", errors.Join(context.Canceled, tools.ErrCommandTeardownUnproven), application.CodeExecTeardownUnproven, application.ToolTextExecTeardownUnproven},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := newSequenceModel(
				[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-exec", Name: tools.NameExec, Arguments: `{"argv":["bin/tool"]}`}}, {Type: engine.StreamEventCompleted}},
				[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "unsafe retry"}, {Type: engine.StreamEventCompleted}},
			)
			runner := testkit.NewScriptedRunner(testkit.ScriptedRun{Err: errors.Join(test.cause, errors.New("secret-token raw argv path"))})
			approver := testkit.NewScriptedApprover(testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: true}})
			service, store := newToolService(t, model, testkit.NewMemFS("/workspace"), runner, approver, application.DefaultConfig())
			created, err := service.CreateSession(t.Context(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			sink := &testkit.RecordingSink{}
			result, err := service.RunTurn(t.Context(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-unsafe-exec", Input: "inspect", Sink: sink})
			var typed *application.Error
			if !errors.As(err, &typed) || typed.Category != application.CategoryInternal || typed.Code != test.code || !result.TerminalCommitted || result.Status != domain.TurnStatusFailed {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if !errors.Is(err, test.cause) && test.name != "cancel_unproven" {
				t.Fatalf("cause lost: %v", err)
			}
			if test.name == "cancel_unproven" && (!errors.Is(err, context.Canceled) || !errors.Is(err, tools.ErrCommandTeardownUnproven)) {
				t.Fatalf("joined cancellation lost: %v", err)
			}
			if len(model.Calls()) != 1 || len(runner.Calls()) != 1 {
				t.Fatal("unknown execution was followed by an automatic retry")
			}
			records, readErr := application.ReadWholeStreamPinned(t.Context(), store, created.SessionID, 256)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if countEventType(records, domain.EventToolCallFailed) != 1 || countEventType(records, domain.EventTurnFailed) != 1 || countEventType(records, domain.EventToolCallCompleted) != 0 {
				t.Fatalf("terminal events=%v", turnEventTypes(records))
			}
			failed := lastToolFailed(records)
			live := runtimeEventOfType(t, sink.Delivered(), engine.RuntimeToolExecutionFailed)
			if failed.Code != test.code || failed.Message != test.text || live.Code != test.code || live.Content != test.text {
				t.Fatalf("durable=%+v live=%+v", failed, live)
			}
			if strings.Contains(err.Error()+failed.Message+live.Content, "secret-token") {
				t.Fatal("raw command failure leaked into user-facing evidence")
			}
			// Recreate Service against the same canonical store, so a local
			// registry cache cannot hide a reconstruction/category defect.
			freshModel := newSequenceModel([]engine.StreamEvent{{Type: engine.StreamEventCompleted}})
			freshRunner := testkit.NewScriptedRunner()
			fresh := newToolServiceWithStore(t, store, freshModel, testkit.NewMemFS("/workspace"), freshRunner, approver, application.DefaultConfig())
			replayed, replayErr := fresh.RunTurn(t.Context(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "request-unsafe-exec", Input: "inspect", Sink: &testkit.RecordingSink{}})
			var replayTyped *application.Error
			if !errors.As(replayErr, &replayTyped) || replayTyped.Category != application.CategoryInternal || replayTyped.Code != test.code || replayed.Status != domain.TurnStatusFailed || !replayed.TerminalCommitted {
				t.Fatalf("replay=%+v error=%v", replayed, replayErr)
			}
			if len(freshModel.Calls()) != 0 || len(freshRunner.Calls()) != 0 {
				t.Fatal("durable failure replay re-executed the command")
			}
		})
	}
}

func TestUnprovenCommandDoesNotAnnounceUncommittedFailure(t *testing.T) {
	base := newTurnMemoryStore(t)
	store := &eventTypeFaultStore{EventStore: base, target: domain.EventToolCallFailed, mode: faultExhaust}
	model := newSequenceModel([]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-exec", Name: tools.NameExec, Arguments: `{"argv":["bin/tool"]}`}}, {Type: engine.StreamEventCompleted}})
	runner := testkit.NewScriptedRunner(testkit.ScriptedRun{Err: tools.ErrCommandOutputIncomplete})
	approver := testkit.NewScriptedApprover(testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: true}})
	service := newToolServiceWithStore(t, store, model, testkit.NewMemFS("/workspace"), runner, approver, application.DefaultConfig())
	created, err := service.CreateSession(t.Context(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	sink := &testkit.RecordingSink{}
	request := application.RunTurnRequest{SessionID: created.SessionID, RequestID: "unknown-command-terminal", Input: "inspect", Sink: sink}
	result, runErr := service.RunTurn(t.Context(), request)
	if !isUnknown(runErr) || result.TerminalCommitted {
		t.Fatalf("terminal=%v error=%v", result.TerminalCommitted, runErr)
	}
	for _, event := range sink.Delivered() {
		if event.Type == engine.RuntimeToolExecutionFailed || event.Type == engine.RuntimeToolExecutionCompleted {
			t.Fatalf("uncommitted terminal was announced: %+v", event)
		}
	}
	records, err := application.ReadWholeStreamPinned(t.Context(), base, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	if countEventType(records, domain.EventTurnFailed) != 0 || countEventType(records, domain.EventToolCallFailed) != 0 {
		t.Fatal("unknown append invented durable terminal facts")
	}
	_, retryErr := service.RunTurn(t.Context(), request)
	var retryTyped *application.Error
	if !errors.As(retryErr, &retryTyped) || retryTyped.Code != application.CodeReconciliationRequired || len(model.Calls()) != 1 || len(runner.Calls()) != 1 {
		t.Fatalf("unknown terminal retry re-executed command: %v", retryErr)
	}
}
