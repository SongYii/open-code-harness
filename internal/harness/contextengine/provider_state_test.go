package contextengine

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestProviderStateMaterializationAndMeter(t *testing.T) {
	state := &domain.ProviderState{Protocol: domain.DeepSeekThinkingV1, ModelID: "test", EndpointID: "api.example.com", ReasoningContent: "hidden-state-canary"}
	records := []domain.RecordedEvent{
		record(1, domain.TurnStarted{TurnID: "old", Input: "old input"}),
		record(2, domain.AssistantMessageCompleted{TurnID: "old", ItemID: "old-item", Text: "old", ProviderState: state}),
		record(3, domain.TurnStarted{TurnID: "retained", Input: "retained input"}),
		record(4, domain.AssistantMessageCompleted{TurnID: "retained", ItemID: "retained-item", Text: "visible", ProviderState: state}),
	}
	units, err := ProjectSourceEvents(records)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &ContextCheckpoint{ID: "cp", Kind: CheckpointKindRollingSummary, Summary: "summary of old turn", Coverage: Coverage{ThroughSequence: 2}}
	prepared := Materialize(MaterializeInput{Checkpoint: checkpoint, RetainedTail: units[2:], Meter: WireEstimateMeter{}})
	messages := prepared.Envelope.Messages
	if len(messages) != 3 || messages[0].Role != domain.PromptRoleUser || messages[0].ProviderState != nil {
		t.Fatal("summary acquired assistant protocol state")
	}
	if messages[2].ProviderState == nil || *messages[2].ProviderState != *state || messages[2].ProviderState == units[3].Messages[0].ProviderState {
		t.Fatal("retained state lost or shared")
	}
	bare := []domain.ModelPromptMessage{{Role: domain.PromptRoleAssistant, Text: "visible"}}
	delta := (WireEstimateMeter{}).EstimateMessages(messages[2:]) - (WireEstimateMeter{}).EstimateMessages(bare)
	if delta != perMessageFraming+textTokens(state.ReasoningContent) {
		t.Fatal("reasoning missing from budget")
	}
	messages[2].ProviderState.ReasoningContent = "mutated"
	if units[3].Messages[0].ProviderState.ReasoningContent != state.ReasoningContent {
		t.Fatal("materializer exposed unit state")
	}
	// Core replay cannot drop an otherwise empty committed assistant that
	// still carries protocol state, even though the HTTP adapter rejects an
	// entirely empty visible completion itself.
	units, err = ProjectSourceEvents([]domain.RecordedEvent{record(1, domain.AssistantMessageCompleted{TurnID: "t", ItemID: "i", ProviderState: state})})
	if err != nil || len(units) != 1 || units[0].Messages[0].ProviderState == nil {
		t.Fatal("state-only committed message was dropped")
	}
}
