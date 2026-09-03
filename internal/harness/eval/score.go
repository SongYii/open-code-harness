package eval

import (
	"fmt"
	"math"
)

// ScoreVerdict is a scorer's overall or per-criterion result (design §20).
type ScoreVerdict string

const (
	ScorePass          ScoreVerdict = "pass"
	ScoreFail          ScoreVerdict = "fail"
	ScoreIndeterminate ScoreVerdict = "indeterminate"
)

// CriterionResult is one per-criterion result within a Score (design §20).
type CriterionResult struct {
	ID     string       `json:"id"`
	Status ScoreVerdict `json:"status"`
	Score  *float64     `json:"score,omitempty"`
}

// ScorerUsage is the usage/timing/cost of the scorer itself, when
// applicable (design §20) — distinct from the Subject's own usage, which
// Outcome/EvidenceManifest evidence carries.
type ScorerUsage struct {
	InputTokens    int64 `json:"inputTokens,omitempty"`
	OutputTokens   int64 `json:"outputTokens,omitempty"`
	DurationMillis int64 `json:"durationMillis,omitempty"`
	CostMicrounits int64 `json:"costMicrounits,omitempty"`
}

// Score is one `och.eval.score` document: one immutable scorer result over
// one manifest digest (design §4/§20), appended once per scoring or
// regrade invocation and never replacing an earlier Score.
type Score struct {
	FormatVersion int     `json:"formatVersion"`
	Schema        string  `json:"schema"`
	ID            ScoreID `json:"id"`

	AttemptID      AttemptID `json:"attemptId"`
	ManifestDigest Digest    `json:"manifestDigest"`
	OutcomeDigest  Digest    `json:"outcomeDigest"`

	ScorerID           string   `json:"scorerId"`
	ScorerVersion      string   `json:"scorerVersion"`
	ScorerConfigDigest Digest   `json:"scorerConfigDigest,omitempty"`
	Lane               EvalLane `json:"lane"`

	Verdict      ScoreVerdict      `json:"verdict"`
	NumericScore *float64          `json:"numericScore,omitempty"`
	Criteria     []CriterionResult `json:"criteria,omitempty"`

	EvidenceReferences    []string `json:"evidenceReferences,omitempty"`
	MissingEvidence       []string `json:"missingEvidence,omitempty"`
	ContradictoryEvidence []string `json:"contradictoryEvidence,omitempty"`

	Rationale string `json:"rationale,omitempty"`

	ScorerUsage *ScorerUsage `json:"scorerUsage,omitempty"`
}

// DecodeScore strictly decodes and validates one `och.eval.score` document
// (design §6).
func DecodeScore(data []byte) (Score, error) {
	var score Score
	if err := decodeStrict(data, &score); err != nil {
		return Score{}, fmt.Errorf("eval: score: %w", err)
	}
	if score.Schema != SchemaScore {
		return Score{}, fmt.Errorf("eval: score: %w: %q", errUnsupportedSchema, score.Schema)
	}
	if score.FormatVersion != FormatVersion {
		return Score{}, fmt.Errorf("eval: score: %w: %d", errUnsupportedFormatVersion, score.FormatVersion)
	}
	if err := score.Validate(); err != nil {
		return Score{}, err
	}
	return score, nil
}

// Validate checks every field this document requires.
func (score Score) Validate() error {
	if _, err := ParseScoreID(string(score.ID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if _, err := ParseAttemptID(string(score.AttemptID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !digestStringPattern.MatchString(string(score.ManifestDigest)) {
		return fmt.Errorf("%w: manifestDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if !digestStringPattern.MatchString(string(score.OutcomeDigest)) {
		return fmt.Errorf("%w: outcomeDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if !hasText(score.ScorerID) {
		return fmt.Errorf("%w: scorerId is required", errInvalidDocument)
	}
	if !hasText(score.ScorerVersion) {
		return fmt.Errorf("%w: scorerVersion is required", errInvalidDocument)
	}
	if score.ScorerConfigDigest != "" && !digestStringPattern.MatchString(string(score.ScorerConfigDigest)) {
		return fmt.Errorf("%w: scorerConfigDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	switch score.Lane {
	case LaneFixture, LaneLive:
	default:
		return fmt.Errorf("%w: lane must be %q or %q", errInvalidDocument, LaneFixture, LaneLive)
	}
	switch score.Verdict {
	case ScorePass, ScoreFail, ScoreIndeterminate:
	default:
		return fmt.Errorf("%w: unknown verdict %q", errInvalidDocument, score.Verdict)
	}
	if err := validateOptionalFiniteScore(score.NumericScore); err != nil {
		return err
	}
	for index, criterion := range score.Criteria {
		if err := criterion.validate(index); err != nil {
			return err
		}
	}
	if err := requireNonEmptyEntries("evidenceReferences", score.EvidenceReferences); err != nil {
		return err
	}
	if err := requireNonEmptyEntries("missingEvidence", score.MissingEvidence); err != nil {
		return err
	}
	if err := requireNonEmptyEntries("contradictoryEvidence", score.ContradictoryEvidence); err != nil {
		return err
	}
	return nil
}

func (criterion CriterionResult) validate(index int) error {
	if !hasText(criterion.ID) {
		return fmt.Errorf("%w: criteria %d: id is required", errInvalidDocument, index)
	}
	switch criterion.Status {
	case ScorePass, ScoreFail, ScoreIndeterminate:
	default:
		return fmt.Errorf("%w: criteria %d: unknown status %q", errInvalidDocument, index, criterion.Status)
	}
	if err := validateOptionalFiniteScore(criterion.Score); err != nil {
		return fmt.Errorf("criteria %d: %w", index, err)
	}
	return nil
}

func validateOptionalFiniteScore(value *float64) error {
	if value == nil {
		return nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) {
		return fmt.Errorf("%w: score must be a finite number", errInvalidDocument)
	}
	if *value < 0 || *value > 1 {
		return fmt.Errorf("%w: score must be between 0 and 1", errInvalidDocument)
	}
	return nil
}
