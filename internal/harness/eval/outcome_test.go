package eval

import (
	"reflect"
	"testing"
	"time"
)

func validOutcome(t *testing.T, attemptID AttemptID) Outcome {
	t.Helper()
	started := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	return Outcome{
		FormatVersion: FormatVersion,
		Schema:        SchemaOutcome,
		AttemptID:     attemptID,
		Status:        OutcomeCompleted,
		Code:          "ok",
		Message:       "attempt completed",
		StartedAt:     started,
		EndedAt:       started.Add(90 * time.Second),
		TerminalSession: &TerminalSessionFacts{
			SessionID: "session-1",
			TurnCount: 3,
			Open:      false,
			Running:   false,
		},
		CollectionStatus: CollectionComplete,
		Recovered:        false,
	}
}

func TestDecodeOutcomeRoundTrip(t *testing.T) {
	want := validOutcome(t, mustAttemptID(t))
	got, err := DecodeOutcome(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeOutcome: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("DecodeOutcome round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestOutcomeDurationIsDerived(t *testing.T) {
	outcome := validOutcome(t, mustAttemptID(t))
	want := 90 * time.Second
	if got := outcome.Duration(); got != want {
		t.Fatalf("Duration() = %v, want %v", got, want)
	}
}

func TestOutcomeValidateRejectsUnknownStatus(t *testing.T) {
	outcome := validOutcome(t, mustAttemptID(t))
	outcome.Status = "unknown"
	if err := outcome.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown status")
	}
}

func TestOutcomeValidateRejectsEndBeforeStart(t *testing.T) {
	outcome := validOutcome(t, mustAttemptID(t))
	outcome.EndedAt = outcome.StartedAt.Add(-time.Second)
	if err := outcome.Validate(); err == nil {
		t.Fatal("Validate() accepted endedAt before startedAt")
	}
}

func TestOutcomeValidateAllowsMissingTerminalSession(t *testing.T) {
	outcome := validOutcome(t, mustAttemptID(t))
	outcome.TerminalSession = nil
	if err := outcome.Validate(); err != nil {
		t.Fatalf("Validate() rejected a nil terminalSession: %v", err)
	}
}

func TestOutcomeValidateRejectsUnknownCollectionStatus(t *testing.T) {
	outcome := validOutcome(t, mustAttemptID(t))
	outcome.CollectionStatus = "unknown"
	if err := outcome.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown collectionStatus")
	}
}

func TestSubjectFailedOutcomeCanRepresentAnExpectedNegativeScenario(t *testing.T) {
	// Design §13: "An expected terminal OCH failure may have subject_failed
	// Outcome and still receive a passing deterministic Score for a
	// negative Scenario." This pins that subject_failed is not itself a
	// document-validity error.
	outcome := validOutcome(t, mustAttemptID(t))
	outcome.Status = OutcomeSubjectFailed
	outcome.Code = "provider_refused_request"
	if err := outcome.Validate(); err != nil {
		t.Fatalf("Validate() rejected a valid subject_failed Outcome: %v", err)
	}
}
