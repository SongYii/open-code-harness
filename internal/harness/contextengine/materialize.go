package contextengine

import (
	"encoding/json"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// PreparedContext is Materialize's output: the complete envelope ready
// for Provider dispatch, plus the ContextPreparedRecorded evidence fields
// (design §7.4) a caller (Application, Task 9) durably records alongside
// it.
type PreparedContext struct {
	Envelope Envelope
	// CheckpointID/CheckpointKind are "" when no checkpoint was used.
	CheckpointID   string
	CheckpointKind CheckpointKind
	// RetainedTailFromSequence/RetainedTailThroughSequence bound the raw
	// (uncompacted) tail actually included; both 0 if nothing beyond a
	// checkpoint (or nothing at all) was retained.
	RetainedTailFromSequence    uint64
	RetainedTailThroughSequence uint64
	EstimatedMessageTokens      uint64
	EstimatedToolSchemaTokens   uint64
	EstimatedTotalTokens        uint64
	MeterID                     string
	// PrunedToolResultCount is how many retained Tool Result messages this
	// call actually replaced with ProjectToolResult's marker-framed excerpt
	// (design §10) — 0 when MaterializeInput never enabled pruning, or when
	// every retained Tool Result already fit under
	// MaxProjectedToolResultTokens(ProtectedTail).
	PrunedToolResultCount uint32
	// ApproximateSerializedBytes is this package's own JSON-encoded proxy
	// for the envelope's size — not the literal wire bytes a Provider
	// Adapter (Task 8) would produce, which this package has no visibility
	// into, but a same-order-of-magnitude, deterministic stand-in for the
	// existing MaxProjectionBytes cap.
	ApproximateSerializedBytes int
}

// MaterializeInput bundles everything Materialize needs to combine a
// selected checkpoint (or none), a retained raw tail, and the current
// input into one PreparedContext.
type MaterializeInput struct {
	// Checkpoint is nil when no compaction was needed for this request.
	Checkpoint   *ContextCheckpoint
	RetainedTail []ContextUnit
	CurrentInput domain.ModelPromptMessage
	Tools        []domain.ToolSchema
	Meter        Meter
	// ProtectedTail, MaxPrunedToolResults, and HardInput together enable
	// design §10's Tool Result projection over RetainedTail: a retained
	// Tool Result message whose own meter estimate exceeds
	// MaxProjectedToolResultTokens(ProtectedTail) is replaced by
	// ProjectToolResult's marker-framed excerpt, oldest first (RetainedTail's
	// own ascending sequence order), up to MaxPrunedToolResults such
	// replacements per call; a Tool Result ProjectToolResult itself cannot
	// fit under HardInput even with an empty excerpt is left byte-identical
	// rather than failing this call (a disclosed, best-effort fallback —
	// Materialize returns no error at all, so it cannot itself report
	// ErrContextUnitTooLarge; a request that still cannot fit remains the
	// existing overflow_retry trigger's problem to react to). Any of the
	// three left at its zero value disables pruning entirely — every caller
	// that built a MaterializeInput before these fields existed continues
	// to receive byte-identical Tool Result content, unchanged.
	ProtectedTail        uint64
	MaxPrunedToolResults uint32
	HardInput            uint64
}

// Materialize combines MaterializeInput into one PreparedContext: an
// optional checkpoint's own materialized message (the summary text for
// CheckpointKindRollingSummary, or BuildResetMarker's fixed text for
// CheckpointKindSourceTailReset) first, then every retained unit's
// messages in order, then the current input last.
func Materialize(input MaterializeInput) PreparedContext {
	var messages []domain.ModelPromptMessage
	result := PreparedContext{}
	if input.Checkpoint != nil {
		result.CheckpointID = input.Checkpoint.ID
		result.CheckpointKind = input.Checkpoint.Kind
		switch input.Checkpoint.Kind {
		case CheckpointKindRollingSummary:
			messages = append(messages, domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: input.Checkpoint.Summary})
		case CheckpointKindSourceTailReset:
			messages = append(messages, domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: BuildResetMarker(input.Checkpoint.ID, input.Checkpoint.Coverage.ThroughSequence)})
		}
	}
	if len(input.RetainedTail) > 0 {
		result.RetainedTailFromSequence = input.RetainedTail[0].FirstSequence
		result.RetainedTailThroughSequence = input.RetainedTail[len(input.RetainedTail)-1].LastSequence
	}
	pruningEnabled := input.ProtectedTail > 0 && input.MaxPrunedToolResults > 0
	var maxToolResultTokens uint64
	if pruningEnabled {
		maxToolResultTokens = MaxProjectedToolResultTokens(input.ProtectedTail)
	}
	var prunedCount uint32
	for _, unit := range input.RetainedTail {
		for _, message := range unit.Messages {
			if pruningEnabled && prunedCount < input.MaxPrunedToolResults && message.Role == domain.PromptRoleTool {
				if estimateText(input.Meter, message.Text) > maxToolResultTokens {
					projected, err := ProjectToolResult(message.ToolCallID, message.Text, maxToolResultTokens, input.Meter, input.HardInput)
					if err == nil && projected.Projected {
						message.Text = projected.Text
						prunedCount++
					}
				}
			}
			messages = append(messages, message)
		}
	}
	result.PrunedToolResultCount = prunedCount
	messages = append(messages, currentInputMessages(input.CurrentInput)...)

	envelope := Envelope{Messages: messages, Tools: input.Tools}
	estimate := input.Meter.Estimate(envelope)
	messageTokens := input.Meter.EstimateMessages(messages)

	result.Envelope = envelope
	result.EstimatedMessageTokens = messageTokens
	// The meter's own cost is additive between messages and Tool Schemas
	// (WireEstimateMeter.Estimate sums EstimateMessages plus a per-schema
	// charge); subtracting recovers the Tool Schema share without a
	// separate Meter method for it.
	if estimate.Tokens > messageTokens {
		result.EstimatedToolSchemaTokens = estimate.Tokens - messageTokens
	}
	result.EstimatedTotalTokens = estimate.Tokens
	result.MeterID = estimate.MeterID
	if encoded, err := json.Marshal(envelope); err == nil {
		result.ApproximateSerializedBytes = len(encoded)
	}
	return result
}
