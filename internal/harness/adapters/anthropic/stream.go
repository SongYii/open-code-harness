package anthropic

import (
	"context"
	"io"
	"math/bits"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

type messageStream struct {
	ctx             context.Context
	cancel          context.CancelFunc
	body            *guardedBody
	stopCancel      func() bool
	identity        engine.RequestIdentity
	started         time.Time
	stats           engine.AttemptStats
	pending         []engine.StreamEvent
	decoded, closed bool
	err             error
}

func newMessageStream(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser, identity engine.RequestIdentity, idle time.Duration, started time.Time) *messageStream {
	s := &messageStream{ctx: ctx, cancel: cancel, body: &guardedBody{body: body, idle: idle}, identity: identity, started: started}
	s.stopCancel = context.AfterFunc(ctx, func() { s.body.Close() })
	return s
}

// One owner calls Next/Close. Cancellation and idle timers touch only the
// once-closed body; no decoder goroutine can outlive Close or publish late data.
func (s *messageStream) Next(ctx context.Context) (engine.StreamEvent, error) {
	if ctx == nil {
		return engine.StreamEvent{}, failure(engine.CodeInvalidRequest, 0)
	}
	if ctx.Err() != nil || s.ctx.Err() != nil || s.closed {
		s.pending = nil
		return engine.StreamEvent{}, canceled()
	}
	if !s.decoded {
		stopNext := context.AfterFunc(ctx, func() { s.body.Close() })
		result, err := decodeMessageFor(s.body, deepSeekMessages)
		stopNext()
		closeErr := s.body.Close() // Close failure is a failure BEFORE completion admission.
		s.stopCancel()
		s.decoded = true
		s.stats.LatencyMs = uint64(time.Since(s.started).Milliseconds())
		if ctx.Err() != nil || s.ctx.Err() != nil {
			s.err = canceled()
		} else if err != nil || closeErr != nil || s.body.expired.Load() || result.Model != s.identity.ModelID {
			s.err = failure(engine.CodeModelStream, 0)
		}
		if s.err != nil {
			return engine.StreamEvent{}, s.err
		}
		// Messages reports uncached, cache-read, and cache-created input
		// separately. The engine requires total input, with cached input a
		// subset. Reject overflow before admitting any output or tool calls.
		input, carry := bits.Add64(result.Usage.InputTokens, result.Usage.CacheReadTokens, 0)
		total, creationCarry := bits.Add64(input, result.Usage.CacheCreationTokens, 0)
		if carry != 0 || creationCarry != 0 {
			s.err = failure(engine.CodeModelStream, 0)
			return engine.StreamEvent{}, s.err
		}
		usage := &engine.TokenUsage{InputTokens: total, OutputTokens: result.Usage.OutputTokens, CachedInputTokens: result.Usage.CacheReadTokens}
		state := &domain.ProviderState{Protocol: domain.DeepSeekMessagesV1, ModelID: s.identity.ModelID, EndpointID: s.identity.EndpointID}
		for _, b := range result.Content {
			block := domain.ProviderContentBlock{Type: b.Type, Text: b.Text, Thinking: b.Thinking, ID: b.ID, Name: b.Name, Input: b.Input}
			if b.SignaturePresent {
				signature := b.Signature
				block.Signature = &signature
			}
			state.MessagesContent = append(state.MessagesContent, block)
		}
		if domain.ValidateProviderState(state) != nil {
			s.err = failure(engine.CodeModelStream, 0)
			return engine.StreamEvent{}, s.err
		}
		if text := result.visibleText(); text != "" {
			s.pending = append(s.pending, engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: text})
		}
		for _, b := range result.Content {
			if b.Type == "tool_use" {
				s.pending = append(s.pending, engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: b.ID, Name: b.Name, Arguments: string(b.Input)}})
			}
		}
		s.stats.Usage, s.stats.FinishReason = usage, "stop"
		if result.StopReason == "tool_use" {
			s.stats.FinishReason = "tool_calls"
		}
		s.pending = append(s.pending, engine.StreamEvent{Type: engine.StreamEventCompleted, ProviderState: state, Usage: usage})
	}
	if s.err != nil {
		return engine.StreamEvent{}, s.err
	}
	if len(s.pending) == 0 {
		return engine.StreamEvent{}, io.EOF
	}
	event := s.pending[0]
	s.pending[0] = engine.StreamEvent{}
	s.pending = s.pending[1:]
	return event, nil
}

func (s *messageStream) Close() error {
	s.closed, s.pending = true, nil
	s.stopCancel()
	s.cancel()
	if s.body.Close() != nil {
		return failure(engine.CodeModelStream, 0)
	}
	return nil
}

func (s *messageStream) Snapshot() engine.AttemptStats {
	stats := s.stats
	if stats.Usage != nil {
		usage := *stats.Usage
		stats.Usage = &usage
	}
	return stats
}

type guardedBody struct {
	body    io.ReadCloser
	idle    time.Duration
	once    sync.Once
	err     error
	expired atomic.Bool
}

func (b *guardedBody) Read(p []byte) (int, error) {
	done := make(chan struct{})
	timer := time.AfterFunc(b.idle, func() { b.expired.Store(true); b.Close(); close(done) })
	n, err := b.body.Read(p)
	if !timer.Stop() {
		<-done
	}
	return n, err
}

func (b *guardedBody) Close() error { b.once.Do(func() { b.err = b.body.Close() }); return b.err }

var _ engine.ModelStream = (*messageStream)(nil)
var _ engine.AttemptObserver = (*messageStream)(nil)
