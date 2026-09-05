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

// TestAnUntrustworthyArmMakesTheComparisonUnusable: comparing against a
// measurement already known to be unreliable produces a delta that means
// nothing.
func TestAnUntrustworthyArmMakesTheComparisonUnusable(t *testing.T) {
	policy := validVariancePolicy() // maxNumericSpread 0.20
	unstable := mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.10)),
		repetition(1, ScorePass, score(0.90)),
		repetition(2, ScorePass, score(0.50)),
	}, policy)
	if unstable.Trustworthy {
		t.Fatal("fixture is not actually untrustworthy")
	}

	_, err := ComparePairedDistributions(unstable, armOf(t, 0.70, 0.71, 0.72))
	if !errors.Is(err, errInvalidDocument) {
		t.Fatalf("ComparePairedDistributions() = %v, want an untrustworthy arm to be refused", err)
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
