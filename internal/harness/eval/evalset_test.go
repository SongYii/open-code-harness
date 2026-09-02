package eval

import (
	"reflect"
	"testing"
)

func validEvalSetLimits() EvalSetLimits {
	return EvalSetLimits{TokenCap: 100_000}
}

func validEvalSet(t *testing.T) EvalSet {
	t.Helper()
	scenario := validScenario()
	scenarioDigest, err := ScenarioDigest(scenario)
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	subject := validSubject()
	subjectDigest, err := SubjectDigest(subject)
	if err != nil {
		t.Fatalf("SubjectDigest: %v", err)
	}
	executor := validExecutorInProcess()
	executorDigest, err := ExecutorDigest(executor)
	if err != nil {
		t.Fatalf("ExecutorDigest: %v", err)
	}
	return EvalSet{
		FormatVersion:   FormatVersion,
		Schema:          SchemaEvalSet,
		ID:              "set-1",
		Scenarios:       []ScenarioRef{{ID: scenario.ID, Digest: scenarioDigest}},
		Subjects:        []SubjectRef{{ID: subject.ID, Digest: subjectDigest}},
		Executors:       []ExecutorRef{{ID: executor.ID, Digest: executorDigest}},
		RepetitionCount: 2,
		PairingSeed:     "seed-1",
		Limits:          validEvalSetLimits(),
		ArtifactRoot:    ".eval",
		Lane:            LaneFixture,
	}
}

func TestDecodeEvalSetRoundTrip(t *testing.T) {
	want := validEvalSet(t)
	got, err := DecodeEvalSet(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeEvalSet: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("DecodeEvalSet round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestEvalSetValidateRejectsDuplicateScenario(t *testing.T) {
	set := validEvalSet(t)
	set.Scenarios = append(set.Scenarios, set.Scenarios[0])
	if err := set.Validate(); err == nil {
		t.Fatal("Validate() accepted a duplicate scenario reference")
	}
}

func TestEvalSetValidateRejectsZeroRepetitionCount(t *testing.T) {
	set := validEvalSet(t)
	set.RepetitionCount = 0
	if err := set.Validate(); err == nil {
		t.Fatal("Validate() accepted a zero repetitionCount")
	}
}

func TestEvalSetValidateRejectsUnknownLane(t *testing.T) {
	set := validEvalSet(t)
	set.Lane = "staging"
	if err := set.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown lane")
	}
}

func TestEvalSetValidateRejectsMissingTokenCap(t *testing.T) {
	set := validEvalSet(t)
	set.Limits.TokenCap = 0
	if err := set.Validate(); err == nil {
		t.Fatal("Validate() accepted a zero limits.tokenCap")
	}
}

func TestEvalSetLimitsValidateRejectsAboveHardMaximum(t *testing.T) {
	limits := validEvalSetLimits()
	limits.ConcurrentAttempts = MaxConcurrentAttempts + 1
	if err := limits.validate(); err == nil {
		t.Fatal("validate() accepted concurrentAttempts above its hard maximum")
	}
}

func TestEvalSetLimitsWithDefaultsFillsZeroFields(t *testing.T) {
	limits := EvalSetLimits{TokenCap: 1}.withDefaults()
	if limits.ConcurrentAttempts != DefaultConcurrentAttempts {
		t.Fatalf("ConcurrentAttempts = %d, want default %d", limits.ConcurrentAttempts, DefaultConcurrentAttempts)
	}
	if limits.MaxExpandedAttempts != DefaultMaxExpandedAttempts {
		t.Fatalf("MaxExpandedAttempts = %d, want default %d", limits.MaxExpandedAttempts, DefaultMaxExpandedAttempts)
	}
}
