package engine_test

import (
	"context"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	. "github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func collectRequest() CollectRequest {
	return CollectRequest{
		ModelRequest:   ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "summarize", Purpose: ModelRequestPurposeCompaction},
		MaxOutputBytes: 64,
	}
}

func TestTurnRunnerCollectReturnsConcatenatedText(t *testing.T) {
	model, err := testkit.NewScriptedModel(collectRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
		{Event: StreamEvent{Type: StreamEventTextDelta, Text: "hello "}},
		{Event: StreamEvent{Type: StreamEventTextDelta, Text: "world"}},
		{Event: StreamEvent{Type: StreamEventCompleted}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}

	got, err := runner.Collect(context.Background(), collectRequest())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got.Text != "hello world" {
		t.Fatalf("Collect() text = %q, want %q", got.Text, "hello world")
	}
	assertRunnerCounts(t, model, 3, 1)
}

func TestTurnRunnerCollectRejectsToolCall(t *testing.T) {
	model, err := testkit.NewScriptedModel(collectRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
		{Event: StreamEvent{Type: StreamEventToolCall, ToolCall: &ToolCall{ID: "call-1", Name: "read_file", Arguments: "{}"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}

	got, err := runner.Collect(context.Background(), collectRequest())
	if !IsCode(err, CodeInvalidStream) {
		t.Fatalf("Collect() error = %v, want invalid_stream", err)
	}
	if got.Text != "" {
		t.Fatalf("Collect() text = %q, want empty on tool_call rejection", got.Text)
	}
	assertRunnerCounts(t, model, 1, 1)
}

func TestTurnRunnerCollectRejectsRequestWithTools(t *testing.T) {
	request := collectRequest()
	request.Tools = []domain.ToolSchema{{Name: "read_file"}}
	model, err := testkit.NewScriptedModel(request.ModelRequest, testkit.ScriptedModelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runner.Collect(context.Background(), request); !IsCode(err, CodeInvalidRequest) {
		t.Fatalf("Collect() error = %v, want invalid_request", err)
	}
	if calls := model.NextCalls(); calls != 0 {
		t.Fatalf("NextCalls() = %d, want 0 (no stream started for a Tools-bearing collect request)", calls)
	}
}

func TestTurnRunnerCollectAcceptsMessagesOnlyRequest(t *testing.T) {
	request := CollectRequest{
		ModelRequest: ModelRequest{
			SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1",
			Messages: []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: "summarize this"}},
			Purpose:  ModelRequestPurposeCompaction,
		},
		MaxOutputBytes: 64,
	}
	model, err := testkit.NewScriptedModel(request.ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
		{Event: StreamEvent{Type: StreamEventCompleted}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}

	got, err := runner.Collect(context.Background(), request)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got.Text != "" {
		t.Fatalf("Collect() text = %q, want empty", got.Text)
	}
}

func TestTurnRunnerCollectRejectsRequestWithNeitherInputNorMessages(t *testing.T) {
	request := collectRequest()
	request.Input = ""
	model, err := testkit.NewScriptedModel(request.ModelRequest, testkit.ScriptedModelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Collect(context.Background(), request); !IsCode(err, CodeInvalidRequest) {
		t.Fatalf("Collect() error = %v, want invalid_request", err)
	}
}

func TestTurnRunnerCollectEnforcesOutputLimit(t *testing.T) {
	model, err := testkit.NewScriptedModel(collectRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
		{Event: StreamEvent{Type: StreamEventTextDelta, Text: "this text is longer than the configured byte cap"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	request := collectRequest()
	request.MaxOutputBytes = 8
	if _, err := runner.Collect(context.Background(), request); !IsCode(err, CodeOutputLimit) {
		t.Fatalf("Collect() error = %v, want output_limit", err)
	}
}

func TestTurnRunnerCollectMapsStartupFailure(t *testing.T) {
	model, err := testkit.NewScriptedModel(collectRequest().ModelRequest, testkit.ScriptedModelConfig{
		StartupError: &ProviderFailure{Class: FailureClassPermanent, Code: "provider_permanent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Collect(context.Background(), collectRequest()); !IsCode(err, CodeModelStartup) {
		t.Fatalf("Collect() error = %v, want model_startup", err)
	}
}

func TestTurnRunnerCollectHonorsCancellation(t *testing.T) {
	model, err := testkit.NewScriptedModel(collectRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
		{WaitForCancel: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.Collect(ctx, collectRequest()); !IsCode(err, CodeCanceled) {
		t.Fatalf("Collect() error = %v, want canceled", err)
	}
}

func TestTurnRunnerCollectRejectsNilDependencies(t *testing.T) {
	if _, err := (&TurnRunner{}).Collect(context.Background(), collectRequest()); !IsCode(err, CodeInvalidRequest) {
		t.Fatalf("Collect() on zero-value runner error = %v, want invalid_request", err)
	}
	model, err := testkit.NewScriptedModel(collectRequest().ModelRequest, testkit.ScriptedModelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil {
		t.Fatal("NewTurnRunner returned nil runner")
	}
}
