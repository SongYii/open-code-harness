package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestNewServiceToolComposition(t *testing.T) {
	store := newTurnMemoryStore(t)
	ids := testkit.NewSequenceIDs()
	runner := sessionRunnerForTest(t)
	catalog, err := tools.NewCatalog(tools.DefaultWorkspaceSpecs())
	if err != nil {
		t.Fatal(err)
	}
	identity := scriptedToolIdentity()
	files := testkit.NewMemFS("/workspace")
	commands := testkit.NewScriptedRunner()

	t.Run("required without catalog", func(t *testing.T) {
		config := application.DefaultConfig()
		required := identity
		required.Profile.NativeTools = engine.CapabilityRequired
		config.RequestIdentity = &required
		_, err := application.NewService(store, ids, testkit.FixedClock{Time: toolClock()}, runner, v2Authority, config)
		assertApplicationError(t, err, application.CategoryPolicy, "invalid_configuration")
	})
	t.Run("catalog without identity", func(t *testing.T) {
		config := application.DefaultConfig()
		config.Catalog = catalog
		config.Files = files
		config.Commands = commands
		_, err := application.NewService(store, ids, testkit.FixedClock{Time: toolClock()}, runner, v2Authority, config)
		assertApplicationError(t, err, application.CategoryPolicy, "invalid_configuration")
	})
	t.Run("catalog with unsupported", func(t *testing.T) {
		config := application.DefaultConfig()
		unsupported := identity
		unsupported.Profile.NativeTools = engine.CapabilityUnsupported
		config.RequestIdentity = &unsupported
		config.Catalog = catalog
		config.Files = files
		config.Commands = commands
		_, err := application.NewService(store, ids, testkit.FixedClock{Time: toolClock()}, runner, v2Authority, config)
		assertApplicationError(t, err, application.CategoryPolicy, "invalid_configuration")
	})
	t.Run("catalog missing files", func(t *testing.T) {
		config := toolConfig(catalog, nil, commands, nil)
		_, err := application.NewService(store, ids, testkit.FixedClock{Time: toolClock()}, runner, v2Authority, config)
		assertApplicationError(t, err, application.CategoryPolicy, "invalid_configuration")
	})
	t.Run("catalog missing commands", func(t *testing.T) {
		config := toolConfig(catalog, files, nil, nil)
		_, err := application.NewService(store, ids, testkit.FixedClock{Time: toolClock()}, runner, v2Authority, config)
		assertApplicationError(t, err, application.CategoryPolicy, "invalid_configuration")
	})
	t.Run("exec-only catalog missing files", func(t *testing.T) {
		execOnly, err := tools.NewCatalog(execOnlySpecs())
		if err != nil {
			t.Fatal(err)
		}
		config := toolConfig(execOnly, nil, commands, nil)
		_, err = application.NewService(store, ids, testkit.FixedClock{Time: toolClock()}, runner, v2Authority, config)
		assertApplicationError(t, err, application.CategoryPolicy, "invalid_configuration")
	})
	t.Run("unknown policy mode", func(t *testing.T) {
		config := application.DefaultConfig()
		config.PolicyMode = "yolo"
		_, err := application.NewService(store, ids, testkit.FixedClock{Time: toolClock()}, runner, v2Authority, config)
		assertApplicationError(t, err, application.CategoryValidation, "invalid_configuration")
	})
	t.Run("allow writes is legal", func(t *testing.T) {
		config := toolConfig(catalog, files, commands, nil)
		config.PolicyMode = policy.ModeAllowWrites
		service, err := application.NewService(store, ids, testkit.FixedClock{Time: toolClock()}, runner, v2Authority, config)
		if err != nil || service == nil {
			t.Fatalf("NewService(ModeAllowWrites) = (%v, %v)", service, err)
		}
	})
	t.Run("default mode is ModeDefault", func(t *testing.T) {
		config := application.DefaultConfig()
		if config.PolicyMode != policy.ModeDefault || config.MaxSteps != 8 || config.MaxToolCallsPerStep != 8 {
			t.Fatalf("DefaultConfig() = %#v", config)
		}
	})
}

func TestTwoStepReadFileSuccess(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello from fixture"))
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: "reading"},
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: "done"},
			{Type: engine.StreamEventCompleted},
		},
	)
	service, _ := newToolService(t, model, fs, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-two-step", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatalf("RunTurn() = %v", err)
	}
	if result.ItemID != "item-1" || result.Text != "done" || result.Status != domain.TurnStatusCompleted || !result.TerminalCommitted {
		t.Fatalf("result = %#v", result)
	}
}

