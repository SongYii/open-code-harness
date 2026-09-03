package eval

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// benchmarkEvalSetOfSize builds a real, valid EvalSet plus the
// scenarios/subjects/executors map ExpandAttempts itself requires,
// scaled to exactly cellCount Cells (cellCount distinct Scenarios, one
// shared Subject, one shared Executor — |scenarios|x|subjects|x
// |executors| = cellCount x 1 x 1). No fixture tree is written to disk:
// ExpandAttempts itself never touches the filesystem (RunEvalSet's own,
// separate fixture-verification pass does, deliberately out of scope
// for this benchmark, which measures pure in-memory expansion cost).
func benchmarkEvalSetOfSize(cellCount int) (EvalSet, map[ScenarioID]Scenario, map[SubjectID]Subject, map[ExecutorID]Executor) {
	subject := validSubject()
	executor := validExecutorInProcess()
	subjectDigest, err := SubjectDigest(subject)
	if err != nil {
		panic(err)
	}
	executorDigest, err := ExecutorDigest(executor)
	if err != nil {
		panic(err)
	}

	scenarios := make(map[ScenarioID]Scenario, cellCount)
	scenarioRefs := make([]ScenarioRef, 0, cellCount)
	for i := 0; i < cellCount; i++ {
		scenario := validScenario()
		scenario.ID = ScenarioID("bench-scenario-" + strconv.Itoa(i))
		digest, err := ScenarioDigest(scenario)
		if err != nil {
			panic(err)
		}
		scenarios[scenario.ID] = scenario
		scenarioRefs = append(scenarioRefs, ScenarioRef{ID: scenario.ID, Digest: digest})
	}

	set := EvalSet{
		FormatVersion:   FormatVersion,
		Schema:          SchemaEvalSet,
		ID:              EvalSetID("bench-set-" + strconv.Itoa(cellCount)),
		Scenarios:       scenarioRefs,
		Subjects:        []SubjectRef{{ID: subject.ID, Digest: subjectDigest}},
		Executors:       []ExecutorRef{{ID: executor.ID, Digest: executorDigest}},
		RepetitionCount: 1,
		PairingSeed:     "bench-seed",
		Limits:          EvalSetLimits{TokenCap: 1_000_000, MaxExpandedAttempts: MaxMaxExpandedAttempts},
		ArtifactRoot:    "/bench/artifacts",
		Lane:            LaneFixture,
	}
	return set, scenarios, map[SubjectID]Subject{subject.ID: subject}, map[ExecutorID]Executor{executor.ID: executor}
}

// BenchmarkExpandAttempts measures design §9's own matrix expansion cost
// alone — digest re-verification, capability pairing, and ordering — at
// the plan's own required sample points, with no model call, no
// filesystem fixture I/O, and no subprocess involved at all.
func BenchmarkExpandAttempts(b *testing.B) {
	for _, cellCount := range []int{1, 100, 1000, MaxMaxExpandedAttempts} {
		b.Run(fmt.Sprintf("cells=%d", cellCount), func(b *testing.B) {
			set, scenarios, subjects, executors := benchmarkEvalSetOfSize(cellCount)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				attempts, err := ExpandAttempts(set, scenarios, subjects, executors)
				if err != nil {
					b.Fatalf("ExpandAttempts: %v", err)
				}
				if len(attempts) != cellCount {
					b.Fatalf("len(attempts) = %d, want %d", len(attempts), cellCount)
				}
			}
		})
	}
}

// benchmarkPublishedAttemptDirectories publishes count real, terminal
// Attempt/Outcome/EvidenceManifest document triples under one shared
// artifact root, for BenchmarkClassifyAttemptDirectory and
// BenchmarkAssembleEvaluationResult to read back at scale. It publishes
// the documents directly (Publish* calls) rather than through a real
// RunAttempt/CollectEvidence cycle -- an HTTP round trip and a real
// SQLite writer per Attempt would make constructing thousands of them
// the dominant cost of running this benchmark at all, when what these
// two benchmarks actually measure is each document's own read-back cost,
// not how it was produced. Building fixtures is excluded from each
// sub-benchmark's own timed region either way.
func benchmarkPublishedAttemptDirectories(b *testing.B, count int) (root string, attemptRoots []string) {
	b.Helper()
	root = b.TempDir()
	subject := validSubject()
	executor := validExecutorInProcess()
	scenario := validScenario()

	for i := 0; i < count; i++ {
		attemptID, err := NewAttemptID()
		if err != nil {
			b.Fatalf("NewAttemptID: %v", err)
		}
		directories, err := NewAttemptRoot(root, attemptID)
		if err != nil {
			b.Fatalf("NewAttemptRoot: %v", err)
		}
		attempt, err := buildAttemptDocument("bench-set", CellAttempt{Cell: Cell{ScenarioID: scenario.ID, SubjectID: subject.ID, ExecutorID: executor.ID}}, attemptID, directories, scenario, subject, executor)
		if err != nil {
			b.Fatalf("buildAttemptDocument: %v", err)
		}
		if err := PublishAttempt(directories.Root, attempt); err != nil {
			b.Fatalf("PublishAttempt: %v", err)
		}

		now := time.Now().UTC()
		outcome := Outcome{
			FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
			Status: OutcomeCompleted, Code: "ok", Message: "benchmark fixture",
			StartedAt: now, EndedAt: now, CollectionStatus: CollectionComplete,
		}
		if err := PublishOutcome(directories.Root, outcome); err != nil {
			b.Fatalf("PublishOutcome: %v", err)
		}
		outcomeDigest, err := OutcomeDigest(outcome)
		if err != nil {
			b.Fatalf("OutcomeDigest: %v", err)
		}
		manifest := EvidenceManifest{
			FormatVersion: FormatVersion, Schema: SchemaEvidenceManifest, AttemptID: attemptID, OutcomeDigest: outcomeDigest,
			Entries: []ManifestEntry{{
				Path: "transcript.jsonl", Role: "transcript", MediaType: "application/x-ndjson",
				SHA256: strings.Repeat("a", 64), ByteLength: 128, Required: true, State: EntryCollected, ProducedBy: "benchmark-fixture",
			}},
			TotalBytes: 128, FileCount: 1, CollectionStartedAt: now, CollectionEndedAt: now,
		}
		if err := PublishEvidenceManifest(directories.Root, manifest); err != nil {
			b.Fatalf("PublishEvidenceManifest: %v", err)
		}
		attemptRoots = append(attemptRoots, directories.Root)
	}
	return root, attemptRoots
}

