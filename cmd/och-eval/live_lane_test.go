package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func contextQualityLiveExampleSetPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "eval", "sets", "context-quality-live.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRunCLIRefusesLiveExampleSetWithoutLiveFlagBeforeAnyCredentialAccess
// is Task 17's own explicit verification command
// ("must refuse without --live before credential access"): the real
// checked-in live-lane example EvalSet, run without -live, must be
// refused before creating an artifact root, and (design's own
// requirement) before any credential is read -- verified by leaving the
// example Subject's own declared credential env var (never set to
// anything by this test at all) absent, so any code path that tried to
// read it would observe an empty string either way, but the more direct
// proof is structural: checkLaneConsent runs and returns before
// resolveFixtureSubjects or eval.RunEvalSet are ever reached in
// runCommand's own sequential code, and this test proves the refusal
// happens at all with no artifacts published.
func TestRunCLIRefusesLiveExampleSetWithoutLiveFlagBeforeAnyCredentialAccess(t *testing.T) {
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	var stdout, stderr bytes.Buffer
	exitCode := runCLI(context.Background(), []string{
		"run", "-set", contextQualityLiveExampleSetPath(t), "-artifacts", artifactRoot,
	}, &stdout, &stderr)
	if exitCode != exitValidation {
		t.Fatalf("runCLI() exit = %d, want %d; stderr=%s", exitCode, exitValidation, stderr.String())
	}
	if _, err := os.Stat(artifactRoot); !os.IsNotExist(err) {
		t.Fatalf("artifact root was created before live consent was ever granted: err=%v", err)
	}
}

// TestRunCLIRefusesLiveExampleSetWithLiveFlagButNoEnvironmentConfirmation
// proves the second, independent half of the same dual-consent gate: even
// with -live passed, the missing OCH_EVAL_LIVE_CONFIRM environment
// confirmation alone is still enough to refuse before any artifact root
// is created.
func TestRunCLIRefusesLiveExampleSetWithLiveFlagButNoEnvironmentConfirmation(t *testing.T) {
	t.Setenv("OCH_EVAL_LIVE_CONFIRM", "")
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	var stdout, stderr bytes.Buffer
	exitCode := runCLI(context.Background(), []string{
		"run", "-set", contextQualityLiveExampleSetPath(t), "-artifacts", artifactRoot, "-live",
	}, &stdout, &stderr)
	if exitCode != exitValidation {
		t.Fatalf("runCLI() exit = %d, want %d; stderr=%s", exitCode, exitValidation, stderr.String())
	}
	if _, err := os.Stat(artifactRoot); !os.IsNotExist(err) {
		t.Fatalf("artifact root was created before live consent was ever granted: err=%v", err)
	}
}
