package main

import "github.com/SongYii/open-code-harness/internal/harness/eval"

// scorerCatalog is this command's own compiled table of Scorer
// configurations, keyed by the -scorer flag's ID — the same "code, never
// a data file" discipline internal/harness/eval's own verifier catalog
// follows (implementation plan Task 9).
var scorerCatalog = map[string]eval.Scorer{
	// One scorer per Context Scenario, because RunScorer requires every
	// verifier a scorer names to be declared by the Scenario it runs
	// against. A single broad Context scorer would therefore refuse every
	// Scenario that exercises only part of the catalog — which is the right
	// rule (a scorer must not claim a mechanism the Scenario never ran) and
	// the reason these are split rather than merged.
	"context-manual-reset-scorer-v1": {
		ID:      "context-manual-reset-scorer-v1",
		Version: "v1",
		VerifierIDs: []string{
			eval.VerifierContextManualReset,
			eval.VerifierContextBudgetBounds,
			eval.VerifierContextProjection,
			"outcome-not-infra-failed-v1",
		},
	},
	"context-manual-summary-scorer-v1": {
		ID:      "context-manual-summary-scorer-v1",
		Version: "v1",
		VerifierIDs: []string{
			eval.VerifierContextManualSummary,
			eval.VerifierContextCheckpointReuse,
			eval.VerifierContextBudgetBounds,
			eval.VerifierContextProjection,
			"outcome-not-infra-failed-v1",
		},
	},
	"context-restart-scorer-v1": {
		ID:      "context-restart-scorer-v1",
		Version: "v1",
		VerifierIDs: []string{
			eval.VerifierContextCheckpointReuse,
			eval.VerifierContextBudgetBounds,
			eval.VerifierContextProjection,
			"outcome-not-infra-failed-v1",
		},
	},
	"context-anchor-scorer-v1": {
		ID:      "context-anchor-scorer-v1",
		Version: "v1",
		VerifierIDs: []string{
			eval.VerifierContextUsageAnchor,
			eval.VerifierContextBudgetBounds,
			eval.VerifierContextProjection,
			"outcome-not-infra-failed-v1",
		},
	},
	"context-prune-scorer-v1": {
		ID:      "context-prune-scorer-v1",
		Version: "v1",
		VerifierIDs: []string{
			eval.VerifierContextMidTurn,
			eval.VerifierContextToolResultPruned,
			eval.VerifierContextBudgetBounds,
			"outcome-not-infra-failed-v1",
		},
	},
	"context-overflow-scorer-v1": {
		ID:      "context-overflow-scorer-v1",
		Version: "v1",
		VerifierIDs: []string{
			eval.VerifierContextOverflowRecover,
			eval.VerifierContextBudgetBounds,
			eval.VerifierContextProjection,
			"outcome-not-infra-failed-v1",
		},
	},
	"context-pre-turn-scorer-v1": {
		ID:      "context-pre-turn-scorer-v1",
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
