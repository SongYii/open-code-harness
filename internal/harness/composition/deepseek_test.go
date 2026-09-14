package composition_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// Real composition, SQLite, HTTP/SSE, workspace tool, rolling summary, restart,
// and recorded request comparison. No live model or paid API is used.
func TestDeepSeekThinkingToolsRestartAndCompaction(t *testing.T) {
	ctx := context.Background()
	config := validConfig(t)
	config.Provider.AdapterKind = "deepseek"
	config.Provider.ReasoningEffort = "high"
	config.Context.SummaryReasoningEffort = "low"
	config.Context.TailPercent = 10
	config.Provider.AllowInsecureLoopback = true
	t.Setenv(config.Provider.APIKeyEnv, "fixture-key")
	var mu sync.Mutex
	var requests [][]map[string]any
	var summaryCalls int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any  `json:"messages"`
			Tools    []json.RawMessage `json:"tools"`
			Thinking struct {
				Type string `json:"type"`
			} `json:"thinking"`
			Effort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if body.Thinking.Type != "enabled" {
			t.Error("thinking not explicitly enabled")
		}
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		writeDelta := func(delta map[string]any, finish any) {
			data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		if r.Header.Get("X-Och-Request-Purpose") == "compaction" {
			summaryCalls++
			encoded, _ := json.Marshal(body.Messages)
			if len(body.Tools) != 0 || bytes.Contains(encoded, []byte("protocol-private-")) || bytes.Contains(encoded, []byte("reasoning_content")) {
				t.Error("summarizer received reasoning or tools")
			}
			if body.Effort != "low" {
				t.Error("summary effort override lost")
			}
			var summary strings.Builder
			for _, heading := range []string{"Objective", "User Constraints", "Established Facts", "Work Completed", "Files and Commands", "Open Work", "Risks and Unknowns", "Continuation"} {
				fmt.Fprintf(&summary, "## %s\n\nFixture fact.\n\n", heading)
			}
			writeDelta(map[string]any{"reasoning_content": "summary-private-canary"}, nil)
			writeDelta(map[string]any{"content": summary.String()}, "stop")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		if len(body.Tools) == 0 || body.Effort != "high" {
			t.Error("conversation tools or effort lost")
		}
		for _, message := range body.Messages {
			if message["role"] == "assistant" {
				if value, ok := message["reasoning_content"].(string); !ok || !strings.HasPrefix(value, "protocol-private-") {
					t.Error("prior assistant state not replayed")
				}
			} else if _, ok := message["reasoning_content"]; ok {
				t.Error("state attached to non-assistant")
			}
		}
		requests = append(requests, body.Messages)
		n := len(requests)
		// Two fragments ensure that state is concatenated, not last-delta wins.
		writeDelta(map[string]any{"reasoning_content": "protocol-private-"}, nil)
		writeDelta(map[string]any{"reasoning_content": fmt.Sprintf("%02d", n)}, nil)
		if n == 1 {
			writeDelta(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call-dir", "type": "function", "function": map[string]any{"name": "list_dir", "arguments": `{"path":"."}`}}}}, "tool_calls")
		} else {
			writeDelta(map[string]any{"content": strings.Repeat("visible answer ", 60)}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	config.Provider.BaseURL = provider.URL
	assembly, err := composition.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if assembly != nil {
			_ = assembly.Close()
		}
	}()
	created, err := assembly.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	run := func(id string) application.RunTurnResult {
		t.Helper()
		sink := &testkit.RecordingSink{}
		result, err := assembly.Service().RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(id), Input: strings.Repeat("user fixture input ", 45), Sink: sink})
		if err != nil || result.Status != domain.TurnStatusCompleted {
			t.Fatalf("RunTurn %s: %v (%s)", id, err, result.Status)
		}
		for _, event := range sink.Delivered() {
			if strings.Contains(event.Text, "protocol-private-") {
				t.Fatal("state leaked into runtime output")
			}
		}
		return result
	}
	run("first") // Tool call + final response.
	if err := assembly.Close(); err != nil {
		t.Fatal(err)
	}
	assembly, err = composition.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	run("after-restart")
	mu.Lock()
	if len(requests) != 3 {
		t.Fatalf("expected tool continuation and restart request, got %d", len(requests))
	}
	var replayed []string
	for _, m := range requests[2] {
		if m["role"] == "assistant" {
			replayed = append(replayed, m["reasoning_content"].(string))
		}
	}
	mu.Unlock()
	if len(replayed) != 2 || replayed[0] != "protocol-private-01" || replayed[1] != "protocol-private-02" {
		t.Fatal("SQLite restart lost tool or final assistant state")
	}
	run("third")
	run("fourth")
	compacted, err := assembly.Service().CompactSession(ctx, application.CompactSessionRequest{SessionID: created.SessionID, Strategy: domain.ContextStrategySummary})
	if err != nil || !compacted.Ran {
		t.Fatalf("manual summary: %v, ran=%t", err, compacted.Ran)
	}
	if err := assembly.Close(); err != nil {
		t.Fatal(err)
	}
	assembly, err = composition.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	result := run("after-compaction-restart")
	// Compare the exact retained state set in the wire to committed history
	// beyond checkpoint coverage, not merely a non-empty-state assertion.
	records, err := application.ReadWholeStreamPinned(ctx, assembly.Store(), created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	for _, record := range records {
		if event, ok := record.Event.(domain.AssistantMessageCompleted); ok && record.Sequence > compacted.ThroughSequence && event.TurnID != result.TurnID {
			expected = append(expected, event.ProviderState.ReasoningContent)
		}
	}
	mu.Lock()
	var actual []string
	for _, m := range requests[len(requests)-1] {
		if m["role"] == "assistant" {
			actual = append(actual, m["reasoning_content"].(string))
		}
	}
	gotSummaryCalls := summaryCalls
	mu.Unlock()
	if len(expected) == 0 || strings.Join(actual, "|") != strings.Join(expected, "|") || gotSummaryCalls == 0 {
		t.Fatal("compacted restart did not replay exactly the committed retained tail")
	}
	var exported bytes.Buffer
	if _, err := composition.ExportSession(ctx, config.DatabasePath, created.SessionID, &exported); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exported.String(), "protocol-private-") || strings.Contains(exported.String(), "summary-private-canary") {
		t.Fatal("display transcript exported protocol state")
	}
	if err := assembly.Close(); err != nil {
		t.Fatal(err)
	}
	assembly = nil
	inspection, err := composition.InspectEvaluationStore(ctx, config.DatabasePath, created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	evidenceDirectory := t.TempDir()
	auditDirectory := filepath.Join(evidenceDirectory, "audit")
	if _, err := composition.ExportEvaluationEvidence(ctx, inspection, composition.EvaluationExportDestinations{TranscriptPath: filepath.Join(evidenceDirectory, "transcript.jsonl"), AuditDirectory: auditDirectory}); err != nil {
		t.Fatal(err)
	}
	audit, err := composition.VerifyAuditSnapshot(auditDirectory)
	if err != nil || len(audit.Sessions) != 1 || !reflect.DeepEqual(audit.Sessions[0].Events, records) {
		t.Fatalf("canonical audit replay lost or changed protocol-bearing events: %v", err)
	}
}

func TestDeepSeekConfigValidation(t *testing.T) {
	for _, effort := range []string{"", "low", "high", "max"} {
		config := validConfig(t)
		config.Provider.AdapterKind = "deepseek"
		config.Provider.ThinkingMode = "enabled"
		config.Provider.ReasoningEffort = effort
		if err := config.Validate(); err != nil {
			t.Fatalf("supported effort %q: %v", effort, err)
		}
	}
	for _, effort := range []string{"none", "medium", "xhigh"} {
		config := validConfig(t)
		config.Provider.AdapterKind = "deepseek"
		config.Context.SummaryReasoningEffort = effort
		if err := config.Validate(); err == nil {
			t.Fatal("unsupported summary effort accepted")
		}
	}
	config := validConfig(t)
	config.Provider.AdapterKind = "deepseek"
	config.Provider.ThinkingMode = "disabled"
	if err := config.Validate(); err == nil {
		t.Fatal("thinking replay may not be silently disabled")
	}
}

func TestDeepSeekFailedStateNeverCommitsOrExecutesTools(t *testing.T) {
	for _, mode := range []string{"valid-control", "missing", "sensitive", "truncated", "length"} {
		t.Run(mode, func(t *testing.T) {
			config := validConfig(t)
			config.Provider.AdapterKind = "deepseek"
			config.Provider.AllowInsecureLoopback = true
			t.Setenv(config.Provider.APIKeyEnv, "fixture-key")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if calls.Add(1) > 1 && mode == "valid-control" {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private-state\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				if mode != "missing" {
					reasoning := "private-state"
					if mode == "sensitive" {
						reasoning = "Authorization: Bearer sk-provider-private-key"
					}
					encoded, _ := json.Marshal(reasoning)
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":%s}}]}\n\n", encoded)
				}
				finish := "tool_calls"
				if mode == "length" {
					finish = "length"
				}
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-dir\",\"type\":\"function\",\"function\":{\"name\":\"list_dir\",\"arguments\":\"{\\\"path\\\":\\\".\\\"}\"}}]},\"finish_reason\":%q}]}\n\n", finish)
				if mode != "truncated" {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer server.Close()
			config.Provider.BaseURL = server.URL
			assembly, err := composition.Open(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer assembly.Close()
			created, err := assembly.Service().CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
			if err != nil {
				t.Fatal(err)
			}
			result, err := assembly.Service().RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "bad-state", Input: "list workspace", Sink: &testkit.RecordingSink{}})
			if mode == "valid-control" {
				if err != nil || !result.TerminalCommitted || result.Status != domain.TurnStatusCompleted || calls.Load() != 2 {
					t.Fatalf("failure-test baseline was not a valid tool roundtrip: %v", err)
				}
				return
			}
			if err == nil || !result.TerminalCommitted || result.Status != domain.TurnStatusFailed {
				t.Fatal("invalid state did not terminalize as failure")
			}
			records, err := application.ReadWholeStreamPinned(context.Background(), assembly.Store(), created.SessionID, 256)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range records {
				switch record.Event.(type) {
				case domain.AssistantMessageCompleted, domain.ToolCallStarted:
					t.Fatal("failed response committed or executed a tool")
				}
				encoded, err := domain.MarshalRecordedEvent(record)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(encoded, []byte("sk-provider-private-key")) || bytes.Contains(encoded, []byte("private-state")) {
					t.Fatal("failed protocol state persisted")
				}
			}
		})
	}
}
