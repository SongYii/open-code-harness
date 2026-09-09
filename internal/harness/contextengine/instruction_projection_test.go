package contextengine

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestProjectSourceEventsPreservesWorkspaceInstructionDeltaOrder(t *testing.T) {
	records := []domain.RecordedEvent{
		record(1, domain.TurnStarted{TurnID: "t1", Input: "inspect the project"}),
		record(2, domain.WorkspaceInstructionsRecorded{RenderedMessage: "instruction delta"}),
		record(3, domain.AssistantMessageCompleted{TurnID: "t1", ItemID: "item1", Text: "done"}),
	}

	units, err := ProjectSourceEvents(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 3 {
		t.Fatalf("got %d units, want turn + instruction + assistant: %+v", len(units), units)
	}
	if units[1].Kind != UnitKindInstruction {
		t.Fatalf("unit[1].Kind = %q, want %q", units[1].Kind, UnitKindInstruction)
	}
	if units[1].FirstSequence != 2 || units[1].LastSequence != 2 {
		t.Fatalf("instruction sequence = [%d,%d], want [2,2]", units[1].FirstSequence, units[1].LastSequence)
	}
	if len(units[1].Messages) != 1 || units[1].Messages[0].Role != domain.PromptRoleUser || units[1].Messages[0].Text != "instruction delta" {
		t.Fatalf("instruction messages = %+v, want one framed user-role delta", units[1].Messages)
	}
}

func TestProjectSourceEventsSkipsWorkspaceInstructionAuditWithoutRenderedDelta(t *testing.T) {
	units, err := ProjectSourceEvents([]domain.RecordedEvent{
		record(1, domain.WorkspaceInstructionsRecorded{RenderedMessage: ""}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 0 {
		t.Fatalf("got %+v, want no prompt unit for a diagnostic-only audit event", units)
	}
}

func TestMaterializePlacesPrefixFirstAndCurrentInputLast(t *testing.T) {
	prefix := []domain.ModelPromptMessage{{Role: domain.PromptRoleSystem, Text: "fixed system prompt"}}
	result := Materialize(MaterializeInput{
		PrefixMessages: prefix,
		RetainedTail: []ContextUnit{{
			Kind: UnitKindInstruction, FirstSequence: 1, LastSequence: 1,
			Messages: []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: "instruction delta"}},
		}},
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "current input"},
		Meter:        WireEstimateMeter{},
	})
	want := []string{"fixed system prompt", "instruction delta", "current input"}
	if len(result.Envelope.Messages) != len(want) {
		t.Fatalf("messages = %+v, want %d messages", result.Envelope.Messages, len(want))
	}
	for index, text := range want {
		if result.Envelope.Messages[index].Text != text {
			t.Fatalf("Messages[%d].Text = %q, want %q", index, result.Envelope.Messages[index].Text, text)
		}
	}
}

func TestSelectCutPointCountsPrefixMessagesInRequestBudget(t *testing.T) {
	meter := WireEstimateMeter{}
	prefix := []domain.ModelPromptMessage{{Role: domain.PromptRoleSystem, Text: "fixed system prompt with meaningful token cost"}}
	prefixTokens := meter.EstimateMessages(prefix)
	if prefixTokens == 0 {
		t.Fatal("test prefix unexpectedly has zero estimated tokens")
	}

	result, err := SelectCutPoint(PlanInput{
		PrefixMessages: prefix,
		Budget:         Budget{HardInput: prefixTokens, Trigger: prefixTokens - 1, Target: prefixTokens - 1, ProtectedTail: 1, SummaryOutputCap: 1},
		Meter:          meter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsCompaction {
		t.Fatal("prefix-only request above Trigger was treated as below budget")
	}
	if result.EstimatedTokens != prefixTokens {
		t.Fatalf("EstimatedTokens = %d, want prefix estimate %d", result.EstimatedTokens, prefixTokens)
	}
}