func TestSecondRunTurnSeesPriorTurnMessages(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: "first-answer"},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: "second-answer"},
			{Type: engine.StreamEventCompleted},
		},
	)
	service, _ := newToolService(t, model, fs, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-turn-1", Input: "hello", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-turn-2", Input: "again", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatal(err)
	}
	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	first := calls[0].Messages
	if len(first) != 1 || first[0].Role != domain.PromptRoleUser || first[0].Text != "hello" {
		t.Fatalf("first turn messages = %#v", first)
	}
	second := calls[1].Messages
	if len(second) != 3 {
		t.Fatalf("second turn messages = %#v, want user/assistant/user", second)
	}
	if second[0].Role != domain.PromptRoleUser || second[0].Text != "hello" {
		t.Fatalf("history user = %#v", second[0])
	}
	if second[1].Role != domain.PromptRoleAssistant || second[1].Text != "first-answer" {
		t.Fatalf("history assistant = %#v", second[1])
	}
	if second[2].Role != domain.PromptRoleUser || second[2].Text != "again" {
		t.Fatalf("current user = %#v", second[2])
	}
}

func TestTwoStepReadFileForwardsToolResult(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello from fixture"))
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: "reading"},
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: "done"},
			{Type: engine.StreamEventCompleted},
		},
	)
	service, _ := newToolService(t, model, fs, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-two-step-tool", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("streams = %d, want 2", len(calls))
	}
	if calls[0].Messages == nil || calls[0].Tools == nil || len(calls[0].Messages) != 1 || calls[0].Messages[0].Role != domain.PromptRoleUser {
		t.Fatalf("step1 request = %#v", calls[0])
	}
	if len(calls[1].Messages) < 3 || calls[1].Messages[1].Role != domain.PromptRoleAssistant || calls[1].Messages[2].Role != domain.PromptRoleTool {
		t.Fatalf("step2 messages = %#v", calls[1].Messages)
	}
	if calls[1].Messages[2].Text != "hello from fixture" || calls[1].Messages[2].ToolCallID != "call-read" {
		t.Fatalf("tool message = %#v", calls[1].Messages[2])
	}
	if calls[1].Messages[0].Role != domain.PromptRoleUser {
		t.Fatalf("step2 stream must start with user: %#v", calls[1].Messages)
	}
	suffix := lastModelRequestMessages(result.Records)
	if len(suffix) == 0 || suffix[0].Role != domain.PromptRoleAssistant {
		t.Fatalf("step2 logged envelope = %#v, want suffix starting with assistant", suffix)
	}
	for _, message := range suffix {
		if message.Role == domain.PromptRoleUser {
			t.Fatalf("step2 logged envelope included user: %#v", suffix)
		}
	}

	second, secondErr := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-two-step-tool", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if secondErr != nil || second.Text != "done" || len(model.Calls()) != 2 {
		t.Fatalf("second invocation streams again: result=%#v err=%v calls=%d", second, secondErr, len(model.Calls()))
	}
}

