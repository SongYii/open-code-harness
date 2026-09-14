package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

func deepSeekSSE(reasoning, finish string) string {
	encoded, _ := json.Marshal(reasoning)
	return "data: {\"choices\":[{\"delta\":{\"reasoning_content\":" + string(encoded) + "}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"visible\"},\"finish_reason\":\"" + finish + "\"}]}\n\n" + "data: [DONE]\n\n"
}

func TestDeepSeekCompletedStateAndReplayMapping(t *testing.T) {
	for _, reasoning := range []string{"", "private\n你好"} {
		transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
			return sseResponse(http.StatusOK, deepSeekSSE(reasoning, "stop"), nil), nil
		}}
		cfg := validToolsConfig(transport)
		cfg.Protocol = domain.DeepSeekThinkingV1
		cfg.Hints.ReasoningEffort = engine.ReasoningEffortHigh
		model := newTestModel(t, cfg)
		stream, err := model.Stream(context.Background(), modelRequest())
		if err != nil {
			t.Fatal(err)
		}
		events, err := collectStream(t, stream)
		_ = stream.Close()
		if err != nil || len(events) != 2 {
			t.Fatalf("completion events: %v", err)
		}
		if events[0].Text != "visible" || events[0].ProviderState != nil {
			t.Fatal("protocol state leaked into text delta")
		}
		state := events[1].ProviderState
		want := &domain.ProviderState{Protocol: domain.DeepSeekThinkingV1, ModelID: cfg.ModelID, EndpointID: "api.example.com/v1", ReasoningContent: reasoning}
		if !reflect.DeepEqual(state, want) {
			t.Fatal("completed state lost or changed")
		}
		request := modelRequest()
		request.Messages = []domain.ModelPromptMessage{
			{Role: domain.PromptRoleAssistant, Text: "prior non-tool reply", ProviderState: state},
			{Role: domain.PromptRoleAssistant, Text: "tool reply", ToolCalls: []domain.ToolCallOffer{{ID: "c1", Name: "read_file", Arguments: "{}"}}, ProviderState: state},
			{Role: domain.PromptRoleTool, Text: "result", ToolCallID: "c1", Name: "read_file"},
		}
		request.Tools = []domain.ToolSchema{sampleToolSchema()}
		body, err := model.marshalRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Messages []map[string]any   `json:"messages"`
			Thinking completionThinking `json:"thinking"`
			Effort   string             `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.Thinking.Type != "enabled" || wire.Effort != "high" {
			t.Fatal("thinking controls not explicit")
		}
		for _, message := range wire.Messages[:2] {
			if got, exists := message["reasoning_content"]; !exists || got != reasoning {
				t.Fatal("prior assistant reasoning missing, including empty/non-tool reply")
			}
		}
		if _, ok := wire.Messages[2]["reasoning_content"]; ok {
			t.Fatal("state attached to tool result")
		}
		request.Tools = nil
		body, err = model.marshalRequest(request)
		if err != nil || strings.Contains(string(body), "reasoning_content") {
			t.Fatal("no-tools request should not send reasoning")
		}
	}
}

func TestDeepSeekRejectsInvalidReplayBeforeHTTP(t *testing.T) {
	for _, mutation := range []string{"missing", "version", "model", "endpoint", "role", "legacy_route"} {
		t.Run(mutation, func(t *testing.T) {
			transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) { t.Fatal("invalid replay reached HTTP"); return nil, nil }}
			cfg := validToolsConfig(transport)
			cfg.Protocol = domain.DeepSeekThinkingV1
			state := &domain.ProviderState{Protocol: domain.DeepSeekThinkingV1, ModelID: cfg.ModelID, EndpointID: "api.example.com/v1", ReasoningContent: "private"}
			message := domain.ModelPromptMessage{Role: domain.PromptRoleAssistant, Text: "visible", ProviderState: state}
			switch mutation {
			case "missing":
				message.ProviderState = nil
			case "version":
				state.Protocol = "future"
			case "model":
				state.ModelID = "other"
			case "endpoint":
				state.EndpointID = "other.example.com"
			case "role":
				message.Role = domain.PromptRoleUser
			case "legacy_route":
				cfg.Protocol = ""
			}
			model := newTestModel(t, cfg)
			request := modelRequest()
			request.Messages = []domain.ModelPromptMessage{message}
			request.Tools = []domain.ToolSchema{sampleToolSchema()}
			if _, err := model.Stream(context.Background(), request); err == nil {
				t.Fatal("invalid replay accepted")
			}
		})
	}
}

func TestDeepSeekIncompleteStateNeverCompletes(t *testing.T) {
	valid := deepSeekSSE("private", "stop")
	for name, sse := range map[string]string{
		"unpaired surrogate":    strings.Replace(valid, `"reasoning_content":"private"`, `"reasoning_content":"\ud800"`, 1),
		"multiline event bound": strings.Repeat("data: \n", 1025),
		"missing":               strings.Replace(valid, `"reasoning_content":"private"`, `"role":"assistant"`, 1),
		"null":                  strings.Replace(valid, `"reasoning_content":"private"`, `"reasoning_content":null`, 1),
		"wrong type":            strings.Replace(valid, `"reasoning_content":"private"`, `"reasoning_content":42`, 1),
		"truncated":             strings.Replace(valid, "data: [DONE]\n\n", "", 1),
		"length":                deepSeekSSE("partial", "length"),
		"no finish":             strings.Replace(valid, `,"finish_reason":"stop"`, "", 1),
		"secret":                deepSeekSSE("Authorization: Bearer sk-provider-private-key", "stop"),
		"oversized":             strings.Repeat("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\""+strings.Repeat("x", 64<<10)+"\"}}]}\n\n", 5) + valid,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validToolsConfig(&scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) { return sseResponse(http.StatusOK, sse, nil), nil }})
			cfg.Protocol = domain.DeepSeekThinkingV1
			stream, err := newTestModel(t, cfg).Stream(context.Background(), modelRequest())
			if err != nil {
				t.Fatal(err)
			}
			events, err := collectStream(t, stream)
			_ = stream.Close()
			if err == nil {
				t.Fatal("invalid/incomplete state accepted")
			}
			for _, event := range events {
				if event.ProviderState != nil || event.Type == engine.StreamEventCompleted {
					t.Fatal("failed state published")
				}
			}
		})
	}
}

func TestDeepSeekLosslessUnicodeAndMultilineBound(t *testing.T) {
	for _, payload := range []string{`{"reasoning_content":"\ud83d\ude00"}`, `{"reasoning_content":"\\ud800"}`, `{"reasoning_content":"你好"}`} {
		if !losslessReplayJSON(payload) {
			t.Fatal("valid reasoning encoding rejected")
		}
	}
	stream := &chatStream{providerState: &domain.ProviderState{}}
	line := "data: " + strings.Repeat("x", 64<<10)
	for index := 0; index < 15; index++ {
		if err := stream.consumeLine(line); err != nil {
			t.Fatal("event rejected before byte bound")
		}
	}
	if err := stream.consumeLine(line); err == nil {
		t.Fatal("multiline bytes escaped bound before JSON dispatch")
	}
}
