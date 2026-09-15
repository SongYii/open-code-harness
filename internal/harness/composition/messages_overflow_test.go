package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

const messagesOverflowError = `{"error":{"type":"invalid_request_error","message":"This model's maximum context length is 5000 tokens. However, you requested 5100 tokens (4076 in the messages, 1024 in the completion). Please reduce the length of the messages or completion.","param":null,"code":"invalid_request_error"},"request_id":"private-overflow-canary"}`

func messagesOverflowSummary() string {
	var summary strings.Builder
	for _, heading := range []string{"Objective", "User Constraints", "Established Facts", "Work Completed", "Files and Commands", "Open Work", "Risks and Unknowns", "Continuation"} {
		fmt.Fprintf(&summary, "## %s\nSynthetic fact.\n", heading)
	}
	return summary.String()
}

func TestMessagesNativeOverflowDurableReplay(t *testing.T) {
	t.Setenv(crashKeyEnv, "local-fixture-only")
	for _, mode := range []string{"recovered", "exhausted", "summary_rejected", "stream_error"} {
		t.Run(mode, func(t *testing.T) {
			var armed atomic.Bool
			var summaries atomic.Int32
			var mu sync.Mutex
			var requests [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				r.Body.Close()
				if err != nil {
					t.Error(err)
				}
				if !armed.Load() {
					writeCrashMessages(t, w, "test-model", "Synthetic completed response.", false)
					return
				}
				reject := func() {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(400)
					_, _ = io.WriteString(w, messagesOverflowError)
				}
				if r.Header.Get("X-Och-Request-Purpose") == "compaction" {
					summaries.Add(1)
					if mode == "summary_rejected" {
						reject()
					} else {
						writeCrashMessages(t, w, "test-model", messagesOverflowSummary(), false)
					}
					return
				}
				mu.Lock()
				requests = append(requests, body)
				n := len(requests)
				mu.Unlock()
				if mode == "stream_error" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"id":"fixture","type":"message","role":"assistant","model":"test-model","content":[],"usage":{"input_tokens":12,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"private-overflow-canary"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

`)
					_, _ = io.WriteString(w, "event: error\ndata: "+messagesOverflowError+"\n\n")
				} else if n == 1 || mode == "exhausted" {
					reject()
				} else {
					writeCrashMessages(t, w, "test-model", "Recovered response.", false)
				}
			}))
			defer server.Close()
			config := internalValidConfig(t)
			config.AllowUnsandboxedExec = true
			config.Diagnostics = io.Discard
			config.Provider.AdapterKind, config.Provider.APIKeyEnv = "deepseek-messages", crashKeyEnv
			config.Provider.BaseURL, config.Provider.AllowInsecureLoopback = server.URL+"/anthropic", true
			config.Context.TargetPercent, config.Context.TailPercent = 30, 10
			config.Context.MaxOverflowCompactionsPerTurn = 1
			ctx := context.Background()
			assembly, err := Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer assembly.Close()
			created, err := assembly.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				out, err := assembly.Service().RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("seed-%d", i)), Input: strings.Repeat("synthetic source material ", 160), Sink: &testkit.RecordingSink{}})
				if err != nil || out.Status != domain.TurnStatusCompleted {
					t.Fatalf("seed: %v", err)
				}
			}
			baseline := len(messagesLifecycleRecords(t, assembly.Store(), created.SessionID))
			armed.Store(true)
			sink := &testkit.RecordingSink{}
			request := application.RunTurnRequest{SessionID: created.SessionID, RequestID: "native-overflow", Input: "Continue the synthetic task.", Sink: sink}
			first, runErr := assembly.Service().RunTurn(ctx, request)
			if !first.TerminalCommitted || mode == "recovered" && (runErr != nil || first.Status != domain.TurnStatusCompleted) || mode != "recovered" && (runErr == nil || first.Status != domain.TurnStatusFailed) {
				t.Fatalf("native overflow terminal: %s / %v", first.Status, runErr)
			}
			records := messagesLifecycleRecords(t, assembly.Store(), created.SessionID)
			tail := records[baseline:]
			var preparations []domain.ContextPreparedRecorded
			var attempts []domain.ModelRequestRecorded
			for _, record := range tail {
				switch e := record.Event.(type) {
				case domain.ContextPreparedRecorded:
					preparations = append(preparations, e)
				case domain.ModelRequestRecorded:
					attempts = append(attempts, e)
				case domain.ContextCompactionStarted:
					if e.Trigger != domain.ContextTriggerOverflowRetry {
						t.Fatal("fixture compacted for a different reason")
					}
				}
			}
			wantAttempts, wantSummaries, wantCompleted := 2, 1, 1
			if mode == "summary_rejected" {
				wantAttempts, wantCompleted = 1, 0
			}
			if mode == "stream_error" {
				wantAttempts, wantSummaries, wantCompleted = 1, 0, 0
			}
			mu.Lock()
			captured := append([][]byte(nil), requests...)
			mu.Unlock()
			if len(attempts) != wantAttempts || len(preparations) != wantAttempts || len(captured) != wantAttempts || int(summaries.Load()) != wantSummaries || crashEventCount(tail, domain.EventContextCompactionStarted) != wantSummaries || crashEventCount(tail, domain.EventContextCompactionCompleted) != wantCompleted {
				t.Fatalf("wrong bounded recovery: attempts=%d HTTP=%d summaries=%d completed=%d", len(attempts), len(captured), summaries.Load(), crashEventCount(tail, domain.EventContextCompactionCompleted))
			}
			if wantAttempts == 2 {
				if attempts[0].ItemID != attempts[1].ItemID || attempts[0].AttemptIndex != 1 || attempts[1].AttemptIndex != 2 || attempts[0].ContextDecisionID == attempts[1].ContextDecisionID || preparations[1].Trigger != domain.ContextTriggerOverflowRetry || preparations[1].EstimatedTotalTokens*10 > preparations[0].EstimatedTotalTokens*9 || len(captured[1]) >= len(captured[0]) {
					t.Fatal("retry identity or real request shrink contract violated")
				}
			}
			encoded, err := json.Marshal(records)
			if err != nil || strings.Contains(string(encoded)+fmt.Sprint(runErr)+fmt.Sprint(sink.Attempts()), "private-overflow-canary") {
				t.Fatal("overflow error body escaped into durable/runtime output")
			}
			if err := assembly.Close(); err != nil {
				t.Fatal(err)
			}
			server.Close()
			config.RuntimeID = "native-overflow-successor"
			again, err := Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			replayed, replayErr := again.Service().RunTurn(ctx, request)
			if replayed.Status != first.Status || replayed.Text != first.Text || !replayed.TerminalCommitted {
				t.Fatalf("cold overflow terminal changed: %v", replayErr)
			}
			if mode == "recovered" {
				if replayErr != nil {
					t.Fatal(replayErr)
				}
			} else {
				var failure *application.Error
				want := "context_overflow"
				if mode == "stream_error" {
					want = "provider_permanent"
				}
				if !errors.As(replayErr, &failure) || failure.Code != want || !failure.TerminalCommitted {
					t.Fatalf("cold failure classification changed: %v", replayErr)
				}
			}
			if !reflect.DeepEqual(records, messagesLifecycleRecords(t, again.Store(), created.SessionID)) || summaries.Load() != int32(wantSummaries) {
				t.Fatal("cold retry appended or dispatched work")
			}
		})
	}
}
