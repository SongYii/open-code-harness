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
		got := chatChunk("session-1", "agent_message_chunk", prefix+"你")
		if got.Content.Text != prefix {
			t.Fatalf("chat text len = %d, want %d", len(got.Content.Text), len(prefix))
		}
		if !utf8.ValidString(got.Content.Text) {
			t.Fatal("clipped chat text is not valid UTF-8")
		}
	})
	t.Run("tool content marker", func(t *testing.T) {
		prefix := strings.Repeat("b", maxToolContentBytes-2)
		got := toolTextContent("session-1", "turn-1/call-1", "completed", prefix+"你")
		if len(got) != 1 {
			t.Fatalf("tool content blocks = %d, want 1", len(got))
		}
		text := got[0].Content.Text
		if !strings.HasPrefix(text, prefix) {
			t.Fatal("clipped tool content lost prefix")
		}
		if strings.Contains(text, "你") {
			t.Fatal("clipped tool content kept the split rune")
		}
		if !strings.HasSuffix(text, truncatedMarker) {
			t.Fatal("clipped tool content missing truncation marker")
		}
		if !utf8.ValidString(text) {
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

func TestOversizeToolIdentityDegradesToSendableFrame(t *testing.T) {
	sessionID := "session-1"
	hugeName := strings.Repeat("x", maxFrameBytes)
	hugeCallID := strings.Repeat("c", maxFrameBytes)
	correlation := engine.Correlation{TurnID: "turn-1"}

	t.Run("live name clips title keeps id", func(t *testing.T) {
		updates := ProjectRuntimeEvent(sessionID, engine.RuntimeEvent{
			Correlation: correlation,
			Type:        engine.RuntimeModelToolCall,
			Text:        hugeName + ":call-1",
		}, LiveTool{})
		if len(updates) != 1 {
			t.Fatalf("updates = %d, want 1", len(updates))
		}
		card, ok := updates[0].(toolCallUpdate)
		if !ok {
			t.Fatalf("update type %T", updates[0])
		}
		if card.ToolCallID != "turn-1/call-1" {
			t.Fatalf("toolCallId = %q", card.ToolCallID)
		}
		if card.Kind != "other" {
			t.Fatalf("kind = %q", card.Kind)
		}
		if card.Title == "" || len(card.Title) >= len(hugeName) || !strings.HasPrefix(hugeName, card.Title) {
			t.Fatalf("title len = %d, want clipped prefix", len(card.Title))
		}
		requireSendable(t, sessionID, updates)
	})

	t.Run("live callID omitted", func(t *testing.T) {
		updates := ProjectRuntimeEvent(sessionID, engine.RuntimeEvent{
			Correlation: correlation,
			Type:        engine.RuntimeModelToolCall,
			Text:        "read_file:" + hugeCallID,
		}, LiveTool{})
		if len(updates) != 0 {
			t.Fatalf("updates = %d, want omitted", len(updates))
		}
		started := ProjectRuntimeEvent(sessionID, engine.RuntimeEvent{
			Correlation: correlation,
			Type:        engine.RuntimeToolExecutionStarted,
			Text:        "read_file:" + hugeCallID,
		}, LiveTool{})
		if len(started) != 0 {
			t.Fatalf("started updates = %d, want omitted", len(started))
		}
	})

	t.Run("load name clips title keeps id", func(t *testing.T) {
		updates := ProjectRecordedEvent(sessionID, domain.RecordedEvent{
			Event: domain.ToolCallStarted{
				TurnID: "turn-1", ItemID: "item-1", CallID: "call-1",
				Name: hugeName, Arguments: `{"path":"NOTES.md"}`, StepIndex: 1,
			},
		})
		if len(updates) != 1 {
			t.Fatalf("updates = %d, want 1", len(updates))
		}
		card, ok := updates[0].(toolCallUpdate)
		if !ok {
			t.Fatalf("update type %T", updates[0])
		}
		if card.ToolCallID != "turn-1/call-1" {
			t.Fatalf("toolCallId = %q", card.ToolCallID)
		}
		if card.RawInput != nil {
			t.Fatal("oversize title must drop rawInput before clipping")
		}
		if card.Title == "" || len(card.Title) >= len(hugeName) || !strings.HasPrefix(hugeName, card.Title) {
			t.Fatalf("title len = %d, want clipped prefix", len(card.Title))
		}
		requireSendable(t, sessionID, updates)
	})

	t.Run("load callID omitted", func(t *testing.T) {
		started := ProjectRecordedEvent(sessionID, domain.RecordedEvent{
			Event: domain.ToolCallStarted{
				TurnID: "turn-1", ItemID: "item-1", CallID: hugeCallID,
				Name: "read_file", Arguments: `{"path":"a"}`, StepIndex: 1,
			},
		})
		if len(started) != 0 {
			t.Fatalf("started updates = %d, want omitted", len(started))
		}
		completed := ProjectRecordedEvent(sessionID, domain.RecordedEvent{
			Event: domain.ToolCallCompleted{
				TurnID: "turn-1", ItemID: "item-1", CallID: hugeCallID, Content: "ok",
			},
		})
		if len(completed) != 0 {
			t.Fatalf("completed updates = %d, want omitted", len(completed))
		}
	})

	t.Run("load skips huge id and still projects later chat", func(t *testing.T) {
		var updates []any
		for _, record := range []domain.RecordedEvent{
			{Event: domain.TurnStarted{TurnID: "turn-1", Input: "hello"}},
			{Event: domain.ToolCallStarted{TurnID: "turn-1", ItemID: "item-1", CallID: hugeCallID, Name: "read_file", Arguments: `{}`, StepIndex: 1}},
			{Event: domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-2", Text: "world"}},
		} {
			updates = append(updates, ProjectRecordedEvent(sessionID, record)...)
		}
		assertUpdates(t, updates, []any{
			agentMessageChunk{SessionUpdate: "user_message_chunk", Content: textContent{Type: "text", Text: "hello"}},
			agentMessageChunk{SessionUpdate: "agent_message_chunk", Content: textContent{Type: "text", Text: "world"}},
		})
		requireSendable(t, sessionID, updates)
	})

	t.Run("permission name clips title keeps id", func(t *testing.T) {
		params, ok := fitPermission(json.RawMessage("1"), permissionParams{
			SessionID: sessionID,
			ToolCall:  permissionToolCall{ToolCallID: "turn-1/call-1", Title: hugeName, Kind: "other", Status: "pending"},
			Options: []permissionOption{
				{OptionID: optionAllowOnce, Name: "Allow once", Kind: "allow_once"},
			},
		})
		if !ok {
			t.Fatal("permission with oversize title must still fit after clip")
		}
		if params.ToolCall.ToolCallID != "turn-1/call-1" {
			t.Fatalf("toolCallId = %q", params.ToolCall.ToolCallID)
		}
		if params.ToolCall.Title == "" || len(params.ToolCall.Title) >= len(hugeName) || !strings.HasPrefix(hugeName, params.ToolCall.Title) {
			t.Fatalf("permission title len = %d, want clipped prefix", len(params.ToolCall.Title))
		}
		if !permissionFrameFits(json.RawMessage("1"), params) {
			t.Fatal("clipped permission frame does not fit")
		}
	})

	t.Run("permission callID omitted", func(t *testing.T) {
		_, ok := fitPermission(json.RawMessage("1"), permissionParams{
			SessionID: sessionID,
			ToolCall:  permissionToolCall{ToolCallID: ToolCallID("turn-1", hugeCallID), Title: "write_file", Kind: "edit", Status: "pending"},
		})
		if ok {
			t.Fatal("permission with oversize toolCallId must not be sendable")
		}
	})
}

func requireSendable(t *testing.T, sessionID string, updates []any) {
	t.Helper()
	for index, update := range updates {
		if !frameFits(sessionID, update) {
			t.Fatalf("update %d encoded frame exceeds %d", index, maxFrameBytes)
		}
	}
}

func TestOutgoingFrameFitsAfterJSONEscaping(t *testing.T) {
	sessionID := "session-1"
	cases := []struct {
		name string
		text string
	}{
		{name: "newlines", text: strings.Repeat("\n", maxChatTextBytes)},
		{name: "html-escaped", text: strings.Repeat("<", maxChatTextBytes)},
		{name: "controls", text: strings.Repeat("\x01", maxChatTextBytes)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			updates := ProjectRecordedEvent(sessionID, domain.RecordedEvent{
				Event: domain.TurnStarted{TurnID: "turn-1", Input: test.text},
			})
			if len(updates) != 1 {
				t.Fatalf("updates = %d, want 1", len(updates))
			}
			payload, err := marshalSessionUpdate(sessionID, updates[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(payload)+1 > maxFrameBytes {
				t.Fatalf("encoded frame %d exceeds %d", len(payload)+1, maxFrameBytes)
			}
			chunk, ok := updates[0].(agentMessageChunk)
			if !ok {
				t.Fatalf("update type %T", updates[0])
			}
			if !utf8.ValidString(chunk.Content.Text) {
				t.Fatal("clipped text is not valid UTF-8")
			}
			if len(chunk.Content.Text) >= len(test.text) {
				t.Fatal("escaping payload was not shrunk below the raw-byte clip")
			}
		})
	}
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
