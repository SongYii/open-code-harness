package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func repoRootDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func checkedInJudgeConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRootDir(t), "eval", "judges", "context-quality-judge.example.json")
}

// validJudgeJSON is one strictly-shaped judge response body, matching
// prompts/quality_judge_v1.md's own required output shape.
func validJudgeJSON(t *testing.T, verdict string, criterionIDs []string, evidenceReferences []string) string {
	t.Helper()
	criteria := make([]map[string]any, 0, len(criterionIDs))
	for _, id := range criterionIDs {
		criteria = append(criteria, map[string]any{"id": id, "status": verdict})
	}
	payload := map[string]any{
		"verdict":            verdict,
		"score":              nil,
		"criteria":           criteria,
		"evidenceReferences": evidenceReferences,
		"rationale":          "fixture judge rationale",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal judge output: %v", err)
	}
	return string(data)
}

// newJudgeSSEServer streams body back as OpenAI-compatible SSE content
// deltas, so the caller under test consumes a real stream rather than a
// pre-assembled string.
func newJudgeSSEServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		write := func(payload string) {
			fmt.Fprintf(w, "data: %s\n\n", payload)
			if flusher != nil {
				flusher.Flush()
			}
		}
		write(`{"id":"judge-1","object":"chat.completion.chunk","created":1,"model":"judge","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
		// Split the body so the test proves the caller concatenates deltas.
		for _, chunk := range splitInHalf(body) {
			quoted, err := json.Marshal(chunk)
			if err != nil {
				t.Errorf("marshal chunk: %v", err)
				return
			}
			write(fmt.Sprintf(`{"id":"judge-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":%s},"finish_reason":null}]}`, quoted))
		}
		write(`{"id":"judge-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150}}`)
		write("[DONE]")
	}))
	t.Cleanup(server.Close)
	return server
}

func splitInHalf(value string) []string {
	if len(value) < 2 {
		return []string{value}
	}
	middle := len(value) / 2
	return []string{value[:middle], value[middle:]}
}

// loopbackJudgeConfig is the checked-in frozen document repointed at a
// test server. Only the endpoint and model move; everything the judge
// contract depends on stays exactly as shipped.
func loopbackJudgeConfig(t *testing.T, endpoint string) eval.JudgeConfig {
	t.Helper()
	config, err := loadJudgeConfig(checkedInJudgeConfigPath(t))
	if err != nil {
		t.Fatalf("loadJudgeConfig: %v", err)
	}
	config.Provider.NormalizedEndpoint = endpoint
	config.Provider.ModelID = "judge-fixture-model"
	return config
}

func TestJudgeCallerConsumesFixtureSSE(t *testing.T) {
	body := validJudgeJSON(t, "pass", []string{"constraint-preservation"}, []string{"transcript.jsonl"})
	server := newJudgeSSEServer(t, body)
	config := loopbackJudgeConfig(t, server.URL)
	t.Setenv(config.Provider.CredentialEnvVar, "test-key")

	caller, err := newOpenAICompatibleJudgeCaller(config, server.Client(), true)
	if err != nil {
		t.Fatalf("newOpenAICompatibleJudgeCaller: %v", err)
	}
	raw, usage, err := caller(context.Background(), eval.QualityJudgePromptV1, `<criteria>[]</criteria><evidence></evidence>`)
	if err != nil {
		t.Fatalf("caller: %v", err)
	}
	if !strings.Contains(raw, `"verdict":"pass"`) {
		t.Fatalf("raw response does not carry the streamed verdict: %q", raw)
	}
	if usage.InputTokens != 120 || usage.OutputTokens != 30 {
		t.Fatalf("usage = %+v, want the provider's own reported token counts", usage)
	}
}

// TestJudgeCallerRefusesPlaintextEndpointInProduction pins the split that
// makes the test above safe: the same constructor with production's own
// arguments refuses the exact endpoint the test just used.
func TestJudgeCallerRefusesPlaintextEndpointInProduction(t *testing.T) {
	server := newJudgeSSEServer(t, "{}")
	config := loopbackJudgeConfig(t, server.URL)
	if !strings.HasPrefix(server.URL, "http://") {
		t.Fatalf("expected a plaintext test server, got %q", server.URL)
	}
	if _, err := newOpenAICompatibleJudgeCaller(config, nil, false); err == nil {
		t.Fatal("production arguments accepted a plaintext endpoint")
	}
}

// TestJudgeCallerSendsNoCredentialWhenTheVariableIsUnset proves the
// credential is resolved at call time from the named variable only.
func TestJudgeCallerSendsNoCredentialWhenTheVariableIsUnset(t *testing.T) {
	server := newJudgeSSEServer(t, "{}")
	config := loopbackJudgeConfig(t, server.URL)
	t.Setenv(config.Provider.CredentialEnvVar, "")

	caller, err := newOpenAICompatibleJudgeCaller(config, server.Client(), true)
	if err != nil {
		t.Fatalf("newOpenAICompatibleJudgeCaller: %v", err)
	}
	if _, _, err := caller(context.Background(), eval.QualityJudgePromptV1, "bundle"); err == nil {
		t.Fatal("the judge call proceeded with no credential in the named variable")
	}
}

