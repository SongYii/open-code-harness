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

type appPolicyStrategy struct {
	mu       sync.Mutex
	decision policy.Decision
	err      error
	calls    int
	decide   func(context.Context, policy.Input) (policy.Decision, error)
}

func (strategy *appPolicyStrategy) Decide(ctx context.Context, input policy.Input) (policy.Decision, error) {
	strategy.mu.Lock()
	strategy.calls++
	callback := strategy.decide
	decision := strategy.decision
	err := strategy.err
	strategy.mu.Unlock()
	if callback != nil {
		return callback(ctx, input)
	}
	return decision, err
}

func (strategy *appPolicyStrategy) Calls() int {
	strategy.mu.Lock()
	defer strategy.mu.Unlock()
	return strategy.calls
}

func testToolPolicyIdentity() *domain.ToolPolicyIdentity {
	return &domain.ToolPolicyIdentity{ID: "test_policy", Version: "1.0.0", ConfigDigest: strings.Repeat("a", 64)}
}

func customToolPolicyConfig(strategy policy.Engine) application.Config {
	config := application.DefaultConfig()
	config.PolicyStrategy = strategy
	config.PolicyIdentity = testToolPolicyIdentity()
	return config
}

func TestBuiltinToolPolicyDecisionRetainsLegacyAttributionOmission(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	model := newSequenceModel(
		[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
	)
	service, _ := newToolService(t, model, fs, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "builtin-legacy-attribution", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Text != "continued" {
		t.Fatalf("RunTurn() = (%#v, %v)", result, err)
	}
	decision := onlyPolicyDecision(t, result.Records)
	if decision.Policy != nil {
		t.Fatalf("builtin decision gained custom identity: %#v", decision.Policy)
	}
	for _, record := range result.Records {
		if _, ok := record.Event.(domain.PolicyDecisionRecorded); !ok {
			continue
		}
		encoded, err := domain.MarshalRecordedEvent(record)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"policy"`) {
			t.Fatalf("builtin durable bytes gained policy attribution: %s", encoded)
		}
	}
}

func TestCustomToolPolicyDenyIsAttributedAndDoesNotExecute(t *testing.T) {
	strategy := &appPolicyStrategy{decision: policy.Decision{Effect: policy.EffectDeny, RuleID: "custom.read_deny", Reason: "configured_deny"}}
	config := customToolPolicyConfig(strategy)
	identity := config.PolicyIdentity
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	counter := &countingFS{FileSystem: fs}
	model := newSequenceModel(
		[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
	)
	service, _ := newToolService(t, model, counter, nil, nil, config)
	identity.ID = "mutated_after_startup"
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "custom-deny", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Text != "continued" {
		t.Fatalf("RunTurn() = (%#v, %v)", result, err)
	}
	decision := onlyPolicyDecision(t, result.Records)
	if decision.Effect != domain.PolicyEffectDeny || decision.RuleID != "custom.read_deny" || decision.Policy == identity || decision.Policy.ID != "test_policy" || counter.Reads() != 0 {
		t.Fatalf("decision=%#v reads=%d", decision, counter.Reads())
	}
}

func TestCustomToolPolicyAllowAndApprovalPaths(t *testing.T) {
	t.Run("allow read", func(t *testing.T) {
		strategy := &appPolicyStrategy{decision: policy.Decision{Effect: policy.EffectAllow, RuleID: "custom.read_allow", Reason: "configured_allow"}}
		fs := testkit.NewMemFS("/workspace")
		fs.AddFile("README.md", []byte("hello"))
		counter := &countingFS{FileSystem: fs}
		model := newSequenceModel(
			[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}}, {Type: engine.StreamEventCompleted}},
			[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "done"}, {Type: engine.StreamEventCompleted}},
		)
		service, _ := newToolService(t, model, counter, nil, nil, customToolPolicyConfig(strategy))
		created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "custom-allow", Input: "inspect", Sink: &testkit.RecordingSink{}})
		if err != nil || result.Text != "done" || counter.Reads() != 1 {
			t.Fatalf("RunTurn() = (%#v, %v), reads=%d", result, err, counter.Reads())
		}
		if decision := onlyPolicyDecision(t, result.Records); decision.Effect != domain.PolicyEffectAllow || decision.Policy == nil {
			t.Fatalf("decision = %#v", decision)
		}
	})

	for _, granted := range []bool{false, true} {
		name := "approval denied"
		if granted {
			name = "approval granted"
		}
		t.Run(name, func(t *testing.T) {
			strategy := &appPolicyStrategy{decision: policy.Decision{Effect: policy.EffectRequireApproval, RuleID: "custom.write_approval", Reason: "configured_approval"}}
			counter := &countingFS{FileSystem: testkit.NewMemFS("/workspace")}
			model := newSequenceModel(
				[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameWriteFile, Arguments: `{"path":"out.txt","content":"hello"}`}}, {Type: engine.StreamEventCompleted}},
				[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
			)
			approver := testkit.NewScriptedApprover(testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: granted}})
			service, _ := newToolService(t, model, counter, nil, approver, customToolPolicyConfig(strategy))
			created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: domain.RunTurnRequestID("custom-" + name), Input: "write", Sink: &testkit.RecordingSink{}})
			if err != nil || result.Text != "continued" {
				t.Fatalf("RunTurn() = (%#v, %v)", result, err)
			}
			wantWrites := 0
			if granted {
				wantWrites = 1
			}
			if counter.Writes() != wantWrites || onlyPolicyDecision(t, result.Records).Policy == nil {
				t.Fatalf("writes=%d records=%v", counter.Writes(), turnEventTypes(result.Records))
			}
		})
	}
}

func TestCustomToolPolicyCannotBypassCoreScopeGuard(t *testing.T) {
	strategy := &appPolicyStrategy{decision: policy.Decision{Effect: policy.EffectAllow, RuleID: "custom.allow_all", Reason: "allow_all"}}
	fs := testkit.NewMemFS("/workspace")
	fs.AddSymlink("escape", "/etc/passwd")
	counter := &countingFS{FileSystem: fs}
	model := newSequenceModel(
		[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameReadFile, Arguments: `{"path":"escape"}`}}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
	)
	service, _ := newToolService(t, model, counter, nil, nil, customToolPolicyConfig(strategy))
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "custom-core-guard", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Text != "continued" {
		t.Fatalf("RunTurn() = (%#v, %v)", result, err)
	}
	decision := onlyPolicyDecision(t, result.Records)
	if strategy.Calls() != 0 || counter.Reads() != 0 || decision.RuleID != policy.RuleOutOfWorkspace || decision.Effect != domain.PolicyEffectDeny {
		t.Fatalf("calls=%d reads=%d decision=%#v", strategy.Calls(), counter.Reads(), decision)
	}
}

func TestCustomToolPolicyFailureIsClosedAndSecretFree(t *testing.T) {
	const secret = "POLICY-CALLBACK-SECRET-must-not-leak"
	strategy := &appPolicyStrategy{err: errors.New(secret)}
	traces := &memory.Telemetry{}
	config := customToolPolicyConfig(strategy)
	config.Telemetry = traces
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	counter := &countingFS{FileSystem: fs}
	model := newSequenceModel(
		[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
	)
	service, _ := newToolService(t, model, counter, nil, nil, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	sink := &testkit.RecordingSink{}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "custom-error", Input: "inspect", Sink: sink})
	if err != nil || result.Text != "continued" {
		t.Fatalf("RunTurn() = (%#v, %v)", result, err)
	}
	decision := onlyPolicyDecision(t, result.Records)
	if decision.Effect != domain.PolicyEffectDeny || decision.RuleID != policy.RulePolicyFailed || decision.Reason != policy.ReasonPolicyFailed || decision.Policy == nil || counter.Reads() != 0 {
		t.Fatalf("decision=%#v reads=%d", decision, counter.Reads())
	}
	public, marshalErr := json.Marshal(struct {
		Result application.RunTurnResult
		Sink   []engine.RuntimeEvent
		Traces []memory.TraceRecord
	}{Result: result, Sink: sink.Attempts(), Traces: traces.Records()})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(public), secret) || strings.Contains(fmt.Sprintf("%#v", result), secret) {
		t.Fatalf("callback error leaked: %s", public)
	}
}

func TestCustomToolPolicyInvalidDecisionFailsClosed(t *testing.T) {
	strategy := &appPolicyStrategy{decision: policy.Decision{Effect: policy.EffectAllow}}
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	counter := &countingFS{FileSystem: fs}
	model := newSequenceModel(
		[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "continued"}, {Type: engine.StreamEventCompleted}},
	)
	service, _ := newToolService(t, model, counter, nil, nil, customToolPolicyConfig(strategy))
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "custom-invalid", Input: "inspect", Sink: &testkit.RecordingSink{}})
	if err != nil || result.Text != "continued" {
		t.Fatalf("RunTurn() = (%#v, %v)", result, err)
	}
	decision := onlyPolicyDecision(t, result.Records)
	if decision.Effect != domain.PolicyEffectDeny || decision.RuleID != policy.RulePolicyFailed || decision.Reason != policy.ReasonPolicyFailed || counter.Reads() != 0 {
		t.Fatalf("decision=%#v reads=%d", decision, counter.Reads())
	}
}

func TestCustomToolPolicyCancellationUsesTurnInterruption(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	strategy := &appPolicyStrategy{decide: func(ctx context.Context, _ policy.Input) (policy.Decision, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return policy.Decision{}, ctx.Err()
	}}
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte("hello"))
	model := newSequenceModel([]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}}, {Type: engine.StreamEventCompleted}})
	service, _ := newToolService(t, model, fs, nil, nil, customToolPolicyConfig(strategy))
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
		result, runErr = service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "custom-cancel", Input: "inspect", Sink: &testkit.RecordingSink{}})
	}()
	select {
	case <-entered:
	case <-time.After(testRendezvousTimeout):
		fatalStalled(t, "policy decision did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(testRendezvousTimeout):
		fatalStalled(t, "RunTurn did not return")
	}
	assertRunTurnError(t, runErr, application.CategoryCanceled, domain.InterruptionCallerCanceled, true)
	if countEventType(result.Records, domain.EventPolicyDecisionRecorded) != 0 || !containsType(turnEventTypes(result.Records), domain.EventToolCallInterrupted) || !containsType(turnEventTypes(result.Records), domain.EventTurnInterrupted) {
		t.Fatalf("records = %v", turnEventTypes(result.Records))
	}
}

func TestCustomToolPolicyConcurrentSessionsRemainAttributed(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	strategy := &appPolicyStrategy{decision: policy.Decision{Effect: policy.EffectAllow, RuleID: "custom.concurrent", Reason: "concurrent_allow"}}
	strategy.decide = func(context.Context, policy.Input) (policy.Decision, error) {
		if strategy.Calls() == 2 {
			enteredOnce.Do(func() { close(entered) })
		}
		<-release
		return strategy.decision, nil
	}
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("a.txt", []byte("a"))
	fs.AddFile("b.txt", []byte("b"))
	model := newSequenceModel(
		[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-a", Name: tools.NameReadFile, Arguments: `{"path":"a.txt"}`}}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-b", Name: tools.NameReadFile, Arguments: `{"path":"b.txt"}`}}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "done"}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "done"}, {Type: engine.StreamEventCompleted}},
	)
	service, _ := newToolService(t, model, fs, nil, nil, customToolPolicyConfig(strategy))
	first, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for index, session := range []domain.SessionID{first.SessionID, second.SessionID} {
		go func(index int, session domain.SessionID) {
			result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: session, RequestID: domain.RunTurnRequestID(fmt.Sprintf("custom-concurrent-%d", index)), Input: "inspect", Sink: &testkit.RecordingSink{}})
			outcomes <- outcome{result: result, err: runErr}
		}(index, session)
	}
	select {
	case <-entered:
	case <-time.After(testRendezvousTimeout):
		fatalStalled(t, "both policy decisions did not start")
	}
	close(release)
	for range 2 {
		outcome := <-outcomes
		if outcome.err != nil || outcome.result.Text != "done" {
			t.Fatalf("RunTurn() = (%#v, %v)", outcome.result, outcome.err)
		}
		decision := onlyPolicyDecision(t, outcome.result.Records)
		if decision.Policy == nil || decision.Policy.ID != "test_policy" {
			t.Fatalf("decision = %#v", decision)
		}
		for _, record := range outcome.result.Records {
			if record.SessionID != outcome.result.SessionID {
				t.Fatalf("record session %q crossed into %q", record.SessionID, outcome.result.SessionID)
			}
		}
	}
}

func TestToolPolicyConfigurationRejectsInvalidPairings(t *testing.T) {
	validStrategy := &appPolicyStrategy{decision: policy.Decision{Effect: policy.EffectAllow, RuleID: "custom.allow", Reason: "allow"}}
	validIdentity := testToolPolicyIdentity()
	var typedNil *appPolicyStrategy
	tests := []struct {
		name     string
		strategy policy.Engine
		identity *domain.ToolPolicyIdentity
		mode     policy.Mode
	}{
		{name: "strategy without identity", strategy: validStrategy, mode: policy.ModeDefault},
		{name: "identity without strategy", identity: validIdentity, mode: policy.ModeDefault},
		{name: "typed nil strategy", strategy: typedNil, identity: validIdentity, mode: policy.ModeDefault},
		{name: "read only with custom", strategy: validStrategy, identity: validIdentity, mode: policy.ModeReadOnly},
		{name: "allow writes with custom", strategy: validStrategy, identity: validIdentity, mode: policy.ModeAllowWrites},
		{name: "deny all with custom", strategy: validStrategy, identity: validIdentity, mode: policy.ModeDenyAll},
		{name: "invalid identity", strategy: validStrategy, identity: &domain.ToolPolicyIdentity{ID: "bad/id", Version: "1", ConfigDigest: strings.Repeat("a", 64)}, mode: policy.ModeDefault},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := application.DefaultConfig()
			config.PolicyStrategy = test.strategy
			config.PolicyIdentity = test.identity
			config.PolicyMode = test.mode
			service, err := application.NewService(newTurnMemoryStore(t), testkit.NewSequenceIDs(), testkit.FixedClock{Time: toolClock()}, sessionRunnerForTest(t), v2Authority, config)
			if service != nil {
				t.Fatalf("NewService() service = %#v", service)
			}
			assertApplicationError(t, err, application.CategoryValidation, "invalid_configuration")
		})
	}
}

func onlyPolicyDecision(t *testing.T, records []domain.RecordedEvent) domain.PolicyDecisionRecorded {
	t.Helper()
	var decisions []domain.PolicyDecisionRecorded
	for _, record := range records {
		if decision, ok := record.Event.(domain.PolicyDecisionRecorded); ok {
			decisions = append(decisions, decision)
		}
	}
	if len(decisions) != 1 {
		t.Fatalf("policy decisions = %#v", decisions)
	}
	return decisions[0]
}
