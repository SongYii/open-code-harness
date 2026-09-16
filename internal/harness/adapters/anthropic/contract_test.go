package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/engine/modeltest"
)

// Real SDK request encoding, HTTP boundary and strict decoder; only the remote
// endpoint is a fixture. Messages may coalesce text, but cannot drop/reorder it.
func TestModelContract(t *testing.T) {
	modeltest.RunContract(t, modeltest.Contract{
		Factory: func(_ engine.ModelRequest, config modeltest.Config) modeltest.Probe {
			server := httptest.NewServer(messagesContractHandler(config))
			t.Cleanup(server.Close)
			m, err := New(Config{BaseURL: server.URL, ModelID: "fixture-model", APIKey: "fixture-only", ContextWindow: 8192, MaxOutput: 1024, AllowInsecureLoopback: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Close() })
			return &contractProbe{model: m}
		},
		MatchStartupError: func(err error) bool {
			var failure *engine.ProviderFailure
			return engine.IsCode(err, engine.CodeModelStartup) && errors.As(err, &failure) && failure.Class == engine.FailureClassTransient && failure.HTTPStatus == 503
		},
		MatchStreamError:  func(err error) bool { return engine.IsCode(err, engine.CodeModelStream) },
		MatchCancellation: func(err error) bool { return engine.IsCode(err, engine.CodeCanceled) },
		CheckCompletion: func(t *testing.T, event engine.StreamEvent, text string, calls []engine.ToolCall) {
			t.Helper()
			if event.Usage == nil || *event.Usage != (engine.TokenUsage{InputTokens: 19, OutputTokens: 17, CachedInputTokens: 3}) {
				t.Fatal("Messages fixture usage changed")
			}
			state := event.ProviderState
			if state == nil || state.Protocol != domain.DeepSeekMessagesV1 || state.ModelID != "fixture-model" {
				t.Fatal("Messages replay identity lost")
			}
			var offers []domain.ToolCallOffer
			for _, call := range calls {
				offers = append(offers, domain.ToolCallOffer{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
			}
			if err := domain.ValidateProviderProjection(state, text, offers); err != nil {
				t.Fatal(err)
			}
		},
	})
}

func messagesContractHandler(config modeltest.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.StartupError != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, start())
		w.(http.Flusher).Flush()
		block, tools := 0, 0
		for _, step := range config.Steps {
			if step.WaitForCancel {
				<-r.Context().Done()
				return
			}
			if step.Err != nil {
				fmt.Fprint(w, "event: broken\ndata: {\n\n")
				return
			}
			switch event := step.Event; event.Type {
			case engine.StreamEventTextDelta:
				fmt.Fprint(w, textBlock(block, event.Text))
				block++
			case engine.StreamEventToolCall:
				fmt.Fprint(w, toolBlock(block, event.ToolCall.ID, event.ToolCall.Arguments))
				block++
				tools++
			case engine.StreamEventCompleted:
				// The native protocol requires content, even for empty output.
				if block == 0 {
					fmt.Fprint(w, textBlock(0, ""))
				}
				reason := "end_turn"
				if tools > 0 {
					reason = "tool_use"
				}
				fmt.Fprint(w, stop(reason))
			}
		}
	})
}

// Accounting observes calls without altering the adapter's outputs/errors.
type contractProbe struct {
	model        *Model
	mu           sync.Mutex
	calls        []engine.ModelRequest
	next, closes atomic.Int64
}

func (p *contractProbe) Stream(ctx context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	p.mu.Lock()
	p.calls = append(p.calls, request)
	p.mu.Unlock()
	s, err := p.model.Stream(ctx, request)
	if s == nil {
		return nil, err
	}
	return &contractStream{inner: s, probe: p}, err
}
func (p *contractProbe) Calls() []engine.ModelRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]engine.ModelRequest(nil), p.calls...)
}
func (p *contractProbe) NextCalls() int  { return int(p.next.Load()) }
func (p *contractProbe) CloseCalls() int { return int(p.closes.Load()) }

type contractStream struct {
	inner engine.ModelStream
	probe *contractProbe
}

func (s *contractStream) Next(ctx context.Context) (engine.StreamEvent, error) {
	s.probe.next.Add(1)
	return s.inner.Next(ctx)
}
func (s *contractStream) Close() error { s.probe.closes.Add(1); return s.inner.Close() }