// collectedLiveJudgeAttemptDir publishes and collects one live-lane
// Attempt's evidence through the eval package's own public API, without
// ever launching a Subject: a live Subject's production path refuses a
// plaintext loopback endpoint, so a CLI test must build the evidence
// chain rather than pretend to execute one.
func collectedLiveJudgeAttemptDir(t *testing.T, config eval.JudgeConfig, infraFailed bool) eval.AttemptRootDirectories {
	t.Helper()
	root := t.TempDir()
	attemptID, err := eval.NewAttemptID()
	if err != nil {
		t.Fatalf("NewAttemptID: %v", err)
	}
	directories, err := eval.NewAttemptRoot(root, attemptID)
	if err != nil {
		t.Fatalf("NewAttemptRoot: %v", err)
	}

	tree, err := loadDocumentTree(contextQualityLiveExampleSetPath(t))
	if err != nil {
		t.Fatalf("loadDocumentTree: %v", err)
	}
	scenario := tree.Scenarios["context-quality"]
	// Only the frozen identity documents are collected for this Attempt,
	// so require a role staging always writes and verify it deterministically.
	scenario.RequiredEvidenceRoles = []string{"scenario"}
	scenario.OptionalEvidenceRoles = nil
	scenario.DeterministicVerifierIDs = []string{"manifest-complete-v1", "outcome-not-infra-failed-v1"}
	subject := tree.Subjects["context-quality-live-example"]
	executor := tree.Executors["smoke-executor"]

	scenarioDigest, err := eval.ScenarioDigest(scenario)
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	subjectDigest, err := eval.SubjectDigest(subject)
	if err != nil {
		t.Fatalf("SubjectDigest: %v", err)
	}
	executorDigest, err := eval.ExecutorDigest(executor)
	if err != nil {
		t.Fatalf("ExecutorDigest: %v", err)
	}
	configDigest, err := eval.JudgeConfigDigest(config)
	if err != nil {
		t.Fatalf("JudgeConfigDigest: %v", err)
	}

	set := tree.Set
	set.Scenarios = []eval.ScenarioRef{{ID: scenario.ID, Digest: scenarioDigest}}
	set.Subjects = []eval.SubjectRef{{ID: subject.ID, Digest: subjectDigest}}
	set.Executors = []eval.ExecutorRef{{ID: executor.ID, Digest: executorDigest}}
	set.JudgeConfigDigest = configDigest

	attempt := eval.Attempt{
		FormatVersion: 1, Schema: "och.eval.attempt", ID: attemptID, EvalSetID: set.ID,
		ScenarioID: scenario.ID, ScenarioDigest: scenarioDigest,
		SubjectID: subject.ID, SubjectDigest: subjectDigest,
		ExecutorID: executor.ID, ExecutorDigest: executorDigest,
		RepetitionIndex: 0,
		Paths: eval.AttemptPaths{
			Root: directories.Root, Workspace: directories.Workspace, Database: directories.Database,
			Audit: directories.Audit, Process: directories.Process, Log: directories.Log, Evidence: directories.Evidence,
		},
		RuntimeID:   string(attemptID) + "-launch-0",
		PublishedAt: time.Now().UTC(),
	}
	if err := eval.PublishAttempt(directories.Root, attempt); err != nil {
		t.Fatalf("PublishAttempt: %v", err)
	}

	started := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	outcome := eval.Outcome{
		FormatVersion: 1, Schema: "och.eval.outcome", AttemptID: attemptID,
		Status: eval.OutcomeCompleted, Code: "ok", Message: "attempt completed",
		StartedAt: started, EndedAt: started.Add(time.Minute),
		TerminalSession:  &eval.TerminalSessionFacts{SessionID: "session-1", TurnCount: 1},
		CollectionStatus: eval.CollectionComplete,
	}
	if infraFailed {
		outcome.Status = eval.OutcomeInfraFailed
		outcome.Code = "fixture_failed"
	}

	documents := eval.EvidenceDocuments{
		Scenario: scenario, Subject: subject, Executor: executor,
		Attempt: attempt, EvalSet: set, JudgeConfig: &config,
	}
	if _, _, err := eval.CollectEvidence(context.Background(), directories,
		eval.ExecutionOutcome{WriterStopped: true, Outcome: outcome}, outcome,
		documents, eval.CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	return directories
}

// judgeCLIConfig narrows the checked-in config to the one evidence role
// this synthetic Attempt actually collects, so the run reaches the model
// instead of stopping fail-closed on omitted evidence.
func judgeCLIConfig(t *testing.T) eval.JudgeConfig {
	t.Helper()
	config, err := loadJudgeConfig(checkedInJudgeConfigPath(t))
	if err != nil {
		t.Fatalf("loadJudgeConfig: %v", err)
	}
	config.Criteria = []eval.JudgeCriterion{{
		ID: "constraint-preservation", Rubric: "Judge the frozen scenario.", EvidenceRoles: []string{"scenario"},
	}}
	return config
}

func fixedCLIJudgeCaller(t *testing.T, verdict string) eval.JudgeCaller {
	t.Helper()
	body := validJudgeJSON(t, verdict, []string{"constraint-preservation"}, []string{"scenario.json"})
	return func(context.Context, string, string) (string, eval.ScorerUsage, error) {
		return body, eval.ScorerUsage{InputTokens: 100, OutputTokens: 20}, nil
	}
}

func runFixtureJudgeCLI(t *testing.T, verdict string) (int, eval.Score, string) {
	t.Helper()
	t.Setenv("OCH_EVAL_LIVE_CONFIRM", eval.LiveConfirmValue)
	config := judgeCLIConfig(t)
	directories := collectedLiveJudgeAttemptDir(t, config, false)

	var stdout, stderr bytes.Buffer
	exitCode := runJudgeAndReport(context.Background(), directories, config, true,
		fixedCLIJudgeCaller(t, verdict), nil, &stdout, &stderr)
	if stdout.Len() == 0 {
		return exitCode, eval.Score{}, stderr.String()
	}
	score, err := eval.DecodeScore(stdout.Bytes())
	if err != nil {
		t.Fatalf("DecodeScore(%s): %v", stdout.String(), err)
	}
	return exitCode, score, stderr.String()
}

// TestJudgeCLIQualityFailIsAdvisory pins the exit contract: a live
// quality Fail is a finding to read, not a build to break.
func TestJudgeCLIQualityFailIsAdvisory(t *testing.T) {
	exitCode, score, stderr := runFixtureJudgeCLI(t, "fail")
	if exitCode != exitOK {
		t.Fatalf("exit = %d, want %d (a quality Fail is advisory); stderr=%s", exitCode, exitOK, stderr)
	}
	if score.Verdict != eval.ScoreFail {
		t.Fatalf("Score.Verdict = %q, want %q", score.Verdict, eval.ScoreFail)
	}
	if score.Lane != eval.LaneLive {
		t.Fatalf("Score.Lane = %q, want %q", score.Lane, eval.LaneLive)
	}
}

func TestJudgeCLIQualityPassPublishesOneScoreDocument(t *testing.T) {
	exitCode, score, stderr := runFixtureJudgeCLI(t, "pass")
	if exitCode != exitOK {
		t.Fatalf("exit = %d, want %d; stderr=%s", exitCode, exitOK, stderr)
	}
	if score.Verdict != eval.ScorePass {
		t.Fatalf("Score.Verdict = %q, want %q (rationale: %s)", score.Verdict, eval.ScorePass, score.Rationale)
	}
	if score.ScorerUsage == nil || score.ScorerUsage.CostStatus != eval.CostStatusUnavailable {
		t.Fatalf("ScorerUsage = %+v, want an explicit unavailable cost with no price table", score.ScorerUsage)
	}
}

// TestJudgeCLIRefusesWithoutConsentBeforeReachingTheProvider is the CLI's
// own half of the dual-consent gate.
func TestJudgeCLIRefusesWithoutConsentBeforeReachingTheProvider(t *testing.T) {
	config := judgeCLIConfig(t)
	directories := collectedLiveJudgeAttemptDir(t, config, false)

	for _, testCase := range []struct {
		name    string
		live    bool
		confirm string
	}{
		{"no flag and no environment confirmation", false, ""},
		{"flag without the environment confirmation", true, ""},
		{"environment confirmation without the flag", false, eval.LiveConfirmValue},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("OCH_EVAL_LIVE_CONFIRM", testCase.confirm)
			called := false
			caller := func(context.Context, string, string) (string, eval.ScorerUsage, error) {
				called = true
				return "", eval.ScorerUsage{}, nil
			}
			var stdout, stderr bytes.Buffer
			exitCode := runJudgeAndReport(context.Background(), directories, config, testCase.live, caller, nil, &stdout, &stderr)
			if exitCode != exitValidation {
				t.Fatalf("exit = %d, want %d; stderr=%s", exitCode, exitValidation, stderr.String())
			}
			if called {
				t.Fatal("the CLI reached the provider without live consent")
			}
			if stdout.Len() != 0 {
				t.Fatalf("a refused judge run still printed a Score: %s", stdout.String())
			}
		})
	}
}

