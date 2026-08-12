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
