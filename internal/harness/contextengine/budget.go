package contextengine

import (
	"errors"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// BudgetConfig names the operator-tunable percentages the budget formula
// (design §8) applies against a route's hard input capacity. This package
// does not substitute a default for a zero field — Composition resolves a
// zero scalar to DefaultBudgetConfig's value (design §21) and validates the
// TailPercent < TargetPercent < TriggerPercent ordering before any resource
// is constructed; ComputeBudget stays a pure function of exactly the
// config it is given so its own tests can exercise off-default and
// deliberately invalid configurations without a hidden substitution step.
type BudgetConfig struct {
	TriggerPercent uint32
	TargetPercent  uint32
	TailPercent    uint32
}

// DefaultBudgetConfig is the design's own default trigger/target/tail
// percentages (§8's table: 80/55/25).
var DefaultBudgetConfig = BudgetConfig{TriggerPercent: 80, TargetPercent: 55, TailPercent: 25}

// Budget is the complete set of token thresholds ComputeBudget derives from
// one route's context window (W) and maximum output (O), per design §8.
type Budget struct {
	Safety           uint64
	HardInput        uint64
	Trigger          uint64
	Target           uint64
	ProtectedTail    uint64
	SummaryOutputCap uint64
}

// ErrBudgetInvalid reports that a route's declared context window cannot
// produce a positive hard input budget once its maximum output and safety
// reserve are subtracted (O + safety >= W). The Application layer maps
// this to the design's context_budget_invalid code (§16); this package
// stays free of that string so it never depends on
// internal/harness/application.
var ErrBudgetInvalid = errors.New("contextengine: route capacity cannot produce a positive hard input budget")

// ComputeBudget derives Budget from a route's declared context window (W)
// and maximum output (O) tokens, per design §8:
//
//	safety           = max(512, ceil(W * 0.02))
//	hardInput        = W - O - safety
//	trigger          = floor(hardInput * TriggerPercent / 100)
//	target           = floor(hardInput * TargetPercent / 100)
//	protectedTail    = floor(hardInput * TailPercent / 100)
//	summaryOutputCap = min(O, max(128, floor(hardInput * 0.10)))
//
// It returns ErrBudgetInvalid when O + safety >= W, since no positive
// hardInput budget can exist. Both W and O are expected to already be the
// composition-validated CapabilityProfile.ContextWindowTokens/
// MaxOutputTokens (CE-03, internal/harness/composition/config.go:126-127
// rejects a zero value there); ComputeBudget itself only enforces the
// O + safety >= W relationship, which a zero W or O will also fail.
func ComputeBudget(contextWindowTokens, maxOutputTokens uint32, config BudgetConfig) (Budget, error) {
	windowTokens := uint64(contextWindowTokens)
	outputTokens := uint64(maxOutputTokens)

	safety := maxUint64(512, ceilDiv(windowTokens*2, 100)) // ceil(W * 0.02) == ceil(W*2/100)
	if outputTokens+safety >= windowTokens {
		return Budget{}, ErrBudgetInvalid
	}
	hardInput := windowTokens - outputTokens - safety

	return Budget{
		Safety:           safety,
		HardInput:        hardInput,
		Trigger:          hardInput * uint64(config.TriggerPercent) / 100,
		Target:           hardInput * uint64(config.TargetPercent) / 100,
		ProtectedTail:    hardInput * uint64(config.TailPercent) / 100,
		SummaryOutputCap: minUint64(outputTokens, maxUint64(128, hardInput/10)),
	}, nil
}

func ceilDiv(numerator, denominator uint64) uint64 {
	if denominator == 0 {
		return 0
	}
	return (numerator + denominator - 1) / denominator
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// UsageAnchor is one committed conversation attempt's identity and
// observed token usage, as the caller (internal/harness/application, the
// only package that reads the EventStore) reconstructs it from a matched
// ModelRequestRecorded/ModelUsageRecorded pair. EvaluateUsageAnchor
// independently re-checks every eligibility condition itself; it never
// trusts that the caller has already filtered for eligibility.
type UsageAnchor struct {
	AdapterFamily       string
	ModelID             string
	EndpointID          string
	Purpose             string // must equal "conversation" (engine.RequestPurpose's string form) to be eligible
	MeterID             string
	Tools               []domain.ToolSchema
	Messages            []domain.ModelPromptMessage
	ObservedInputTokens uint64
	CachedInputTokens   uint64
}

// PurposeConversation is the only UsageAnchor.Purpose value
// EvaluateUsageAnchor accepts. It is a plain string, not
// engine.ModelRequestPurpose, because this package may not import
// internal/harness/engine (CE-01); the two constants must be kept in sync
// by the Task 9 caller that converts one into the other.
const PurposeConversation = "conversation"

// AnchorEstimate is EvaluateUsageAnchor's result for an eligible anchor.
type AnchorEstimate struct {
	Eligible bool
	// Tokens is anchoredEstimate = max(0, observedInputTokens + signedSurfaceDelta),
	// meaningful only when Eligible is true.
	Tokens uint64
}

// EvaluateUsageAnchor decides whether anchor is an eligible non-lowering
// usage anchor for the current request (currentIdentity/currentTools/
// currentMessages), and if so computes anchoredEstimate, per design §8 as
// amended by the CE-04 review-resolution:
//
//	budgetEstimate = max(wireEstimate, anchoredEstimate?)
//
// Eligibility requires all of:
//   - anchor.Purpose == PurposeConversation and ObservedInputTokens > 0
//     (a zero, missing, or compaction-purpose observation is never eligible);
//   - CachedInputTokens <= ObservedInputTokens (a malformed observation is
//     ignored, never trusted);
//   - AdapterFamily, ModelID, EndpointID, MeterID, and Tools (by exact
//     canonical-JSON equality, order-sensitive) all match the current
//     request's identity exactly;
//   - the current message surface is derivable from the anchor's own
//     recorded Messages by an ordered append: currentMessages must start
//     with exactly anchor.Messages as a prefix.
//
// Only the ordered-append derivability case is implemented by this
// function. The design's second derivability case — a current surface
// reachable via a checkpoint/rewrite replacement the same Meter can price
// — requires contextengine.Checkpoint (implementation plan Task 5) and is
// added by the Task 9 orchestrator once a checkpoint is actually in play;
// until then, a request whose surface was rewritten by a checkpoint always
// falls through to the deterministic wireEstimate alone, which is always a
// safe (never-too-low) answer, never an incorrect one, since
// budgetEstimate never lowers wireEstimate.
//
// signedSurfaceDelta prices the appended suffix using meter. Raw
// OutputTokens is never added (it may include unpersisted reasoning);
// CachedInputTokens is never added to ObservedInputTokens (it is already a
// subset of it, under the Engine contract).
func EvaluateUsageAnchor(anchor UsageAnchor, meter Meter, currentAdapterFamily, currentModelID, currentEndpointID, currentMeterID string, currentTools []domain.ToolSchema, currentMessages []domain.ModelPromptMessage) AnchorEstimate {
	if anchor.Purpose != PurposeConversation || anchor.ObservedInputTokens == 0 {
		return AnchorEstimate{}
	}
	if anchor.CachedInputTokens > anchor.ObservedInputTokens {
		return AnchorEstimate{}
	}
	if anchor.AdapterFamily != currentAdapterFamily || anchor.ModelID != currentModelID || anchor.EndpointID != currentEndpointID || anchor.MeterID != currentMeterID {
		return AnchorEstimate{}
	}
	if !toolSchemasEqual(anchor.Tools, currentTools) {
		return AnchorEstimate{}
	}
	appended, ok := appendedSuffix(anchor.Messages, currentMessages)
	if !ok {
		return AnchorEstimate{}
	}

	delta := meter.EstimateMessages(appended)
	return AnchorEstimate{Eligible: true, Tokens: anchor.ObservedInputTokens + delta}
}

// appendedSuffix reports the messages in current that come after an exact
// prefix match of anchor, or ok=false if current does not start with
// anchor verbatim (a rewrite, truncation, or reorder — none of which this
// function's caller may treat as a simple append).
func appendedSuffix(anchor, current []domain.ModelPromptMessage) (suffix []domain.ModelPromptMessage, ok bool) {
	if len(current) < len(anchor) {
		return nil, false
	}
	for index := range anchor {
		if !messageEqual(anchor[index], current[index]) {
			return nil, false
		}
	}
	return current[len(anchor):], true
}

func messageEqual(a, b domain.ModelPromptMessage) bool {
	if a.Role != b.Role || a.Text != b.Text || a.ToolCallID != b.ToolCallID || a.Name != b.Name {
		return false
	}
	if len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for index := range a.ToolCalls {
		if a.ToolCalls[index] != b.ToolCalls[index] {
			return false
		}
	}
	return true
}

func toolSchemasEqual(a, b []domain.ToolSchema) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index].Name != b[index].Name || a[index].Description != b[index].Description || string(a[index].InputSchema) != string(b[index].InputSchema) {
			return false
		}
	}
	return true
}
