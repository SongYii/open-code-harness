package eval

import (
	"reflect"
	"testing"
)

func validScore(t *testing.T, attemptID AttemptID) Score {
	t.Helper()
	id, err := NewScoreID()
	if err != nil {
		t.Fatalf("NewScoreID: %v", err)
	}
	numeric := 0.9
	return Score{
		FormatVersion:  FormatVersion,
		Schema:         SchemaScore,
		ID:             id,
		AttemptID:      attemptID,
		ManifestDigest: mustDigest(t, 6),
		OutcomeDigest:  mustDigest(t, 7),
		ScorerID:       "tool-workspace-verifier",
		ScorerVersion:  "1",
		Lane:           LaneFixture,
		Verdict:        ScorePass,
		NumericScore:   &numeric,
		Criteria: []CriterionResult{
			{ID: "file-written", Status: ScorePass},
		},
		EvidenceReferences: []string{"transcript.jsonl"},
		Rationale:          "workspace file matched the expected content",
	}
}

func TestDecodeScoreRoundTrip(t *testing.T) {
	want := validScore(t, mustAttemptID(t))
	got, err := DecodeScore(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeScore: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("DecodeScore round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestScoreValidateRejectsUnknownVerdict(t *testing.T) {
	score := validScore(t, mustAttemptID(t))
	score.Verdict = "maybe"
	if err := score.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown verdict")
	}
}

func TestScoreValidateRejectsUnknownLane(t *testing.T) {
	score := validScore(t, mustAttemptID(t))
	score.Lane = "staging"
	if err := score.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown lane")
	}
}

func TestScoreValidateRejectsBadManifestDigest(t *testing.T) {
	score := validScore(t, mustAttemptID(t))
	score.ManifestDigest = "not-a-digest"
	if err := score.Validate(); err == nil {
		t.Fatal("Validate() accepted a malformed manifestDigest")
	}
}

func TestScoreValidateRejectsCriterionWithoutID(t *testing.T) {
	score := validScore(t, mustAttemptID(t))
	score.Criteria = []CriterionResult{{Status: ScorePass}}
	if err := score.Validate(); err == nil {
		t.Fatal("Validate() accepted a criterion without an id")
	}
}

func TestScoreValidateAllowsIndeterminateWithMissingEvidence(t *testing.T) {
	// Design §20/§21: missing or contradictory evidence must not silently
	// become a passing score; indeterminate with a recorded reason is the
	// honest shape, and Validate() must not itself refuse that shape.
	score := validScore(t, mustAttemptID(t))
	score.Verdict = ScoreIndeterminate
	score.NumericScore = nil
	score.MissingEvidence = []string{"workspace/output.txt"}
	if err := score.Validate(); err != nil {
		t.Fatalf("Validate() rejected a valid indeterminate score: %v", err)
	}
}
