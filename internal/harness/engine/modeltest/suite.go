// Package modeltest provides reusable behavioral checks for engine.Model adapters.
package modeltest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

type Factory func(expected engine.ModelRequest, config Config) Probe

type Probe interface {
	engine.Model
	Calls() []engine.ModelRequest
	NextCalls() int
	CloseCalls() int
}

type Config struct {
	Steps                      []ContractStep
	StartupError               error
	ReturnStreamOnStartupError bool
	ReturnNilStream            bool
	CloseError                 error
}

type ContractStep struct {
	Event         engine.StreamEvent
	Err           error
	WaitForCancel bool
}

// Run executes the model-port contract against a factory. A factory's returned
// streams are consumed by exactly one goroutine; Stream itself is exercised
// concurrently across independent requests.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("delivers the complete request and ordered unicode events", func(t *testing.T) {
		expected := request("input \u2705")
		probe := factory(expected, Config{Steps: []ContractStep{
			{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "\u4f60\u597d"}},
			{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: " 🌍"}},
			{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}},
		}})

		stream, err := probe.Stream(context.Background(), expected)
		if err != nil || stream == nil {
			t.Fatalf("Stream() = (%v, %v), want usable stream", stream, err)
		}
		defer func() {
			if err := stream.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		}()

		var got []engine.StreamEvent
		for range 3 {
			event, err := stream.Next(context.Background())
			if err != nil {
				t.Fatalf("Next() error = %v", err)
			}
			got = append(got, event)
		}
		want := []engine.StreamEvent{
			{Type: engine.StreamEventTextDelta, Text: "\u4f60\u597d"},
			{Type: engine.StreamEventTextDelta, Text: " 🌍"},
			{Type: engine.StreamEventCompleted},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("events = %#v, want %#v", got, want)
		}
		if gotCalls := probe.Calls(); !reflect.DeepEqual(gotCalls, []engine.ModelRequest{expected}) {
			t.Fatalf("Calls() = %#v, want %#v", gotCalls, []engine.ModelRequest{expected})
		}
		if probe.NextCalls() != 3 {
			t.Fatalf("NextCalls() = %d, want 3", probe.NextCalls())
		}
	})

	t.Run("returns configured startup error", func(t *testing.T) {
		startup := errors.New("provider unavailable")
		probe := factory(request("startup"), Config{StartupError: startup})
		stream, err := probe.Stream(context.Background(), request("startup"))
		if !errors.Is(err, startup) || stream != nil {
			t.Fatalf("Stream() = (%v, %v), want (nil, startup error)", stream, err)
		}
	})

	t.Run("expresses startup stream pairs and close accounting", func(t *testing.T) {
		startup := errors.New("startup")
		closeErr := errors.New("close")
		cases := []struct {
			name               string
			config             Config
			stream, startupErr bool
		}{
			{"nil nil", Config{ReturnNilStream: true}, false, false},
			{"nil error", Config{StartupError: startup}, false, true},
			{"stream error", Config{StartupError: startup, ReturnStreamOnStartupError: true}, true, true},
			{"nil precedence", Config{StartupError: startup, ReturnStreamOnStartupError: true, ReturnNilStream: true}, false, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				probe := factory(request(tc.name), tc.config)
				stream, err := probe.Stream(context.Background(), request(tc.name))
				if (stream != nil) != tc.stream || (err != nil) != tc.startupErr {
					t.Fatalf("Stream() = (%v, %v), want stream=%t error=%t", stream, err, tc.stream, tc.startupErr)
				}
				if stream != nil {
					_ = stream.Close()
				}
				if got := probe.CloseCalls(); got != map[bool]int{true: 1, false: 0}[tc.stream] {
					t.Fatalf("CloseCalls() = %d", got)
				}
			})
		}
		probe := factory(request("close"), Config{CloseError: closeErr})
		stream, err := probe.Stream(context.Background(), request("close"))
		if err != nil || stream == nil {
			t.Fatal("wanted usable stream")
		}
		if err := stream.Close(); !errors.Is(err, closeErr) {
			t.Fatalf("Close() = %v, want close error", err)
		}
		if probe.CloseCalls() != 1 || probe.NextCalls() != 0 {
			t.Fatalf("counts = (%d, %d), want (1, 0)", probe.CloseCalls(), probe.NextCalls())
		}
	})

	t.Run("returns configured mid-stream error", func(t *testing.T) {
		midstream := errors.New("connection lost")
		probe := factory(request("midstream"), Config{Steps: []ContractStep{{Err: midstream}}})
		stream, err := probe.Stream(context.Background(), request("midstream"))
		if err != nil || stream == nil {
			t.Fatalf("Stream() = (%v, %v), want usable stream", stream, err)
		}
		defer stream.Close()
		_, err = stream.Next(context.Background())
		if !errors.Is(err, midstream) {
			t.Fatalf("Next() error = %v, want mid-stream error", err)
		}
	})

	t.Run("blocks a configured step until cancellation", func(t *testing.T) {
		probe := factory(request("cancel"), Config{Steps: []ContractStep{{WaitForCancel: true}}})
		stream, err := probe.Stream(context.Background(), request("cancel"))
		if err != nil || stream == nil {
			t.Fatalf("Stream() = (%v, %v), want usable stream", stream, err)
		}
		defer stream.Close()
		ctx, cancel := newDoneObservedContext(context.Background())
		result := make(chan error, 1)
		go func() { _, err := stream.Next(ctx); result <- err }()
		select {
		case <-ctx.entered:
		case <-time.After(time.Second):
			t.Fatal("Next() did not begin waiting for context cancellation")
		}
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Next() error = %v, want context.Canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Next() did not unblock after cancellation")
		}
	})

	t.Run("records concurrent stream requests independently", func(t *testing.T) {
		first := request("first")
		probe := factory(first, Config{})
		requests := []engine.ModelRequest{first, request("second")}
		var wait sync.WaitGroup
		for _, req := range requests {
			wait.Add(1)
			go func(req engine.ModelRequest) {
				defer wait.Done()
				stream, err := probe.Stream(context.Background(), req)
				if err == nil && stream != nil {
					_ = stream.Close()
				}
			}(req)
		}
		wait.Wait()
		calls := probe.Calls()
		if len(calls) != 2 {
			t.Fatalf("len(Calls()) = %d, want 2", len(calls))
		}
		seen := map[string]bool{}
		for _, call := range calls {
			seen[call.Input] = true
		}
		if !seen["first"] || !seen["second"] {
			t.Fatalf("Calls() = %#v, want both independent requests", calls)
		}
	})

	RunRuntime(t)
}

