package acp

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

func TestToolCallIDAndToolKind(t *testing.T) {
	if got := ToolCallID("turn-1", "call-1"); got != "turn-1/call-1" {
		t.Fatalf("ToolCallID = %q", got)
	}
	for name, kind := range map[string]string{
		"read_file":  "read",
		"list_dir":   "read",
		"write_file": "edit",
		"exec":       "execute",
		"unknown":    "other",
	} {
		if got := ToolKind(name); got != kind {
			t.Fatalf("ToolKind(%q) = %q, want %q", name, got, kind)
		}
	}
}

func TestProjectRuntimeEvent(t *testing.T) {
	turn := domain.TurnID("turn-1")
	live := LiveTool{TurnID: turn, CallID: "call-1", Name: "read_file"}
	correlation := engine.Correlation{TurnID: turn}
	tests := []struct {
		name  string
		event engine.RuntimeEvent
		live  LiveTool
		want  []any
	}{
		{
			name:  "model.stream.started",
			event: engine.RuntimeEvent{Type: engine.RuntimeModelStreamStarted, Correlation: correlation},
		},
		{
			name:  "model.text.delta",
			event: engine.RuntimeEvent{Type: engine.RuntimeModelTextDelta, Text: "hello", Correlation: correlation},
			want: []any{agentMessageChunk{
				SessionUpdate: "agent_message_chunk",
				Content:       textContent{Type: "text", Text: "hello"},
			}},
		},
		{
			name:  "model.text.delta empty",
			event: engine.RuntimeEvent{Type: engine.RuntimeModelTextDelta, Correlation: correlation},
		},
		{
			name:  "model.tool_call",
			event: engine.RuntimeEvent{Type: engine.RuntimeModelToolCall, Text: "read_file:call-1", Correlation: correlation},
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call",
				ToolCallID:    "turn-1/call-1",
				Title:         "read_file",
				Kind:          "read",
				Status:        "pending",
			}},
		},
		{
			name:  "model.stream.completed",
			event: engine.RuntimeEvent{Type: engine.RuntimeModelStreamCompleted, Correlation: correlation},
		},
		{
			name:  "model.stream.failed",
			event: engine.RuntimeEvent{Type: engine.RuntimeModelStreamFailed, Code: "model_stream", Correlation: correlation},
		},
		{
			name:  "model.stream.interrupted",
			event: engine.RuntimeEvent{Type: engine.RuntimeModelStreamInterrupted, Code: "canceled", Correlation: correlation},
		},
		{
			name:  "tool.execution.started",
			event: engine.RuntimeEvent{Type: engine.RuntimeToolExecutionStarted, Text: "write_file:call-2", Correlation: correlation},
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    "turn-1/call-2",
				Status:        "in_progress",
			}},
		},
		{
			name:  "tool.execution.completed",
			event: engine.RuntimeEvent{Type: engine.RuntimeToolExecutionCompleted, Text: "read_file:call-1", Correlation: correlation},
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    "turn-1/call-1",
				Status:        "completed",
			}},
		},
		{
			name:  "tool.execution.completed uses live when Text empty",
			event: engine.RuntimeEvent{Type: engine.RuntimeToolExecutionCompleted, Correlation: correlation},
			live:  live,
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    "turn-1/call-1",
				Status:        "completed",
			}},
		},
		{
			name:  "tool.execution.failed code-only",
			event: engine.RuntimeEvent{Type: engine.RuntimeToolExecutionFailed, Code: "policy_denied", Correlation: correlation},
			live:  live,
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    "turn-1/call-1",
				Status:        "failed",
			}},
		},
		{
			name:  "approval.requested",
			event: engine.RuntimeEvent{Type: engine.RuntimeApprovalRequested, Text: "write_file:call-1", Correlation: correlation},
			live:  live,
		},
		{
			name:  "approval.resolved",
			event: engine.RuntimeEvent{Type: engine.RuntimeApprovalResolved, Text: "write_file:call-1", Correlation: correlation},
			live:  live,
		},
		{
			name:  "append.completed",
			event: engine.RuntimeEvent{Type: engine.RuntimeAppendCompleted, Correlation: correlation},
		},
	}
	seen := map[engine.RuntimeEventType]bool{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			seen[test.event.Type] = true
			assertUpdates(t, ProjectRuntimeEvent("session-1", test.event, test.live), test.want)
		})
	}
	for _, eventType := range []engine.RuntimeEventType{
		engine.RuntimeModelStreamStarted,
		engine.RuntimeModelTextDelta,
		engine.RuntimeModelToolCall,
		engine.RuntimeModelStreamCompleted,
		engine.RuntimeModelStreamFailed,
		engine.RuntimeModelStreamInterrupted,
		engine.RuntimeToolExecutionStarted,
		engine.RuntimeToolExecutionCompleted,
		engine.RuntimeToolExecutionFailed,
		engine.RuntimeApprovalRequested,
		engine.RuntimeApprovalResolved,
		engine.RuntimeAppendCompleted,
	} {
		if !seen[eventType] {
			t.Errorf("missing runtime event type %q", eventType)
		}
	}
}

