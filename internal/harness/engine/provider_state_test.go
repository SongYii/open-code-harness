package engine_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	. "github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestRunnerProviderStateIsCompletionOnlyAndDetached(t *testing.T) {
	state := &domain.ProviderState{Protocol: domain.DeepSeekThinkingV1, ModelID: "test", EndpointID: "api.example.com", ReasoningContent: "hidden-state-canary"}
	oversized := domain.CloneProviderState(state)
	oversized.ReasoningContent = strings.Repeat("x", domain.MaxProviderReasoningBytes+1)
	for _, tc := range []struct {
		name     string
		event    StreamEvent
		closeErr error
		succeeds bool
	}{
		{"completed", StreamEvent{Type: StreamEventCompleted, ProviderState: state}, nil, true},
		{"state on delta", StreamEvent{Type: StreamEventTextDelta, Text: "visible", ProviderState: state}, nil, false},
		{"state on tool call", StreamEvent{Type: StreamEventToolCall, ToolCall: &ToolCall{ID: "c1", Name: "read_file", Arguments: "{}"}, ProviderState: state}, nil, false},
		{"oversized", StreamEvent{Type: StreamEventCompleted, ProviderState: oversized}, nil, false},
		{"close failed", StreamEvent{Type: StreamEventCompleted, ProviderState: state}, errors.New("close failed"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, err := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: tc.event}}, CloseError: tc.closeErr})
			if err != nil {
				t.Fatal(err)
			}
			runner, _ := NewTurnRunner(model)
			sink := &testkit.RecordingSink{}
			emitter, _ := NewEmitter(sink, validRunnerCorrelation())
			result, err := runner.Run(context.Background(), runnerRequest(), emitter)
			if tc.succeeds {
				if err != nil || result.ProviderState == nil || result.ProviderState == state || !reflect.DeepEqual(result.ProviderState, state) {
					t.Fatalf("completion state not copied: %v", err)
				}
			} else if err == nil || result.ProviderState != nil {
				t.Fatal("failed run retained state")
			}
			if !tc.succeeds && tc.closeErr == nil && len(sink.Delivered()) != 1 {
				t.Fatal("invalid state was rejected only after visible/tool output escaped")
			}
			for _, ev := range sink.Delivered() {
				if strings.Contains(ev.Text, state.ReasoningContent) {
					t.Fatal("protocol state leaked into runtime output")
				}
			}
		})
	}
}

func TestRunnerCanceledCompletionClearsProviderState(t *testing.T) {
	for _, cancelAt := range []string{"next", "close"} {
		t.Run(cancelAt, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			state := &domain.ProviderState{Protocol: domain.DeepSeekThinkingV1, ModelID: "test", EndpointID: "api.example.com", ReasoningContent: "private"}
			stream := &controlledStream{
				next: func(context.Context) (StreamEvent, error) {
					if cancelAt == "next" {
						cancel()
					}
					return StreamEvent{Type: StreamEventCompleted, ProviderState: state}, nil
				},
				close: func() error {
					if cancelAt == "close" {
						cancel()
					}
					return nil
				},
			}
			runner, _ := NewTurnRunner(&controlledModel{stream: func(context.Context, ModelRequest) (ModelStream, error) { return stream, nil }})
			emitter, _ := NewEmitter(&testkit.RecordingSink{}, validRunnerCorrelation())
			result, err := runner.Run(ctx, runnerRequest(), emitter)
			if !IsCode(err, CodeCanceled) || result.ProviderState != nil || stream.closeCalls != 1 {
				t.Fatal("canceled completion retained state or failed to close")
			}
		})
	}
}
