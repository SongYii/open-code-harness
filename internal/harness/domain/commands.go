package domain

type Command interface {
	CommandType() string
	TargetSessionID() SessionID
}

const (
	CommandCreateSession = "session.create"
	CommandStartTurn     = "turn.start"
	CommandCompleteTurn  = "turn.complete"
	CommandFailTurn      = "turn.fail"
	CommandInterruptTurn = "turn.interrupt"
	CommandCloseSession  = "session.close"

	CommandStartAssistantTurn     = "assistant.turn.start"
	CommandStartAssistantMessage  = "assistant.message.start"
	CommandCompleteAssistantTurn  = "assistant.turn.complete"
	CommandFailAssistantTurn      = "assistant.turn.fail"
	CommandInterruptAssistantTurn = "assistant.turn.interrupt"
	CommandRecordModelUsage       = "model.usage.record"
	InterruptionCallerCanceled    = "caller_canceled"
	InterruptionDeliveryFailed    = "runtime_delivery_failed"
	InterruptionRequestAbandoned  = "request_abandoned"
)

type CreateSession struct {
	SessionID     SessionID
	WorkspaceRoot string
}

func (CreateSession) CommandType() string          { return CommandCreateSession }
func (c CreateSession) TargetSessionID() SessionID { return c.SessionID }

type StartTurn struct {
	SessionID SessionID
	TurnID    TurnID
	Input     string
}

func (StartTurn) CommandType() string          { return CommandStartTurn }
func (c StartTurn) TargetSessionID() SessionID { return c.SessionID }

type CompleteTurn struct {
	SessionID SessionID
	TurnID    TurnID
}

func (CompleteTurn) CommandType() string          { return CommandCompleteTurn }
func (c CompleteTurn) TargetSessionID() SessionID { return c.SessionID }

type FailTurn struct {
	SessionID SessionID
	TurnID    TurnID
	Code      string
	Message   string
}

func (FailTurn) CommandType() string          { return CommandFailTurn }
func (c FailTurn) TargetSessionID() SessionID { return c.SessionID }

type InterruptTurn struct {
	SessionID SessionID
	TurnID    TurnID
	Reason    string
}

func (InterruptTurn) CommandType() string          { return CommandInterruptTurn }
func (c InterruptTurn) TargetSessionID() SessionID { return c.SessionID }

type StartAssistantMessage struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
}

type StartAssistantTurn struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	Input     string
	Request   *ModelRequestSpec
}

type ModelRequestSpec struct {
	AdapterFamily       string
	ModelID             string
	EndpointID          string
	NativeTools         string
	Images              string
	StructuredOutput    string
	ReasoningFields     string
	PromptCache         string
	ContextWindowTokens uint32
	MaxOutputTokens     uint32
	IncludeUsage        bool
	MaxTokensField      string
	Messages            []ModelPromptMessage
}

type RecordModelUsage struct {
	SessionID SessionID
	ModelUsageRecorded
}

func (RecordModelUsage) CommandType() string          { return CommandRecordModelUsage }
func (c RecordModelUsage) TargetSessionID() SessionID { return c.SessionID }

func (StartAssistantTurn) CommandType() string          { return CommandStartAssistantTurn }
func (c StartAssistantTurn) TargetSessionID() SessionID { return c.SessionID }

func (StartAssistantMessage) CommandType() string          { return CommandStartAssistantMessage }
func (c StartAssistantMessage) TargetSessionID() SessionID { return c.SessionID }

type CompleteAssistantTurn struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	Text      string
}

func (CompleteAssistantTurn) CommandType() string          { return CommandCompleteAssistantTurn }
func (c CompleteAssistantTurn) TargetSessionID() SessionID { return c.SessionID }

type FailAssistantTurn struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	Code      string
	Message   string
}

func (FailAssistantTurn) CommandType() string          { return CommandFailAssistantTurn }
func (c FailAssistantTurn) TargetSessionID() SessionID { return c.SessionID }

type InterruptAssistantTurn struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	Code      string
	Message   string
}

func (InterruptAssistantTurn) CommandType() string          { return CommandInterruptAssistantTurn }
func (c InterruptAssistantTurn) TargetSessionID() SessionID { return c.SessionID }

type CloseSession struct {
	SessionID SessionID
}

func (CloseSession) CommandType() string          { return CommandCloseSession }
func (c CloseSession) TargetSessionID() SessionID { return c.SessionID }