func TestOversizedToolBatchFailsWithoutStarting(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	counter := &countingFS{FileSystem: fs}
	calls := make([]engine.StreamEvent, 0, 10)
	for i := 0; i < 9; i++ {
		calls = append(calls, engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{
			ID: fmt.Sprintf("call-%d", i+1), Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`,
		}})
	}
	calls = append(calls, engine.StreamEvent{Type: engine.StreamEventCompleted})
	model := newSequenceModel(calls)
	service, _ := newToolService(t, model, counter, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-oversize", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	assertRunTurnError(t, runErr, application.CategoryModel, string(engine.CodeInvalidStream), true)
	if result.Status != domain.TurnStatusFailed || counter.Reads() != 0 || counter.Resolves() != 0 {
		t.Fatalf("result=%#v reads=%d resolves=%d", result, counter.Reads(), counter.Resolves())
	}
	if started := countEventType(result.Records, domain.EventToolCallStarted); started != 0 {
		t.Fatalf("tool.call.started = %d, want 0", started)
	}
}

func TestMaxStepsFailsAfterDurableTools(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("ok"))
	model := newSequenceModel([]engine.StreamEvent{
		{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
		{Type: engine.StreamEventCompleted},
	})
	config := application.DefaultConfig()
	config.MaxSteps = 1
	service, _ := newToolService(t, model, fs, nil, nil, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-step-limit", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	assertRunTurnError(t, runErr, application.CategoryModel, application.CodeStepLimit, true)
	if result.Status != domain.TurnStatusFailed || len(model.Calls()) != 1 {
		t.Fatalf("result=%#v calls=%d", result, len(model.Calls()))
	}
	if countEventType(result.Records, domain.EventToolCallCompleted) != 1 {
		t.Fatalf("missing durable tool: %v", turnEventTypes(result.Records))
	}
}

func TestEnvelopeLimitFailsWithoutStream(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("tiny.txt", []byte("x"))
	large := strings.Repeat("a", 2<<20)
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: large},
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "c1", Name: tools.NameReadFile, Arguments: `{"path":"tiny.txt"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: large},
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "c2", Name: tools.NameReadFile, Arguments: `{"path":"tiny.txt"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: "should-not-stream"},
			{Type: engine.StreamEventCompleted},
		},
	)
	config := application.DefaultConfig()
	config.MaxAssistantBytes = 2 << 20
	config.MaxSteps = 3
	service, _ := newToolService(t, model, fs, nil, nil, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-envelope", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	assertRunTurnError(t, runErr, application.CategoryModel, application.CodeEnvelopeLimit, true)
	if len(model.Calls()) != 2 {
		t.Fatalf("streams = %d, want 2", len(model.Calls()))
	}
	if result.Status != domain.TurnStatusFailed {
		t.Fatalf("result = %#v", result)
	}
}

func TestLoggedEnvelopePersistsAtBudget(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	payload := strings.Repeat("b", application.MaxToolResultBytes)
	for i := 0; i < 8; i++ {
		fs.AddFile(fmt.Sprintf("blob-%d.txt", i), []byte(payload))
	}
	assistant := strings.Repeat("c", application.DefaultMaxAssistantBytes-64)
	var first []engine.StreamEvent
	first = append(first, engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: assistant})
	for i := 0; i < 8; i++ {
		first = append(first, engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{
			ID: fmt.Sprintf("blob-%d", i), Name: tools.NameReadFile, Arguments: fmt.Sprintf(`{"path":"blob-%d.txt"}`, i),
		}})
	}
	first = append(first, engine.StreamEvent{Type: engine.StreamEventCompleted})
	model := newSequenceModel(first, []engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "ok"}, {Type: engine.StreamEventCompleted}})
	service, store := newToolService(t, model, fs, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-budget", Input: "inspect", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatal(err)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var last domain.ModelRequestRecorded
	found := false
	for _, record := range records {
		if event, ok := record.Event.(domain.ModelRequestRecorded); ok {
			last = event
			found = true
		}
	}
	if !found || len(last.Messages) == 0 {
		t.Fatal("missing last model.request.recorded")
	}
	encoded, err := jsonMarshalProjection(last.Messages, last.Tools)
	if err != nil {
		t.Fatal(err)
	}
	budget := application.LoggedEnvelopeBudget(application.DefaultMaxAssistantBytes, application.DefaultMaxToolCallsPerStep)
	if len(encoded) < budget/2 || len(encoded) > 8<<20 {
		t.Fatalf("logged envelope = %d, budget = %d", len(encoded), budget)
	}
}

func TestCancelDuringExecuteUsesInterruptToolTurn(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("secret-should-not-leak"))
	entered := make(chan struct{})
	blocker := &blockingFS{FileSystem: fs, entered: entered}
	model := newSequenceModel([]engine.StreamEvent{
		{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
		{Type: engine.StreamEventCompleted},
	})
	service, _ := newToolService(t, model, blocker, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result application.RunTurnResult
	var runErr error
	go func() {
		defer close(done)
		result, runErr = service.RunTurn(ctx, application.RunTurnRequest{
			SessionID: created.SessionID, RequestID: "request-cancel-exec", Input: "inspect", Sink: &testkit.RecordingSink{},
		})
	}()
	select {
	case <-entered:
	case <-time.After(testRendezvousTimeout):
		fatalStalled(t, "execute did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(testRendezvousTimeout):
		fatalStalled(t, "RunTurn did not return")
	}
	assertRunTurnError(t, runErr, application.CategoryCanceled, domain.InterruptionCallerCanceled, true)
	types := turnEventTypes(result.Records)
	if !containsType(types, domain.EventToolCallInterrupted) || !containsType(types, domain.EventTurnInterrupted) {
		t.Fatalf("records = %v", types)
	}
	if containsType(types, domain.EventAssistantMessageInterrupted) {
		t.Fatalf("used assistant interrupt while tool active: %v", types)
	}
	if result.Status != domain.TurnStatusInterrupted {
		t.Fatalf("result = %#v", result)
	}
}

func TestToolDenialsContinueTurnWithFrozenText(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		mode     policy.Mode
		approver tools.Approver
		wantText string
		wantCode string
		path     string
	}{
		{name: "policy deny write", args: `{"path":"out.txt","content":"x"}`, mode: policy.ModeReadOnly, wantText: application.ToolTextPolicyDenied, wantCode: application.CodePolicyDenied, path: tools.NameWriteFile},
		{name: "approval deny", args: `{"path":"out.txt","content":"x"}`, mode: policy.ModeDefault, approver: testkit.NewScriptedApprover(testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: false}}), wantText: application.ToolTextApprovalDenied, wantCode: application.CodeApprovalDenied, path: tools.NameWriteFile},
		{name: "nil approver", args: `{"path":"out.txt","content":"x"}`, mode: policy.ModeDefault, wantText: application.ToolTextApprovalDenied, wantCode: application.CodeApprovalDenied, path: tools.NameWriteFile},
		{name: "lexical escape", args: `{"path":"../etc/passwd"}`, mode: policy.ModeDefault, wantText: application.ToolTextScopeDenied, wantCode: application.CodeScopeDenied, path: tools.NameReadFile},
		{name: "invalid args", args: `{"path":""}`, mode: policy.ModeDefault, wantText: application.ToolTextInvalidArgs, wantCode: application.CodeInvalidArgs, path: tools.NameReadFile},
		{name: "unknown tool", args: `{}`, mode: policy.ModeDefault, wantText: application.ToolTextUnknownTool, wantCode: application.CodeUnknownTool, path: "not_a_tool"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fs := testkit.NewMemFS("/workspace")
			counter := &countingFS{FileSystem: fs}
			model := newSequenceModel(
				[]engine.StreamEvent{
					{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: test.path, Arguments: test.args}},
					{Type: engine.StreamEventCompleted},
				},
				[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
			)
			config := application.DefaultConfig()
			config.PolicyMode = test.mode
			service, _ := newToolService(t, model, counter, nil, test.approver, config)
			created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
				SessionID: created.SessionID, RequestID: domain.RunTurnRequestID("request-" + test.name), Input: "inspect", Sink: &testkit.RecordingSink{},
			})
			if err != nil || result.Text != "continued" {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if len(model.Calls()) != 2 {
				t.Fatalf("streams = %d", len(model.Calls()))
			}
			toolMsg := lastToolMessage(model.Calls()[1].Messages)
			if toolMsg.Text != test.wantText {
				t.Fatalf("tool text = %q, want %q", toolMsg.Text, test.wantText)
			}
			if test.wantCode == application.CodeScopeDenied && test.name == "lexical escape" {
				if counter.Resolves() != 0 {
					t.Fatalf("lexical escape called Resolve: %d", counter.Resolves())
				}
				if countEventType(result.Records, domain.EventPolicyDecisionRecorded) != 0 {
					t.Fatalf("lexical escape recorded Decide: %v", turnEventTypes(result.Records))
				}
			}
			if test.wantCode != application.CodeScopeDenied && test.wantCode != application.CodeInvalidArgs && test.wantCode != application.CodeUnknownTool {
				if counter.Writes() != 0 {
					t.Fatalf("denied write still executed: %d", counter.Writes())
				}
			}
			if failed := lastToolFailed(result.Records); failed.Code != test.wantCode || failed.Message != test.wantText {
				t.Fatalf("failed = %#v", failed)
			}
		})
	}
}

func TestResolveEscapeDecidesThenScopeDenied(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddSymlink("escape", "/etc/passwd")
	counter := &countingFS{FileSystem: fs}
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameReadFile, Arguments: `{"path":"escape"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
	)
	service, _ := newToolService(t, model, counter, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-resolve-escape", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if err != nil || result.Text != "continued" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if counter.Resolves() == 0 || counter.Reads() != 0 {
		t.Fatalf("resolves=%d reads=%d", counter.Resolves(), counter.Reads())
	}
	if lastToolMessage(model.Calls()[1].Messages).Text != application.ToolTextScopeDenied {
		t.Fatalf("tool text = %#v", model.Calls()[1].Messages)
	}
	if countEventType(result.Records, domain.EventPolicyDecisionRecorded) != 1 {
		t.Fatalf("expected Decide after Resolve: %v", turnEventTypes(result.Records))
	}
}

func TestApprovalTimeoutContinuesTurn(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	approver := testkit.NewScriptedApprover(testkit.ScriptedApproval{Release: make(chan struct{})})
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameWriteFile, Arguments: `{"path":"out.txt","content":"x"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
	)
	config := application.DefaultConfig()
	config.ApprovalTimeout = 20 * time.Millisecond
	service, _ := newToolService(t, model, fs, nil, approver, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-approval-timeout", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if err != nil || result.Text != "continued" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if lastToolMessage(model.Calls()[1].Messages).Text != application.ToolTextApprovalTimeout {
		t.Fatalf("tool text = %#v", model.Calls()[1].Messages)
	}
}

func TestModeAllowWritesExecutesWrite(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	counter := &countingFS{FileSystem: fs}
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameWriteFile, Arguments: `{"path":"out.txt","content":"hi"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "wrote"}, {Type: engine.StreamEventCompleted}},
	)
	config := application.DefaultConfig()
	config.PolicyMode = policy.ModeAllowWrites
	service, _ := newToolService(t, model, counter, nil, nil, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-allow-writes", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if err != nil || result.Text != "wrote" || counter.Writes() != 1 {
		t.Fatalf("result=%#v err=%v writes=%d", result, err, counter.Writes())
	}
	if lastToolMessage(model.Calls()[1].Messages).Text != "wrote 2 bytes" {
		t.Fatalf("tool text = %#v", model.Calls()[1].Messages)
	}
}

func TestTableA2UnknownStartedDoesNotExecute(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	counter := &countingFS{FileSystem: fs}
	base := newTurnMemoryStore(t)
	store := &eventTypeFaultStore{EventStore: base, target: domain.EventToolCallStarted, mode: faultExhaust}
	model := newSequenceModel([]engine.StreamEvent{
		{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
		{Type: engine.StreamEventCompleted},
	})
	service := newToolServiceWithStore(t, store, model, counter, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-unknown-started", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if !isUnknown(runErr) {
		t.Fatalf("err = %v", runErr)
	}
	if counter.Reads() != 0 || counter.Resolves() != 0 || len(model.Calls()) != 1 {
		t.Fatalf("reads=%d resolves=%d streams=%d", counter.Reads(), counter.Resolves(), len(model.Calls()))
	}
	retryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, retryErr := service.RunTurn(retryCtx, application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-unknown-started", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	assertApplicationError(t, retryErr, application.CategoryConflict, application.CodeReconciliationRequired)
	if retryCtx.Err() != nil {
		t.Fatal("same-request retry waited on a closed-over unknown lease")
	}
	if len(model.Calls()) != 1 {
		t.Fatalf("retry streamed: %d", len(model.Calls()))
	}
}

func TestTableA2UnknownToolTerminalDoesNotReexecute(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	counter := &countingFS{FileSystem: fs}
	base := newTurnMemoryStore(t)
	store := &eventTypeFaultStore{EventStore: base, target: domain.EventToolCallCompleted, mode: faultExhaust}
	model := newSequenceModel([]engine.StreamEvent{
		{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
		{Type: engine.StreamEventCompleted},
	})
	service := newToolServiceWithStore(t, store, model, counter, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-unknown-terminal", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if !isUnknown(runErr) {
		t.Fatalf("err = %v", runErr)
	}
	if counter.Reads() != 1 {
		t.Fatalf("reads = %d, want 1", counter.Reads())
	}
	if len(model.Calls()) != 1 {
		t.Fatalf("re-streamed after unknown terminal: %d", len(model.Calls()))
	}
}

func TestTableA2UnknownStepStartDoesNotStream(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	base := newTurnMemoryStore(t)
	store := &eventTypeFaultStore{EventStore: base, target: domain.EventAssistantMessageStarted, mode: faultExhaust}
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "nope"}, {Type: engine.StreamEventCompleted}},
	)
	service := newToolServiceWithStore(t, store, model, fs, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-unknown-step", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if !isUnknown(runErr) {
		t.Fatalf("err = %v", runErr)
	}
	if len(model.Calls()) != 1 {
		t.Fatalf("streamed while step-start unknown: %d", len(model.Calls()))
	}
}

func TestStepTwoStreamCancelInterruptsLiveAssistant(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	entered := make(chan struct{})
	model := &stepTwoControlModel{entered: entered, second: []engine.StreamEvent{}}
	model.waitSecond = true
	service, _ := newToolService(t, model, fs, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result application.RunTurnResult
	var runErr error
	go func() {
		defer close(done)
		result, runErr = service.RunTurn(ctx, application.RunTurnRequest{
			SessionID: created.SessionID, RequestID: "request-step2-cancel", Input: "inspect", Sink: &testkit.RecordingSink{},
		})
	}()
	select {
	case <-entered:
	case <-time.After(testRendezvousTimeout):
		fatalStalled(t, "step 2 stream did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(testRendezvousTimeout):
		fatalStalled(t, "RunTurn did not return")
	}
	assertRunTurnError(t, runErr, application.CategoryCanceled, "canceled", true)
	if result.ItemID != "item-1" {
		t.Fatalf("ItemID = %s, want admission item-1", result.ItemID)
	}
	stepItem := lastAssistantStarted(result.Records)
	interrupted := lastAssistantInterrupted(result.Records)
	if stepItem == "" || stepItem == result.ItemID || interrupted.ItemID != stepItem {
		t.Fatalf("step2 item=%s admission=%s interrupted=%#v", stepItem, result.ItemID, interrupted)
	}
}

func TestStepTwoInvalidStreamFailsLiveAssistant(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "c2", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
			{Type: engine.StreamEventTextDelta, Text: "after-tool"},
			{Type: engine.StreamEventCompleted},
		},
	)
	service, _ := newToolService(t, model, fs, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-step2-invalid", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	assertRunTurnError(t, runErr, application.CategoryModel, string(engine.CodeInvalidStream), true)
	if result.ItemID != "item-1" {
		t.Fatalf("ItemID = %s, want admission item-1", result.ItemID)
	}
	stepItem := lastAssistantStarted(result.Records)
	failed := lastAssistantFailed(result.Records)
	if stepItem == "" || stepItem == result.ItemID || failed.ItemID != stepItem || failed.Code != string(engine.CodeInvalidStream) {
		t.Fatalf("step2 item=%s admission=%s failed=%#v", stepItem, result.ItemID, failed)
	}
}

func TestExecArgvSymlinkOutNeverRuns(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddSymlink("bin/tool", "/usr/bin/tool")
	counter := &countingFS{FileSystem: fs}
	runner := testkit.NewScriptedRunner()
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-exec", Name: tools.NameExec, Arguments: `{"argv":["bin/tool"]}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
	)
	service, _ := newToolService(t, model, counter, runner, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-exec-symlink", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if err != nil || result.Text != "continued" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if counter.Resolves() == 0 {
		t.Fatal("exec argv path never called Resolve")
	}
	if len(runner.Calls()) != 0 {
		t.Fatalf("CommandRunner.Run called: %#v", runner.Calls())
	}
	if lastToolMessage(model.Calls()[1].Messages).Text != application.ToolTextScopeDenied {
		t.Fatalf("tool text = %#v", model.Calls()[1].Messages)
	}
	if failed := lastToolFailed(result.Records); failed.Code != application.CodeScopeDenied {
		t.Fatalf("failed = %#v", failed)
	}
	if countEventType(result.Records, domain.EventPolicyDecisionRecorded) != 1 {
		t.Fatalf("expected Decide after Resolve-out: %v", turnEventTypes(result.Records))
	}
}

func TestEmptyCatalogKeepsNilMessagesAndTools(t *testing.T) {
	model := &repeatingSuccessModel{text: "plain"}
	service := newTurnService(t, newTurnMemoryStore(t), testkit.NewSequenceIDs(), model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-empty-catalog", Input: "inspect", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatal(err)
	}
	calls := model.Calls()
	if len(calls) != 1 || calls[0].Messages != nil || calls[0].Tools != nil {
		t.Fatalf("empty catalog request = %#v", calls)
	}
}

const faultExhaust = "exhaust"

type eventTypeFaultStore struct {
	application.EventStore
	target string
	mode   string
	hits   int
}

func (store *eventTypeFaultStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	if store.matches(request) && store.hits == 0 {
		store.hits++
		unknown, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, SessionID: request.SessionID, MayHaveCommitted: true})
		if err != nil {
			return application.CommitReceipt{}, err
		}
		return application.CommitReceipt{}, unknown
	}
	return store.EventStore.Append(ctx, request)
}

