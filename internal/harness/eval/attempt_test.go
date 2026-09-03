package eval

import (
	"errors"
	"reflect"
	"testing"
	"time"
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

func validAttemptPaths() AttemptPaths {
	return AttemptPaths{
		Root:      "/attempts/attempt-1",
		Workspace: "/attempts/attempt-1/workspace",
		Database:  "/attempts/attempt-1/database",
		Audit:     "/attempts/attempt-1/audit",
		Process:   "/attempts/attempt-1/process",
		Log:       "/attempts/attempt-1/log",
		Evidence:  "/attempts/attempt-1/evidence",
	}
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
		Paths:           validAttemptPaths(),
		RuntimeID:       "runtime-attempt-1",
		PublishedAt:     time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
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

func TestAttemptValidateRejectsRelativePath(t *testing.T) {
	attempt := validAttempt(t)
	attempt.Paths.Workspace = "relative/workspace"
	if err := attempt.Validate(); err == nil {
		t.Fatal("Validate() accepted a relative paths.workspace")
	}
}

func TestAttemptValidateRejectsEmptyPath(t *testing.T) {
	attempt := validAttempt(t)
	attempt.Paths.Audit = ""
	if err := attempt.Validate(); err == nil {
		t.Fatal("Validate() accepted an empty paths.audit")
	}
}

func TestAttemptValidateRejectsMissingRuntimeID(t *testing.T) {
	attempt := validAttempt(t)
	attempt.RuntimeID = ""
	if err := attempt.Validate(); err == nil {
		t.Fatal("Validate() accepted a missing runtimeId")
	}
}

func TestAttemptValidateRejectsMissingPublishedAt(t *testing.T) {
	attempt := validAttempt(t)
	attempt.PublishedAt = time.Time{}
	if err := attempt.Validate(); err == nil {
		t.Fatal("Validate() accepted a zero publishedAt")
	}
}