func TestProjectRecordedEvent(t *testing.T) {
	turn := domain.TurnID("turn-1")
	tests := []struct {
		name   string
		record domain.RecordedEvent
		want   []any
	}{
		{name: "session.created", record: domain.RecordedEvent{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}},
		{name: "session.closed", record: domain.RecordedEvent{Event: domain.SessionClosed{}}},
		{
			name:   "turn.started",
			record: domain.RecordedEvent{Event: domain.TurnStarted{TurnID: turn, Input: "hello"}},
			want: []any{agentMessageChunk{
				SessionUpdate: "user_message_chunk",
				Content:       textContent{Type: "text", Text: "hello"},
			}},
		},
		{name: "turn.started empty", record: domain.RecordedEvent{Event: domain.TurnStarted{TurnID: turn}}},
		{name: "turn.completed", record: domain.RecordedEvent{Event: domain.TurnCompleted{TurnID: turn}}},
		{name: "turn.failed", record: domain.RecordedEvent{Event: domain.TurnFailed{TurnID: turn, Code: "failed", Message: "boom"}}},
		{name: "turn.interrupted", record: domain.RecordedEvent{Event: domain.TurnInterrupted{TurnID: turn, Reason: "canceled"}}},
		{name: "assistant.message.started", record: domain.RecordedEvent{Event: domain.AssistantMessageStarted{TurnID: turn, ItemID: "item-1"}}},
		{
			name:   "assistant.message.completed",
			record: domain.RecordedEvent{Event: domain.AssistantMessageCompleted{TurnID: turn, ItemID: "item-1", Text: "world"}},
			want: []any{agentMessageChunk{
				SessionUpdate: "agent_message_chunk",
				Content:       textContent{Type: "text", Text: "world"},
			}},
		},
		{name: "assistant.message.completed empty", record: domain.RecordedEvent{Event: domain.AssistantMessageCompleted{TurnID: turn, ItemID: "item-1"}}},
		{
			name:   "assistant.message.failed",
			record: domain.RecordedEvent{Event: domain.AssistantMessageFailed{TurnID: turn, ItemID: "item-1", Code: "model_stream", Message: "partial"}},
			want: []any{agentMessageChunk{
				SessionUpdate: "agent_message_chunk",
				Content:       textContent{Type: "text", Text: "partial"},
			}},
		},
		{name: "assistant.message.failed empty", record: domain.RecordedEvent{Event: domain.AssistantMessageFailed{TurnID: turn, ItemID: "item-1", Code: "model_stream"}}},
		{
			name:   "assistant.message.interrupted",
			record: domain.RecordedEvent{Event: domain.AssistantMessageInterrupted{TurnID: turn, ItemID: "item-1", Code: "canceled", Message: "stop"}},
			want: []any{agentMessageChunk{
				SessionUpdate: "agent_message_chunk",
				Content:       textContent{Type: "text", Text: "stop"},
			}},
		},
		{name: "assistant.message.interrupted empty", record: domain.RecordedEvent{Event: domain.AssistantMessageInterrupted{TurnID: turn, ItemID: "item-1", Code: "canceled"}}},
		{name: "model.request.recorded", record: domain.RecordedEvent{Event: domain.ModelRequestRecorded{TurnID: turn, ItemID: "item-1"}}},
		{name: "model.usage.recorded", record: domain.RecordedEvent{Event: domain.ModelUsageRecorded{TurnID: turn, ItemID: "item-1", InputTokens: 3}}},
		{
			name: "tool.call.started",
			record: domain.RecordedEvent{Event: domain.ToolCallStarted{
				TurnID: turn, ItemID: "item-2", CallID: "call-1", Name: "read_file",
				Arguments: `{"path":"NOTES.md"}`, StepIndex: 1,
			}},
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call",
				ToolCallID:    "turn-1/call-1",
				Title:         "read_file",
				Kind:          "read",
				Status:        "in_progress",
				RawInput:      json.RawMessage(`{"path":"NOTES.md"}`),
			}},
		},
		{
			name: "tool.call.started omits invalid rawInput",
			record: domain.RecordedEvent{Event: domain.ToolCallStarted{
				TurnID: turn, ItemID: "item-2", CallID: "call-1", Name: "exec", Arguments: "echo hi",
			}},
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call",
				ToolCallID:    "turn-1/call-1",
				Title:         "exec",
				Kind:          "execute",
				Status:        "in_progress",
			}},
		},
		{
			name: "tool.call.completed",
			record: domain.RecordedEvent{Event: domain.ToolCallCompleted{
				TurnID: turn, ItemID: "item-2", CallID: "call-1", Content: "file body", Truncated: true,
			}},
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    "turn-1/call-1",
				Status:        "completed",
				Content: []toolCallContent{{
					Type:    "content",
					Content: textContent{Type: "text", Text: "file body"},
				}},
			}},
		},
		{
			name: "tool.call.failed",
			record: domain.RecordedEvent{Event: domain.ToolCallFailed{
				TurnID: turn, ItemID: "item-2", CallID: "call-1", Code: "policy_denied", Message: "denied",
			}},
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    "turn-1/call-1",
				Status:        "failed",
				Content: []toolCallContent{{
					Type:    "content",
					Content: textContent{Type: "text", Text: "denied"},
				}},
			}},
		},
		{
			name: "tool.call.interrupted",
			record: domain.RecordedEvent{Event: domain.ToolCallInterrupted{
				TurnID: turn, ItemID: "item-2", CallID: "call-1", Code: "canceled", Message: "stopped",
			}},
			want: []any{toolCallUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    "turn-1/call-1",
				Status:        "failed",
			}},
		},
		{name: "policy.decision.recorded", record: domain.RecordedEvent{Event: domain.PolicyDecisionRecorded{TurnID: turn, ItemID: "item-2", CallID: "call-1", Effect: "allow"}}},
		{name: "approval.requested", record: domain.RecordedEvent{Event: domain.ApprovalRequested{TurnID: turn, ItemID: "item-2", CallID: "call-1", Name: "write_file"}}},
		{name: "approval.resolved", record: domain.RecordedEvent{Event: domain.ApprovalResolved{TurnID: turn, ItemID: "item-2", Decision: "granted"}}},
	}
	seen := map[string]bool{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.record.Event != nil {
				seen[test.record.Event.EventType()] = true
			}
			assertUpdates(t, ProjectRecordedEvent("session-1", test.record), test.want)
		})
	}
	for _, eventType := range []string{
		domain.EventSessionCreated,
		domain.EventTurnStarted,
		domain.EventTurnCompleted,
		domain.EventTurnFailed,
		domain.EventTurnInterrupted,
		domain.EventSessionClosed,
		domain.EventAssistantMessageStarted,
		domain.EventAssistantMessageCompleted,
		domain.EventAssistantMessageFailed,
		domain.EventAssistantMessageInterrupted,
		domain.EventModelRequestRecorded,
		domain.EventModelUsageRecorded,
		domain.EventToolCallStarted,
		domain.EventToolCallCompleted,
		domain.EventToolCallFailed,
		domain.EventToolCallInterrupted,
		domain.EventPolicyDecisionRecorded,
		domain.EventApprovalRequested,
		domain.EventApprovalResolved,
	} {
		if !seen[eventType] {
			t.Errorf("missing domain event type %q", eventType)
		}
	}
}

