package eval

import (
	"math"
	"strings"
	"testing"
)

// repetition builds one Cell repetition. Every repetition of a Cell shares
// its identity digests by construction here; Task 4 owns proving that a
// disagreement is refused.
func repetition(index int, verdict ScoreVerdict, numeric *float64) CellRepetition {
	return CellRepetition{
		Attempt: Attempt{
			ScenarioDigest:  "sha256:scenario",
			SubjectDigest:   "sha256:subject",
			ExecutorDigest:  "sha256:executor",
			RepetitionIndex: index,
		},
		Score: Score{Verdict: verdict, NumericScore: numeric},
	}
}

func score(value float64) *float64 { return &value }

func mustDistribution(t *testing.T, reps []CellRepetition, policy VariancePolicy) CellDistribution {
	t.Helper()
	distribution, err := ComputeCellDistribution(reps, policy)
	if err != nil {
		t.Fatalf("ComputeCellDistribution: %v", err)
	}
	return distribution
}

// TestDistributionPublishesEveryScoreInRepetitionOrder is the design's core
// commitment made concrete. A reviewer must be able to see the actual
// sequence — 0.71 0.74 0.69 0.73 0.41 — rather than a summary of it, because
// the last value is the whole story and a median hides it.
func TestDistributionPublishesEveryScoreInRepetitionOrder(t *testing.T) {
	// Deliberately supplied out of order, to prove the ordering is by
	// repetition index rather than by arrival.
	reps := []CellRepetition{
		repetition(3, ScorePass, score(0.73)),
		repetition(0, ScorePass, score(0.71)),
		repetition(4, ScoreFail, score(0.41)),
		repetition(2, ScorePass, score(0.69)),
		repetition(1, ScorePass, score(0.74)),
	}
	distribution := mustDistribution(t, reps, validVariancePolicy())

	want := []float64{0.71, 0.74, 0.69, 0.73, 0.41}
	if len(distribution.NumericScores) != len(want) {
		t.Fatalf("NumericScores = %v, want %v", distribution.NumericScores, want)
	}
	for i, value := range want {
		if math.Abs(distribution.NumericScores[i]-value) > 1e-9 {
			t.Fatalf("NumericScores = %v, want %v (differs at index %d)", distribution.NumericScores, want, i)
		}
	}
	if distribution.Attempts != 5 {
		t.Fatalf("Attempts = %d, want 5", distribution.Attempts)
	}
}

// TestDistributionReportsVerdictCountsNotADerivedVerdict: 4-pass/1-fail and
// 5-pass are different facts and must stay different. Collapsing them is what
// this design rejects.
func TestDistributionReportsVerdictCountsNotADerivedVerdict(t *testing.T) {
	fourOne := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.7)),
		repetition(1, ScorePass, score(0.7)),
		repetition(2, ScorePass, score(0.7)),
		repetition(3, ScorePass, score(0.7)),
		repetition(4, ScoreFail, score(0.7)),
	}, validVariancePolicy())
	fiveZero := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.7)),
		repetition(1, ScorePass, score(0.7)),
		repetition(2, ScorePass, score(0.7)),
		repetition(3, ScorePass, score(0.7)),
		repetition(4, ScorePass, score(0.7)),
	}, validVariancePolicy())

	if fourOne.Verdicts[ScorePass] != 4 || fourOne.Verdicts[ScoreFail] != 1 {
		t.Fatalf("Verdicts = %v, want 4 pass and 1 fail", fourOne.Verdicts)
	}
	if fiveZero.Verdicts[ScorePass] != 5 {
		t.Fatalf("Verdicts = %v, want 5 pass", fiveZero.Verdicts)
	}
	if fourOne.VerdictStability == fiveZero.VerdictStability {
		t.Fatal("a 4/1 split and a 5/0 sweep produced the same stability; they are different facts")
	}
}

