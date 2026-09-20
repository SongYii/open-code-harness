package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/runtime"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// No injected model/host: Open builds the actual Messages adapter, HTTP client,
// application, context engine, SQLite and heartbeat. The local server holds a
// valid response before its terminal frames, then attempts a late completion.
func TestMessagesInFlightLifecycle(t *testing.T) {
	for _, purpose := range []string{"turn", "summary"} {
		for _, stop := range []string{"complete", "caller_cancel", "close", "lease_loss"} {
			t.Run(purpose+"/"+stop, func(t *testing.T) {
				testMessagesInFlightLifecycle(t, purpose, stop)
			})
		}
	}
}

func testMessagesInFlightLifecycle(t *testing.T, purpose, stop string) {
	config := internalValidConfig(t)
	config.AllowUnsandboxedExec = true
	config.Diagnostics = io.Discard
	config.Provider.AdapterKind = "deepseek-messages"
	config.Provider.AllowInsecureLoopback = true
	config.Policy = policy.ModeReadOnly
	config.Context.TailPercent = 10
	config.ShutdownTimeout = 5 * time.Second
	t.Setenv(config.Provider.APIKeyEnv, "lifecycle-fixture-key")
	server := newMessagesLifecycleServer(t, config.Provider.ModelID, purpose)
	defer server.http.Close()
	config.Provider.BaseURL = server.http.URL + "/anthropic"
	ctx := context.Background()
	assembly, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	operationCtx, cancel := context.WithCancel(ctx)
	defer func() { cancel(); server.unblock(); _ = assembly.Close() }()
	service, cachedStore := assembly.Service(), assembly.Store()
	created, err := service.CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	if purpose == "summary" {
		// Two full turns, with enough coverable source to amortize the fixed
		// summary format. The latest turn remains protected by the real planner.
		for i := 0; i < 2; i++ {
			result, err := service.RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("seed-%d", i)), Input: strings.Repeat("synthetic source material ", 160), Sink: &testkit.RecordingSink{}})
			if err != nil || result.Status != domain.TurnStatusCompleted {
				t.Fatalf("seed failed: %v", err)
			}
		}
	}
	baseline := messagesLifecycleRecords(t, assembly.Store(), created.SessionID)
	baselineHead := baseline[len(baseline)-1].Sequence
	server.armed.Store(true)
	sink := &testkit.RecordingSink{}
	type outcome struct {
		turn    application.RunTurnResult
		compact application.CompactSessionResult
		err     error
	}
	operation := make(chan outcome, 1)
	go func() {
		var out outcome
		if purpose == "summary" {
			out.compact, out.err = service.CompactSession(operationCtx, application.CompactSessionRequest{SessionID: created.SessionID, Strategy: domain.ContextStrategySummary})
		} else {
			out.turn, out.err = service.RunTurn(operationCtx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: "inflight", Input: "List this synthetic workspace.", Sink: sink})
		}
		operation <- out
	}()
	awaitMessagesLifecycle(t, "incomplete response flushed", server.entered)
	assertMessagesLifecycleNoPublication(t, messagesLifecycleRecords(t, assembly.Store(), created.SessionID), baselineHead, sink)
	closed := make(chan error, 1)
	switch stop {
	case "complete":
		server.unblock()
	case "caller_cancel":
		cancel()
	case "close":
		go func() { closed <- assembly.Close() }()
	case "lease_loss":
		store, err := assembly.host.Store()
		if err != nil {
			t.Fatal(err)
		}
		// Change only this fixture database's lease, not event history. The
		// actual renewal must detect expiry and fence the real running Host.
		// Do not call Abandon: that would skip the heartbeat integration gate.
		if err := store.ExpireLeaseForTesting(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if stop == "close" || stop == "lease_loss" {
		awaitMessagesLifecycle(t, "host cancellation", assembly.Done())
		if assembly.Ready() {
			t.Fatal("stopped host still ready")
		}
		if _, err := service.LoadSession(ctx, created.SessionID); !errors.Is(err, runtime.ErrNotReady) {
			t.Fatalf("cached service escaped admission: %v", err)
		}
		if _, err := cachedStore.ReadStream(ctx, application.ReadStreamRequest{SessionID: created.SessionID, Limit: 1}); !errors.Is(err, runtime.ErrNotReady) {
			t.Fatalf("cached store escaped admission: %v", err)
		}
	}
	if stop != "complete" {
		// Require the HTTP request to close and the operation to unwind while
		// the server still withholds message_stop, not because it hit EOF.
		awaitMessagesLifecycle(t, "HTTP request canceled before server release", server.canceled)
	}
	out := awaitMessagesLifecycle(t, "operation termination", operation)
	if stop == "complete" {
		if out.err != nil || purpose == "turn" && out.turn.Status != domain.TurnStatusCompleted || purpose == "summary" && !out.compact.Ran {
			t.Fatalf("valid control did not complete: %+v", out)
		}
	} else {
		if out.err == nil || out.turn.Status == domain.TurnStatusCompleted || out.compact.Ran {
			t.Fatalf("stopped operation reported success: %+v", out)
		}
		if purpose == "turn" && stop != "lease_loss" && (!errors.Is(out.err, context.Canceled) || !out.turn.TerminalCommitted || out.turn.Status != domain.TurnStatusInterrupted) {
			t.Fatalf("cancellation was not durably preserved: status=%s committed=%t error=%v", out.turn.Status, out.turn.TerminalCommitted, out.err)
		}
	}
	if stop == "close" {
		if err := awaitMessagesLifecycle(t, "close before server release", closed); err != nil {
			t.Fatal(err)
		}
	}
	// Now attempt the previously withheld, valid tool offer/summary completion.
	server.unblock()
	awaitMessagesLifecycle(t, "late terminal frames attempted", server.finished)
	if stop == "caller_cancel" || stop == "complete" {
		if !assembly.Ready() {
			t.Fatal("caller completion/cancellation stopped host admission")
		}
		select {
		case <-assembly.Done():
			t.Fatal("caller cancellation signaled host Done")
		default:
		}
	}
	var successor *Assembly
	if stop == "lease_loss" {
		// A different host takes the expired lease and reconciles the unfinished
		// operation. Closing the old host must not expire the successor's lease.
		nextConfig := config
		nextConfig.RuntimeID += "-successor"
		successor, err = Open(ctx, nextConfig)
		if err != nil {
			t.Fatal(err)
		}
		defer successor.Close()
		if err := assembly.Close(); !application.IsStoreCode(err, application.StoreCodeWriterFenced) {
			t.Fatalf("stale Close must report lost ownership: %v", err)
		}
		if assembly.Ready() || !successor.Ready() {
			t.Fatal("old host revived or successor stopped")
		}
	} else if stop == "close" {
		successor, err = Open(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		defer successor.Close()
	} else {
		successor = assembly
	}
	records := messagesLifecycleRecords(t, successor.Store(), created.SessionID)
	if stop == "complete" {
		tools, summaries := 0, 0
		for _, record := range records {
			if record.Sequence <= baselineHead {
				continue
			}
			switch record.Event.(type) {
			case domain.ToolCallStarted:
				tools++
			case domain.ContextCompactionCompleted:
				summaries++
			}
		}
		if purpose == "turn" && tools != 1 || purpose == "summary" && summaries != 1 {
			t.Fatalf("positive control coverage: tools=%d summaries=%d", tools, summaries)
		}
	} else {
		assertMessagesLifecycleNoPublication(t, records, baselineHead, sink)
		interrupted, failedSummaries := 0, 0
		for _, record := range records {
			if record.Sequence <= baselineHead {
				continue
			}
			switch e := record.Event.(type) {
			case domain.TurnInterrupted:
				interrupted++
				if stop == "lease_loss" && e.Reason != "process_crash" {
					t.Fatal("successor did not reconcile interrupted turn")
				}
			case domain.ContextCompactionFailed:
				failedSummaries++
				if stop == "lease_loss" && e.Code != "runtime_recovered" {
					t.Fatal("successor did not reconcile interrupted summary")
				}
			}
		}
		if purpose == "turn" && interrupted != 1 || purpose == "summary" && failedSummaries != 1 {
			t.Fatalf("terminal brackets: interrupted=%d summary_failed=%d", interrupted, failedSummaries)
		}
		state, err := domain.Replay(records)
		if err != nil || state.ActiveTurn != nil || state.ContextCompaction != nil {
			t.Fatalf("cancellation/recovery left active work: %v", err)
		}
	}
	if server.blocked.Load() != 1 {
		t.Fatal("target request retried or fixture missed")
	}
	wantRequests := int32(1)
	if purpose == "summary" {
		wantRequests += 2
	}
	if stop == "complete" && purpose == "turn" {
		wantRequests++
	}
	if server.requests.Load() != wantRequests {
		t.Fatalf("HTTP requests=%d; want %d (no hidden retries)", server.requests.Load(), wantRequests)
	}
	// A new operation needs a successful durable append, not just Ready=true.
	if _, err := successor.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot}); err != nil {
		t.Fatalf("healthy host cannot commit new work: %v", err)
	}
	if stop == "close" || stop == "lease_loss" {
		if _, err := service.CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot}); !errors.Is(err, runtime.ErrNotReady) {
			t.Fatalf("old facade revived after successor: %v", err)
		}
	}
}

