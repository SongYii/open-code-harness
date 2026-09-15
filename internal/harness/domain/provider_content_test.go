package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func messagesState() *ProviderState {
	sig := ""
	return &ProviderState{Protocol: DeepSeekMessagesV1, ModelID: "test-model", EndpointID: "api.deepseek.com/anthropic", MessagesContent: []ProviderContentBlock{
		{Type: "thinking", Thinking: "private"}, {Type: "text", Text: ""}, {Type: "thinking", Thinking: "", Signature: &sig},
		{Type: "tool_use", ID: "call_1", Name: "read_file", Input: json.RawMessage(`{"n":9007199254740993,"path":"<a>"}`)}, {Type: "text", Text: "visible"},
	}}
}

func TestMessagesStateStrictCodecAndProjection(t *testing.T) {
	s := messagesState()
	calls := []ToolCallOffer{{ID: "call_1", Name: "read_file", Arguments: `{"n":9007199254740993,"path":"<a>"}`}}
	assistant := AssistantMessageCompleted{TurnID: "t", ItemID: "i", Text: "visible", ToolCalls: calls, ProviderState: s}
	req := validModelRequestRecorded("t", "i", "hello")
	req.Messages = []ModelPromptMessage{{Role: "assistant", Text: "visible", ToolCalls: calls, ProviderState: s}}
	for _, event := range []Event{assistant, req} {
		record := compactRecord(compactActiveSession(t), event)
		encoded, err := MarshalRecordedEvent(record)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := UnmarshalRecordedEvent(encoded)
		if err != nil {
			t.Fatal(err)
		}
		again, err := MarshalRecordedEvent(decoded)
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatal("canonical Messages roundtrip changed")
		}
		for _, change := range [][2]string{
			{`"messagesContent":[`, `"unknownContent":[`}, {`"protocol":"deepseek_messages_v1"`, `"protocol":"deepseek_thinking_v1"`},
			{`"thinking":"private"`, `"thinking":null`}, {`"thinking":"private"`, `"thinking":"private","thinking":"duplicate"`},
			{`"signature":""`, `"signature":null`}, {`"type":"thinking"`, `"type":"redacted_thinking"`},
			{`"n":9007199254740993`, `"n":9007199254740993,"n":0`}, {`"n":9007199254740993`, `"n":"\ud800"`},
			{`"reasoningContent":""`, `"reasoningContent":"mixed-protocol"`}, {`"thinking":"private"`, `"thinking":"private","text":"cross-variant"`},
		} {
			bad := bytes.Replace(encoded, []byte(change[0]), []byte(change[1]), 1)
			if bytes.Equal(bad, encoded) {
				t.Fatalf("mutation target missing: %s", change[0])
			}
			if _, err := UnmarshalRecordedEvent(bad); err == nil {
				t.Fatalf("invalid state accepted: %s", change[1])
			}
		}
	}
	if err := ValidateProviderProjection(s, "visible", calls); err != nil {
		t.Fatal(err)
	}
	if ValidateProviderProjection(s, "changed", calls) == nil || ValidateProviderProjection(s, "visible", nil) == nil {
		t.Fatal("projection mismatch accepted")
	}
	calls[0].Arguments = `{"n":9007199254740992,"path":"<a>"}`
	if ValidateProviderProjection(s, "visible", calls) == nil {
		t.Fatal("large integer mismatch rounded away")
	}
	clone := CloneProviderState(s)
	clone.MessagesContent[0].Thinking = "changed"
	*clone.MessagesContent[2].Signature = "changed"
	clone.MessagesContent[3].Input[0] = '!'
	if s.MessagesContent[0].Thinking != "private" || *s.MessagesContent[2].Signature != "" || s.MessagesContent[3].Input[0] != '{' {
		t.Fatal("clone aliases mutable content")
	}
}

func TestMessagesStateBoundsAndAtomicCommand(t *testing.T) {
	for _, mutate := range []func(*ProviderState){func(s *ProviderState) { s.MessagesContent = nil }, func(s *ProviderState) {
		s.MessagesContent[0].Thinking = strings.Repeat("h", MaxProviderReasoningBytes+1)
	}, func(s *ProviderState) { s.MessagesContent[4].Text = strings.Repeat("x", 1<<20) }, func(s *ProviderState) { s.MessagesContent[0].Text = "cross-variant" }, func(s *ProviderState) {
		s.MessagesContent[3].Input = json.RawMessage(`{"x":` + strings.Repeat(`[`, 65) + `0` + strings.Repeat(`]`, 65) + `}`)
	}} {
		s := messagesState()
		mutate(s)
		if ValidateProviderState(s) == nil {
			t.Fatal("invalid in-memory state accepted")
		}
	}
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, TurnStarted{TurnID: "t", Input: "hello"})
	state = applyCompactRecord(t, state, AssistantMessageStarted{TurnID: "t", ItemID: "i"})
	s := messagesState()
	calls := []ToolCallOffer{{ID: "call_1", Name: "read_file", Arguments: string(s.MessagesContent[3].Input)}}
	command := CompleteAssistantMessage{SessionID: state.ID, TurnID: "t", ItemID: "i", Text: "visible", ToolCalls: calls, ProviderState: s}
	events, err := Decide(state, command)
	if err != nil || len(events) != 1 {
		t.Fatal("valid command rejected")
	}
	if !reflect.DeepEqual(events[0].Event.(AssistantMessageCompleted).ProviderState, s) {
		t.Fatal("command lost state")
	}
	command.Text = "different"
	if events, err := Decide(state, command); err == nil || len(events) != 0 {
		t.Fatal("mismatch emitted events")
	}
}
