package acp

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

const (
	maxChatTextBytes    = 768 << 10
	maxToolContentBytes = 16 << 10
	truncatedMarker     = "\n[truncated]"
)

// LiveTool is the outstanding tool of an in-flight prompt.
type LiveTool struct {
	TurnID domain.TurnID
	CallID string
	Name   string
}

func ToolCallID(turnID domain.TurnID, callID string) string {
	return string(turnID) + "/" + callID
}

func ToolKind(name string) string {
	switch name {
	case "read_file", "list_dir":
		return "read"
	case "write_file":
		return "edit"
	case "exec":
		return "execute"
	default:
		return "other"
	}
}

func ProjectRuntimeEvent(sessionID string, event engine.RuntimeEvent, live LiveTool) []any {
	live = resolveLiveTool(event, live)
	switch event.Type {
	case engine.RuntimeModelTextDelta:
		if event.Text == "" {
			return nil
		}
		return []any{chatChunk(sessionID, "agent_message_chunk", event.Text)}
	case engine.RuntimeModelToolCall:
		return []any{toolCallUpdate{
			SessionUpdate: "tool_call",
			ToolCallID:    ToolCallID(live.TurnID, live.CallID),
			Title:         live.Name,
			Kind:          ToolKind(live.Name),
			Status:        "pending",
		}}
	case engine.RuntimeToolExecutionStarted:
		return []any{toolCallUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    ToolCallID(live.TurnID, live.CallID),
			Status:        "in_progress",
		}}
	case engine.RuntimeToolExecutionCompleted:
		return []any{toolCallUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    ToolCallID(live.TurnID, live.CallID),
			Status:        "completed",
		}}
	case engine.RuntimeToolExecutionFailed:
		return []any{toolCallUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    ToolCallID(live.TurnID, live.CallID),
			Status:        "failed",
		}}
	default:
		return nil
	}
}

func ProjectRecordedEvent(sessionID string, record domain.RecordedEvent) []any {
	switch event := record.Event.(type) {
	case domain.TurnStarted:
		if event.Input == "" {
			return nil
		}
		return []any{chatChunk(sessionID, "user_message_chunk", event.Input)}
	case domain.AssistantMessageCompleted:
		if event.Text == "" {
			return nil
		}
		return []any{chatChunk(sessionID, "agent_message_chunk", event.Text)}
	case domain.AssistantMessageFailed:
		if event.Message == "" {
			return nil
		}
		return []any{chatChunk(sessionID, "agent_message_chunk", event.Message)}
	case domain.AssistantMessageInterrupted:
		if event.Message == "" {
			return nil
		}
		return []any{chatChunk(sessionID, "agent_message_chunk", event.Message)}
	case domain.ToolCallStarted:
		return []any{startedToolCall(sessionID, event)}
	case domain.ToolCallCompleted:
		return []any{toolCallUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    ToolCallID(event.TurnID, event.CallID),
			Status:        "completed",
			Content:       toolTextContent(sessionID, ToolCallID(event.TurnID, event.CallID), "completed", event.Content),
		}}
	case domain.ToolCallFailed:
		return []any{toolCallUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    ToolCallID(event.TurnID, event.CallID),
			Status:        "failed",
			Content:       toolTextContent(sessionID, ToolCallID(event.TurnID, event.CallID), "failed", event.Message),
		}}
	case domain.ToolCallInterrupted:
		return []any{toolCallUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    ToolCallID(event.TurnID, event.CallID),
			Status:        "failed",
		}}
	default:
		return nil
	}
}

func resolveLiveTool(event engine.RuntimeEvent, live LiveTool) LiveTool {
	if event.Text == "" {
		return live
	}
	name, callID := splitToolText(event.Text)
	resolved := LiveTool{TurnID: event.TurnID, CallID: callID, Name: name}
	if resolved.TurnID == "" {
		resolved.TurnID = live.TurnID
	}
	if resolved.CallID == "" {
		resolved.CallID = live.CallID
	}
	if resolved.Name == "" {
		resolved.Name = live.Name
	}
	return resolved
}

