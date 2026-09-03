package eval

import (
	"context"
	"fmt"
)

// LiveConsent carries the two independent confirmations a live judge run
// requires: the caller's own explicit flag and the exact literal it read
// from its own trusted environment variable. This type never reads an
// environment variable itself — the caller does, and passes what it found.
type LiveConsent struct {
	Flag        bool
	Environment string
}

// JudgeAttemptResult is one live judge invocation's published Score plus
// the deterministic verdict that decided whether the judge ran at all.
// PrerequisiteVerdict is reported separately because a Score that reads
// Indeterminate for "the deterministic invariants did not hold" and one
// that reads Indeterminate for "the judge could not answer" are different
// facts, and an operator needs to tell them apart.
type JudgeAttemptResult struct {
	Score               Score
	PrerequisiteVerdict ScoreVerdict
}

// EvaluateJudgeAttempt runs one live quality judgement against
// directories' already-committed evidence and appends the resulting Score.
//
// The order of the gates below is the contract, not an implementation
// detail. Frozen evidence and consent are both checked before caller is
// ever invoked, so a run that is not entitled to happen cannot reach a
// provider — and since caller is what holds any credential, it cannot
// reach a credential either. Deterministic verifiers then run to
// completion, and a non-Pass prerequisite publishes an Indeterminate
// Score without calling the model: quality judging answers "was this good",
// which is not a question worth paying for when the run did not even meet
// its own invariants.
//
// It never invokes the Subject, reopens its session, or touches Outcome.
// The Score it publishes is appended, never substituted for a
// deterministic Score.
func EvaluateJudgeAttempt(ctx context.Context, directories AttemptRootDirectories, suppliedConfig JudgeConfig, consent LiveConsent, caller JudgeCaller, priceTable *PriceTable) (JudgeAttemptResult, error) {
	if ctx == nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: context is required")
	}
	if caller == nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: caller is required")
	}
	reader, err := NewArtifactReader(directories)
	if err != nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: %w", err)
	}
	publishedAttempt, err := ReadAttempt(directories.Root)
	if err != nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: %w", err)
	}

	// Frozen evidence first: an Attempt that cannot prove which judge
	// configuration it was entitled to use is refused outright.
	documents, lane, err := readJudgeEvidenceDocuments(reader, publishedAttempt)
	if err != nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: frozen evidence: %w", err)
	}
	suppliedDigest, err := JudgeConfigDigest(suppliedConfig)
	if err != nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: %w", err)
	}
	frozenDigest, err := JudgeConfigDigest(*documents.JudgeConfig)
	if err != nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: %w", err)
	}
	if suppliedDigest != frozenDigest {
		return JudgeAttemptResult{}, fmt.Errorf(
			"eval: evaluate judge attempt: supplied judge config digest %q disagrees with this Attempt's own frozen evidence %q",
			suppliedDigest, frozenDigest)
	}

	// Consent second, still before any provider call.
	if err := RequireLiveConsent(lane, consent.Flag, consent.Environment); err != nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: %w", err)
	}

	// Deterministic invariants third. Every verifier the Scenario declares
	// runs; the judge is a supplement to them, never a substitute.
	prerequisiteScorer := Scorer{
		ID:          string(documents.Scenario.ID) + "-deterministic",
		Version:     "v1",
		VerifierIDs: documents.Scenario.DeterministicVerifierIDs,
	}
	prerequisiteVerdict, prerequisiteCriteria, err := RunScorer(reader, documents.Scenario, prerequisiteScorer)
	if err != nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: deterministic prerequisites: %w", err)
	}

	outcome := JudgeOutcome{
		Verdict:  ScoreIndeterminate,
		Criteria: prerequisiteCriteria,
		Rationale: fmt.Sprintf("deterministic prerequisites returned %q; quality judging was not attempted",
			prerequisiteVerdict),
	}
	if prerequisiteVerdict == ScorePass {
		outcome, err = RunJudge(ctx, reader, suppliedConfig, caller)
		if err != nil {
			return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: %w", err)
		}
	}

	score, err := buildJudgeScore(reader, suppliedConfig, suppliedDigest, outcome, priceTable)
	if err != nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: %w", err)
	}
	if err := PublishScore(directories.Root, score); err != nil {
		return JudgeAttemptResult{}, fmt.Errorf("eval: evaluate judge attempt: %w", err)
	}
	return JudgeAttemptResult{Score: score, PrerequisiteVerdict: prerequisiteVerdict}, nil
}

// buildJudgeScore assembles the `och.eval.score` document one judge run
// publishes. Its scorer identity comes from the frozen JudgeConfig rather
// than from anything the caller chose at invocation time, so a reader can
// resolve exactly which configuration produced this verdict.
func buildJudgeScore(reader *ArtifactReader, config JudgeConfig, configDigest Digest, outcome JudgeOutcome, priceTable *PriceTable) (Score, error) {
	manifestDigest, err := EvidenceManifestDigest(reader.Manifest())
	if err != nil {
		return Score{}, fmt.Errorf("digest manifest: %w", err)
	}
	outcomeDigest, err := OutcomeDigest(reader.Outcome())
	if err != nil {
		return Score{}, fmt.Errorf("digest outcome: %w", err)
	}
	scoreID, err := NewScoreID()
	if err != nil {
		return Score{}, err
	}

	usage := outcome.Usage
	status, microunits, currency := ResolveScorerCost(priceTable, config.Provider.ModelID,
		uint64(max(usage.InputTokens, 0)), uint64(max(usage.OutputTokens, 0)), 0)
	usage.CostStatus = status
	usage.CostMicrounits = microunits
	usage.CostCurrency = currency

	score := Score{
		FormatVersion:  FormatVersion,
		Schema:         SchemaScore,
		ID:             scoreID,
		AttemptID:      reader.Outcome().AttemptID,
		ManifestDigest: manifestDigest,
		OutcomeDigest:  outcomeDigest,

		ScorerID:           config.ID,
		ScorerVersion:      config.Version,
		ScorerConfigDigest: configDigest,
		Lane:               LaneLive,

		Verdict:      outcome.Verdict,
		NumericScore: outcome.NumericScore,
		Criteria:     outcome.Criteria,

		EvidenceReferences:    outcome.EvidenceReferences,
		MissingEvidence:       outcome.MissingEvidence,
		ContradictoryEvidence: outcome.ContradictoryEvidence,

		Rationale:   outcome.Rationale,
		ScorerUsage: &usage,
	}
	if err := score.Validate(); err != nil {
		return Score{}, err
	}
	return score, nil
}
