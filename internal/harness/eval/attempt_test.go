package eval

import (
	"errors"
	"reflect"
	"testing"
)

func mustAttemptID(t *testing.T) AttemptID {
	t.Helper()
	id, err := NewAttemptID()
	if err != nil {
		t.Fatalf("NewAttemptID: %v", err)
	}
	return id
}

func mustDigest(t *testing.T, filler byte) Digest {
	t.Helper()
	return Digest("sha256:" + repeatHex(filler))
}

// repeatHex is a tiny local helper so digest fixtures below stay readable;
// it does not need to be a real digest of anything, only shaped like one.
func repeatHex(filler byte) string {
	digits := "0123456789abcdef"
	char := digits[filler%16]
	out := make([]byte, 64)
	for i := range out {
		out[i] = char
	}
	return string(out)
}

func validAttempt(t *testing.T) Attempt {
	t.Helper()
	return Attempt{
		FormatVersion:   FormatVersion,
		Schema:          SchemaAttempt,
		ID:              mustAttemptID(t),
		EvalSetID:       "set-1",
		ScenarioID:      "scenario-1",
		ScenarioDigest:  mustDigest(t, 1),
		SubjectID:       "subject-1",
		SubjectDigest:   mustDigest(t, 2),
		ExecutorID:      "in-process",
		ExecutorDigest:  mustDigest(t, 3),
		RepetitionIndex: 0,
	}
}

func TestDecodeAttemptRoundTrip(t *testing.T) {
	want := validAttempt(t)
	got, err := DecodeAttempt(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeAttempt: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("DecodeAttempt round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestDecodeAttemptRejectsWrongSchema(t *testing.T) {
	attempt := validAttempt(t)
	attempt.Schema = SchemaOutcome
	if _, err := DecodeAttempt(marshal(t, attempt)); !errors.Is(err, errUnsupportedSchema) {
		t.Fatalf("DecodeAttempt error = %v, want wrapping errUnsupportedSchema", err)
	}
}

func TestAttemptValidateRejectsBadDigest(t *testing.T) {
	attempt := validAttempt(t)
	attempt.ScenarioDigest = "not-a-digest"
	if err := attempt.Validate(); err == nil {
		t.Fatal("Validate() accepted a malformed scenarioDigest")
	}
}

func TestAttemptValidateRejectsNegativeRepetitionIndex(t *testing.T) {
	attempt := validAttempt(t)
	attempt.RepetitionIndex = -1
	if err := attempt.Validate(); err == nil {
		t.Fatal("Validate() accepted a negative repetitionIndex")
	}
}
