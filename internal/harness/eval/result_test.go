package eval

import (
	"reflect"
	"testing"
)

func TestNewEvaluationResultAssemblesOutcomeAndScores(t *testing.T) {
	attemptID := mustAttemptID(t)
	outcome := validOutcome(t, attemptID)
	first := validScore(t, attemptID)
	second := validScore(t, attemptID)

	result, err := NewEvaluationResult(outcome, []Score{first, second})
	if err != nil {
		t.Fatalf("NewEvaluationResult: %v", err)
	}
	if result.AttemptID != attemptID {
		t.Fatalf("AttemptID = %q, want %q", result.AttemptID, attemptID)
	}
	if result.Outcome.Status != outcome.Status {
		t.Fatalf("Outcome = %+v, want %+v", result.Outcome, outcome)
	}
	if !reflect.DeepEqual(result.Scores, []Score{first, second}) {
		t.Fatalf("Scores = %+v, want %+v", result.Scores, []Score{first, second})
	}
}

func TestNewEvaluationResultAllowsNoScores(t *testing.T) {
	attemptID := mustAttemptID(t)
	outcome := validOutcome(t, attemptID)
	result, err := NewEvaluationResult(outcome, nil)
	if err != nil {
		t.Fatalf("NewEvaluationResult: %v", err)
	}
	if len(result.Scores) != 0 {
		t.Fatalf("Scores = %+v, want none", result.Scores)
	}
}

func TestNewEvaluationResultRejectsScoreForADifferentAttempt(t *testing.T) {
	outcome := validOutcome(t, mustAttemptID(t))
	mismatched := validScore(t, mustAttemptID(t))
	if _, err := NewEvaluationResult(outcome, []Score{mismatched}); err == nil {
		t.Fatal("NewEvaluationResult accepted a Score referencing a different Attempt")
	}
}

func TestNewEvaluationResultRejectsInvalidOutcome(t *testing.T) {
	invalid := validOutcome(t, mustAttemptID(t))
	invalid.Status = "unknown"
	if _, err := NewEvaluationResult(invalid, nil); err == nil {
		t.Fatal("NewEvaluationResult accepted an invalid Outcome")
	}
}

func TestNewEvaluationResultRejectsInvalidScore(t *testing.T) {
	attemptID := mustAttemptID(t)
	outcome := validOutcome(t, attemptID)
	invalid := validScore(t, attemptID)
	invalid.Verdict = "unknown"
	if _, err := NewEvaluationResult(outcome, []Score{invalid}); err == nil {
		t.Fatal("NewEvaluationResult accepted an invalid Score")
	}
}
