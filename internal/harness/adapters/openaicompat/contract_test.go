package openaicompat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/engine/modeltest"
)

// TestModelContract runs the transport-neutral half of the model port against
// the real HTTP adapter, over a loopback server replaying generated SSE. The
// adapter's own HTTP, SSE, and classification code paths execute; no network
// and no credential are involved.
//
// Cases excluded by modeltest.RunContract, and why they cannot be expressed
// here (see the Slice 5 design, section 9):
//
//   - ReturnNilStream, and returning a stream alongside a startup error, ask
//     an implementation to return a particular pair of Go values from Stream.
//     A transport returns whatever the adapter constructs from a response; the
//     pairing is the adapter's invariant, already asserted by model_test.go.
//   - Close accounting counts calls on a returned value. Over HTTP, Close
//     tears down a response body, which stream_test.go covers directly.
//
// Faking any of these would assert the behavior of this file's server rather
// than the adapter's.
func TestModelContract(t *testing.T) {
	modeltest.RunContract(t, modeltest.Contract{
		Factory:           newContractProbe(t),
		MatchStartupError: matchStartupFailure,
		MatchStreamError:  matchStreamFailure,
	})
}

// matchStartupFailure accepts the classification the adapter produces for the
// 503 this file's server returns when a startup failure is configured. The
// contract cannot require error identity across a transport: the adapter maps
// a response to a ProviderFailure and never returns the caller's sentinel.
func matchStartupFailure(err error) bool {
	var engineErr *engine.Error
	if !errors.As(err, &engineErr) || engineErr.Code != engine.CodeModelStartup {
		return false
	}
	var failure *engine.ProviderFailure
	if !errors.As(err, &failure) {
		return false
	}
	return failure.Class == engine.FailureClassTransient && failure.HTTPStatus == http.StatusServiceUnavailable
}

// matchStreamFailure accepts the classification the adapter produces for the
// malformed event this file's server writes when a mid-stream failure is
// configured.
func matchStreamFailure(err error) bool {
	var engineErr *engine.Error
	if !errors.As(err, &engineErr) {
		return false
	}
	if engineErr.Code != engine.CodeInvalidStream && engineErr.Code != engine.CodeModelStream {
		return false
	}
	var failure *engine.ProviderFailure
	return errors.As(err, &failure) && failure.Class == engine.FailureClassPermanent
}

func newContractProbe(t *testing.T) modeltest.Factory {
	t.Helper()
	return func(_ engine.ModelRequest, config modeltest.Config) modeltest.Probe {
		server := httptest.NewServer(sseHandler(config))
		t.Cleanup(server.Close)

		model, err := openaicompat.New(openaicompat.Config{
			BaseURL:               server.URL,
			ModelID:               "contract-model",
			APIKey:                openaicompat.StaticAPIKey{Value: "test-key"},
			Profile:               openaicompat.ProfileTextOnly(8192, 1024),
			AllowInsecureLoopback: true,
		})
		if err != nil {
			t.Fatalf("openaicompat.New() error = %v", err)
		}
		return &contractProbe{model: model}
	}
}

// sseHandler serves one modeltest.Config as an HTTP response.
//
// StartupError becomes a 503, which the adapter classifies as a transient
// provider failure before any stream exists. A step carrying Err becomes a
// malformed event, which is how a transport expresses a failure that arrives
// after the response headers. WaitForCancel becomes a handler that never
// answers, so Next blocks until the caller's context is done.
func sseHandler(config modeltest.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.StartupError != nil {
			http.Error(w, `{"error":{"message":"unavailable"}}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		flush := func() {
			if flusher != nil {
				flusher.Flush()
			}
		}
		for _, step := range config.Steps {
			if step.WaitForCancel {
				flush()
				<-r.Context().Done()
				return
			}
			if step.Err != nil {
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":\n\n")
				flush()
				return
			}
			line, ok := sseChunk(step.Event)
			if !ok {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", line)
			flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flush()
	})
}

// sseChunk renders one provider-neutral event as an OpenAI-compatible chunk.
// Usage is deliberately omitted from the finish chunk: the contract asserts a
// completed event carries no Usage when the script reports none.
func sseChunk(event engine.StreamEvent) (string, bool) {
	type function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	}
	type toolCall struct {
		Index    int      `json:"index"`
		ID       string   `json:"id,omitempty"`
		Function function `json:"function"`
	}
	type delta struct {
		Content   string     `json:"content,omitempty"`
		ToolCalls []toolCall `json:"tool_calls,omitempty"`
	}
	type choice struct {
		Index        int    `json:"index"`
		Delta        delta  `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	}
	type chunk struct {
		ID      string   `json:"id"`
		Object  string   `json:"object"`
		Model   string   `json:"model"`
		Choices []choice `json:"choices"`
	}

	body := chunk{ID: "chatcmpl-contract", Object: "chat.completion.chunk", Model: "contract-model"}
	switch event.Type {
	case engine.StreamEventTextDelta:
		body.Choices = []choice{{Delta: delta{Content: event.Text}}}
	case engine.StreamEventToolCall:
		if event.ToolCall == nil {
			return "", false
		}
		body.Choices = []choice{{Delta: delta{ToolCalls: []toolCall{{
			ID:       event.ToolCall.ID,
			Function: function{Name: event.ToolCall.Name, Arguments: event.ToolCall.Arguments},
		}}}}}
	case engine.StreamEventCompleted:
		body.Choices = []choice{{Delta: delta{}, FinishReason: "stop"}}
	default:
		return "", false
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// contractProbe adds the call accounting modeltest.Probe requires around the
// real adapter. It decides nothing; every Stream call reaches the adapter.
type contractProbe struct {
	model  *openaicompat.Model
	mu     sync.Mutex
	calls  []engine.ModelRequest
	next   atomic.Int64
	closes atomic.Int64
}

func (probe *contractProbe) Stream(ctx context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	probe.mu.Lock()
	probe.calls = append(probe.calls, request)
	probe.mu.Unlock()
	stream, err := probe.model.Stream(ctx, request)
	if stream == nil {
		return nil, err
	}
	return &countingStream{stream: stream, probe: probe}, err
}

func (probe *contractProbe) Calls() []engine.ModelRequest {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return append([]engine.ModelRequest(nil), probe.calls...)
}

func (probe *contractProbe) NextCalls() int  { return int(probe.next.Load()) }
func (probe *contractProbe) CloseCalls() int { return int(probe.closes.Load()) }

type countingStream struct {
	stream engine.ModelStream
	probe  *contractProbe
}

func (s *countingStream) Next(ctx context.Context) (engine.StreamEvent, error) {
	s.probe.next.Add(1)
	return s.stream.Next(ctx)
}

func (s *countingStream) Close() error {
	s.probe.closes.Add(1)
	return s.stream.Close()
}
