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
	EventSessionDeleted              = "session.deleted"
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

	EventContextCompactionStarted   = "context.compaction.started"
	EventContextCompactionCompleted = "context.compaction.completed"
	EventContextCompactionFailed    = "context.compaction.failed"
	EventContextPreparedRecorded    = "context.prepared"
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

	// Context compaction triggers (design §15). PreTurn and Manual start
	// require no active Turn; MidTurn and OverflowRetry require one.
	ContextTriggerPreTurn       = "pre_turn"
	ContextTriggerManual        = "manual"
	ContextTriggerMidTurn       = "mid_turn"
	ContextTriggerOverflowRetry = "overflow_retry"

	// Context compaction strategies (design §11/§12).
	ContextStrategySummary = "summary"
	ContextStrategyReset   = "reset"

	// Context checkpoint kinds (design §7.3), duplicated here as plain
	// strings rather than importing internal/harness/contextengine's
	// CheckpointKind type: domain may not import contextengine, since
	// contextengine itself imports domain (CE-01).
	ContextCheckpointKindRollingSummary  = "rolling_summary_v1"
	ContextCheckpointKindSourceTailReset = "source_tail_reset_v1"

	// ModelRequestPurpose values (design §6.3). Empty is treated as
	// ModelRequestPurposeConversation for backward compatibility with
	// every ModelRequestRecorded constructed before this field existed.
	ModelRequestPurposeConversation = "conversation"
	ModelRequestPurposeCompaction   = "compaction"
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

type SessionDeleted struct{}

func (SessionDeleted) EventType() string { return EventSessionDeleted }

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
	// Purpose distinguishes a conversation attempt from a Context Engine
	// summarization attempt (design §6.3). Empty is equivalent to
	// ModelRequestPurposeConversation, so every ModelRequestRecorded
	// constructed before this field existed remains valid.
	Purpose string `json:"purpose,omitempty"`
	// AttemptIndex is 1-based, matching this project's existing StepIndex
	// convention; zero (the Go zero value) means "not yet assigned by a
	// Context Engine-aware caller," not "first attempt." It exists so an
	// overflow attempt and its retry (design §15.3) are never conflated.
	AttemptIndex uint32 `json:"attemptIndex,omitempty"`
	// ContextDecisionID names the ContextPreparedRecorded evidence this
	// request's envelope came from, when one exists (design §7.4). Empty
	// for a request built without going through the Context Engine.
	ContextDecisionID ContextDecisionID `json:"contextDecisionID,omitempty"`
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
	// AttemptIndex mirrors ModelRequestRecorded.AttemptIndex so a usage
	// fact can be paired back to the exact request that produced it, even
	// across an overflow retry within the same Turn/Item.
	AttemptIndex uint32 `json:"attemptIndex,omitempty"`
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

// ContextCheckpointRecord is domain's own durable representation of a
// Context Engine checkpoint (design §7.3), embedded in
// ContextCompactionCompleted. It deliberately duplicates the shape of
// internal/harness/contextengine.ContextCheckpoint as plain fields rather
// than importing that type: domain may not import contextengine, since
// contextengine itself imports domain (CE-01). Application (Task 9) is
// responsible for translating between the two. SourceDigest is hex-encoded
// (contextengine's own [32]byte does not marshal usefully to JSON).
type ContextCheckpointRecord struct {
	ID                     string `json:"id"`
	Kind                   string `json:"kind"`
	SourceSchema           string `json:"sourceSchema"`
	SummaryFormat          string `json:"summaryFormat,omitempty"`
	PromptVersion          string `json:"promptVersion,omitempty"`
	CoveredEventCount      uint64 `json:"coveredEventCount"`
	CoveredTurnCount       uint64 `json:"coveredTurnCount"`
	ThroughSequence        uint64 `json:"throughSequence"`
	SourceDigestHex        string `json:"sourceDigestHex"`
	PreviousCheckpointID   string `json:"previousCheckpointID,omitempty"`
	Summary                string `json:"summary,omitempty"`
	Limitations            string `json:"limitations,omitempty"`
	TokensBefore           uint64 `json:"tokensBefore"`
	CheckpointTokens       uint64 `json:"checkpointTokens"`
	RetainedTailTokens     uint64 `json:"retainedTailTokens"`
	EstimatedRequestTokens uint64 `json:"estimatedRequestTokens"`
	SummarizerRoute        string `json:"summarizerRoute,omitempty"`
	SummarizerUsage        uint64 `json:"summarizerUsage,omitempty"`
	SummaryChunks          uint32 `json:"summaryChunks,omitempty"`
	PrunedToolResultCount  uint32 `json:"prunedToolResultCount,omitempty"`
}

