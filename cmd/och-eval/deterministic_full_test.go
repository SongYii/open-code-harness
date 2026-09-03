package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func deterministicFullSetPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "eval", "sets", "deterministic-full.json"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCheckedInDeterministicFullSetProvesToolWorkspaceSuite is design
// §25.2's own tool/workspace suite, run for real: read success, an exec
// call whose secret-shaped stdout is actually redacted before it ever
// reaches committed evidence, a read of a workspace-relative path that
// does not exist, and a read of a path outside the workspace root
// (design's own containment guarantee). Each Attempt's own dedicated
// scorer proves the SPECIFIC expected behavioral evidence was observed,
// not merely that the Attempt completed.
//
// This is the explicit/scheduled full-matrix set (design §23: "the
// complete deterministic matrix runs only by explicit command"), never
// part of ordinary PR CI -- unlike eval/sets/pr-*.json, nothing in this
// package invokes it from a PR-gating test.
func TestCheckedInDeterministicFullSetProvesToolWorkspaceSuite(t *testing.T) {
	artifactRoot := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"run", "-set", deterministicFullSetPath(t), "-artifacts", artifactRoot,
	}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run exit = %d; stderr=%s", code, stderr.String())
	}
	var report runReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Attempts) != 4 {
		t.Fatalf("attempts = %d, want 4 (read success, exec redaction, read missing, containment)", len(report.Attempts))
	}

	scorers := map[eval.ScenarioID]string{
		"tool-read-success":   "tool-read-success-v1",
		"tool-exec-redaction": "tool-exec-redaction-v1",
		"tool-read-missing":   "tool-read-missing-v1",
		"tool-containment":    "tool-containment-v1",
	}
	for _, attempt := range report.Attempts {
		scorer, ok := scorers[attempt.ScenarioID]
		if !ok {
			t.Fatalf("unexpected scenario %q", attempt.ScenarioID)
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
