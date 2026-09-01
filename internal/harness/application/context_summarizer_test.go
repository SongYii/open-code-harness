package application_test

import (
	"context"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestEngineContextSummarizerSendsCompactionPurposeAndNoTools(t *testing.T) {
	expected := engine.ModelRequest{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1",
		Input: "summarize this", Purpose: engine.ModelRequestPurposeCompaction, MaxOutputTokens: 128,
	}
	model, err := testkit.NewScriptedModel(expected, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
		{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "## Objective\nsummary"}},
		{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	summarizer, err := application.NewEngineContextSummarizer(runner)
	if err != nil {
		t.Fatal(err)
	}

	result, err := summarizer.Summarize(context.Background(), application.ContextSummarizeRequest{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Content: "summarize this",
		MaxOutputTokens: 128, MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if result.Text != "## Objective\nsummary" {
		t.Fatalf("Summarize() text = %q", result.Text)
	}
	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("model received %d calls, want 1", len(calls))
	}
	if calls[0].Purpose != engine.ModelRequestPurposeCompaction {
		t.Fatalf("Purpose = %q, want compaction", calls[0].Purpose)
	}
	if len(calls[0].Tools) != 0 {
		t.Fatalf("Tools = %#v, want none: a compaction attempt must never send Tools", calls[0].Tools)
	}
}

func TestEngineContextSummarizerRejectsToolCallFromStream(t *testing.T) {
	expected := engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "summarize", Purpose: engine.ModelRequestPurposeCompaction}
	model, err := testkit.NewScriptedModel(expected, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
		{Event: engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-1", Name: "read_file", Arguments: "{}"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	summarizer, err := application.NewEngineContextSummarizer(runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := summarizer.Summarize(context.Background(), application.ContextSummarizeRequest{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Content: "summarize", MaxOutputBytes: 1024,
	}); !engine.IsCode(err, engine.CodeInvalidStream) {
		t.Fatalf("Summarize() error = %v, want engine.CodeInvalidStream", err)
	}
}

func TestNewEngineContextSummarizerRejectsNilRunner(t *testing.T) {
	if _, err := application.NewEngineContextSummarizer(nil); !application.IsCategory(err, application.CategoryValidation) {
		t.Fatalf("NewEngineContextSummarizer(nil) error = %v, want CategoryValidation", err)
	}
}
