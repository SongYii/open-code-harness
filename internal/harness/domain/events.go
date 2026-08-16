package domain

import "encoding/json"

type Event interface {
	EventType() string
}

const (
	EventSessionCreated              = "session.created"
	EventTurnStarted                 = "turn.started"
	EventTurnCompleted               = "turn.completed"
	EventTurnFailed                  = "turn.failed"
	EventTurnInterrupted             = "turn.interrupted"
	EventSessionClosed               = "session.closed"
	EventAssistantMessageStarted     = "assistant.message.started"
	EventAssistantMessageCompleted   = "assistant.message.completed"
	EventAssistantMessageFailed      = "assistant.message.failed"
	EventAssistantMessageInterrupted = "assistant.message.interrupted"
	EventModelRequestRecorded        = "model.request.recorded"
	EventModelUsageRecorded          = "model.usage.recorded"
	EventToolCallStarted             = "tool.call.started"
	EventToolCallCompleted           = "tool.call.completed"
	EventToolCallFailed              = "tool.call.failed"
	EventToolCallInterrupted         = "tool.call.interrupted"
	EventPolicyDecisionRecorded      = "policy.decision.recorded"
	EventApprovalRequested           = "approval.requested"
	EventApprovalResolved            = "approval.resolved"
)

const (
	PromptRoleSystem    = "system"
	PromptRoleUser      = "user"
	PromptRoleAssistant = "assistant"
	PromptRoleTool      = "tool"

	FinishReasonStop      = "stop"
	FinishReasonLength    = "length"
	FinishReasonUnknown   = "unknown"
	FinishReasonToolCalls = "tool_calls"

	PolicyEffectAllow           = "allow"
	PolicyEffectDeny            = "deny"
	PolicyEffectRequireApproval = "require_approval"

	ApprovalDecisionGranted  = "granted"
	ApprovalDecisionDenied   = "denied"
	ApprovalDecisionTimeout  = "timeout"
	ApprovalDecisionCanceled = "canceled"
)

type SessionCreated struct {
	WorkspaceRoot string `json:"workspaceRoot"`
}

func (SessionCreated) EventType() string { return EventSessionCreated }

type TurnStarted struct {
	TurnID TurnID `json:"turnID"`
	Input  string `json:"input"`
}

func (TurnStarted) EventType() string { return EventTurnStarted }

type TurnCompleted struct {
	TurnID TurnID `json:"turnID"`
}

func (TurnCompleted) EventType() string { return EventTurnCompleted }

