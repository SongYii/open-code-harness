package application_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// newContextAwareToolConfig mirrors loop_test.go's own toolConfig, adding
// implementation plan Task 10's opt-in Config.Context with a generous
// Budget (mid-turn preparation is exercised for its own sake here, not
// compaction pressure -- that is already covered by
// context_orchestrator_test.go).
func newContextAwareToolConfig(catalog *tools.Catalog, files tools.FileSystem, commands tools.CommandRunner, approver tools.Approver) application.Config {
	config := toolConfig(catalog, files, commands, approver)
	config.Context = application.ContextConfig{
		Enabled:         true,
		Budget:          contextengine.Budget{HardInput: 1_000_000, Trigger: 900_000, Target: 500_000, ProtectedTail: 100_000, SummaryOutputCap: 4_000},
		Meter:           contextengine.WireEstimateMeter{},
		Summarizer:      &scriptedSummarizer{text: validSummaryText()},
		CheckpointStore: &fakeCheckpointStore{},
	}
	return config
}

func TestMidTurnPreparationAppendsContextPreparedBetweenToolResultAndSecondRequest(t *testing.T) {
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
	catalog, err := tools.NewCatalog(tools.DefaultWorkspaceSpecs())
	if err != nil {
		t.Fatal(err)
	}
	store := newTurnMemoryStore(t)
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, newContextAwareToolConfig(catalog, fs, testkit.NewScriptedRunner(), nil))
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-mid-turn", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Text != "done" || result.Status != domain.TurnStatusCompleted || !result.TerminalCommitted {
		t.Fatalf("result = %#v", result)
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{
		domain.EventSessionCreated,
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventContextPreparedRecorded,
		domain.EventModelRequestRecorded,
		domain.EventModelUsageRecorded,
		domain.EventAssistantMessageCompleted,
		domain.EventToolCallStarted,
		domain.EventPolicyDecisionRecorded,
		domain.EventToolCallCompleted,
		domain.EventAssistantMessageStarted,
		domain.EventContextPreparedRecorded,
		domain.EventModelRequestRecorded,
		domain.EventAssistantMessageCompleted,
		domain.EventTurnCompleted,
	}
	if got := acceptanceEventTypes(records); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}

	var preparedEvents []domain.ContextPreparedRecorded
	var requestEvents []domain.ModelRequestRecorded
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.ContextPreparedRecorded:
			preparedEvents = append(preparedEvents, event)
		case domain.ModelRequestRecorded:
			requestEvents = append(requestEvents, event)
		}
	}
	if len(preparedEvents) != 2 || len(requestEvents) != 2 {
		t.Fatalf("prepared=%d request=%d, want 2 and 2", len(preparedEvents), len(requestEvents))
	}
	if preparedEvents[0].Trigger != domain.ContextTriggerPreTurn {
		t.Fatalf("first Trigger = %q, want pre_turn", preparedEvents[0].Trigger)
	}
	if preparedEvents[1].Trigger != domain.ContextTriggerMidTurn {
		t.Fatalf("second Trigger = %q, want mid_turn", preparedEvents[1].Trigger)
	}
	if preparedEvents[1].ContextDecisionID == preparedEvents[0].ContextDecisionID {
		t.Fatal("mid-turn ContextDecisionID equals the pre-turn one, want a distinct decision per attempt")
	}
	if requestEvents[1].ContextDecisionID != preparedEvents[1].ContextDecisionID {
		t.Fatalf("second request ContextDecisionID = %q, want %q", requestEvents[1].ContextDecisionID, preparedEvents[1].ContextDecisionID)
	}

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model received %d calls, want 2", len(calls))
	}
	// The same Global Constraint as the admission path: the second
	// recorded request's Messages/Tools equal exactly what engine.Model
	// received for the second Step.
	if !reflect.DeepEqual(calls[1].Messages, requestEvents[1].Messages) {
		t.Fatalf("dispatched second Messages = %#v, want exactly the recorded %#v", calls[1].Messages, requestEvents[1].Messages)
	}
	if !reflect.DeepEqual(calls[1].Tools, requestEvents[1].Tools) {
		t.Fatalf("dispatched second Tools = %#v, want exactly the recorded %#v", calls[1].Tools, requestEvents[1].Tools)
	}
	// The second request's history must include the first Step's own
	// assistant message and the tool result -- mid-turn preparation must
	// not drop what already happened this Turn.
	foundToolResult := false
	for _, message := range requestEvents[1].Messages {
		if message.Role == domain.PromptRoleTool && message.ToolCallID == "call-read" {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("second request Messages = %#v, missing the first Step's tool result", requestEvents[1].Messages)
	}
}

