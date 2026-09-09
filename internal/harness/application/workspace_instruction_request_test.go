package application_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/agentinstructions"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestConversationRequestsKeepStablePrefixAndAppendInstructionChanges(t *testing.T) {
	files := testkit.NewMemFS("/workspace")
	files.AddFile("AGENTS.md", []byte("Run focused tests.\n"))
	model := &repeatingSuccessModel{text: "ok"}
	config := application.DefaultConfig()
	config.Files = files
	config.Context = application.ContextConfig{
		Enabled: true,
		Budget:  contextengine.Budget{HardInput: 1_000_000, Trigger: 900_000, Target: 500_000, ProtectedTail: 100_000, SummaryOutputCap: 4_000},
		Meter:   contextengine.WireEstimateMeter{}, Summarizer: &scriptedSummarizer{text: validSummaryText()}, CheckpointStore: &fakeCheckpointStore{},
	}
	store := newTurnMemoryStore(t)
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}

	run := func(requestID, input string) {
		t.Helper()
		if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
			SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(requestID), Input: input, Sink: &testkit.RecordingSink{},
		}); err != nil {
			t.Fatalf("RunTurn(%q) error = %v", input, err)
		}
	}
	run("request-instructions-1", "first")
	run("request-instructions-2", "second")
	files.AddFile("AGENTS.md", []byte("Run all tests before completion.\n"))
	run("request-instructions-3", "third")
	run("request-instructions-4", "fourth")

	calls := model.Calls()
	if len(calls) != 4 {
		t.Fatalf("model calls = %d, want 4", len(calls))
	}
	for index, call := range calls {
		if len(call.Messages) == 0 || !reflect.DeepEqual(call.Messages[0], agentinstructions.SystemPromptMessage()) {
			t.Fatalf("call[%d] first message = %#v, want exact fixed system prompt", index, call.Messages)
		}
		if call.Messages[len(call.Messages)-1].Text != []string{"first", "second", "third", "fourth"}[index] {
			t.Fatalf("call[%d] current input is not last: %#v", index, call.Messages)
		}
	}
	if !reflect.DeepEqual(calls[0].Messages, calls[1].Messages[:len(calls[0].Messages)]) {
		t.Fatal("unchanged second turn altered the existing provider prefix")
	}
	if !reflect.DeepEqual(calls[1].Messages, calls[2].Messages[:len(calls[1].Messages)]) {
		t.Fatal("replacement turn rewrote bytes before the newly appended delta")
	}
	if !reflect.DeepEqual(calls[2].Messages, calls[3].Messages[:len(calls[2].Messages)]) {
		t.Fatal("unchanged turn after replacement altered the existing provider prefix")
	}

	wantDeltas := []int{1, 1, 2, 2}
	for index, call := range calls {
		got := 0
		for _, message := range call.Messages {
			if message.Role == domain.PromptRoleUser && strings.Contains(message.Text, "<workspace_instruction") {
				got++
			}
		}
		if got != wantDeltas[index] {
			t.Fatalf("call[%d] instruction delta messages = %d, want %d: %#v", index, got, wantDeltas[index], call.Messages)
		}
	}
}

func TestSuccessfulStructuredFileToolDiscoversNestedInstructionsBeforeNextRequest(t *testing.T) {
	files := testkit.NewMemFS("/workspace")
	files.AddFile("pkg/AGENTS.md", []byte("Treat pkg as a compatibility boundary.\n"))
	files.AddFile("pkg/input.txt", []byte("fixture"))
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"pkg/input.txt"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "done"}, {Type: engine.StreamEventCompleted}},
	)
	catalog, err := tools.NewCatalog(tools.DefaultWorkspaceSpecs())
	if err != nil {
		t.Fatal(err)
	}
	config := newContextAwareToolConfig(catalog, files, testkit.NewScriptedRunner(), nil)
	store := newTurnMemoryStore(t)
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-nested-instructions", Input: "inspect pkg/input.txt", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatal(err)
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	toolCompleted, nestedInstruction, secondRequest := -1, -1, -1
	requestCount := 0
	for index, record := range records {
		switch event := record.Event.(type) {
		case domain.ToolCallCompleted:
			toolCompleted = index
		case domain.WorkspaceInstructionsRecorded:
			for _, change := range event.Changes {
				if change.Path == "pkg/AGENTS.md" {
					nestedInstruction = index
				}
			}
		case domain.ModelRequestRecorded:
			requestCount++
			if requestCount == 2 {
				secondRequest = index
				found := false
				for _, message := range event.Messages {
					if strings.Contains(message.Text, "Treat pkg as a compatibility boundary.") {
						found = true
					}
				}
				if !found {
					t.Fatalf("second request does not contain nested instruction delta: %#v", event.Messages)
				}
			}
		}
	}
	if !(toolCompleted >= 0 && nestedInstruction > toolCompleted && secondRequest > nestedInstruction) {
		t.Fatalf("indices tool_completed=%d nested_instruction=%d second_request=%d; want committed tool result < instruction event < consuming request", toolCompleted, nestedInstruction, secondRequest)
	}
}
