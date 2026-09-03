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

// TestScenarioDigestSensitiveToApprovalScript is a golden mutation check
// (implementation plan Task 1): if ApprovalScript were ever accidentally
// dropped from Scenario's canonical encoding (a stray `json:"-"`, a field
// removed from the digested struct), this test fails, because two Scenarios
// that differ only in ApprovalScript would then digest identically.
func TestScenarioDigestSensitiveToApprovalScript(t *testing.T) {
	base, err := ScenarioDigest(validScenario())
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	changed := validScenario()
	changed.ApprovalScript = []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "read_file", Answer: ApprovalDeny},
	}
	other, err := ScenarioDigest(changed)
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	if base == other {
		t.Fatal("ScenarioDigest did not change when ApprovalScript changed")
	}
}

// TestScenarioDigestSensitiveToRestartMode is the same golden mutation check
// (implementation plan Task 1) for RestartAction.Mode.
func TestScenarioDigestSensitiveToRestartMode(t *testing.T) {
	base := validScenario()
	base.Actions = []ScenarioAction{
		{ID: "restart-1", Type: ActionRestart, Restart: &RestartAction{Mode: RestartModeCleanShutdown}},
	}
	base.ApprovalScript = nil
	baseDigest, err := ScenarioDigest(base)
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	changed := base
	changed.Actions = []ScenarioAction{
		{ID: "restart-1", Type: ActionRestart, Restart: &RestartAction{Mode: RestartModeInterrupt}},
	}
	changedDigest, err := ScenarioDigest(changed)
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	if baseDigest == changedDigest {
		t.Fatal("ScenarioDigest did not change when RestartAction.Mode changed")
	}
}

// TestScenarioDigestSensitiveToActionID is the same golden mutation check
// for ScenarioAction.ID, the new stable coordinate Task 1 introduces.
func TestScenarioDigestSensitiveToActionID(t *testing.T) {
	base := validScenario()
	base.ApprovalScript = nil
	base.Actions = []ScenarioAction{
		{ID: "prompt-1", Type: ActionPrompt, Prompt: &PromptAction{Text: "hello"}},
	}
	baseDigest, err := ScenarioDigest(base)
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	changed := base
	changed.Actions = []ScenarioAction{
		{ID: "prompt-2", Type: ActionPrompt, Prompt: &PromptAction{Text: "hello"}},
	}
	changedDigest, err := ScenarioDigest(changed)
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	if baseDigest == changedDigest {
		t.Fatal("ScenarioDigest did not change when a ScenarioAction's ID changed")
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

func TestEvalSetDigestDeterministic(t *testing.T) {
	first, err := EvalSetDigest(validEvalSet(t))
	if err != nil {
		t.Fatalf("EvalSetDigest: %v", err)
	}
	second, err := EvalSetDigest(validEvalSet(t))
	if err != nil {
		t.Fatalf("EvalSetDigest: %v", err)
	}
	if first != second {
		t.Fatalf("EvalSetDigest is not deterministic: %q != %q", first, second)
	}
}

func TestEvalSetDigestSensitiveToPairingSeed(t *testing.T) {
	base, err := EvalSetDigest(validEvalSet(t))
	if err != nil {
		t.Fatalf("EvalSetDigest: %v", err)
	}
	changed := validEvalSet(t)
	changed.PairingSeed = "a-different-seed"
	other, err := EvalSetDigest(changed)
	if err != nil {
		t.Fatalf("EvalSetDigest: %v", err)
	}
	if base == other {
		t.Fatal("EvalSetDigest did not change when PairingSeed changed")
	}
}

func TestEvalSetDigestSensitiveToArtifactRoot(t *testing.T) {
	base, err := EvalSetDigest(validEvalSet(t))
	if err != nil {
		t.Fatalf("EvalSetDigest: %v", err)
	}
	changed := validEvalSet(t)
	changed.ArtifactRoot = ".eval-other"
	other, err := EvalSetDigest(changed)
	if err != nil {
		t.Fatalf("EvalSetDigest: %v", err)
	}
	if base == other {
		t.Fatal("EvalSetDigest did not change when ArtifactRoot changed")
	}
}

func TestEvalSetDigestRejectsInvalidEvalSet(t *testing.T) {
	invalid := validEvalSet(t)
	invalid.ID = ""
	if _, err := EvalSetDigest(invalid); err == nil {
		t.Fatal("EvalSetDigest accepted an invalid EvalSet")
	}
}
