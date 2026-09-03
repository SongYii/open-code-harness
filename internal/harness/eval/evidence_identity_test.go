package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// liveTestSubject is a frozen live-lane Subject. Nothing in this file ever
// executes it: a live Subject's production path deliberately refuses a
// plain-HTTP loopback endpoint, so a test that pretended to run one would
// be testing a path production must never take. These tests exercise the
// evidence chain itself — staging frozen identity documents and reading
// them back — which is where the live lane's own binding rules actually
// live.
func liveTestSubject() Subject {
	subject := validSubject()
	subject.ID = "live-subject"
	subject.Provider.Lane = ProviderLaneLive
	subject.Provider.NormalizedEndpoint = "https://api.example.com/v1"
	return subject
}

// testEvalSetFor builds the frozen EvalSet that actually references the
// given documents, so a binding test is never passing merely because the
// set was vacuous.
func testEvalSetFor(t testing.TB, lane EvalLane, scenario Scenario, subject Subject, executor Executor, config *JudgeConfig) EvalSet {
	t.Helper()
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
		ID:              "test-set",
		Scenarios:       []ScenarioRef{{ID: scenario.ID, Digest: scenarioDigest}},
		Subjects:        []SubjectRef{{ID: subject.ID, Digest: subjectDigest}},
		Executors:       []ExecutorRef{{ID: executor.ID, Digest: executorDigest}},
		RepetitionCount: 1,
		PairingSeed:     "seed-1",
		Limits:          validEvalSetLimits(),
		ArtifactRoot:    ".eval",
		Lane:            lane,
	}
	if config != nil {
		digest, err := JudgeConfigDigest(*config)
		if err != nil {
			t.Fatalf("JudgeConfigDigest: %v", err)
		}
		set.JudgeConfigDigest = digest
	}
	return set
}

// collectedLiveJudgeAttempt publishes and collects one live-lane Attempt's
// complete evidence tree without ever launching a Subject: CollectEvidence
// stages the frozen identity documents and commits a manifest on its own,
// which is exactly the surface these tests are about.
func collectedLiveJudgeAttempt(t *testing.T) (AttemptRootDirectories, EvidenceDocuments) {
	t.Helper()
	config := validJudgeConfig()
	return collectedIdentityAttempt(t, LaneLive, liveTestSubject(), &config, identityAttemptOptions{})
}

func collectedFixtureJudgeAttempt(t *testing.T) (AttemptRootDirectories, EvidenceDocuments) {
	t.Helper()
	return collectedIdentityAttempt(t, LaneFixture, validSubject(), nil, identityAttemptOptions{})
}

// identityAttemptOptions lets a caller steer what the deterministic
// verifiers will conclude about the resulting Attempt, without any of
// these tests having to execute a Subject.
type identityAttemptOptions struct {
	// VerifierIDs the Scenario declares. Empty means validScenario's own.
	VerifierIDs []string
	// RequiredEvidenceRoles the Scenario declares. Empty means "scenario",
	// which staging always collects, so manifest-complete-v1 passes.
	RequiredEvidenceRoles []string
	// OutcomeStatus overrides the published Outcome's status, which is how
	// outcome-not-infra-failed-v1 is driven to Fail.
	OutcomeStatus OutcomeStatus
}

