package contextengine

import (
	"strings"
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

// bigToolResultText is long enough that its wire estimate exceeds even the
// largest possible MaxProjectedToolResultTokens (2048) by a wide margin,
// so every test below can rely on it needing projection regardless of the
// exact ProtectedTail it derives the cap from.
func bigToolResultText() string {
	return strings.Repeat("this line of tool output repeats many times. ", 2000)
}

func stepUnitWithToolResult(seq uint64, callID, toolText string) ContextUnit {
	return ContextUnit{
		Kind: UnitKindStep, FirstSequence: seq, LastSequence: seq + 1,
		Messages: []domain.ModelPromptMessage{
			{Role: domain.PromptRoleAssistant, Text: "calling a tool", ToolCalls: []domain.ToolCallOffer{{ID: callID, Name: "read_file"}}},
			{Role: domain.PromptRoleTool, Text: toolText, ToolCallID: callID},
		},
	}
}

// TestMaterializeToolResultPruningDisabledByDefaultLeavesContentByteIdentical
// proves design's own backward-compatibility requirement: a MaterializeInput
// built before ProtectedTail/MaxPrunedToolResults existed (both left at
// their zero value) must dispatch every retained Tool Result byte-identical,
// however large.
func TestMaterializeToolResultPruningDisabledByDefaultLeavesContentByteIdentical(t *testing.T) {
	big := bigToolResultText()
	result := Materialize(MaterializeInput{
		RetainedTail: []ContextUnit{stepUnitWithToolResult(1, "call-1", big)},
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
		Meter:        WireEstimateMeter{},
	})
	var sawToolMessage bool
	for _, message := range result.Envelope.Messages {
		if message.Role == domain.PromptRoleTool {
			sawToolMessage = true
			if message.Text != big {
				t.Fatalf("Tool Result text was altered despite pruning being disabled: got %d bytes, want %d", len(message.Text), len(big))
			}
		}
	}
	if !sawToolMessage {
		t.Fatal("no Tool Result message found in the materialized envelope")
	}
	if result.PrunedToolResultCount != 0 {
		t.Fatalf("PrunedToolResultCount = %d, want 0 with pruning disabled", result.PrunedToolResultCount)
	}
}

// TestMaterializePrunesOversizedToolResultsUpToTheCap proves design §10's
// projection actually runs from Materialize's own pipeline once enabled:
// oversized Tool Results become ProjectToolResult's marker-framed excerpt,
// small ones and non-Tool messages stay untouched, and no more than
// MaxPrunedToolResults are ever replaced in one call.
func TestMaterializePrunesOversizedToolResultsUpToTheCap(t *testing.T) {
	big := bigToolResultText()
	tail := []ContextUnit{
		stepUnitWithToolResult(1, "call-1", big),
		stepUnitWithToolResult(3, "call-2", big),
		{Kind: UnitKindAssistant, FirstSequence: 5, LastSequence: 5, Messages: []domain.ModelPromptMessage{{Role: domain.PromptRoleAssistant, Text: "a short unrelated reply"}}},
	}
	meter := WireEstimateMeter{}
	protectedTail := uint64(2000) // MaxProjectedToolResultTokens(2000) = min(2048, max(256, 1000)) = 1000
	result := Materialize(MaterializeInput{
		RetainedTail: tail, CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"}, Meter: meter,
		ProtectedTail: protectedTail, MaxPrunedToolResults: 1, HardInput: 1_000_000,
	})
	if result.PrunedToolResultCount != 1 {
		t.Fatalf("PrunedToolResultCount = %d, want exactly 1 (the configured cap)", result.PrunedToolResultCount)
	}
	var prunedSeen, untouchedSeen, shortReplySeen int
	for _, message := range result.Envelope.Messages {
		switch {
		case message.Role == domain.PromptRoleTool && message.Text == big:
			untouchedSeen++
		case message.Role == domain.PromptRoleTool:
			prunedSeen++
			maxTokens := MaxProjectedToolResultTokens(protectedTail)
			if estimateText(meter, message.Text) > maxTokens {
				t.Fatalf("pruned Tool Result still estimates above the cap: %d > %d", estimateText(meter, message.Text), maxTokens)
			}
			if !strings.Contains(message.Text, "call-1") && !strings.Contains(message.Text, "call-2") {
				t.Fatalf("pruned Tool Result marker does not name either event_id: %q", message.Text)
			}
		case message.Role == domain.PromptRoleAssistant && message.Text == "a short unrelated reply":
			shortReplySeen++
		}
	}
	if prunedSeen != 1 {
		t.Fatalf("saw %d pruned Tool Result messages in the envelope, want exactly 1", prunedSeen)
	}
	if untouchedSeen != 1 {
		t.Fatalf("saw %d byte-identical Tool Result messages (beyond the cap), want exactly 1", untouchedSeen)
	}
	if shortReplySeen != 1 {
		t.Fatal("the short, unrelated assistant reply was altered or dropped")
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
