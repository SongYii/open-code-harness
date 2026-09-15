package composition_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestMessagesInvalidToolInputCannotCommitOrExecute(t *testing.T) {
	ctx := context.Background()
	config := validConfig(t)
	config.Provider.AdapterKind = "deepseek-messages"
	config.Provider.AllowInsecureLoopback = true
	t.Setenv(config.Provider.APIKeyEnv, "fixture-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeMessagesFrame(w, "message_start", map[string]any{"message": map[string]any{"id": "m", "type": "message", "role": "assistant", "model": config.Provider.ModelID, "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}}})
		writeMessagesFrame(w, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": "partial-must-not-escape"}})
		writeMessagesFrame(w, "content_block_stop", map[string]any{"index": 0})
		writeMessagesFrame(w, "content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "tool_use", "id": "call_dir", "name": "list_dir", "input": map[string]any{}}})
		writeMessagesFrame(w, "content_block_delta", map[string]any{"index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"path":`}})
		writeMessagesFrame(w, "content_block_stop", map[string]any{"index": 1})
		writeMessagesFrame(w, "message_delta", map[string]any{"delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": 2}})
		writeMessagesFrame(w, "message_stop", nil)
	}))
	defer server.Close()
	config.Provider.BaseURL = server.URL + "/anthropic"
	assembly, err := composition.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer assembly.Close()
	created, err := assembly.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	sink := &testkit.RecordingSink{}
	result, err := assembly.Service().RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "invalid-native-input", Input: "list workspace", Sink: sink})
	if err == nil || !result.TerminalCommitted || result.Status != domain.TurnStatusFailed {
		t.Fatal("invalid native input was not terminalized")
	}
	for _, e := range sink.Delivered() {
		if e.Text == "partial-must-not-escape" {
			t.Fatal("partial response reached runtime")
		}
	}
	records, err := application.ReadWholeStreamPinned(ctx, assembly.Store(), created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		switch record.Event.(type) {
		case domain.AssistantMessageCompleted, domain.ToolCallStarted:
			t.Fatal("invalid input committed or executed a tool")
		}
		encoded, err := domain.MarshalRecordedEvent(record)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte("partial-must-not-escape")) {
			t.Fatal("failed response persisted")
		}
	}
}