func (store *eventTypeFaultStore) ResolveAppend(context.Context, application.ResolveAppendRequest) (application.AppendResolution, error) {
	if store.hits > 0 && store.mode == faultExhaust {
		unknown, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, MayHaveCommitted: true})
		if err != nil {
			return application.AppendResolution{}, err
		}
		return application.AppendResolution{}, unknown
	}
	return application.AppendResolution{Kind: application.AppendResolutionNotFound}, nil
}

func (store *eventTypeFaultStore) matches(request application.AppendRequest) bool {
	if len(request.Events) == 0 || request.Events[0].Event.EventType() != store.target {
		return false
	}
	if store.target == domain.EventAssistantMessageStarted && request.Admission != nil {
		return false
	}
	return true
}

type sequenceModel struct {
	mu      sync.Mutex
	scripts [][]engine.StreamEvent
	calls   []engine.ModelRequest
}

func newSequenceModel(scripts ...[]engine.StreamEvent) *sequenceModel {
	return &sequenceModel{scripts: scripts}
}

func (model *sequenceModel) Stream(_ context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	cloned := request
	cloned.Messages = make([]domain.ModelPromptMessage, len(request.Messages))
	for i, message := range request.Messages {
		cloned.Messages[i] = message
		if message.ToolCalls != nil {
			cloned.Messages[i].ToolCalls = append([]domain.ToolCallOffer(nil), message.ToolCalls...)
		}
	}
	cloned.Tools = make([]domain.ToolSchema, len(request.Tools))
	for i, schema := range request.Tools {
		cloned.Tools[i] = schema
		if schema.InputSchema != nil {
			cloned.Tools[i].InputSchema = append([]byte(nil), schema.InputSchema...)
		}
	}
	model.calls = append(model.calls, cloned)
	if len(model.calls) > len(model.scripts) {
		return nil, errors.New("script exhausted")
	}
	return &turnSuccessStream{events: model.scripts[len(model.calls)-1]}, nil
}

