//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// prLaneSetPaths names every checked-in EvalSet document that together
// make up design §23's own ordinary-PR lane: at most four Cells, exactly
// one paired parity Scenario through both executors (two Cells) plus one
// in-process tool/approval/failure Cell and one in-process Context
// compaction Cell. eval/sets/matrix.go's own ExpandCells is a flat
// Cartesian product of everything one EvalSet document declares, with no
// way to pair a Subject/Executor selectively within a single document —
// listing both the baseline and candidate Subject/Executor pairs in one
// EvalSet would cross-multiply into unwanted Cells (e.g. the in-process
// tool/approval Scenario paired with the acp_subprocess Executor), not
// the exact four this lane needs. Three separate, minimal-cardinality
// EvalSet documents sharing one artifact root is how "exactly four
// Cells, no more" is actually achievable under that model; the plan's
// own eval/sets/pr.json name is honored by pr-tool-and-compaction.json,
// the file this lane's own -set flag (and this lane's own report) is
// invoked against.
var prLaneSetPaths = []string{
	"pr-tool-and-compaction.json",
	"pr-context.json",
	"pr-parity-baseline.json",
	"pr-parity-candidate.json",
}

func prLaneSetPath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "eval", "sets", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPRLaneExpandsToExactlyFourFixtureCells is design §23's own guard:
// ordinary PR expansion (every EvalSet this lane's own CI step runs) must
// total exactly four Cells, and none may be a live Cell (live/model-judge
// quality Cells never enter ordinary PR CI — this codebase has not yet
// built a live or model-judge scorer/lane beyond the fixture lane's own
// eval.LaneLive/eval.LaneFixture distinction, so this guard checks the
// one such fact that exists today: every PR-lane EvalSet must declare
// lane "fixture").
func TestPRLaneExpandsToExactlyFourFixtureCells(t *testing.T) {
	total := 0
	for _, name := range prLaneSetPaths {
		tree, err := loadDocumentTree(prLaneSetPath(t, name))
		if err != nil {
			t.Fatalf("loadDocumentTree(%s): %v", name, err)
		}
		if tree.Set.Lane != eval.LaneFixture {
			t.Fatalf("%s: lane = %q, want %q: a live Cell must never enter ordinary PR CI", name, tree.Set.Lane, eval.LaneFixture)
		}
		cells := tree.Set.ExpandCells()
		total += len(cells)
	}
	if total != 4 {
		t.Fatalf("total PR-lane Cells = %d, want exactly 4", total)
	}
}

var (
	prLaneOchOnce sync.Once
	prLaneOchPath string
	prLaneOchErr  error
)

func buildOchForPRLane(t *testing.T) string {
	t.Helper()
	prLaneOchOnce.Do(func() {
		wd, err := os.Getwd()
		if err != nil {
			prLaneOchErr = err
			return
		}
		repoRoot := filepath.Join(wd, "..", "..")
		dir, err := os.MkdirTemp("", "och-pr-lane-build")
		if err != nil {
			prLaneOchErr = err
			return
		}
		path := filepath.Join(dir, "och")
		build := exec.Command("go", "build", "-o", path, "./cmd/och")
		build.Dir = repoRoot
		if out, err := build.CombinedOutput(); err != nil {
			prLaneOchErr = err
			t.Logf("go build ./cmd/och: %v\n%s", err, out)
			return
		}
		prLaneOchPath = path
	})
	if prLaneOchErr != nil {
		t.Fatalf("build och: %v", prLaneOchErr)
	}
	return prLaneOchPath
}

// TestPRLaneRunAndReportEndToEnd drives design §23's own PR lane exactly
// as CI does: `run` every checked-in PR-lane EvalSet into one shared
// artifact root (the candidate arm through a real, freshly built och
// binary), then `report` against that root and confirm the parity pair
// it finds carries no mismatches and the report's own exit code is
// exitOK — a lifecycle-only difference between the two executors (every
// distinct process, lease, ID, and timestamp) must never surface as a
// gate failure.
func TestPRLaneRunAndReportEndToEnd(t *testing.T) {
	ochBinary := buildOchForPRLane(t)
	artifactRoot := t.TempDir()

	for _, args := range [][]string{
		{"run", "-set", prLaneSetPath(t, "pr-tool-and-compaction.json"), "-artifacts", artifactRoot},
		{"run", "-set", prLaneSetPath(t, "pr-context.json"), "-artifacts", artifactRoot},
		{"run", "-set", prLaneSetPath(t, "pr-parity-baseline.json"), "-artifacts", artifactRoot},
		{"run", "-set", prLaneSetPath(t, "pr-parity-candidate.json"), "-artifacts", artifactRoot, "-och-binary", ochBinary},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(context.Background(), args, &stdout, &stderr); code != exitOK {
			t.Fatalf("run %v exit = %d; stderr=%s", args, code, stderr.String())
		}
	}

	var reportStdout, reportStderr bytes.Buffer
	reportCode := runCLI(context.Background(), []string{
		"report", "-set", prLaneSetPath(t, "pr-tool-and-compaction.json"), "-artifacts", artifactRoot,
	}, &reportStdout, &reportStderr)
	if reportCode != exitOK {
		t.Fatalf("report exit = %d, want exitOK; stderr=%s\nstdout=%s", reportCode, reportStderr.String(), reportStdout.String())
	}

	var report evaluationReport
	if err := json.Unmarshal(reportStdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if len(report.Attempts) != 4 {
		t.Fatalf("report has %d attempts, want 4", len(report.Attempts))
	}
	for _, attempt := range report.Attempts {
		if attempt.Status != string(eval.OutcomeCompleted) {
			t.Fatalf("attempt %+v: status = %q, want %q", attempt, attempt.Status, eval.OutcomeCompleted)
		}
	}
	if len(report.Parity) != 1 {
		t.Fatalf("report.Parity has %d entries, want exactly 1", len(report.Parity))
	}
	if len(report.Parity[0].Mismatches) != 0 {
		t.Fatalf("parity mismatches = %+v, want none: a lifecycle-only difference must not gate the PR lane", report.Parity[0].Mismatches)
	}
}
