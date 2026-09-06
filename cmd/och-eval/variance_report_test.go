package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// writePolicy writes an uncalibrated policy document and returns its path.
func writePolicy(t *testing.T, mutate func(*eval.VariancePolicy)) string {
	t.Helper()
	policy := eval.VariancePolicy{
		FormatVersion:           1,
		Schema:                  eval.SchemaVariancePolicy,
		ID:                      "provisional-v1",
		Version:                 "v1",
		Calibration:             eval.CalibrationUncalibrated,
		MaxNumericSpread:        0.20,
		MinVerdictStability:     0.80,
		MinEvaluableRepetitions: 2,
	}
	if mutate != nil {
		mutate(&policy)
	}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "variance-policy.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runReportCLI(t *testing.T, args ...string) (evaluationReport, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), append([]string{"report"}, args...), &stdout, &stderr)
	var report evaluationReport
	if stdout.Len() > 0 {
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("decode report: %v\n%s", err, stdout.String())
		}
	}
	return report, stderr.String(), code
}

// producedArtifacts runs the checked-in PR set and then scores every Attempt.
//
// The scoring pass is not optional decoration. `och-eval run` executes and
// publishes evidence but produces no Score at all; scoring is a separate
// command. A variance signal is computed over Scores, so a report asked for
// one against a freshly-run artifact root would find nothing to measure — and
// an empty variance block reads exactly like "no variance problems", which is
// the misreading this whole mechanism exists to prevent.
func producedArtifacts(t *testing.T) string {
	t.Helper()
	artifactRoot := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"run", "-set", checkedInSetPath(t), "-artifacts", artifactRoot,
	}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run exit = %d; stderr=%s", code, stderr.String())
	}
	scoreEveryAttempt(t, artifactRoot)
	return artifactRoot
}

// scoreEveryAttempt regrades each published Attempt so Scores exist.
func scoreEveryAttempt(t *testing.T, artifactRoot string) {
	t.Helper()
	entries, err := os.ReadDir(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	scored := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var stdout, stderr bytes.Buffer
		if code := runCLI(context.Background(), []string{
			"regrade", "-attempt", filepath.Join(artifactRoot, entry.Name()), "-scorer", varianceTestScorer,
		}, &stdout, &stderr); code == exitOK {
			scored++
		}
	}
	if scored == 0 {
		t.Fatalf("no Attempt under %s could be scored; a variance block needs Scores", artifactRoot)
	}
}

// varianceTestScorer is the deterministic scorer these tests measure over.
const varianceTestScorer = "baseline-v1"

// TestReportWithoutAVariancePolicyIsUnchanged is the guard that matters most.
// Every checked-in set runs each Cell once and declares no policy, so the
// overwhelmingly common report must be exactly the one it is today.
func TestReportWithoutAVariancePolicyIsUnchanged(t *testing.T) {
	artifactRoot := producedArtifacts(t)

	report, stderr, code := runReportCLI(t, "-set", checkedInSetPath(t), "-artifacts", artifactRoot)
	if code != exitOK {
		t.Fatalf("report exit = %d; stderr=%s", code, stderr)
	}
	if len(report.Variance) != 0 {
		t.Fatalf("Variance = %+v, want no block at all without a policy", report.Variance)
	}
	if report.VariancePolicy != nil {
		t.Fatalf("VariancePolicy = %+v, want absent", report.VariancePolicy)
	}
}

