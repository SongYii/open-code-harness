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
