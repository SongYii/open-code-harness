package eval

import "testing"

func TestScenarioDigestBindsWorkspaceExpectedState(t *testing.T) {
	present := validScenario()
	present.Actions = []ScenarioAction{{
		ID: "collect-1", Type: ActionCollect,
		Collect: &CollectAction{WorkspacePath: "secrets.txt"},
	}}
	present.ApprovalScript = nil
	absent := present
	absent.Actions = []ScenarioAction{{
		ID: "collect-1", Type: ActionCollect,
		Collect: &CollectAction{WorkspacePath: "secrets.txt", ExpectedState: WorkspaceExpectedAbsent},
	}}

	presentDigest, err := ScenarioDigest(present)
	if err != nil {
		t.Fatal(err)
	}
	absentDigest, err := ScenarioDigest(absent)
	if err != nil {
		t.Fatal(err)
	}
	if presentDigest == absentDigest {
		t.Fatal("ScenarioDigest did not bind collect.expectedState")
	}
}
