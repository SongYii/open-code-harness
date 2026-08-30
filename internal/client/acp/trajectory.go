package acp

import (
	"encoding/json"
	"fmt"
)

// ToolCallStatus mirrors this project's own agent's tool_call and
// tool_call_update status values (internal/harness/adapters/acp/project.go,
// read directly): "pending" (created), "in_progress", "completed", or
// "failed". A different agent's unrecognized status string is stored as-is
// rather than rejected — only the sessionUpdate variant and an unknown
// toolCallId are treated as anomalies (see RenderAnomaly).
type ToolCallStatus string

const (
	ToolCallPending    ToolCallStatus = "pending"
	ToolCallInProgress ToolCallStatus = "in_progress"
	ToolCallCompleted  ToolCallStatus = "completed"
	ToolCallFailed     ToolCallStatus = "failed"
)

// ToolCall is one tracked tool call's accumulated state, keyed by its
// toolCallId.
type ToolCall struct {
	ID     string
	Title  string
	Kind   string
	Status ToolCallStatus
}

// RenderEventKind discriminates what Trajectory.Apply produced.
type RenderEventKind int

const (
	// RenderMessageChunk is one streamed chunk of user or agent text
	// (RenderEvent.FromUser distinguishes which).
	RenderMessageChunk RenderEventKind = iota
	// RenderToolCall is a newly created tool call.
	RenderToolCall
	// RenderToolCallUpdate is a mutation of an existing tool call.
	RenderToolCallUpdate
	// RenderAnomaly is an unrecognized sessionUpdate variant (a future
	// version of this project's own agent, or a different agent
	// entirely), a tool_call_update naming a toolCallId Apply has not
	// seen created, or a session/update whose params or update object
	// does not parse at all. None of these are a reason to panic or
	// silently drop the data — a caller can render RenderAnomaly as a
	// labeled, raw fallback line.
	RenderAnomaly
)

// RenderEvent is what Trajectory.Apply returns for a caller to render.
type RenderEvent struct {
	Kind RenderEventKind

	// Set when Kind is RenderMessageChunk.
	FromUser bool
	Text     string

	// Set when Kind is RenderToolCall or RenderToolCallUpdate: the tool
	// call's full state after this update was applied.
	Tool ToolCall

	// Set when Kind is RenderAnomaly.
	SessionUpdate string          // the raw "sessionUpdate" string, "" if that field itself did not parse
	Detail        string          // a short, human-readable reason
	Raw           json.RawMessage // the raw update object (or, if that itself failed to parse, the raw params), for a caller that wants to log it verbatim
}

// Trajectory reduces a stream of session/update notifications into
// per-toolCallId state. It is not safe for concurrent use: Apply is meant
// to be called only from the single goroutine a Handler's
// HandleSessionUpdate runs on — Connection's own read loop dispatches
// notifications inline, in order, never concurrently with itself.
type Trajectory struct {
	calls map[string]*ToolCall
}

// NewTrajectory returns an empty Trajectory.
func NewTrajectory() *Trajectory {
	return &Trajectory{calls: make(map[string]*ToolCall)}
}

type sessionUpdateEnvelope struct {
	Update json.RawMessage `json:"update"`
}

type sessionUpdateKind struct {
	SessionUpdate string `json:"sessionUpdate"`
}

type messageChunkUpdate struct {
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
}

type toolCallFields struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
}

// Apply reduces one session/update notification's raw params — the full
// {"sessionId":...,"update":{...}} envelope Handler.HandleSessionUpdate
// receives verbatim — into exactly one RenderEvent. It never panics and
// never silently drops an update it does not understand.
func (t *Trajectory) Apply(params json.RawMessage) RenderEvent {
	var envelope sessionUpdateEnvelope
	if err := json.Unmarshal(params, &envelope); err != nil || len(envelope.Update) == 0 {
		return RenderEvent{Kind: RenderAnomaly, Detail: fmt.Sprintf("session/update params missing a usable update object: %v", err), Raw: params}
	}

	var kind sessionUpdateKind
	if err := json.Unmarshal(envelope.Update, &kind); err != nil {
		return RenderEvent{Kind: RenderAnomaly, Detail: fmt.Sprintf("update object does not carry a sessionUpdate string: %v", err), Raw: envelope.Update}
	}

	switch kind.SessionUpdate {
	case "user_message_chunk", "agent_message_chunk":
		return t.applyMessageChunk(kind.SessionUpdate, envelope.Update)
	case "tool_call":
		return t.applyToolCall(envelope.Update)
	case "tool_call_update":
		return t.applyToolCallUpdate(envelope.Update)
	default:
		return t.anomaly(kind.SessionUpdate, "unrecognized sessionUpdate variant", envelope.Update)
	}
}

func (t *Trajectory) applyMessageChunk(sessionUpdate string, raw json.RawMessage) RenderEvent {
	var chunk messageChunkUpdate
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return t.anomaly(sessionUpdate, "malformed message chunk", raw)
	}
	return RenderEvent{Kind: RenderMessageChunk, FromUser: sessionUpdate == "user_message_chunk", Text: chunk.Content.Text}
}

func (t *Trajectory) applyToolCall(raw json.RawMessage) RenderEvent {
	var fields toolCallFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		return t.anomaly("tool_call", "malformed tool_call", raw)
	}
	call := &ToolCall{ID: fields.ToolCallID, Title: fields.Title, Kind: fields.Kind, Status: ToolCallStatus(fields.Status)}
	t.calls[fields.ToolCallID] = call
	return RenderEvent{Kind: RenderToolCall, Tool: *call}
}

func (t *Trajectory) applyToolCallUpdate(raw json.RawMessage) RenderEvent {
	var fields toolCallFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		return t.anomaly("tool_call_update", "malformed tool_call_update", raw)
	}
	call, ok := t.calls[fields.ToolCallID]
	if !ok {
		return t.anomaly("tool_call_update", fmt.Sprintf("unknown toolCallId %q", fields.ToolCallID), raw)
	}
	if fields.Status != "" {
		call.Status = ToolCallStatus(fields.Status)
	}
	if fields.Title != "" {
		call.Title = fields.Title
	}
	if fields.Kind != "" {
		call.Kind = fields.Kind
	}
	return RenderEvent{Kind: RenderToolCallUpdate, Tool: *call}
}

func (t *Trajectory) anomaly(sessionUpdate, detail string, raw json.RawMessage) RenderEvent {
	return RenderEvent{Kind: RenderAnomaly, SessionUpdate: sessionUpdate, Detail: detail, Raw: raw}
}
