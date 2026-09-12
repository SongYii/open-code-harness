package eval

import "fmt"

const SchemaJudgeMetaPolicy = "och.eval.judge-meta-policy"
const SchemaJudgeMetaPolicyResult = "och.eval.judge-meta-policy-result"

// JudgeMetaPolicy is an empirical, provider-specific envelope learned from
// one complete calibration report. It is intentionally applied only to a
// different set digest so calibration data cannot validate itself.
type JudgeMetaPolicy struct {
	FormatVersion int         `json:"formatVersion"`
	Schema        string      `json:"schema"`
	ID            string      `json:"id"`
	Version       string      `json:"version"`
	Calibration   Calibration `json:"calibration"`

	CalibratedFrom             Digest `json:"calibratedFrom"`
	CalibrationSetDigest       Digest `json:"calibrationSetDigest"`
	ValidationSetDigest        Digest `json:"validationSetDigest"`
	JudgeConfigDigest          Digest `json:"judgeConfigDigest"`
	RequiredObservations       int    `json:"requiredObservations"`
	MinExactMatches            int    `json:"minExactMatches"`
	MaxUnsafePasses            int    `json:"maxUnsafePasses"`
	MaxFalseFails              int    `json:"maxFalseFails"`
	MaxUnexpectedIndeterminate int    `json:"maxUnexpectedIndeterminates"`
	MaxOverclaims              int    `json:"maxOverclaims"`
}

type JudgeMetaPolicyResult struct {
	FormatVersion int      `json:"formatVersion"`
	Schema        string   `json:"schema"`
	PolicyDigest  Digest   `json:"policyDigest"`
	ReportDigest  Digest   `json:"reportDigest"`
	Passed        bool     `json:"passed"`
	Breaches      []string `json:"breaches"`
}

func JudgeMetaReportDigest(report JudgeMetaReport) (Digest, error) {
	if err := report.Validate(); err != nil {
		return "", fmt.Errorf("eval: judge meta report digest: %w", err)
	}
	return canonicalDigest(report)
}

func CalibrateJudgeMetaPolicy(report JudgeMetaReport, validationSet JudgeMetaSet, id, version string) (JudgeMetaPolicy, error) {
	if err := report.Validate(); err != nil {
		return JudgeMetaPolicy{}, err
	}
	if !report.Complete {
		return JudgeMetaPolicy{}, fmt.Errorf("%w: judge meta calibration report must be complete", errInvalidDocument)
	}
	if err := validationSet.Validate(); err != nil {
		return JudgeMetaPolicy{}, err
	}
	if validationSet.JudgeConfigDigest != report.JudgeConfigDigest || validationSet.CallCount() != report.CompletedCalls {
		return JudgeMetaPolicy{}, fmt.Errorf("%w: validation set must use the calibration config and call count", errInvalidDocument)
	}
	validationSetDigest, err := JudgeMetaSetDigest(validationSet)
	if err != nil {
		return JudgeMetaPolicy{}, err
	}
	digest, err := JudgeMetaReportDigest(report)
	if err != nil {
		return JudgeMetaPolicy{}, err
	}
	policy := JudgeMetaPolicy{
		FormatVersion: FormatVersion, Schema: SchemaJudgeMetaPolicy, ID: id, Version: version,
		Calibration: CalibrationCalibrated, CalibratedFrom: digest,
		CalibrationSetDigest: report.SetDigest, ValidationSetDigest: validationSetDigest,
		JudgeConfigDigest:    report.JudgeConfigDigest,
		RequiredObservations: report.Summary.Observations, MinExactMatches: report.Summary.ExactMatches,
		MaxUnsafePasses: report.Summary.UnsafePasses, MaxFalseFails: report.Summary.FalseFails,
		MaxUnexpectedIndeterminate: report.Summary.UnexpectedIndeterminates, MaxOverclaims: report.Summary.Overclaims,
	}
	if err := policy.Validate(); err != nil {
		return JudgeMetaPolicy{}, err
	}
	return policy, nil
}

