//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func TestCheckedInSubagentSetRunsBothRoutesAndRegradesOffline(t *testing.T) {
	setPath, err := filepath.Abs(filepath.Join("..", "..", "eval", "sets", "subagent-delegation.json"))
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"run", "-set", setPath, "-artifacts", artifactRoot, "-och-binary", buildOchForPRLane(t),
	}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run exit = %d; stderr=%s", code, stderr.String())
	}
	var report runReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode run report: %v; stdout=%s", err, stdout.String())
	}
	if len(report.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2 (in-process and ACP)", len(report.Attempts))
	}
	seenExecutors := map[eval.ExecutorID]bool{}
	for _, attempt := range report.Attempts {
		if attempt.Status != string(eval.OutcomeCompleted) || attempt.CollectionStatus != string(eval.CollectionComplete) {
			t.Fatalf("attempt = %+v, want completed with complete evidence", attempt)
		}
		seenExecutors[attempt.ExecutorID] = true

		stdout.Reset()
		stderr.Reset()
		code := runCLI(context.Background(), []string{
			"regrade", "-attempt", filepath.Join(artifactRoot, string(attempt.AttemptID)), "-scorer", "subagent-delegation-v1",
		}, &stdout, &stderr)
		if code != exitOK {
			t.Fatalf("regrade %s exit = %d; stderr=%s", attempt.ExecutorID, code, stderr.String())
		}
		var score eval.Score
		if err := json.Unmarshal(stdout.Bytes(), &score); err != nil {
			t.Fatalf("decode score: %v; stdout=%s", err, stdout.String())
		}
		if score.Verdict != eval.ScorePass {
			t.Fatalf("%s verdict = %q, want pass; criteria=%+v", attempt.ExecutorID, score.Verdict, score.Criteria)
		}
	}
	for _, executorID := range []eval.ExecutorID{"subagent-executor", "subagent-executor-acp"} {
		if !seenExecutors[executorID] {
			t.Fatalf("executor %q did not run: %v", executorID, seenExecutors)
		}
	}
}
