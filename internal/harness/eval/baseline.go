package eval

import (
	"fmt"
	"sort"
	"time"
)

// SchemaBaseline names the pinned historical baseline document.
const SchemaBaseline = "och.eval.baseline"

// CellIdentity is what makes two measurements comparable: the frozen
// digests of what was run. A baseline recorded for one identity says nothing
// about another, which is why a comparison matches on all three or not at
// all.
type CellIdentity struct {
	ScenarioDigest Digest `json:"scenarioDigest"`
	SubjectDigest  Digest `json:"subjectDigest"`
	ExecutorDigest Digest `json:"executorDigest"`
}

// BaselineCell is one Cell's recorded distribution.
type BaselineCell struct {
	ScenarioDigest Digest `json:"scenarioDigest"`
	SubjectDigest  Digest `json:"subjectDigest"`
	ExecutorDigest Digest `json:"executorDigest"`

	Attempts          int       `json:"attempts"`
	EvaluableAttempts int       `json:"evaluableAttempts"`
	NumericScores     []float64 `json:"numericScores,omitempty"`
	NumericSpread     float64   `json:"numericSpread"`
	VerdictStability  float64   `json:"verdictStability"`

	// AttemptIDs names what this record was derived from. A baseline nobody
	// can trace back to real Attempts is an assertion, not evidence.
	AttemptIDs []AttemptID `json:"attemptIds"`
}

// Baseline is one `och.eval.baseline` document: a previous run's published
// distributions, pinned so later runs can be compared against them.
//
// It is written only by an explicit regeneration command and reviewed like
// any other commit. A lane that rewrote its own baseline whenever it drifted
// would measure nothing at all.
type Baseline struct {
	FormatVersion int       `json:"formatVersion"`
	Schema        string    `json:"schema"`
	ID            string    `json:"id"`
	RecordedAt    time.Time `json:"recordedAt"`

	Cells []BaselineCell `json:"cells"`
}

// BaselineComparison is one Cell's result against a baseline.
type BaselineComparison struct {
	// Matched reports whether a baseline record for this exact identity was
	// found.
	Matched bool

	// UnmatchedReason states why not. An unmatched baseline is a fact a
	// reviewer needs — the usual cause is that the Scenario or Subject was
	// edited — so it is reported rather than read as "no baseline".
	UnmatchedReason string

	// Stale reports that the baseline is older than the declared bound. It
	// is disclosed and the comparison is still shown; staleness never
	// silently drops a comparison.
	Stale bool

	// Regressed reports a current median below the baseline's, and is only
	// ever set when Matched.
	Regressed bool

	// MedianDelta is current minus baseline, over evaluable medians.
	MedianDelta float64
}

// DecodeBaseline strictly decodes and validates one document.
func DecodeBaseline(data []byte) (Baseline, error) {
	var baseline Baseline
	if err := decodeStrict(data, &baseline); err != nil {
		return Baseline{}, fmt.Errorf("eval: baseline: %w", err)
	}
	if baseline.Schema != SchemaBaseline {
		return Baseline{}, fmt.Errorf("eval: baseline: %w: %q", errUnsupportedSchema, baseline.Schema)
	}
	if baseline.FormatVersion != FormatVersion {
		return Baseline{}, fmt.Errorf("eval: baseline: %w: %d", errUnsupportedFormatVersion, baseline.FormatVersion)
	}
	if err := baseline.Validate(); err != nil {
		return Baseline{}, fmt.Errorf("eval: baseline: %w", err)
	}
	return baseline, nil
}

// BaselineDigest is the canonical digest over a validated document.
func BaselineDigest(baseline Baseline) (Digest, error) {
	if err := baseline.Validate(); err != nil {
		return "", fmt.Errorf("eval: baseline digest: %w", err)
	}
	return canonicalDigest(baseline)
}

func (baseline Baseline) Validate() error {
	if !hasText(baseline.ID) {
		return fmt.Errorf("%w: baseline id is required", errInvalidDocument)
	}
	if baseline.RecordedAt.IsZero() {
		return fmt.Errorf("%w: baseline recordedAt is required", errInvalidDocument)
	}
	for _, cell := range baseline.Cells {
		if cell.ScenarioDigest == "" || cell.SubjectDigest == "" || cell.ExecutorDigest == "" {
			return fmt.Errorf("%w: every baseline cell needs its three identity digests", errInvalidDocument)
		}
		if len(cell.AttemptIDs) == 0 {
			return fmt.Errorf("%w: baseline cell %q names no Attempts; a record that cannot be traced back is not evidence",
				errInvalidDocument, cell.ScenarioDigest)
		}
	}
	return nil
}

