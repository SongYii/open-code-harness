package main

import "github.com/SongYii/open-code-harness/internal/harness/eval"

// scorerCatalog is this command's own compiled table of Scorer
// configurations, keyed by the -scorer flag's ID — the same "code, never
// a data file" discipline internal/harness/eval's own verifier catalog
// follows (implementation plan Task 9).
var scorerCatalog = map[string]eval.Scorer{
	// context-core-v1 scores the Context mechanism Scenarios that run on the
	// core budget profile. Each Scenario declares exactly the verifiers it
	// exercises; RunScorer refuses any this Scenario did not declare, so one
	// scorer can serve the whole profile without silently over-claiming.
	"context-core-v1": {
		ID:      "context-core-v1",
		Version: "v1",
		VerifierIDs: []string{
			eval.VerifierContextPreTurnSummary,
			eval.VerifierContextBudgetBounds,
			eval.VerifierContextProjection,
			"outcome-not-infra-failed-v1",
		},
	},
	"baseline-v1": {
		ID:      "baseline-v1",
		Version: "v1",
		VerifierIDs: []string{
			"manifest-complete-v1",
			"transcript-present-v1",
			"audit-included-v1",
			"outcome-not-infra-failed-v1",
		},
	},
	"tool-approval-failure-v1": {
		ID:      "tool-approval-failure-v1",
		Version: "v1",
		VerifierIDs: []string{
			"tool-approval-failure-observed-v1",
			"outcome-not-infra-failed-v1",
		},
	},
	"context-compaction-v1": {
		ID:      "context-compaction-v1",
		Version: "v1",
		VerifierIDs: []string{
			"context-compaction-observed-v1",
			"outcome-not-infra-failed-v1",
		},
	},
	"tool-read-success-v1": {
		ID:      "tool-read-success-v1",
		Version: "v1",
		VerifierIDs: []string{
			"read-file-completed-v1",
			"outcome-not-infra-failed-v1",
		},
	},
	"tool-exec-redaction-v1": {
		ID:      "tool-exec-redaction-v1",
		Version: "v1",
		VerifierIDs: []string{
			"redaction-observed-v1",
			"outcome-not-infra-failed-v1",
		},
	},
	"tool-read-missing-v1": {
		ID:      "tool-read-missing-v1",
		Version: "v1",
		VerifierIDs: []string{
			"expected-tool-failure-observed-v1",
			"outcome-not-infra-failed-v1",
		},
	},
	"tool-containment-v1": {
		ID:      "tool-containment-v1",
		Version: "v1",
		VerifierIDs: []string{
			"containment-refused-v1",
			"outcome-not-infra-failed-v1",
		},
	},
}
