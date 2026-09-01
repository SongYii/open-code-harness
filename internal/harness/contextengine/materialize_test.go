package contextengine

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestMaterializeNoCheckpointNoTail(t *testing.T) {
	result := Materialize(MaterializeInput{
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "hello"},
		Meter:        WireEstimateMeter{},
	})
	if len(result.Envelope.Messages) != 1 || result.Envelope.Messages[0].Text != "hello" {
		t.Fatalf("got %+v, want a single current-input message", result.Envelope.Messages)
	}
	if result.CheckpointID != "" || result.CheckpointKind != "" {
		t.Fatalf("expected no checkpoint fields set, got ID=%q Kind=%q", result.CheckpointID, result.CheckpointKind)
	}
	if result.MeterID != WireEstimateMeterID {
		t.Fatalf("MeterID = %q, want %q", result.MeterID, WireEstimateMeterID)
	}
}

func TestMaterializeWithRollingSummaryCheckpointAndTail(t *testing.T) {
	checkpoint := &ContextCheckpoint{
		ID:               "ckpt_1",
		Kind:             CheckpointKindRollingSummary,
		Summary:          "## Objective\nsummary text",
		Coverage:         Coverage{ThroughSequence: 50},
		CheckpointTokens: 10,
	}
	tail := []ContextUnit{
		{TurnID: "t1", FirstSequence: 51, LastSequence: 51, Messages: []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: "recent turn"}}},
		{TurnID: "t1", FirstSequence: 52, LastSequence: 52, Messages: []domain.ModelPromptMessage{{Role: domain.PromptRoleAssistant, Text: "recent reply"}}},
	}
	result := Materialize(MaterializeInput{
		Checkpoint:   checkpoint,
		RetainedTail: tail,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "current"},
		Meter:        WireEstimateMeter{},
	})
	if result.CheckpointID != "ckpt_1" || result.CheckpointKind != CheckpointKindRollingSummary {
		t.Fatalf("got CheckpointID=%q CheckpointKind=%q, want ckpt_1/rolling_summary_v1", result.CheckpointID, result.CheckpointKind)
	}
	// Order: checkpoint summary, then tail messages in order, then current input.
	want := []string{"## Objective\nsummary text", "recent turn", "recent reply", "current"}
	if len(result.Envelope.Messages) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(result.Envelope.Messages), len(want), result.Envelope.Messages)
	}
	for index, text := range want {
		if result.Envelope.Messages[index].Text != text {
			t.Fatalf("Messages[%d].Text = %q, want %q", index, result.Envelope.Messages[index].Text, text)
		}
	}
	if result.RetainedTailFromSequence != 51 || result.RetainedTailThroughSequence != 52 {
		t.Fatalf("got tail range [%d,%d], want [51,52]", result.RetainedTailFromSequence, result.RetainedTailThroughSequence)
	}
}

func TestMaterializeWithSourceTailResetCheckpoint(t *testing.T) {
	checkpoint := &ContextCheckpoint{
		ID:       "ckpt_reset",
		Kind:     CheckpointKindSourceTailReset,
		Coverage: Coverage{ThroughSequence: 99},
	}
	result := Materialize(MaterializeInput{
		Checkpoint:   checkpoint,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "current"},
		Meter:        WireEstimateMeter{},
	})
	if len(result.Envelope.Messages) != 2 {
		t.Fatalf("got %d messages, want 2 (reset marker + current input)", len(result.Envelope.Messages))
	}
	if result.Envelope.Messages[0].Text != BuildResetMarker("ckpt_reset", 99) {
		t.Fatalf("first message is not the exact reset marker: %q", result.Envelope.Messages[0].Text)
	}
}

func TestMaterializeTokenBreakdownSumsToTotal(t *testing.T) {
	tools := []domain.ToolSchema{{Name: "read_file", Description: "reads a file", InputSchema: []byte(`{"type":"object"}`)}}
	result := Materialize(MaterializeInput{
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "hello world"},
		Tools:        tools,
		Meter:        WireEstimateMeter{},
	})
	if result.EstimatedMessageTokens+result.EstimatedToolSchemaTokens != result.EstimatedTotalTokens {
		t.Fatalf("message(%d) + toolSchema(%d) != total(%d)", result.EstimatedMessageTokens, result.EstimatedToolSchemaTokens, result.EstimatedTotalTokens)
	}
	if result.EstimatedToolSchemaTokens == 0 {
		t.Fatal("expected a non-zero Tool Schema token share given a real Tool Schema was supplied")
	}
	if result.ApproximateSerializedBytes == 0 {
		t.Fatal("expected a non-zero ApproximateSerializedBytes")
	}
}
