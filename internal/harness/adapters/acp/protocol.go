package acp

import "encoding/json"

const protocolVersion = 1

const (
	methodInitialize        = "initialize"
	methodSessionNew        = "session/new"
	methodSessionLoad       = "session/load"
	methodSessionPrompt     = "session/prompt"
	methodSessionCancel     = "session/cancel"
	methodSessionUpdate     = "session/update"
	methodRequestPermission = "session/request_permission"
	optionAllowOnce         = "allow-once"
	optionRejectOnce        = "reject-once"
	stopReasonEndTurn       = "end_turn"
	stopReasonCancelled     = "cancelled"
	promptFailedMessage     = "session prompt failed"
	promptInFlightMessage   = "a prompt is already in flight for this session"
	agentName               = "open-code-harness"
	agentVersion            = "0.0.0"
)

type initializeParams struct {
	ProtocolVersion json.RawMessage `json:"protocolVersion"`
}

type initializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities agentCapabilities `json:"agentCapabilities"`
	AgentInfo         agentInfo         `json:"agentInfo"`
	AuthMethods       []struct{}        `json:"authMethods"`
}

type agentCapabilities struct {
	LoadSession        bool               `json:"loadSession"`
	PromptCapabilities promptCapabilities `json:"promptCapabilities"`
}

type promptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

type agentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type sessionNewParams struct {
	Cwd string `json:"cwd"`
}

type sessionIDParams struct {
	SessionID string `json:"sessionId"`
}

type promptParams struct {
	SessionID string        `json:"sessionId"`
	Prompt    []promptBlock `json:"prompt"`
}

type promptBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
}

type sessionUpdateParams struct {
	SessionID string `json:"sessionId"`
	Update    any    `json:"update"`
}

type agentMessageChunk struct {
	SessionUpdate string      `json:"sessionUpdate"`
	Content       textContent `json:"content"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallUpdate struct {
	SessionUpdate string            `json:"sessionUpdate"`
	ToolCallID    string            `json:"toolCallId"`
	Title         string            `json:"title,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Status        string            `json:"status,omitempty"`
	Content       []toolCallContent `json:"content,omitempty"`
	RawInput      json.RawMessage   `json:"rawInput,omitempty"`
}

type toolCallContent struct {
	Type    string      `json:"type"`
	Content textContent `json:"content"`
}

type permissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  permissionToolCall `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

type permissionToolCall struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type permissionResult struct {
	Outcome permissionOutcome `json:"outcome"`
}

type permissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}
