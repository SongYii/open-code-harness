package eval

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// CellRepetition pairs one Attempt with the Score published for it.
//
// Both halves are needed: the Attempt carries the repetition index and the
// identity digests that make repetitions comparable, and the Score carries
// the verdict and number being measured.
type CellRepetition struct {
	Attempt Attempt
	Score   Score
}

// CellDistribution is what one Cell's repeated executions actually produced.
//
// It deliberately has **no** single derived verdict. A 4-pass/1-fail split
// and a 5-pass sweep are different facts, and collapsing them into one word
// is what this design rejects: a consumer that wants a single answer must
// choose its own rule and say so, rather than finding one pre-decided here.
type CellDistribution struct {
	// Attempts is the raw denominator: every terminal repetition, including
	// the ones nothing could judge.
	Attempts int

	// EvaluableAttempts is the filtered denominator: repetitions that
	// produced an actual quality verdict.
	//
	// Both denominators are published, and neither a raw stability nor a raw
	// spread is, because those would mix a quality signal with an
	// infrastructure one into a number nobody should read. The parent
	// design's rule is about the denominators of rates — never discard infra
	// failures from a denominator without showing both views — so what a
	// consumer is owed here is both counts, from which it computes whichever
	// rate it means.
	EvaluableAttempts int

	// Verdicts counts each verdict. Never a derived single verdict.
	Verdicts map[ScoreVerdict]int

	// NumericScores are every published number, **in repetition-index
	// order**. The sequence is the point: a reviewer seeing
	// 0.71 0.74 0.69 0.73 0.41 learns something a median would have hidden.
	NumericScores []float64

	// NumericSpread is max - min over the evaluable numbers: a range, not a
	// standard deviation. At the sample sizes a nightly lane can afford a
	// standard deviation has no useful sampling behavior, while a range is
	// what a reviewer can check by eye against the sequence above.
	NumericSpread float64

	// VerdictStability is modalVerdictCount / evaluableCount. 1.0 means
	// unanimous.
	VerdictStability float64

	// EvaluableEnough reports whether enough repetitions were judgeable for
	// this Cell to be a measurement at all.
	//
	// This is a structural fact: arithmetic on counts, certain without any
	// calibration. One survivor of five repetitions is not a measurement,
	// whatever the survivor said. A Cell that is not EvaluableEnough must
	// never be reported as a pass.
	EvaluableEnough bool

	// NotEvaluableEnoughReason states why, when EvaluableEnough is false.
	NotEvaluableEnoughReason string

	// ExceedsDeclaredLimits reports whether the evaluable repetitions
	// disagreed by more than the policy declared.
	//
	// This is a threshold judgement, and it is only ever as good as the
	// limits it compares against. It is kept apart from EvaluableEnough
	// because the two have different warrants and only one of them can
	// block a result: while LimitsCalibration is uncalibrated, this field
	// is advisory and must not rewrite a Cell from a pass into "not a pass".
	//
	// No framework in the comparison set — inspect_ai, terminal-bench,
	// evals, vitest-evals, Maka — has a gate of this kind. That is a fact
	// about this field, not about EvaluableEnough, whose analogues
	// (insufficient n, an inconclusive A/B test) are ordinary.
	ExceedsDeclaredLimits bool

	// ExceededLimitsReason states which limit, when ExceedsDeclaredLimits.
	ExceededLimitsReason string

	// LimitsCalibration carries the policy's calibration state, so a reader
	// of ExceedsDeclaredLimits cannot see the judgement without also seeing
	// what it is worth.
	LimitsCalibration Calibration
}

// MayBeReportedAsPass applies the one hard reporting rule of design §3.
//
// It reads only the structural half deliberately. A limit breach under an
// uncalibrated policy is a number compared against a guess, and a guess does
// not get to turn a pass into a non-pass; a Cell nobody could judge is a
// different matter and blocks unconditionally. This is not the old merged
// boolean returning under a new name: it is the reporting rule, it never
// consults ExceedsDeclaredLimits while the limits are uncalibrated, and both
// fields stay separately published either way.
func (d CellDistribution) MayBeReportedAsPass() bool {
	if !d.EvaluableEnough {
		return false
	}
	if d.LimitsCalibration == CalibrationCalibrated && d.ExceedsDeclaredLimits {
		return false
	}
	return true
}

