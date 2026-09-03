package transcript

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// TestProjectContextPreparedCarriesPrunedToolResultCount proves the public
// transcript projects the observed per-request pruning count. The transcript
// deliberately omits model.request.recorded, so without this field a
// transcript reader has no way to see that Tool Result projection happened
// at all.
func TestProjectContextPreparedCarriesPrunedToolResultCount(t *testing.T) {
	occurred := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	event := domain.ContextPreparedRecorded{
		TurnID: "turn-1", ItemID: "item-1", AttemptIndex: 2, ContextDecisionID: "decision-1",
		Trigger: domain.ContextTriggerMidTurn, SourceHeadVersion: 18,
		BudgetHardInput: 100000, BudgetTrigger: 80000, BudgetTarget: 55000,
		EstimatedMessageTokens: 900, EstimatedToolSchemaTokens: 100, EstimatedTotalTokens: 1000,
		MeterID: "och_wire_estimate_v1", SerializedEnvelopeBytes: 3200,
		PrunedToolResultCount: 2,
	}
	line, ok, err := ProjectRecord(fixtureRecord(30, occurred, event), map[domain.TurnID]uint32{})
	if err != nil || !ok {
		t.Fatalf("ProjectRecord() ok=%v err=%v", ok, err)
	}
	encoded, err := MarshalLine(line)
	if err != nil {
		t.Fatalf("MarshalLine: %v", err)
	}
	var envelope struct {
		Payload struct {
			PrunedToolResultCount uint32 `json:"prunedToolResultCount"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("unmarshal projected line: %v", err)
	}
	if envelope.Payload.PrunedToolResultCount != 2 {
		t.Fatalf("projected prunedToolResultCount = %d, want 2: %s", envelope.Payload.PrunedToolResultCount, encoded)
	}
	if _, err := UnmarshalLine(encoded); err != nil {
		t.Fatalf("UnmarshalLine(projected): %v", err)
	}
}