func (model *sequenceModel) Calls() []engine.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]engine.ModelRequest(nil), model.calls...)
}

type stepTwoControlModel struct {
	mu         sync.Mutex
	calls      []engine.ModelRequest
	entered    chan struct{}
	waitSecond bool
	second     []engine.StreamEvent
}

func (model *stepTwoControlModel) Stream(ctx context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	model.mu.Lock()
	model.calls = append(model.calls, request)
	n := len(model.calls)
	model.mu.Unlock()
	if n == 1 {
		return &turnSuccessStream{events: []engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}},
			{Type: engine.StreamEventCompleted},
		}}, nil
	}
	if model.waitSecond {
		return &waitCancelStream{entered: model.entered}, nil
	}
	return &turnSuccessStream{events: model.second}, nil
}

type waitCancelStream struct {
	entered chan struct{}
	once    sync.Once
}

func (stream *waitCancelStream) Next(ctx context.Context) (engine.StreamEvent, error) {
	stream.once.Do(func() {
		if stream.entered != nil {
			close(stream.entered)
		}
	})
	<-ctx.Done()
	return engine.StreamEvent{}, ctx.Err()
}

func (*waitCancelStream) Close() error { return nil }

type countingFS struct {
	tools.FileSystem
	mu       sync.Mutex
	resolves int
	reads    int
	writes   int
	lists    int
}