// TestReportPublishesThePerCellDistributionBlock.
func TestReportPublishesThePerCellDistributionBlock(t *testing.T) {
	artifactRoot := producedArtifacts(t)

	report, stderr, code := runReportCLI(t,
		"-set", checkedInSetPath(t),
		"-artifacts", artifactRoot,
		"-variance-policy", writePolicy(t, func(p *eval.VariancePolicy) {
			// The checked-in sets run each Cell once, so the floor is lowered
			// to let this report produce a block at all. The Cell will be
			// Cell will fail evaluableEnough for having too few evaluable
			// repetitions, which is the correct answer.
			p.MinEvaluableRepetitions = 2
		}),
		"-variance-scorer", "baseline-v1",
	)
	if code != exitOK {
		t.Fatalf("report exit = %d; stderr=%s", code, stderr)
	}
	if len(report.Variance) == 0 {
		t.Fatalf("Variance block is empty; stderr=%s", stderr)
	}
	for _, cell := range report.Variance {
		if cell.Attempts == 0 {
			t.Fatalf("a variance cell reports no attempts: %+v", cell)
		}
		if cell.ScenarioDigest == "" {
			t.Fatalf("a variance cell carries no identity: %+v", cell)
		}
	}
}

// TestReportMarksAnUncalibratedPolicyOnEveryCellItGoverns closes the accepted
// ordering's own risk at the point a reader actually sees a number.
func TestReportMarksAnUncalibratedPolicyOnEveryCellItGoverns(t *testing.T) {
	artifactRoot := producedArtifacts(t)

	report, stderr, code := runReportCLI(t,
		"-set", checkedInSetPath(t),
		"-artifacts", artifactRoot,
		"-variance-policy", writePolicy(t, nil),
		"-variance-scorer", "baseline-v1",
	)
	if code != exitOK {
		t.Fatalf("report exit = %d; stderr=%s", code, stderr)
	}
	if report.VariancePolicy == nil {
		t.Fatal("the report does not name the policy it applied")
	}
	if report.VariancePolicy.Calibration != string(eval.CalibrationUncalibrated) {
		t.Fatalf("Calibration = %q, want it stated", report.VariancePolicy.Calibration)
	}
	if report.VariancePolicy.Digest == "" {
		t.Fatal("the report does not name the policy digest a reader would need to reproduce it")
	}
	for _, cell := range report.Variance {
		if !cell.Uncalibrated {
			t.Fatalf("cell %+v is not marked as governed by an uncalibrated policy", cell)
		}
	}
}

// TestReportNeverGatesOnAVarianceSignal: a variance result must not change
// the exit code. Ordinary PR CI gates on deterministic verifiers only.
func TestReportNeverGatesOnAVarianceSignal(t *testing.T) {
	artifactRoot := producedArtifacts(t)

	withoutPolicy, _, codeWithout := runReportCLI(t, "-set", checkedInSetPath(t), "-artifacts", artifactRoot)
	_, stderr, codeWith := runReportCLI(t,
		"-set", checkedInSetPath(t),
		"-artifacts", artifactRoot,
		"-variance-policy", writePolicy(t, func(p *eval.VariancePolicy) {
			// Impossible to satisfy: no Cell will be evaluableEnough.
			p.MinEvaluableRepetitions = 99
		}),
		"-variance-scorer", "baseline-v1",
	)
	if codeWith != codeWithout {
		t.Fatalf("exit code changed from %d to %d because of a variance signal; stderr=%s",
			codeWithout, codeWith, stderr)
	}
	_ = withoutPolicy
}

