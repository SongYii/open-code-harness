package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

var (
	_ engine.ModelStream     = (*chatStream)(nil)
	_ engine.AttemptObserver = (*chatStream)(nil)

	errIdleTimeout    = errors.New("stream idle timeout")
	errSSELineTooLong = errors.New("sse line too long")
	errInvalidUsage   = errors.New("invalid usage")
)

type chatStream struct {
	mu          sync.Mutex
	body        io.ReadCloser
	scanner     *bufio.Scanner
	cancel      context.CancelFunc
	started     time.Time
	stats       engine.AttemptStats
	pending     []engine.StreamEvent
	dataLines   []string
	sawText     bool
	finish      string
	completed   bool
	closed      bool
	done        bool
	terminalErr error
}

func newChatStream(ctx context.Context, body io.ReadCloser, cancel context.CancelFunc, started time.Time, requestID string, idle time.Duration, maxLine int) *chatStream {
	reader := &idleReader{ctx: ctx, r: body, timeout: idle}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxLine+1)
	scanner.Split(scanSSELines(maxLine))
	return &chatStream{
		body:    body,
		scanner: scanner,
		cancel:  cancel,
		started: started,
		stats:   engine.AttemptStats{ProviderRequestID: requestID},
	}
}

func (s *chatStream) Next(ctx context.Context) (engine.StreamEvent, error) {
	if ctx == nil {
		return engine.StreamEvent{}, &engine.Error{Code: engine.CodeInvalidRequest, Cause: errors.New("nil context")}
	}
	if err := ctx.Err(); err != nil {
		return engine.StreamEvent{}, canceledError(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return engine.StreamEvent{}, canceledError(context.Canceled)
	}
	if ev, ok := s.popPending(); ok {
		return ev, nil
	}
	if s.done {
		if s.terminalErr != nil {
			return engine.StreamEvent{}, s.terminalErr
		}
		return engine.StreamEvent{}, io.EOF
	}
	for {
		if err := ctx.Err(); err != nil {
			return engine.StreamEvent{}, canceledError(err)
		}
		if ev, ok := s.popPending(); ok {
			return ev, nil
		}
		if !s.scanner.Scan() {
			err := s.finishScan(ctx)
			if ev, ok := s.popPending(); ok {
				return ev, nil
			}
			s.done = true
			s.terminalErr = err
			if err != nil {
				return engine.StreamEvent{}, err
			}
			return engine.StreamEvent{}, io.EOF
		}
		if err := s.consumeLine(s.scanner.Text()); err != nil {
			s.done = true
			s.terminalErr = err
			return engine.StreamEvent{}, err
		}
	}
}

func (s *chatStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.body != nil {
		err := s.body.Close()
		s.body = nil
		return err
	}
	return nil
}

func (s *chatStream) Snapshot() engine.AttemptStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.stats
	if !s.started.IsZero() {
		elapsed := time.Since(s.started).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
		stats.LatencyMs = uint64(elapsed)
	}
	stats.Usage = copyUsage(s.stats.Usage)
	return stats
}

func (s *chatStream) popPending() (engine.StreamEvent, bool) {
	if len(s.pending) == 0 {
		return engine.StreamEvent{}, false
	}
	ev := s.pending[0]
	s.pending = s.pending[1:]
	return ev, true
}

func (s *chatStream) consumeLine(line string) error {
	if line == "" {
		return s.dispatchEvent()
	}
	if strings.HasPrefix(line, ":") {
		return nil
	}
	field, value, ok := strings.Cut(line, ":")
	if !ok {
		return nil
	}
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	switch field {
	case "data":
		s.dataLines = append(s.dataLines, value)
	}
	return nil
}

func (s *chatStream) dispatchEvent() error {
	if len(s.dataLines) == 0 {
		return nil
	}
	payload := strings.Join(s.dataLines, "\n")
	s.dataLines = nil
	if payload == "[DONE]" {
		return s.finishStream()
	}
	return s.consumePayload(payload)
}