func collectedIdentityAttempt(t *testing.T, lane EvalLane, subject Subject, config *JudgeConfig, options identityAttemptOptions) (AttemptRootDirectories, EvidenceDocuments) {
	t.Helper()
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	if len(options.VerifierIDs) > 0 {
		scenario.DeterministicVerifierIDs = options.VerifierIDs
	}
	if len(options.RequiredEvidenceRoles) > 0 {
		scenario.RequiredEvidenceRoles = options.RequiredEvidenceRoles
		scenario.OptionalEvidenceRoles = nil
	}
	executor := validExecutorInProcess()
	set := testEvalSetFor(t, lane, scenario, subject, executor, config)

	attempt, err := buildAttemptDocument(
		set.ID,
		CellAttempt{Cell: Cell{ScenarioID: scenario.ID, SubjectID: subject.ID, ExecutorID: executor.ID}},
		attemptID, directories, scenario, subject, executor,
	)
	if err != nil {
		t.Fatalf("buildAttemptDocument: %v", err)
	}
	if err := PublishAttempt(directories.Root, attempt); err != nil {
		t.Fatalf("PublishAttempt: %v", err)
	}

	documents := EvidenceDocuments{
		Scenario: scenario, Subject: subject, Executor: executor,
		Attempt: attempt, EvalSet: set, JudgeConfig: config,
	}
	outcome := validOutcome(t, attemptID)
	if options.OutcomeStatus != "" {
		outcome.Status = options.OutcomeStatus
	}
	if _, _, err := CollectEvidence(context.Background(), directories,
		ExecutionOutcome{WriterStopped: true, Outcome: outcome}, outcome, documents, CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	return directories, documents
}

func TestStageEvidenceDocumentsBindsEvalSetOnEveryAttempt(t *testing.T) {
	directories, _ := collectedFixtureJudgeAttempt(t)
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	entries := reader.Entries("eval_set")
	if len(entries) != 1 || entries[0].State != EntryCollected {
		t.Fatalf("Entries(eval_set) = %+v, want exactly one collected entry", entries)
	}
	if entries[0].Path != "eval-set.json" {
		t.Fatalf("eval_set entry path = %q, want %q", entries[0].Path, "eval-set.json")
	}
	if judge := reader.Entries("judge_config"); len(judge) != 0 {
		t.Fatalf("a fixture Attempt staged judge_config evidence: %+v", judge)
	}
}

func TestStageEvidenceDocumentsBindsJudgeConfigOnLiveAttempts(t *testing.T) {
	directories, documents := collectedLiveJudgeAttempt(t)
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	entries := reader.Entries("judge_config")
	if len(entries) != 1 || entries[0].State != EntryCollected {
		t.Fatalf("Entries(judge_config) = %+v, want exactly one collected entry", entries)
	}
	if entries[0].Path != "judge-config.json" {
		t.Fatalf("judge_config entry path = %q, want %q", entries[0].Path, "judge-config.json")
	}
	staged, err := reader.ReadEntry(entries[0].Path)
	if err != nil {
		t.Fatalf("ReadEntry: %v", err)
	}
	decoded, err := DecodeJudgeConfig(staged)
	if err != nil {
		t.Fatalf("DecodeJudgeConfig(staged bytes): %v", err)
	}
	digest, err := JudgeConfigDigest(decoded)
	if err != nil {
		t.Fatalf("JudgeConfigDigest: %v", err)
	}
	if digest != documents.EvalSet.JudgeConfigDigest {
		t.Fatalf("staged judge config digest %q disagrees with the frozen EvalSet's %q", digest, documents.EvalSet.JudgeConfigDigest)
	}
}

func TestStageEvidenceDocumentsRejectsBrokenBinding(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*EvidenceDocuments)
	}{
		{"eval set id disagrees with the attempt", func(documents *EvidenceDocuments) {
			documents.EvalSet.ID = "other-set"
		}},
		{"eval set does not reference the attempt's subject", func(documents *EvidenceDocuments) {
			documents.EvalSet.Subjects = []SubjectRef{{ID: "some-other-subject", Digest: documents.Attempt.SubjectDigest}}
		}},
		{"eval set references the subject at a different digest", func(documents *EvidenceDocuments) {
			documents.EvalSet.Subjects = []SubjectRef{{ID: documents.Subject.ID, Digest: mustDigest(t, 0x33)}}
		}},
		{"live eval set carries no judge config", func(documents *EvidenceDocuments) {
			documents.JudgeConfig = nil
		}},
		{"judge config digest disagrees with the eval set", func(documents *EvidenceDocuments) {
			other := validJudgeConfig()
			other.Version = "v2"
			documents.JudgeConfig = &other
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			attemptID := testAttemptID(t)
			directories := testDirectories(t, attemptID)
			scenario := validScenario()
			subject := liveTestSubject()
			executor := validExecutorInProcess()
			config := validJudgeConfig()
			set := testEvalSetFor(t, LaneLive, scenario, subject, executor, &config)
			attempt, err := buildAttemptDocument(set.ID,
				CellAttempt{Cell: Cell{ScenarioID: scenario.ID, SubjectID: subject.ID, ExecutorID: executor.ID}},
				attemptID, directories, scenario, subject, executor)
			if err != nil {
				t.Fatalf("buildAttemptDocument: %v", err)
			}
			if err := PublishAttempt(directories.Root, attempt); err != nil {
				t.Fatalf("PublishAttempt: %v", err)
			}
			documents := EvidenceDocuments{
				Scenario: scenario, Subject: subject, Executor: executor,
				Attempt: attempt, EvalSet: set, JudgeConfig: &config,
			}
			testCase.mutate(&documents)

			budget := &collectionBudget{limits: CollectionLimits{}.withDefaults()}
			if err := stageEvidenceDocuments(directories, documents, budget); err == nil {
				t.Fatalf("stageEvidenceDocuments accepted %s", testCase.name)
			}
			if _, err := os.Lstat(filepath.Join(directories.Evidence, "judge-config.json")); err == nil {
				t.Fatal("a rejected binding still staged judge-config.json")
			}
		})
	}
}

