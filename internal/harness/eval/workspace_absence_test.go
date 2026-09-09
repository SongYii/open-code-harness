package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpectedWorkspaceAbsenceIsCollectedAndVerified(t *testing.T) {
	for _, test := range []struct {
		name       string
		createPath bool
		want       ScoreVerdict
	}{
		{name: "absent", want: ScorePass},
		{name: "present", createPath: true, want: ScoreFail},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newEchoProvider(t)
			subject := testSubject(t, server.Server)
			attemptID := testAttemptID(t)
			directories := testDirectories(t, attemptID)

			scenario := validScenario()
			scenario.Actions = []ScenarioAction{
				newEchoScenarioAction("prompt-1", "hello"),
				{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{
					WorkspacePath: "secrets.txt", ExpectedState: WorkspaceExpectedAbsent,
				}},
			}
			scenario.ApprovalScript = nil
			scenario.RequiredEvidenceRoles = []string{"transcript", "audit", "workspace"}
			scenario.OptionalEvidenceRoles = nil
			scenario.DeterministicVerifierIDs = []string{"manifest-complete-v1", "workspace-paths-absent-v1"}
			documents := publishTestEvidenceDocuments(t, directories, attemptID, scenario, subject)

			execution, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, NewApprovalMatcher(nil))
			if err != nil {
				t.Fatalf("RunAttempt: %v", err)
			}
			if test.createPath {
				if err := os.WriteFile(filepath.Join(directories.Workspace, "secrets.txt"), []byte("unexpected"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			outcome, manifest, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, documents, CollectionLimits{})
			if err != nil {
				t.Fatalf("CollectEvidence: %v", err)
			}
			if outcome.CollectionStatus != CollectionComplete {
				t.Fatalf("CollectionStatus = %q, want complete", outcome.CollectionStatus)
			}

			var observation ManifestEntry
			for _, entry := range manifest.Entries {
				if entry.Role == "workspace" {
					observation = entry
					break
				}
			}
			if observation.State != EntryCollected || observation.ProducedBy != "collect-1" {
				t.Fatalf("workspace observation = %+v, want collected evidence produced by collect-1", observation)
			}
			data, err := os.ReadFile(filepath.Join(directories.Evidence, filepath.FromSlash(observation.Path)))
			if err != nil {
				t.Fatal(err)
			}
			wantState := `"state":"absent"`
			if test.createPath {
				wantState = `"state":"present"`
			}
			if !strings.Contains(string(data), wantState) {
				t.Fatalf("observation = %s, want %s", data, wantState)
			}

			reader, err := NewArtifactReader(directories)
			if err != nil {
				t.Fatal(err)
			}
			verdict, _, err := RunScorer(reader, scenario, Scorer{
				ID: "scorer-1", Version: "v1",
				VerifierIDs: []string{"manifest-complete-v1", "workspace-paths-absent-v1"},
			})
			if err != nil {
				t.Fatalf("RunScorer: %v", err)
			}
			if verdict != test.want {
				t.Fatalf("verdict = %q, want %q", verdict, test.want)
			}
		})
	}
}

// TestTheAbsenceVerifierRefusesToPassHavingCheckedNothing closes the vacuous
// pass.
//
// A Scenario can name this verifier and carry no absence expectation at all —
// most easily by someone deleting the collect action and leaving the
// verifier list alone. Reporting a pass there would be a criterion announcing
// success over an examination it never performed, which is worth less than no
// criterion: a reader counts it as evidence.
//
// Indeterminate is the honest answer. It is also the fail-closed one, because
// the parent design's scoring rules never let an indeterminate criterion stand
// in for a pass.
func TestTheAbsenceVerifierRefusesToPassHavingCheckedNothing(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	// Every action except the collect: the verifier is named, and there is
	// nothing for it to look at.
	scenario.Actions = []ScenarioAction{newEchoScenarioAction("prompt-1", "hello")}
	scenario.ApprovalScript = nil
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit"}
	scenario.OptionalEvidenceRoles = nil
	scenario.DeterministicVerifierIDs = []string{"workspace-paths-absent-v1"}
	documents := publishTestEvidenceDocuments(t, directories, attemptID, scenario, subject)

	execution, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, NewApprovalMatcher(nil))
	if err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if _, _, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, documents, CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}

	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatal(err)
	}
	verdict, results, err := RunScorer(reader, scenario, Scorer{
		ID: "scorer-1", Version: "v1",
		VerifierIDs: []string{"workspace-paths-absent-v1"},
	})
	if err != nil {
		t.Fatalf("RunScorer: %v", err)
	}
	for _, result := range results {
		if result.ID == VerifierWorkspacePathsAbsent && result.Status == ScorePass {
			t.Fatal("the absence verifier passed a Scenario that declares no absence expectation")
		}
	}
	if verdict == ScorePass {
		t.Fatalf("verdict = %q; a Scenario with nothing to check must not score a pass on this criterion", verdict)
	}
}
