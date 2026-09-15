package composition

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestMessagesProcessCrashHelper(t *testing.T) {
	root, boundary := os.Getenv("OCH_CRASH_CHILD_ROOT"), os.Getenv("OCH_CRASH_CHILD_BOUNDARY")
	if root == "" || boundary == "" {
		t.Skip("subprocess fixture only")
	}
	midTurn := strings.HasPrefix(boundary, "midturn_")
	overflow := strings.HasPrefix(boundary, "overflow_")
	config := Config{
		WorkspaceRoot: filepath.Join(root, "workspace"),
		DatabasePath:  filepath.Join(root, "harness.db"),
		Provider:      Provider{ModelID: "test-model", ContextWindow: 8192, MaxOutput: 1024},
	}
	if err := os.Mkdir(config.WorkspaceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	config.RuntimeID = "crash-child"
	config.AllowUnsandboxedExec = true
	config.Diagnostics = io.Discard
	config.Policy = policy.ModeAllowWrites
	config.Context.TailPercent = 10
	if midTurn || overflow {
		config.Context.TargetPercent = 30
		config.Context.MaxOverflowCompactionsPerTurn = 1
	}
	if midTurn {
		if err := os.WriteFile(filepath.Join(config.WorkspaceRoot, "large.txt"), []byte(strings.Repeat("bounded tool result\n", 200)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config.Provider.AdapterKind = "deepseek-messages"
	config.Provider.AllowInsecureLoopback = true
	config.Provider.APIKeyEnv = crashKeyEnv
	t.Setenv(crashKeyEnv, "local-fixture-only")
	ctx := context.Background()
	var armed, hit, overflowed atomic.Bool
	var requests atomic.Int32
	var store *sqlite.Store
	var point crashCheckpoint
	var releasedHead uint64
	var expectedCommit string
	// A released before-publish hook must be followed by the intended event,
	// not merely some intervening append (e.g. usage). Controls enforce this.
	checkpoint := func(before bool, expected string) {
		if !hit.CompareAndSwap(false, true) {
			t.Fatal("checkpoint reached twice")
		}
		records := messagesLifecycleRecords(t, store, point.Session)
		if before {
			releasedHead = uint64(len(records))
			expectedCommit = expected
		}
		encoded, err := json.Marshal(point)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(crashPrefix + string(encoded))
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || line != "continue\n" {
			t.Fatalf("checkpoint release failed: %v", err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		target := armed.CompareAndSwap(true, false)
		summaryRequest := r.Header.Get("X-Och-Request-Purpose") == "compaction"
		if overflow && target && !summaryRequest {
			overflowed.Store(true)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = io.WriteString(w, messagesOverflowError)
			return
		}
		overflowRetry := boundary == "overflow_retry_before_commit" && overflowed.Load() && !summaryRequest
		summaryBoundary := strings.HasPrefix(boundary, "summary_") || midTurn || overflow && boundary != "overflow_retry_before_commit"
		if target && boundary == "assistant_before_commit" || summaryBoundary && summaryRequest || overflowRetry {
			want := domain.EventAssistantMessageCompleted
			if summaryBoundary {
				want = domain.EventContextCompactionCompleted
				if r.Header.Get("X-Och-Request-Purpose") != "compaction" {
					t.Error("wrong request purpose")
				}
			}
			if boundary == "summary_after_commit" || boundary == "midturn_after_commit" || boundary == "overflow_after_commit" {
				store.SetCommitHook("after_publish", func() {
					if !hit.Load() {
						records := messagesLifecycleRecords(t, store, point.Session)
						if records[len(records)-1].Event.EventType() != want {
							t.Fatal("wrong post-summary commit")
						}
						checkpoint(false, want)
					}
				})
			} else {
				store.SetCommitHook("before_publish", func() {
					if !hit.Load() {
						checkpoint(true, want)
					}
				})
			}
		}
		text := "Synthetic completed response."
		tool := target && strings.HasPrefix(boundary, "tool_")
		if summaryRequest {
			text = messagesOverflowSummary()
		}
		if midTurn && target && !summaryRequest {
			writeCrashMessagesTool(t, w, config.Provider.ModelID, text, "read_file", map[string]string{"path": "large.txt"})
		} else {
			writeCrashMessages(t, w, config.Provider.ModelID, text, tool)
		}
	}))
	defer server.Close()
	config.Provider.BaseURL = server.URL + "/anthropic"
	assembly, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer assembly.Close()
	store, err = assembly.host.Store()
	if err != nil {
		t.Fatal(err)
	}
	created, err := assembly.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(boundary, "summary_") || midTurn || overflow {
		for i := 0; i < 2; i++ {
			out, err := assembly.Service().RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("seed-%d", i)), Input: strings.Repeat("synthetic source material ", 160), Sink: &testkit.RecordingSink{}})
			if err != nil || out.Status != domain.TurnStatusCompleted || hit.Load() {
				t.Fatalf("summary seed: %v", err)
			}
		}
	}
	config.Diagnostics = nil // Only serializable fixture configuration crosses IPC.
	point = crashCheckpoint{Session: created.SessionID, Baseline: len(messagesLifecycleRecords(t, store, created.SessionID)), Boundary: boundary, Config: config}
	if boundary == "audit_before_export" {
		// Freeze a real baseline replica by leaving periodic export disabled.
		// Only the successor runs the production exporter again.
		if _, err := store.ExportOnce(ctx, sqlite.ExportConfig{Directory: filepath.Join(root, "audit")}); err != nil {
			t.Fatal(err)
		}
	}
	store.SetCommitHook("after_publish", func() {
		records := messagesLifecycleRecords(t, store, created.SessionID)
		if expectedCommit != "" && uint64(len(records)) > releasedHead {
			if crashEventCount(records[releasedHead:], expectedCommit) != 1 {
				t.Fatalf("precommit hook stopped before wrong batch; wanted %s", expectedCommit)
			}
			expectedCommit = ""
		}
		if hit.Load() {
			return
		}
		kind := records[len(records)-1].Event.EventType()
		if boundary == "tool_before_execution" && kind == domain.EventToolCallStarted || boundary == "audit_before_export" && kind == domain.EventTurnCompleted {
			checkpoint(false, kind)
		}
	})
	if boundary == "tool_after_effect" {
		store.SetCommitHook("before_publish", func() {
			if hit.Load() {
				return
			}
			if _, err := os.Stat(filepath.Join(config.WorkspaceRoot, "effect.txt")); err == nil {
				checkpoint(true, domain.EventToolCallCompleted)
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
	armed.Store(true)
	if strings.HasPrefix(boundary, "summary_") {
		result, err := assembly.Service().CompactSession(ctx, application.CompactSessionRequest{SessionID: created.SessionID, Strategy: domain.ContextStrategySummary})
		if err != nil || !result.Ran {
			t.Fatalf("summary did not complete: %v", err)
		}
	} else {
		result, err := assembly.Service().RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "crash-request", Input: crashInput, Sink: &testkit.RecordingSink{}})
		if err != nil || result.Status != domain.TurnStatusCompleted {
			t.Fatalf("turn did not complete: %v", err)
		}
	}
	if !hit.Load() || expectedCommit != "" {
		for _, record := range messagesLifecycleRecords(t, store, created.SessionID) {
			if e, ok := record.Event.(domain.ContextPreparedRecorded); ok {
				t.Logf("preparation: trigger=%s tokens=%d", e.Trigger, e.EstimatedTotalTokens)
			}
		}
		t.Fatal("operation missed intended commit boundary")
	}
	wantRequests := int32(1)
	if strings.HasPrefix(boundary, "tool_") {
		wantRequests = 2
	}
	if strings.HasPrefix(boundary, "summary_") {
		wantRequests = 3
	}
	if midTurn {
		wantRequests = 5 // Two history turns, tool offer, summary, final answer.
		records := messagesLifecycleRecords(t, store, created.SessionID)[point.Baseline:]
		if crashEventCount(records, domain.EventContextCompactionStarted) != 1 || crashEventCount(records, domain.EventContextCompactionCompleted) != 1 || crashEventCount(records, domain.EventToolCallCompleted) != 1 {
			t.Fatal("mid-turn control missed compaction/tool bracket")
		}
		for _, record := range records {
			if e, ok := record.Event.(domain.ContextCompactionStarted); ok && e.Trigger != domain.ContextTriggerMidTurn {
				t.Fatal("fixture compacted outside mid-turn boundary")
			}
		}
	}
	if overflow {
		wantRequests = 5 // Two history turns, HTTP rejection, summary, retry.
		records := messagesLifecycleRecords(t, store, created.SessionID)[point.Baseline:]
		if !overflowed.Load() || crashEventCount(records, domain.EventContextCompactionStarted) != 1 || crashEventCount(records, domain.EventContextCompactionCompleted) != 1 || crashEventCount(records, domain.EventModelRequestRecorded) != 2 {
			t.Fatal("overflow control missed compaction/retry bracket")
		}
	}
	if requests.Load() != wantRequests {
		t.Fatalf("unexpected requests: %d want %d", requests.Load(), wantRequests)
	}
	if err := assembly.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeCrashMessages(t *testing.T, w http.ResponseWriter, model, text string, tool bool) {
	t.Helper()
	name := ""
	if tool {
		name = "write_file"
	}
	writeCrashMessagesTool(t, w, model, text, name, map[string]string{"path": "effect.txt", "content": crashEffect})
}

func writeCrashMessagesTool(t *testing.T, w http.ResponseWriter, model, text, tool string, arguments map[string]string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	frame := func(name string, fields map[string]any) {
		if fields == nil {
			fields = map[string]any{}
		}
		fields["type"] = name
		encoded, err := json.Marshal(fields)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, encoded)
	}
	frame("message_start", map[string]any{"message": map[string]any{"id": "crash-fixture", "type": "message", "role": "assistant", "model": model, "content": []any{}, "usage": map[string]int{"input_tokens": 12, "output_tokens": 1}}})
	frame("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "thinking", "thinking": "synthetic private thinking"}})
	frame("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "synthetic-signature"}})
	frame("content_block_stop", map[string]any{"index": 0})
	frame("content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "text", "text": text}})
	frame("content_block_stop", map[string]any{"index": 1})
	reason := "end_turn"
	if tool != "" {
		reason = "tool_use"
		frame("content_block_start", map[string]any{"index": 2, "content_block": map[string]any{"type": "tool_use", "id": "crash-tool", "name": tool, "input": map[string]any{}}})
		args, _ := json.Marshal(arguments)
		frame("content_block_delta", map[string]any{"index": 2, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(args)}})
		frame("content_block_stop", map[string]any{"index": 2})
	}
	frame("message_delta", map[string]any{"delta": map[string]any{"stop_reason": reason}, "usage": map[string]int{"output_tokens": 20}})
	frame("message_stop", nil)
}
