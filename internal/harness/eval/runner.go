package eval

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// RunnerInputs bundles everything RunEvalSet needs beyond the EvalSet
// itself: the full documents set's references name, keyed by ID (matching
// ExpandAttempts' own parameters), and each referenced Scenario's fixture
// source directory on disk. RunEvalSet verifies every fixture tree against
// its frozen Scenario.FixtureDigest before creating any Attempt.
type RunnerInputs struct {
	Set            EvalSet
	Scenarios      map[ScenarioID]Scenario
	Subjects       map[SubjectID]Subject
	Executors      map[ExecutorID]Executor
	FixtureSources map[ScenarioID]string

	// ProviderEndpointOverrides maps a frozen fixture Subject identity to
	// this invocation's ephemeral HTTP loopback endpoint. It is an execution
	// fact: ExpandAttempts and Attempt digests always use Subjects above.
	ProviderEndpointOverrides map[SubjectID]string

	// ArtifactRootOverride is this invocation's publication directory. The
	// frozen EvalSet retains its declared artifactRoot for identity; absolute
	// Attempt paths record where this invocation actually wrote artifacts.
	ArtifactRootOverride string
}

// AttemptRunResult is one Cell repetition's outcome (design §9). Err is
// non-nil only when nothing durable was published for this Attempt at all
// (e.g. fixture copy or Attempt publication itself failed) — once
// RunAttempt has run at least one action, its own facts always reach
// Outcome/Manifest through CollectEvidence, matching RunAttempt's own
// "a Go error must not prevent the runner from publishing them" contract.
type AttemptRunResult struct {
	Cell            Cell
	RepetitionIndex int
	AttemptID       AttemptID
	Outcome         Outcome
	Manifest        EvidenceManifest
	Err             error
}

// RunEvalSet drives every expanded Cell repetition of inputs.Set in design
// §9's fixed order, sequentially (design §19's ConcurrentAttempts > 1 is
// not implemented by this slice — every Attempt runs to completion before
// the next starts; see limits.go's own doc comment for what else design
// §19 asks for that this Runner does not yet enforce).
//
// Every Cell is validated — matrix expansion's own capability pairing,
// this Runner's in-process-only executor-kind check (design §26: Stage A
// accepts only in-process executors), the artifact root's absolute-path
// and fixture-non-nesting requirements, and that every referenced
// Scenario has a fixture source — before any Attempt is created (design
// §8's "Do not start work for any Cell until whole-set validation
// succeeds"). One Cell repetition's own failure never aborts the rest of
// the batch; RunEvalSet keeps going and reports that repetition's result
// alongside every other's.
func RunEvalSet(ctx context.Context, inputs RunnerInputs) ([]AttemptRunResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("eval: run eval set: context is required")
	}
	cellAttempts, err := ExpandAttempts(inputs.Set, inputs.Scenarios, inputs.Subjects, inputs.Executors)
	if err != nil {
		return nil, fmt.Errorf("eval: run eval set: %w", err)
	}
	executionSubjects, err := resolveExecutionSubjects(inputs.Subjects, inputs.ProviderEndpointOverrides)
	if err != nil {
		return nil, fmt.Errorf("eval: run eval set: %w", err)
	}
	artifactRoot := inputs.Set.ArtifactRoot
	if inputs.ArtifactRootOverride != "" {
		artifactRoot = inputs.ArtifactRootOverride
	}
	if !filepath.IsAbs(artifactRoot) {
		return nil, fmt.Errorf("eval: run eval set: artifactRoot must be an absolute path")
	}
	for _, ref := range inputs.Set.Executors {
		executor := inputs.Executors[ref.ID]
		if executor.Kind != ExecutorInProcess {
			return nil, fmt.Errorf("eval: run eval set: executor %q: only %q is supported by this Runner, got %q",
				executor.ID, ExecutorInProcess, executor.Kind)
		}
	}
	for _, ref := range inputs.Set.Scenarios {
		scenario := inputs.Scenarios[ref.ID]
		source, ok := inputs.FixtureSources[scenario.ID]
		if !ok {
			return nil, fmt.Errorf("eval: run eval set: no fixture source directory provided for scenario %q", scenario.ID)
		}
		if err := RefuseArtifactRootWithinFixture(artifactRoot, source); err != nil {
			return nil, fmt.Errorf("eval: run eval set: scenario %q: %w", scenario.ID, err)
		}
		fixtureDigest, err := DigestFixtureTree(source)
		if err != nil {
			return nil, fmt.Errorf("eval: run eval set: scenario %q: %w", scenario.ID, err)
		}
		if fixtureDigest != Digest(scenario.FixtureDigest) {
			return nil, fmt.Errorf("eval: run eval set: scenario %q: fixtureDigest changed: got %q, frozen %q",
				scenario.ID, fixtureDigest, scenario.FixtureDigest)
		}
	}

	executionLimits := resolveAttemptExecutionLimits(inputs.Set.Limits)
	results := make([]AttemptRunResult, 0, len(cellAttempts))
	for _, cellAttempt := range cellAttempts {
		results = append(results, runOneAttempt(ctx, inputs, executionSubjects, artifactRoot, cellAttempt, executionLimits))
	}
	return results, nil
}

