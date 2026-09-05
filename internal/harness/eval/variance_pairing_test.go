package eval

import (
	"errors"
	"math"
	"testing"
)

func armOf(t *testing.T, values ...float64) CellDistribution {
	t.Helper()
	reps := make([]CellRepetition, 0, len(values))
	for i, value := range values {
		reps = append(reps, repetition(i, ScorePass, score(value)))
	}
	policy := validVariancePolicy()
	policy.MaxNumericSpread = 1
	policy.MinEvaluableRepetitions = 2
	return mustDistribution(t, reps, policy)
}

// TestPairedDeltaIsComputedBetweenDistributionsNotSingleScores.
func TestPairedDeltaIsComputedBetweenDistributionsNotSingleScores(t *testing.T) {
	baseline := armOf(t, 0.50, 0.52, 0.51)  // median 0.51
	candidate := armOf(t, 0.70, 0.72, 0.71) // median 0.71

	delta, err := ComparePairedDistributions(baseline, candidate)
	if err != nil {
		t.Fatalf("ComparePairedDistributions: %v", err)
	}
	if math.Abs(delta.MedianDelta-0.20) > 1e-9 {
		t.Fatalf("MedianDelta = %v, want 0.20", delta.MedianDelta)
	}
	if delta.WithinNoise {
		t.Fatalf("a 0.20 delta against spreads of ~0.02 was called within noise: %+v", delta)
	}
}

// TestADeltaSmallerThanTheWiderArmsSpreadIsPublishedAsWithinNoise.
//
// "This run cannot distinguish the arms" is a different claim from "no
// difference exists", and only the first is honest at these sample sizes.
func TestADeltaSmallerThanTheWiderArmsSpreadIsPublishedAsWithinNoise(t *testing.T) {
	baseline := armOf(t, 0.40, 0.80, 0.60)  // spread 0.40, median 0.60
	candidate := armOf(t, 0.62, 0.64, 0.63) // spread 0.02, median 0.63

	delta, err := ComparePairedDistributions(baseline, candidate)
	if err != nil {
		t.Fatalf("ComparePairedDistributions: %v", err)
	}
	if !delta.WithinNoise {
		t.Fatalf("a 0.03 delta against a 0.40 spread was not called within noise: %+v", delta)
	}
	if delta.WithinNoiseReason == "" {
		t.Fatal("a within-noise result carries no stated reason")
	}
}

// TestWithinNoiseUsesTheWiderArmNotAnAverage: averaging the two spreads would
// let a tight candidate arm mask a wildly variable baseline, and declare a
// difference this run cannot actually see.
func TestWithinNoiseUsesTheWiderArmNotAnAverage(t *testing.T) {
	baseline := armOf(t, 0.40, 0.80, 0.60)  // spread 0.40
	candidate := armOf(t, 0.68, 0.70, 0.69) // spread 0.02, median 0.69

	delta, err := ComparePairedDistributions(baseline, candidate)
	if err != nil {
		t.Fatalf("ComparePairedDistributions: %v", err)
	}
	// Delta is 0.09. Against the wider arm (0.40) it is noise; against the
	// mean of the two spreads (0.21) it would still be noise, but against the
	// narrower arm (0.02) it would look like a real difference. The wider arm
	// is the honest comparator.
	if !delta.WithinNoise {
		t.Fatalf("delta %v against the wider spread %v was not called within noise",
			delta.MedianDelta, delta.WiderSpread)
	}
	if math.Abs(delta.WiderSpread-0.40) > 1e-9 {
		t.Fatalf("WiderSpread = %v, want the baseline arm's 0.40", delta.WiderSpread)
	}
}

// TestAnUnmeasuredArmMakesTheComparisonUnusable: comparing against a Cell
// nobody could judge produces a delta that means nothing. This is the
// structural half, and it refuses unconditionally.
func TestAnUnmeasuredArmMakesTheComparisonUnusable(t *testing.T) {
	policy := validVariancePolicy() // minEvaluableRepetitions 3
	barelyJudged := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScoreIndeterminate, nil),
		repetition(2, ScoreIndeterminate, nil),
	}, policy)
	if barelyJudged.EvaluableEnough {
		t.Fatal("fixture is not actually short of evaluable repetitions")
	}

	_, err := ComparePairedDistributions(barelyJudged, armOf(t, 0.70, 0.71, 0.72))
	if !errors.Is(err, errInvalidDocument) {
		t.Fatalf("ComparePairedDistributions() = %v, want an unmeasured arm to be refused", err)
	}
}

// TestAWideArmUnderAnUncalibratedLimitIsDisclosedRatherThanRefused is design
// §3.2 reaching the paired comparison.
//
// A spread of 0.80 against a declared 0.20 is a real measurement compared
// against a number nobody has calibrated. Refusing the comparison would let
// that guess decide what a reviewer is allowed to see. The mechanism already
// has an honest answer for a noisy arm — the delta comes back WithinNoise,
// which says "this run cannot distinguish the arms" without pretending to
// know the arms are the same. Disclosure replaces refusal until the limits
// are earned.
func TestAWideArmUnderAnUncalibratedLimitIsDisclosedRatherThanRefused(t *testing.T) {
	policy := validVariancePolicy() // maxNumericSpread 0.20, uncalibrated
	wide := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.10)),
		repetition(1, ScorePass, score(0.90)),
		repetition(2, ScorePass, score(0.50)),
	}, policy)
	if !wide.ExceedsDeclaredLimits {
		t.Fatal("fixture does not actually exceed its declared spread")
	}

	delta, err := ComparePairedDistributions(wide, armOf(t, 0.70, 0.71, 0.72))
	if err != nil {
		t.Fatalf("ComparePairedDistributions() = %v; an uncalibrated limit must not refuse a comparison", err)
	}
	if !delta.WithinNoise {
		t.Fatalf("a delta against a 0.80 spread was not called within noise: %+v", delta)
	}
}

// TestAWideArmUnderACalibratedLimitIsRefused is the other half of the same
// rule: once the limits are earned from a cited run, the breach does bite.
func TestAWideArmUnderACalibratedLimitIsRefused(t *testing.T) {
	policy := validVariancePolicy()
	policy.Calibration = CalibrationCalibrated
	policy.CalibratedFrom = "run-2026-09-05-01"
	wide := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.10)),
		repetition(1, ScorePass, score(0.90)),
		repetition(2, ScorePass, score(0.50)),
	}, policy)

	_, err := ComparePairedDistributions(wide, armOf(t, 0.70, 0.71, 0.72))
	if !errors.Is(err, errInvalidDocument) {
		t.Fatalf("ComparePairedDistributions() = %v, want a calibrated breach to be refused", err)
	}
}

func TestComparePairedDistributionsRefusesAnArmWithNoNumbers(t *testing.T) {
	policy := validVariancePolicy()
	policy.MinEvaluableRepetitions = 2
	noNumbers := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, nil),
		repetition(1, ScorePass, nil),
	}, policy)

	if _, err := ComparePairedDistributions(noNumbers, armOf(t, 0.7, 0.7)); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("err = %v, want an arm with no numeric scores to be refused", err)
	}
}
