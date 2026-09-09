package eval

import "testing"

func TestCollectExpectedStateValidation(t *testing.T) {
	scenario := validScenario()
	scenario.Actions = []ScenarioAction{{
		ID: "collect-1", Type: ActionCollect,
		Collect: &CollectAction{WorkspacePath: "secrets.txt", ExpectedState: WorkspaceExpectedAbsent},
	}}
	scenario.ApprovalScript = nil
	if err := scenario.Validate(); err != nil {
		t.Fatalf("Validate() rejected expected absence: %v", err)
	}

	scenario.Actions[0].Collect.ExpectedState = "unknown"
	if err := scenario.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown workspace expectedState")
	}

	scenario.Actions[0].Collect = &CollectAction{VerifierFact: "fact-1", ExpectedState: WorkspaceExpectedAbsent}
	if err := scenario.Validate(); err == nil {
		t.Fatal("Validate() accepted expectedState beside verifierFact")
	}
}

func TestScenarioRequiredWorkspaceRoleRequiresWorkspaceCollection(t *testing.T) {
	scenario := validScenario()
	scenario.Actions = []ScenarioAction{{
		ID: "collect-1", Type: ActionCollect,
		Collect: &CollectAction{VerifierFact: "fact-1"},
	}}
	scenario.ApprovalScript = nil
	scenario.RequiredEvidenceRoles = []string{"transcript", "workspace"}
	scenario.OptionalEvidenceRoles = nil
	if err := scenario.Validate(); err == nil {
		t.Fatal("Validate() accepted required workspace evidence without a workspace collection action")
	}
}
