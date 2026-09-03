package eval

import "fmt"

// EvaluationResult is the architecture charter's `EvaluationResult`,
// resolved here as an eval-owned read DTO (implementation plan Task 1):
// one immutable Outcome plus zero or more committed Score references,
// assembled from those authoritative documents on read. It has no
// independent wire schema, no `formatVersion`/`schema` pair, no file, no
// identity, and no publication path of its own -- constructing one is
// always a pure, local operation over already-published documents, never a
// Domain event, and it is never written to a Session event stream.
type EvaluationResult struct {
	AttemptID AttemptID
	Outcome   Outcome
	Scores    []Score
}

// NewEvaluationResult assembles an EvaluationResult from one Outcome and
// its Scores. Every Score must reference outcome's own Attempt; a Score for
// a different Attempt is a caller error, not a shape this type can silently
// represent.
func NewEvaluationResult(outcome Outcome, scores []Score) (EvaluationResult, error) {
	if err := outcome.Validate(); err != nil {
		return EvaluationResult{}, fmt.Errorf("eval: evaluation result: %w", err)
	}
	for index, score := range scores {
		if err := score.Validate(); err != nil {
			return EvaluationResult{}, fmt.Errorf("eval: evaluation result: score %d: %w", index, err)
		}
		if score.AttemptID != outcome.AttemptID {
			return EvaluationResult{}, fmt.Errorf(
				"eval: evaluation result: score %q references attempt %q, want %q",
				score.ID, score.AttemptID, outcome.AttemptID)
		}
	}
	return EvaluationResult{AttemptID: outcome.AttemptID, Outcome: outcome, Scores: scores}, nil
}
