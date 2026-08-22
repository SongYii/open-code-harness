package composition_test

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

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
)

// TestAssemblyRunsAToolCallingTurnEndToEnd is the first test in this
// repository that assembles every implemented slice at once: Domain, the
// Application step loop, the SQLite canonical EventStore, the Runtime Host,
// the OpenAI-compatible provider adapter, the workspace filesystem, and the
// policy engine.
//
// Six slices were individually verified and jointly unproven. This proves the
// joint case, and it proves it over the real code paths: a real database file,
// the adapter's own HTTP and SSE handling against a loopback server, and the
// real policy Decide table rather than an allow-all bypass. No network, no
// credential beyond an environment variable this test sets, and no sleep.
func TestAssemblyRunsAToolCallingTurnEndToEnd(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "assembly-key")

	const fileName = "NOTES.md"
	const fileBody = "the workspace file the model asks for"
	if err := os.WriteFile(filepath.Join(config.WorkspaceRoot, fileName), []byte(fileBody), 0o600); err != nil {
		t.Fatal(err)
	}

	// policy.ModeDefault allows a read without approval, so the turn exercises
	// the real authorization path. A mode that allowed everything would prove
	// only that the wiring compiles.
	config.Policy = policy.ModeDefault

	server := newTwoStepProvider(t, fileName)
	config.Provider.BaseURL = server.URL
	config.Provider.AllowInsecureLoopback = true

	assembly, err := composition.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := assembly.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	ctx := context.Background()
	created, err := assembly.Service().CreateSession(ctx, application.CreateSessionRequest{
		WorkspaceRoot: config.WorkspaceRoot,
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	result, err := assembly.Service().RunTurn(ctx, application.RunTurnRequest{
		SessionID: created.SessionID,
		RequestID: "assembly-turn",
		Input:     "what is in " + fileName + "?",
		Sink:      discardSink{},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Status != domain.TurnStatusCompleted {
		t.Fatalf("RunTurn() status = %q, want %q", result.Status, domain.TurnStatusCompleted)
	}
	if !strings.Contains(result.Text, fileBody) {
		t.Fatalf("RunTurn() text = %q, want it to reflect the file contents %q", result.Text, fileBody)
	}
	if server.requests.Load() != 2 {
		t.Fatalf("provider requests = %d, want 2: one for the tool call and one for the answer", server.requests.Load())
	}

	// The durable stream is the authority. Read it back from the database
	// rather than trusting the in-memory result.
	records, err := application.ReadWholeStreamPinned(ctx, assembly.Store(), created.SessionID, 256)
	if err != nil {
		t.Fatalf("ReadWholeStreamPinned() error = %v", err)
	}
	types := make([]string, 0, len(records))
	for _, record := range records {
		types = append(types, record.Event.EventType())
	}
	for _, wanted := range []string{
		domain.EventSessionCreated,
		domain.EventTurnStarted,
		domain.EventToolCallStarted,
		domain.EventPolicyDecisionRecorded,
		domain.EventToolCallCompleted,
		domain.EventTurnCompleted,
	} {
		if !containsString(types, wanted) {
			t.Fatalf("durable stream = %v, missing %q", types, wanted)
		}
	}

	// Replay is the state authority: the persisted events must reconstruct a
	// session with no turn still running.
	state, err := domain.Replay(records)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if state.Status != domain.SessionStatusActive || state.ActiveTurn != nil {
		t.Fatalf("replayed state = %#v, want an active session with no running turn", state)
	}
}

// TestAssemblyServesACPTurnEndToEnd drives the same assembled harness through
// ACP v1 JSON-RPC over in-memory pipes. Stdio is not involved.
func TestAssemblyServesACPTurnEndToEnd(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "assembly-key")
	const fileName = "NOTES.md"
	const fileBody = "the workspace file the model asks for"
	if err := os.WriteFile(filepath.Join(config.WorkspaceRoot, fileName), []byte(fileBody), 0o600); err != nil {
		t.Fatal(err)
	}
	config.Policy = policy.ModeDefault
	server := newTwoStepProvider(t, fileName)
	config.Provider.BaseURL = server.URL
	config.Provider.AllowInsecureLoopback = true

	assembly, err := composition.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = assembly.Close() })

	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- assembly.ServeACP(context.Background(), agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})

	writeACP(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`)
	_ = readACP(t, clientIn)
	writeACP(t, clientOut, fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":%q}}`, config.WorkspaceRoot))
	created := readACP(t, clientIn)
	sessionID, _ := created["result"].(map[string]any)["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("session/new = %#v", created)
	}
	writeACP(t, clientOut, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":%q}]}}`, sessionID, "what is in "+fileName+"?"))
	for {
		message := readACP(t, clientIn)
		if message["method"] == "session/update" {
			continue
		}
		if message["id"] != float64(3) {
			t.Fatalf("unexpected ACP frame %#v", message)
		}
		if message["error"] != nil {
			t.Fatalf("session/prompt error = %#v", message["error"])
		}
		if message["result"].(map[string]any)["stopReason"] != "end_turn" {
			t.Fatalf("session/prompt = %#v", message)
		}
		break
	}
}

func writeACP(t *testing.T, w io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func readACP(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		t.Fatalf("read ACP: %v", scanner.Err())
	}
	var message map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
		t.Fatalf("ACP frame %q: %v", scanner.Text(), err)
	}
	return message
}

// twoStepProvider answers the first request with a read_file tool call and the
// second with an answer quoting whatever the tool result carried, which is how
// the test observes that the workspace read actually reached the model.
type twoStepProvider struct {
	*httptest.Server
	requests atomic.Int32
}

func newTwoStepProvider(t *testing.T, fileName string) *twoStepProvider {
	t.Helper()
	provider := &twoStepProvider{}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := readAll(r)
		if err != nil {
			http.Error(w, "unreadable request", http.StatusBadRequest)
			return
		}
		turn := provider.requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		if turn == 1 {
			write(w, flusher, fmt.Sprintf(
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_read","function":{"name":"read_file","arguments":%s}}]},"finish_reason":null}]}`,
				jsonString(fmt.Sprintf(`{"path":%q}`, fileName))))
			write(w, flusher, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
			write(w, flusher, "[DONE]")
			return
		}

		// The second request carries the tool result in its messages. Echo it
		// back so the assertion on the final text proves the workspace read
		// travelled the whole loop rather than being fabricated here.
		observed := extractToolResult(body)
		write(w, flusher, fmt.Sprintf(`{"choices":[{"delta":{"content":%s},"finish_reason":null}]}`, jsonString(observed)))
		write(w, flusher, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		write(w, flusher, "[DONE]")
	}))
	t.Cleanup(provider.Server.Close)
	return provider
}

// discardSink accepts runtime events without inspecting them: this test
// asserts on the durable stream, which is the authority, not on delivery.
type discardSink struct{}

func (discardSink) Emit(context.Context, engine.RuntimeEvent) error { return nil }

func write(w http.ResponseWriter, flusher http.Flusher, payload string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher != nil {
		flusher.Flush()
	}
}

func readAll(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(r.Body)
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// extractToolResult pulls the text of the last tool-role message out of the
// request the adapter sent. It parses only what it needs: the assertion is
// about the tool result reaching the provider, not about wire shape, which
// the adapter's own tests already cover.
func extractToolResult(body []byte) string {
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "unparseable request"
	}
	for index := len(payload.Messages) - 1; index >= 0; index-- {
		if payload.Messages[index].Role == "tool" {
			return payload.Messages[index].Content
		}
	}
	return "no tool message"
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
