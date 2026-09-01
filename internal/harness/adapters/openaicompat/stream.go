package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

var (
	_ engine.ModelStream     = (*chatStream)(nil)
	_ engine.AttemptObserver = (*chatStream)(nil)

	errIdleTimeout    = errors.New("stream idle timeout")
	errSSELineTooLong = errors.New("sse line too long")
	errInvalidUsage   = errors.New("invalid usage")
)

const maxAssembledArgumentsBytes = 32 << 10

type assembledCall struct {
	id        string
	name      string
	arguments string
}

type chatStream struct {
	mu          sync.Mutex
	body        *onceCloser
	scanner     *bufio.Scanner
	cancel      context.CancelFunc
	started     time.Time
	idleTimeout time.Duration
	idleExpired atomic.Bool
	stats       engine.AttemptStats
	pending     []engine.StreamEvent
	dataLines   []string
	sawText     bool
	finish      string
	completed   bool
	closed      bool
	done        bool
	terminalErr error
	nativeTools engine.CapabilityTriState
	content     []string
	calls       map[int]*assembledCall
}

func newChatStream(_ context.Context, body io.ReadCloser, cancel context.CancelFunc, started time.Time, requestID string, idle time.Duration, maxLine int, nativeTools engine.CapabilityTriState) *chatStream {
	wrapped := &onceCloser{c: body}
	scanner := bufio.NewScanner(wrapped)
	scanner.Buffer(make([]byte, 64*1024), maxLine+1)
	scanner.Split(scanSSELines(maxLine))
	return &chatStream{
		body:        wrapped,
		scanner:     scanner,
		cancel:      cancel,
		started:     started,
		idleTimeout: idle,
		stats:       engine.AttemptStats{ProviderRequestID: requestID},
		nativeTools: nativeTools,
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
		stopIdle := s.armIdle()
		scanned := s.scanner.Scan()
		stopIdle()
		if !scanned {
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
	if s.body == nil {
		return nil
	}
	return s.body.Close()
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
	if s.idleExpired.Load() {
		return s.mapReadError(ctx, errIdleTimeout)
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
	if s.assemblesTools() {
		return s.emitAssembled()
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

func (s *chatStream) assemblesTools() bool {
	return s != nil && nativeToolsEnabled(engine.CapabilityProfile{NativeTools: s.nativeTools})
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
		if err := s.consumeToolCallsField(message["tool_calls"]); err != nil {
			return err
		}
	}
	return s.consumeFinish(choice["finish_reason"])
}

func (s *chatStream) consumeDelta(delta map[string]any) error {
	if err := s.consumeToolCallsField(delta["tool_calls"]); err != nil {
		return err
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
	if s.assemblesTools() {
		s.content = append(s.content, text)
		return nil
	}
	s.pending = append(s.pending, engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: text})
	return nil
}

func (s *chatStream) consumeToolCallsField(raw any) error {
	if raw == nil {
		return nil
	}
	if !s.assemblesTools() {
		if hasToolCalls(raw) {
			return capabilityMismatch(s.stats.ProviderRequestID)
		}
		return nil
	}
	return s.consumeToolCalls(raw)
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
		if !s.assemblesTools() {
			s.stats.FinishReason = ""
			return capabilityMismatch(s.stats.ProviderRequestID)
		}
		s.finish = "tool_calls"
		return nil
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

func (s *chatStream) consumeToolCalls(raw any) error {
	items, ok := raw.([]any)
	if !ok {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
		}
		if err := s.consumeAssembledCall(obj); err != nil {
			return err
		}
	}
	return nil
}

func (s *chatStream) consumeAssembledCall(obj map[string]any) error {
	rawIndex, present := obj["index"]
	if !present || rawIndex == nil {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	index, ok := asCallIndex(rawIndex)
	if !ok {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	if s.calls == nil {
		s.calls = make(map[int]*assembledCall)
	}
	call := s.calls[index]
	if call == nil {
		call = &assembledCall{}
		s.calls[index] = call
	}
	if rawID, exists := obj["id"]; exists && rawID != nil {
		id, ok := rawID.(string)
		if !ok {
			return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
		}
		if id != "" {
			if call.id != "" && call.id != id {
				return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
			}
			call.id = id
		}
	}
	rawFn, exists := obj["function"]
	if !exists || rawFn == nil {
		return nil
	}
	fn, ok := rawFn.(map[string]any)
	if !ok {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	if rawName, hasName := fn["name"]; hasName && rawName != nil {
		name, ok := rawName.(string)
		if !ok {
			return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
		}
		if name != "" {
			if call.name != "" && call.name != name {
				return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
			}
			call.name = name
		}
	}
	if rawArgs, hasArgs := fn["arguments"]; hasArgs && rawArgs != nil {
		args, ok := rawArgs.(string)
		if !ok {
			return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
		}
		if len(call.arguments)+len(args) > maxAssembledArgumentsBytes {
			return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
		}
		call.arguments += args
	}
	return nil
}

func (s *chatStream) emitAssembled() error {
	if len(s.calls) > 0 && s.finish != "tool_calls" {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	if s.finish == "tool_calls" && len(s.calls) == 0 {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	if len(s.content) == 0 && len(s.calls) == 0 {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "empty_response", httpStatusOK, s.stats.ProviderRequestID, "provider returned an empty completion")
	}
	for _, text := range s.content {
		s.pending = append(s.pending, engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: text})
	}
	if len(s.calls) > 0 {
		if err := s.emitAssembledCalls(); err != nil {
			s.pending = nil
			return err
		}
		s.emitCompleted("tool_calls")
		return nil
	}
	reason := s.finish
	if reason == "" {
		reason = "unknown"
	}
	s.emitCompleted(reason)
	return nil
}

func (s *chatStream) emitAssembledCalls() error {
	indices := make([]int, 0, len(s.calls))
	for index := range s.calls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	if indices[0] != 0 || indices[len(indices)-1] != len(indices)-1 {
		return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
	}
	seen := make(map[string]struct{}, len(indices))
	for _, index := range indices {
		call := s.calls[index]
		if call == nil || strings.TrimSpace(call.id) == "" || strings.TrimSpace(call.name) == "" {
			return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
		}
		if !utf8.ValidString(call.id) || !utf8.ValidString(call.name) {
			return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
		}
		args := call.arguments
		if args == "" {
			args = "{}"
		}
		if !utf8.ValidString(args) || len(args) > maxAssembledArgumentsBytes {
			return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
		}
		if _, exists := seen[call.id]; exists {
			return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "invalid_stream", httpStatusOK, s.stats.ProviderRequestID, "invalid stream")
		}
		seen[call.id] = struct{}{}
		s.pending = append(s.pending, engine.StreamEvent{
			Type: engine.StreamEventToolCall,
			ToolCall: &engine.ToolCall{
				ID:        call.id,
				Name:      call.name,
				Arguments: args,
			},
		})
	}
	return nil
}

func asCallIndex(raw any) (int, bool) {
	switch value := raw.(type) {
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value != math.Trunc(value) || value > float64(math.MaxInt32) {
			return 0, false
		}
		return int(value), true
	default:
		return 0, false
	}
}

func (s *chatStream) mapReadError(ctx context.Context, err error) *engine.Error {
	if ctx != nil && ctx.Err() != nil {
		return canceledError(ctx.Err())
	}
	if s.idleExpired.Load() || errors.Is(err, errIdleTimeout) {
		return streamFailure(engine.CodeModelStream, engine.FailureClassTransient, "provider_transient", httpStatusOK, s.stats.ProviderRequestID, "provider temporarily unavailable")
	}
	if s.closed || isCanceled(err) {
		return canceledError(err)
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
	// InputTokens is total prompt occupancy including cached input;
	// CachedInputTokens is a strict subset of it (design §20.1). A
	// provider reporting more cached tokens than total input tokens is a
	// classified anomaly, not something to silently clamp or accept.
	if cached > input {
		return nil, errInvalidUsage
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

func (s *chatStream) armIdle() func() {
	if s == nil || s.idleTimeout <= 0 {
		return func() {}
	}
	timer := time.AfterFunc(s.idleTimeout, s.expireIdle)
	return func() { timer.Stop() }
}

func (s *chatStream) expireIdle() {
	if !s.idleExpired.CompareAndSwap(false, true) {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.body != nil {
		_ = s.body.Close()
	}
}

type onceCloser struct {
	once sync.Once
	c    io.ReadCloser
	err  error
}

func (c *onceCloser) Read(p []byte) (int, error) {
	return c.c.Read(p)
}

func (c *onceCloser) Close() error {
	c.once.Do(func() {
		if c.c != nil {
			c.err = c.c.Close()
		}
	})
	return c.err
}
