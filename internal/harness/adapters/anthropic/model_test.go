package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countedBody struct {
	io.Reader
	closes   atomic.Int32
	closeErr error
}

func (b *countedBody) Close() error { b.closes.Add(1); return b.closeErr }

func fixtureModel(t *testing.T, rt roundTripFunc) *Model {
	t.Helper()
	m, err := New(Config{BaseURL: "https://api.deepseek.com/anthropic", ModelID: "fixture-model", APIKey: "fixture-only", ContextWindow: 8192, MaxOutput: 1024, ReasoningEffort: engine.ReasoningEffortHigh, HTTPClient: &http.Client{Transport: rt}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func responseFor(body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
}

func drain(t *testing.T, stream engine.ModelStream) []engine.StreamEvent {
	t.Helper()
	defer stream.Close()
	var events []engine.StreamEvent
	for {
		event, err := stream.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func TestSDKModelToolContinuationAndExplicitHTTP(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-fixture-must-not-be-sent")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "https://wrong.invalid/")
	wire := start() + thinkingBlock(0, "private-first", "sig-1") + textBlock(1, "before ") + toolBlock(2, "call_1", `{"n":9007199254740993,"path":"<a>"}`) +
		blockStart(3, `{"type":"thinking","thinking":""}`) + blockStop(3) + toolBlock(4, "call_2", `{}`) + textBlock(5, "after") + stop("tool_use")
	var bodies []*countedBody
	var requests []map[string]json.RawMessage
	m := fixtureModel(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.deepseek.com/anthropic/v1/messages" || r.Method != "POST" || r.Header.Get("x-api-key") != "fixture-only" || r.Header.Get("Authorization") != "" {
			t.Error("route/auth escaped explicit configuration")
		}
		if r.Header.Get("X-Och-Request-Purpose") != "conversation" {
			t.Error("purpose attribution lost")
		}
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests = append(requests, request)
		if string(request["stream"]) != "true" || string(request["max_tokens"]) != "512" || string(request["thinking"]) != `{"type":"enabled"}` || string(request["output_config"]) != `{"effort":"low"}` {
			t.Error("native request controls changed")
		}
		if len(requests) > 1 {
			wire = start() + textBlock(0, "done") + stop("end_turn")
		}
		body := &countedBody{Reader: strings.NewReader(wire)}
		bodies = append(bodies, body)
		return responseFor(body), nil
	})
	request := engine.ModelRequest{Input: "hello", Purpose: engine.ModelRequestPurposeConversation, MaxOutputTokens: 512, ReasoningEffort: engine.ReasoningEffortLow, Tools: []domain.ToolSchema{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	stream, err := m.Stream(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	events := drain(t, stream)
	if len(events) != 4 || events[0].Text != "before after" || events[1].ToolCall.ID != "call_1" || events[2].ToolCall.ID != "call_2" || events[3].Type != engine.StreamEventCompleted {
		t.Fatal("engine projection/order changed")
	}
	state := events[3].ProviderState
	if state == nil || len(state.MessagesContent) != 6 || state.MessagesContent[3].Signature != nil {
		t.Fatal("replay blocks or signature absence lost")
	}
	calls := []domain.ToolCallOffer{{ID: "call_1", Name: "read_file", Arguments: events[1].ToolCall.Arguments}, {ID: "call_2", Name: "read_file", Arguments: events[2].ToolCall.Arguments}}
	request.Messages = []domain.ModelPromptMessage{{Role: "user", Text: "hello"}, {Role: "assistant", Text: "before after", ToolCalls: calls, ProviderState: state}, {Role: "tool", ToolCallID: "call_1", Name: "read_file", Text: "result-one"}, {Role: "tool", ToolCallID: "call_2", Name: "read_file", Text: "result-two"}}
	stream, err = m.Stream(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, stream)
	var messages []struct {
		Role    string
		Content json.RawMessage
	}
	if json.Unmarshal(requests[1]["messages"], &messages) != nil || len(messages) != 3 {
		t.Fatal("tool results not grouped")
	}
	want, _ := json.Marshal(state.MessagesContent)
	if string(messages[1].Content) != string(want) || !strings.Contains(string(want), "9007199254740993") {
		t.Fatal("SDK changed ordered replay content")
	}
	if messages[2].Role != "user" || !strings.Contains(string(messages[2].Content), `"tool_use_id":"call_1"`) || !strings.Contains(string(messages[2].Content), `"tool_use_id":"call_2"`) {
		t.Fatal("tool result associations lost")
	}
	for _, b := range bodies {
		if b.closes.Load() != 1 {
			t.Fatal("body not closed exactly once")
		}
	}
	for _, mutate := range []func(*domain.ProviderState){func(s *domain.ProviderState) { s.ModelID = "other" }, func(s *domain.ProviderState) { s.EndpointID = "other" }, func(s *domain.ProviderState) { s.MessagesContent[1].Text = "changed" }} {
		bad := domain.CloneProviderState(state)
		mutate(bad)
		request.Messages[1].ProviderState = bad
		if _, err := m.Stream(context.Background(), request); err == nil {
			t.Fatal("invalid replay accepted")
		}
	}
	if len(requests) != 2 {
		t.Fatal("invalid replay reached transport")
	}
}

func TestSDKUsageNormalizesTotalInput(t *testing.T) {
	for _, tc := range []struct {
		name, start string
		want        engine.TokenUsage
	}{
		{"all counters", start(), engine.TokenUsage{InputTokens: 19, OutputTokens: 17, CachedInputTokens: 3}},
		{"cache exceeds uncached live regression", strings.NewReplacer(`"input_tokens":12`, `"input_tokens":178`, `"cache_read_input_tokens":3`, `"cache_read_input_tokens":256`, `"cache_creation_input_tokens":4`, `"cache_creation_input_tokens":0`).Replace(start()), engine.TokenUsage{InputTokens: 434, OutputTokens: 17, CachedInputTokens: 256}},
		{"cache absent", strings.NewReplacer(`,"cache_read_input_tokens":3`, "", `,"cache_creation_input_tokens":4`, "").Replace(start()), engine.TokenUsage{InputTokens: 12, OutputTokens: 17}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader(tc.start + textBlock(0, "ok") + stop("end_turn"))}
			m := fixtureModel(t, func(*http.Request) (*http.Response, error) { return responseFor(body), nil })
			stream, err := m.Stream(context.Background(), engine.ModelRequest{Input: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			events := drain(t, stream)
			got := events[len(events)-1].Usage
			if got == nil || *got != tc.want {
				t.Fatalf("completion usage = %+v; want %+v", got, tc.want)
			}
			stats := stream.(engine.AttemptObserver).Snapshot()
			if stats.Usage == nil || *stats.Usage != tc.want {
				t.Fatalf("attempt usage = %+v; want %+v", stats.Usage, tc.want)
			}
		})
	}
}

func TestSDKHTTPRejectsWithoutReadingErrorBodiesOrRetrying(t *testing.T) {
	for _, status := range []int{200, 204, 307, 400, 401, 413, 422, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			body := &countedBody{Reader: errorReader{t: t}}
			var calls atomic.Int32
			m := fixtureModel(t, func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "Location": {"https://must-not-follow.invalid/"}}, Body: body}, nil
			})
			stream, err := m.Stream(context.Background(), engine.ModelRequest{Input: "hello"})
			var failure *engine.ProviderFailure
			if stream != nil || !errors.As(err, &failure) || failure.HTTPStatus != status || calls.Load() != 1 || body.closes.Load() != 1 {
				t.Fatal("HTTP gate/retry/close invariant failed")
			}
			if (status == 400 || status == 413 || status == 422) && (failure.Code != "provider_permanent" || failure.Class != engine.FailureClassPermanent || failure.Retryable) {
				t.Fatal("ambiguous HTTP status was promoted to recoverable context overflow")
			}
		})
	}
}

type errorReader struct{ t *testing.T }

func (r errorReader) Read([]byte) (int, error) { r.t.Error("error body was read"); return 0, io.EOF }

func TestSDKStreamRejectsBeforeAnyOutput(t *testing.T) {
	wires := rejectedFixtures()
	delete(wires, "missing signature")
	delete(wires, "empty signature")
	wires["redacted unsupported"] = start() + blockStart(0, `{"type":"redacted_thinking","data":"opaque"}`) + blockStop(0) + stop("end_turn")
	wires["wrong response model"] = strings.Replace(start(), "fixture-model", "other-model", 1) + textBlock(0, "ok") + stop("end_turn")
	wires["total input overflow"] = strings.NewReplacer(`"input_tokens":12`, `"input_tokens":9223372036854775807`, `"cache_read_input_tokens":3`, `"cache_read_input_tokens":9223372036854775807`).Replace(start()) + textBlock(0, "must not escape") + toolBlock(1, "overflow_call", `{}`) + stop("tool_use")
	wires["SDK repeated empty object repair"] = start() + blockStart(0, `{"type":"tool_use","id":"c","name":"read_file","input":{}}`) + delta(0, "input_json_delta", "partial_json", `{}`) + delta(0, "input_json_delta", "partial_json", `{}`) + blockStop(0) + stop("tool_use")
	for name, wire := range wires {
		t.Run(name, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader(wire)}
			m := fixtureModel(t, func(*http.Request) (*http.Response, error) { return responseFor(body), nil })
			s, err := m.Stream(context.Background(), engine.ModelRequest{Input: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			event, err := s.Next(context.Background())
			if err == nil || !reflect.DeepEqual(event, engine.StreamEvent{}) {
				t.Fatal("invalid stream exposed partial output")
			}
			s.Close()
			if body.closes.Load() != 1 {
				t.Fatal("rejected body leaked")
			}
		})
	}
	body := &countedBody{Reader: strings.NewReader(start() + textBlock(0, "ok") + stop("end_turn")), closeErr: errors.New("private close failure")}
	m := fixtureModel(t, func(*http.Request) (*http.Response, error) { return responseFor(body), nil })
	s, _ := m.Stream(context.Background(), engine.ModelRequest{Input: "hello"})
	defer s.Close()
	if e, err := s.Next(context.Background()); err == nil || !reflect.DeepEqual(e, engine.StreamEvent{}) {
		t.Fatal("close failure exposed completion")
	}
}

type blockingBody struct {
	entered chan struct{}
	done    chan struct{}
	once    sync.Once
	closes  atomic.Int32
}

func (b *blockingBody) Read([]byte) (int, error) { close(b.entered); <-b.done; return 0, io.EOF }
func (b *blockingBody) Close() error {
	b.closes.Add(1)
	b.once.Do(func() { close(b.done) })
	return nil
}

func TestSDKStreamCancellationIdleAndEarlyClose(t *testing.T) {
	for _, mode := range []string{"parent", "next", "idle", "early-close"} {
		t.Run(mode, func(t *testing.T) {
			body := &blockingBody{entered: make(chan struct{}), done: make(chan struct{})}
			m := fixtureModel(t, func(*http.Request) (*http.Response, error) { return responseFor(body), nil })
			m.idle = 20 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			nextCtx, nextCancel := context.WithCancel(context.Background())
			defer nextCancel()
			s, err := m.Stream(ctx, engine.ModelRequest{Input: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "early-close" {
				s.Close()
				s.Close()
			} else {
				done := make(chan error, 1)
				go func() { _, err := s.Next(nextCtx); done <- err }()
				<-body.entered
				if mode == "parent" {
					cancel()
				}
				if mode == "next" {
					nextCancel()
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("blocked stream succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("cancellation/idle did not unblock")
				}
				s.Close()
			}
			if body.closes.Load() != 1 {
				t.Fatal("body not closed exactly once")
			}
		})
	}
}

func TestSDKIndexedOpenBlocksAndDeepSeekSignatures(t *testing.T) {
	wire := start() + blockStart(0, `{"type":"thinking","thinking":""}`) + blockStart(1, `{"type":"text","text":""}`) + delta(1, "text_delta", "text", "visible") + blockStop(1) + delta(0, "thinking_delta", "thinking", "private") + blockStop(0) + stop("end_turn")
	got, err := decodeMessageFor(strings.NewReader(wire), deepSeekMessages)
	if err != nil || got.Content[0].Thinking != "private" || got.Content[1].Text != "visible" {
		t.Fatal("indexed open blocks failed")
	}
	encoded, _ := got.replayContent()
	if strings.Contains(string(encoded), "signature") {
		t.Fatal("missing signature fabricated")
	}
	wire = strings.Replace(wire, blockStop(0), delta(0, "signature_delta", "signature", "")+blockStop(0), 1)
	got, err = decodeMessageFor(strings.NewReader(wire), deepSeekMessages)
	encoded, _ = got.replayContent()
	if err != nil || !strings.Contains(string(encoded), `"signature":""`) {
		t.Fatal("empty signature not preserved")
	}
}

func TestSDKRawToolInputOwnsEmptyObjectFragments(t *testing.T) {
	wire := start() + blockStart(0, `{"type":"tool_use","id":"c","name":"read_file","input":{ }}`) + delta(0, "input_json_delta", "partial_json", `{}`) + delta(0, "input_json_delta", "partial_json", " \n") + blockStop(0) + stop("tool_use")
	got, err := decodeMessageFor(strings.NewReader(wire), deepSeekMessages)
	if err != nil || string(got.Content[0].Input) != "{} \n" {
		t.Fatal("valid raw fragments were reset by SDK sentinel")
	}
}
