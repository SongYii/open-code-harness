package eval

import (
	"context"
	"testing"
)

// TestToolWorkspaceVerifiersFailClosedOnEmptyAuditEvidence proves Task
// 16's own required fail-closed check for every verifier this file adds:
// real, readable audit evidence (collectTranscriptAndAudit runs
// unconditionally regardless of a Scenario's own RequiredEvidenceRoles,
// verified directly rather than assumed) that genuinely carries none of
// the specific facts a given verifier looks for (an ordinary happy-path
// Attempt with no tool calls at all) must report Fail, not Pass -- the
// evidence exists and was readable, it simply never contained the
// claimed behavior. Every verifier here shares readAuditEvents with the
// package's own pre-existing audit-backed verifiers
// (verifyToolApprovalFailureObserved, verifyContextCompactionObserved),
// so the separate, genuinely-absent-evidence case (Indeterminate) is
// exactly their own already-established contract, not retested here.
func TestToolWorkspaceVerifiersFailClosedOnEmptyAuditEvidence(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "hello"),
	}
	scenario.ApprovalScript = nil
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit"}
	scenario.OptionalEvidenceRoles = nil
	documents := publishTestEvidenceDocuments(t, directories, attemptID, scenario, subject)

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if _, _, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, documents, CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}

	for _, verifier := range []struct {
		name string
		fn   Verifier
	}{
		{"read-file-completed-v1", verifyReadFileCompleted},
		{"redaction-observed-v1", verifyRedactionObserved},
		{"expected-tool-failure-observed-v1", verifyExpectedToolFailureObserved},
		{"containment-refused-v1", verifyContainmentRefused},
	} {
		t.Run(verifier.name, func(t *testing.T) {
			result := verifier.fn(reader, scenario)
			if result.Status != ScoreFail {
				t.Fatalf("Status = %q, want %q: real, readable audit evidence with none of the claimed behavior must Fail, not Pass", result.Status, ScoreFail)
			}
		})
	}
}
