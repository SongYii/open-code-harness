package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// Each response has distinct visible text, tool ID, arguments, usage and
// (Messages only) replay state. Pausing after headers proves two live requests
// exist before cancellation; the server also witnesses cancellation of A.
func TestProviderDistinctRequestsAndHTTP2Isolation(t *testing.T) {
	for _, kind := range []string{"chat", "messages"} {
		for _, protocol := range []string{"http1", "http2"} {
			for _, cancelA := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/cancel=%t", kind, protocol, cancelA), func(t *testing.T) {
					testProviderIsolation(t, kind, protocol, cancelA)
				})
			}
		}
	}
}

type isolationArrival struct {
	name, address string
	protocol      int
}
type isolationResult struct {
	text      string
	calls     []engine.ToolCall
	completed *engine.StreamEvent
	err       error
}

func testProviderIsolation(t *testing.T, kind, protocol string, cancelA bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	arrivals := make(chan isolationArrival, 2)
	cancelled := make(chan string, 2)
	closed := make(chan string, 8)
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if len(request.Messages) != 1 {
			t.Errorf("unexpected request: %#v", request)
			return
		}
		name := ""
		if kind == "chat" {
			_ = json.Unmarshal(request.Messages[0].Content, &name)
		} else {
			var blocks []struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(request.Messages[0].Content, &blocks)
			if len(blocks) == 1 {
				name = blocks[0].Text
			}
		}
		if name != "A" && name != "B" {
			t.Errorf("request isolation lost: %q", name)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		arrivals <- isolationArrival{name: name, address: r.RemoteAddr, protocol: r.ProtoMajor}
		release := releaseA
		if name == "B" {
			release = releaseB
		}
		select {
		case <-release:
			fmt.Fprint(w, isolationWire(kind, name))
		case <-r.Context().Done():
			cancelled <- name
		case <-ctx.Done():
		}
	}))
	server.Config.ConnState = func(c net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- c.RemoteAddr().String()
		}
	}
	if protocol == "http2" {
		server.EnableHTTP2 = true
		server.StartTLS()
	} else {
		server.Start()
	}
	defer server.Close()
	defer cancel() // Unblock server handlers before Server.Close on failure.
	client := server.Client()
	if protocol == "http2" {
		client.Transport.(*http.Transport).ForceAttemptHTTP2 = true
	}
	model := resourceModel(t, kind, server.URL, client)
	defer model.Close()
	aCtx, stopA := context.WithCancel(ctx)
	defer stopA()
	a, err := model.Stream(aCtx, isolationRequest("A"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	first := awaitIsolation(t, ctx, arrivals)
	b, err := model.Stream(ctx, isolationRequest("B"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	second := awaitIsolation(t, ctx, arrivals)
	if first.name != "A" || second.name != "B" {
		t.Fatal("request names crossed")
	}
	wantProtocol := 1
	if protocol == "http2" {
		wantProtocol = 2
	}
	if first.protocol != wantProtocol || second.protocol != wantProtocol {
		t.Fatal("requested HTTP version was not actually negotiated")
	}
	if protocol == "http2" && first.address != second.address {
		t.Fatal("HTTP2 requests did not multiplex over one connection")
	}
	aDone, bDone := make(chan isolationResult, 1), make(chan isolationResult, 1)
	var workers sync.WaitGroup
	workers.Add(2)
	defer func() { cancel(); workers.Wait() }() // Join consumers before deferred Close, including assertion failures.
	go func() { defer workers.Done(); aDone <- consumeIsolation(aCtx, a) }()
	go func() { defer workers.Done(); bDone <- consumeIsolation(ctx, b) }()
	var completedA isolationResult
	if cancelA {
		stopA()
		got := awaitIsolation(t, ctx, aDone)
		if got.err == nil || got.completed != nil || len(got.calls) != 0 {
			t.Fatal("cancelled A produced successful output")
		}
		if !engine.IsCode(got.err, engine.CodeCanceled) {
			t.Fatalf("A cancellation misclassified: %v", got.err)
		}
		if name := awaitIsolation(t, ctx, cancelled); name != "A" {
			t.Fatal("cancelling A cancelled B")
		}
		select {
		case <-bDone:
			t.Fatal("B terminated when only A was cancelled")
		default:
		}
	} else {
		close(releaseA)
		completedA = awaitIsolation(t, ctx, aDone)
		assertIsolation(t, kind, "A", completedA)
	}
	close(releaseB)
	assertIsolation(t, kind, "B", awaitIsolation(t, ctx, bDone))
	if !cancelA {
		assertIsolation(t, kind, "A", completedA)
	} // B must not mutate A's previously returned metadata.
	if protocol == "http2" {
		// An open process with a live shared H2 connection is the positive
		// control: neither per-stream close/cancel may close the connection.
		select {
		case <-closed:
			t.Fatal("HTTP2 connection closed before model teardown")
		default:
		}
		if err := model.Close(); err != nil {
			t.Fatal(err)
		}
		if address := awaitIsolation(t, ctx, closed); address != first.address {
			t.Fatal("wrong connection closed")
		}
	}
}

func isolationRequest(name string) engine.ModelRequest {
	return engine.ModelRequest{SessionID: domain.SessionID("session-" + name), TurnID: domain.TurnID("turn-" + name), ItemID: domain.ItemID("item-" + name), Input: name,
		Tools: []domain.ToolSchema{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
}

func consumeIsolation(ctx context.Context, stream engine.ModelStream) (result isolationResult) {
	defer func() { result.err = errors.Join(result.err, stream.Close()) }()
	for range 32 {
		event, err := stream.Next(ctx)
		if err != nil {
			result.err = err
			return
		}
		switch event.Type {
		case engine.StreamEventTextDelta:
			result.text += event.Text
		case engine.StreamEventToolCall:
			if event.ToolCall == nil {
				result.err = errors.New("nil tool")
				return
			}
			result.calls = append(result.calls, *event.ToolCall)
		case engine.StreamEventCompleted:
			result.completed = &event
			return
		default:
			result.err = errors.New("unknown event")
			return
		}
	}
	result.err = errors.New("event bound exceeded")
	return
}

func assertIsolation(t *testing.T, kind, name string, got isolationResult) {
	t.Helper()
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.text != "visible-"+name || len(got.calls) != 1 || got.calls[0] != (engine.ToolCall{ID: "call_" + name, Name: "read_file", Arguments: `{"path":"` + name + `"}`}) || got.completed == nil {
		t.Fatalf("crossed/lost output for %s: %#v", name, got)
	}
	n := uint64(11)
	if name == "B" {
		n = 22
	}
	if got.completed.Usage == nil || *got.completed.Usage != (engine.TokenUsage{InputTokens: n, OutputTokens: n + 1}) {
		t.Fatalf("usage crossed for %s: %#v", name, got.completed.Usage)
	}
	if kind == "chat" {
		if got.completed.ProviderState != nil {
			t.Fatal("legacy route invented state")
		}
		return
	}
	state := got.completed.ProviderState
	if state == nil || domain.ValidateProviderProjection(state, got.text, []domain.ToolCallOffer{{ID: got.calls[0].ID, Name: got.calls[0].Name, Arguments: got.calls[0].Arguments}}) != nil {
		t.Fatal("replay projection crossed")
	}
	encoded, _ := json.Marshal(state)
	other := "A"
	if name == "A" {
		other = "B"
	}
	if !strings.Contains(string(encoded), "private-"+name) || strings.Contains(string(encoded), "private-"+other) {
		t.Fatal("private replay state crossed")
	}
}

func isolationWire(kind, name string) string {
	n := 11
	if name == "B" {
		n = 22
	}
	if kind == "chat" {
		return fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"visible-%s\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_%s\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"%s\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d}}\n\ndata: [DONE]\n\n", name, name, name, n, n+1)
	}
	var out strings.Builder
	frame := func(kind string, fields map[string]any) {
		fields["type"] = kind
		encoded, _ := json.Marshal(fields)
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", kind, encoded)
	}
	frame("message_start", map[string]any{"message": map[string]any{"id": "msg_" + name, "type": "message", "role": "assistant", "model": "test-model", "content": []any{}, "usage": map[string]int{"input_tokens": n, "output_tokens": 1}}})
	for index, block := range []map[string]any{{"type": "thinking", "thinking": "private-" + name}, {"type": "text", "text": "visible-" + name}, {"type": "tool_use", "id": "call_" + name, "name": "read_file", "input": map[string]string{}}} {
		frame("content_block_start", map[string]any{"index": index, "content_block": block})
		if index == 2 {
			frame("content_block_delta", map[string]any{"index": index, "delta": map[string]string{"type": "input_json_delta", "partial_json": `{"path":"` + name + `"}`}})
		}
		frame("content_block_stop", map[string]any{"index": index})
	}
	frame("message_delta", map[string]any{"delta": map[string]string{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": n + 1}})
	frame("message_stop", map[string]any{})
	return out.String()
}

func awaitIsolation[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-ctx.Done():
		t.Fatalf("fixture rendezvous: %v", ctx.Err())
		var zero T
		return zero
	}
}

// Keep an independent source-client H2 pool alive while closing the model's
// clone. This is intentionally an in-process check, not process-exit evidence.
func TestProviderHTTP2BorrowedPoolSurvives(t *testing.T) {
	for _, kind := range []string{"chat", "messages"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			addresses := make(chan string, 4)
			closed := make(chan string, 4)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 {
					t.Error("not HTTP2")
				}
				addresses <- r.RemoteAddr
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, providerResourceWire(kind))
			}))
			server.Config.ConnState = func(c net.Conn, state http.ConnState) {
				if state == http.StateClosed {
					closed <- c.RemoteAddr().String()
				}
			}
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()
			client := server.Client()
			client.Timeout = 15 * time.Second
			client.Transport.(*http.Transport).ForceAttemptHTTP2 = true
			defer client.CloseIdleConnections()
			response, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			sourceAddress := awaitIsolation(t, ctx, addresses)
			model := resourceModel(t, kind, server.URL, client)
			defer model.Close()
			stream, err := model.Stream(ctx, engine.ModelRequest{Input: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			if got := consumeIsolation(ctx, stream); got.err != nil || got.completed == nil {
				t.Fatalf("model response: %+v", got)
			}
			modelAddress := awaitIsolation(t, ctx, addresses)
			if modelAddress == sourceAddress {
				t.Fatal("model reused the source client's HTTP2 pool")
			}
			if err := model.Close(); err != nil {
				t.Fatal(err)
			}
			if address := awaitIsolation(t, ctx, closed); address != modelAddress {
				t.Fatal("model failed to close only its private HTTP2 connection")
			}
			// The existing HTTP1 pool test checks reuse; here instrument H2 reuse too.
			assertHTTP2SourceReuse(t, client, server.URL)
		})
	}
}

func assertHTTP2SourceReuse(t *testing.T, client *http.Client, endpoint string) {
	t.Helper()
	reused := false
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }})
	request, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	if !reused || response.ProtoMajor != 2 {
		t.Fatal("source HTTP2 pool was closed or protocol downgraded")
	}
}
