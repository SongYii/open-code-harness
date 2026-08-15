package openaicompat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

func TestStreamSuccessEmitsDeltasCompletedAndUsage(t *testing.T) {
	var closed atomic.Int32
	header := make(http.Header)
	header.Set("Content-Type", "TEXT/EVENT-STREAM; charset=UTF-8")
	header.Set("x-request-id", "req-abc")
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		resp := sseResponse(http.StatusOK, loadSSE(t, "success.sse"), header)
		resp.Body = &closeTracker{ReadCloser: resp.Body, closed: &closed}
		return resp, nil
	}}
	model := newTestModel(t, validConfig(transport))
	stream, err := model.Stream(context.Background(), modelRequest())
	if err != nil {
		t.Fatal(err)
	}
	events, err := collectStream(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if closed.Load() == 0 {
		t.Fatal("response body was not closed")
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	if events[0] != (engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "Hello"}) || events[1].Text != " world" {
		t.Fatalf("deltas = %#v", events[:2])
	}
	if events[2].Type != engine.StreamEventCompleted || events[2].Text != "" || events[2].Usage == nil {
		t.Fatalf("completed = %#v", events[2])
	}
	if *events[2].Usage != (engine.TokenUsage{InputTokens: 8, OutputTokens: 2, CachedInputTokens: 3}) {
		t.Fatalf("usage = %#v", events[2].Usage)
	}
	observer, ok := stream.(engine.AttemptObserver)
	if !ok {
		t.Fatal("stream does not implement AttemptObserver")
	}
	stats := observer.Snapshot()
	if stats.FinishReason != "stop" || stats.ProviderRequestID != "req-abc" || stats.Usage == nil {
		t.Fatalf("snapshot = %#v", stats)
	}
}

func TestStreamIgnoresReasoningContent(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, loadSSE(t, "reasoning.sse"), nil), nil
	}}
	model := newTestModel(t, validConfig(transport))
	stream, err := model.Stream(context.Background(), modelRequest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	events, err := collectStream(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Text != "Visible" || events[1].Type != engine.StreamEventCompleted {
		t.Fatalf("events = %#v", events)
	}
	for _, event := range events {
		if strings.Contains(event.Text, "secret") || strings.Contains(event.Text, "hidden") || strings.Contains(event.Text, "nope") {
			t.Fatalf("reasoning leaked into %q", event.Text)
		}
	}
}

func TestStreamEmptyCompletion(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, loadSSE(t, "empty.sse"), nil), nil
	}}
	model := newTestModel(t, validConfig(transport))
	stream, err := model.Stream(context.Background(), modelRequest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	_, err = collectStream(t, stream)
	failure := requireProviderFailure(t, err, engine.CodeInvalidStream, "empty_response")
	if failure.Retryable {
		t.Fatal("empty completion must not be retryable")
	}
	observer := stream.(engine.AttemptObserver)
	if got := observer.Snapshot().FinishReason; got != "" {
		t.Fatalf("FinishReason = %q, want empty", got)
	}
}

func TestStreamContentFilterAndToolCallsLeaveFinishReasonEmpty(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		code    engine.ErrorCode
		durable string
	}{
		{name: "content_filter", fixture: "content_filter.sse", code: engine.CodeModelStream, durable: "provider_permanent"},
		{name: "tool_calls", fixture: "tool_calls.sse", code: engine.CodeInvalidStream, durable: "capability_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				return sseResponse(http.StatusOK, loadSSE(t, test.fixture), nil), nil
			}}
			model := newTestModel(t, validConfig(transport))
			stream, err := model.Stream(context.Background(), modelRequest())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stream.Close() })
			_, err = collectStream(t, stream)
			requireProviderFailure(t, err, test.code, test.durable)
			if got := stream.(engine.AttemptObserver).Snapshot().FinishReason; got != "" {
				t.Fatalf("FinishReason = %q, want empty", got)
			}
		})
	}
}

