package main

import "github.com/SongYii/open-code-harness/internal/harness/eval"

// scorerCatalog is this command's own compiled table of Scorer
// configurations, keyed by the -scorer flag's ID — the same "code, never
// a data file" discipline internal/harness/eval's own verifier catalog
// follows (implementation plan Task 9).
var scorerCatalog = map[string]eval.Scorer{
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