func TestClipBounds(t *testing.T) {
	t.Run("chat utf8 prefix", func(t *testing.T) {
		prefix := strings.Repeat("a", maxChatTextBytes-2)
		got := clipUpdateText(prefix + "你")
		if got != prefix {
			t.Fatalf("clipUpdateText len = %d, want %d", len(got), len(prefix))
		}
		if !utf8.ValidString(got) {
			t.Fatal("clipped chat text is not valid UTF-8")
		}
	})
	t.Run("tool content marker", func(t *testing.T) {
		prefix := strings.Repeat("b", maxToolContentBytes-2)
		got := clipToolContent(prefix + "你")
		if !strings.HasPrefix(got, prefix) {
			t.Fatal("clipped tool content lost prefix")
		}
		if strings.Contains(got, "你") {
			t.Fatal("clipped tool content kept the split rune")
		}
		if !strings.HasSuffix(got, truncatedMarker) {
			t.Fatal("clipped tool content missing truncation marker")
		}
		if !utf8.ValidString(got) {
			t.Fatal("clipped tool content is not valid UTF-8")
		}
	})
	t.Run("rawInput omit invalid truncated json", func(t *testing.T) {
		arguments := `{"k":"` + strings.Repeat("a", maxToolContentBytes) + `"}`
		if rawInputValue(arguments) != nil {
			t.Fatal("oversize rawInput must be omitted")
		}
		if got := string(rawInputValue(`{"path":"NOTES.md"}`)); got != `{"path":"NOTES.md"}` {
			t.Fatalf("rawInput = %s", got)
		}
		if rawInputValue("not-json") != nil {
			t.Fatal("non-json rawInput must be omitted")
		}
		if rawInputValue(`"string"`) != nil {
			t.Fatal("json string rawInput must be omitted")
		}
	})
}

func assertUpdates(t *testing.T, got, want []any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("updates = %s, want %s", gotJSON, wantJSON)
	}
}