// BuildBaseline records the current distributions as a baseline.
//
// It is deliberately not reachable from a run path. Regeneration is an
// explicit command whose output is committed and reviewed, because a lane
// that rewrites its own baseline when it drifts has stopped measuring
// anything.
func BuildBaseline(id string, recordedAt time.Time, cells map[CellIdentity][]CellRepetition, policy VariancePolicy) (Baseline, error) {
	baseline := Baseline{
		FormatVersion: FormatVersion,
		Schema:        SchemaBaseline,
		ID:            id,
		RecordedAt:    recordedAt.UTC(),
	}

	// Go map iteration order is randomized, so the identities are sorted
	// before anything is recorded. Without this the same inputs would
	// produce a different document — and a different digest — on every run,
	// which is the most common way a "reproducible" artifact turns out not
	// to be.
	identities := make([]CellIdentity, 0, len(cells))
	for identity := range cells {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].ScenarioDigest != identities[j].ScenarioDigest {
			return identities[i].ScenarioDigest < identities[j].ScenarioDigest
		}
		if identities[i].SubjectDigest != identities[j].SubjectDigest {
			return identities[i].SubjectDigest < identities[j].SubjectDigest
		}
		return identities[i].ExecutorDigest < identities[j].ExecutorDigest
	})

	for _, identity := range identities {
		repetitions := cells[identity]
		distribution, err := ComputeCellDistribution(repetitions, policy)
		if err != nil {
			return Baseline{}, err
		}
		// A baseline is what later runs are compared against, so recording a
		// measurement already known to be unreliable would poison every
		// comparison that follows it.
		if !distribution.MayBeReadAsAResult() {
			return Baseline{}, fmt.Errorf(
				"%w: refusing to record a Cell that cannot be read as a result as a baseline: %s",
				errInvalidDocument, distribution.unreadableReason())
		}

		ordered := append([]CellRepetition(nil), repetitions...)
		sort.SliceStable(ordered, func(i, j int) bool {
			return ordered[i].Attempt.RepetitionIndex < ordered[j].Attempt.RepetitionIndex
		})
		ids := make([]AttemptID, 0, len(ordered))
		for _, repetition := range ordered {
			ids = append(ids, repetition.Attempt.ID)
		}

		baseline.Cells = append(baseline.Cells, BaselineCell{
			ScenarioDigest:    identity.ScenarioDigest,
			SubjectDigest:     identity.SubjectDigest,
			ExecutorDigest:    identity.ExecutorDigest,
			Attempts:          distribution.Attempts,
			EvaluableAttempts: distribution.EvaluableAttempts,
			NumericScores:     append([]float64(nil), distribution.NumericScores...),
			NumericSpread:     distribution.NumericSpread,
			VerdictStability:  distribution.VerdictStability,
			AttemptIDs:        ids,
		})
	}
	return baseline, nil
}

// MatchBaseline compares one current distribution against a baseline.
//
// staleAfter of zero disables the staleness check.
func MatchBaseline(baseline Baseline, identity CellIdentity, current CellDistribution, now time.Time, staleAfter time.Duration) (BaselineComparison, error) {
	if err := baseline.Validate(); err != nil {
		return BaselineComparison{}, err
	}

	var comparison BaselineComparison
	if staleAfter > 0 && now.Sub(baseline.RecordedAt) > staleAfter {
		comparison.Stale = true
	}

	for _, cell := range baseline.Cells {
		if cell.ScenarioDigest != identity.ScenarioDigest ||
			cell.SubjectDigest != identity.SubjectDigest ||
			cell.ExecutorDigest != identity.ExecutorDigest {
			continue
		}
		comparison.Matched = true
		comparison.MedianDelta = medianOf(current.NumericScores) - medianOf(cell.NumericScores)
		comparison.Regressed = comparison.MedianDelta < 0
		return comparison, nil
	}

	comparison.UnmatchedReason = fmt.Sprintf(
		"the baseline holds no record for scenario %q, subject %q, executor %q; the usual cause is that one of them was edited",
		identity.ScenarioDigest, identity.SubjectDigest, identity.ExecutorDigest)
	return comparison, nil
}

// medianOf is the median of values, or zero for an empty set.
func medianOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}