func (s *chatStream) finishScan(ctx context.Context) error {
	if err := s.dispatchEvent(); err != nil {
		return err
	}
	if err := s.scanner.Err(); err != nil {
		return s.mapReadError(ctx, err)
	}
	if s.completed {
		return nil
	}
	return s.finishStream()
}

func (s *chatStream) finishStream() error {
	if s.completed {
		return nil
	}
	if !s.sawText {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "empty_response", httpStatusOK, s.stats.ProviderRequestID, "provider returned an empty completion")
	}
	reason := s.finish
	if reason == "" {
		reason = "unknown"
	}
	s.emitCompleted(reason)
	return nil
}

func (s *chatStream) consumePayload(payload string) error {
	var root map[string]any
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	if err := s.consumeUsage(root["usage"]); err != nil {
		return err
	}
	rawChoices, present := root["choices"]
	if !present || rawChoices == nil {
		return nil
	}
	choices, ok := rawChoices.([]any)
	if !ok {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	if len(choices) > 1 {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	if len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	if err := s.consumeChoice(choice); err != nil {
		return err
	}
	return nil
}

func (s *chatStream) consumeChoice(choice map[string]any) error {
	if err := inspectContentType(choice["delta"], s.stats.ProviderRequestID); err != nil {
		return err
	}
	if err := inspectContentType(choice["message"], s.stats.ProviderRequestID); err != nil {
		return err
	}
	if delta, ok := choice["delta"].(map[string]any); ok {
		if err := s.consumeDelta(delta); err != nil {
			return err
		}
	}
	if message, ok := choice["message"].(map[string]any); ok {
		if hasToolCalls(message["tool_calls"]) {
			return capabilityMismatch(s.stats.ProviderRequestID)
		}
	}
	return s.consumeFinish(choice["finish_reason"])
}

func (s *chatStream) consumeDelta(delta map[string]any) error {
	if hasToolCalls(delta["tool_calls"]) {
		return capabilityMismatch(s.stats.ProviderRequestID)
	}
	raw, ok := delta["content"]
	if !ok || raw == nil {
		return nil
	}
	text, ok := raw.(string)
	if !ok {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	if text == "" {
		return nil
	}
	s.sawText = true
	s.pending = append(s.pending, engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: text})
	return nil
}

func (s *chatStream) consumeFinish(raw any) error {
	if raw == nil {
		return nil
	}
	reason, ok := raw.(string)
	if !ok {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	switch reason {
	case "", "null":
		return nil
	case "stop":
		s.finish = "stop"
		return nil
	case "length":
		s.finish = "length"
		return nil
	case "content_filter":
		s.stats.FinishReason = ""
		return streamFailure(engine.CodeModelStream, engine.FailureClassPermanent, "provider_permanent", httpStatusOK, s.stats.ProviderRequestID, "provider rejected the request")
	case "tool_calls":
		s.stats.FinishReason = ""
		return capabilityMismatch(s.stats.ProviderRequestID)
	default:
		if s.finish == "" {
			s.finish = "unknown"
		}
		return nil
	}
}

func (s *chatStream) consumeUsage(raw any) error {
	if raw == nil {
		return nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	usage, err := mapUsage(obj)
	if err != nil {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	s.stats.Usage = usage
	return nil
}

func (s *chatStream) emitCompleted(reason string) {
	s.completed = true
	s.stats.FinishReason = reason
	s.pending = append(s.pending, engine.StreamEvent{
		Type:  engine.StreamEventCompleted,
		Usage: copyUsage(s.stats.Usage),
	})
}

func (s *chatStream) mapReadError(ctx context.Context, err error) *engine.Error {
	if ctx != nil && ctx.Err() != nil {
		return canceledError(ctx.Err())
	}
	if s.closed || isCanceled(err) {
		return canceledError(err)
	}
	if errors.Is(err, errIdleTimeout) {
		return streamFailure(engine.CodeModelStream, engine.FailureClassTransient, "provider_transient", httpStatusOK, s.stats.ProviderRequestID, "provider temporarily unavailable")
	}
	if errors.Is(err, errSSELineTooLong) {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	return streamFailure(engine.CodeModelStream, engine.FailureClassTransient, "provider_transient", httpStatusOK, s.stats.ProviderRequestID, "provider temporarily unavailable")
}

func inspectContentType(node any, requestID string) error {
	obj, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	raw, present := obj["content"]
	if !present || raw == nil {
		return nil
	}
	if _, ok := raw.(string); ok {
		return nil
	}
	return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, requestID, "invalid stream")
}

func hasToolCalls(raw any) bool {
	if raw == nil {
		return false
	}
	switch value := raw.(type) {
	case []any:
		return len(value) > 0
	default:
		return true
	}
}

func capabilityMismatch(requestID string) *engine.Error {
	return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "capability_mismatch", httpStatusOK, requestID, "provider returned an unsupported capability")
}

func mapUsage(obj map[string]any) (*engine.TokenUsage, error) {
	input, err := firstPresentToken(obj, "prompt_tokens", "input_tokens")
	if err != nil {
		return nil, err
	}
	output, err := firstPresentToken(obj, "completion_tokens", "output_tokens")
	if err != nil {
		return nil, err
	}
	cached, err := cachedInputTokens(obj)
	if err != nil {
		return nil, err
	}
	return &engine.TokenUsage{InputTokens: input, OutputTokens: output, CachedInputTokens: cached}, nil
}

func cachedInputTokens(obj map[string]any) (uint64, error) {
	if details, ok := obj["prompt_tokens_details"].(map[string]any); ok {
		if raw, present := details["cached_tokens"]; present && raw != nil {
			return asToken(raw)
		}
	}
	if raw, present := obj["prompt_cache_hit_tokens"]; present && raw != nil {
		return asToken(raw)
	}
	return 0, nil
}

func firstPresentToken(obj map[string]any, keys ...string) (uint64, error) {
	for _, key := range keys {
		raw, present := obj[key]
		if !present || raw == nil {
			continue
		}
		return asToken(raw)
	}
	return 0, nil
}

func asToken(raw any) (uint64, error) {
	switch value := raw.(type) {
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value != math.Trunc(value) || value > float64(math.MaxUint64) {
			return 0, errInvalidUsage
		}
		return uint64(value), nil
	case json.Number:
		f, err := value.Float64()
		if err != nil {
			return 0, errInvalidUsage
		}
		return asToken(f)
	default:
		return 0, errInvalidUsage
	}
}

func copyUsage(usage *engine.TokenUsage) *engine.TokenUsage {
	if usage == nil {
		return nil
	}
	copied := *usage
	return &copied
}

func scanSSELines(max int) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line := data[:i]
			if i > 0 && data[i-1] == '\r' {
				line = data[:i-1]
			}
			if len(line) > max {
				return 0, nil, errSSELineTooLong
			}
			return i + 1, line, nil
		}
		if !atEOF {
			if len(data) > max {
				return 0, nil, errSSELineTooLong
			}
			return 0, nil, nil
		}
		if len(data) == 0 {
			return 0, nil, nil
		}
		if len(data) > max {
			return 0, nil, errSSELineTooLong
		}
		return len(data), data, nil
	}
}

const httpStatusOK = 200

type idleReader struct {
	ctx     context.Context
	r       io.Reader
	timeout time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	if r.timeout <= 0 {
		return r.r.Read(p)
	}
	type outcome struct {
		n   int
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		n, err := r.r.Read(p)
		done <- outcome{n, err}
	}()
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	var cancel <-chan struct{}
	if r.ctx != nil {
		cancel = r.ctx.Done()
	}
	select {
	case out := <-done:
		return out.n, out.err
	case <-timer.C:
		return 0, errIdleTimeout
	case <-cancel:
		if r.ctx != nil && r.ctx.Err() != nil {
			return 0, r.ctx.Err()
		}
		return 0, context.Canceled
	}
}