// TestNumericSpreadIsTheRangeNotAStandardDeviation pins the design's stated
// choice. At N in single digits a standard deviation has no useful sampling
// behavior, while a range is what a reviewer can check by eye against the
// published sequence.
func TestNumericSpreadIsTheRangeNotAStandardDeviation(t *testing.T) {
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.20)),
		repetition(1, ScorePass, score(0.50)),
		repetition(2, ScorePass, score(0.90)),
	}, validVariancePolicy())

	const wantRange = 0.70 // 0.90 - 0.20
	if math.Abs(distribution.NumericSpread-wantRange) > 1e-9 {
		t.Fatalf("NumericSpread = %v, want the range %v", distribution.NumericSpread, wantRange)
	}
	// The population standard deviation of this sample is ~0.286; a spread
	// near that value would mean the wrong statistic is being reported.
	if math.Abs(distribution.NumericSpread-0.286) < 0.05 {
		t.Fatalf("NumericSpread = %v looks like a standard deviation, not a range", distribution.NumericSpread)
	}
}

func TestVerdictStabilityIsTheModalShareOfEvaluableRepetitions(t *testing.T) {
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.7)),
		repetition(1, ScorePass, score(0.7)),
		repetition(2, ScorePass, score(0.7)),
		repetition(3, ScoreFail, score(0.7)),
	}, validVariancePolicy())

	if math.Abs(distribution.VerdictStability-0.75) > 1e-9 {
		t.Fatalf("VerdictStability = %v, want 0.75 (3 of 4)", distribution.VerdictStability)
	}

	unanimous := mustDistribution(t, []CellRepetition{
		repetition(0, ScoreFail, score(0.1)),
		repetition(1, ScoreFail, score(0.1)),
	}, validVariancePolicy())
	if unanimous.VerdictStability != 1 {
		t.Fatalf("VerdictStability = %v for a unanimous Cell, want 1", unanimous.VerdictStability)
	}
}

// TestALimitIsAMaximumSoEqualityDoesNotBreach: a value exactly at the
// declared limit is within it. Getting this backwards would make every
// policy one notch stricter than it reads.
func TestALimitIsAMaximumSoEqualityDoesNotBreach(t *testing.T) {
	// The evaluable floor is lowered to two here so this test measures only
	// what it claims to. With the default floor of three, a two-repetition
	// fixture is untrustworthy for a different and correct reason, and the
	// limit comparison would never be reached.
	policy := validVariancePolicy() // maxNumericSpread 0.20, minVerdictStability 0.80
	policy.MinEvaluableRepetitions = 2

	exactSpread := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.60)),
		repetition(1, ScorePass, score(0.80)), // spread exactly 0.20
	}, policy)
	if exactSpread.ExceedsDeclaredLimits {
		t.Fatalf("a spread exactly at the limit was reported as exceeding it: %+v", exactSpread)
	}

	exactStability := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScorePass, score(0.70)),
		repetition(2, ScorePass, score(0.70)),
		repetition(3, ScorePass, score(0.70)),
		repetition(4, ScoreFail, score(0.70)), // stability exactly 0.80
	}, policy)
	if exactStability.ExceedsDeclaredLimits {
		t.Fatalf("a stability exactly at the limit was reported as exceeding it: %+v", exactStability)
	}
}

// TestSpreadBreachSetsExceedsDeclaredLimitsNotAFailingVerdict is the
// distinction the design rests on: a limit breach is a statement about the
// measurement, never a third verdict about the Subject.
func TestSpreadBreachSetsExceedsDeclaredLimitsNotAFailingVerdict(t *testing.T) {
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.71)),
		repetition(1, ScorePass, score(0.74)),
		repetition(2, ScorePass, score(0.69)),
		repetition(3, ScorePass, score(0.73)),
		repetition(4, ScorePass, score(0.41)), // spread 0.33 > 0.20
	}, validVariancePolicy())

	if !distribution.ExceedsDeclaredLimits {
		t.Fatalf("a 0.33 spread under a 0.20 limit was reported as within it: %+v", distribution)
	}
	if distribution.ExceededLimitsReason == "" {
		t.Fatal("a Cell over its declared limits carries no stated reason")
	}
	// The breach is real and reported, and the repetitions were plentiful and
	// judgeable, so the structural half must stay clean. Merging the two
	// would lose exactly this distinction.
	if !distribution.EvaluableEnough {
		t.Fatalf("five judged repetitions were called structurally insufficient: %+v", distribution)
	}
	// Every verdict here was a pass. The Cell is untrustworthy, and that must
	// not have been achieved by rewriting what the judge said.
	if distribution.Verdicts[ScorePass] != 5 {
		t.Fatalf("Verdicts = %v; the underlying verdicts must be reported unchanged", distribution.Verdicts)
	}
}

