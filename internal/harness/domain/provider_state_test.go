package domain

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func replayState() *ProviderState {
	return &ProviderState{Protocol: DeepSeekThinkingV1, ModelID: "test-model", EndpointID: "api.example.com", ReasoningContent: "private reasoning\n你好"}
}

func TestProviderStateStrictCodecAndLegacyBytes(t *testing.T) {
	assistant := AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "visible"}
	request := validModelRequestRecorded("turn-1", "item-1", "hello")
	request.Messages = []ModelPromptMessage{{Role: PromptRoleAssistant, Text: "visible"}}
	for _, event := range []Event{assistant, request} {
		record := compactRecord(compactActiveSession(t), event)
		legacy, err := MarshalRecordedEvent(record)
		if err != nil || bytes.Contains(legacy, []byte(`"providerState"`)) {
			t.Fatalf("legacy payload: %v", err)
		}
		decoded, err := UnmarshalRecordedEvent(legacy)
		if err != nil {
			t.Fatal(err)
		}
		again, err := MarshalRecordedEvent(decoded)
		if err != nil || !bytes.Equal(legacy, again) {
			t.Fatal("legacy bytes changed")
		}
		switch e := event.(type) {
		case AssistantMessageCompleted:
			e.ProviderState = replayState()
			event = e
		case ModelRequestRecorded:
			e.Messages[0].ProviderState = replayState()
			event = e
		}
		record.Event = event
		encoded, err := MarshalRecordedEvent(record)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err = UnmarshalRecordedEvent(encoded)
		if err != nil || !reflect.DeepEqual(decoded.Event, event) {
			t.Fatalf("state roundtrip: %v", err)
		}
		for _, bad := range [][]byte{
			bytes.Replace(encoded, []byte(`"reasoningContent":"private reasoning\n你好"`), []byte(`"reasoningContent":null`), 1),
			bytes.Replace(encoded, []byte(`"protocol":"deepseek_thinking_v1"`), []byte(`"protocol":"future"`), 1),
			bytes.Replace(encoded, []byte(`"protocol":"deepseek_thinking_v1"`), []byte(`"protocol":"deepseek_thinking_v1","extra":true`), 1),
			bytes.Replace(encoded, []byte(`"reasoningContent":`), []byte(`"reasoningContent":"duplicate","reasoningContent":`), 1),
			bytes.Replace(encoded, []byte(`"reasoningContent":`), []byte(`"missingReasoning":`), 1),
		} {
			if _, err := UnmarshalRecordedEvent(bad); err == nil {
				t.Fatal("malformed state accepted")
			}
		}
		cloned, err := CloneEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		switch e := cloned.(type) {
		case AssistantMessageCompleted:
			e.ProviderState.ReasoningContent = "mutated"
			if event.(AssistantMessageCompleted).ProviderState.ReasoningContent == "mutated" {
				t.Fatal("shared state")
			}
		case ModelRequestRecorded:
			e.Messages[0].ProviderState.ReasoningContent = "mutated"
			if event.(ModelRequestRecorded).Messages[0].ProviderState.ReasoningContent == "mutated" {
				t.Fatal("shared request state")
			}
		}
	}
}

func TestProviderStateBoundsAndRole(t *testing.T) {
	for _, mutate := range []func(*ProviderState){
		func(s *ProviderState) { s.ReasoningContent = strings.Repeat("x", MaxProviderReasoningBytes+1) },
		func(s *ProviderState) { s.ReasoningContent = "\xff" },
		func(s *ProviderState) { s.EndpointID = "user:password@api.example.com" },
		func(s *ProviderState) { s.ModelID = "" },
	} {
		s := replayState()
		mutate(s)
		if ValidateProviderState(s) == nil {
			t.Fatal("invalid state accepted")
		}
	}
	s := replayState()
	s.ReasoningContent = ""
	if err := ValidateProviderState(s); err != nil {
		t.Fatal("genuinely empty reasoning must be representable")
	}
	for _, role := range []string{PromptRoleUser, PromptRoleSystem, PromptRoleTool} {
		message := ModelPromptMessage{Role: role, Text: "visible"}
		if role == PromptRoleTool {
			message.ToolCallID = "c"
			message.Name = "tool"
		}
		if err := validateModelPromptMessages([]ModelPromptMessage{message}, CodeInvalidEvent); err != nil {
			t.Fatalf("invalid role-test baseline: %v", err)
		}
		message.ProviderState = s
		if validateModelPromptMessages([]ModelPromptMessage{message}, CodeInvalidEvent) == nil {
			t.Fatal("state accepted outside assistant role")
		}
	}
}

func TestProviderStateCommandCompletionIsAtomicAndDetached(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, TurnStarted{TurnID: "turn-1", Input: "input"})
	state = applyCompactRecord(t, state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
	for _, terminal := range []bool{false, true} {
		provider := replayState()
		var command Command = CompleteAssistantMessage{SessionID: state.ID, TurnID: "turn-1", ItemID: "item-1", Text: "visible", ProviderState: provider}
		want := 1
		if terminal {
			command = CompleteAssistantTurn{SessionID: state.ID, TurnID: "turn-1", ItemID: "item-1", Text: "visible", ProviderState: provider}
			want = 2
		}
		events, err := Decide(state, command)
		if err != nil || len(events) != want {
			t.Fatalf("completion: %v", err)
		}
		completed := events[0].Event.(AssistantMessageCompleted)
		if completed.ProviderState == nil || completed.ProviderState == provider || !reflect.DeepEqual(completed.ProviderState, provider) {
			t.Fatal("completion lost/shared state")
		}
		provider.Protocol = "unknown"
		if bad, err := Decide(state, command); err == nil || len(bad) != 0 {
			t.Fatal("invalid state emitted a completion batch")
		}
	}
}
