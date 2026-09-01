package contextengine

import (
	"errors"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func baseCheckpoint() ContextCheckpoint {
	return ContextCheckpoint{
		ID:               "ckpt_1",
		SessionID:        "sess1",
		Kind:             CheckpointKindRollingSummary,
		SourceSchema:     SourceSchemaVersion,
		SummaryFormat:    SummaryFormatVersion,
		PromptVersion:    SummaryPromptVersion,
		Coverage:         Coverage{CoveredEventCount: 10, ThroughSequence: 100, SourceDigest: [32]byte{1, 2, 3}},
		Summary:          "## Objective\nsomething",
		CheckpointTokens: 200,
	}
}

func TestValidateSuccessorFirstCheckpoint(t *testing.T) {
	valid := baseCheckpoint()
	if err := ValidateSuccessor(nil, valid); err != nil {
		t.Fatalf("expected a valid first checkpoint, got %v", err)
	}

	namesPredecessor := baseCheckpoint()
	namesPredecessor.PreviousCheckpointID = "ckpt_0"
	if err := ValidateSuccessor(nil, namesPredecessor); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("got %v, want ErrCheckpointInvalid (first checkpoint names a predecessor)", err)
	}

	empty := baseCheckpoint()
	empty.Coverage.CoveredEventCount = 0
	if err := ValidateSuccessor(nil, empty); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("got %v, want ErrCheckpointInvalid (zero coverage)", err)
	}
}

func TestValidateSuccessorAdvancingCoverage(t *testing.T) {
	previous := baseCheckpoint()
	successor := baseCheckpoint()
	successor.ID = "ckpt_2"
	successor.PreviousCheckpointID = previous.ID
	successor.Coverage.ThroughSequence = 200 // strictly advances
	successor.Coverage.SourceDigest = [32]byte{9, 9, 9}
	if err := ValidateSuccessor(&previous, successor); err != nil {
		t.Fatalf("expected a valid advancing successor, got %v", err)
	}
}

func TestValidateSuccessorSameCoverageRewrite(t *testing.T) {
	previous := baseCheckpoint()

	rewrite := baseCheckpoint()
	rewrite.ID = "ckpt_2"
	rewrite.PreviousCheckpointID = previous.ID
	rewrite.Coverage.ThroughSequence = previous.Coverage.ThroughSequence
	rewrite.Coverage.SourceDigest = previous.Coverage.SourceDigest // identical digest: legal rewrite
	if err := ValidateSuccessor(&previous, rewrite); err != nil {
		t.Fatalf("expected a valid same-coverage rewrite, got %v", err)
	}

	rewriteDifferentDigest := baseCheckpoint()
	rewriteDifferentDigest.ID = "ckpt_2"
	rewriteDifferentDigest.PreviousCheckpointID = previous.ID
	rewriteDifferentDigest.Coverage.ThroughSequence = previous.Coverage.ThroughSequence
	rewriteDifferentDigest.Coverage.SourceDigest = [32]byte{7, 7, 7} // different digest at same coverage: illegal
	if err := ValidateSuccessor(&previous, rewriteDifferentDigest); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("got %v, want ErrCheckpointInvalid (same-coverage rewrite, different digest)", err)
	}
}

func TestValidateSuccessorNeverBackward(t *testing.T) {
	previous := baseCheckpoint()
	backward := baseCheckpoint()
	backward.ID = "ckpt_2"
	backward.PreviousCheckpointID = previous.ID
	backward.Coverage.ThroughSequence = previous.Coverage.ThroughSequence - 1
	if err := ValidateSuccessor(&previous, backward); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("got %v, want ErrCheckpointInvalid (coverage moved backward)", err)
	}
}

func TestValidateSuccessorWrongPredecessor(t *testing.T) {
	previous := baseCheckpoint()
	wrongPredecessor := baseCheckpoint()
	wrongPredecessor.ID = "ckpt_2"
	wrongPredecessor.PreviousCheckpointID = "not-the-real-predecessor"
	wrongPredecessor.Coverage.ThroughSequence = previous.Coverage.ThroughSequence + 1
	if err := ValidateSuccessor(&previous, wrongPredecessor); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("got %v, want ErrCheckpointInvalid (wrong predecessor named)", err)
	}
}

func TestContextCheckpointClone(t *testing.T) {
	original := baseCheckpoint()
	clone := original.Clone()
	clone.Summary = "mutated"
	if original.Summary == clone.Summary {
		t.Fatal("mutating the clone's Summary affected the original")
	}
}

