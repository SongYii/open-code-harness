package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// judgeTestFixture returns a real, collected Attempt's own ArtifactReader
// plus a real manifest path for each of "transcript" and "audit" — every
// judge test below builds its own fixture JudgeCaller responses around
// these real paths rather than invented ones, so a test proving a
// reference is accepted is exercising the same path-existence check a
// test proving one is rejected relies on.
func judgeTestFixture(t *testing.T) (reader *ArtifactReader, transcriptPath, auditPath string) {
	t.Helper()
	directories, _, _ := collectedHappyAttempt(t)
	var err error
	reader, err = NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	transcriptEntries := reader.Entries("transcript")
	if len(transcriptEntries) == 0 {
		t.Fatal("no transcript entries in a happy Attempt's own manifest")
	}
	auditEntries := reader.Entries("audit")
	if len(auditEntries) == 0 {
		t.Fatal("no audit entries in a happy Attempt's own manifest")
	}
	return reader, transcriptEntries[0].Path, auditEntries[0].Path
}

// testJudgeConfig is the canonical frozen document with the two criteria
// every judge test below scores against, so a judge test always exercises
// the same validation a real `och.eval.judge-config` file passes.
func testJudgeConfig() JudgeConfig {
	config := validJudgeConfig()
	config.Provider.ModelID = "judge-model-fixture"
	config.Criteria = []JudgeCriterion{
		{ID: "quality", Rubric: "Judge the transcript's own quality.", EvidenceRoles: []string{"transcript"}},
		{ID: "continuity", Rubric: "Judge the audit record's own continuity.", EvidenceRoles: []string{"audit"}},
	}
	return config
}

func fixedJudgeCaller(t *testing.T, output judgeRawOutput) JudgeCaller {
	t.Helper()
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal fixture judge output: %v", err)
	}
	return func(context.Context, string, string) (string, ScorerUsage, error) {
		return string(data), ScorerUsage{InputTokens: 42, OutputTokens: 7}, nil
	}
}

func TestRunJudgeKnownPassFixture(t *testing.T) {
	reader, transcriptPath, auditPath := judgeTestFixture(t)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict: "pass",
		Criteria: []judgeRawCriterion{
			{ID: "quality", Status: "pass"},
			{ID: "continuity", Status: "pass"},
		},
		EvidenceReferences: []string{transcriptPath, auditPath},
		Rationale:          "the transcript and audit both support a clean completion",
	})

	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScorePass {
		t.Fatalf("Verdict = %q, want %q (rationale: %s)", outcome.Verdict, ScorePass, outcome.Rationale)
	}
	if len(outcome.Criteria) != 2 {
		t.Fatalf("Criteria = %+v, want 2 entries", outcome.Criteria)
	}
	if outcome.Usage.InputTokens != 42 || outcome.Usage.OutputTokens != 7 {
		t.Fatalf("Usage = %+v, want the caller's own reported usage carried through", outcome.Usage)
	}
}

func TestRunJudgeKnownFailFixture(t *testing.T) {
	reader, transcriptPath, _ := judgeTestFixture(t)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict: "fail",
		Criteria: []judgeRawCriterion{
			{ID: "quality", Status: "fail"},
			{ID: "continuity", Status: "pass"},
		},
		EvidenceReferences: []string{transcriptPath},
		Rationale:          "the transcript shows the constraint was dropped",
	})

	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreFail {
		t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreFail)
	}
}

// TestRunJudgeRejectsNonexistentEvidenceReference is the hallucinated-
// reference meta-eval fixture: a judge output citing a manifest path it was
// never actually shown must never be trusted at face value.
//
// It declares every criterion testJudgeConfig requires, and asserts the
// refusal *reason*, not merely that some refusal happened. Until 2026-09-04
// it did neither: it named only `quality` of the two required criteria, so
// RunJudge refused it at the earlier omitted-criterion check and never
// reached the reference validation this fixture exists to prove. Deleting
// the entire reference check left the test green — verified by mutation.
// A meta-eval fixture that accepts any refusal is satisfied by the wrong
// one.
func TestRunJudgeRejectsNonexistentEvidenceReference(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict: "pass",
		Criteria: []judgeRawCriterion{
			{ID: "quality", Status: "pass"},
			{ID: "continuity", Status: "pass"},
		},
		EvidenceReferences: []string{"evidence/this-path-was-never-shown.jsonl"},
		Rationale:          "looks fine",
	})

	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q: a hallucinated evidence reference must never be trusted", outcome.Verdict, ScoreIndeterminate)
	}
	if !strings.Contains(outcome.Rationale, "never shown to it") {
		t.Fatalf("refused for the wrong reason: %q; want the hallucinated-reference refusal", outcome.Rationale)
	}
}

