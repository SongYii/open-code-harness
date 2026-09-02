package eval

import "testing"

func TestScenarioDigestDeterministic(t *testing.T) {
	first, err := ScenarioDigest(validScenario())
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	second, err := ScenarioDigest(validScenario())
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	if first != second {
		t.Fatalf("ScenarioDigest is not deterministic: %q != %q", first, second)
	}
	if !digestStringPattern.MatchString(string(first)) {
		t.Fatalf("ScenarioDigest %q does not match %s", first, digestStringPattern.String())
	}
}

func TestScenarioDigestSensitiveToChange(t *testing.T) {
	base, err := ScenarioDigest(validScenario())
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	changed := validScenario()
	changed.Description = "a different scenario"
	other, err := ScenarioDigest(changed)
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	if base == other {
		t.Fatal("ScenarioDigest did not change when Description changed")
	}
}

func TestScenarioDigestRejectsInvalidScenario(t *testing.T) {
	invalid := validScenario()
	invalid.ID = ""
	if _, err := ScenarioDigest(invalid); err == nil {
		t.Fatal("ScenarioDigest accepted an invalid Scenario")
	}
}

func TestSubjectDigestDeterministic(t *testing.T) {
	first, err := SubjectDigest(validSubject())
	if err != nil {
		t.Fatalf("SubjectDigest: %v", err)
	}
	second, err := SubjectDigest(validSubject())
	if err != nil {
		t.Fatalf("SubjectDigest: %v", err)
	}
	if first != second {
		t.Fatalf("SubjectDigest is not deterministic: %q != %q", first, second)
	}
}

func TestSubjectDigestSensitiveToChange(t *testing.T) {
	base, err := SubjectDigest(validSubject())
	if err != nil {
		t.Fatalf("SubjectDigest: %v", err)
	}
	changed := validSubject()
	changed.Provider.ModelID = "a-different-model"
	other, err := SubjectDigest(changed)
	if err != nil {
		t.Fatalf("SubjectDigest: %v", err)
	}
	if base == other {
		t.Fatal("SubjectDigest did not change when Provider.ModelID changed")
	}
}

func TestSubjectDigestRejectsInvalidSubject(t *testing.T) {
	invalid := validSubject()
	invalid.Provider.NormalizedEndpoint = "https://user:pass@api.example.com/v1"
	if _, err := SubjectDigest(invalid); err == nil {
		t.Fatal("SubjectDigest accepted an invalid Subject")
	}
}

func TestExecutorDigestDeterministic(t *testing.T) {
	first, err := ExecutorDigest(validExecutorInProcess())
	if err != nil {
		t.Fatalf("ExecutorDigest: %v", err)
	}
	second, err := ExecutorDigest(validExecutorInProcess())
	if err != nil {
		t.Fatalf("ExecutorDigest: %v", err)
	}
	if first != second {
		t.Fatalf("ExecutorDigest is not deterministic: %q != %q", first, second)
	}
}

func TestExecutorDigestDiffersByKind(t *testing.T) {
	inProcess, err := ExecutorDigest(validExecutorInProcess())
	if err != nil {
		t.Fatalf("ExecutorDigest: %v", err)
	}
	acpSubprocess, err := ExecutorDigest(validExecutorACPSubprocess())
	if err != nil {
		t.Fatalf("ExecutorDigest: %v", err)
	}
	if inProcess == acpSubprocess {
		t.Fatal("ExecutorDigest did not differ between in_process and acp_subprocess executors")
	}
}

func TestExecutorDigestRejectsInvalidExecutor(t *testing.T) {
	invalid := validExecutorInProcess()
	invalid.Kind = "unknown"
	if _, err := ExecutorDigest(invalid); err == nil {
		t.Fatal("ExecutorDigest accepted an invalid Executor")
	}
}