// TestJudgeCLIDeterministicFailurePreventsTheProviderCall pins the
// prerequisite gate at the CLI boundary, including its exit code: the
// Score is published, so the command still succeeds.
func TestJudgeCLIDeterministicFailurePreventsTheProviderCall(t *testing.T) {
	t.Setenv("OCH_EVAL_LIVE_CONFIRM", eval.LiveConfirmValue)
	config := judgeCLIConfig(t)
	directories := collectedLiveJudgeAttemptDir(t, config, true)

	called := false
	caller := func(context.Context, string, string) (string, eval.ScorerUsage, error) {
		called = true
		return "", eval.ScorerUsage{}, nil
	}
	var stdout, stderr bytes.Buffer
	exitCode := runJudgeAndReport(context.Background(), directories, config, true, caller, nil, &stdout, &stderr)
	if called {
		t.Fatal("an infra-failed Attempt still reached the provider")
	}
	if exitCode != exitOK {
		t.Fatalf("exit = %d, want %d; stderr=%s", exitCode, exitOK, stderr.String())
	}
	score, err := eval.DecodeScore(stdout.Bytes())
	if err != nil {
		t.Fatalf("DecodeScore: %v", err)
	}
	if score.Verdict != eval.ScoreIndeterminate {
		t.Fatalf("Score.Verdict = %q, want %q", score.Verdict, eval.ScoreIndeterminate)
	}
	if !strings.Contains(stderr.String(), "deterministic prerequisites") {
		t.Fatalf("stderr does not explain why judging was skipped: %s", stderr.String())
	}
}

