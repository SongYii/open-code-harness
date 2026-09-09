package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func TestContextQualityExampleReachesRealCompactionAndAbsenceVerificationWithFixtureProvider(t *testing.T) {
	tree, err := loadDocumentTree(contextQualityLiveExampleSetPath(t))
	if err != nil {
		t.Fatalf("loadDocumentTree: %v", err)
	}
	scenario := tree.Scenarios["context-quality"]
	subject := tree.Subjects["context-quality-live-example"]
	executor := tree.Executors["smoke-executor"]

	server := httptest.NewServer(http.HandlerFunc(contextMechanismFixtureScript))
	defer server.Close()
	subject.Provider.NormalizedEndpoint = "fixture://context-mechanism"
	subject.Provider.Lane = eval.ProviderLaneFixture
	t.Setenv(subject.Provider.CredentialEnvVar, "fixture-only")

	scenarioDigest, err := eval.ScenarioDigest(scenario)
	if err != nil {
		t.Fatal(err)
	}
	subjectDigest, err := eval.SubjectDigest(subject)
	if err != nil {
		t.Fatal(err)
	}
	executorDigest, err := eval.ExecutorDigest(executor)
	if err != nil {
		t.Fatal(err)
	}

	set := tree.Set
	set.Lane = eval.LaneFixture
	set.JudgeConfigDigest = ""
	set.Scenarios = []eval.ScenarioRef{{ID: scenario.ID, Digest: scenarioDigest}}
	set.Subjects = []eval.SubjectRef{{ID: subject.ID, Digest: subjectDigest}}
	set.Executors = []eval.ExecutorRef{{ID: executor.ID, Digest: executorDigest}}
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	fixtureRoot := filepath.Join(repoRootDir(t), "eval", "scenarios", "context-quality", "fixture")

	results, err := eval.RunEvalSet(context.Background(), eval.RunnerInputs{
		Set:                       set,
		Scenarios:                 map[eval.ScenarioID]eval.Scenario{scenario.ID: scenario},
		Subjects:                  map[eval.SubjectID]eval.Subject{subject.ID: subject},
		Executors:                 map[eval.ExecutorID]eval.Executor{executor.ID: executor},
		FixtureSources:            map[eval.ScenarioID]string{scenario.ID: fixtureRoot},
		ProviderEndpointOverrides: map[eval.SubjectID]string{subject.ID: server.URL + "/v1"},
		ArtifactRootOverride:      artifactRoot,
	})
	if err != nil {
		t.Fatalf("RunEvalSet: %v", err)
	}
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v, want one successful run", results)
	}
	if results[0].Outcome.Status != eval.OutcomeCompleted ||
		results[0].Outcome.CollectionStatus != eval.CollectionComplete {
		t.Fatalf("Outcome = %+v, want completed/complete", results[0].Outcome)
	}

	reader, err := eval.NewArtifactReader(eval.AttemptRootDirectoriesFor(
		filepath.Join(artifactRoot, string(results[0].AttemptID)),
	))
	if err != nil {
		t.Fatal(err)
	}
	verdict, criteria, err := eval.RunScorer(reader, scenario, eval.Scorer{
		ID: "context-quality-fixture-proof", Version: "v1",
		VerifierIDs: scenario.DeterministicVerifierIDs,
	})
	if err != nil {
		t.Fatalf("RunScorer: %v", err)
	}
	if verdict != eval.ScorePass {
		t.Fatalf("verdict = %q, want pass; criteria=%+v", verdict, criteria)
	}
}
