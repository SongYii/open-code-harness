package eval

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validBaseline() Baseline {
	return Baseline{
		FormatVersion: FormatVersion,
		Schema:        SchemaBaseline,
		ID:            "context-quality-baseline",
		RecordedAt:    time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Cells: []BaselineCell{{
			ScenarioDigest:    "sha256:scenario",
			SubjectDigest:     "sha256:subject",
			ExecutorDigest:    "sha256:executor",
			Attempts:          5,
			EvaluableAttempts: 5,
			NumericScores:     []float64{0.70, 0.72, 0.71, 0.73, 0.70},
			NumericSpread:     0.03,
			VerdictStability:  1,
			AttemptIDs:        []AttemptID{"a1", "a2", "a3", "a4", "a5"},
		}},
	}
}

func currentDistribution(t *testing.T) CellDistribution {
	t.Helper()
	return mustDistribution(t, []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScorePass, score(0.72)),
		repetition(2, ScorePass, score(0.71)),
	}, validVariancePolicy())
}

func TestDecodeBaselineRoundTripAndDigest(t *testing.T) {
	want := validBaseline()
	got, err := DecodeBaseline(mustMarshal(t, want))
	if err != nil {
		t.Fatalf("DecodeBaseline: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the document")
	}
	first, err := BaselineDigest(want)
	if err != nil {
		t.Fatalf("BaselineDigest: %v", err)
	}
	second, err := BaselineDigest(got)
	if err != nil || first != second {
		t.Fatalf("digest unstable: %q %q %v", first, second, err)
	}
}

// TestBaselineComparesOnlyToMatchingIdentityDigests: a baseline recorded for
// one Cell says nothing about another.
func TestBaselineComparesOnlyToMatchingIdentityDigests(t *testing.T) {
	baseline := validBaseline()
	identity := CellIdentity{
		ScenarioDigest: "sha256:scenario",
		SubjectDigest:  "sha256:subject",
		ExecutorDigest: "sha256:executor",
	}
	comparison, err := MatchBaseline(baseline, identity, currentDistribution(t), time.Now(), 0)
	if err != nil {
		t.Fatalf("MatchBaseline: %v", err)
	}
	if !comparison.Matched {
		t.Fatalf("an identical Cell did not match: %+v", comparison)
	}
}

// TestABaselineMismatchIsReportedNotSilentlyTreatedAsAbsentOrPassing is the
// fact a reviewer needs: the usual cause is that the Scenario or Subject was
// edited, and an unmatched baseline that reads as "no baseline" hides it.
func TestABaselineMismatchIsReportedNotSilentlyTreatedAsAbsentOrPassing(t *testing.T) {
	baseline := validBaseline()
	identity := CellIdentity{
		ScenarioDigest: "sha256:scenario-edited",
		SubjectDigest:  "sha256:subject",
		ExecutorDigest: "sha256:executor",
	}
	comparison, err := MatchBaseline(baseline, identity, currentDistribution(t), time.Now(), 0)
	if err != nil {
		t.Fatalf("MatchBaseline: %v", err)
	}
	if comparison.Matched {
		t.Fatal("a Cell with a different scenario digest matched the baseline")
	}
	if comparison.UnmatchedReason == "" {
		t.Fatal("an unmatched baseline carries no stated reason")
	}
	if comparison.Regressed {
		t.Fatal("an unmatched baseline must not also claim a regression it cannot have measured")
	}
}

// TestAStaleBaselineIsDisclosedAndStillShown: staleness is disclosed, never
// used to silently drop the comparison.
func TestAStaleBaselineIsDisclosedAndStillShown(t *testing.T) {
	baseline := validBaseline()
	identity := CellIdentity{
		ScenarioDigest: "sha256:scenario",
		SubjectDigest:  "sha256:subject",
		ExecutorDigest: "sha256:executor",
	}
	now := baseline.RecordedAt.Add(90 * 24 * time.Hour)
	comparison, err := MatchBaseline(baseline, identity, currentDistribution(t), now, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("MatchBaseline: %v", err)
	}
	if !comparison.Matched {
		t.Fatal("a stale baseline stopped matching; staleness is disclosed, not disqualifying")
	}
	if !comparison.Stale {
		t.Fatal("a 90-day-old baseline under a 30-day bound was not marked stale")
	}
}

