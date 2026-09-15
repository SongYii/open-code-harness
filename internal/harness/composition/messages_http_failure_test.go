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
	"sync/atomic"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// HTTP status alone does not identify model context overflow. The Messages
// adapter deliberately never reads error bodies; the generic Application
// overflow recovery tests must not be mistaken for native route coverage.
func TestMessagesHTTPFailureDurableReplay(t *testing.T) {
	t.Setenv(crashKeyEnv, "local-fixture-only")
	for _, status := range []int{400, 413, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			const canary = "private-error-body-must-not-be-persisted"
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				r.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"prompt is too long %s"}}`, canary)
			}))
			defer server.Close()
			config := internalValidConfig(t)
			config.AllowUnsandboxedExec = true
			config.Diagnostics = io.Discard
			config.Provider.AdapterKind = "deepseek-messages"
			config.Provider.BaseURL = server.URL + "/anthropic"
			config.Provider.AllowInsecureLoopback = true
			config.Provider.APIKeyEnv = crashKeyEnv
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
			sink := &testkit.RecordingSink{}
			request := application.RunTurnRequest{SessionID: created.SessionID, RequestID: "http-failure", Input: "synthetic request", Sink: sink}
			first, err := assembly.Service().RunTurn(ctx, request)
			var failure *engine.ProviderFailure
			if !errors.As(err, &failure) || failure.HTTPStatus != status || failure.Code != "provider_permanent" || failure.Retryable || first.Status != domain.TurnStatusFailed || !first.TerminalCommitted {
				t.Fatalf("unexpected HTTP terminal: %+v / %v", first, err)
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatal("error body escaped through the returned error")
			}
			records := messagesLifecycleRecords(t, assembly.Store(), created.SessionID)
			for _, kind := range []string{domain.EventContextCompactionStarted, domain.EventAssistantMessageCompleted, domain.EventToolCallStarted} {
				if crashEventCount(records, kind) != 0 {
					t.Fatalf("HTTP failure caused %s", kind)
				}
			}
			if crashEventCount(records, domain.EventModelRequestRecorded) != 1 || calls.Load() != 1 {
				t.Fatal("HTTP failure was retried")
			}
			encoded, err := json.Marshal(records)
			if err != nil || strings.Contains(string(encoded), canary) || strings.Contains(fmt.Sprint(sink.Attempts()), canary) {
				t.Fatalf("error body escaped HTTP boundary: %v", err)
			}
			if err := assembly.Close(); err != nil {
				t.Fatal(err)
			}
			server.Close() // Cold replay must work without a reachable provider.
			config.RuntimeID = "http-failure-successor"
			again, err := Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			replayed, err := again.Service().RunTurn(ctx, request)
			var terminal *application.Error
			if !errors.As(err, &terminal) || terminal.Code != "provider_permanent" || !terminal.TerminalCommitted || replayed.Status != first.Status || !replayed.TerminalCommitted {
				t.Fatalf("cold HTTP failure replay: %+v / %v", replayed, err)
			}
			if calls.Load() != 1 || !reflect.DeepEqual(records, messagesLifecycleRecords(t, again.Store(), created.SessionID)) {
				t.Fatal("cold replay dispatched or appended work")
			}
		})
	}
}