func TestVerdictInstabilityAlsoExceedsTheDeclaredLimits(t *testing.T) {
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScoreFail, score(0.70)),
		repetition(2, ScorePass, score(0.70)),
		repetition(3, ScoreFail, score(0.70)), // stability 0.50 < 0.80
	}, validVariancePolicy())

	if !distribution.ExceedsDeclaredLimits {
		t.Fatalf("a 0.50 stability under a 0.80 floor was reported as within it: %+v", distribution)
	}
}

// TestDistributionCarriesNoDerivedCellVerdict is a shape guard. The design
// deliberately has no such field, and a consumer wanting one must decide its
// own rule and say so rather than finding it pre-decided here.
func TestDistributionCarriesNoDerivedCellVerdict(t *testing.T) {
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.7)),
		repetition(1, ScorePass, score(0.7)),
	}, validVariancePolicy())

	if hasVerdictField(distribution) {
		t.Fatal("CellDistribution grew a single derived verdict field; repetitions are reported, never collapsed")
	}
}

// TestTheTwoReliabilityFieldsAreNeverMergedIntoOne is the second shape guard.
//
// The split of a structural fact from an uncalibrated threshold judgement is
// a design decision that a later convenience field would quietly undo, and
// the failure is not hypothetical: an earlier revision carried one
// Trustworthy bool with a joined reason string, and reported a Cell with one
// evaluable repetition of five as trustworthy with perfect stability.
func TestTheTwoReliabilityFieldsAreNeverMergedIntoOne(t *testing.T) {
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.7)),
		repetition(1, ScorePass, score(0.7)),
		repetition(2, ScorePass, score(0.7)),
	}, validVariancePolicy())

	if hasMergedReliabilityField(distribution) {
		t.Fatal("CellDistribution grew a merged reliability field; a consumer branches on the bool and never reads the reason beside it")
	}
}

// TestAnUncalibratedLimitBreachDoesNotBlockReportingACellAsAPass is design
// §3.2's rule, and it is the reason the two fields differ in power rather
// than only in name.
//
// No limit in this repository has been calibrated — no run against a live
// model has ever happened here — so exceedsDeclaredLimits compares a real
// measurement against a guess. A guess is worth reporting and is not worth
// overriding a verdict with. A shortfall in evaluable repetitions, by
// contrast, is arithmetic and blocks unconditionally.
func TestAnUncalibratedLimitBreachDoesNotBlockReportingACellAsAPass(t *testing.T) {
	uncalibrated := validVariancePolicy()
	if uncalibrated.Calibration != CalibrationUncalibrated {
		t.Fatalf("Calibration = %q; this test needs an uncalibrated policy", uncalibrated.Calibration)
	}

	breached := []CellRepetition{
		repetition(0, ScorePass, score(0.71)),
		repetition(1, ScorePass, score(0.74)),
		repetition(2, ScorePass, score(0.69)),
		repetition(3, ScorePass, score(0.73)),
		repetition(4, ScorePass, score(0.41)), // spread 0.33 > 0.20
	}

	distribution := mustDistribution(t, breached, uncalibrated)
	if !distribution.ExceedsDeclaredLimits {
		t.Fatalf("the breach must still be reported, not suppressed: %+v", distribution)
	}
	if distribution.LimitsCalibration != CalibrationUncalibrated {
		t.Fatalf("LimitsCalibration = %q; a reader of the judgement must see what it is worth",
			distribution.LimitsCalibration)
	}
	if !distribution.MayBeReadAsAResult() {
		t.Fatalf("an uncalibrated limit rewrote a Cell of five passes into a non-pass: %+v", distribution)
	}

	// Once the limits are earned from a real run, the same breach does bite.
	calibrated := uncalibrated
	calibrated.Calibration = CalibrationCalibrated
	calibrated.CalibratedFrom = "run-2026-09-05-01"
	earned := mustDistribution(t, breached, calibrated)
	if earned.MayBeReadAsAResult() {
		t.Fatalf("a calibrated limit breach must block a pass: %+v", earned)
	}
}

