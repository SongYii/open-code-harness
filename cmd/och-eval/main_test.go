package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func checkedInSetPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "eval", "sets", "pr-inprocess.json"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// Removing the runtime endpoint override or mutating the Subject in place
// makes this fail at ExpandAttempts with a stale frozen digest.
func TestRunCLIExecutesFixtureSubjectWithoutRepinningDigest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runCLI(context.Background(), []string{
		"run",
		"-set", checkedInSetPath(t),
		"-artifacts", t.TempDir(),
	}, &stdout, &stderr)
	if exitCode != exitOK {
		t.Fatalf("runCLI() exit = %d, want %d; stderr=%s", exitCode, exitOK, stderr.String())
	}

	var report runReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode stdout: %v; stdout=%s", err, stdout.String())
	}
	if len(report.Attempts) == 0 {
		t.Fatalf("report.Attempts is empty, want at least the checked-in smoke-prompt Attempt")
	}
	for _, attempt := range report.Attempts {
		if attempt.Status != string(eval.OutcomeCompleted) {
			t.Fatalf("attempt %+v, want status %q", attempt, eval.OutcomeCompleted)
		}
	}
}

func TestResolveFixtureSubjectsPreservesIdentityAndRestoresCredential(t *testing.T) {
	tree, err := loadDocumentTree(checkedInSetPath(t))
	if err != nil {
		t.Fatal(err)
	}
	subjectID := tree.Set.Subjects[0].ID
	before := tree.Subjects[subjectID]
	beforeDigest, err := eval.SubjectDigest(before)
	if err != nil {
		t.Fatal(err)
	}
	const originalCredential = "preexisting-value"
	t.Setenv(before.Provider.CredentialEnvVar, originalCredential)

	overrides, cleanup, err := resolveFixtureSubjects(tree.Subjects)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.Parse(overrides[subjectID])
	if err != nil {
		cleanup()
		t.Fatalf("parse resolved endpoint: %v", err)
	}
	if endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" {
		cleanup()
		t.Fatalf("resolved endpoint = %q, want HTTP loopback", endpoint.String())
	}
	afterDigest, err := eval.SubjectDigest(tree.Subjects[subjectID])
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if afterDigest != beforeDigest {
		cleanup()
		t.Fatalf("Subject digest changed from %q to %q", beforeDigest, afterDigest)
	}

	cleanup()
	if got := os.Getenv(before.Provider.CredentialEnvVar); got != originalCredential {
		t.Fatalf("credential after cleanup = %q, want restored value", got)
	}
}

func TestResolveFixtureSubjectsRejectsExternalFixtureEndpoint(t *testing.T) {
	tree, err := loadDocumentTree(checkedInSetPath(t))
	if err != nil {
		t.Fatal(err)
	}
	subjectID := tree.Set.Subjects[0].ID
	subject := tree.Subjects[subjectID]
	subject.Provider.NormalizedEndpoint = "https://api.example.com/v1"
	tree.Subjects[subjectID] = subject

	_, cleanup, err := resolveFixtureSubjects(tree.Subjects)
	if cleanup != nil {
		defer cleanup()
	}
	if err == nil {
		t.Fatal("resolveFixtureSubjects() accepted an external endpoint in the fixture lane")
	}
}

func TestCheckLaneConsentRequiresAllLiveConfirmations(t *testing.T) {
	tree, err := loadDocumentTree(checkedInSetPath(t))
	if err != nil {
		t.Fatal(err)
	}
	tree.Set.Lane = eval.LaneLive
	t.Setenv("OCH_EVAL_LIVE_CONFIRM", "")
	if err := checkLaneConsent(tree.Set, true); err == nil {
		t.Fatal("checkLaneConsent() accepted live execution without environment confirmation")
	}
	t.Setenv("OCH_EVAL_LIVE_CONFIRM", "I_UNDERSTAND")
	if err := checkLaneConsent(tree.Set, true); err != nil {
		t.Fatalf("checkLaneConsent(live set, -live) = %v, want nil", err)
	}
}

func TestRunCLIRefusesNestedArtifactRootBeforeCreatingIt(t *testing.T) {
	setPath := copyCheckedInEvalTree(t)
	fixtureRoot := filepath.Join(filepath.Dir(filepath.Dir(setPath)), "scenarios", "smoke-prompt", "fixture")
	artifactRoot := filepath.Join(fixtureRoot, "artifacts")

	var stdout, stderr bytes.Buffer
	exitCode := runCLI(context.Background(), []string{
		"run",
		"-set", setPath,
		"-artifacts", artifactRoot,
	}, &stdout, &stderr)
	if exitCode != exitValidation {
		t.Fatalf("runCLI() exit = %d, want %d; stderr=%s", exitCode, exitValidation, stderr.String())
	}
	if _, err := os.Stat(artifactRoot); !os.IsNotExist(err) {
		t.Fatalf("artifact root was created before containment validation: err=%v", err)
	}
}

