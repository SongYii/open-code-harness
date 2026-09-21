package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

type childOutcomeModel struct {
	mu       sync.Mutex
	calls    int
	entered  chan struct{}
	childErr error
}

type concurrentDelegationModel struct{}

func (*concurrentDelegationModel) Stream(_ context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	if strings.HasPrefix(request.Input, "child-") {
		return &turnSuccessStream{events: []engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "answer-" + strings.TrimPrefix(request.Input, "child-")}, {Type: engine.StreamEventCompleted}}}, nil
	}
	for _, message := range request.Messages {
		if message.Role == domain.PromptRoleTool {
			return &turnSuccessStream{events: []engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "done-" + strings.TrimPrefix(request.Input, "parent-")}, {Type: engine.StreamEventCompleted}}}, nil
		}
	}
	suffix := strings.TrimPrefix(request.Input, "parent-")
	return &turnSuccessStream{events: []engine.StreamEvent{
		{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "delegate-" + suffix, Name: tools.NameDelegateTask, Arguments: `{"task":"child-` + suffix + `"}`}},
		{Type: engine.StreamEventCompleted},
	}}, nil
}

func (model *childOutcomeModel) Stream(context.Context, engine.ModelRequest) (engine.ModelStream, error) {
	model.mu.Lock()
	model.calls++
	call := model.calls
	model.mu.Unlock()
	if call == 1 {
		return &turnSuccessStream{events: []engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-delegate", Name: tools.NameDelegateTask, Arguments: `{"task":"slow child"}`}},
			{Type: engine.StreamEventCompleted},
		}}, nil
	}
	if call == 2 {
		if model.childErr != nil {
			return nil, model.childErr
		}
		return &waitCancelStream{entered: model.entered}, nil
	}
	return &turnSuccessStream{events: []engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "parent recovered"}, {Type: engine.StreamEventCompleted}}}, nil
}

func TestDelegateTaskCallerCancellationStopsParentAndChild(t *testing.T) {
	model := &childOutcomeModel{entered: make(chan struct{})}
	config := application.DefaultConfig()
	config.Subagents.Enabled = true
	service, _ := newToolService(t, model, testkit.NewMemFS("/workspace"), nil, nil, config)
	parent, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: parent.SessionID, RequestID: "request-parent", Input: "delegate", Sink: &testkit.RecordingSink{}})
		done <- runErr
	}()
	<-model.entered
	cancel()
	if err := <-done; !application.IsCategory(err, application.CategoryCanceled) {
		t.Fatalf("RunTurn() error = %v, want canceled", err)
	}
	for _, id := range []domain.SessionID{parent.SessionID, "session-2"} {
		state, err := service.LoadSession(context.Background(), id)
		if err != nil || state.ActiveTurn != nil {
			t.Fatalf("session %s after cancel = (%#v, %v)", id, state, err)
		}
	}
}

func TestDelegateTaskPreCanceledRequestCreatesNoChild(t *testing.T) {
	config := application.DefaultConfig()
	config.Subagents.Enabled = true
	service, _ := newToolService(t, &childOutcomeModel{}, testkit.NewMemFS("/workspace"), nil, nil, config)
	parent, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: parent.SessionID, RequestID: "request-parent", Input: "delegate", Sink: &testkit.RecordingSink{}}); !application.IsCategory(err, application.CategoryCanceled) {
		t.Fatalf("RunTurn() error = %v, want canceled", err)
	}
	page, err := service.ListSessions(context.Background(), application.ListSessionsRequest{WorkspaceRoot: "/workspace"})
	if err != nil || len(page.Sessions) != 1 {
		t.Fatalf("sessions = (%#v, %v), want parent only", page, err)
	}
}

func TestDelegateTaskTimeoutIsStableToolFailure(t *testing.T) {
	model := &childOutcomeModel{entered: make(chan struct{})}
	config := application.DefaultConfig()
	config.Subagents = application.SubagentConfig{Enabled: true, Timeout: 20 * time.Millisecond}
	service, store := newToolService(t, model, testkit.NewMemFS("/workspace"), nil, nil, config)
	parent, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: parent.SessionID, RequestID: "request-parent", Input: "delegate", Sink: &testkit.RecordingSink{}}); err != nil {
		t.Fatal(err)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, parent.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	failed := lastToolFailed(records)
	if failed.Code != application.CodeSubagentTimeout || failed.Message != application.ToolTextSubagentTimeout {
		t.Fatalf("tool failure = %#v", failed)
	}
}

