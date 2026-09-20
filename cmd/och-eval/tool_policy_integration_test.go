//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func toolPolicySetPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "eval", "sets", "tool-policy-denial-acp.json"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func buildDenyToolsLauncher(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "deny-tools-och")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, ".")
	command.Dir = filepath.Join(repoRoot, "examples", "deny-tools")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build examples/deny-tools: %v\n%s", err, output)
	}
	return binary
}

// TestToolPolicyLauncherRunsAndRegradesFromCommittedEvidence catches a
// vertical integration break: och-eval must be able to launch a separately
// compiled policy implementation, observe its denial through canonical audit,
// and later prove that denial from committed evidence without launching the
// Subject again.
func TestToolPolicyLauncherRunsAndRegradesFromCommittedEvidence(t *testing.T) {
	launcher := buildDenyToolsLauncher(t)
	artifactRoot := t.TempDir()

	var runStdout, runStderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"run",
		"-set", toolPolicySetPath(t),
		"-artifacts", artifactRoot,
		"-och-binary", launcher,
	}, &runStdout, &runStderr); code != exitOK {
		t.Fatalf("run exit = %d, want %d; stderr=%s", code, exitOK, runStderr.String())
	}

	var report runReport
	if err := json.Unmarshal(runStdout.Bytes(), &report); err != nil {
		t.Fatalf("decode run report: %v; stdout=%s", err, runStdout.String())
	}
	if len(report.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1: %#v", len(report.Attempts), report.Attempts)
	}
	attempt := report.Attempts[0]
	if attempt.Status != string(eval.OutcomeCompleted) || attempt.CollectionStatus != string(eval.CollectionComplete) {
		t.Fatalf("attempt = %+v, want completed with complete evidence", attempt)
	}

	attemptRoot := filepath.Join(artifactRoot, string(attempt.AttemptID))
	published, err := eval.ReadAttempt(attemptRoot)
	if err != nil {
		t.Fatalf("ReadAttempt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(published.Paths.Workspace, "policy-should-not-run.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied exec created its marker: %v", err)
	}

	var regradeStdout, regradeStderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"regrade",
		"-attempt", attemptRoot,
		"-scorer", "tool-policy-denial-v1",
	}, &regradeStdout, &regradeStderr); code != exitOK {
		t.Fatalf("regrade exit = %d, want %d; stderr=%s", code, exitOK, regradeStderr.String())
	}
	var score eval.Score
	if err := json.Unmarshal(regradeStdout.Bytes(), &score); err != nil {
		t.Fatalf("decode score: %v; stdout=%s", err, regradeStdout.String())
	}
	if score.Verdict != eval.ScorePass {
		t.Fatalf("verdict = %q, want %q; criteria=%+v", score.Verdict, eval.ScorePass, score.Criteria)
	}
}
