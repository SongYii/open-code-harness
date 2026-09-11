package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func contextAutoQualityLiveExampleSetPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "eval", "sets", "context-auto-quality-live.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestContextAutoQualityExampleUsesAutomaticSummaryWithoutFocus(t *testing.T) {
	tree, err := loadDocumentTree(contextAutoQualityLiveExampleSetPath(t))
	if err != nil {
		t.Fatalf("loadDocumentTree: %v", err)
	}
	scenario := tree.Scenarios["context-auto-quality"]
	subject := tree.Subjects["context-auto-quality-live-example"]
	executor := tree.Executors["smoke-executor"]

	for _, action := range scenario.Actions {
		if action.Type == eval.ActionCompact {
			t.Fatalf("Scenario action %q is an explicit compact shortcut", action.ID)
		}
	}

	var capturesMu sync.Mutex
	var summaryRequests []string
	var summaryEfforts, conversationEfforts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, maxFixtureRequestBytes))
		_ = r.Body.Close()
		if readErr != nil {
			writeFixtureContractFailure(w, "capture could not read request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var request contextFixtureRequest
		// Decode independently, then restore the exact bytes so the production
		// fixture handler must decode them again rather than trust this capture.
		if err := json.Unmarshal(body, &request); err != nil {
			writeFixtureContractFailure(w, "capture received invalid JSON")
			return
		}
		if isSummarizerRequest(request) {
			capturesMu.Lock()
			summaryRequests = append(summaryRequests, request.Messages[len(request.Messages)-1].Content)
			summaryEfforts = append(summaryEfforts, request.ReasoningEffort)
			capturesMu.Unlock()
		} else {
			capturesMu.Lock()
			conversationEfforts = append(conversationEfforts, request.ReasoningEffort)
			capturesMu.Unlock()
		}
		contextMechanismFixtureScript(w, r)
	}))
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
	fixtureRoot := filepath.Join(repoRootDir(t), "eval", "scenarios", "context-auto-quality", "fixture")

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

	capturesMu.Lock()
	captured := append([]string(nil), summaryRequests...)
	capturedSummaryEfforts := append([]string(nil), summaryEfforts...)
	capturedConversationEfforts := append([]string(nil), conversationEfforts...)
	capturesMu.Unlock()
	if len(captured) == 0 {
		t.Fatalf("no automatic summarizer request was observed; result=%+v conversation_requests=%d", results[0], len(capturedConversationEfforts))
	}
	for _, effort := range capturedSummaryEfforts {
		if effort != "none" {
			t.Fatalf("summary reasoning_effort = %q, want none", effort)
		}
	}
	if len(capturedConversationEfforts) == 0 {
		t.Fatal("no conversation request was observed")
	}
	for _, effort := range capturedConversationEfforts {
		if effort != "high" {
			t.Fatalf("conversation reasoning_effort = %q, want high", effort)
		}
	}
	first := captured[0]
	if !strings.Contains(first, "长期有效的硬性约束") || !strings.Contains(first, "secrets.txt") {
		t.Fatal("first automatic summarizer request did not receive the original constraint-bearing source Turn")
	}
	if strings.Contains(first, "## MANUAL FOCUS") {
		t.Fatal("automatic summarizer request unexpectedly contained manual focus")
	}

	reader, err := eval.NewArtifactReader(eval.AttemptRootDirectoriesFor(
		filepath.Join(artifactRoot, string(results[0].AttemptID)),
	))
	if err != nil {
		t.Fatal(err)
	}
	verdict, criteria, err := eval.RunScorer(reader, scenario, eval.Scorer{
		ID: "context-auto-quality-fixture-proof", Version: "v1",
		VerifierIDs: scenario.DeterministicVerifierIDs,
	})
	if err != nil {
		t.Fatalf("RunScorer: %v", err)
	}
	if verdict != eval.ScorePass {
		t.Fatalf("verdict = %q, want pass; criteria=%+v", verdict, criteria)
	}
}
