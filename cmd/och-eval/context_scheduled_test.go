//go:build unix

// This lane starts real och -acp subprocesses. The design keeps Windows on
// cross-build only — it does not run ACP subprocess recovery until the parent
// design's Job Object contract exists — so this file carries the same unix
// constraint prset_test.go does.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contextScheduledSets is the explicit/scheduled deterministic lane for the
// Context mechanism suite: every paired in-process/ACP set plus the ACP-only
// recovery set.
//
// It is deliberately not part of ordinary PR CI. The PR lane carries exactly
// one representative Context Cell (eval/sets/pr-context.json); running the
// full matrix on every pull request is what the design's tiny-matrix rule
// exists to prevent. That exclusion is enforced by scheduledContextMatrixEnv,
// not by this comment: until 2026-09-04 the only gate here was testing.Short(),
// which no CI job passes, so the whole matrix ran on every pull request — once
// in the `go` job and three more times under `determinism` — while three
// separate documents said it never did.
var contextScheduledSets = []string{
	"context-core-inprocess.json",
	"context-prune-inprocess.json",
	"context-overflow-inprocess.json",
	"context-anchor-inprocess.json",
	"context-core-acp.json",
	"context-prune-acp.json",
	"context-overflow-acp.json",
	"context-anchor-acp.json",
	"context-recovery-acp.json",
}

func contextSetPath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "eval", "sets", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// TestContextScheduledLaneRunsEveryPairedSet drives the scheduled Context
// lane exactly as an operator would: every checked-in set into one shared
// artifact root, the ACP arms through a real freshly built och binary.
//
// It asserts every Attempt reached a definite, non-infrastructure Outcome.
// Criterion verdicts are proven by each Scenario's own regrade, not here —
// this test's job is that the whole matrix still executes end to end on the
// supported Unix environment, which is the thing that silently rots when
// only unit tests are run.
func TestContextScheduledLaneRunsEveryPairedSet(t *testing.T) {
	requireScheduledContextMatrix(t)
	ochBinary := buildOchForPRLane(t)
	artifactRoot := t.TempDir()

	for _, name := range contextScheduledSets {
		t.Run(name, func(t *testing.T) {
			args := []string{"run", "-set", contextSetPath(t, name), "-artifacts", artifactRoot}
			if filepath.Ext(name) == ".json" && len(name) > 8 && name[len(name)-9:] == "-acp.json" {
				args = append(args, "-och-binary", ochBinary)
			}
			var stdout, stderr bytes.Buffer
			if code := runCLI(context.Background(), args, &stdout, &stderr); code != exitOK {
				t.Fatalf("run %s exit = %d; stderr=%s", name, code, stderr.String())
			}
			var report runReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("decode run report: %v", err)
			}
			if len(report.Attempts) == 0 {
				t.Fatalf("%s expanded to no Attempts", name)
			}
			for _, attempt := range report.Attempts {
				if attempt.Error != "" {
					t.Fatalf("%s: %s published nothing durable: %s", name, attempt.ScenarioID, attempt.Error)
				}
				if attempt.Status != "completed" {
					t.Fatalf("%s: %s reached status %q, want completed", name, attempt.ScenarioID, attempt.Status)
				}
				if attempt.CollectionStatus != "complete" {
					t.Fatalf("%s: %s collection status %q, want complete",
						name, attempt.ScenarioID, attempt.CollectionStatus)
				}
			}
		})
	}
}

// TestContextScheduledLaneKeepsTheScanRegressionGated pins the design's
// section 12 requirement that the scheduled lane also runs
// TestPrepareContextResumesScanFromCheckpointRatherThanStreamStart as a hard
// structural regression.
//
// That fact is not observable from an artifact-only verifier: proving the
// steady-state scan resumes from the checkpoint rather than the stream start
// would need store instrumentation inside eval, which the architecture
// forbids. So the guarantee is carried by a package test, and this guard
// makes its absence a failure here rather than a silent gap — someone
// deleting or renaming it would otherwise quietly remove the only proof that
// re-reading pre-checkpoint history has not come back.
func TestContextScheduledLaneKeepsTheScanRegressionGated(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	source := readRepoFile(t, filepath.Join(root,
		"internal/harness/application/context_orchestrator_test.go"))
	const regression = "func TestPrepareContextResumesScanFromCheckpointRatherThanStreamStart("
	if !strings.Contains(source, regression) {
		t.Fatalf("the scheduled lane's required scan regression is gone: %s", regression)
	}

	// The benchmarks the design keeps as performance evidence alongside it.
	bench := readRepoFile(t, filepath.Join(root, "internal/harness/contextengine/bench_test.go"))
	for _, name := range []string{"func BenchmarkScan(", "func BenchmarkScanFromCheckpoint("} {
		if !strings.Contains(bench, name) {
			t.Fatalf("the scheduled lane's required benchmark is gone: %s", name)
		}
	}
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