func (fs *countingFS) Resolve(ctx context.Context, workspace, requested string) (string, error) {
	fs.mu.Lock()
	fs.resolves++
	fs.mu.Unlock()
	return fs.FileSystem.Resolve(ctx, workspace, requested)
}
func (fs *countingFS) Read(ctx context.Context, abs string, limit int) ([]byte, bool, error) {
	fs.mu.Lock()
	fs.reads++
	fs.mu.Unlock()
	return fs.FileSystem.Read(ctx, abs, limit)
}
func (fs *countingFS) Write(ctx context.Context, abs string, data []byte) error {
	fs.mu.Lock()
	fs.writes++
	fs.mu.Unlock()
	return fs.FileSystem.Write(ctx, abs, data)
}
func (fs *countingFS) List(ctx context.Context, abs string, depth, limit int) ([]string, bool, error) {
	fs.mu.Lock()
	fs.lists++
	fs.mu.Unlock()
	return fs.FileSystem.List(ctx, abs, depth, limit)
}
func (fs *countingFS) Resolves() int { fs.mu.Lock(); defer fs.mu.Unlock(); return fs.resolves }
func (fs *countingFS) Reads() int    { fs.mu.Lock(); defer fs.mu.Unlock(); return fs.reads }
func (fs *countingFS) Writes() int   { fs.mu.Lock(); defer fs.mu.Unlock(); return fs.writes }