func copyCheckedInEvalTree(t *testing.T) string {
	t.Helper()
	sourceRoot := filepath.Dir(filepath.Dir(checkedInSetPath(t)))
	destinationRoot := t.TempDir()
	for _, relative := range []string{
		"sets/pr-inprocess.json",
		"scenarios/smoke-prompt/scenario.json",
		"subjects/smoke-subject.json",
		"executors/smoke-executor.json",
	} {
		data, err := os.ReadFile(filepath.Join(sourceRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(destinationRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(destinationRoot, "scenarios", "smoke-prompt", "fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(destinationRoot, "sets", "pr-inprocess.json")
}

func TestRunRegradeReportWorkflow(t *testing.T) {
	artifactRoot := t.TempDir()
	var runStdout, runStderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"run", "-set", checkedInSetPath(t), "-artifacts", artifactRoot,
	}, &runStdout, &runStderr); code != exitOK {
		t.Fatalf("run exit = %d; stderr=%s", code, runStderr.String())
	}
	var runResult runReport
	if err := json.Unmarshal(runStdout.Bytes(), &runResult); err != nil {
		t.Fatal(err)
	}
	if len(runResult.Attempts) == 0 {
		t.Fatalf("run attempts is empty, want at least the checked-in smoke-prompt Attempt")
	}
	var smokePromptAttemptID eval.AttemptID
	for _, attempt := range runResult.Attempts {
		if attempt.ScenarioID == "smoke-prompt" {
			smokePromptAttemptID = attempt.AttemptID
		}
	}
	if smokePromptAttemptID == "" {
		t.Fatalf("no smoke-prompt Attempt in run report: %#v", runResult.Attempts)
	}
	attemptRoot := filepath.Join(artifactRoot, string(smokePromptAttemptID))

	var regradeStdout, regradeStderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"regrade",
		"-attempt", attemptRoot,
		"-scorer", "baseline-v1",
	}, &regradeStdout, &regradeStderr); code != exitOK {
		t.Fatalf("regrade exit = %d; stderr=%s", code, regradeStderr.String())
	}
	var score eval.Score
	if err := json.Unmarshal(regradeStdout.Bytes(), &score); err != nil {
		t.Fatalf("decode regrade stdout: %v", err)
	}
	if score.Verdict != eval.ScorePass {
		t.Fatalf("regrade verdict = %q, want %q", score.Verdict, eval.ScorePass)
	}

	var reportStdout, reportStderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"report", "-set", checkedInSetPath(t), "-artifacts", artifactRoot,
	}, &reportStdout, &reportStderr); code != exitOK {
		t.Fatalf("report exit = %d; stderr=%s", code, reportStderr.String())
	}
	var report evaluationReport
	if err := json.Unmarshal(reportStdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report stdout: %v", err)
	}
	if len(report.Attempts) != len(runResult.Attempts) {
		t.Fatalf("report has %d attempts, want %d (every published Attempt, regraded or not)", len(report.Attempts), len(runResult.Attempts))
	}
	var scoredCount int
	for _, attempt := range report.Attempts {
		if attempt.AttemptID == smokePromptAttemptID {
			if len(attempt.Scores) != 1 {
				t.Fatalf("regraded smoke-prompt attempt has %d scores, want 1: %#v", len(attempt.Scores), attempt)
			}
			scoredCount++
			continue
		}
		if len(attempt.Scores) != 0 {
			t.Fatalf("attempt %+v was never regraded but has %d scores, want 0", attempt, len(attempt.Scores))
		}
	}
	if scoredCount != 1 {
		t.Fatalf("scoredCount = %d, want 1", scoredCount)
	}
}

func TestCLIValidationExitCodes(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "missing subcommand"},
		{name: "unknown subcommand", args: []string{"unknown"}},
		{name: "run missing flags", args: []string{"run"}},
		{name: "regrade exposes no provider flag", args: []string{"regrade", "-provider-url", "https://api.example.com"}},
		{name: "report missing set", args: []string{"report"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runCLI(context.Background(), test.args, &stdout, &stderr); code != exitValidation {
				t.Fatalf("runCLI(%q) exit = %d, want %d", test.args, code, exitValidation)
			}
			if stdout.Len() != 0 {
				t.Fatalf("validation failure wrote machine stdout: %q", stdout.String())
			}
			if stderr.Len() == 0 {
				t.Fatal("validation failure wrote no diagnostic to stderr")
			}
		})
	}
}

func TestCheckedInSmokeSetProvesToolFailureAndCompaction(t *testing.T) {
	artifactRoot := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"run", "-set", checkedInSetPath(t), "-artifacts", artifactRoot,
	}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run exit = %d; stderr=%s", code, stderr.String())
	}
	var report runReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Attempts) != 3 {
		t.Fatalf("smoke Attempts = %d, want prompt, tool failure, and context compaction", len(report.Attempts))
	}

	scorers := map[eval.ScenarioID]string{
		"smoke-prompt":          "baseline-v1",
		"tool-approval-failure": "tool-approval-failure-v1",
		"context-compaction":    "context-compaction-v1",
	}
	for _, attempt := range report.Attempts {
		scorer, ok := scorers[attempt.ScenarioID]
		if !ok {
			t.Fatalf("unexpected smoke scenario %q", attempt.ScenarioID)
		}
		var regradeStdout, regradeStderr bytes.Buffer
		code := runCLI(context.Background(), []string{
			"regrade",
			"-attempt", filepath.Join(artifactRoot, string(attempt.AttemptID)),
			"-scorer", scorer,
		}, &regradeStdout, &regradeStderr)
		if code != exitOK {
			t.Fatalf("regrade %s exit = %d; stderr=%s", attempt.ScenarioID, code, regradeStderr.String())
		}
		var score eval.Score
		if err := json.Unmarshal(regradeStdout.Bytes(), &score); err != nil {
			t.Fatal(err)
		}
		if score.Verdict != eval.ScorePass {
			t.Fatalf("%s verdict = %q, want pass; criteria=%+v", attempt.ScenarioID, score.Verdict, score.Criteria)
		}
	}
}
