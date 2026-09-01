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
	CommandDeleteSession = "session.delete"

	CommandStartAssistantTurn       = "assistant.turn.start"
	CommandStartAssistantMessage    = "assistant.message.start"
	CommandCompleteAssistantMessage = "assistant.message.complete"
	CommandCompleteAssistantTurn    = "assistant.turn.complete"
	CommandFailAssistantTurn        = "assistant.turn.fail"
	CommandInterruptAssistantTurn   = "assistant.turn.interrupt"
	CommandRecordModelUsage         = "model.usage.record"
	CommandRecordModelRequest       = "model.request.record"
	CommandStartToolCall            = "tool.call.start"
	CommandCompleteToolCall         = "tool.call.complete"
	CommandFailToolCall             = "tool.call.fail"
	CommandInterruptToolTurn        = "tool.turn.interrupt"
	CommandFailToolTurn             = "tool.turn.fail"
	CommandRecordPolicyDecision     = "policy.decision.record"
	CommandRequestApproval          = "approval.request"
	CommandResolveApproval          = "approval.resolve"
	InterruptionCallerCanceled      = "caller_canceled"
	InterruptionDeliveryFailed      = "runtime_delivery_failed"
	InterruptionRequestAbandoned    = "request_abandoned"

	CommandStartContextCompaction    = "context.compaction.start"
	CommandCompleteContextCompaction = "context.compaction.complete"
	CommandFailContextCompaction     = "context.compaction.fail"
	CommandRecordContextPreparation  = "context.preparation.record"
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
	Tools               []ToolSchema
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

type CompleteAssistantMessage struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	Text      string
	ToolCalls []ToolCallOffer
}

func (CompleteAssistantMessage) CommandType() string          { return CommandCompleteAssistantMessage }
func (c CompleteAssistantMessage) TargetSessionID() SessionID { return c.SessionID }

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

type DeleteSession struct {
	SessionID SessionID
}

func (DeleteSession) CommandType() string          { return CommandDeleteSession }
func (c DeleteSession) TargetSessionID() SessionID { return c.SessionID }

type RecordModelRequest struct {
	SessionID SessionID
	ModelRequestRecorded
}

func (RecordModelRequest) CommandType() string          { return CommandRecordModelRequest }
func (c RecordModelRequest) TargetSessionID() SessionID { return c.SessionID }

type StartToolCall struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	CallID    string
	Name      string
	Arguments string
	StepIndex uint32
}

func (StartToolCall) CommandType() string          { return CommandStartToolCall }
func (c StartToolCall) TargetSessionID() SessionID { return c.SessionID }

type CompleteToolCall struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	CallID    string
	Content   string
	Truncated bool
}

func (CompleteToolCall) CommandType() string          { return CommandCompleteToolCall }
func (c CompleteToolCall) TargetSessionID() SessionID { return c.SessionID }

type FailToolCall struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	CallID    string
	Code      string
	Message   string
}

func (FailToolCall) CommandType() string          { return CommandFailToolCall }
func (c FailToolCall) TargetSessionID() SessionID { return c.SessionID }

type InterruptToolTurn struct {
	SessionID  SessionID
	TurnID     TurnID
	ItemID     ItemID
	CallID     string
	Code       string
	Message    string
	ApprovalID ApprovalID
}

func (InterruptToolTurn) CommandType() string          { return CommandInterruptToolTurn }
func (c InterruptToolTurn) TargetSessionID() SessionID { return c.SessionID }

type FailToolTurn struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	CallID    string
	Code      string
	Message   string
}

func (FailToolTurn) CommandType() string          { return CommandFailToolTurn }
func (c FailToolTurn) TargetSessionID() SessionID { return c.SessionID }

type RecordPolicyDecision struct {
	SessionID SessionID
	TurnID    TurnID
	ItemID    ItemID
	CallID    string
	Name      string
	Effect    string
	RuleID    string
	Reason    string
}

func (RecordPolicyDecision) CommandType() string          { return CommandRecordPolicyDecision }
func (c RecordPolicyDecision) TargetSessionID() SessionID { return c.SessionID }

type RequestApproval struct {
	SessionID  SessionID
	TurnID     TurnID
	ItemID     ItemID
	ApprovalID ApprovalID
	CallID     string
	Name       string
	Reason     string
}

func (RequestApproval) CommandType() string          { return CommandRequestApproval }
func (c RequestApproval) TargetSessionID() SessionID { return c.SessionID }

type ResolveApproval struct {
	SessionID  SessionID
	TurnID     TurnID
	ItemID     ItemID
	ApprovalID ApprovalID
	Decision   string
}

func (ResolveApproval) CommandType() string          { return CommandResolveApproval }
func (c ResolveApproval) TargetSessionID() SessionID { return c.SessionID }

type StartContextCompaction struct {
	SessionID SessionID
	ContextCompactionStarted
}

func (StartContextCompaction) CommandType() string          { return CommandStartContextCompaction }
func (c StartContextCompaction) TargetSessionID() SessionID { return c.SessionID }

type CompleteContextCompaction struct {
	SessionID SessionID
	ContextCompactionCompleted
}

func (CompleteContextCompaction) CommandType() string          { return CommandCompleteContextCompaction }
func (c CompleteContextCompaction) TargetSessionID() SessionID { return c.SessionID }

type FailContextCompaction struct {
	SessionID SessionID
	ContextCompactionFailed
}

func (FailContextCompaction) CommandType() string          { return CommandFailContextCompaction }
func (c FailContextCompaction) TargetSessionID() SessionID { return c.SessionID }

type RecordContextPreparation struct {
	SessionID SessionID
	ContextPreparedRecorded
}

func (RecordContextPreparation) CommandType() string          { return CommandRecordContextPreparation }
func (c RecordContextPreparation) TargetSessionID() SessionID { return c.SessionID }