func TestDelegateTaskRedactsChildProviderFailure(t *testing.T) {
	const secret = "provider-secret-canary"
	model := &childOutcomeModel{childErr: errors.New(secret)}
	config := application.DefaultConfig()
	config.Subagents.Enabled = true
	service, store := newToolService(t, model, testkit.NewMemFS("/workspace"), nil, nil, config)
	parent, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: parent.SessionID, RequestID: "request-parent", Input: "delegate", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Text != "parent recovered" {
		t.Fatalf("RunTurn() = (%#v, %v)", result, err)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, parent.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	failed := lastToolFailed(records)
	if failed.Code != application.CodeSubagentFailed || failed.Message != application.ToolTextSubagentFailed {
		t.Fatalf("tool failure = %#v", failed)
	}
	for _, record := range records {
		wire, marshalErr := domain.MarshalRecordedEvent(record)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(wire), secret) {
			t.Fatal("raw child provider error leaked into parent events")
		}
	}
}

func TestDelegateTaskConcurrentParentsKeepLineageIsolated(t *testing.T) {
	config := application.DefaultConfig()
	config.Subagents.Enabled = true
	service, _ := newToolService(t, &concurrentDelegationModel{}, testkit.NewMemFS("/workspace"), nil, nil, config)
	parents := make([]application.CreateSessionResult, 2)
	for index := range parents {
		created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		if err != nil {
			t.Fatal(err)
		}
		parents[index] = created
	}
	errs := make(chan error, 2)
	for index, parent := range parents {
		go func(index int, parent application.CreateSessionResult) {
			suffix := string(rune('a' + index))
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: parent.SessionID, RequestID: domain.RunTurnRequestID("request-" + suffix), Input: "parent-" + suffix, Sink: &testkit.RecordingSink{}})
			if err == nil && result.Text != "done-"+suffix {
				err = errors.New("wrong parent result")
			}
			errs <- err
		}(index, parent)
	}
	for range parents {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.ListSessions(context.Background(), application.ListSessionsRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	lineage := make(map[domain.SessionID]string)
	for _, listed := range page.Sessions {
		state, loadErr := service.LoadSession(context.Background(), listed.SessionID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if state.Parent != nil {
			lineage[state.Parent.SessionID] = state.Parent.CallID
		}
	}
	if lineage[parents[0].SessionID] != "delegate-a" || lineage[parents[1].SessionID] != "delegate-b" || len(lineage) != 2 {
		t.Fatalf("child lineage = %#v", lineage)
	}
}

func TestDelegateTaskUnknownParentResultDoesNotRunSecondChild(t *testing.T) {
	baseStore := newTurnMemoryStore(t)
	store := &eventTypeFaultStore{EventStore: baseStore, target: domain.EventToolCallCompleted, mode: faultExhaust}
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-delegate", Name: tools.NameDelegateTask, Arguments: `{"task":"inspect"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "child answer"}, {Type: engine.StreamEventCompleted}},
	)
	config := application.DefaultConfig()
	config.Subagents.Enabled = true
	service := newToolServiceWithStore(t, store, model, testkit.NewMemFS("/workspace"), nil, nil, config)
	parent, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	request := application.RunTurnRequest{SessionID: parent.SessionID, RequestID: "request-parent", Input: "delegate", Sink: &testkit.RecordingSink{}}
	if _, err := service.RunTurn(context.Background(), request); !isUnknown(err) {
		t.Fatalf("RunTurn() error = %v, want unknown outcome", err)
	}
	if len(model.Calls()) != 2 {
		t.Fatalf("provider calls = %d, want parent plus exactly one child", len(model.Calls()))
	}
	retryCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := service.RunTurn(retryCtx, request); err == nil {
		t.Fatal("retry unexpectedly succeeded")
	}
	if len(model.Calls()) != 2 {
		t.Fatalf("retry ran another child; provider calls = %d", len(model.Calls()))
	}
	page, err := service.ListSessions(context.Background(), application.ListSessionsRequest{WorkspaceRoot: "/workspace"})
	if err != nil || len(page.Sessions) != 2 {
		t.Fatalf("sessions = (%#v, %v), want one parent and one child", page, err)
	}
}