type blockingFS struct {
	tools.FileSystem
	entered chan struct{}
	once    sync.Once
}

func (fs *blockingFS) Read(ctx context.Context, abs string, limit int) ([]byte, bool, error) {
	fs.once.Do(func() { close(fs.entered) })
	<-ctx.Done()
	return nil, false, ctx.Err()
}

func scriptedToolIdentity() engine.RequestIdentity {
	return engine.RequestIdentity{
		AdapterFamily: "scripted",
		ModelID:       "scripted",
		EndpointID:    "test",
		Profile: engine.CapabilityProfile{
			NativeTools:      engine.CapabilitySupported,
			Images:           engine.CapabilityUnsupported,
			StructuredOutput: engine.CapabilityUnsupported,
			ReasoningFields:  engine.CapabilityUnsupported,
			PromptCache:      engine.CapabilityUnsupported,
		},
	}
}

func toolConfig(catalog *tools.Catalog, files tools.FileSystem, commands tools.CommandRunner, approver tools.Approver) application.Config {
	config := application.DefaultConfig()
	identity := scriptedToolIdentity()
	config.RequestIdentity = &identity
	config.Catalog = catalog
	config.Files = files
	config.Commands = commands
	config.Approver = approver
	return config
}

func newToolService(t *testing.T, model engine.Model, files tools.FileSystem, commands tools.CommandRunner, approver tools.Approver, base application.Config) (*application.Service, *memory.EventStore) {
	t.Helper()
	store := newTurnMemoryStore(t)
	return newToolServiceWithStore(t, store, model, files, commands, approver, base), store
}

