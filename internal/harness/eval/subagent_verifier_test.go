package eval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type sessionTraceEvent struct {
	sessionID string
	eventType string
	data      any
}

func subagentTraceReader(t *testing.T, events []sessionTraceEvent) *ArtifactReader {
	t.Helper()
	lines := make([]string, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event.data)
		if err != nil {
			t.Fatalf("marshal %s: %v", event.eventType, err)
		}
		recorded := map[string]any{
			"sessionId": event.sessionID,
			"type":      event.eventType,
			"data":      json.RawMessage(payload),
		}
		line, err := json.Marshal(map[string]any{"events": []any{recorded}})
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		lines = append(lines, string(line))
	}
	return traceReaderFromLines(t, lines)
}

func validSubagentTrace() []sessionTraceEvent {
	return []sessionTraceEvent{
		{"parent", domain.EventToolCallStarted, domain.ToolCallStarted{
			TurnID: "parent-turn", ItemID: "parent-tool", CallID: "delegate-call", Name: "delegate_task", Arguments: `{"task":"inspect NOTES.md"}`, StepIndex: 1,
		}},
		{"child", domain.EventSessionCreated, domain.SessionCreated{
			WorkspaceRoot: "/workspace", Parent: &domain.SessionParent{SessionID: "parent", TurnID: "parent-turn", ItemID: "parent-tool", CallID: "delegate-call"},
		}},
		{"child", domain.EventModelRequestRecorded, domain.ModelRequestRecorded{
			TurnID: "child-turn", ItemID: "child-model-1", Messages: []domain.ModelPromptMessage{{Role: "user", Text: "inspect NOTES.md"}},
			Tools: []domain.ToolSchema{{Name: "read_file"}, {Name: "list_dir"}},
		}},
		{"child", domain.EventToolCallStarted, domain.ToolCallStarted{
			TurnID: "child-turn", ItemID: "child-tool", CallID: "read-call", Name: "read_file", Arguments: `{"path":"NOTES.md"}`, StepIndex: 1,
		}},
		{"child", domain.EventToolCallCompleted, domain.ToolCallCompleted{
			TurnID: "child-turn", ItemID: "child-tool", CallID: "read-call", Content: "fixture note",
		}},
		{"child", domain.EventModelRequestRecorded, domain.ModelRequestRecorded{
			TurnID: "child-turn", ItemID: "child-model-2", Messages: []domain.ModelPromptMessage{{Role: "tool", Text: "fixture note", ToolCallID: "read-call", Name: "read_file"}},
			Tools: []domain.ToolSchema{{Name: "read_file"}, {Name: "list_dir"}},
		}},
		{"child", domain.EventAssistantMessageCompleted, domain.AssistantMessageCompleted{
			TurnID: "child-turn", ItemID: "child-model-2", Text: "acknowledged",
		}},
		{"child", domain.EventTurnCompleted, domain.TurnCompleted{TurnID: "child-turn"}},
		{"parent", domain.EventToolCallCompleted, domain.ToolCallCompleted{
			TurnID: "parent-turn", ItemID: "parent-tool", CallID: "delegate-call", Content: "child session: child\nacknowledged",
		}},
	}
}

func TestVerifySubagentDelegationObservedCorrelatesCompleteReadOnlyChild(t *testing.T) {
	got := verifySubagentDelegationObserved(subagentTraceReader(t, validSubagentTrace()), validScenario())
	if got.Status != ScorePass {
		t.Fatalf("status = %q, want pass", got.Status)
	}
}

func TestVerifySubagentDelegationObservedRejectsBrokenProof(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]sessionTraceEvent) []sessionTraceEvent
	}{
		{"wrong lineage", func(events []sessionTraceEvent) []sessionTraceEvent {
			created := events[1].data.(domain.SessionCreated)
			created.Parent.CallID = "other-call"
			events[1].data = created
			return events
		}},
		{"child exec", func(events []sessionTraceEvent) []sessionTraceEvent {
			events[3].data = domain.ToolCallStarted{TurnID: "child-turn", ItemID: "child-tool", CallID: "exec-call", Name: "exec", Arguments: `{}`, StepIndex: 1}
			return events
		}},
		{"child recursion", func(events []sessionTraceEvent) []sessionTraceEvent {
			events[3].data = domain.ToolCallStarted{TurnID: "child-turn", ItemID: "child-tool", CallID: "nested-call", Name: "delegate_task", Arguments: `{}`, StepIndex: 1}
			return events
		}},
		{"widened schema", func(events []sessionTraceEvent) []sessionTraceEvent {
			request := events[2].data.(domain.ModelRequestRecorded)
			request.Tools = append(request.Tools, domain.ToolSchema{Name: "exec"})
			events[2].data = request
			return events
		}},
		{"missing child terminal", func(events []sessionTraceEvent) []sessionTraceEvent {
			return append(events[:6], events[8:]...)
		}},
		{"parent received another child", func(events []sessionTraceEvent) []sessionTraceEvent {
			completed := events[8].data.(domain.ToolCallCompleted)
			completed.Content = strings.Replace(completed.Content, "child session: child", "child session: other", 1)
			events[8].data = completed
			return events
		}},
		{"parent receipt precedes child terminal", func(events []sessionTraceEvent) []sessionTraceEvent {
			events[7], events[8] = events[8], events[7]
			return events
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := test.mutate(validSubagentTrace())
			got := verifySubagentDelegationObserved(subagentTraceReader(t, events), validScenario())
			if got.Status != ScoreFail {
				t.Fatalf("status = %q, want fail", got.Status)
			}
		})
	}
}

func TestVerifySubagentDelegationObservedIsIndeterminateWithoutAudit(t *testing.T) {
	got := verifySubagentDelegationObserved(&ArtifactReader{}, validScenario())
	if got.Status != ScoreIndeterminate {
		t.Fatalf("status = %q, want indeterminate", got.Status)
	}
}
