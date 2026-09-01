package contextengine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// CheckpointKind names one of design §7.3's two checkpoint variants.
type CheckpointKind string

const (
	// CheckpointKindRollingSummary is the normal, LLM-generated variant.
	CheckpointKindRollingSummary CheckpointKind = "rolling_summary_v1"
	// CheckpointKindSourceTailReset is the deterministic, fact-free
	// fallback (design §12).
	CheckpointKindSourceTailReset CheckpointKind = "source_tail_reset_v1"
)

// SummaryFormatVersion and SummaryPromptVersion identify the summary
// contract Task 6 implements; ContextCheckpoint and its replay validation
// reference them so a format/prompt change is a visible, versioned event
// rather than a silent drift.
const (
	SummaryFormatVersion = "och_context_summary_v1"
	SummaryPromptVersion = "och_context_summary_prompt_v1"
)

// Coverage is design §7.2's coverage record: an ordered prefix of
// compactable source events through a safe unit, plus the digest chain
// proof of exactly which events that prefix contains.
type Coverage struct {
	CoveredEventCount uint64
	CoveredTurnCount  uint64
	ThroughSequence   uint64
	SourceDigest      [32]byte
}

// ContextCheckpoint is design §7.3's durable checkpoint value.
type ContextCheckpoint struct {
	ID                     string
	SessionID              domain.SessionID
	Kind                   CheckpointKind
	SourceSchema           string
	SummaryFormat          string // only CheckpointKindRollingSummary
	PromptVersion          string // only CheckpointKindRollingSummary
	Coverage               Coverage
	PreviousCheckpointID   string // "" for the first checkpoint in a Session
	Summary                string // only CheckpointKindRollingSummary
	Limitations            string
	TokensBefore           uint64
	CheckpointTokens       uint64
	RetainedTailTokens     uint64
	EstimatedRequestTokens uint64
	SummarizerRoute        string // non-secret route identity, optional
	SummarizerUsage        uint64
	SummaryChunks          uint32
	PrunedToolResultCount  uint32
}

// Clone returns a deep copy safe to hand to a caller that must not alias
// this package's own owned copies (CE-01's "concrete implementations use
// owned copies" rule).
func (checkpoint ContextCheckpoint) Clone() ContextCheckpoint {
	clone := checkpoint
	return clone
}

// ErrCheckpointInvalid reports a checkpoint that violates the successor
// lineage rules (design §7.3) — the Application layer maps this to
// context_checkpoint_invalid (design §16).
var ErrCheckpointInvalid = errors.New("contextengine: checkpoint violates coverage/lineage/schema rules")

// ValidateSuccessor checks that successor is a legal next checkpoint given
// previous (nil for a Session's first-ever checkpoint): a successor must
// strictly advance coverage, or be an explicit same-coverage rewrite whose
// PreviousCheckpointID names the current checkpoint with an identical
// source digest — never backward, never silently replacing a different
// source range under the same claimed coverage.
func ValidateSuccessor(previous *ContextCheckpoint, successor ContextCheckpoint) error {
	if successor.SourceSchema != SourceSchemaVersion {
		return fmt.Errorf("%w: unsupported source schema %q", ErrCheckpointInvalid, successor.SourceSchema)
	}
	if successor.Kind != CheckpointKindRollingSummary && successor.Kind != CheckpointKindSourceTailReset {
		return fmt.Errorf("%w: unsupported checkpoint kind %q", ErrCheckpointInvalid, successor.Kind)
	}
	if previous == nil {
		if successor.PreviousCheckpointID != "" {
			return fmt.Errorf("%w: first checkpoint names a predecessor", ErrCheckpointInvalid)
		}
		if successor.Coverage.CoveredEventCount == 0 {
			return fmt.Errorf("%w: checkpoint covers zero events", ErrCheckpointInvalid)
		}
		return nil
	}
	if successor.PreviousCheckpointID != previous.ID {
		return fmt.Errorf("%w: does not name its actual predecessor", ErrCheckpointInvalid)
	}
	if successor.Coverage.ThroughSequence == previous.Coverage.ThroughSequence {
		if successor.Coverage.SourceDigest != previous.Coverage.SourceDigest {
			return fmt.Errorf("%w: same-coverage rewrite has a different source digest", ErrCheckpointInvalid)
		}
		return nil
	}
	if successor.Coverage.ThroughSequence < previous.Coverage.ThroughSequence {
		return fmt.Errorf("%w: coverage moved backward", ErrCheckpointInvalid)
	}
	return nil
}

// BuildResetMarker returns design §12's fixed, versioned user-role marker
// text for a CheckpointKindSourceTailReset checkpoint: no LLM-generated
// text, no source content, and no digest — only the checkpoint ID and the
// covered-through sequence, both stated as diagnostic identifiers, never
// as instructions or historical claims.
func BuildResetMarker(checkpointID string, coveredThroughSequence uint64) string {
	return fmt.Sprintf(`[context reset by Open Code Harness]
checkpoint_id: %s
covered_through_sequence: %d
An earlier portion of this conversation's history was omitted because it
exceeded the available context capacity. This marker does not summarize
or assert anything about that omitted history; the identifiers above are
diagnostic only, not instructions. Continue from the retained messages
and the current request below.
[end context reset]`, checkpointID, coveredThroughSequence)
}

