package application_test

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
)

// TestContextPreparedRecordedFromResultCopiesObservedPruningCount proves the
// evidence carries what Materialize actually did, not what configuration
// allowed. The count comes from the prepared result; nothing here may derive
// it from MaxPrunedToolResultsPerRequest.
func TestContextPreparedRecordedFromResultCopiesObservedPruningCount(t *testing.T) {
	result := application.PrepareContextResult{
		SourceHeadVersion: 12,
		Budget:            contextengine.Budget{HardInput: 6656, Trigger: 5324, Target: 3660},
		Prepared: contextengine.PreparedContext{
			EstimatedMessageTokens: 100, EstimatedToolSchemaTokens: 20, EstimatedTotalTokens: 120,
			MeterID: "och_wire_estimate_v1", ApproximateSerializedBytes: 500,
			PrunedToolResultCount: 2,
		},
	}
	event := application.ContextPreparedRecordedFromResult(
		result, "mid_turn", 2, "ctxdecision-00000000000000000000000000000001", "turn-1", "item-1")
	if event.PrunedToolResultCount != 2 {
		t.Fatalf("PrunedToolResultCount = %d, want the observed 2", event.PrunedToolResultCount)
	}
}

// TestContextPreparedRecordedFromResultReportsNoPruningAsZero pins the
// meaning of zero: no Tool Result was projected for this request. It does not
// mean pruning was disabled, and it must not be conflated with an absent
// decision.
func TestContextPreparedRecordedFromResultReportsNoPruningAsZero(t *testing.T) {
	result := application.PrepareContextResult{
		Budget: contextengine.Budget{HardInput: 6656, Trigger: 5324, Target: 3660},
		Prepared: contextengine.PreparedContext{
			MeterID: "och_wire_estimate_v1", EstimatedTotalTokens: 10, ApproximateSerializedBytes: 100,
		},
	}
	event := application.ContextPreparedRecordedFromResult(
		result, "pre_turn", 1, "ctxdecision-00000000000000000000000000000002", "turn-1", "item-1")
	if event.PrunedToolResultCount != 0 {
		t.Fatalf("PrunedToolResultCount = %d, want 0", event.PrunedToolResultCount)
	}
}
