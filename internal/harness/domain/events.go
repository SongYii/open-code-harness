package domain

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
)

const (
	PromptRoleSystem    = "system"
	PromptRoleUser      = "user"
	PromptRoleAssistant = "assistant"

	FinishReasonStop    = "stop"
	FinishReasonLength  = "length"
	FinishReasonUnknown = "unknown"
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
	TurnID TurnID `json:"turnID"`
	ItemID ItemID `json:"itemID"`
	Text   string `json:"text"`
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

type ModelPromptMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
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