func TestReadJudgeEvidenceDocumentsReturnsTheFrozenChain(t *testing.T) {
	directories, documents := collectedLiveJudgeAttempt(t)
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	published, err := ReadAttempt(directories.Root)
	if err != nil {
		t.Fatalf("ReadAttempt: %v", err)
	}
	got, lane, err := readJudgeEvidenceDocuments(reader, published)
	if err != nil {
		t.Fatalf("readJudgeEvidenceDocuments: %v", err)
	}
	if lane != LaneLive {
		t.Fatalf("lane = %q, want %q", lane, LaneLive)
	}
	if got.EvalSet.ID != documents.EvalSet.ID {
		t.Fatalf("EvalSet.ID = %q, want %q", got.EvalSet.ID, documents.EvalSet.ID)
	}
	if got.JudgeConfig == nil {
		t.Fatal("readJudgeEvidenceDocuments returned no JudgeConfig for a live Attempt")
	}
	if got.JudgeConfig.ID != documents.JudgeConfig.ID || got.JudgeConfig.Version != documents.JudgeConfig.Version {
		t.Fatalf("JudgeConfig = %+v, want %+v", got.JudgeConfig, documents.JudgeConfig)
	}
	if got.Subject.Provider.Lane != ProviderLaneLive {
		t.Fatalf("frozen Subject lane = %q, want %q", got.Subject.Provider.Lane, ProviderLaneLive)
	}
}

// TestReadJudgeEvidenceDocumentsRefusesFixtureAttempts pins the rule that
// only a live Attempt can be judged: a fixture Attempt stages no
// judge_config evidence, so there is nothing to prove a judge run against.
func TestReadJudgeEvidenceDocumentsRefusesFixtureAttempts(t *testing.T) {
	directories, _ := collectedFixtureJudgeAttempt(t)
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	published, err := ReadAttempt(directories.Root)
	if err != nil {
		t.Fatalf("ReadAttempt: %v", err)
	}
	if _, _, err := readJudgeEvidenceDocuments(reader, published); err == nil {
		t.Fatal("readJudgeEvidenceDocuments accepted a fixture Attempt")
	}
}

// TestReadJudgeEvidenceDocumentsRefusesTamperedJudgeConfig proves the
// staged bytes are re-verified against the frozen EvalSet rather than
// trusted, so editing judge-config.json after collection cannot change
// which configuration a Score claims to have used.
func TestReadJudgeEvidenceDocumentsRefusesTamperedJudgeConfig(t *testing.T) {
	directories, _ := collectedLiveJudgeAttempt(t)
	tampered := validJudgeConfig()
	tampered.Version = "v2"
	if err := os.WriteFile(filepath.Join(directories.Evidence, "judge-config.json"), marshal(t, tampered), 0o600); err != nil {
		t.Fatalf("rewrite judge-config.json: %v", err)
	}
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	published, err := ReadAttempt(directories.Root)
	if err != nil {
		t.Fatalf("ReadAttempt: %v", err)
	}
	if _, _, err := readJudgeEvidenceDocuments(reader, published); err == nil {
		t.Fatal("readJudgeEvidenceDocuments accepted a tampered judge-config.json")
	}
}

// degradeToLegacyAttempt rewrites a collected Attempt's manifest to the
// four-document shape an older collector produced, deleting the staged
// EvalSet/JudgeConfig files with it. That is the only faithful way to
// build a legacy Attempt now that staging requires the new roles, and it
// is what the compatibility tests below actually need to exercise.
func degradeToLegacyAttempt(t *testing.T, directories AttemptRootDirectories) {
	t.Helper()
	manifest, err := ReadEvidenceManifest(directories.Root)
	if err != nil {
		t.Fatalf("ReadEvidenceManifest: %v", err)
	}
	kept := manifest.Entries[:0]
	for _, entry := range manifest.Entries {
		if entry.Role == "eval_set" || entry.Role == "judge_config" {
			if err := os.Remove(filepath.Join(directories.Evidence, entry.Path)); err != nil {
				t.Fatalf("remove %s: %v", entry.Path, err)
			}
			continue
		}
		kept = append(kept, entry)
	}
	manifest.Entries = kept
	if err := os.WriteFile(filepath.Join(directories.Evidence, manifestFilename), marshal(t, manifest), 0o600); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
}

// TestLegacyDeterministicRegradeSurvivesWithoutNewRoles pins the
// compatibility rule: an Attempt collected before EvalSet/JudgeConfig
// evidence existed must still regrade deterministically, even though it
// can never be live-judged.
func TestLegacyDeterministicRegradeSurvivesWithoutNewRoles(t *testing.T) {
	directories, _, _ := collectedHappyAttempt(t)
	degradeToLegacyAttempt(t, directories)

	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	published, err := ReadAttempt(directories.Root)
	if err != nil {
		t.Fatalf("ReadAttempt: %v", err)
	}
	if _, _, err := readEvidenceDocuments(reader, published); err != nil {
		t.Fatalf("legacy readEvidenceDocuments: %v", err)
	}
	if _, _, err := readJudgeEvidenceDocuments(reader, published); err == nil {
		t.Fatal("readJudgeEvidenceDocuments accepted a legacy Attempt with no frozen EvalSet evidence")
	}
}
