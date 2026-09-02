package eval

import "fmt"

// Attempt is the frozen `och.eval.attempt` document: one execution of one
// Cell (Scenario × Subject × Executor) and repetition index (design §4).
// Retry always creates another Attempt; this document is written once,
// atomically, before the Subject starts (design §12) and is never mutated
// afterward.
type Attempt struct {
	FormatVersion int       `json:"formatVersion"`
	Schema        string    `json:"schema"`
	ID            AttemptID `json:"id"`

	EvalSetID EvalSetID `json:"evalSetId"`

	ScenarioID     ScenarioID `json:"scenarioId"`
	ScenarioDigest Digest     `json:"scenarioDigest"`
	SubjectID      SubjectID  `json:"subjectId"`
	SubjectDigest  Digest     `json:"subjectDigest"`
	ExecutorID     ExecutorID `json:"executorId"`
	ExecutorDigest Digest     `json:"executorDigest"`

	// RepetitionIndex is this Attempt's position within its Cell's
	// repeated executions (design §4/§9), zero-based.
	RepetitionIndex int `json:"repetitionIndex"`
}

// DecodeAttempt strictly decodes and validates one `och.eval.attempt`
// document (design §6).
func DecodeAttempt(data []byte) (Attempt, error) {
	var attempt Attempt
	if err := decodeStrict(data, &attempt); err != nil {
		return Attempt{}, fmt.Errorf("eval: attempt: %w", err)
	}
	if attempt.Schema != SchemaAttempt {
		return Attempt{}, fmt.Errorf("eval: attempt: %w: %q", errUnsupportedSchema, attempt.Schema)
	}
	if attempt.FormatVersion != FormatVersion {
		return Attempt{}, fmt.Errorf("eval: attempt: %w: %d", errUnsupportedFormatVersion, attempt.FormatVersion)
	}
	if err := attempt.Validate(); err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

// Validate checks every field this document requires. It does not check
// that the referenced Scenario/Subject/Executor digests actually match a
// published document — that cross-check belongs to whatever constructs an
// Attempt (matrix expansion, not yet implemented), which has those
// documents in hand.
func (attempt Attempt) Validate() error {
	if _, err := ParseAttemptID(string(attempt.ID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if _, err := ParseEvalSetID(string(attempt.EvalSetID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if _, err := ParseScenarioID(string(attempt.ScenarioID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !digestStringPattern.MatchString(string(attempt.ScenarioDigest)) {
		return fmt.Errorf("%w: scenarioDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if _, err := ParseSubjectID(string(attempt.SubjectID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !digestStringPattern.MatchString(string(attempt.SubjectDigest)) {
		return fmt.Errorf("%w: subjectDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if _, err := ParseExecutorID(string(attempt.ExecutorID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !digestStringPattern.MatchString(string(attempt.ExecutorDigest)) {
		return fmt.Errorf("%w: executorDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if attempt.RepetitionIndex < 0 {
		return fmt.Errorf("%w: repetitionIndex must not be negative", errInvalidDocument)
	}
	return nil
}