// TestBuildResetMarkerGolden pins the reset marker's exact fixed text
// (design §12): it must contain the checkpoint ID and covered-through
// sequence as diagnostic identifiers, and must NOT contain any source
// content or digest.
func TestBuildResetMarkerGolden(t *testing.T) {
	marker := BuildResetMarker("ckpt_42", 1234)
	if !strings.Contains(marker, "ckpt_42") {
		t.Fatal("marker missing checkpoint ID")
	}
	if !strings.Contains(marker, "1234") {
		t.Fatal("marker missing covered-through sequence")
	}
	if !strings.Contains(marker, "does not summarize") {
		t.Fatal("marker missing the no-historical-claim statement")
	}
	// No digest (hex-encoded sha256 is 64 hex chars) should appear.
	for _, word := range strings.Fields(marker) {
		if len(word) == 64 && isHex(word) {
			t.Fatalf("marker unexpectedly contains what looks like a digest: %q", word)
		}
	}
	// Determinism: identical inputs produce byte-identical output.
	if BuildResetMarker("ckpt_42", 1234) != marker {
		t.Fatal("BuildResetMarker is not deterministic")
	}
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// TestResetEligibleFourConditions tests design §12's gate: each of the
// four required conditions, tested independently as the sole reason
// eligibility fails when false alone.
func TestResetEligibleFourConditions(t *testing.T) {
	allTrue := ResetEligibility{
		HardLimitExceeded:         true,
		RollingSummaryUnavailable: true,
		SafeCoveredPrefixExists:   true,
		ResetFitsHardInput:        true,
	}
	if !ResetEligible(allTrue) {
		t.Fatal("expected eligible when all four conditions hold")
	}

	tests := []struct {
		name   string
		mutate func(ResetEligibility) ResetEligibility
	}{
		{name: "hard limit not exceeded", mutate: func(e ResetEligibility) ResetEligibility { e.HardLimitExceeded = false; return e }},
		{name: "rolling summary still available", mutate: func(e ResetEligibility) ResetEligibility { e.RollingSummaryUnavailable = false; return e }},
		{name: "no safe covered prefix", mutate: func(e ResetEligibility) ResetEligibility { e.SafeCoveredPrefixExists = false; return e }},
		{name: "reset does not fit hard input", mutate: func(e ResetEligibility) ResetEligibility { e.ResetFitsHardInput = false; return e }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if ResetEligible(test.mutate(allTrue)) {
				t.Fatalf("expected ineligible when %s", test.name)
			}
		})
	}
}

// TestResetEligibleCancellationNeverSatisfiesGate is the mutation-check
// counterpart for the "reset hard-limit gate" target (design §22.4, plan
// Task 5): cancellation must force ineligibility even when the other four
// conditions all hold, matching the Global Constraint that cancellation
// never silently becomes a reset.
func TestResetEligibleCancellationNeverSatisfiesGate(t *testing.T) {
	eligibility := ResetEligibility{
		HardLimitExceeded:         true,
		RollingSummaryUnavailable: true,
		SafeCoveredPrefixExists:   true,
		ResetFitsHardInput:        true,
		CallerCanceled:            true,
	}
	if ResetEligible(eligibility) {
		t.Fatal("expected ineligible when the caller canceled, regardless of the other four conditions")
	}
}

func validReplayInput() ReplayValidationInput {
	checkpoint := baseCheckpoint()
	return ReplayValidationInput{
		Checkpoint:         checkpoint,
		SessionID:          checkpoint.SessionID,
		PinnedHeadSequence: 1000,
		SourceDigestProof:  true,
		CurrentBudget:      Budget{HardInput: 10_000},
		RetainedTailTokens: 500,
	}
}

func TestValidateCheckpointReplayAccepts(t *testing.T) {
	if err := ValidateCheckpointReplay(validReplayInput()); err != nil {
		t.Fatalf("expected a valid replay input to pass, got %v", err)
	}
}

func TestValidateCheckpointReplayRejectsWrongSession(t *testing.T) {
	input := validReplayInput()
	input.SessionID = domain.SessionID("different-session")
	if err := ValidateCheckpointReplay(input); !errors.Is(err, ErrCheckpointReplayInvalid) {
		t.Fatalf("got %v, want ErrCheckpointReplayInvalid", err)
	}
}

func TestValidateCheckpointReplayRejectsCoverageBeyondPinnedHead(t *testing.T) {
	input := validReplayInput()
	input.PinnedHeadSequence = input.Checkpoint.Coverage.ThroughSequence - 1
	if err := ValidateCheckpointReplay(input); !errors.Is(err, ErrCheckpointReplayInvalid) {
		t.Fatalf("got %v, want ErrCheckpointReplayInvalid", err)
	}
}

func TestValidateCheckpointReplayRejectsUnprovenDigest(t *testing.T) {
	input := validReplayInput()
	input.SourceDigestProof = false
	if err := ValidateCheckpointReplay(input); !errors.Is(err, ErrCheckpointReplayInvalid) {
		t.Fatalf("got %v, want ErrCheckpointReplayInvalid", err)
	}
}

// TestValidateCheckpointReplayRejectsAfterModelSwitch is the "checkpoint
// valid under one route's W rejected after simulating a smaller W" case
// the plan requires, and is also the mutation-check counterpart for the
// "checkpoint current-budget replay check" target (design §22.4).
func TestValidateCheckpointReplayRejectsAfterModelSwitch(t *testing.T) {
	input := validReplayInput()
	// Under the original (larger) budget this checkpoint replays fine.
	if err := ValidateCheckpointReplay(input); err != nil {
		t.Fatalf("expected the original budget to accept this checkpoint, got %v", err)
	}
	// Simulate a model switch to a much smaller route: the checkpoint's
	// own CheckpointTokens (200) plus the current retained tail (500)
	// must now be evaluated against the new, smaller HardInput.
	input.CurrentBudget = Budget{HardInput: 100}
	if err := ValidateCheckpointReplay(input); !errors.Is(err, ErrCheckpointReplayInvalid) {
		t.Fatalf("got %v, want ErrCheckpointReplayInvalid after the simulated model switch", err)
	}
}

func TestValidateCheckpointReplayRejectsMissingSummaryText(t *testing.T) {
	input := validReplayInput()
	input.Checkpoint.Summary = "   "
	if err := ValidateCheckpointReplay(input); !errors.Is(err, ErrCheckpointReplayInvalid) {
		t.Fatalf("got %v, want ErrCheckpointReplayInvalid (blank summary)", err)
	}
}