// ResetEligibility bundles design §12's four independent eligibility
// conditions for automatic reset, plus cancellation, which never
// satisfies the gate on its own regardless of the other four.
type ResetEligibility struct {
	// HardLimitExceeded is true when the projected request exceeds
	// hardInput, or a classified startup context_overflow occurred.
	HardLimitExceeded bool
	// RollingSummaryUnavailable is true when a rolling summary is
	// impossible, canceled without the caller itself having canceled,
	// invalid, non-shrinking, or exceeds the bounded chunk count — the
	// caller (Task 9/10) combines whichever of those applies into this
	// one field, since this package does not itself run a summarizer.
	RollingSummaryUnavailable bool
	// SafeCoveredPrefixExists is true when SelectCutPoint found at least
	// one coverable unit.
	SafeCoveredPrefixExists bool
	// ResetFitsHardInput is true when the reset marker plus the retained
	// complete tail together fit hardInput.
	ResetFitsHardInput bool
	// CallerCanceled is true when the caller itself requested
	// cancellation. This alone forces ineligibility regardless of the
	// four fields above — cancellation must never silently become a
	// reset (Global Constraint, design §26).
	CallerCanceled bool
}

// ResetEligible evaluates design §12's gate: automatic reset is allowed
// only when every one of HardLimitExceeded, RollingSummaryUnavailable,
// SafeCoveredPrefixExists, and ResetFitsHardInput holds, and CallerCanceled
// does not.
func ResetEligible(eligibility ResetEligibility) bool {
	if eligibility.CallerCanceled {
		return false
	}
	return eligibility.HardLimitExceeded &&
		eligibility.RollingSummaryUnavailable &&
		eligibility.SafeCoveredPrefixExists &&
		eligibility.ResetFitsHardInput
}

// ErrCheckpointReplayInvalid reports a checkpoint that failed replay
// validation (design §14.3) — the Application layer maps this to
// context_checkpoint_invalid (design §16), the same code
// ValidateSuccessor's failures map to, since both report an unusable
// checkpoint from the caller's point of view.
var ErrCheckpointReplayInvalid = errors.New("contextengine: checkpoint failed replay validation")

// ReplayValidationInput bundles everything ValidateCheckpointReplay needs
// to decide whether a previously-committed checkpoint may still be used
// for a new request (design §14.3).
type ReplayValidationInput struct {
	Checkpoint ContextCheckpoint
	SessionID  domain.SessionID
	// PinnedHeadSequence is the current scan's own pinned head — the
	// checkpoint's coverage must not claim events beyond it.
	PinnedHeadSequence uint64
	// SourceDigestProof is supplied by the ContextCheckpointStore contract
	// (Task 13): true only once the store has independently re-verified
	// the checkpoint's coverage and digest against canonical events. This
	// function never fabricates that proof itself.
	SourceDigestProof bool
	// CurrentBudget is the route's budget as of *this* request — may
	// differ from what the checkpoint was built against (a model switch,
	// a tightened configuration).
	CurrentBudget Budget
	// RetainedTailTokens is the meter's current estimate of the tail that
	// would sit alongside this checkpoint under CurrentBudget.
	RetainedTailTokens uint64
}

// ValidateCheckpointReplay implements design §14.3's checklist. A
// previously valid checkpoint failing any check here — most commonly
// after a model switch shrinks the route's capacity — is rejected, never
// silently accepted; the caller then rebuilds, advances, resets, or
// proceeds uncheckpointed instead.
func ValidateCheckpointReplay(input ReplayValidationInput) error {
	checkpoint := input.Checkpoint
	if checkpoint.SessionID != input.SessionID {
		return fmt.Errorf("%w: checkpoint belongs to a different Session", ErrCheckpointReplayInvalid)
	}
	if checkpoint.SourceSchema != SourceSchemaVersion {
		return fmt.Errorf("%w: unsupported source schema %q", ErrCheckpointReplayInvalid, checkpoint.SourceSchema)
	}
	switch checkpoint.Kind {
	case CheckpointKindRollingSummary:
		if checkpoint.SummaryFormat != SummaryFormatVersion {
			return fmt.Errorf("%w: unsupported summary format %q", ErrCheckpointReplayInvalid, checkpoint.SummaryFormat)
		}
		if strings.TrimSpace(checkpoint.Summary) == "" {
			return fmt.Errorf("%w: rolling summary checkpoint has no summary text", ErrCheckpointReplayInvalid)
		}
	case CheckpointKindSourceTailReset:
		// No summary structure to validate; BuildResetMarker's own fixed
		// format is not re-derived here.
	default:
		return fmt.Errorf("%w: unsupported checkpoint kind %q", ErrCheckpointReplayInvalid, checkpoint.Kind)
	}
	if checkpoint.Coverage.ThroughSequence > input.PinnedHeadSequence {
		return fmt.Errorf("%w: coverage extends beyond the pinned scan head", ErrCheckpointReplayInvalid)
	}
	if !input.SourceDigestProof {
		return fmt.Errorf("%w: source digest was not independently re-verified", ErrCheckpointReplayInvalid)
	}
	if checkpoint.CheckpointTokens+input.RetainedTailTokens > input.CurrentBudget.HardInput {
		return fmt.Errorf("%w: checkpoint plus retained tail no longer fits the current budget", ErrCheckpointReplayInvalid)
	}
	return nil
}
