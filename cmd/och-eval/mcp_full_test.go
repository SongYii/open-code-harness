//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func TestCheckedInMCPSetProvesApprovalAndRedaction(t *testing.T) {
	if os.Getenv("OCH_EVAL_EXPLICIT_MCP_SUITE") != "1" {
		t.Skip("set OCH_EVAL_EXPLICIT_MCP_SUITE=1 to run the explicit MCP process suite")
	}
	setPath, err := filepath.Abs(filepath.Join("..", "..", "eval", "sets", "mcp-deterministic.json"))
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), []string{"run", "-set", setPath, "-artifacts", artifactRoot}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run exit = %d; stderr=%s", code, stderr.String())
	}
	var report runReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(report.Attempts))
	}
	scorers := map[eval.ScenarioID]string{
		"mcp-approval-denied":  "mcp-approval-denied-scorer-v1",
		"mcp-result-redaction": "mcp-result-redaction-scorer-v1",
	}
	for _, attempt := range report.Attempts {
		scorer, ok := scorers[attempt.ScenarioID]
		if !ok {
			t.Fatalf("unexpected scenario %q", attempt.ScenarioID)
		}
		stdout.Reset()
		stderr.Reset()
		code := runCLI(context.Background(), []string{
			"regrade", "-attempt", filepath.Join(artifactRoot, string(attempt.AttemptID)), "-scorer", scorer,
		}, &stdout, &stderr)
		if code != exitOK {
			t.Fatalf("regrade %s exit = %d; stderr=%s", attempt.ScenarioID, code, stderr.String())
		}
		var score eval.Score
		if err := json.Unmarshal(stdout.Bytes(), &score); err != nil {
			t.Fatal(err)
		}
		if score.Verdict != eval.ScorePass {
			t.Fatalf("%s verdict = %q, want pass; criteria=%+v", attempt.ScenarioID, score.Verdict, score.Criteria)
		}
	}
}
