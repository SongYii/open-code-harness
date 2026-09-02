package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// runnerScenario is a minimal, real Scenario a Runner test can execute
// end-to-end: one prompt, one collect of a workspace path the prompt
// action's tool call writes (when the caller wants a real artifact) or
// leaves absent.
func runnerScenario(id ScenarioID) Scenario {
	scenario := validScenario()
	scenario.ID = id
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "hello"),
	}
	scenario.ApprovalScript = nil
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit"}
	scenario.OptionalEvidenceRoles = nil
	scenario.FixtureCopyPolicy = FixtureCopyPolicy{MaxFiles: 10, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20}
	return scenario
}

func runnerInputs(t *testing.T, server *echoProvider, artifactRoot string) RunnerInputs {
	t.Helper()
	scenario := runnerScenario("runner-scenario")
	subject := testSubject(t, server.Server)
	subject.ID = "runner-subject"
	executor := validExecutorInProcess()

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
		ID:              "runner-set",
		Scenarios:       []ScenarioRef{{ID: scenario.ID, Digest: scenarioDigest}},
		Subjects:        []SubjectRef{{ID: subject.ID, Digest: subjectDigest}},
		Executors:       []ExecutorRef{{ID: executor.ID, Digest: executorDigest}},
		RepetitionCount: 1,
		PairingSeed:     "seed-1",
		Limits:          EvalSetLimits{TokenCap: 100_000},
		ArtifactRoot:    artifactRoot,
		Lane:            LaneFixture,
	}

	fixtureSource := t.TempDir()
	return RunnerInputs{
		Set:            set,
		Scenarios:      map[ScenarioID]Scenario{scenario.ID: scenario},
		Subjects:       map[SubjectID]Subject{subject.ID: subject},
		Executors:      map[ExecutorID]Executor{executor.ID: executor},
		FixtureSources: map[ScenarioID]string{scenario.ID: fixtureSource},
	}
}

func TestRunEvalSetHappyPath(t *testing.T) {
	server := newEchoProvider(t)
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := runnerInputs(t, server, artifactRoot)

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
	if result.Outcome.CollectionStatus != CollectionComplete {
		t.Fatalf("CollectionStatus = %q, want %q", result.Outcome.CollectionStatus, CollectionComplete)
	}
	if len(result.Manifest.Entries) == 0 {
		t.Fatal("Manifest has no entries")
	}
	if result.AttemptID == "" {
		t.Fatal("AttemptID is empty")
	}

	// The published documents live under a fresh directory named by the
	// generated AttemptID, directly beneath ArtifactRoot.
	attemptDirectory := filepath.Join(artifactRoot, string(result.AttemptID))
	if _, err := ReadAttempt(attemptDirectory); err != nil {
		t.Fatalf("ReadAttempt: %v", err)
	}
	if _, err := ReadEvidenceManifest(attemptDirectory); err != nil {
		t.Fatalf("ReadEvidenceManifest: %v", err)
	}
}

func TestRunEvalSetRejectsNonInProcessExecutorBeforeAnyWorkStarts(t *testing.T) {
	server := newEchoProvider(t)
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := runnerInputs(t, server, artifactRoot)

	acpExecutor := validExecutorACPSubprocess()
	acpDigest, err := ExecutorDigest(acpExecutor)
	if err != nil {
		t.Fatalf("ExecutorDigest: %v", err)
	}
	inputs.Set.Executors = []ExecutorRef{{ID: acpExecutor.ID, Digest: acpDigest}}
	inputs.Executors = map[ExecutorID]Executor{acpExecutor.ID: acpExecutor}

	_, err = RunEvalSet(context.Background(), inputs)
	if err == nil {
		t.Fatal("RunEvalSet() error = nil, want a refusal for an acp_subprocess executor")
	}
	entries, readErr := os.ReadDir(artifactRoot)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("artifactRoot has %d entries, want 0: no Attempt should have been created before whole-set validation failed", len(entries))
	}
}