// bigFixtureFileContent is a real read_file Tool Result large enough
// (comfortably under Tool Runtime's own MaxToolResultBytes cap, but far
// above design §10's largest possible per-result cap of 2048 tokens) that
// it must be pruned once MaxPrunedToolResultsPerRequest enables it.
func bigFixtureFileContent() []byte {
	line := "this line of file content repeats many times in the fixture.\n"
	content := make([]byte, 0, len(line)*800)
	for i := 0; i < 800; i++ {
		content = append(content, line...)
	}
	return content
}

// dispatchedToolResultText builds a Session that reads a large fixture
// file mid-Turn (mirroring TestMidTurnPreparationAppendsContextPreparedBetweenToolResultAndSecondRequest's
// own real read_file/mid_turn shape) with maxPrunedToolResults configured,
// and returns the second (mid_turn) ModelRequestRecorded's own text for
// the read_file Tool Result -- exactly what the composition-wired path
// would actually dispatch to a real Provider.
func dispatchedToolResultText(t *testing.T, maxPrunedToolResults uint32) string {
	t.Helper()
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", bigFixtureFileContent())
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
	catalog, err := tools.NewCatalog(tools.DefaultWorkspaceSpecs())
	if err != nil {
		t.Fatal(err)
	}
	config := newContextAwareToolConfig(catalog, fs, testkit.NewScriptedRunner(), nil)
	config.Context.MaxPrunedToolResultsPerRequest = maxPrunedToolResults
	store := newTurnMemoryStore(t)
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-mid-turn", Input: "inspect", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var requestEvents []domain.ModelRequestRecorded
	for _, record := range records {
		if event, ok := record.Event.(domain.ModelRequestRecorded); ok {
			requestEvents = append(requestEvents, event)
		}
	}
	if len(requestEvents) != 2 {
		t.Fatalf("recorded %d ModelRequestRecorded, want 2", len(requestEvents))
	}
	for _, message := range requestEvents[1].Messages {
		if message.Role == domain.PromptRoleTool && message.ToolCallID == "call-read" {
			return message.Text
		}
	}
	t.Fatal("second request Messages missing the read_file Tool Result")
	return ""
}

// TestMidTurnToolResultPruningIsWiredEndToEnd is the regression test for
// closing design §10/MaxPrunedToolResultsPerRequest's own disclosed
// "accepted but inert" gap: a real, composition-shaped tool-calling Turn's
// second (mid_turn) request must dispatch a pruned Tool Result once
// MaxPrunedToolResultsPerRequest is configured, and the exact same Turn
// with pruning left at its zero value (every existing caller's own
// default) must keep dispatching the byte-identical original content.
func TestMidTurnToolResultPruningIsWiredEndToEnd(t *testing.T) {
	original := bigFixtureFileContent()

	disabled := dispatchedToolResultText(t, 0)
	if disabled != string(original) {
		t.Fatalf("pruning disabled (MaxPrunedToolResultsPerRequest=0): dispatched Tool Result was altered, got %d bytes want %d byte-identical original", len(disabled), len(original))
	}

	pruned := dispatchedToolResultText(t, 5)
	if pruned == string(original) {
		t.Fatal("pruning enabled (MaxPrunedToolResultsPerRequest=5): dispatched Tool Result is still the full, unpruned original content")
	}
	if !strings.Contains(pruned, "call-read") {
		t.Fatalf("pruned Tool Result does not name its own event_id: %q", pruned)
	}
	if len(pruned) >= len(original) {
		t.Fatalf("pruned Tool Result (%d bytes) is not smaller than the original (%d bytes)", len(pruned), len(original))
	}
}
