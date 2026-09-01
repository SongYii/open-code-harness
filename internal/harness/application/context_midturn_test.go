package application_test

import (
	"context"
	"reflect"
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
