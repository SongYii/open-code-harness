package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// reportVariancePolicy names the policy a report applied, so a reader can
// reproduce the trustworthiness judgement offline from the artifacts.
type reportVariancePolicy struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	Calibration string `json:"calibration"`
}

// reportVarianceCell is one Cell's published distribution.
//
// It carries no derived verdict, matching the type it is built from: a
// consumer wanting one answer must choose its own rule.
type reportVarianceCell struct {
	ScenarioDigest string `json:"scenarioDigest"`
	SubjectDigest  string `json:"subjectDigest"`
	ExecutorDigest string `json:"executorDigest"`

	// Attempts is the raw denominator, EvaluableAttempts the filtered one.
	// Both are published; neither statistic is computed twice.
	Attempts          int `json:"attempts"`
	EvaluableAttempts int `json:"evaluableAttempts"`

	Verdicts      map[string]int `json:"verdicts"`
	NumericScores []float64      `json:"numericScores,omitempty"`

	NumericSpread    float64 `json:"numericSpread"`
	VerdictStability float64 `json:"verdictStability"`

	// The two reliability answers stay separate here for the same reason
	// they are separate on the type: a consumer branches on a boolean and
	// does not read the reason beside it, so one merged flag would report a
	// Cell measured once out of five as fine.
	EvaluableEnough          bool   `json:"evaluableEnough"`
	NotEvaluableEnoughReason string `json:"notEvaluableEnoughReason,omitempty"`
	ExceedsDeclaredLimits    bool   `json:"exceedsDeclaredLimits"`
	ExceededLimitsReason     string `json:"exceededLimitsReason,omitempty"`

	// Reliability is absent below two evaluable repetitions, where these
	// readings would describe a measurement that did not happen.
	Reliability *reportCellReliability `json:"reliability,omitempty"`

	// Uncalibrated repeats, per Cell, that the limits this judgement used
	// were never measured. The accepted ordering shipped the mechanism before
	// any calibrated number existed, and the hazard of that ordering is a
	// provisional number read as a final one — so the disclosure travels with
	// every number rather than sitting once at the top of a document a reader
	// may scroll past.
	Uncalibrated bool `json:"uncalibrated,omitempty"`
}

// reportCellReliability is design §3.4: arithmetic on counts the block
// already carries, needing no calibrated threshold at all. That is why these
// are the readings a reader can trust today, while exceedsDeclaredLimits
// compares against limits nobody has measured.
//
// Naming, deliberately: AtLeastOnePassed is at_least(1) over this Cell's
// repetitions. It is **not** pass@k. Chen et al.'s pass@k is an unbiased
// dataset-level estimator over n samples of which c passed, and borrowing its
// name for a per-Cell count would make the first public comparison dishonest.
// AllPassed is the reading this project actually cares about — the number
// that falls as repetitions grow, which is what asking "can this be trusted
// unattended" means — and is tau-bench's pass^k rather than a leaderboard's
// pass@k.
type reportCellReliability struct {
	// EvaluablePasses is c; the Cell's EvaluableAttempts is n. The raw
	// counts are published rather than a rate, so a reader computes whichever
	// rate they mean instead of inheriting one.
	EvaluablePasses int `json:"evaluablePasses"`

	AtLeastOnePassed bool `json:"atLeastOnePassed"`
	AllPassed        bool `json:"allPassed"`
}

// varianceInputs are the flags that turn the variance block on.
type varianceInputs struct {
	policyPath string
	scorerID   string
}

// loadVariancePolicy reads and validates the policy, or reports that none was
// requested.
//
// An unreadable or invalid policy is an error rather than a silent skip. A
// caller who asked for a variance signal and received a report without one
// would reasonably read the absence as "no variance problems".
func loadVariancePolicy(inputs varianceInputs, stderr io.Writer) (eval.VariancePolicy, eval.Digest, bool, error) {
	if inputs.policyPath == "" {
		return eval.VariancePolicy{}, "", false, nil
	}
	data, err := os.ReadFile(inputs.policyPath)
	if err != nil {
		return eval.VariancePolicy{}, "", false, fmt.Errorf("variance policy: %w", err)
	}
	policy, err := eval.DecodeVariancePolicy(data)
	if err != nil {
		return eval.VariancePolicy{}, "", false, fmt.Errorf("variance policy: %w", err)
	}
	digest, err := eval.VariancePolicyDigest(policy)
	if err != nil {
		return eval.VariancePolicy{}, "", false, fmt.Errorf("variance policy: %w", err)
	}
	if policy.Calibration == eval.CalibrationUncalibrated {
		fmt.Fprintf(stderr,
			"och-eval: variance policy %q is uncalibrated; its limits were never measured against a real run\n",
			policy.ID)
	}
	return policy, digest, true, nil
}

// buildVarianceBlock computes each Cell's distribution from the pairs the
// report already gathered.
//
// Cells are emitted in a sorted order because Go randomizes map iteration and
// a report whose contents shuffle between runs cannot be diffed — the same
// reason the baseline document sorts before recording.
func buildVarianceBlock(pairs []eval.AttemptScore, scorerID string, policy eval.VariancePolicy) ([]reportVarianceCell, error) {
	cells, err := eval.GroupByCellForScorer(pairs, scorerID)
	if err != nil {
		return nil, err
	}

	identities := make([]eval.CellIdentity, 0, len(cells))
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

	block := make([]reportVarianceCell, 0, len(identities))
	for _, identity := range identities {
		distribution, err := eval.ComputeCellDistribution(cells[identity], policy)
		if err != nil {
			return nil, err
		}
		verdicts := make(map[string]int, len(distribution.Verdicts))
		for verdict, count := range distribution.Verdicts {
			verdicts[string(verdict)] = count
		}
		block = append(block, reportVarianceCell{
			ScenarioDigest:           string(identity.ScenarioDigest),
			SubjectDigest:            string(identity.SubjectDigest),
			ExecutorDigest:           string(identity.ExecutorDigest),
			Attempts:                 distribution.Attempts,
			EvaluableAttempts:        distribution.EvaluableAttempts,
			Verdicts:                 verdicts,
			NumericScores:            distribution.NumericScores,
			NumericSpread:            distribution.NumericSpread,
			VerdictStability:         distribution.VerdictStability,
			EvaluableEnough:          distribution.EvaluableEnough,
			NotEvaluableEnoughReason: distribution.NotEvaluableEnoughReason,
			ExceedsDeclaredLimits:    distribution.ExceedsDeclaredLimits,
			ExceededLimitsReason:     distribution.ExceededLimitsReason,
			Reliability:              reliabilityOf(distribution),
			Uncalibrated:             policy.Calibration == eval.CalibrationUncalibrated,
		})
	}
	return block, nil
}

// reliabilityOf derives §3.4's readings, or nothing when there were fewer
// than two evaluable repetitions.
//
// The floor is not decoration. With one evaluable repetition "all passed"
// and "at least one passed" are the same statement, and publishing them
// would dress a single sample up as agreement — the exact hazard this whole
// mechanism exists to prevent, and the reason inspect_ai returning stderr = 0
// from one sample is called out in the design as the thing not to copy.
func reliabilityOf(distribution eval.CellDistribution) *reportCellReliability {
	if distribution.EvaluableAttempts < 2 {
		return nil
	}
	passes := distribution.Verdicts[eval.ScorePass]
	return &reportCellReliability{
		EvaluablePasses:  passes,
		AtLeastOnePassed: passes > 0,
		AllPassed:        passes == distribution.EvaluableAttempts,
	}
}
