package application_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/telemetry"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestTelemetryTurnTopologyContainsOnlyMetadata(t *testing.T) {
	const canary = "PROMPT CANARY secret=must-not-enter-telemetry"
	store := newTurnMemoryStore(t)
	model, err := testkit.NewScriptedModel(
		engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: canary},
		testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &memory.Telemetry{}
	config := application.DefaultConfig()
	config.Telemetry = recorder
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace/canary-secret"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-telemetry", Input: canary, Sink: &testkit.RecordingSink{},
	})
	if err != nil || result.Status != domain.TurnStatusCompleted {
		t.Fatalf("RunTurn() = (%#v, %v)", result, err)
	}

	records := recorder.Records()
	if strings.Contains(fmt.Sprintf("%#v", records), canary) || strings.Contains(fmt.Sprintf("%#v", records), "canary-secret") {
		t.Fatalf("telemetry contained prompt or path content: %#v", records)
	}
	var turnID uint64
	var childKinds = map[telemetry.Kind]int{}
	for _, record := range records {
		if record.Start.Kind == telemetry.KindTurn {
			turnID = record.ID
			if record.Parent != 0 || record.End.Outcome != telemetry.OutcomeOK {
				t.Fatalf("turn span = %#v, want successful root", record)
			}
		}
	}
	if turnID == 0 {
		t.Fatalf("records = %#v, want turn span", records)
	}
	for _, record := range records {
		if record.Parent == turnID {
			childKinds[record.Start.Kind]++
		}
	}
	if childKinds[telemetry.KindModelRequest] != 1 || childKinds[telemetry.KindStoreAppend] != 2 {
		t.Fatalf("turn child kinds = %#v, want one model request and two logical appends", childKinds)
	}

	beforeReplay := len(records)
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-telemetry", Input: canary, Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatal(err)
	}
	replay := recorder.Records()[beforeReplay:]
	if len(replay) != 1 || replay[0].Start.Kind != telemetry.KindTurn || stringAttribute(replay[0].End.Attributes, telemetry.KeyTurnRole) != "replayed" {
		t.Fatalf("replay telemetry = %#v, want one short replayed Turn", replay)
	}
}

func TestTelemetryToolApprovalTopology(t *testing.T) {
	recorder := &memory.Telemetry{}
	model := newSequenceModel(
		[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-write", Name: tools.NameWriteFile, Arguments: `{"path":"secret-file.txt","content":"TOOL CONTENT CANARY"}`}}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "done"}, {Type: engine.StreamEventCompleted}},
	)
	config := application.DefaultConfig()
	config.Telemetry = recorder
	service, _ := newToolService(t, model, testkit.NewMemFS("/workspace"), nil,
		testkit.NewScriptedApprover(testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: true}}), config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "telemetry-tool", Input: "write it", Sink: &testkit.RecordingSink{}}); err != nil {
		t.Fatal(err)
	}

	records := recorder.Records()
	var turnID uint64
	for _, record := range records {
		if record.Start.Kind == telemetry.KindTurn {
			turnID = record.ID
		}
	}
	want := map[telemetry.Kind]int{telemetry.KindModelRequest: 2, telemetry.KindPolicyDecide: 1, telemetry.KindApprovalWait: 1, telemetry.KindToolExecute: 1}
	got := map[telemetry.Kind]int{}
	for _, record := range records {
		if record.Parent == turnID {
			got[record.Start.Kind]++
		}
	}
	for kind, count := range want {
		if got[kind] != count {
			t.Fatalf("child %s count = %d, want %d; all=%#v", kind, got[kind], count, records)
		}
	}
	if strings.Contains(fmt.Sprintf("%#v", records), "TOOL CONTENT CANARY") || strings.Contains(fmt.Sprintf("%#v", records), "secret-file.txt") {
		t.Fatalf("tool arguments leaked into telemetry: %#v", records)
	}
}

