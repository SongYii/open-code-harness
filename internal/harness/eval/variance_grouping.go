package eval

import "fmt"

// AttemptScore pairs one Attempt with one of its published Scores.
//
// It exists because neither document alone can be grouped: the Attempt
// carries the identity digests and the repetition index, and the Score
// carries the verdict and number being measured.
type AttemptScore struct {
	Attempt Attempt
	Score   Score
}

// GroupByCell groups repetitions by the Cell they belong to.
//
// The key is the three identity digests, never the ids. That is the design's
// own rule, and both failure modes it prevents are real: a document renamed
// but otherwise identical would split one Cell in two, and a document edited
// while keeping its name would silently pool two different measurements into
// one and compute a spread between them.
func GroupByCell(pairs []AttemptScore) (map[CellIdentity][]CellRepetition, error) {
	cells := make(map[CellIdentity][]CellRepetition)
	for _, pair := range pairs {
		attempt := pair.Attempt
		if attempt.ScenarioDigest == "" || attempt.SubjectDigest == "" || attempt.ExecutorDigest == "" {
			return nil, fmt.Errorf("%w: attempt %q carries no identity digests; it cannot be placed in a Cell",
				errInvalidDocument, attempt.ID)
		}
		identity := CellIdentity{
			ScenarioDigest: attempt.ScenarioDigest,
			SubjectDigest:  attempt.SubjectDigest,
			ExecutorDigest: attempt.ExecutorDigest,
		}
		cells[identity] = append(cells[identity], CellRepetition{Attempt: attempt, Score: pair.Score})
	}
	return cells, nil
}

// GroupByCellForScorer groups the repetitions scored by one named scorer.
//
// Selecting a scorer is not optional bookkeeping. An Attempt can carry a
// deterministic verifier's Score and a live judge's Score, which answer
// different questions; pooling them would compute a spread across two
// different measurements and call it variance.
//
// Two Scores from the same scorer for the same repetition are refused rather
// than resolved. Scores are append-only and a regrade appends rather than
// replaces, so this is a real situation — and silently taking one would make
// the published distribution depend on the order files happened to be read
// in, which is precisely the kind of irreproducibility this mechanism exists
// to expose in other people's measurements.
func GroupByCellForScorer(pairs []AttemptScore, scorerID string) (map[CellIdentity][]CellRepetition, error) {
	selected := make([]AttemptScore, 0, len(pairs))
	seen := make(map[AttemptID]struct{}, len(pairs))
	for _, pair := range pairs {
		if pair.Score.ScorerID != scorerID {
			continue
		}
		if _, duplicate := seen[pair.Attempt.ID]; duplicate {
			return nil, fmt.Errorf(
				"%w: attempt %q carries more than one %q Score; which one a distribution should use is not this function's to guess",
				errInvalidDocument, pair.Attempt.ID, scorerID)
		}
		seen[pair.Attempt.ID] = struct{}{}
		selected = append(selected, pair)
	}
	return GroupByCell(selected)
}