// TestRunJudgeRejectsAReferenceItWasNotShown is the realistic shape of the
// same defect: the cited path is not invented at all, it is a genuine entry
// in this Attempt's own manifest that no declared criterion role put into
// the bundle. `workspace/output.txt` really exists here; testJudgeConfig
// declares only the transcript and audit roles, so the judge never saw it.
//
// A reference check that resolved against the manifest rather than against
// the bundle would accept this and be wrong, which is why the guard is
// written against the paths actually shown.
func TestRunJudgeRejectsAReferenceItWasNotShown(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	unshown := reader.Entries("workspace")
	if len(unshown) == 0 {
		t.Fatal("fixture no longer carries a workspace entry; this fixture needs a real manifest path outside the declared roles")
	}
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict: "pass",
		Criteria: []judgeRawCriterion{
			{ID: "quality", Status: "pass"},
			{ID: "continuity", Status: "pass"},
		},
		EvidenceReferences: []string{unshown[0].Path},
		Rationale:          "the workspace output looks correct",
	})

	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q: a real manifest path the judge was never shown is still a claim it cannot support",
			outcome.Verdict, ScoreIndeterminate)
	}
	if !strings.Contains(outcome.Rationale, "never shown to it") {
		t.Fatalf("refused for the wrong reason: %q", outcome.Rationale)
	}
}

// TestRunJudgeRejectsADeterminateVerdictCitingNoEvidence closes the
// citation-free hole in the same family as the budget-omission defect: until
// 2026-09-04 a judge could return `pass` with an empty evidenceReferences
// list and be believed. Every reference rule guarded the references that
// were present; none required any to be.
func TestRunJudgeRejectsADeterminateVerdictCitingNoEvidence(t *testing.T) {
	for _, verdict := range []string{"pass", "fail"} {
		t.Run(verdict, func(t *testing.T) {
			reader, _, _ := judgeTestFixture(t)
			status := verdict
			caller := fixedJudgeCaller(t, judgeRawOutput{
				Verdict: verdict,
				Criteria: []judgeRawCriterion{
					{ID: "quality", Status: status},
					{ID: "continuity", Status: status},
				},
				EvidenceReferences: nil,
				Rationale:          "everything looked fine to me",
			})

			outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
			if err != nil {
				t.Fatalf("RunJudge: %v", err)
			}
			if outcome.Verdict != ScoreIndeterminate {
				t.Fatalf("Verdict = %q, want %q: a determinate verdict citing nothing is unfalsifiable",
					outcome.Verdict, ScoreIndeterminate)
			}
			if !strings.Contains(outcome.Rationale, "citing no evidence at all") {
				t.Fatalf("refused for the wrong reason: %q", outcome.Rationale)
			}
		})
	}
}

// TestRunJudgeIndeterminateMayCiteNoEvidence is the other half of the rule
// above, kept as its own fixture so the guard cannot be tightened into
// refusing every citation-free output: an indeterminate verdict citing
// nothing is exactly what a judge that could not read its material should
// return.
func TestRunJudgeIndeterminateMayCiteNoEvidence(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict: "indeterminate",
		Criteria: []judgeRawCriterion{
			{ID: "quality", Status: "indeterminate"},
			{ID: "continuity", Status: "indeterminate"},
		},
		EvidenceReferences: nil,
		Rationale:          "the transcript excerpt was not legible",
	})

	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreIndeterminate)
	}
	if strings.Contains(outcome.Rationale, "citing no evidence at all") {
		t.Fatalf("an indeterminate verdict must not be refused for citing nothing: %q", outcome.Rationale)
	}
}