func TestTelemetryFailureUsesStableCodeNotRawError(t *testing.T) {
	const secretError = "provider failed with Authorization: Bearer sk-do-not-export"
	store := newTurnMemoryStore(t)
	model, err := testkit.NewScriptedModel(engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "fail"}, testkit.ScriptedModelConfig{StartupError: fmt.Errorf("%s", secretError)})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &memory.Telemetry{}
	config := application.DefaultConfig()
	config.Telemetry = recorder
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "telemetry-failure", Input: "fail", Sink: &testkit.RecordingSink{}})
	records := recorder.Records()
	if strings.Contains(fmt.Sprintf("%#v", records), secretError) || strings.Contains(fmt.Sprintf("%#v", records), "sk-do-not-export") {
		t.Fatalf("raw error leaked into telemetry: %#v", records)
	}
	var failed bool
	for _, record := range records {
		if record.Start.Kind == telemetry.KindTurn && record.End.Outcome == telemetry.OutcomeFailed && record.End.Code == "model_startup" {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("records = %#v, want failed Turn with stable model_startup code", records)
	}
}

func TestTelemetryDuplicateJoinDoesNotClaimOwnerWork(t *testing.T) {
	store := newTurnMemoryStore(t)
	model := newBlockingAcceptanceModel("done")
	recorder := &memory.Telemetry{}
	signals := &turnSignalTracer{Telemetry: recorder, second: make(chan struct{})}
	config := application.DefaultConfig()
	config.Telemetry = signals
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct{ err error }
	ownerDone := make(chan outcome, 1)
	joinDone := make(chan outcome, 1)
	request := application.RunTurnRequest{SessionID: created.SessionID, RequestID: "telemetry-join", Input: "wait", Sink: &testkit.RecordingSink{}}
	go func() { _, runErr := service.RunTurn(context.Background(), request); ownerDone <- outcome{runErr} }()
	<-model.started
	go func() { _, runErr := service.RunTurn(context.Background(), request); joinDone <- outcome{runErr} }()
	<-signals.second
	awaitRegistryLeases(t, service, request.RequestID, 2, "telemetry duplicate join")
	close(model.release)
	if result := <-ownerDone; result.err != nil {
		t.Fatal(result.err)
	}
	if result := <-joinDone; result.err != nil {
		t.Fatal(result.err)
	}

	roles := map[string]int{}
	children := map[uint64]int{}
	for _, record := range recorder.Records() {
		if record.Start.Kind == telemetry.KindTurn {
			roles[stringAttribute(record.End.Attributes, telemetry.KeyTurnRole)]++
		}
		if record.Parent != 0 {
			children[record.Parent]++
		}
	}
	if roles["owner"] != 1 || roles["joined"] != 1 {
		t.Fatalf("Turn roles = %#v, want one owner and one joined", roles)
	}
	for _, record := range recorder.Records() {
		if record.Start.Kind == telemetry.KindTurn && stringAttribute(record.End.Attributes, telemetry.KeyTurnRole) == "joined" && children[record.ID] != 0 {
			t.Fatalf("joined Turn claimed %d owner child spans", children[record.ID])
		}
	}
}

func TestTelemetryCancellationAndPolicyDenialOutcomes(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		store := newTurnMemoryStore(t)
		model := newBlockingAcceptanceModel("unused")
		recorder := &memory.Telemetry{}
		config := application.DefaultConfig()
		config.Telemetry = recorder
		service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
		created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, runErr := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "telemetry-cancel", Input: "wait", Sink: &testkit.RecordingSink{}})
			done <- runErr
		}()
		<-model.started
		cancel()
		if err := <-done; err == nil {
			t.Fatal("canceled RunTurn returned nil error")
		}
		if !hasOutcome(recorder.Records(), telemetry.KindTurn, telemetry.OutcomeCanceled) {
			t.Fatalf("records = %#v, want canceled Turn", recorder.Records())
		}
	})

	t.Run("policy denial", func(t *testing.T) {
		recorder := &memory.Telemetry{}
		model := newSequenceModel(
			[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-deny", Name: tools.NameWriteFile, Arguments: `{"path":"out.txt","content":"x"}`}}, {Type: engine.StreamEventCompleted}},
			[]engine.StreamEvent{{Type: engine.StreamEventCompleted}},
		)
		config := application.DefaultConfig()
		config.PolicyMode = policy.ModeReadOnly
		config.Telemetry = recorder
		service, _ := newToolService(t, model, testkit.NewMemFS("/workspace"), nil, nil, config)
		created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "telemetry-deny", Input: "write", Sink: &testkit.RecordingSink{}}); err != nil {
			t.Fatal(err)
		}
		if !hasOutcome(recorder.Records(), telemetry.KindPolicyDecide, telemetry.OutcomeDenied) || countKind(recorder.Records(), telemetry.KindApprovalWait) != 0 || countKind(recorder.Records(), telemetry.KindToolExecute) != 0 {
			t.Fatalf("records = %#v, want denied policy and no approval/execution", recorder.Records())
		}
	})
}