func splitToolText(text string) (name, callID string) {
	index := strings.LastIndex(text, ":")
	if index < 0 {
		return text, ""
	}
	return text[:index], text[index+1:]
}

func chatChunk(sessionID, sessionUpdate, text string) agentMessageChunk {
	text = clipUTF8Prefix(text, maxChatTextBytes)
	makeUpdate := func(s string) any {
		return agentMessageChunk{SessionUpdate: sessionUpdate, Content: textContent{Type: "text", Text: s}}
	}
	return makeUpdate(shrinkUntilFrameFits(sessionID, text, makeUpdate)).(agentMessageChunk)
}

func startedToolCall(sessionID string, event domain.ToolCallStarted) toolCallUpdate {
	update := toolCallUpdate{
		SessionUpdate: "tool_call",
		ToolCallID:    ToolCallID(event.TurnID, event.CallID),
		Title:         event.Name,
		Kind:          ToolKind(event.Name),
		Status:        "in_progress",
		RawInput:      rawInputValue(event.Arguments),
	}
	if !frameFits(sessionID, update) {
		update.RawInput = nil
	}
	return update
}

func toolTextContent(sessionID, toolCallID, status, text string) []toolCallContent {
	if text == "" {
		return nil
	}
	clipped := clipUTF8Prefix(text, maxToolContentBytes)
	if clipped != text && !strings.HasSuffix(clipped, truncatedMarker) {
		clipped += truncatedMarker
	}
	makeUpdate := func(s string) any {
		return toolCallUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    toolCallID,
			Status:        status,
			Content:       []toolCallContent{{Type: "content", Content: textContent{Type: "text", Text: s}}},
		}
	}
	clipped = shrinkUntilFrameFits(sessionID, clipped, makeUpdate)
	if clipped != text && !strings.HasSuffix(clipped, truncatedMarker) {
		marked := clipUTF8Prefix(clipped, max(0, len(clipped)-len(truncatedMarker))) + truncatedMarker
		clipped = shrinkUntilFrameFits(sessionID, marked, makeUpdate)
	}
	return []toolCallContent{{Type: "content", Content: textContent{Type: "text", Text: clipped}}}
}

func shrinkUntilFrameFits(sessionID, text string, makeUpdate func(string) any) string {
	if frameFits(sessionID, makeUpdate(text)) {
		return text
	}
	low, high := 0, len(text)
	best := ""
	for low <= high {
		mid := low + (high-low)/2
		candidate := clipUTF8Prefix(text, mid)
		if frameFits(sessionID, makeUpdate(candidate)) {
			best = candidate
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return best
}

func frameFits(sessionID string, update any) bool {
	payload, err := marshalSessionUpdate(sessionID, update)
	return err == nil && len(payload)+1 <= maxFrameBytes
}

func marshalSessionUpdate(sessionID string, update any) ([]byte, error) {
	params, err := json.Marshal(sessionUpdateParams{SessionID: sessionID, Update: update})
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{JSONRPC: jsonRPCVersion, Method: methodSessionUpdate, Params: params})
}

func clipUTF8Prefix(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if limit <= 0 {
		return ""
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func rawInputValue(arguments string) json.RawMessage {
	compact := bytes.TrimSpace([]byte(arguments))
	if len(compact) == 0 || compact[0] != '{' && compact[0] != '[' {
		return nil
	}
	if !json.Valid(compact) {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, compact); err != nil {
		return nil
	}
	encoded := buf.Bytes()
	if len(encoded) <= maxToolContentBytes {
		return append(json.RawMessage(nil), encoded...)
	}
	limit := maxToolContentBytes
	for limit > 0 && !utf8.RuneStart(encoded[limit]) {
		limit--
	}
	clipped := encoded[:limit]
	if !json.Valid(clipped) {
		return nil
	}
	return append(json.RawMessage(nil), clipped...)
}