// ComputeCellDistribution measures one Cell's repetitions against a policy.
//
// It is pure: no I/O, no store access, no clock. Everything it reports is
// derived from the documents handed to it, so a report's own trustworthiness
// claim can be recomputed offline from the same artifacts.
func ComputeCellDistribution(repetitions []CellRepetition, policy VariancePolicy) (CellDistribution, error) {
	if len(repetitions) == 0 {
		return CellDistribution{}, fmt.Errorf("%w: a Cell distribution needs at least one repetition", errInvalidDocument)
	}
	if err := policy.Validate(); err != nil {
		return CellDistribution{}, fmt.Errorf("eval: variance: %w", err)
	}

	ordered := append([]CellRepetition(nil), repetitions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Attempt.RepetitionIndex < ordered[j].Attempt.RepetitionIndex
	})

	distribution := CellDistribution{
		Attempts: len(ordered),
		Verdicts: make(map[ScoreVerdict]int, 3),
	}

	evaluable := 0
	for _, repetition := range ordered {
		verdict := repetition.Score.Verdict
		distribution.Verdicts[verdict]++

		// An indeterminate repetition is an infrastructure or judgeability
		// signal, not a quality one: a judge that could not be reached or was
		// denied evidence has said nothing about the Subject. It is counted
		// above and excluded from the measurement below.
		if verdict == ScoreIndeterminate {
			continue
		}
		evaluable++
		if repetition.Score.NumericScore != nil {
			distribution.NumericScores = append(distribution.NumericScores, *repetition.Score.NumericScore)
		}
	}

	distribution.EvaluableAttempts = evaluable
	distribution.NumericSpread = spreadOf(distribution.NumericScores)
	distribution.VerdictStability = modalShare(distribution.Verdicts, evaluable)

	distribution.LimitsCalibration = policy.Calibration
	distribution.EvaluableEnough, distribution.NotEvaluableEnoughReason = judgeEvaluability(distribution, policy)
	distribution.ExceedsDeclaredLimits, distribution.ExceededLimitsReason = judgeDeclaredLimits(distribution, policy)
	return distribution, nil
}

// spreadOf is the range of values, or zero when there is nothing to spread.
//
// Zero for an empty set is not a claim of perfect agreement; the caller
// distinguishes "no numbers were published" from "every number agreed" by
// looking at NumericScores, which is why the sequence is published rather
// than only its summary.
func spreadOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	low, high := values[0], values[0]
	for _, value := range values[1:] {
		if value < low {
			low = value
		}
		if value > high {
			high = value
		}
	}
	return high - low
}

// modalShare is the largest verdict count over the evaluable count.
func modalShare(verdicts map[ScoreVerdict]int, evaluable int) float64 {
	if evaluable <= 0 {
		return 0
	}
	modal := 0
	for verdict, count := range verdicts {
		if verdict == ScoreIndeterminate {
			continue
		}
		if count > modal {
			modal = count
		}
	}
	return float64(modal) / float64(evaluable)
}

// limitEpsilon absorbs binary floating-point representation error when a
// value is compared against a declared limit.
//
// It is not a fudge factor. Scores and limits are written as decimals, and
// decimals are not exactly representable in binary: a spread between the
// perfectly ordinary scores 0.6 and 0.8 computes as 0.20000000000000007,
// which is strictly greater than a declared limit of 0.20. Without a
// tolerance, a policy would be one notch stricter than it reads and an
// operator would get a spurious breach from values that visibly agree with
// the limit they wrote.
//
// 1e-9 is far below any difference these documents can express meaningfully
// — Scores live in [0,1] and come from JSON — so it cannot mask a real
// breach.
const limitEpsilon = 1e-9

// exceeds reports whether value is above limit, treating the limit as
// inclusive within representation error.
func exceeds(value, limit float64) bool { return value > limit+limitEpsilon }

// falls reports whether value is below limit, on the same terms.
func falls(value, limit float64) bool { return value < limit-limitEpsilon }

