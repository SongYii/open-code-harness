package eval

import (
	"fmt"
	"math"
)

// SchemaVariancePolicy names the frozen document that carries the limits a
// variance signal is judged against.
//
// The limits live in a document rather than in code so a Score's own
// trustworthiness rule is provable offline from the artifacts alone, rather
// than from whatever the report generator happened to be compiled with —
// the same reason och.eval.judge-config exists.
const SchemaVariancePolicy = "och.eval.variance-policy"

// Calibration states whether a policy's limits came from real measurement.
//
// It is a field rather than a comment for one reason: this project accepted
// the variance mechanism before any live run existed to calibrate it, so the
// first policies carry invented numbers. A provisional number that looks
// identical to a calibrated one is the whole hazard of that ordering, and an
// unstated calibration would leave the disclosure to whoever remembered.
type Calibration string

const (
	// CalibrationUncalibrated marks limits nobody has measured. A Score
	// derived under such a policy is still a real Score; what it is not is
	// evidence that the limits are the right ones.
	CalibrationUncalibrated Calibration = "uncalibrated"

	// CalibrationCalibrated marks limits derived from a real run, which the
	// document must cite.
	CalibrationCalibrated Calibration = "calibrated"
)

// VariancePolicy is one `och.eval.variance-policy` document.
//
// It supplies no defaults. Every limit is mandatory, because this repository
// has never run a judge against a live model and a shipped default would be
// a guess wearing the authority of a specification.
type VariancePolicy struct {
	FormatVersion int    `json:"formatVersion"`
	Schema        string `json:"schema"`
	ID            string `json:"id"`
	Version       string `json:"version"`

	// Calibration is mandatory and never inferred from absence.
	Calibration Calibration `json:"calibration"`

	// CalibratedFrom cites the run that produced these limits. Required when
	// Calibration is calibrated, and forbidden otherwise: an uncalibrated
	// policy has nothing to cite, and letting it name something anyway would
	// make the two states indistinguishable in exactly the way this field
	// exists to prevent.
	CalibratedFrom string `json:"calibratedFrom,omitempty"`

	// MaxNumericSpread bounds max(NumericScore) - min(NumericScore) across a
	// Cell's evaluable repetitions. Range, not standard deviation: at the
	// sample sizes a nightly lane can afford, a standard deviation has no
	// useful sampling behavior, while a range is what a reviewer can check by
	// eye against the published sequence.
	MaxNumericSpread float64 `json:"maxNumericSpread"`

	// MinVerdictStability bounds modalVerdictCount / evaluableCount. 1.0
	// means unanimous.
	MinVerdictStability float64 `json:"minVerdictStability"`

	// MinEvaluableRepetitions is how many repetitions must be judgeable for
	// the Cell's signal to be read at all. Below it, the Cell is
	// untrustworthy for that reason, reported distinctly from a spread
	// breach — "mostly unjudgeable" and "judged inconsistently" are different
	// problems.
	MinEvaluableRepetitions int `json:"minEvaluableRepetitions"`
}

// DecodeVariancePolicy strictly decodes and validates one document. The
// decode rejects unknown fields, so a value this type does not model cannot
// ride along inside an unmodeled key and reach an Attempt's evidence.
func DecodeVariancePolicy(data []byte) (VariancePolicy, error) {
	var policy VariancePolicy
	if err := decodeStrict(data, &policy); err != nil {
		return VariancePolicy{}, fmt.Errorf("eval: variance policy: %w", err)
	}
	if policy.Schema != SchemaVariancePolicy {
		return VariancePolicy{}, fmt.Errorf("eval: variance policy: %w: %q", errUnsupportedSchema, policy.Schema)
	}
	if policy.FormatVersion != FormatVersion {
		return VariancePolicy{}, fmt.Errorf("eval: variance policy: %w: %d", errUnsupportedFormatVersion, policy.FormatVersion)
	}
	if err := policy.Validate(); err != nil {
		return VariancePolicy{}, fmt.Errorf("eval: variance policy: %w", err)
	}
	return policy, nil
}

// VariancePolicyDigest is the canonical digest over a validated document, so
// a Score can name the exact policy it was judged under.
func VariancePolicyDigest(policy VariancePolicy) (Digest, error) {
	if err := policy.Validate(); err != nil {
		return "", fmt.Errorf("eval: variance policy digest: %w", err)
	}
	return canonicalDigest(policy)
}

// Validate is total and fail-closed: every limit is required and bounded, and
// the calibration state must be stated.
func (policy VariancePolicy) Validate() error {
	if !hasText(policy.ID) {
		return fmt.Errorf("%w: variance policy id is required", errInvalidDocument)
	}
	if !hasText(policy.Version) {
		return fmt.Errorf("%w: variance policy version is required", errInvalidDocument)
	}

	switch policy.Calibration {
	case CalibrationUncalibrated:
		if hasText(policy.CalibratedFrom) {
			return fmt.Errorf("%w: an uncalibrated policy must not cite a calibrating run", errInvalidDocument)
		}
	case CalibrationCalibrated:
		if !hasText(policy.CalibratedFrom) {
			return fmt.Errorf("%w: a calibrated policy must cite the run that produced its limits", errInvalidDocument)
		}
	default:
		return fmt.Errorf("%w: calibration must be %q or %q, not %q",
			errInvalidDocument, CalibrationUncalibrated, CalibrationCalibrated, policy.Calibration)
	}

	if err := requireBoundedFraction("maxNumericSpread", policy.MaxNumericSpread); err != nil {
		return err
	}
	if err := requireBoundedFraction("minVerdictStability", policy.MinVerdictStability); err != nil {
		return err
	}

	// Two is the smallest number of samples from which a spread can be
	// measured at all. One sample reporting spread=0 would be the most
	// dangerous output this mechanism could produce: perfect stability,
	// measured never.
	if policy.MinEvaluableRepetitions < 2 {
		return fmt.Errorf("%w: minEvaluableRepetitions must be at least 2; a spread cannot be measured from one sample",
			errInvalidDocument)
	}
	return nil
}

// requireBoundedFraction rejects a missing, non-finite, or out-of-range
// limit. Zero counts as missing rather than as a deliberate bound: a policy
// stating "no spread whatsoever is tolerated" is not something to infer from
// an unset field, and every limit here is mandatory.
func requireBoundedFraction(field string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%w: %s must be a finite number", errInvalidDocument, field)
	}
	if value <= 0 {
		return fmt.Errorf("%w: %s is required and must be greater than zero; this policy supplies no defaults",
			errInvalidDocument, field)
	}
	if value > 1 {
		return fmt.Errorf("%w: %s must not exceed 1", errInvalidDocument, field)
	}
	return nil
}
