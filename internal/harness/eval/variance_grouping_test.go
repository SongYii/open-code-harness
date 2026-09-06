package eval

import (
	"errors"
	"testing"
)

func attemptFor(id AttemptID, scenario, subject, executor Digest, index int) Attempt {
	return Attempt{
		ID:              id,
		ScenarioDigest:  scenario,
		SubjectDigest:   subject,
		ExecutorDigest:  executor,
		RepetitionIndex: index,
	}
}

// TestGroupByCellKeysOnIdentityDigestsNotIds is the design's own rule made
// executable: repetitions are comparable because their identity inputs are
// frozen and digested. Keying on ids instead would let a renamed-but-
// identical document split a Cell, and an edited-but-same-named one silently
// join two different measurements into one.
func TestGroupByCellKeysOnIdentityDigestsNotIds(t *testing.T) {
	pairs := []AttemptScore{
		{Attempt: attemptFor("a1", "sha256:s", "sha256:u", "sha256:e", 0), Score: Score{Verdict: ScorePass}},
		{Attempt: attemptFor("a2", "sha256:s", "sha256:u", "sha256:e", 1), Score: Score{Verdict: ScorePass}},
		{Attempt: attemptFor("a3", "sha256:OTHER", "sha256:u", "sha256:e", 0), Score: Score{Verdict: ScorePass}},
	}
	// Deliberately different ScenarioIDs on the same digest, and the same
	// ScenarioID on different digests, so a grouping keyed on ids would
	// produce a visibly different answer.
	pairs[0].Attempt.ScenarioID = "renamed"
	pairs[1].Attempt.ScenarioID = "original"
	pairs[2].Attempt.ScenarioID = "original"

	cells, err := GroupByCell(pairs)
	if err != nil {
		t.Fatalf("GroupByCell: %v", err)
	}
	if len(cells) != 2 {
		t.Fatalf("grouped into %d cells, want 2", len(cells))
	}
	first := CellIdentity{ScenarioDigest: "sha256:s", SubjectDigest: "sha256:u", ExecutorDigest: "sha256:e"}
	if len(cells[first]) != 2 {
		t.Fatalf("the two repetitions sharing a digest did not group together: %v", cells[first])
	}
}

// TestGroupByCellKeepsOnlyTheScoresOfOneScorer: an Attempt can carry a
// deterministic verifier Score and a live judge Score, and pooling them would
// compute a spread between two different questions.
func TestGroupByCellSelectsOneScorer(t *testing.T) {
	pairs := []AttemptScore{
		{Attempt: attemptFor("a1", "sha256:s", "sha256:u", "sha256:e", 0),
			Score: Score{ScorerID: "quality-judge", Verdict: ScorePass}},
		{Attempt: attemptFor("a1", "sha256:s", "sha256:u", "sha256:e", 0),
			Score: Score{ScorerID: "deterministic", Verdict: ScoreFail}},
	}
	cells, err := GroupByCellForScorer(pairs, "quality-judge")
	if err != nil {
		t.Fatalf("GroupByCellForScorer: %v", err)
	}
	identity := CellIdentity{ScenarioDigest: "sha256:s", SubjectDigest: "sha256:u", ExecutorDigest: "sha256:e"}
	if len(cells[identity]) != 1 {
		t.Fatalf("selected %d repetitions, want only the named scorer's", len(cells[identity]))
	}
	if cells[identity][0].Score.Verdict != ScorePass {
		t.Fatalf("selected the wrong scorer's Score: %+v", cells[identity][0].Score)
	}
}

// TestGroupByCellRefusesTwoScoresForOneRepetitionFromOneScorer: Scores are
// append-only and a regrade appends rather than replaces, so one repetition
// can legitimately carry several Scores from one scorer. Silently taking one
// would make the distribution depend on file order.
func TestGroupByCellRefusesTwoScoresForOneRepetitionFromOneScorer(t *testing.T) {
	pairs := []AttemptScore{
		{Attempt: attemptFor("a1", "sha256:s", "sha256:u", "sha256:e", 0),
			Score: Score{ScorerID: "judge", Verdict: ScorePass}},
		{Attempt: attemptFor("a1", "sha256:s", "sha256:u", "sha256:e", 0),
			Score: Score{ScorerID: "judge", Verdict: ScoreFail}},
	}
	_, err := GroupByCellForScorer(pairs, "judge")
	if !errors.Is(err, errInvalidDocument) {
		t.Fatalf("err = %v, want an ambiguous repetition to be refused rather than resolved by arrival order", err)
	}
}

func TestGroupByCellRefusesAnAttemptWithoutIdentityDigests(t *testing.T) {
	_, err := GroupByCell([]AttemptScore{
		{Attempt: Attempt{ID: "a1", RepetitionIndex: 0}, Score: Score{Verdict: ScorePass}},
	})
	if !errors.Is(err, errInvalidDocument) {
		t.Fatalf("err = %v, want an Attempt with no identity digests to be refused", err)
	}
}

func TestGroupByCellOnNoInputIsEmptyNotAnError(t *testing.T) {
	cells, err := GroupByCell(nil)
	if err != nil {
		t.Fatalf("GroupByCell(nil) = %v, want no error", err)
	}
	if len(cells) != 0 {
		t.Fatalf("cells = %v, want empty", cells)
	}
}