// TestScoresWithoutNumericValuesStillYieldAVerdictDistribution: a judge may
// return a verdict without a number, and that must not be an error or a
// silent zero.
func TestScoresWithoutNumericValuesStillYieldAVerdictDistribution(t *testing.T) {
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, nil),
		repetition(1, ScoreFail, nil),
	}, validVariancePolicy())

	if len(distribution.NumericScores) != 0 {
		t.Fatalf("NumericScores = %v, want none when no Score carried a number", distribution.NumericScores)
	}
	if distribution.NumericSpread != 0 {
		t.Fatalf("NumericSpread = %v with no numbers to spread", distribution.NumericSpread)
	}
	if distribution.Verdicts[ScorePass] != 1 || distribution.Verdicts[ScoreFail] != 1 {
		t.Fatalf("Verdicts = %v, want the verdicts to still be counted", distribution.Verdicts)
	}
}

func TestComputeCellDistributionRefusesAnEmptyCell(t *testing.T) {
	if _, err := ComputeCellDistribution(nil, validVariancePolicy()); err == nil {
		t.Fatal("an empty Cell produced a distribution")
	}
}

func TestComputeCellDistributionRefusesAnInvalidPolicy(t *testing.T) {
	policy := validVariancePolicy()
	policy.MaxNumericSpread = 0
	if _, err := ComputeCellDistribution([]CellRepetition{repetition(0, ScorePass, score(0.5))}, policy); err == nil {
		t.Fatal("an invalid policy was accepted; the limits it fails to declare cannot be applied")
	}
}

// TestNoEvaluableRepetitionsIsNamedAsSuchNotAsInstability is the first of the
// two bugs a probe found in this file's own first implementation.
//
// With every repetition indeterminate there is no stability to report: the
// modal share of zero evaluable repetitions is not 0.0, it is undefined. The
// first implementation reported "verdict stability 0.0000 is below the
// declared minimum 0.8000", which tells an operator the Cell was judged
// inconsistently when in truth it was never judged at all. The design says
// plainly that "mostly unjudgeable" and "judged inconsistently" are different
// problems that must not share a label.
func TestNoEvaluableRepetitionsIsNamedAsSuchNotAsInstability(t *testing.T) {
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScoreIndeterminate, nil),
		repetition(1, ScoreIndeterminate, nil),
		repetition(2, ScoreIndeterminate, nil),
	}, validVariancePolicy())

	if distribution.EvaluableEnough {
		t.Fatal("a Cell nothing could judge was called structurally sufficient")
	}
	if !strings.Contains(distribution.NotEvaluableEnoughReason, "evaluable") {
		t.Fatalf("reason = %q; it must name the real problem", distribution.NotEvaluableEnoughReason)
	}
	// The threshold half must stay silent rather than inventing a stability
	// claim about a measurement that never happened. Separate fields make
	// this checkable; a single joined reason string did not.
	if distribution.ExceedsDeclaredLimits {
		t.Fatalf("reason = %q; a Cell with nothing evaluable must not be reported as unstable",
			distribution.ExceededLimitsReason)
	}
	if distribution.ExceededLimitsReason != "" {
		t.Fatalf("ExceededLimitsReason = %q, want empty for a Cell with nothing evaluable",
			distribution.ExceededLimitsReason)
	}
}

