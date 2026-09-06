package eval

import (
	"errors"
	"strings"
	"testing"
)

// TestEvalSetReferencingAPolicyWithOneRepetitionIsRefused is this plan's
// single most important guard.
//
// A spread cannot be measured from one sample. An EvalSet that declares a
// variance policy while running each Cell once would publish spread=0 and
// stability=1 for every Cell — perfect agreement, measured never — and the
// refusal has to land at load, before an Attempt root exists, because by the
// time a report is written the misleading numbers already exist.
func TestEvalSetReferencingAPolicyWithOneRepetitionIsRefused(t *testing.T) {
	set := validEvalSet(t)
	set.VariancePolicyDigest = Digest("sha256:" + strings.Repeat("a", 64))
	set.RepetitionCount = 1

	err := set.Validate()
	if !errors.Is(err, errInvalidDocument) {
		t.Fatalf("Validate() = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "repetition") {
		t.Fatalf("err = %v, want it to name the single-repetition cause", err)
	}
}

func TestEvalSetWithoutAPolicyMayStillRunOnce(t *testing.T) {
	// Every checked-in set today declares repetitionCount: 1 and no policy.
	// Requiring repetitions unconditionally would break all of them for a
	// rule that does not apply.
	set := validEvalSet(t)
	set.RepetitionCount = 1
	set.VariancePolicyDigest = ""
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate() = %v; a set without a variance policy is unaffected", err)
	}
}

func TestEvalSetVariancePolicyDigestMustBeWellFormed(t *testing.T) {
	set := validEvalSet(t)
	set.RepetitionCount = 3
	set.VariancePolicyDigest = "not-a-digest"
	if err := set.Validate(); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("Validate() = %v, want a malformed digest to be refused", err)
	}
}

// TestAPolicyDigestMismatchIsRefusedRatherThanFallingBackToNoPolicy: a Score
// names the policy it was judged under, and a reader recomputing that
// judgement offline must be using the same document. Falling back to "no
// policy" would silently turn an untrustworthy Cell into an unqualified one.
func TestAPolicyDigestMismatchIsRefusedRatherThanFallingBackToNoPolicy(t *testing.T) {
	policy := validVariancePolicy()
	actual, err := VariancePolicyDigest(policy)
	if err != nil {
		t.Fatalf("VariancePolicyDigest: %v", err)
	}
	other := Digest("sha256:" + strings.Repeat("b", 64))

	if err := VerifyVariancePolicyBinding(policy, other); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("VerifyVariancePolicyBinding() = %v, want a mismatch to be refused", err)
	}
	if err := VerifyVariancePolicyBinding(policy, actual); err != nil {
		t.Fatalf("VerifyVariancePolicyBinding() rejected the matching digest: %v", err)
	}
	if err := VerifyVariancePolicyBinding(policy, ""); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("VerifyVariancePolicyBinding() = %v, want an absent binding to be refused rather than assumed", err)
	}
}

// TestScoresDisagreeingOnAnyIdentityDigestAreAHardError: two Attempts that
// differ in what was run are not repetitions of the same thing, and pooling
// them would produce a spread between two different measurements.
func TestScoresDisagreeingOnAnyIdentityDigestAreAHardError(t *testing.T) {
	for name, mutate := range map[string]func(*Attempt){
		"scenario": func(a *Attempt) { a.ScenarioDigest = "sha256:other" },
		"subject":  func(a *Attempt) { a.SubjectDigest = "sha256:other" },
		"executor": func(a *Attempt) { a.ExecutorDigest = "sha256:other" },
	} {
		t.Run(name, func(t *testing.T) {
			odd := repetition(1, ScorePass, score(0.7))
			mutate(&odd.Attempt)

			_, err := ComputeCellDistribution([]CellRepetition{
				repetition(0, ScorePass, score(0.7)),
				odd,
			}, validVariancePolicy())
			if !errors.Is(err, errInvalidDocument) {
				t.Fatalf("ComputeCellDistribution() = %v, want disagreeing %s digests to be refused", err, name)
			}
		})
	}
}

// TestRepetitionIndexesMustBeDistinct: two Attempts claiming the same
// position are a bookkeeping error, and silently ordering them by arrival
// would make the published sequence unreproducible.
func TestRepetitionIndexesMustBeDistinct(t *testing.T) {
	_, err := ComputeCellDistribution([]CellRepetition{
		repetition(0, ScorePass, score(0.7)),
		repetition(0, ScoreFail, score(0.4)),
	}, validVariancePolicy())
	if !errors.Is(err, errInvalidDocument) {
		t.Fatalf("ComputeCellDistribution() = %v, want duplicate repetition indexes to be refused", err)
	}
}