func runOneAttempt(ctx context.Context, inputs RunnerInputs, executionSubjects map[SubjectID]Subject, artifactRoot string, cellAttempt CellAttempt, executionLimits AttemptExecutionLimits) AttemptRunResult {
	result := AttemptRunResult{Cell: cellAttempt.Cell, RepetitionIndex: cellAttempt.RepetitionIndex}

	scenario := inputs.Scenarios[cellAttempt.Cell.ScenarioID]
	subject := inputs.Subjects[cellAttempt.Cell.SubjectID]
	executionSubject := executionSubjects[cellAttempt.Cell.SubjectID]
	executor := inputs.Executors[cellAttempt.Cell.ExecutorID]

	attemptID, err := NewAttemptID()
	if err != nil {
		result.Err = fmt.Errorf("eval: run attempt: generate attempt id: %w", err)
		return result
	}
	result.AttemptID = attemptID

	directories, err := NewAttemptRoot(artifactRoot, attemptID)
	if err != nil {
		result.Err = fmt.Errorf("eval: run attempt: %w", err)
		return result
	}

	fixtureLimits, err := ResolveFixtureCopyLimits(inputs.Set.Limits, scenario.FixtureCopyPolicy)
	if err != nil {
		result.Err = fmt.Errorf("eval: run attempt: %w", err)
		return result
	}
	if _, err := CopyFixture(inputs.FixtureSources[scenario.ID], directories.Workspace, fixtureLimits); err != nil {
		result.Err = fmt.Errorf("eval: run attempt: copy fixture: %w", err)
		return result
	}

	copiedDigest, err := DigestFixtureTree(directories.Workspace)
	if err != nil {
		result.Err = fmt.Errorf("eval: run attempt: digest copied fixture: %w", err)
		return result
	}
	if copiedDigest != Digest(scenario.FixtureDigest) {
		result.Err = fmt.Errorf("eval: run attempt: copied fixtureDigest %q disagrees with frozen %q", copiedDigest, scenario.FixtureDigest)
		return result
	}
	attempt, err := buildAttemptDocument(inputs.Set.ID, cellAttempt, attemptID, directories, scenario, subject, executor)
	if err != nil {
		result.Err = fmt.Errorf("eval: run attempt: %w", err)
		return result
	}
	if err := PublishAttempt(directories.Root, attempt); err != nil {
		result.Err = fmt.Errorf("eval: run attempt: %w", err)
		return result
	}

	attemptCtx := ctx
	if executionLimits.WallTime > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, executionLimits.WallTime)
		defer cancel()
	}

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunAttempt(attemptCtx, attemptID, executionSubject, directories, scenario, matcher)
	if err != nil {
		// RunAttempt returns a non-nil error only when nothing durable
		// happened yet (its own doc comment): no Outcome exists to collect
		// evidence for, so there is nothing more this Attempt can publish.
		result.Err = fmt.Errorf("eval: run attempt: %w", err)
		return result
	}

	documents := EvidenceDocuments{
		Scenario: scenario, Subject: subject, Executor: executor, Attempt: attempt,
	}
	outcome, manifest, err := CollectEvidence(attemptCtx, directories, execution, execution.Outcome, documents, executionLimits.CollectionLimits)
	if err != nil {
		result.Err = fmt.Errorf("eval: run attempt: collect evidence: %w", err)
		return result
	}
	result.Outcome = outcome
	result.Manifest = manifest
	return result
}

func buildAttemptDocument(evalSetID EvalSetID, cellAttempt CellAttempt, attemptID AttemptID, directories AttemptRootDirectories, scenario Scenario, subject Subject, executor Executor) (Attempt, error) {
	scenarioDigest, err := ScenarioDigest(scenario)
	if err != nil {
		return Attempt{}, fmt.Errorf("digest scenario: %w", err)
	}
	subjectDigest, err := SubjectDigest(subject)
	if err != nil {
		return Attempt{}, fmt.Errorf("digest subject: %w", err)
	}
	executorDigest, err := ExecutorDigest(executor)
	if err != nil {
		return Attempt{}, fmt.Errorf("digest executor: %w", err)
	}
	return Attempt{
		FormatVersion:   FormatVersion,
		Schema:          SchemaAttempt,
		ID:              attemptID,
		EvalSetID:       evalSetID,
		ScenarioID:      scenario.ID,
		ScenarioDigest:  scenarioDigest,
		SubjectID:       subject.ID,
		SubjectDigest:   subjectDigest,
		ExecutorID:      executor.ID,
		ExecutorDigest:  executorDigest,
		RepetitionIndex: cellAttempt.RepetitionIndex,
		Paths: AttemptPaths{
			Root: directories.Root, Workspace: directories.Workspace, Database: directories.Database,
			Audit: directories.Audit, Process: directories.Process, Log: directories.Log, Evidence: directories.Evidence,
		},
		// The first launch's runtime ID (design §16), computed with the
		// exact same launchRuntimeID(attemptID, 0) RunAttempt itself uses
		// for its own initial Assembly, so this document's RuntimeID can
		// never drift from what actually ran.
		RuntimeID:   launchRuntimeID(attemptID, 0),
		PublishedAt: time.Now().UTC(),
	}, nil
}
