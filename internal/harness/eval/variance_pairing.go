package eval

import "fmt"

// PairedDelta is the comparison between two arms of one paired run.
//
// The delta is between **distributions**, not between single Scores. Two
// arms each ran several times, and comparing one number from each would throw
// away the only information that says whether the difference is real.
type PairedDelta struct {
	// MedianDelta is the candidate arm's evaluable median minus the
	// baseline arm's.
	MedianDelta float64

	// WiderSpread is the larger of the two arms' spreads, and the comparator
	// WithinNoise is judged against.
	WiderSpread float64

	// WithinNoise reports that this run cannot distinguish the arms.
	//
	// That is deliberately not the same claim as "no difference exists". At
	// these sample sizes the honest statement is about what the run can see,
	// and reporting the stronger claim would turn an absence of evidence into
	// evidence of absence.
	WithinNoise bool

	// WithinNoiseReason states the comparison in words.
	WithinNoiseReason string
}

// ComparePairedDistributions compares a baseline arm with a candidate arm.
//
// Both arms must be trustworthy and carry numbers. Comparing against a
// measurement already known to be unreliable produces a delta that means
// nothing, and returning it anyway would invite exactly the reading this
// mechanism exists to prevent.
func ComparePairedDistributions(baseline, candidate CellDistribution) (PairedDelta, error) {
	for name, arm := range map[string]CellDistribution{"baseline": baseline, "candidate": candidate} {
		if !arm.Trustworthy {
			return PairedDelta{}, fmt.Errorf(
				"%w: the %s arm is untrustworthy (%s); a delta against it would mean nothing",
				errInvalidDocument, name, arm.UntrustworthyReason)
		}
		if len(arm.NumericScores) == 0 {
			return PairedDelta{}, fmt.Errorf(
				"%w: the %s arm published no numeric scores to compare", errInvalidDocument, name)
		}
	}

	delta := PairedDelta{
		MedianDelta: medianOf(candidate.NumericScores) - medianOf(baseline.NumericScores),
		WiderSpread: baseline.NumericSpread,
	}
	if candidate.NumericSpread > delta.WiderSpread {
		delta.WiderSpread = candidate.NumericSpread
	}

	// The wider arm, never an average of the two. Averaging would let a tight
	// candidate arm mask a wildly variable baseline and declare a difference
	// this run cannot actually see.
	magnitude := delta.MedianDelta
	if magnitude < 0 {
		magnitude = -magnitude
	}
	if !exceeds(magnitude, delta.WiderSpread) {
		delta.WithinNoise = true
		delta.WithinNoiseReason = fmt.Sprintf(
			"median delta %.4f does not exceed the wider arm's own spread %.4f; this run cannot distinguish the arms, which is not the same as finding no difference",
			delta.MedianDelta, delta.WiderSpread)
	}
	return delta, nil
}