func messagesLifecycleRecords(t *testing.T, store application.EventStore, session domain.SessionID) []domain.RecordedEvent {
	t.Helper()
	records, err := application.ReadWholeStreamPinned(context.Background(), store, session, 256)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func assertMessagesLifecycleNoPublication(t *testing.T, records []domain.RecordedEvent, after uint64, sink *testkit.RecordingSink) {
	t.Helper()
	for _, record := range records {
		if record.Sequence <= after {
			continue
		}
		switch record.Event.(type) {
		case domain.AssistantMessageCompleted, domain.ToolCallStarted, domain.ContextCompactionCompleted:
			t.Fatalf("incomplete/stopped stream published %s", record.Event.EventType())
		}
		encoded, err := domain.MarshalRecordedEvent(record)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "lifecycle-uncommitted-") {
			t.Fatal("uncommitted response content entered canonical history")
		}
	}
	for _, event := range sink.Delivered() {
		if strings.Contains(event.Text, "lifecycle-uncommitted-") {
			t.Fatal("partial/late visible text escaped")
		}
	}
}

func awaitMessagesLifecycle[T any](t *testing.T, stage string, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(15 * time.Second):
		t.Fatalf("timeout waiting for %s", stage)
		var zero T
		return zero
	}
}

type messagesLifecycleServer struct {
	http                                 *httptest.Server
	armed                                atomic.Bool
	blocked                              atomic.Int32
	requests                             atomic.Int32
	entered, canceled, release, finished chan struct{}
	once                                 sync.Once
}