func TestTelemetryManualCompactionIsARootWithAppendChildren(t *testing.T) {
	store, state, _, ids := buildHistorySession(t, 6)
	runner, err := engine.NewTurnRunner(&acceptanceSuccessModel{text: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &memory.Telemetry{}
	config := application.DefaultConfig()
	config.Telemetry = recorder
	config.Context = application.ContextConfig{
		Enabled: true, Budget: contextengine.Budget{HardInput: 1_000_000, Trigger: 1_000_000, Target: 500_000, ProtectedTail: 1, SummaryOutputCap: 4_000},
		Meter: contextengine.WireEstimateMeter{}, Summarizer: &scriptedSummarizer{text: validSummaryText()}, CheckpointStore: &fakeCheckpointStore{},
	}
	service, err := application.NewService(store, ids, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID}); err != nil {
		t.Fatal(err)
	}
	records := recorder.Records()
	var compactID uint64
	for _, record := range records {
		if record.Start.Kind == telemetry.KindContextCompact {
			compactID = record.ID
			if record.Parent != 0 || record.End.Outcome != telemetry.OutcomeOK || stringAttribute(record.Start.Attributes, telemetry.KeyContextStrategy) != "summary" {
				t.Fatalf("compact span = %#v, want successful summary root", record)
			}
		}
	}
	children := 0
	for _, record := range records {
		if record.Parent == compactID && record.Start.Kind == telemetry.KindStoreAppend {
			children++
		}
	}
	if compactID == 0 || children != 2 {
		t.Fatalf("records = %#v, want manual compact root with start/complete append children", records)
	}
}

func TestTelemetryAutomaticContextTriggersHaveStableTopology(t *testing.T) {
	t.Run("pre-turn compaction", func(t *testing.T) {
		store, state, scan, ids := buildHistorySession(t, 6)
		fullEstimate := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
		recorder := &memory.Telemetry{}
		deps := application.ContextOrchestratorDeps{
			Store: store, IDs: ids, Clock: testkit.FixedClock{Time: acceptanceTime}, Telemetry: recorder,
			Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
			CheckpointStore: &fakeCheckpointStore{}, Summarizer: &scriptedSummarizer{text: validSummaryText()}, Meter: contextengine.WireEstimateMeter{},
			Budget: contextengine.Budget{HardInput: fullEstimate * 10, Trigger: fullEstimate / 4, Target: fullEstimate / 8, ProtectedTail: 1, SummaryOutputCap: 400},
		}
		result, err := application.PrepareContext(context.Background(), deps, state, application.PrepareContextInput{
			SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn,
			CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
		})
		if err != nil || !result.CompactionRan {
			t.Fatalf("PrepareContext() = (%#v, %v), want compaction", result, err)
		}
		assertContextTrace(t, recorder.Records(), 0, domain.ContextTriggerPreTurn, true)
	})

	t.Run("mid-turn", func(t *testing.T) {
		fs := testkit.NewMemFS("/workspace")
		fs.AddFile("README.md", []byte("fixture"))
		model := newSequenceModel(
			[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"README.md"}`}}, {Type: engine.StreamEventCompleted}},
			[]engine.StreamEvent{{Type: engine.StreamEventCompleted}},
		)
		catalog, err := tools.NewCatalog(tools.DefaultWorkspaceSpecs())
		if err != nil {
			t.Fatal(err)
		}
		recorder := &memory.Telemetry{}
		config := newContextAwareToolConfig(catalog, fs, testkit.NewScriptedRunner(), nil)
		config.Telemetry = recorder
		service := newTurnServiceWithConfig(t, newTurnMemoryStore(t), testkit.NewSequenceIDs(), model, config)
		created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "telemetry-mid-turn", Input: "read", Sink: &testkit.RecordingSink{}}); err != nil {
			t.Fatal(err)
		}
		assertContextTrace(t, recorder.Records(), traceIDForKind(recorder.Records(), telemetry.KindTurn), domain.ContextTriggerMidTurn, false)
	})

	t.Run("overflow retry", func(t *testing.T) {
		store, state, _, ids := buildHistorySession(t, 6)
		model := &overflowModel{failCount: 1, text: "recovered"}
		runner, err := engine.NewTurnRunner(model)
		if err != nil {
			t.Fatal(err)
		}
		recorder := &memory.Telemetry{}
		config := application.DefaultConfig()
		config.Context = newOverflowContextConfig(2)
		config.Telemetry = recorder
		service, err := application.NewService(store, ids, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: state.ID, RequestID: "telemetry-overflow", Input: "continue", Sink: &testkit.RecordingSink{}}); err != nil {
			t.Fatal(err)
		}
		assertContextTrace(t, recorder.Records(), traceIDForKind(recorder.Records(), telemetry.KindTurn), domain.ContextTriggerOverflowRetry, true)
	})
}

func assertContextTrace(t *testing.T, records []memory.TraceRecord, parent uint64, trigger string, wantCompaction bool) {
	t.Helper()
	var prepareID, compactID uint64
	for _, record := range records {
		if record.Start.Kind == telemetry.KindContextPrepare && stringAttribute(record.Start.Attributes, telemetry.KeyContextTrigger) == trigger {
			prepareID = record.ID
			if record.Parent != parent || record.End.Outcome != telemetry.OutcomeOK {
				t.Fatalf("prepare span = %#v, want successful child of %d", record, parent)
			}
		}
	}
	for _, record := range records {
		if record.Start.Kind == telemetry.KindContextCompact && record.Parent == prepareID {
			compactID = record.ID
		}
	}
	if prepareID == 0 || (compactID != 0) != wantCompaction {
		t.Fatalf("records = %#v, trigger %s compaction=%t", records, trigger, wantCompaction)
	}
}

func traceIDForKind(records []memory.TraceRecord, kind telemetry.Kind) uint64 {
	for _, record := range records {
		if record.Start.Kind == kind {
			return record.ID
		}
	}
	return 0
}

func hasOutcome(records []memory.TraceRecord, kind telemetry.Kind, outcome telemetry.Outcome) bool {
	for _, record := range records {
		if record.Start.Kind == kind && record.End.Outcome == outcome {
			return true
		}
	}
	return false
}

func countKind(records []memory.TraceRecord, kind telemetry.Kind) int {
	count := 0
	for _, record := range records {
		if record.Start.Kind == kind {
			count++
		}
	}
	return count
}

type turnSignalTracer struct {
	*memory.Telemetry
	turns  atomic.Int32
	second chan struct{}
}

func (tracer *turnSignalTracer) Start(ctx context.Context, start telemetry.Start) (context.Context, telemetry.Span) {
	if start.Kind == telemetry.KindTurn && tracer.turns.Add(1) == 2 {
		close(tracer.second)
	}
	return tracer.Telemetry.Start(ctx, start)
}

func stringAttribute(attributes []telemetry.Attribute, key telemetry.Key) string {
	for _, attribute := range attributes {
		if attribute.Key == key && attribute.Kind == telemetry.ValueString {
			return attribute.String
		}
	}
	return ""
}