func TestRunEvalSetOneFailingCellDoesNotAbortTheRest(t *testing.T) {
	server := newEchoProvider(t)
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := runnerInputs(t, server, artifactRoot)

	// A second Subject whose endpoint refuses every connection, so its
	// Cell fails during composition.Open/RunAttempt rather than during
	// validation.
	brokenSubject := testSubject(t, server.Server)
	brokenSubject.ID = "runner-subject-broken"
	brokenSubject.Provider.NormalizedEndpoint = "https://127.0.0.1:1/v1"
	brokenDigest, err := SubjectDigest(brokenSubject)
	if err != nil {
		t.Fatalf("SubjectDigest: %v", err)
	}
	inputs.Set.Subjects = append(inputs.Set.Subjects, SubjectRef{ID: brokenSubject.ID, Digest: brokenDigest})
	inputs.Subjects[brokenSubject.ID] = brokenSubject

	results, err := RunEvalSet(context.Background(), inputs)
	if err != nil {
		t.Fatalf("RunEvalSet() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	var sawSuccess, sawFailure bool
	for _, result := range results {
		switch result.Cell.SubjectID {
		case "runner-subject":
			sawSuccess = result.Err == nil && result.Outcome.Status == OutcomeCompleted
		case "runner-subject-broken":
			// The broken endpoint fails inside a Turn, not before
			// composition.Open/CreateSession -- RunAttempt's own contract
			// converts that into a durably published, non-completed
			// Outcome rather than a Go error (a Go error only escapes
			// RunAttempt when nothing durable happened yet). The
			// interesting property here is that this Cell's poor result
			// was still published and did not abort the batch.
			sawFailure = result.Err == nil && result.Outcome.Status != OutcomeCompleted
		}
	}
	if !sawSuccess {
		t.Fatal("the working Subject's Cell did not succeed")
	}
	if !sawFailure {
		t.Fatal("the broken Subject's Cell did not record a non-completed, but still published, Outcome")
	}
}

func TestClassifyAttemptDirectory(t *testing.T) {
	directories, execution, scenario := runHappyAttempt(t)

	attempt, err := buildAttemptDocument("set-1",
		CellAttempt{Cell: Cell{ScenarioID: scenario.ID, SubjectID: "subject-1", ExecutorID: "executor-1"}},
		testAttemptID(t), directories, scenario, validSubject(), validExecutorInProcess())
	if err != nil {
		t.Fatalf("buildAttemptDocument: %v", err)
	}
	if err := PublishAttempt(directories.Root, attempt); err != nil {
		t.Fatalf("PublishAttempt: %v", err)
	}

	state, err := ClassifyAttemptDirectory(directories.Root)
	if err != nil {
		t.Fatalf("ClassifyAttemptDirectory (attempt only): %v", err)
	}
	if state != RecoveryInspectRequired {
		t.Fatalf("state = %q, want %q", state, RecoveryInspectRequired)
	}

	outcome := execution.Outcome
	outcome.CollectionStatus = CollectionNotStarted
	if err := PublishOutcome(directories.Root, outcome); err != nil {
		t.Fatalf("PublishOutcome: %v", err)
	}
	state, err = ClassifyAttemptDirectory(directories.Root)
	if err != nil {
		t.Fatalf("ClassifyAttemptDirectory (attempt+outcome): %v", err)
	}
	if state != RecoveryResumeCollectionOnly {
		t.Fatalf("state = %q, want %q", state, RecoveryResumeCollectionOnly)
	}

	if _, _, err := ResumeCollection(context.Background(), directories, execution, scenario, CollectionLimits{}); err != nil {
		t.Fatalf("ResumeCollection: %v", err)
	}
	state, err = ClassifyAttemptDirectory(directories.Root)
	if err != nil {
		t.Fatalf("ClassifyAttemptDirectory (terminal): %v", err)
	}
	if state != RecoveryTerminal {
		t.Fatalf("state = %q, want %q", state, RecoveryTerminal)
	}
}

func TestClassifyAttemptDirectoryUncommittedWhenEmpty(t *testing.T) {
	state, err := ClassifyAttemptDirectory(t.TempDir())
	if err != nil {
		t.Fatalf("ClassifyAttemptDirectory: %v", err)
	}
	if state != RecoveryUncommitted {
		t.Fatalf("state = %q, want %q", state, RecoveryUncommitted)
	}
}

func TestResumeCollectionNeverMutatesTheExistingOutcome(t *testing.T) {
	directories, execution, scenario := runHappyAttempt(t)

	outcome := execution.Outcome
	outcome.CollectionStatus = CollectionNotStarted
	if err := PublishOutcome(directories.Root, outcome); err != nil {
		t.Fatalf("PublishOutcome: %v", err)
	}
	publishedDigest, err := OutcomeDigest(outcome)
	if err != nil {
		t.Fatalf("OutcomeDigest: %v", err)
	}

	resumedOutcome, manifest, err := ResumeCollection(context.Background(), directories, execution, scenario, CollectionLimits{})
	if err != nil {
		t.Fatalf("ResumeCollection: %v", err)
	}
	resumedDigest, err := OutcomeDigest(resumedOutcome)
	if err != nil {
		t.Fatalf("OutcomeDigest(resumed): %v", err)
	}
	if resumedDigest != publishedDigest {
		t.Fatal("ResumeCollection returned a different Outcome than the one already published")
	}
	if manifest.OutcomeDigest != publishedDigest {
		t.Fatalf("manifest.OutcomeDigest = %q, want %q", manifest.OutcomeDigest, publishedDigest)
	}

	readBack, err := ReadOutcome(directories.Root)
	if err != nil {
		t.Fatalf("ReadOutcome: %v", err)
	}
	readDigest, err := OutcomeDigest(readBack)
	if err != nil {
		t.Fatalf("OutcomeDigest(readBack): %v", err)
	}
	if readDigest != publishedDigest {
		t.Fatal("the on-disk Outcome changed after ResumeCollection")
	}
}