// TestBaselineRegenerationIsDeterministic. A baseline nobody can reproduce is
// an assertion, not evidence.
func TestBaselineRegenerationIsDeterministic(t *testing.T) {
	reps := []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScorePass, score(0.72)),
		repetition(2, ScorePass, score(0.71)),
	}
	recordedAt := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

	first, err := BuildBaseline("b", recordedAt, map[CellIdentity][]CellRepetition{
		{ScenarioDigest: "sha256:scenario", SubjectDigest: "sha256:subject", ExecutorDigest: "sha256:executor"}: reps,
	}, validVariancePolicy())
	if err != nil {
		t.Fatalf("BuildBaseline: %v", err)
	}
	second, err := BuildBaseline("b", recordedAt, map[CellIdentity][]CellRepetition{
		{ScenarioDigest: "sha256:scenario", SubjectDigest: "sha256:subject", ExecutorDigest: "sha256:executor"}: reps,
	}, validVariancePolicy())
	if err != nil {
		t.Fatalf("BuildBaseline: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two builds from the same inputs produced different documents")
	}
	firstDigest, _ := BaselineDigest(first)
	secondDigest, _ := BaselineDigest(second)
	if firstDigest != secondDigest {
		t.Fatalf("digests differ: %q %q", firstDigest, secondDigest)
	}
}

// TestBaselineCellsAreOrderedDeterministically closes the usual source of
// non-reproducibility: Go map iteration.
func TestBaselineCellsAreOrderedDeterministically(t *testing.T) {
	reps := []CellRepetition{repetition(0, ScorePass, score(0.7)), repetition(1, ScorePass, score(0.7))}
	cells := map[CellIdentity][]CellRepetition{}
	for _, name := range []string{"c", "a", "d", "b"} {
		cells[CellIdentity{
			ScenarioDigest: Digest("sha256:" + name),
			SubjectDigest:  "sha256:subject",
			ExecutorDigest: "sha256:executor",
		}] = withScenario(reps, Digest("sha256:"+name))
	}
	policy := validVariancePolicy()
	policy.MinEvaluableRepetitions = 2

	var previous []Digest
	for i := 0; i < 8; i++ {
		built, err := BuildBaseline("b", time.Unix(0, 0).UTC(), cells, policy)
		if err != nil {
			t.Fatalf("BuildBaseline: %v", err)
		}
		order := make([]Digest, 0, len(built.Cells))
		for _, cell := range built.Cells {
			order = append(order, cell.ScenarioDigest)
		}
		if previous != nil && !reflect.DeepEqual(order, previous) {
			t.Fatalf("cell order changed between builds: %v then %v", previous, order)
		}
		previous = order
	}
}

func withScenario(reps []CellRepetition, digest Digest) []CellRepetition {
	out := make([]CellRepetition, len(reps))
	copy(out, reps)
	for i := range out {
		out[i].Attempt.ScenarioDigest = digest
	}
	return out
}

func TestBaselineRecordsTheAttemptIdsItWasDerivedFrom(t *testing.T) {
	reps := []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScorePass, score(0.72)),
	}
	reps[0].Attempt.ID = "attempt-one"
	reps[1].Attempt.ID = "attempt-two"
	policy := validVariancePolicy()
	policy.MinEvaluableRepetitions = 2

	built, err := BuildBaseline("b", time.Unix(0, 0).UTC(), map[CellIdentity][]CellRepetition{
		{ScenarioDigest: "sha256:scenario", SubjectDigest: "sha256:subject", ExecutorDigest: "sha256:executor"}: reps,
	}, policy)
	if err != nil {
		t.Fatalf("BuildBaseline: %v", err)
	}
	if len(built.Cells) != 1 {
		t.Fatalf("built %d cells, want 1", len(built.Cells))
	}
	ids := built.Cells[0].AttemptIDs
	if len(ids) != 2 || ids[0] != "attempt-one" || ids[1] != "attempt-two" {
		t.Fatalf("AttemptIDs = %v; a baseline must name what it was derived from", ids)
	}
}