type TurnFailed struct {
	TurnID  TurnID `json:"turnID"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (TurnFailed) EventType() string { return EventTurnFailed }

type TurnInterrupted struct {
	TurnID TurnID `json:"turnID"`
	Reason string `json:"reason"`
}

func (TurnInterrupted) EventType() string { return EventTurnInterrupted }

type SessionClosed struct{}

func (SessionClosed) EventType() string { return EventSessionClosed }

type AssistantMessageStarted struct {
	TurnID TurnID `json:"turnID"`
	ItemID ItemID `json:"itemID"`
}

func (AssistantMessageStarted) EventType() string { return EventAssistantMessageStarted }

type AssistantMessageCompleted struct {
	TurnID    TurnID          `json:"turnID"`
	ItemID    ItemID          `json:"itemID"`
	Text      string          `json:"text"`
	ToolCalls []ToolCallOffer `json:"toolCalls,omitempty"`
}

func (AssistantMessageCompleted) EventType() string { return EventAssistantMessageCompleted }

type AssistantMessageFailed struct {
	TurnID  TurnID `json:"turnID"`
	ItemID  ItemID `json:"itemID"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (AssistantMessageFailed) EventType() string { return EventAssistantMessageFailed }

type AssistantMessageInterrupted struct {
	TurnID  TurnID `json:"turnID"`
	ItemID  ItemID `json:"itemID"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (AssistantMessageInterrupted) EventType() string { return EventAssistantMessageInterrupted }

type ToolCallOffer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type RiskClass string

const (
	RiskRead    RiskClass = "read"
	RiskWrite   RiskClass = "write"
	RiskExec    RiskClass = "exec"
	RiskNetwork RiskClass = "network"
)

type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Source      string
	Risk        RiskClass
	Mutates     bool
}

type ModelPromptMessage struct {
	Role       string          `json:"role"`
	Text       string          `json:"text"`
	ToolCalls  []ToolCallOffer `json:"toolCalls,omitempty"`
	ToolCallID string          `json:"toolCallID,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type ModelRequestRecorded struct {
	TurnID              TurnID               `json:"turnID"`
	ItemID              ItemID               `json:"itemID"`
	AdapterFamily       string               `json:"adapterFamily"`
	ModelID             string               `json:"modelID"`
	EndpointID          string               `json:"endpointID"`
	NativeTools         string               `json:"nativeTools"`
	Images              string               `json:"images"`
	StructuredOutput    string               `json:"structuredOutput"`
	ReasoningFields     string               `json:"reasoningFields"`
	PromptCache         string               `json:"promptCache"`
	ContextWindowTokens uint32               `json:"contextWindowTokens"`
	MaxOutputTokens     uint32               `json:"maxOutputTokens"`
	IncludeUsage        bool                 `json:"includeUsage"`
	MaxTokensField      string               `json:"maxTokensField"`
	Messages            []ModelPromptMessage `json:"messages"`
	Tools               []ToolSchema         `json:"tools,omitempty"`
}

func (ModelRequestRecorded) EventType() string { return EventModelRequestRecorded }

type ModelUsageRecorded struct {
	TurnID            TurnID `json:"turnID"`
	ItemID            ItemID `json:"itemID"`
	InputTokens       uint64 `json:"inputTokens"`
	OutputTokens      uint64 `json:"outputTokens"`
	CachedInputTokens uint64 `json:"cachedInputTokens"`
	LatencyMs         uint64 `json:"latencyMs"`
	FinishReason      string `json:"finishReason"`
	ProviderRequestID string `json:"providerRequestID"`
}

func (ModelUsageRecorded) EventType() string { return EventModelUsageRecorded }

type ToolCallStarted struct {
	TurnID    TurnID `json:"turnID"`
	ItemID    ItemID `json:"itemID"`
	CallID    string `json:"callID"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	StepIndex uint32 `json:"stepIndex"`
}

func (ToolCallStarted) EventType() string { return EventToolCallStarted }

type ToolCallCompleted struct {
	TurnID    TurnID `json:"turnID"`
	ItemID    ItemID `json:"itemID"`
	CallID    string `json:"callID"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

func (ToolCallCompleted) EventType() string { return EventToolCallCompleted }

type ToolCallFailed struct {
	TurnID  TurnID `json:"turnID"`
	ItemID  ItemID `json:"itemID"`
	CallID  string `json:"callID"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (ToolCallFailed) EventType() string { return EventToolCallFailed }

type ToolCallInterrupted struct {
	TurnID  TurnID `json:"turnID"`
	ItemID  ItemID `json:"itemID"`
	CallID  string `json:"callID"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (ToolCallInterrupted) EventType() string { return EventToolCallInterrupted }

type PolicyDecisionRecorded struct {
	TurnID TurnID `json:"turnID"`
	ItemID ItemID `json:"itemID"`
	CallID string `json:"callID"`
	Name   string `json:"name"`
	Effect string `json:"effect"`
	RuleID string `json:"ruleID"`
	Reason string `json:"reason"`
}

func (PolicyDecisionRecorded) EventType() string { return EventPolicyDecisionRecorded }

type ApprovalRequested struct {
	TurnID     TurnID     `json:"turnID"`
	ItemID     ItemID     `json:"itemID"`
	ApprovalID ApprovalID `json:"approvalID"`
	CallID     string     `json:"callID"`
	Name       string     `json:"name"`
	Reason     string     `json:"reason"`
}

func (ApprovalRequested) EventType() string { return EventApprovalRequested }

type ApprovalResolved struct {
	TurnID     TurnID     `json:"turnID"`
	ItemID     ItemID     `json:"itemID"`
	ApprovalID ApprovalID `json:"approvalID"`
	Decision   string     `json:"decision"`
}

func (ApprovalResolved) EventType() string { return EventApprovalResolved }
