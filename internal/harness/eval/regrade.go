package eval

import "fmt"

// RegradeAttempt runs scorer against directories' already-committed
// evidence (design §20) and publishes a new Score. It never reads or
// replaces any earlier Score — PublishScore names every Score by its own
// freshly generated ID, so a regrade always appends rather than
// overwrites — and it never invokes the Subject or constructs an Executor,
// Provider, or Service. Scenario and lane come only from manifest-constrained
// frozen evidence, never from caller-supplied execution configuration.
func RegradeAttempt(directories AttemptRootDirectories, scorer Scorer) (Score, error) {
	reader, err := NewArtifactReader(directories)
	if err != nil {
		return Score{}, fmt.Errorf("eval: regrade attempt: %w", err)
	}

	publishedAttempt, err := ReadAttempt(directories.Root)
	if err != nil {
		return Score{}, fmt.Errorf("eval: regrade attempt: %w", err)
	}
	documents, lane, err := readEvidenceDocuments(reader, publishedAttempt)
	if err != nil {
		return Score{}, fmt.Errorf("eval: regrade attempt: frozen evidence: %w", err)
	}
	verdict, criteria, err := RunScorer(reader, documents.Scenario, scorer)
	if err != nil {
		return Score{}, fmt.Errorf("eval: regrade attempt: %w", err)
	}

	manifestDigest, err := EvidenceManifestDigest(reader.Manifest())
	if err != nil {
		return Score{}, fmt.Errorf("eval: regrade attempt: digest manifest: %w", err)
	}
	outcomeDigest, err := OutcomeDigest(reader.Outcome())
	if err != nil {
		return Score{}, fmt.Errorf("eval: regrade attempt: digest outcome: %w", err)
	}
	scoreID, err := NewScoreID()
	if err != nil {
		return Score{}, fmt.Errorf("eval: regrade attempt: %w", err)
	}

	score := Score{
		FormatVersion:  FormatVersion,
		Schema:         SchemaScore,
		ID:             scoreID,
		AttemptID:      reader.Outcome().AttemptID,
		ManifestDigest: manifestDigest,
		OutcomeDigest:  outcomeDigest,
		ScorerID:       scorer.ID,
		ScorerVersion:  scorer.Version,
		Lane:           lane,
		Verdict:        verdict,
		Criteria:       criteria,
	}
	if err := PublishScore(directories.Root, score); err != nil {
		return Score{}, fmt.Errorf("eval: regrade attempt: %w", err)
	}
	return score, nil
}