// TestBaselineRefusesToRecordACellNobodyCouldJudge: a baseline is what later
// runs are compared against, so pinning a Cell that was never really measured
// would poison every comparison that follows. This is the structural half of
// design §3, and it refuses unconditionally.
func TestBaselineRefusesToRecordACellNobodyCouldJudge(t *testing.T) {
	reps := []CellRepetition{
		repetition(0, ScorePass, score(0.70)),
		repetition(1, ScoreIndeterminate, nil),
		repetition(2, ScoreIndeterminate, nil), // one evaluable, floor is three
	}
	_, err := BuildBaseline("b", time.Unix(0, 0).UTC(), map[CellIdentity][]CellRepetition{
		{ScenarioDigest: "sha256:scenario", SubjectDigest: "sha256:subject", ExecutorDigest: "sha256:executor"}: reps,
	}, validVariancePolicy())
	if !errors.Is(err, errInvalidDocument) {
		t.Fatalf("BuildBaseline() = %v, want an unmeasured Cell to be refused", err)
	}
	if err != nil && !strings.Contains(err.Error(), "evaluable") {
		t.Fatalf("err = %v, want it to name the cause", err)
	}
}

// TestBaselineRecordsAWideCellUnderAnUncalibratedLimit is design §3.2 reaching
// the baseline document.
//
// A spread of 0.80 against a declared 0.20 is a real measurement compared
// against a number nobody has calibrated. Refusing to record it would let an
// invented limit decide what history exists, and the recorded distribution
// carries its own spread, so a later reader sees exactly how wide this
// baseline was. Once the limits are earned, the refusal below applies.
func TestBaselineRecordsAWideCellUnderAnUncalibratedLimit(t *testing.T) {
	reps := []CellRepetition{
		repetition(0, ScorePass, score(0.10)),
		repetition(1, ScorePass, score(0.90)), // spread 0.80, far over the limit
		repetition(2, ScorePass, score(0.50)),
	}
	cells := map[CellIdentity][]CellRepetition{
		{ScenarioDigest: "sha256:scenario", SubjectDigest: "sha256:subject", ExecutorDigest: "sha256:executor"}: reps,
	}

	baseline, err := BuildBaseline("b", time.Unix(0, 0).UTC(), cells, validVariancePolicy())
	if err != nil {
		t.Fatalf("BuildBaseline() = %v; an uncalibrated limit must not decide what history exists", err)
	}
	if len(baseline.Cells) != 1 {
		t.Fatalf("Cells = %d, want the wide Cell recorded", len(baseline.Cells))
	}
	// The record carries the spread itself, which is what lets a later reader
	// see how wide this baseline was rather than taking its existence as a
	// claim that it was narrow.
	if math.Abs(baseline.Cells[0].NumericSpread-0.80) > 1e-9 {
		t.Fatalf("NumericSpread = %v, want the recorded Cell to carry its own 0.80 spread",
			baseline.Cells[0].NumericSpread)
	}

	calibrated := validVariancePolicy()
	calibrated.Calibration = CalibrationCalibrated
	calibrated.CalibratedFrom = "run-2026-09-05-01"
	if _, err := BuildBaseline("b", time.Unix(0, 0).UTC(), cells, calibrated); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("BuildBaseline() = %v, want a calibrated breach to be refused", err)
	}
}

func TestDecodeBaselineRejectsUnknownFieldsAndWrongSchema(t *testing.T) {
	wrong := validBaseline()
	wrong.Schema = SchemaVariancePolicy
	if _, err := DecodeBaseline(mustMarshal(t, wrong)); err == nil {
		t.Fatal("a document with another schema was accepted")
	}
	if _, err := DecodeBaseline([]byte(`{"formatVersion":1,"schema":"och.eval.baseline","id":"b","recordedAt":"2026-09-01T00:00:00Z","cells":[],"extra":1}`)); err == nil {
		t.Fatal("an unknown field was accepted")
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
