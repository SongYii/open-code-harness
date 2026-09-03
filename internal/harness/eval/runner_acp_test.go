//go:build unix

package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// runnerInputsACP mirrors runnerInputs (runner_test.go) but wires an
// acp_subprocess Executor and an ACPLaunch resolved against the real och
// binary, rather than the in-process one.
func runnerInputsACP(t *testing.T, binary ACPBinaryIdentity, server *echoProvider, artifactRoot string) RunnerInputs {
	t.Helper()
	scenario := runnerScenario("runner-acp-scenario")
	fixtureSource := t.TempDir()
	fixtureDigest, err := DigestFixtureTree(fixtureSource)
	if err != nil {
		t.Fatalf("DigestFixtureTree: %v", err)
	}
	scenario.FixtureDigest = string(fixtureDigest)
	subject := testSubject(t, server.Server)
	subject.ID = "runner-acp-subject"
	executor := validExecutorACPSubprocess()

	scenarioDigest, err := ScenarioDigest(scenario)
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	subjectDigest, err := SubjectDigest(subject)
	if err != nil {
		t.Fatalf("SubjectDigest: %v", err)
	}
	executorDigest, err := ExecutorDigest(executor)
	if err != nil {
		t.Fatalf("ExecutorDigest: %v", err)
	}

	set := EvalSet{
		FormatVersion:   FormatVersion,
		Schema:          SchemaEvalSet,
		ID:              "runner-acp-set",
		Scenarios:       []ScenarioRef{{ID: scenario.ID, Digest: scenarioDigest}},
		Subjects:        []SubjectRef{{ID: subject.ID, Digest: subjectDigest}},
		Executors:       []ExecutorRef{{ID: executor.ID, Digest: executorDigest}},
		RepetitionCount: 1,
		PairingSeed:     "seed-acp-1",
		Limits:          EvalSetLimits{TokenCap: 100_000},
		ArtifactRoot:    artifactRoot,
		Lane:            LaneFixture,
	}

	return RunnerInputs{
		Set:            set,
		Scenarios:      map[ScenarioID]Scenario{scenario.ID: scenario},
		Subjects:       map[SubjectID]Subject{subject.ID: subject},
		Executors:      map[ExecutorID]Executor{executor.ID: executor},
		FixtureSources: map[ScenarioID]string{scenario.ID: fixtureSource},
		ACPLaunch:      ACPLaunchConfig{Binary: binary},
	}
}

// TestRunEvalSetDrivesARealACPCellEndToEnd proves RunEvalSet itself (not
// just RunACPAttempt directly) dispatches an acp_subprocess Cell through a
// real och -acp subprocess once ACPLaunch.Binary is resolved, and
// publishes a real, readable Outcome/manifest for it exactly like an
// in-process Cell.
func TestRunEvalSetDrivesARealACPCellEndToEnd(t *testing.T) {
	ochBin := buildOchBinary(t)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	server := newEchoProvider(t)
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := runnerInputsACP(t, binary, server, artifactRoot)

	results, err := RunEvalSet(context.Background(), inputs)
	if err != nil {
		t.Fatalf("RunEvalSet() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	result := results[0]
	if result.Err != nil {
		t.Fatalf("result.Err = %v, want nil", result.Err)
	}
	if result.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q (message: %s)", result.Outcome.Status, OutcomeCompleted, result.Outcome.Message)
	}
	if result.Manifest.Entries == nil {
		t.Fatal("manifest has no entries")
	}
	if server.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", server.calls.Load())
	}
}