// TestTooFewEvaluableRepetitionsFailsEvaluableEnough is the second and sharper
// bug: the single-sample hazard reappearing through a door the policy
// document's own guard does not cover.
//
// Task 1 refuses a policy declaring fewer than two evaluable repetitions, and
// Task 4 will refuse an EvalSet declaring repetitionCount: 1 under a policy.
// Neither helps here. A Cell can be configured to run five times, have four
// of them come back unjudgeable, and compute a perfect stability of 1/1 and a
// spread of 0 from the single survivor — the same "perfect stability,
// measured never" this whole mechanism exists to prevent, arrived at through
// a configuration that looks entirely correct.
func TestTooFewEvaluableRepetitionsFailsEvaluableEnough(t *testing.T) {
	policy := validVariancePolicy() // minEvaluableRepetitions 3
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScoreIndeterminate, nil),
		repetition(2, ScoreIndeterminate, nil),
		repetition(3, ScoreIndeterminate, nil),
		repetition(4, ScoreIndeterminate, nil),
	}, policy)

	if distribution.EvaluableEnough {
		t.Fatalf("one evaluable repetition of five was called structurally sufficient: %+v", distribution)
	}
	if !strings.Contains(distribution.NotEvaluableEnoughReason, "evaluable") {
		t.Fatalf("reason = %q; it must name the shortfall", distribution.NotEvaluableEnoughReason)
	}
	// The single survivor agreed with itself perfectly, so the threshold half
	// sees nothing wrong. That is precisely why it cannot be the field a
	// consumer reads: only the structural half knows this was never measured.
	if distribution.ExceedsDeclaredLimits {
		t.Fatalf("the lone survivor's own perfect agreement was reported as a limit breach: %+v", distribution)
	}
	if distribution.MayBeReadAsAResult() {
		t.Fatalf("a Cell measured once out of five may not be reported as a pass: %+v", distribution)
	}
}

// TestBothDenominatorsArePublished. The parent design's rule is about the
// denominators of rates — never discard infra failures from a denominator
// without showing both views — so what this type owes a consumer is both
// counts, not a second statistic.
//
// A "raw stability" would mix a quality signal with an infrastructure one
// into a number nobody should read, which is why it is deliberately absent.
func TestBothDenominatorsArePublished(t *testing.T) {
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScorePass, score(0.72)),
		repetition(2, ScorePass, score(0.71)),
		repetition(3, ScoreIndeterminate, nil),
	}, validVariancePolicy())

	if distribution.Attempts != 4 {
		t.Fatalf("Attempts = %d, want the raw denominator 4", distribution.Attempts)
	}
	if distribution.EvaluableAttempts != 3 {
		t.Fatalf("EvaluableAttempts = %d, want the filtered denominator 3", distribution.EvaluableAttempts)
	}
	if distribution.Verdicts[ScoreIndeterminate] != 1 {
		t.Fatalf("Verdicts = %v; indeterminate repetitions must still be counted, not dropped",
			distribution.Verdicts)
	}
}

// TestEvaluableShortfallAndInstabilityAreReportedSeparately keeps the two
// answers from being merged once both can occur. They are separate fields
// with separate reasons, because they have separate warrants: the shortfall
// is certain arithmetic, the instability is a comparison against a number
// nobody has calibrated.
func TestEvaluableShortfallAndInstabilityAreReportedSeparately(t *testing.T) {
	policy := validVariancePolicy()
	policy.MinEvaluableRepetitions = 4
	distribution := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScoreFail, score(0.70)),
		repetition(2, ScoreIndeterminate, nil),
	}, policy)

	if distribution.EvaluableEnough {
		t.Fatal("a Cell short of evaluable repetitions was called structurally sufficient")
	}
	if !distribution.ExceedsDeclaredLimits {
		t.Fatal("a Cell whose evaluable repetitions disagreed was reported as within its limits")
	}
	if !strings.Contains(distribution.NotEvaluableEnoughReason, "evaluable") {
		t.Fatalf("structural reason = %q, want the shortfall named", distribution.NotEvaluableEnoughReason)
	}
	if !strings.Contains(distribution.ExceededLimitsReason, "stability") {
		t.Fatalf("threshold reason = %q, want the instability named", distribution.ExceededLimitsReason)
	}
}
