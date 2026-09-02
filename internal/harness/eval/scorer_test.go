package eval

import (
	"context"
	"testing"
)

var allCatalogVerifierIDs = []string{
	"manifest-complete-v1",
	"transcript-present-v1",
	"audit-included-v1",
	"outcome-not-infra-failed-v1",
}

func TestRunScorerHappyPathAllPass(t *testing.T) {
	directories, _, _ := collectedHappyAttempt(t)
	reader, scenario := scoredReaderAndScenario(t, directories, allCatalogVerifierIDs)

	verdict, criteria, err := RunScorer(reader, scenario, Scorer{ID: "scorer-1", Version: "v1", VerifierIDs: allCatalogVerifierIDs})
	if err != nil {
		t.Fatalf("RunScorer: %v", err)
	}
	if verdict != ScorePass {
		t.Fatalf("verdict = %q, want %q (criteria: %+v)", verdict, ScorePass, criteria)
	}
	if len(criteria) != len(allCatalogVerifierIDs) {
		t.Fatalf("len(criteria) = %d, want %d", len(criteria), len(allCatalogVerifierIDs))
	}
	for _, criterion := range criteria {
		if criterion.Status != ScorePass {
			t.Fatalf("criterion %+v, want status %q", criterion, ScorePass)
		}
	}
}

func TestRunScorerIndeterminateWhenRequiredEvidenceMissing(t *testing.T) {
	// A scenario that declares "workspace" required but whose collect
	// action names a file the Subject never creates, so collection is
	// Partial and manifest-complete-v1 must report Indeterminate.
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "hello"),
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "never-created.txt"}},
	}
	scenario.ApprovalScript = nil
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit", "workspace"}
	scenario.OptionalEvidenceRoles = nil
	scenario.DeterministicVerifierIDs = []string{"manifest-complete-v1"}

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if _, _, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}

	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	verdict, criteria, err := RunScorer(reader, scenario, Scorer{ID: "scorer-1", Version: "v1", VerifierIDs: []string{"manifest-complete-v1"}})
	if err != nil {
		t.Fatalf("RunScorer: %v", err)
	}
	if verdict != ScoreIndeterminate {
		t.Fatalf("verdict = %q, want %q (criteria: %+v)", verdict, ScoreIndeterminate, criteria)
	}
}

func TestRunScorerRejectsVerifierNotDeclaredByScenario(t *testing.T) {
	directories, _, _ := collectedHappyAttempt(t)
	reader, scenario := scoredReaderAndScenario(t, directories, []string{"manifest-complete-v1"})

	_, _, err := RunScorer(reader, scenario, Scorer{ID: "scorer-1", Version: "v1", VerifierIDs: []string{"transcript-present-v1"}})
	if err == nil {
		t.Fatal("RunScorer() error = nil, want a refusal for a verifier the scenario never declared")
	}
}

func TestRunScorerRejectsUnknownVerifierID(t *testing.T) {
	directories, _, _ := collectedHappyAttempt(t)
	reader, scenario := scoredReaderAndScenario(t, directories, []string{"not-a-real-verifier"})

	_, _, err := RunScorer(reader, scenario, Scorer{ID: "scorer-1", Version: "v1", VerifierIDs: []string{"not-a-real-verifier"}})
	if err == nil {
		t.Fatal("RunScorer() error = nil, want a refusal for an unregistered verifier id")
	}
}

func TestVerifyOutcomeNotInfraFailedFailsOnInfraFailure(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)
	directories.Workspace = directories.Workspace + "-does-not-exist" // forces composition.Open to fail

	scenario := runnerScenario("infra-fail-scenario")
	scenario.DeterministicVerifierIDs = []string{"outcome-not-infra-failed-v1"}

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err == nil {
		t.Fatal("RunAttempt() error = nil, want composition.Open to fail against a missing workspace")
	}
	if !execution.WriterStopped {
		t.Fatal("WriterStopped = false, want true (nothing was ever opened)")
	}
	if execution.Outcome.Status != OutcomeInfraFailed {
		t.Fatalf("Outcome.Status = %q, want %q", execution.Outcome.Status, OutcomeInfraFailed)
	}

	if _, _, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	verdict, criteria, err := RunScorer(reader, scenario, Scorer{ID: "scorer-1", Version: "v1", VerifierIDs: []string{"outcome-not-infra-failed-v1"}})
	if err != nil {
		t.Fatalf("RunScorer: %v", err)
	}
	if verdict != ScoreFail {
		t.Fatalf("verdict = %q, want %q (criteria: %+v)", verdict, ScoreFail, criteria)
	}
}

// scoredReaderAndScenario opens a real ArtifactReader over directories'
// already-collected evidence and returns a Scenario declaring
// verifierIDs, matching how RunScorer cross-checks a Scorer's verifiers
// against Scenario.DeterministicVerifierIDs.
func scoredReaderAndScenario(t *testing.T, directories AttemptRootDirectories, verifierIDs []string) (*ArtifactReader, Scenario) {
	t.Helper()
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	scenario := validScenario()
	scenario.DeterministicVerifierIDs = verifierIDs
	return reader, scenario
}