func TestStreamUsageAlternateFields(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, loadSSE(t, "usage_alt_fields.sse"), nil), nil
	}}
	model := newTestModel(t, validConfig(transport))
	stream, err := model.Stream(context.Background(), modelRequest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	events, err := collectStream(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	completed := events[len(events)-1]
	if completed.Usage == nil || *completed.Usage != (engine.TokenUsage{InputTokens: 4, OutputTokens: 1, CachedInputTokens: 2}) {
		t.Fatalf("usage = %#v", completed.Usage)
	}
}

func TestStreamRejectsFractionalUsage(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10.5,\"completion_tokens\":1}}\n\ndata: [DONE]\n"
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, body, nil), nil
	}}
	model := newTestModel(t, validConfig(transport))
	stream, err := model.Stream(context.Background(), modelRequest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	_, err = collectStream(t, stream)
	requireProviderFailure(t, err, engine.CodeInvalidStream, "invalid_stream")
}

func TestStreamCancelUnblocksNext(t *testing.T) {
	started := make(chan struct{})
	transport := &scriptedTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		pr, pw := io.Pipe()
		go func() {
			<-req.Context().Done()
			_ = pw.CloseWithError(req.Context().Err())
		}()
		close(started)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       pr,
			Request:    req,
		}, nil
	}}
	model := newTestModel(t, validConfig(transport))
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := model.Stream(ctx, modelRequest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	<-started
	done := make(chan error, 1)
	go func() {
		_, nextErr := stream.Next(ctx)
		done <- nextErr
	}()
	cancel()
	select {
	case err := <-done:
		if !engine.IsCode(err, engine.CodeCanceled) {
			t.Fatalf("Next() error = %v, want canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Next() did not return after cancel")
	}
	if got := stream.(engine.AttemptObserver).Snapshot(); got.FinishReason != "" {
		t.Fatalf("canceled snapshot finish = %q", got.FinishReason)
	}
}

func TestStreamCancelKeepsLatencyAndUsage(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n"
	started := make(chan struct{})
	transport := &scriptedTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		pr, pw := io.Pipe()
		go func() {
			_, _ = pw.Write([]byte(body))
			close(started)
			<-req.Context().Done()
			_ = pw.CloseWithError(req.Context().Err())
		}()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       pr,
			Request:    req,
		}, nil
	}}
	model := newTestModel(t, validConfig(transport))
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := model.Stream(ctx, modelRequest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	event, err := stream.Next(ctx)
	if err != nil || event.Text != "hi" {
		t.Fatalf("first Next() = (%#v, %v)", event, err)
	}
	<-started
	before := stream.(engine.AttemptObserver).Snapshot()
	if before.Usage == nil {
		t.Fatal("expected usage before cancel")
	}
	cancel()
	after := stream.(engine.AttemptObserver).Snapshot()
	if after.Usage == nil || *after.Usage != *before.Usage {
		t.Fatalf("usage cleared after cancel: %#v", after.Usage)
	}
	if after.LatencyMs < before.LatencyMs {
		t.Fatalf("latency cleared after cancel: %d < %d", after.LatencyMs, before.LatencyMs)
	}
}

func TestStreamIdleTimeoutIsTransient(t *testing.T) {
	tests := []struct {
		name       string
		writeFirst bool
	}{
		{name: "blocked before first byte", writeFirst: false},
		{name: "blocked after delta", writeFirst: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var closed atomic.Int32
			transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				body := &stallBody{closed: make(chan struct{})}
				if test.writeFirst {
					body.rest = []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       &closeTracker{ReadCloser: body, closed: &closed},
				}, nil
			}}
			cfg := validConfig(transport)
			cfg.IdleTimeout = 40 * time.Millisecond
			model := newTestModel(t, cfg)
			stream, err := model.Stream(context.Background(), modelRequest())
			if err != nil {
				t.Fatal(err)
			}
			if test.writeFirst {
				event, nextErr := stream.Next(context.Background())
				if nextErr != nil || event.Text != "hi" {
					t.Fatalf("first Next() = (%#v, %v)", event, nextErr)
				}
			}
			done := make(chan error, 1)
			go func() {
				_, nextErr := stream.Next(context.Background())
				done <- nextErr
			}()
			var nextErr error
			select {
			case nextErr = <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("Next() did not return after idle timeout")
			}
			failure := requireProviderFailure(t, nextErr, engine.CodeModelStream, "provider_transient")
			if !failure.Retryable {
				t.Fatal("idle timeout should be retryable")
			}
			if errors.Is(nextErr, io.EOF) {
				t.Fatalf("unwrap chain contains io.EOF: %v", nextErr)
			}
			if got := stream.(engine.AttemptObserver).Snapshot().FinishReason; got != "" {
				t.Fatalf("FinishReason = %q, want empty", got)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			if closed.Load() == 0 {
				t.Fatal("response body was not closed")
			}
		})
	}
}