// TestRunJudgeUnresolvedContradictionIsAlwaysIndeterminate is the
// "contradiction" meta-eval fixture: design's own rule is that an
// unresolved contradiction is indeterminate regardless of whatever
// verdict the judge itself claimed alongside it.
//
// Like the hallucinated-reference fixture, this named only `quality` of the
// two required criteria until 2026-09-04, so it was refused at the earlier
// omitted-criterion check and the contradiction branch never ran — its
// ContradictoryEvidence came back empty, and disabling the contradiction
// rule outright left the test green. It now declares both criteria and
// asserts the contradiction actually survived into the outcome.
func TestRunJudgeUnresolvedContradictionIsAlwaysIndeterminate(t *testing.T) {
	reader, transcriptPath, auditPath := judgeTestFixture(t)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict: "pass",
		Criteria: []judgeRawCriterion{
			{ID: "quality", Status: "pass"},
			{ID: "continuity", Status: "pass"},
		},
		EvidenceReferences:    []string{transcriptPath},
		ContradictoryEvidence: []string{auditPath},
		Rationale:             "the transcript claims success but the audit log disagrees",
	})

	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q: an unresolved contradiction must override the judge's own claimed pass", outcome.Verdict, ScoreIndeterminate)
	}
	if len(outcome.ContradictoryEvidence) != 1 || outcome.ContradictoryEvidence[0] != auditPath {
		t.Fatalf("ContradictoryEvidence = %v, want the audit path carried through: the contradiction branch never ran",
			outcome.ContradictoryEvidence)
	}
}

// TestRunJudgeRejectsUnsupportedClaim is the "unsupported-claim"
// meta-eval fixture: a judge output naming a criterion this run never
// declared must never be silently accepted as if it were real.
func TestRunJudgeRejectsUnsupportedClaim(t *testing.T) {
	reader, transcriptPath, _ := judgeTestFixture(t)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict:            "pass",
		Criteria:           []judgeRawCriterion{{ID: "criterion-nobody-declared", Status: "pass"}},
		EvidenceReferences: []string{transcriptPath},
		Rationale:          "looks fine",
	})

	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q: a criterion this run never declared must never be trusted", outcome.Verdict, ScoreIndeterminate)
	}
}

// TestRunJudgeRejectsMalformedOutput covers both an unparsable body and
// a well-formed-but-unknown-field body (design's own "unknown fields...
// is indeterminate").
func TestRunJudgeRejectsMalformedOutput(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	config := testJudgeConfig()

	t.Run("not json", func(t *testing.T) {
		caller := func(context.Context, string, string) (string, ScorerUsage, error) {
			return "this is not json at all", ScorerUsage{}, nil
		}
		outcome, err := RunJudge(context.Background(), reader, config, caller)
		if err != nil {
			t.Fatalf("RunJudge: %v", err)
		}
		if outcome.Verdict != ScoreIndeterminate {
			t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreIndeterminate)
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		caller := func(context.Context, string, string) (string, ScorerUsage, error) {
			return `{"verdict":"pass","criteria":[],"evidenceReferences":[],"rationale":"ok","unexpectedField":true}`, ScorerUsage{}, nil
		}
		outcome, err := RunJudge(context.Background(), reader, config, caller)
		if err != nil {
			t.Fatalf("RunJudge: %v", err)
		}
		if outcome.Verdict != ScoreIndeterminate {
			t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreIndeterminate)
		}
	})

	t.Run("unknown verdict", func(t *testing.T) {
		caller := fixedJudgeCaller(t, judgeRawOutput{Verdict: "excellent", Rationale: "great job"})
		outcome, err := RunJudge(context.Background(), reader, config, caller)
		if err != nil {
			t.Fatalf("RunJudge: %v", err)
		}
		if outcome.Verdict != ScoreIndeterminate {
			t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreIndeterminate)
		}
	})
}

func TestRunJudgeRejectsTrailingJSONValue(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	caller := func(context.Context, string, string) (string, ScorerUsage, error) {
		return `{"verdict":"pass","score":1,"criteria":[{"id":"quality","status":"pass"},{"id":"continuity","status":"pass"}],"evidenceReferences":[],"rationale":"ok"} {}`, ScorerUsage{}, nil
	}
	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q for trailing JSON", outcome.Verdict, ScoreIndeterminate)
	}
}

func TestRunJudgeRequiresEveryCriterionExactlyOnce(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	for _, test := range []struct {
		name     string
		criteria []judgeRawCriterion
	}{
		{name: "omitted", criteria: []judgeRawCriterion{{ID: "quality", Status: "pass"}}},
		{name: "duplicate", criteria: []judgeRawCriterion{{ID: "quality", Status: "pass"}, {ID: "quality", Status: "pass"}, {ID: "continuity", Status: "pass"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			caller := fixedJudgeCaller(t, judgeRawOutput{Verdict: "pass", Score: float64Pointer(1), Criteria: test.criteria, Rationale: "ok"})
			outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
			if err != nil {
				t.Fatalf("RunJudge: %v", err)
			}
			if outcome.Verdict != ScoreIndeterminate {
				t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreIndeterminate)
			}
		})
	}
}

