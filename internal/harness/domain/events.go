package domain

type Event interface {
	EventType() string
}

const (
	EventSessionCreated  = "session.created"
	EventTurnStarted     = "turn.started"
	EventTurnCompleted   = "turn.completed"
	EventTurnFailed      = "turn.failed"
	EventTurnInterrupted = "turn.interrupted"
	EventSessionClosed   = "session.closed"
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