func (s *messagesLifecycleServer) unblock() { s.once.Do(func() { close(s.release) }) }

func newMessagesLifecycleServer(t *testing.T, model, purpose string) *messagesLifecycleServer {
	s := &messagesLifecycleServer{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	s.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		target := s.armed.CompareAndSwap(true, false)
		if target {
			s.blocked.Add(1)
			defer close(s.finished)
			go func() {
				select {
				case <-r.Context().Done():
					close(s.canceled)
				case <-s.finished:
				}
			}()
			wantPurpose := "conversation"
			if purpose == "summary" {
				wantPurpose = "compaction"
			}
			gotPurpose := r.Header.Get("X-Och-Request-Purpose")
			if gotPurpose == "" {
				gotPurpose = "conversation"
			} // Engine's documented zero-value purpose.
			if gotPurpose != wantPurpose {
				t.Error("blocked wrong model purpose")
			}
		}
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
		frame("message_start", map[string]any{"message": map[string]any{"id": "lifecycle", "type": "message", "role": "assistant", "model": model, "content": []any{}, "usage": map[string]int{"input_tokens": 12, "output_tokens": 1}}})
		frame("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
		hidden, signature := "lifecycle-history-thinking", "lifecycle-history-signature"
		if target {
			hidden, signature = "lifecycle-uncommitted-thinking", "lifecycle-uncommitted-signature"
		}
		frame("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": hidden}})
		frame("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "signature_delta", "signature": signature}})
		frame("content_block_stop", map[string]any{"index": 0})
		text := "Fixture complete."
		if target {
			text = "lifecycle-uncommitted-text"
			if purpose == "summary" {
				var summary strings.Builder
				for _, heading := range []string{"Objective", "User Constraints", "Established Facts", "Work Completed", "Files and Commands", "Open Work", "Risks and Unknowns", "Continuation"} {
					fmt.Fprintf(&summary, "## %s\nFixture fact.\n", heading)
				}
				text = summary.String()
			}
		}
		frame("content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "text", "text": text}})
		frame("content_block_stop", map[string]any{"index": 1})
		reason := "end_turn"
		if target && purpose == "turn" {
			reason = "tool_use"
			frame("content_block_start", map[string]any{"index": 2, "content_block": map[string]any{"type": "tool_use", "id": "lifecycle-tool", "name": "list_dir", "input": map[string]any{}}})
			frame("content_block_delta", map[string]any{"index": 2, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"path":"."}`}})
			frame("content_block_stop", map[string]any{"index": 2})
		}
		if target {
			w.(http.Flusher).Flush()
			close(s.entered)
			<-s.release // Deliberately ignore request cancellation until released.
		}
		frame("message_delta", map[string]any{"delta": map[string]any{"stop_reason": reason}, "usage": map[string]int{"output_tokens": 20}})
		frame("message_stop", nil)
		w.(http.Flusher).Flush()
	}))
	return s
}