func TestStreamConnectionDropHasNoEOF(t *testing.T) {
	payload := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(&errAfterReader{data: []byte(payload), err: fmt.Errorf("conn reset: %w", io.EOF)}),
		}, nil
	}}
	model := newTestModel(t, validConfig(transport))
	stream, err := model.Stream(context.Background(), modelRequest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	event, err := stream.Next(context.Background())
	if err != nil || event.Text != "hi" {
		t.Fatalf("first Next() = (%#v, %v)", event, err)
	}
	_, err = stream.Next(context.Background())
	failure := requireProviderFailure(t, err, engine.CodeModelStream, "provider_transient")
	if !failure.Retryable {
		t.Fatal("connection drop should be retryable")
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("unwrap chain contains io.EOF: %v", err)
	}
}

func TestStreamNonSSEAndNon200TwoXXFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header http.Header
		body   string
	}{
		{name: "json 200", status: 200, header: http.Header{"Content-Type": []string{"application/json"}}, body: `{"choices":[{"message":{"content":"nope"}}]}`},
		{name: "missing content type", status: 200, header: http.Header{}, body: `data: {"choices":[]}`},
		{name: "201", status: 201, header: http.Header{"Content-Type": []string{"text/event-stream"}}, body: loadSSE(t, "success.sse")},
		{name: "204", status: 204, header: http.Header{"Content-Type": []string{"text/event-stream"}}, body: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var closed atomic.Int32
			transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				resp := &http.Response{StatusCode: test.status, Header: test.header.Clone(), Body: io.NopCloser(strings.NewReader(test.body))}
				if resp.Header == nil {
					resp.Header = make(http.Header)
				}
				resp.Body = &closeTracker{ReadCloser: resp.Body, closed: &closed}
				return resp, nil
			}}
			model := newTestModel(t, validConfig(transport))
			_, err := model.Stream(context.Background(), modelRequest())
			requireProviderFailure(t, err, engine.CodeModelStartup, "provider_permanent")
			if closed.Load() == 0 {
				t.Fatal("error response body was not closed")
			}
		})
	}
}

func TestStreamRejectsNonStringContentAndOversizeLine(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "array content", body: "data: {\"choices\":[{\"delta\":{\"content\":[{\"type\":\"text\"}]}}]}\n\n"},
		{name: "object content", body: "data: {\"choices\":[{\"message\":{\"content\":{\"text\":\"x\"}}}]}\n\n"},
		{name: "number content", body: "data: {\"choices\":[{\"delta\":{\"content\":1}}]}\n\n"},
		{name: "non json", body: "data: not-json\n\n"},
		{name: "multiple choices", body: "data: {\"choices\":[{},{}]}\n\n"},
		{name: "oversize line", body: "data: " + strings.Repeat("a", 300) + "\n\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				return sseResponse(http.StatusOK, test.body, nil), nil
			}}
			cfg := validConfig(transport)
			if test.name == "oversize line" {
				cfg.MaxSSELineBytes = 64
			}
			model := newTestModel(t, cfg)
			stream, err := model.Stream(context.Background(), modelRequest())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stream.Close() })
			_, err = collectStream(t, stream)
			requireProviderFailure(t, err, engine.CodeInvalidStream, "invalid_stream")
		})
	}
}

func TestStreamConcurrentCallsOwnRequests(t *testing.T) {
	var mu sync.Mutex
	var bodies int
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		mu.Lock()
		bodies++
		mu.Unlock()
		return sseResponse(http.StatusOK, loadSSE(t, "success.sse"), nil), nil
	}}
	model := newTestModel(t, validConfig(transport))
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream, err := model.Stream(context.Background(), modelRequest())
			if err != nil {
				errs <- err
				return
			}
			defer stream.Close()
			_, err = collectStream(t, stream)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if transport.requests.Load() != 2 || bodies != 2 {
		t.Fatalf("requests=%d bodies=%d", transport.requests.Load(), bodies)
	}
}