// judgeEvaluability answers the structural question: were enough
// repetitions judgeable for this to be a measurement?
//
// It is deliberately a separate function from judgeDeclaredLimits, and its
// answer is deliberately a separate field. The two questions have different
// warrants — this one is certain, that one is only as good as an
// uncalibrated number — and an earlier revision that merged them into one
// boolean with a joined reason string reported a Cell with a single
// evaluable repetition of five as perfectly trustworthy.
// Two bugs found by probing this file's own first implementation forced the
// separation. With nothing evaluable, the modal share is not 0.0 — it is
// undefined — and reporting "stability 0.0000 is below 0.8000" tells an
// operator the Cell was judged inconsistently when it was never judged at
// all. And with a single survivor among four unjudgeable repetitions,
// stability computes to a perfect 1/1 and spread to 0 from one number,
// reproducing the "perfect stability, measured never" hazard through a
// configuration that looks entirely correct — the policy document's own
// two-repetition floor cannot see this, because the shortfall happens at run
// time rather than in the configuration.
func judgeEvaluability(distribution CellDistribution, policy VariancePolicy) (bool, string) {
	if distribution.EvaluableAttempts >= policy.MinEvaluableRepetitions {
		return true, ""
	}
	if distribution.EvaluableAttempts == 0 {
		return false, fmt.Sprintf(
			"no repetition was evaluable; %d of %d could not be judged",
			distribution.Verdicts[ScoreIndeterminate], distribution.Attempts)
	}
	return false, fmt.Sprintf(
		"only %d of %d repetitions were evaluable, below the declared minimum %d",
		distribution.EvaluableAttempts, distribution.Attempts, policy.MinEvaluableRepetitions)
}

// judgeDeclaredLimits answers the threshold question: did the evaluable
// repetitions disagree by more than the policy declared?
//
// Both comparisons treat a declared limit as inclusive: a value exactly at
// the limit is within it. Getting that backwards would silently make every
// policy one notch stricter than it reads.
//
// The answer does not depend on the policy's calibration state — an
// uncalibrated limit is still a limit, and hiding the comparison would leave
// an operator unable to see the number they wrote being crossed. What
// calibration governs is what the answer is allowed to do, which is
// MayBeReportedAsPass's business, not this function's.
func judgeDeclaredLimits(distribution CellDistribution, policy VariancePolicy) (bool, string) {
	// Spread and stability are claims about how the evaluable repetitions
	// agreed, so they are only made when there were any. Stating them for an
	// empty set would be asserting something about a measurement that never
	// happened; that Cell's problem is structural and judgeEvaluability has
	// already named it.
	if distribution.EvaluableAttempts == 0 {
		return false, ""
	}

	var reasons []string
	if exceeds(distribution.NumericSpread, policy.MaxNumericSpread) {
		reasons = append(reasons, fmt.Sprintf(
			"numeric spread %.4f exceeds the declared maximum %.4f",
			distribution.NumericSpread, policy.MaxNumericSpread))
	}
	if falls(distribution.VerdictStability, policy.MinVerdictStability) {
		reasons = append(reasons, fmt.Sprintf(
			"verdict stability %.4f is below the declared minimum %.4f",
			distribution.VerdictStability, policy.MinVerdictStability))
	}
	if len(reasons) == 0 {
		return false, ""
	}
	return true, strings.Join(reasons, "; ")
}

// hasMergedReliabilityField reports whether CellDistribution has grown a
// field that merges the two reliability answers back into one.
//
// It exists for the same reason as hasVerdictField: the separation of a
// structural fact from an uncalibrated threshold judgement is a design
// decision that a later convenience field would quietly undo. A consumer
// branches on a boolean and does not read the reason string beside it, so a
// single "trustworthy" flag is not a summary of the two fields — it is a
// replacement for them.
func hasMergedReliabilityField(distribution CellDistribution) bool {
	value := reflect.TypeOf(distribution)
	for i := 0; i < value.NumField(); i++ {
		switch value.Field(i).Name {
		case "Trustworthy", "Untrustworthy", "UntrustworthyReason", "Reliable":
			return true
		}
	}
	return false
}

// hasVerdictField reports whether CellDistribution has grown a single
// derived verdict field.
//
// It exists so the design's central refusal is enforced by a test rather
// than by everyone remembering: repetitions are reported as a distribution
// and never collapsed, so a field named for one verdict would be the shape
// of exactly the collapse this design rejects.
func hasVerdictField(distribution CellDistribution) bool {
	value := reflect.TypeOf(distribution)
	for i := 0; i < value.NumField(); i++ {
		name := value.Field(i).Name
		if name == "Verdict" || name == "CellVerdict" || name == "Result" {
			return true
		}
	}
	return false
}