// TestReportRefusesAnInvalidVariancePolicyRatherThanIgnoringIt.
func TestReportRefusesAnInvalidVariancePolicyRatherThanIgnoringIt(t *testing.T) {
	artifactRoot := producedArtifacts(t)
	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte(`{"formatVersion":1,"schema":"och.eval.variance-policy","id":"p","version":"v1","calibration":"uncalibrated","maxNumericSpread":0,"minVerdictStability":0.8,"minEvaluableRepetitions":3}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runReportCLI(t,
		"-set", checkedInSetPath(t), "-artifacts", artifactRoot,
		"-variance-policy", broken, "-variance-scorer", "baseline-v1")
	if code == exitOK {
		t.Fatal("a policy missing a required limit was accepted")
	}
	if !strings.Contains(stderr, "variance") {
		t.Fatalf("stderr = %q, want it to name the cause", stderr)
	}
}

// TestBaselineCommandWritesADeterministicDocument.
//
// This test has to build its own EvalSet, and the reason is worth stating:
// **no checked-in set can exercise this command at all.** All sixteen declare
// repetitionCount: 1, while any valid variance policy requires at least two
// evaluable repetitions — a spread cannot be measured from one sample. So a
// baseline built from any checked-in fixture is correctly refused, and a test
// using one would only ever prove the refusal.
//
// A two-repetition set is therefore written here. That is not a workaround
// for an awkward fixture; it is the first configuration in this repository
// that a variance signal can be read from at all.
func TestBaselineCommandWritesADeterministicDocument(t *testing.T) {
	setPath := twoRepetitionSetPath(t)
	artifactRoot := t.TempDir()
	var runStdout, runStderr bytes.Buffer
	if code := runCLI(context.Background(), []string{
		"run", "-set", setPath, "-artifacts", artifactRoot,
	}, &runStdout, &runStderr); code != exitOK {
		t.Fatalf("run exit = %d; stderr=%s", code, runStderr.String())
	}
	scoreEveryAttempt(t, artifactRoot)

	policyPath := writePolicy(t, nil) // minEvaluableRepetitions 2
	read := func() []byte {
		t.Helper()
		out := filepath.Join(t.TempDir(), "baseline.json")
		var stdout, stderr bytes.Buffer
		code := runCLI(context.Background(), []string{
			"baseline",
			"-set", setPath,
			"-artifacts", artifactRoot,
			"-variance-policy", policyPath,
			"-variance-scorer", varianceTestScorer,
			"-id", "pr-lane",
			"-recorded-at", "2026-09-05T00:00:00Z",
			"-output", out,
		}, &stdout, &stderr)
		if code != exitOK {
			t.Fatalf("baseline exit = %d; stderr=%s", code, stderr.String())
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	first := read()
	second := read()
	if !bytes.Equal(first, second) {
		t.Fatalf("two regenerations produced different documents:\n%s\n%s", first, second)
	}
	baseline, err := eval.DecodeBaseline(first)
	if err != nil {
		t.Fatalf("the written baseline does not decode: %v", err)
	}
	if len(baseline.Cells) == 0 {
		t.Fatal("the baseline recorded no cells")
	}
	for _, cell := range baseline.Cells {
		if cell.EvaluableAttempts < 2 {
			t.Fatalf("cell recorded %d evaluable attempts; a baseline needs a real measurement: %+v",
				cell.EvaluableAttempts, cell)
		}
		if len(cell.AttemptIDs) != cell.Attempts {
			t.Fatalf("cell names %d Attempt ids for %d attempts", len(cell.AttemptIDs), cell.Attempts)
		}
	}
}

// TestBaselineRefusesASingleRepetitionSet states the other half plainly: the
// command cannot be pointed at the sets this repository actually ships.
func TestBaselineRefusesASingleRepetitionSet(t *testing.T) {
	artifactRoot := producedArtifacts(t)
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		"baseline",
		"-set", checkedInSetPath(t),
		"-artifacts", artifactRoot,
		"-variance-policy", writePolicy(t, nil),
		"-variance-scorer", varianceTestScorer,
		"-id", "pr-lane",
	}, &stdout, &stderr)
	if code == exitOK {
		t.Fatal("a baseline was built from single-repetition Attempts")
	}
	if !strings.Contains(stderr.String(), "evaluable") {
		t.Fatalf("stderr = %q, want it to name the shortfall", stderr.String())
	}
}

// twoRepetitionSetPath writes a copy of the checked-in PR set that runs each
// Cell twice.
func twoRepetitionSetPath(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(checkedInSetPath(t))
	if err != nil {
		t.Fatal(err)
	}
	set, err := eval.DecodeEvalSet(data)
	if err != nil {
		t.Fatal(err)
	}
	set.RepetitionCount = 2
	encoded, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	// The copy has to live beside the original: an EvalSet's Scenario,
	// Subject, and Executor references resolve relative to the set document's
	// own directory, so a copy in a temp dir cannot find any of them.
	path := filepath.Join(filepath.Dir(checkedInSetPath(t)), "zz-two-repetitions-test.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

// TestReportPublishesDerivedReliabilityReadings is design §3.4.
//
// These readings need no calibrated threshold, which is why they are the
// part of this report a reader can trust today. They are computed from
// counts the block already carries rather than from any declared limit.
func TestReportPublishesDerivedReliabilityReadings(t *testing.T) {
	mixed := reliabilityOf(eval.CellDistribution{
		EvaluableAttempts: 3,
		Verdicts:          map[eval.ScoreVerdict]int{eval.ScorePass: 2, eval.ScoreFail: 1},
	})
	if mixed == nil {
		t.Fatal("three evaluable repetitions produced no reliability readings")
	}
	if mixed.EvaluablePasses != 2 {
		t.Fatalf("EvaluablePasses = %d, want the raw count 2", mixed.EvaluablePasses)
	}
	if !mixed.AtLeastOnePassed {
		t.Fatal("two passes of three did not register as at least one passing")
	}
	if mixed.AllPassed {
		t.Fatal("two passes of three were reported as all passing")
	}

	unanimous := reliabilityOf(eval.CellDistribution{
		EvaluableAttempts: 3,
		Verdicts:          map[eval.ScoreVerdict]int{eval.ScorePass: 3},
	})
	if !unanimous.AllPassed {
		t.Fatalf("three passes of three were not reported as all passing: %+v", unanimous)
	}
}

// TestDerivedReadingsAreAbsentBelowTwoEvaluableRepetitions. With one
// evaluable repetition "all passed" and "at least one passed" are the same
// statement, and publishing them would dress a single sample up as
// agreement.
func TestDerivedReadingsAreAbsentBelowTwoEvaluableRepetitions(t *testing.T) {
	for _, evaluable := range []int{0, 1} {
		got := reliabilityOf(eval.CellDistribution{
			EvaluableAttempts: evaluable,
			Verdicts:          map[eval.ScoreVerdict]int{eval.ScorePass: evaluable},
		})
		if got != nil {
			t.Fatalf("%d evaluable repetitions produced readings %+v", evaluable, got)
		}
	}
}

// TestDerivedReadingsAreNotNamedPassAtK is a naming guard.
//
// A Cell-level "at least one of k passed" is at_least(1). Chen et al.'s
// pass@k is an unbiased dataset-level estimator, and inspect_ai's pass_at(k)
// is that estimator; borrowing either name for a per-Cell count would make
// the first public comparison dishonest. The published field names are part
// of the contract, so the refusal is a test rather than a comment.
func TestDerivedReadingsAreNotNamedPassAtK(t *testing.T) {
	encoded, err := json.Marshal(reportVarianceCell{
		Reliability: reliabilityOf(eval.CellDistribution{
			EvaluableAttempts: 2,
			Verdicts:          map[eval.ScoreVerdict]int{eval.ScorePass: 2},
		}),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rendered := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"passat", "pass@", "pass_at", "passk", "pass^"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("the report block uses %q; a per-Cell count must not borrow a dataset-level estimator's name: %s",
				forbidden, encoded)
		}
	}
}

// TestReportCarriesBothReliabilityFieldsSeparately: the split of a
// structural fact from an uncalibrated threshold judgement survives
// serialization, or it does not exist for anyone reading the artifacts.
func TestReportCarriesBothReliabilityFieldsSeparately(t *testing.T) {
	encoded, err := json.Marshal(reportVarianceCell{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, required := range []string{"evaluableEnough", "exceedsDeclaredLimits"} {
		if _, ok := decoded[required]; !ok {
			t.Fatalf("the published cell omits %q: %s", required, encoded)
		}
	}
	for key := range decoded {
		if strings.Contains(strings.ToLower(key), "trustworthy") {
			t.Fatalf("the published cell grew a merged reliability field %q: %s", key, encoded)
		}
	}
}