func TestRunJudgeCannotPassWithMissingEvidence(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict: "pass",
		Score:   float64Pointer(1),
		Criteria: []judgeRawCriterion{
			{ID: "quality", Status: "pass"},
			{ID: "continuity", Status: "pass"},
		},
		MissingEvidence: []string{"the final workspace state"},
		Rationale:       "probably fine",
	})
	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q when evidence is missing", outcome.Verdict, ScoreIndeterminate)
	}
}

func TestRunJudgeRejectsAggregateVerdictInconsistentWithCriteria(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict: "pass",
		Criteria: []judgeRawCriterion{
			{ID: "quality", Status: "fail"},
			{ID: "continuity", Status: "pass"},
		},
		Rationale: "internally inconsistent",
	})
	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreIndeterminate)
	}
}

func TestRunJudgeRejectsOutOfRangeScores(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict: "pass",
		Score:   float64Pointer(1.1),
		Criteria: []judgeRawCriterion{
			{ID: "quality", Status: "pass"},
			{ID: "continuity", Status: "pass"},
		},
		Rationale: "out of range",
	})
	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreIndeterminate)
	}
}

func TestRunJudgeValidatesFrozenConfigBeforeCallingModel(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*JudgeConfig)
	}{
		{name: "missing model", mutate: func(config *JudgeConfig) { config.Provider.ModelID = "" }},
		{name: "wrong prompt digest", mutate: func(config *JudgeConfig) {
			config.Prompt.Digest = Digest("sha256:" + strings.Repeat("0", 64))
		}},
		{name: "duplicate criterion", mutate: func(config *JudgeConfig) { config.Criteria[1].ID = config.Criteria[0].ID }},
		{name: "empty role", mutate: func(config *JudgeConfig) { config.Criteria[0].EvidenceRoles = []string{""} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := testJudgeConfig()
			test.mutate(&config)
			called := false
			caller := func(context.Context, string, string) (string, ScorerUsage, error) {
				called = true
				return "", ScorerUsage{}, nil
			}
			if _, err := RunJudge(context.Background(), reader, config, caller); err == nil {
				t.Fatal("RunJudge() error = nil, want invalid configuration error")
			}
			if called {
				t.Fatal("RunJudge called the model before rejecting invalid frozen config")
			}
		})
	}
}

func TestBuildJudgeEvidenceBundleIncludesTrustedCriteriaContract(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	bundle, err := buildJudgeEvidenceBundle(reader, testJudgeConfig())
	if err != nil {
		t.Fatalf("buildJudgeEvidenceBundle: %v", err)
	}
	if !strings.Contains(bundle.Text, `<criteria>`) || !strings.Contains(bundle.Text, `"id":"quality"`) || !strings.Contains(bundle.Text, `"id":"continuity"`) {
		t.Fatalf("bundle does not carry the frozen criteria contract: %s", bundle.Text)
	}
}

func float64Pointer(value float64) *float64 { return &value }

// TestRunJudgeCallerFailureBecomesIndeterminate proves a live-model call
// failure (network error, timeout, whatever a real JudgeCaller might
// return) is represented as a real, informative Indeterminate outcome
// rather than aborting scoring outright.
func TestRunJudgeCallerFailureBecomesIndeterminate(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	caller := func(context.Context, string, string) (string, ScorerUsage, error) {
		return "", ScorerUsage{}, errFixtureJudgeCallFailed
	}
	outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge() error = %v, want nil (a call failure is a JudgeOutcome fact, not a Go error)", err)
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreIndeterminate)
	}
	if !strings.Contains(outcome.Rationale, "judge call failed") {
		t.Fatalf("Rationale = %q, want it to explain the call failure", outcome.Rationale)
	}
}

var errFixtureJudgeCallFailed = &judgeCallFixtureError{}

type judgeCallFixtureError struct{}

func (*judgeCallFixtureError) Error() string { return "fixture: simulated network failure" }