// ContextCompactionStarted opens design §13.3's bounded ContextCompaction
// aggregate value. BaseSourceHead is the pinned scan head the compaction
// plans against; PriorCheckpointID is "" for a Session's first compaction.
// PlannedRoute is a non-secret route identity only (never a credential).
type ContextCompactionStarted struct {
	ID                ContextCompactionID `json:"id"`
	Trigger           string              `json:"trigger"`
	Strategy          string              `json:"strategy"`
	BaseSourceHead    uint64              `json:"baseSourceHead"`
	PriorCheckpointID string              `json:"priorCheckpointID,omitempty"`
	PromptVersion     string              `json:"promptVersion,omitempty"`
	SourceSchema      string              `json:"sourceSchema"`
	MeterID           string              `json:"meterID"`
	PlannedRoute      string              `json:"plannedRoute,omitempty"`
}

func (ContextCompactionStarted) EventType() string { return EventContextCompactionStarted }

// ContextCompactionCompleted closes an active compaction successfully,
// embedding the validated checkpoint (design §13.2/§13.3). Completing
// clears Session.ContextCompaction; the checkpoint itself never enters the
// bounded aggregate.
type ContextCompactionCompleted struct {
	ID         ContextCompactionID     `json:"id"`
	Checkpoint ContextCheckpointRecord `json:"checkpoint"`
}

func (ContextCompactionCompleted) EventType() string { return EventContextCompactionCompleted }

// ContextCompactionFailed closes an active compaction with a closed,
// stable code and a safe message — never partial model output (design
// §13.2).
type ContextCompactionFailed struct {
	ID      ContextCompactionID `json:"id"`
	Code    string              `json:"code"`
	Message string              `json:"message"`
}

func (ContextCompactionFailed) EventType() string { return EventContextCompactionFailed }

// ContextPreparedRecorded is design §7.4's per-attempt evidence: what the
// Context Engine decided before this ModelRequestRecorded was dispatched.
type ContextPreparedRecorded struct {
	TurnID                    TurnID            `json:"turnID"`
	ItemID                    ItemID            `json:"itemID"`
	AttemptIndex              uint32            `json:"attemptIndex"`
	ContextDecisionID         ContextDecisionID `json:"contextDecisionID"`
	Trigger                   string            `json:"trigger"`
	SourceHeadVersion         uint64            `json:"sourceHeadVersion"`
	CheckpointID              string            `json:"checkpointID,omitempty"`
	CheckpointKind            string            `json:"checkpointKind,omitempty"`
	RawTailFromSequence       uint64            `json:"rawTailFromSequence,omitempty"`
	RawTailThroughSequence    uint64            `json:"rawTailThroughSequence,omitempty"`
	BudgetHardInput           uint64            `json:"budgetHardInput"`
	BudgetTrigger             uint64            `json:"budgetTrigger"`
	BudgetTarget              uint64            `json:"budgetTarget"`
	EstimatedMessageTokens    uint64            `json:"estimatedMessageTokens"`
	EstimatedToolSchemaTokens uint64            `json:"estimatedToolSchemaTokens"`
	EstimatedTotalTokens      uint64            `json:"estimatedTotalTokens"`
	MeterID                   string            `json:"meterID"`
	// UsageAnchorApplied/UsageAnchorTokens record whether the non-lowering
	// usage anchor (design §8, CE-04) raised the estimate this decision
	// used, and by how much — omitted (both zero-valued) when no anchor
	// was eligible.
	UsageAnchorApplied bool   `json:"usageAnchorApplied,omitempty"`
	UsageAnchorTokens  uint64 `json:"usageAnchorTokens,omitempty"`
	// SerializedEnvelopeBytes is the actual wire size Application's
	// Provider Adapter produced for this request — not
	// contextengine.PreparedContext's own JSON-encoded approximation.
	SerializedEnvelopeBytes uint64 `json:"serializedEnvelopeBytes"`
	// PrunedToolResultCount is how many retained Tool Result messages this
	// request actually projected, as observed by Materialize. Zero means no
	// Tool Result was projected for this request; it never means pruning was
	// disabled, and it must never be derived from configuration such as
	// MaxPrunedToolResultsPerRequest. It is optional and additive: an event
	// written before this field existed decodes as zero.
	PrunedToolResultCount uint32 `json:"prunedToolResultCount,omitempty"`
}

func (ContextPreparedRecorded) EventType() string { return EventContextPreparedRecorded }
