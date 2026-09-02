package eval

import (
	"context"
	"fmt"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// ApprovalObservation is one resolved permission request the ApprovalMatcher
// recorded, kept as evidence regardless of how it resolved (design §7:
// undeclared/out-of-order/mismatched requests are "recorded" as an
// approval_script_violation fact, not silently dropped). CallID is the
// wire-level tool-call identifier retained for evidence only; it never
// participates in matching.
type ApprovalObservation struct {
	PromptActionID ActionID
	Ordinal        int
	ToolName       string
	CallID         string
	Answer         ApprovalAnswer
	Violation      string
}

// ApprovalDecision is what one Decide call resolved to: the answer to act
// on, and -- only when this request violated the script -- a bounded
// violation code. A violation always resolves to ApprovalDeny; Violation
// being non-empty is what distinguishes "the script declared deny" from
// "the request did not match the script at all."
type ApprovalDecision struct {
	Answer    ApprovalAnswer
	Violation string
}

// approvalCoordinate is reused from model.go's Scenario.validateApprovalScript
// (same package): one (prompt action, ordinal) pair.

// ApprovalMatcher is a stateful, fail-closed matcher compiled from one
// Scenario's frozen ApprovalScript (design §7). Both the in-process
// tools.Approver adapter (NewApprover, this file) and a future ACP
// session/request_permission handler (design §16/Task 13) compile from the
// same matcher, never a second matching algorithm.
//
// Match coordinates are {current prompt action ID, next zero-based ordinal
// for that prompt, exact tool name} -- never a generated wire or domain ID.
// An undeclared, exhausted, out-of-order, or tool-name-mismatched request is
// denied, recorded as a bounded approval_script_violation observation, and
// never advances the ordinal cursor past a declaration it did not actually
// consume, so a later, correctly-ordered request can still match.
type ApprovalMatcher struct {
	mu sync.Mutex

	entries map[approvalCoordinate]ApprovalScriptEntry

	havePrompt    bool
	currentPrompt ActionID
	nextOrdinal   int

	observations []ApprovalObservation
}

// NewApprovalMatcher compiles script into a matcher, indexed by coordinate
// for O(1) lookup. script must already satisfy Scenario.Validate's approval
// script rules (known-prompt references, unique coordinates, contiguous
// per-prompt ordinals from zero); NewApprovalMatcher does not re-derive or
// re-check them.
func NewApprovalMatcher(script []ApprovalScriptEntry) *ApprovalMatcher {
	entries := make(map[approvalCoordinate]ApprovalScriptEntry, len(script))
	for _, entry := range script {
		entries[approvalCoordinate{promptActionID: entry.PromptActionID, ordinal: entry.Ordinal}] = entry
	}
	return &ApprovalMatcher{entries: entries}
}

// BeginPrompt resets the zero-based approval ordinal for a new prompt
// action. The executor calls it once before the first permission request
// that prompt action's Turn may raise; a request received before any
// BeginPrompt call is itself a violation (design §7's "undeclared" case).
func (matcher *ApprovalMatcher) BeginPrompt(actionID ActionID) {
	matcher.mu.Lock()
	defer matcher.mu.Unlock()
	matcher.currentPrompt = actionID
	matcher.nextOrdinal = 0
	matcher.havePrompt = true
}

// Decide matches one live permission request's tool name against the
// current prompt action and the next ordinal. callID is retained on the
// resulting observation as evidence only. A match consumes the ordinal
// (advancing it) and returns the script's declared answer; anything else is
// denied and recorded as a violation without consuming an ordinal.
func (matcher *ApprovalMatcher) Decide(toolName, callID string) ApprovalDecision {
	matcher.mu.Lock()
	defer matcher.mu.Unlock()

	if !matcher.havePrompt {
		return matcher.recordViolation(ActionID(""), 0, toolName, callID, "no active prompt")
	}

	coordinate := approvalCoordinate{promptActionID: matcher.currentPrompt, ordinal: matcher.nextOrdinal}
	entry, ok := matcher.entries[coordinate]
	if !ok {
		return matcher.recordViolation(matcher.currentPrompt, matcher.nextOrdinal, toolName, callID,
			"no declared entry at this ordinal (exhausted or undeclared)")
	}
	if entry.ToolName != toolName {
		return matcher.recordViolation(matcher.currentPrompt, matcher.nextOrdinal, toolName, callID,
			fmt.Sprintf("expected tool %q, got %q", entry.ToolName, toolName))
	}

	matcher.nextOrdinal++
	matcher.observations = append(matcher.observations, ApprovalObservation{
		PromptActionID: coordinate.promptActionID,
		Ordinal:        coordinate.ordinal,
		ToolName:       toolName,
		CallID:         callID,
		Answer:         entry.Answer,
	})
	return ApprovalDecision{Answer: entry.Answer}
}

func (matcher *ApprovalMatcher) recordViolation(promptActionID ActionID, ordinal int, toolName, callID, reason string) ApprovalDecision {
	violation := "approval_script_violation: " + reason
	matcher.observations = append(matcher.observations, ApprovalObservation{
		PromptActionID: promptActionID,
		Ordinal:        ordinal,
		ToolName:       toolName,
		CallID:         callID,
		Answer:         ApprovalDeny,
		Violation:      violation,
	})
	return ApprovalDecision{Answer: ApprovalDeny, Violation: violation}
}

// Observations returns every resolved request in order, for evidence
// collection (design §7). The returned slice is a copy; it never grants a
// caller a way to mutate matcher state.
func (matcher *ApprovalMatcher) Observations() []ApprovalObservation {
	matcher.mu.Lock()
	defer matcher.mu.Unlock()
	return append([]ApprovalObservation(nil), matcher.observations...)
}

// approverAdapter wraps one ApprovalMatcher as a tools.Approver, so the
// in-process executor compiles its approval handling from the exact same
// matcher a future ACP adapter will (design §7).
type approverAdapter struct {
	matcher *ApprovalMatcher
}

// NewApprover returns a tools.Approver backed by matcher.
func NewApprover(matcher *ApprovalMatcher) tools.Approver {
	return approverAdapter{matcher: matcher}
}

func (adapter approverAdapter) Decide(_ context.Context, request tools.ApprovalRequest) (tools.ApprovalAnswer, error) {
	decision := adapter.matcher.Decide(request.Name, request.CallID)
	return tools.ApprovalAnswer{Granted: decision.Answer == ApprovalAllow}, nil
}