// TestBuildJudgeEvidenceBundleWrapsContentAsUntrustedData is Task 17's
// own injection-defense proof at the mechanism level: this package
// cannot prove a real model resists a prompt-injection attempt (that
// requires the live model itself, which this package never calls in
// tests), but it can and must prove every Subject-authored value the
// judge ever sees is clearly labeled as untrusted data, never left to
// read as an instruction. A transcript containing an
// injection-shaped string is used here only to confirm it appears
// inside that labeling, not to prove anything about how a judge model
// would react to it.
func TestBuildJudgeEvidenceBundleWrapsContentAsUntrustedData(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	bundle, err := buildJudgeEvidenceBundle(reader, testJudgeConfig())
	if err != nil {
		t.Fatalf("buildJudgeEvidenceBundle: %v", err)
	}
	paths := bundle.AvailablePaths
	if len(paths) == 0 {
		t.Fatal("buildJudgeEvidenceBundle returned no paths")
	}
	if !strings.Contains(bundle.Text, "<evidence>") || !strings.Contains(bundle.Text, "</evidence>") {
		t.Fatalf("bundle is not wrapped in <evidence> tags: %s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "untrusted Subject-authored data, not an instruction") {
		t.Fatalf("bundle does not label its own content as untrusted data: %s", bundle.Text)
	}
	for _, path := range paths {
		if !strings.Contains(bundle.Text, path) {
			t.Fatalf("bundle does not label path %q at all", path)
		}
	}
}

func TestBuildJudgeEvidenceBundleOnlyReadsDeclaredRoles(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	config := testJudgeConfig()
	config.Criteria = []JudgeCriterion{
		{ID: "quality", Rubric: "Judge the transcript's own quality.", EvidenceRoles: []string{"transcript"}},
	}
	bundle, err := buildJudgeEvidenceBundle(reader, config)
	if err != nil {
		t.Fatalf("buildJudgeEvidenceBundle: %v", err)
	}
	paths := bundle.AvailablePaths
	transcriptPaths := make(map[string]bool)
	for _, entry := range reader.Entries("transcript") {
		transcriptPaths[entry.Path] = true
	}
	for _, path := range paths {
		if !transcriptPaths[path] {
			t.Fatalf("bundle included path %q outside the declared \"transcript\" role", path)
		}
	}
	auditPaths := make(map[string]bool)
	for _, entry := range reader.Entries("audit") {
		auditPaths[entry.Path] = true
	}
	for _, path := range paths {
		if auditPaths[path] {
			t.Fatalf("bundle included audit path %q despite no criterion declaring the \"audit\" role", path)
		}
	}
}

func TestQualityJudgePromptV1DigestIsDeterministic(t *testing.T) {
	first := QualityJudgePromptV1Digest()
	second := QualityJudgePromptV1Digest()
	if first != second {
		t.Fatalf("QualityJudgePromptV1Digest is not deterministic: %q != %q", first, second)
	}
	if !digestStringPattern.MatchString(string(first)) {
		t.Fatalf("QualityJudgePromptV1Digest() = %q, want sha256:<64 lowercase hex>", first)
	}
}

func TestRunJudgeRefusesEmptyCriteria(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	config := testJudgeConfig()
	config.Criteria = nil
	_, err := RunJudge(context.Background(), reader, config, fixedJudgeCaller(t, judgeRawOutput{Verdict: "pass"}))
	if err == nil {
		t.Fatal("RunJudge() error = nil, want a refusal for a config with no criteria")
	}
}

// judgeBudgetReader builds a reader over two roles whose bounded excerpts
// together exceed the total bundle budget, so the builder must drop some
// of them. perRole entries of exactly maxJudgeEvidenceEntryBytes make the
// arithmetic exact: only maxJudgeEvidenceBundleBytes/maxJudgeEvidenceEntryBytes
// entries can ever fit.
func judgeBudgetReader(t *testing.T, perRole int) *ArtifactReader {
	t.Helper()
	root := t.TempDir()
	manifest := EvidenceManifest{}
	// "beta" is declared first so manifest order and sorted order disagree:
	// a builder that leaked map or manifest order into its selection would
	// produce a different answer than one that sorts.
	for _, role := range []string{"beta", "alpha"} {
		for index := 0; index < perRole; index++ {
			path := fmt.Sprintf("%s-%02d.txt", role, index)
			data := []byte(strings.Repeat("a", maxJudgeEvidenceEntryBytes))
			if err := os.WriteFile(filepath.Join(root, path), data, 0o600); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			sum := sha256.Sum256(data)
			manifest.Entries = append(manifest.Entries, ManifestEntry{
				Path: path, Role: role, MediaType: "text/plain", Required: true,
				State: EntryCollected, SHA256: hex.EncodeToString(sum[:]),
				ByteLength: int64(len(data)), ProducedBy: "test",
			})
		}
	}
	return &ArtifactReader{evidenceRoot: root, manifest: manifest}
}

func judgeBudgetConfig() JudgeConfig {
	config := validJudgeConfig()
	config.Criteria = []JudgeCriterion{
		{ID: "quality", Rubric: "Judge alpha.", EvidenceRoles: []string{"alpha"}},
		{ID: "continuity", Rubric: "Judge beta.", EvidenceRoles: []string{"beta"}},
	}
	return config
}

// TestJudgeBundleIsStableBeforeLimits pins the property the whole contract
// rests on: judging the same Attempt twice must show the judge the same
// evidence. Selection happens after sorting, so Go's randomized map
// iteration over the declared role set cannot reach it.
func TestJudgeBundleIsStableBeforeLimits(t *testing.T) {
	reader := judgeBudgetReader(t, 20)
	config := judgeBudgetConfig()

	first, err := buildJudgeEvidenceBundle(reader, config)
	if err != nil {
		t.Fatalf("buildJudgeEvidenceBundle: %v", err)
	}
	for round := 0; round < 50; round++ {
		next, err := buildJudgeEvidenceBundle(reader, config)
		if err != nil {
			t.Fatalf("buildJudgeEvidenceBundle round %d: %v", round, err)
		}
		if next.Text != first.Text {
			t.Fatalf("bundle text changed at round %d", round)
		}
		if !reflect.DeepEqual(next.AvailablePaths, first.AvailablePaths) {
			t.Fatalf("selection changed at round %d:\n got=%v\nfirst=%v", round, next.AvailablePaths, first.AvailablePaths)
		}
		if !reflect.DeepEqual(next.MissingPaths, first.MissingPaths) {
			t.Fatalf("omissions changed at round %d:\n got=%v\nfirst=%v", round, next.MissingPaths, first.MissingPaths)
		}
	}
	if !sort.StringsAreSorted(first.AvailablePaths) {
		t.Fatalf("AvailablePaths is not sorted: %v", first.AvailablePaths)
	}
	if len(first.MissingPaths) == 0 {
		t.Fatal("the budget never engaged; this fixture proves nothing")
	}
}

// TestRunJudgeSkipsModelWhenSelectedEvidenceIsOmitted is the fail-closed
// half: evidence a criterion declared but the budget could not carry must
// stop the run, not silently shrink what the judge was asked about.
func TestRunJudgeSkipsModelWhenSelectedEvidenceIsOmitted(t *testing.T) {
	called := false
	caller := func(context.Context, string, string) (string, ScorerUsage, error) {
		called = true
		return "", ScorerUsage{}, nil
	}
	outcome, err := RunJudge(context.Background(), judgeBudgetReader(t, 20), judgeBudgetConfig(), caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if called {
		t.Fatal("RunJudge called the model even though selected evidence was omitted")
	}
	if outcome.Verdict != ScoreIndeterminate {
		t.Fatalf("Verdict = %q, want %q", outcome.Verdict, ScoreIndeterminate)
	}
	if len(outcome.MissingEvidence) == 0 {
		t.Fatal("RunJudge reported no MissingEvidence for silently omitted entries")
	}
	if !sort.StringsAreSorted(outcome.MissingEvidence) {
		t.Fatalf("MissingEvidence is not sorted: %v", outcome.MissingEvidence)
	}
}

// TestRunJudgeSkipsModelWhenADeclaredRoleHasNoCollectedEntry covers the
// other omission the spec names: a role a criterion declared that the
// manifest never collected at all.
func TestRunJudgeSkipsModelWhenADeclaredRoleHasNoCollectedEntry(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	config := testJudgeConfig()
	config.Criteria = append(config.Criteria, JudgeCriterion{
		ID: "coverage", Rubric: "Judge a role nothing collected.", EvidenceRoles: []string{"never-collected"},
	})
	called := false
	caller := func(context.Context, string, string) (string, ScorerUsage, error) {
		called = true
		return "", ScorerUsage{}, nil
	}
	outcome, err := RunJudge(context.Background(), reader, config, caller)
	if err != nil {
		t.Fatalf("RunJudge: %v", err)
	}
	if called {
		t.Fatal("RunJudge called the model for a criterion whose evidence role collected nothing")
	}
	if outcome.Verdict != ScoreIndeterminate || len(outcome.MissingEvidence) == 0 {
		t.Fatalf("outcome = %+v, want indeterminate with missing evidence", outcome)
	}
}

// TestJudgeBundleLabelsRecordTruncation pins the spec's requirement that a
// bounded excerpt is explicit in the request rather than silently passing
// as the whole file.
func TestJudgeBundleLabelsRecordTruncation(t *testing.T) {
	root := t.TempDir()
	data := []byte(strings.Repeat("b", maxJudgeEvidenceEntryBytes*2))
	if err := os.WriteFile(filepath.Join(root, "big.txt"), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	sum := sha256.Sum256(data)
	reader := &ArtifactReader{evidenceRoot: root, manifest: EvidenceManifest{Entries: []ManifestEntry{{
		Path: "big.txt", Role: "alpha", MediaType: "text/plain", Required: true,
		State: EntryCollected, SHA256: hex.EncodeToString(sum[:]),
		ByteLength: int64(len(data)), ProducedBy: "test",
	}}}}
	config := validJudgeConfig()
	config.Criteria = []JudgeCriterion{{ID: "quality", Rubric: "Judge alpha.", EvidenceRoles: []string{"alpha"}}}

	bundle, err := buildJudgeEvidenceBundle(reader, config)
	if err != nil {
		t.Fatalf("buildJudgeEvidenceBundle: %v", err)
	}
	for _, want := range []string{"big.txt", fmt.Sprintf("originalBytes=%d", len(data)), "truncated=true"} {
		if !strings.Contains(bundle.Text, want) {
			t.Fatalf("bundle label does not record %q:\n%s", want, bundle.Text[:min(len(bundle.Text), 400)])
		}
	}
	if len(bundle.MissingPaths) != 0 {
		t.Fatalf("per-entry truncation must not count as omission: %v", bundle.MissingPaths)
	}
}

// TestJudgeBundleRendersRubricsOutsideTheUntrustedBlock pins the spec's
// separation: trusted criteria text is rendered from the frozen config,
// never mixed into Subject-authored evidence.
func TestJudgeBundleRendersRubricsOutsideTheUntrustedBlock(t *testing.T) {
	reader, _, _ := judgeTestFixture(t)
	config := testJudgeConfig()
	bundle, err := buildJudgeEvidenceBundle(reader, config)
	if err != nil {
		t.Fatalf("buildJudgeEvidenceBundle: %v", err)
	}
	criteriaBlock := bundle.Text[strings.Index(bundle.Text, "<criteria>"):strings.Index(bundle.Text, "</criteria>")]
	for _, criterion := range config.Criteria {
		if !strings.Contains(criteriaBlock, criterion.Rubric) {
			t.Fatalf("rubric %q is not rendered inside <criteria>", criterion.Rubric)
		}
	}
	evidenceBlock := bundle.Text[strings.Index(bundle.Text, "<evidence>"):]
	for _, criterion := range config.Criteria {
		if strings.Contains(evidenceBlock, criterion.Rubric) {
			t.Fatalf("rubric %q leaked into the untrusted evidence block", criterion.Rubric)
		}
	}
}

// TestRunJudgeRejectsDuplicateEvidenceReferences pins the spec's "empty,
// duplicate, or nonexistent references are rejected" rule.
func TestRunJudgeRejectsDuplicateEvidenceReferences(t *testing.T) {
	reader, transcriptPath, auditPath := judgeTestFixture(t)
	for _, test := range []struct {
		name       string
		references []string
	}{
		{"duplicate", []string{transcriptPath, transcriptPath}},
		{"empty", []string{transcriptPath, ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			caller := fixedJudgeCaller(t, judgeRawOutput{
				Verdict: "pass",
				Criteria: []judgeRawCriterion{
					{ID: "quality", Status: "pass"}, {ID: "continuity", Status: "pass"},
				},
				EvidenceReferences: test.references,
				Rationale:          "ok",
			})
			outcome, err := RunJudge(context.Background(), reader, testJudgeConfig(), caller)
			if err != nil {
				t.Fatalf("RunJudge: %v", err)
			}
			if outcome.Verdict != ScoreIndeterminate {
				t.Fatalf("Verdict = %q, want %q (auditPath=%q)", outcome.Verdict, ScoreIndeterminate, auditPath)
			}
		})
	}
}
