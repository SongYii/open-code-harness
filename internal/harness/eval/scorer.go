package eval

import "fmt"

// Scorer is one deterministic scorer configuration (design §20): a fixed,
// ordered list of compiled Verifier IDs to run. Every one of them must
// already be declared in the Scenario's own DeterministicVerifierIDs — a
// Scorer naming a verifier the Scenario never declared is a configuration
// error RunScorer refuses, not something this package silently ignores or
// runs anyway (design §7's verifier IDs are part of the frozen Scenario
// contract).
type Scorer struct {
	ID          string
	Version     string
	VerifierIDs []string
}

// RunScorer runs scorer's verifiers against reader's committed evidence
// and scenario's own declarations, and returns the overall verdict and
// per-criterion results a Score document needs. It never touches the
// Subject, an Executor, a Provider, or anything beyond reader's own
// bounded evidence surface.
//
// The overall verdict follows design §20's own stated priority:
// Indeterminate dominates whenever any criterion is Indeterminate (an
// incomplete or unreadable picture is never silently treated as a clean
// pass or a definite fail); otherwise any Fail makes the whole verdict
// Fail; otherwise Pass.
func RunScorer(reader *ArtifactReader, scenario Scenario, scorer Scorer) (ScoreVerdict, []CriterionResult, error) {
	if reader == nil {
		return "", nil, fmt.Errorf("eval: run scorer: reader is required")
	}
	if len(scorer.VerifierIDs) == 0 {
		return "", nil, fmt.Errorf("eval: run scorer: scorer %q declares no verifiers", scorer.ID)
	}
	declared := make(map[string]bool, len(scenario.DeterministicVerifierIDs))
	for _, id := range scenario.DeterministicVerifierIDs {
		declared[id] = true
	}

	criteria := make([]CriterionResult, 0, len(scorer.VerifierIDs))
	for _, id := range scorer.VerifierIDs {
		if !declared[id] {
			return "", nil, fmt.Errorf("eval: run scorer: verifier %q is not declared by scenario %q", id, scenario.ID)
		}
		verifier, ok := LookupVerifier(id)
		if !ok {
			return "", nil, fmt.Errorf("eval: run scorer: %q: unknown verifier id", id)
		}
		criteria = append(criteria, verifier(reader, scenario))
	}
	return aggregateVerdict(criteria), criteria, nil
}

func aggregateVerdict(criteria []CriterionResult) ScoreVerdict {
	sawFail := false
	for _, criterion := range criteria {
		if criterion.Status == ScoreIndeterminate {
			return ScoreIndeterminate
		}
		if criterion.Status == ScoreFail {
			sawFail = true
		}
	}
	if sawFail {
		return ScoreFail
	}
	return ScorePass
}