// TestJudgeCLIStreamsARealSSEResponseIntoAnAppendedScore is the
// end-to-end case: a fixture SSE stream reaches an appended Score through
// the real adapter, with no shortcut caller anywhere in the path.
func TestJudgeCLIStreamsARealSSEResponseIntoAnAppendedScore(t *testing.T) {
	t.Setenv("OCH_EVAL_LIVE_CONFIRM", eval.LiveConfirmValue)
	body := validJudgeJSON(t, "pass", []string{"constraint-preservation"}, []string{"scenario.json"})
	server := newJudgeSSEServer(t, body)

	config := judgeCLIConfig(t)
	config.Provider.NormalizedEndpoint = server.URL
	config.Provider.ModelID = "judge-fixture-model"
	t.Setenv(config.Provider.CredentialEnvVar, "test-key")

	directories := collectedLiveJudgeAttemptDir(t, config, false)
	caller, err := newOpenAICompatibleJudgeCaller(config, server.Client(), true)
	if err != nil {
		t.Fatalf("newOpenAICompatibleJudgeCaller: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if exitCode := runJudgeAndReport(context.Background(), directories, config, true, caller, nil, &stdout, &stderr); exitCode != exitOK {
		t.Fatalf("exit = %d, want %d; stderr=%s", exitCode, exitOK, stderr.String())
	}
	score, err := eval.DecodeScore(stdout.Bytes())
	if err != nil {
		t.Fatalf("DecodeScore: %v", err)
	}
	if score.Verdict != eval.ScorePass {
		t.Fatalf("Score.Verdict = %q, want %q (rationale: %s)", score.Verdict, eval.ScorePass, score.Rationale)
	}
	if score.ScorerUsage == nil || score.ScorerUsage.InputTokens != 120 || score.ScorerUsage.OutputTokens != 30 {
		t.Fatalf("ScorerUsage = %+v, want the streamed provider usage", score.ScorerUsage)
	}

	scores, err := eval.ReadScores(directories.Root)
	if err != nil {
		t.Fatalf("ReadScores: %v", err)
	}
	if len(scores) != 1 || scores[0].ID != score.ID {
		t.Fatalf("ReadScores = %+v, want exactly the one appended Score", scores)
	}
}

func TestJudgeCLIValidationExitCodes(t *testing.T) {
	root := t.TempDir()
	badConfig := filepath.Join(root, "bad-judge-config.json")
	if err := os.WriteFile(badConfig, []byte(`{"formatVersion":1,"schema":"och.eval.judge-config"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{"no flags", []string{"judge"}},
		{"attempt without judge config", []string{"judge", "-attempt", root}},
		{"unreadable judge config", []string{"judge", "-attempt", root, "-judge-config", filepath.Join(root, "missing.json")}},
		{"invalid judge config", []string{"judge", "-attempt", root, "-judge-config", badConfig}},
		{"price table without a pinned digest", []string{
			"judge", "-attempt", root, "-judge-config", checkedInJudgeConfigPath(t), "-price-table", badConfig,
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if exitCode := runCLI(context.Background(), testCase.args, &stdout, &stderr); exitCode != exitValidation {
				t.Fatalf("exit = %d, want %d; stderr=%s", exitCode, exitValidation, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("a validation failure still wrote to stdout: %s", stdout.String())
			}
		})
	}
}
