package application

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

func TestProviderStateSurvivesLegacyProjectionButNotSummaryRendering(t *testing.T) {
	state := &domain.ProviderState{Protocol: domain.DeepSeekThinkingV1, ModelID: "test", EndpointID: "api.example.com", ReasoningContent: "hidden-state-canary"}
	event := domain.AssistantMessageCompleted{TurnID: "old", ItemID: "item", Text: "visible", ProviderState: state}
	records := []domain.RecordedEvent{{Event: event}}
	messages := projectPriorTurns(records, "current")
	if len(messages) != 1 || messages[0].ProviderState == nil || !reflect.DeepEqual(messages[0].ProviderState, state) || messages[0].ProviderState == state {
		t.Fatal("prior-turn projection dropped/shared state")
	}
	var b strings.Builder
	renderUnits(&b, []contextengine.ContextUnit{{Messages: messages}})
	if !strings.Contains(b.String(), "visible") || strings.Contains(b.String(), state.ReasoningContent) {
		t.Fatal("summary renderer mixed protocol and visible content")
	}
	cloned := clonePromptMessages(messages)
	cloned[0].ProviderState.ReasoningContent = "changed"
	if messages[0].ProviderState.ReasoningContent != state.ReasoningContent {
		t.Fatal("clone shares state")
	}

	event.ToolCalls = []domain.ToolCallOffer{{ID: "call", Name: "read_file", Arguments: "{}"}}
	projection := &turnProjection{}
	projection.applyRecords([]domain.RecordedEvent{{Event: event}})
	if len(projection.messages) != 1 || projection.messages[0].ProviderState == nil || !reflect.DeepEqual(projection.messages[0].ProviderState, state) {
		t.Fatal("mid-turn projection dropped state")
	}
}

func TestApplicationProviderStateGate(t *testing.T) {
	state := &domain.ProviderState{Protocol: domain.DeepSeekThinkingV1, ModelID: "test", EndpointID: "api.example.com", ReasoningContent: "private"}
	service := &Service{}
	if !service.acceptProviderState(nil) || service.acceptProviderState(state) {
		t.Fatal("legacy nil-identity path changed")
	}
	identity := &engine.RequestIdentity{AdapterFamily: domain.DeepSeekThinkingV1, ModelID: state.ModelID, EndpointID: state.EndpointID}
	service.config.RequestIdentity = identity
	if !service.acceptProviderState(state) || service.acceptProviderState(nil) {
		t.Fatal("required state not enforced")
	}
	identity.AdapterFamily = "openai_compat"
	if service.acceptProviderState(state) {
		t.Fatal("legacy route accepted foreign protocol")
	}
	identity.AdapterFamily = domain.DeepSeekThinkingV1
	for _, mutate := range []func(*domain.ProviderState){
		func(s *domain.ProviderState) { s.ModelID = "other" },
		func(s *domain.ProviderState) { s.EndpointID = "other.example.com" },
		func(s *domain.ProviderState) { s.Protocol = "other" },
		func(s *domain.ProviderState) { s.ReasoningContent = "Authorization: Bearer sk-provider-private-key" },
	} {
		bad := domain.CloneProviderState(state)
		mutate(bad)
		if service.acceptProviderState(bad) {
			t.Fatal("unsafe protocol state accepted")
		}
	}
}