func newToolServiceWithStore(t *testing.T, store application.EventStore, model engine.Model, files tools.FileSystem, commands tools.CommandRunner, approver tools.Approver, base application.Config) *application.Service {
	t.Helper()
	catalog, err := tools.NewCatalog(tools.DefaultWorkspaceSpecs())
	if err != nil {
		t.Fatal(err)
	}
	if commands == nil {
		commands = testkit.NewScriptedRunner()
	}
	config := toolConfig(catalog, files, commands, approver)
	config.MaxSteps = base.MaxSteps
	config.MaxToolCallsPerStep = base.MaxToolCallsPerStep
	config.MaxAssistantBytes = base.MaxAssistantBytes
	config.ApprovalTimeout = base.ApprovalTimeout
	config.PolicyMode = base.PolicyMode
	if config.MaxSteps == 0 {
		config.MaxSteps = application.DefaultMaxSteps
	}
	if config.MaxToolCallsPerStep == 0 {
		config.MaxToolCallsPerStep = application.DefaultMaxToolCallsPerStep
	}
	if config.MaxAssistantBytes == 0 {
		config.MaxAssistantBytes = application.DefaultMaxAssistantBytes
	}
	return newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
}

func toolClock() time.Time {
	return time.Date(2026, 8, 16, 6, 7, 8, 9, time.UTC)
}

func countEventType(records []domain.RecordedEvent, eventType string) int {
	count := 0
	for _, record := range records {
		if record.Event.EventType() == eventType {
			count++
		}
	}
	return count
}

func containsType(types []string, eventType string) bool {
	for _, got := range types {
		if got == eventType {
			return true
		}
	}
	return false
}

func lastToolMessage(messages []domain.ModelPromptMessage) domain.ModelPromptMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == domain.PromptRoleTool {
			return messages[i]
		}
	}
	return domain.ModelPromptMessage{}
}

func lastToolFailed(records []domain.RecordedEvent) domain.ToolCallFailed {
	for i := len(records) - 1; i >= 0; i-- {
		if failed, ok := records[i].Event.(domain.ToolCallFailed); ok {
			return failed
		}
	}
	return domain.ToolCallFailed{}
}

func jsonMarshalProjection(messages []domain.ModelPromptMessage, toolSchemas []domain.ToolSchema) ([]byte, error) {
	return json.Marshal(struct {
		Messages []domain.ModelPromptMessage `json:"messages"`
		Tools    []domain.ToolSchema         `json:"tools"`
	}{Messages: messages, Tools: toolSchemas})
}

func lastModelRequestMessages(records []domain.RecordedEvent) []domain.ModelPromptMessage {
	var last []domain.ModelPromptMessage
	for _, record := range records {
		if event, ok := record.Event.(domain.ModelRequestRecorded); ok {
			last = event.Messages
		}
	}
	return last
}

func lastAssistantStarted(records []domain.RecordedEvent) domain.ItemID {
	var id domain.ItemID
	for _, record := range records {
		if event, ok := record.Event.(domain.AssistantMessageStarted); ok {
			id = event.ItemID
		}
	}
	return id
}

func lastAssistantInterrupted(records []domain.RecordedEvent) domain.AssistantMessageInterrupted {
	for i := len(records) - 1; i >= 0; i-- {
		if event, ok := records[i].Event.(domain.AssistantMessageInterrupted); ok {
			return event
		}
	}
	return domain.AssistantMessageInterrupted{}
}

func lastAssistantFailed(records []domain.RecordedEvent) domain.AssistantMessageFailed {
	for i := len(records) - 1; i >= 0; i-- {
		if event, ok := records[i].Event.(domain.AssistantMessageFailed); ok {
			return event
		}
	}
	return domain.AssistantMessageFailed{}
}

func execOnlySpecs() []domain.ToolSpec {
	for _, spec := range tools.DefaultWorkspaceSpecs() {
		if spec.Name == tools.NameExec {
			return []domain.ToolSpec{spec}
		}
	}
	return nil
}