func DecodeJudgeMetaPolicy(data []byte) (JudgeMetaPolicy, error) {
	var policy JudgeMetaPolicy
	if err := decodeStrict(data, &policy); err != nil {
		return JudgeMetaPolicy{}, fmt.Errorf("eval: judge meta policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return JudgeMetaPolicy{}, err
	}
	return policy, nil
}

func JudgeMetaPolicyDigest(policy JudgeMetaPolicy) (Digest, error) {
	if err := policy.Validate(); err != nil {
		return "", err
	}
	return canonicalDigest(policy)
}

func (policy JudgeMetaPolicy) Validate() error {
	if policy.FormatVersion != FormatVersion || policy.Schema != SchemaJudgeMetaPolicy {
		return fmt.Errorf("%w: invalid judge meta policy format/schema", errInvalidDocument)
	}
	if !hasText(policy.ID) || !hasText(policy.Version) || policy.Calibration != CalibrationCalibrated {
		return fmt.Errorf("%w: judge meta policy requires identity and calibrated state", errInvalidDocument)
	}
	for _, field := range []struct {
		name   string
		digest Digest
	}{
		{"calibratedFrom", policy.CalibratedFrom}, {"calibrationSetDigest", policy.CalibrationSetDigest},
		{"validationSetDigest", policy.ValidationSetDigest}, {"judgeConfigDigest", policy.JudgeConfigDigest},
	} {
		if !digestStringPattern.MatchString(string(field.digest)) {
			return fmt.Errorf("%w: judge meta policy %s is invalid", errInvalidDocument, field.name)
		}
	}
	if policy.CalibrationSetDigest == policy.ValidationSetDigest {
		return fmt.Errorf("%w: judge meta policy calibration and validation sets must differ", errInvalidDocument)
	}
	if policy.RequiredObservations < 1 || policy.MinExactMatches < 0 || policy.MinExactMatches > policy.RequiredObservations {
		return fmt.Errorf("%w: judge meta policy observation limits are invalid", errInvalidDocument)
	}
	for _, field := range []struct {
		name  string
		value int
	}{
		{"maxUnsafePasses", policy.MaxUnsafePasses}, {"maxFalseFails", policy.MaxFalseFails},
		{"maxUnexpectedIndeterminates", policy.MaxUnexpectedIndeterminate}, {"maxOverclaims", policy.MaxOverclaims},
	} {
		if field.value < 0 || field.value > policy.RequiredObservations {
			return fmt.Errorf("%w: judge meta policy %s is invalid", errInvalidDocument, field.name)
		}
	}
	return nil
}

func EvaluateJudgeMetaPolicy(policy JudgeMetaPolicy, report JudgeMetaReport) (JudgeMetaPolicyResult, error) {
	if err := policy.Validate(); err != nil {
		return JudgeMetaPolicyResult{}, err
	}
	if err := report.Validate(); err != nil {
		return JudgeMetaPolicyResult{}, err
	}
	if !report.Complete {
		return JudgeMetaPolicyResult{}, fmt.Errorf("%w: judge meta validation report must be complete", errInvalidDocument)
	}
	if report.SetDigest != policy.ValidationSetDigest {
		return JudgeMetaPolicyResult{}, fmt.Errorf("%w: report is not the policy's predeclared validation set", errInvalidDocument)
	}
	if report.JudgeConfigDigest != policy.JudgeConfigDigest || report.Summary.Observations != policy.RequiredObservations {
		return JudgeMetaPolicyResult{}, fmt.Errorf("%w: judge meta validation report is outside the calibrated identity/sample boundary", errInvalidDocument)
	}
	policyDigest, err := JudgeMetaPolicyDigest(policy)
	if err != nil {
		return JudgeMetaPolicyResult{}, err
	}
	reportDigest, err := JudgeMetaReportDigest(report)
	if err != nil {
		return JudgeMetaPolicyResult{}, err
	}
	result := JudgeMetaPolicyResult{FormatVersion: FormatVersion, Schema: SchemaJudgeMetaPolicyResult, PolicyDigest: policyDigest, ReportDigest: reportDigest}
	checks := []struct {
		breach bool
		text   string
	}{
		{report.Summary.ExactMatches < policy.MinExactMatches, "exact matches fell below calibration"},
		{report.Summary.UnsafePasses > policy.MaxUnsafePasses, "unsafe passes exceeded calibration"},
		{report.Summary.FalseFails > policy.MaxFalseFails, "false fails exceeded calibration"},
		{report.Summary.UnexpectedIndeterminates > policy.MaxUnexpectedIndeterminate, "unexpected indeterminates exceeded calibration"},
		{report.Summary.Overclaims > policy.MaxOverclaims, "overclaims exceeded calibration"},
	}
	for _, check := range checks {
		if check.breach {
			result.Breaches = append(result.Breaches, check.text)
		}
	}
	result.Passed = len(result.Breaches) == 0
	return result, nil
}