// RunRuntime exercises runtime delivery without relying on a concrete sink.
func RunRuntime(t *testing.T) {
	t.Helper()
	t.Run("runtime emitter validates stamps and orders events", func(t *testing.T) {
		sink := &captureSink{}
		emitter, err := engine.NewEmitter(sink, correlation())
		if err != nil {
			t.Fatalf("NewEmitter() error = %v", err)
		}
		payloads := []engine.RuntimePayload{
			{Type: engine.RuntimeModelStreamStarted},
			{Type: engine.RuntimeModelTextDelta, Text: "你好 🌍"},
			{Type: engine.RuntimeModelStreamCompleted},
			{Type: engine.RuntimeModelStreamFailed, Code: "model_stream"},
			{Type: engine.RuntimeModelStreamInterrupted, Code: "canceled"},
			{Type: engine.RuntimeAppendCompleted},
		}
		for _, payload := range payloads {
			if err := emitter.Emit(context.Background(), payload); err != nil {
				t.Fatalf("Emit(%#v) error = %v", payload, err)
			}
		}
		want := []engine.RuntimeEvent{
			{Correlation: correlation(), Ordinal: 1, Type: engine.RuntimeModelStreamStarted},
			{Correlation: correlation(), Ordinal: 2, Type: engine.RuntimeModelTextDelta, Text: "你好 🌍"},
			{Correlation: correlation(), Ordinal: 3, Type: engine.RuntimeModelStreamCompleted},
			{Correlation: correlation(), Ordinal: 4, Type: engine.RuntimeModelStreamFailed, Code: "model_stream"},
			{Correlation: correlation(), Ordinal: 5, Type: engine.RuntimeModelStreamInterrupted, Code: "canceled"},
			{Correlation: correlation(), Ordinal: 6, Type: engine.RuntimeAppendCompleted},
		}
		if !reflect.DeepEqual(sink.events, want) {
			t.Fatalf("events = %#v, want %#v", sink.events, want)
		}
	})

	t.Run("runtime emitter rejects malformed payloads without delivery", func(t *testing.T) {
		sink := &captureSink{}
		emitter, err := engine.NewEmitter(sink, correlation())
		if err != nil {
			t.Fatalf("NewEmitter() error = %v", err)
		}
		invalid := []engine.RuntimePayload{{Type: engine.RuntimeModelStreamStarted, Text: "text"}, {Type: engine.RuntimeModelStreamStarted, Code: "code"}, {Type: engine.RuntimeModelStreamCompleted, Text: "text"}, {Type: engine.RuntimeModelStreamCompleted, Code: "code"}, {Type: engine.RuntimeAppendCompleted, Text: "text"}, {Type: engine.RuntimeAppendCompleted, Code: "code"}, {Type: engine.RuntimeModelTextDelta}, {Type: engine.RuntimeModelTextDelta, Text: "\xff"}, {Type: engine.RuntimeModelTextDelta, Text: "text", Code: "code"}, {Type: engine.RuntimeModelStreamFailed, Text: "text", Code: "model_stream"}, {Type: engine.RuntimeModelStreamFailed}, {Type: engine.RuntimeModelStreamInterrupted, Text: "text", Code: "canceled"}, {Type: engine.RuntimeModelStreamInterrupted}, {Type: engine.RuntimeEventType("unknown")}}
		for _, payload := range invalid {
			if err := emitter.Emit(context.Background(), payload); !engine.IsCode(err, engine.CodeInvalidRequest) {
				t.Errorf("Emit(%#v) error = %v, want invalid_request", payload, err)
			}
		}
		if len(sink.events) != 0 {
			t.Fatalf("delivered = %#v, want none", sink.events)
		}
		if err := emitter.Emit(context.Background(), engine.RuntimePayload{Type: engine.RuntimeModelStreamStarted}); err != nil {
			t.Fatalf("valid Emit() error = %v", err)
		}
		if sink.events[0].Ordinal != 1 {
			t.Fatalf("ordinal = %d, want 1 after rejected payloads", sink.events[0].Ordinal)
		}
	})

	t.Run("runtime emitter maps cancellation and delivery failures", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sink := &captureSink{}
		emitter, err := engine.NewEmitter(sink, correlation())
		if err != nil {
			t.Fatalf("NewEmitter() error = %v", err)
		}
		if err := emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeModelStreamStarted}); !engine.IsCode(err, engine.CodeCanceled) {
			t.Fatalf("Emit(canceled) = %v, want canceled", err)
		}
		if len(sink.events) != 0 {
			t.Fatalf("canceled attempt delivered %#v", sink.events)
		}
		sink.err = errors.New("sink down")
		if err := emitter.Emit(context.Background(), engine.RuntimePayload{Type: engine.RuntimeModelStreamStarted}); !engine.IsCode(err, engine.CodeDelivery) {
			t.Fatalf("Emit(delivery failure) = %v, want delivery", err)
		}
		sink.err = nil
		if err := emitter.Emit(context.Background(), engine.RuntimePayload{Type: engine.RuntimeModelStreamCompleted}); err != nil {
			t.Fatalf("Emit after delivery failure = %v", err)
		}
		if got := sink.events[len(sink.events)-1].Ordinal; got != 2 {
			t.Fatalf("ordinal after failed attempt = %d, want 2", got)
		}
	})

	t.Run("runtime emitter rejects invalid correlation and nil sinks", func(t *testing.T) {
		if _, err := engine.NewEmitter(nil, correlation()); !engine.IsCode(err, engine.CodeInvalidRequest) {
			t.Fatalf("NewEmitter(nil) = %v, want invalid_request", err)
		}
		var typedNil *captureSink
		if _, err := engine.NewEmitter(typedNil, correlation()); !engine.IsCode(err, engine.CodeInvalidRequest) {
			t.Fatalf("NewEmitter(typed nil) = %v, want invalid_request", err)
		}
		for _, mutate := range []func(*engine.Correlation){func(c *engine.Correlation) { c.SessionID = " " }, func(c *engine.Correlation) { c.TurnID = " " }, func(c *engine.Correlation) { c.ItemID = " " }, func(c *engine.Correlation) { c.CommandID = " " }} {
			bad := correlation()
			mutate(&bad)
			if _, err := engine.NewEmitter(&captureSink{}, bad); !engine.IsCode(err, engine.CodeInvalidRequest) {
				t.Fatalf("NewEmitter(bad correlation) = %v, want invalid_request", err)
			}
		}
	})

	t.Run("runtime cancellation after sink attempt is primary", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		sink := cancelingSink{cancel: cancel, err: errors.New("delivery")}
		emitter, _ := engine.NewEmitter(&sink, correlation())
		if err := emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeModelStreamStarted}); !engine.IsCode(err, engine.CodeCanceled) {
			t.Fatalf("Emit() = %v, want canceled", err)
		}
		if len(sink.events) != 1 || sink.events[0].Ordinal != 1 {
			t.Fatalf("attempts = %#v, want ordinal 1 attempt", sink.events)
		}
	})

	t.Run("stable runtime codes accept only lower snake tokens", func(t *testing.T) {
		valid := []string{"a", "model_stream", "a1_b2"}
		invalid := []string{"", string(make([]byte, 65)), "m\u00f6del", "Model", "1model", "model code", "model-code"}
		for _, code := range valid {
			sink := &captureSink{}
			emitter, _ := engine.NewEmitter(sink, correlation())
			if err := emitter.Emit(context.Background(), engine.RuntimePayload{Type: engine.RuntimeModelStreamFailed, Code: code}); err != nil {
				t.Errorf("code %q rejected: %v", code, err)
			}
		}
		for _, code := range invalid {
			sink := &captureSink{}
			emitter, _ := engine.NewEmitter(sink, correlation())
			if err := emitter.Emit(context.Background(), engine.RuntimePayload{Type: engine.RuntimeModelStreamFailed, Code: code}); !engine.IsCode(err, engine.CodeInvalidRequest) {
				t.Errorf("code %q error = %v, want invalid_request", code, err)
			}
		}
	})
}

type captureSink struct {
	events []engine.RuntimeEvent
	err    error
}

type cancelingSink struct {
	events []engine.RuntimeEvent
	cancel context.CancelFunc
	err    error
}

func (sink *cancelingSink) Emit(_ context.Context, event engine.RuntimeEvent) error {
	sink.events = append(sink.events, event)
	sink.cancel()
	return sink.err
}

type doneObservedContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func newDoneObservedContext(parent context.Context) (*doneObservedContext, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	return &doneObservedContext{Context: ctx, entered: make(chan struct{})}, cancel
}
func (ctx *doneObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered) })
	return ctx.Context.Done()
}

func (sink *captureSink) Emit(_ context.Context, event engine.RuntimeEvent) error {
	sink.events = append(sink.events, event)
	return sink.err
}

func request(input string) engine.ModelRequest {
	return engine.ModelRequest{SessionID: "session-contract", TurnID: "turn-contract", ItemID: "item-contract", Input: input}
}
func correlation() engine.Correlation {
	return engine.Correlation{SessionID: domain.SessionID("session-contract"), TurnID: domain.TurnID("turn-contract"), ItemID: domain.ItemID("item-contract"), CommandID: domain.CommandID("command-contract")}
}