// BenchmarkClassifyAttemptDirectory measures design §18's own recovery
// classification cost (the pure-filesystem part of recovery this
// package can benchmark without a real crash to recover from) at scale:
// how long it takes to classify every already-published Attempt
// directory under an artifact root.
func BenchmarkClassifyAttemptDirectory(b *testing.B) {
	for _, cellCount := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("cells=%d", cellCount), func(b *testing.B) {
			_, attemptRoots := benchmarkPublishedAttemptDirectories(b, cellCount)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, attemptRoot := range attemptRoots {
					if _, err := ClassifyAttemptDirectory(attemptRoot); err != nil {
						b.Fatalf("ClassifyAttemptDirectory: %v", err)
					}
				}
			}
		})
	}
}

// BenchmarkAssembleEvaluationResult measures och-eval report's own
// aggregation cost (design §26) at scale: reading back every terminal
// Attempt's Outcome and Scores under an artifact root, the same
// operation cmd/och-eval/report.go performs once per published Attempt.
func BenchmarkAssembleEvaluationResult(b *testing.B) {
	for _, cellCount := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("cells=%d", cellCount), func(b *testing.B) {
			_, attemptRoots := benchmarkPublishedAttemptDirectories(b, cellCount)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, attemptRoot := range attemptRoots {
					directories := AttemptRootDirectoriesFor(attemptRoot)
					if _, err := AssembleEvaluationResult(directories); err != nil {
						b.Fatalf("AssembleEvaluationResult: %v", err)
					}
				}
			}
		})
	}
}

// BenchmarkCollectEvidence measures design §14's own evidence-export
// cost in isolation from model latency: one real, already-completed
// in-process Attempt's post-shutdown staging and manifest publication
// (transcript/audit copy, digesting, atomic publish), with the real
// RunAttempt call it depends on excluded from the timed region.
func BenchmarkCollectEvidence(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		directories, execution, documents := benchmarkHappyAttempt(b)
		b.StartTimer()
		if _, _, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, documents, CollectionLimits{}); err != nil {
			b.Fatalf("CollectEvidence: %v", err)
		}
	}
}

// benchmarkHappyAttempt mirrors evidence_test.go's own runHappyAttempt
// (a real approved write_file call through a real in-process httptest
// fixture, so there is a real workspace artifact and audit trail to
// export), parameterized over testing.TB so BenchmarkCollectEvidence can
// share it.
func benchmarkHappyAttempt(b *testing.B) (directories AttemptRootDirectories, execution ExecutionOutcome, documents EvidenceDocuments) {
	b.Helper()
	server := newApprovalProvider(b)
	subject := testSubject(b, server.Server)
	attemptID := testAttemptID(b)
	directories = testDirectories(b, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "write the file"),
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "output.txt"}},
	}
	scenario.ApprovalScript = []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "write_file", Answer: ApprovalAllow},
	}
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit", "workspace"}
	scenario.OptionalEvidenceRoles = nil
	executor := validExecutorInProcess()
	set := testEvalSetFor(b, LaneFixture, scenario, subject, executor, nil)
	set.ID = "bench-set"
	attempt, err := buildAttemptDocument(set.ID, CellAttempt{Cell: Cell{ScenarioID: scenario.ID, SubjectID: subject.ID, ExecutorID: executor.ID}}, attemptID, directories, scenario, subject, executor)
	if err != nil {
		b.Fatalf("buildAttemptDocument: %v", err)
	}
	if err := PublishAttempt(directories.Root, attempt); err != nil {
		b.Fatalf("PublishAttempt: %v", err)
	}
	documents = EvidenceDocuments{Scenario: scenario, Subject: subject, Executor: executor, Attempt: attempt, EvalSet: set}

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err = RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		b.Fatalf("RunAttempt: %v", err)
	}
	return directories, execution, documents
}
