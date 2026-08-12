package engine_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	. "github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestTurnRunnerPreservesBoundedUTF8Output(t *testing.T) {
	model := scriptedModel(t, []string{"你", "好\n"})
	sink := &testkit.RecordingSink{}
	emitter, err := NewEmitter(sink, validRunnerCorrelation())
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}

	got, err := runner.Run(context.Background(), RunRequest{
		ModelRequest:      runnerRequest().ModelRequest,
		MaxAssistantBytes: len([]byte("你好\n")),
	}, emitter)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.Text != "你好\n" {
		t.Fatalf("Text = %q, want %q", got.Text, "你好\n")
	}
	assertRunnerCounts(t, model, 3, 1)
	assertRuntimeEvents(t, sink.Attempts(), []RuntimeEvent{
		{Correlation: validRunnerCorrelation(), Ordinal: 1, Type: RuntimeModelStreamStarted},
		{Correlation: validRunnerCorrelation(), Ordinal: 2, Type: RuntimeModelTextDelta, Text: "你"},
		{Correlation: validRunnerCorrelation(), Ordinal: 3, Type: RuntimeModelTextDelta, Text: "好\n"},
	})
	assertRuntimeEvents(t, sink.Delivered(), sink.Attempts())
}

func TestTurnRunnerRejectsInvalidDependenciesAndRequestsBeforeStream(t *testing.T) {
	t.Run("nil and typed nil model", func(t *testing.T) {
		if _, err := NewTurnRunner(nil); !IsCode(err, CodeInvalidRequest) {
			t.Fatalf("NewTurnRunner(nil) error = %v, want invalid_request", err)
		}
		var typedNil *nilRunnerModel
		if _, err := NewTurnRunner(typedNil); !IsCode(err, CodeInvalidRequest) {
			t.Fatalf("NewTurnRunner(typed nil) error = %v, want invalid_request", err)
		}
	})

	invalid := []struct {
		name   string
		mutate func(*RunRequest)
	}{
		{"missing session", func(request *RunRequest) { request.SessionID = "" }},
		{"invalid turn", func(request *RunRequest) { request.TurnID = " turn" }},
		{"missing item", func(request *RunRequest) { request.ItemID = "" }},
		{"empty input", func(request *RunRequest) { request.Input = "" }},
		{"blank input", func(request *RunRequest) { request.Input = " \t" }},
		{"invalid utf8 input", func(request *RunRequest) { request.Input = "\xff" }},
		{"zero limit", func(request *RunRequest) { request.MaxAssistantBytes = 0 }},
		{"negative limit", func(request *RunRequest) { request.MaxAssistantBytes = -1 }},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			model := scriptedModel(t, nil)
			emitter, _ := NewEmitter(&testkit.RecordingSink{}, validRunnerCorrelation())
			runner, _ := NewTurnRunner(model)
			request := runnerRequest()
			tc.mutate(&request)
			got, err := runner.Run(context.Background(), request, emitter)
			assertRunFailure(t, got, err, CodeInvalidRequest)
			assertRunnerCounts(t, model, 0, 0)
		})
	}

	t.Run("nil emitter", func(t *testing.T) {
		model := scriptedModel(t, nil)
		runner, _ := NewTurnRunner(model)
		got, err := runner.Run(context.Background(), runnerRequest(), nil)
		assertRunFailure(t, got, err, CodeInvalidRequest)
		assertRunnerCounts(t, model, 0, 0)
	})

	t.Run("typed nil emitter", func(t *testing.T) {
		model := scriptedModel(t, nil)
		runner, _ := NewTurnRunner(model)
		var emitter *Emitter
		got, err := runner.Run(context.Background(), runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeInvalidRequest)
		assertRunnerCounts(t, model, 0, 0)
	})

	t.Run("nil context", func(t *testing.T) {
		model := scriptedModel(t, nil)
		emitter, _ := NewEmitter(&testkit.RecordingSink{}, validRunnerCorrelation())
		runner, _ := NewTurnRunner(model)
		got, err := runner.Run(nil, runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeInvalidRequest)
		assertRunnerCounts(t, model, 0, 0)
	})
}

func TestTurnRunnerStreamPairsAndPullFailures(t *testing.T) {
	startup := errors.New("startup failed")
	streamFailure := errors.New("stream failed")
	cases := []struct {
		name             string
		config           testkit.ScriptedModelConfig
		wantCode         ErrorCode
		wantNext         int
		wantClose        int
		wantAttempts     int
		wantDelivered    int
		wantPrimaryCause error
	}{
		{"nil nil", testkit.ScriptedModelConfig{ReturnNilStream: true}, CodeInvalidStream, 0, 0, 0, 0, nil},
		{"nil startup", testkit.ScriptedModelConfig{StartupError: startup}, CodeModelStartup, 0, 0, 0, 0, startup},
		{"stream startup", testkit.ScriptedModelConfig{StartupError: startup, ReturnStreamOnStartupError: true}, CodeModelStartup, 0, 1, 0, 0, startup},
		{"next error before delta", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: streamFailure}}}, CodeModelStream, 1, 1, 1, 1, streamFailure},
		{"next error after delta", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventTextDelta, Text: "ok"}}, {Err: streamFailure}}}, CodeModelStream, 2, 1, 2, 2, streamFailure},
		{"next event and error", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventTextDelta, Text: "ignored"}, Err: streamFailure}}}, CodeModelStream, 1, 1, 1, 1, streamFailure},
		{"eof before completed", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: io.EOF}}}, CodeInvalidStream, 1, 1, 1, 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, err := testkit.NewScriptedModel(runnerRequest().ModelRequest, tc.config)
			if err != nil {
				t.Fatal(err)
			}
			sink := &testkit.RecordingSink{}
			emitter, _ := NewEmitter(sink, validRunnerCorrelation())
			runner, _ := NewTurnRunner(model)
			got, err := runner.Run(context.Background(), runnerRequest(), emitter)
			assertRunFailure(t, got, err, tc.wantCode)
			if tc.wantPrimaryCause != nil && !errors.Is(err, tc.wantPrimaryCause) {
				t.Fatalf("Run() error = %v, does not retain primary cause %v", err, tc.wantPrimaryCause)
			}
			assertRunnerCounts(t, model, tc.wantNext, tc.wantClose)
			assertAttemptCounts(t, sink, tc.wantAttempts, tc.wantDelivered)
		})
	}
}

func TestTurnRunnerDoesNotNestDependencyEngineErrors(t *testing.T) {
	providerCause := errors.New("provider cause")
	model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: &Error{Code: CodeDelivery, Cause: providerCause}}}})
	sink := &testkit.RecordingSink{}
	emitter, _ := NewEmitter(sink, validRunnerCorrelation())
	runner, _ := NewTurnRunner(model)
	got, err := runner.Run(context.Background(), runnerRequest(), emitter)
	assertRunFailure(t, got, err, CodeModelStream)
	if IsCode(err, CodeDelivery) {
		t.Fatalf("Run() error = %v, must retain only the model_stream Engine code", err)
	}
	if !errors.Is(err, providerCause) {
		t.Fatalf("Run() error = %v, does not retain provider cause", err)
	}
	assertRunnerCounts(t, model, 1, 1)
	assertAttemptCounts(t, sink, 1, 1)
}

func TestTurnRunnerNormalizesJoinedEngineErrorsAlongsideClose(t *testing.T) {
	rawCause := errors.New("provider raw cause")
	closeCause := errors.New("close cause")
	joined := errors.Join(
		&Error{Code: CodeDelivery, Cause: rawCause},
		errors.New("secondary provider branch"),
	)
	model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{
		Steps:      []testkit.ScriptedStep{{Err: joined}},
		CloseError: closeCause,
	})
	sink := &testkit.RecordingSink{}
	emitter, _ := NewEmitter(sink, validRunnerCorrelation())
	runner, _ := NewTurnRunner(model)
	got, err := runner.Run(context.Background(), runnerRequest(), emitter)
	assertRunFailure(t, got, err, CodeModelStream)
	if IsCode(err, CodeDelivery) {
		t.Fatalf("Run() error = %v, must not retain nested delivery Engine code", err)
	}
	if !errors.Is(err, rawCause) || !errors.Is(err, closeCause) {
		t.Fatalf("Run() error = %v, want raw and close causes", err)
	}
	assertRunnerCounts(t, model, 1, 1)
	assertAttemptCounts(t, sink, 1, 1)
}

func TestTurnRunnerCleanupCancellationOrdering(t *testing.T) {
	t.Run("failure cancels derived context before close", func(t *testing.T) {
		var streamCtx context.Context
		closeObserved := make(chan bool, 1)
		streamFailure := errors.New("stream failure")
		stream := &controlledStream{
			next: func(ctx context.Context) (StreamEvent, error) {
				streamCtx = ctx
				return StreamEvent{}, streamFailure
			},
			close: func() error {
				closeObserved <- streamCtx.Err() != nil
				return nil
			},
		}
		runner, _ := NewTurnRunner(&controlledModel{stream: func(context.Context, ModelRequest) (ModelStream, error) { return stream, nil }})
		emitter, _ := NewEmitter(&controlledSink{}, validRunnerCorrelation())
		got, err := runner.Run(context.Background(), runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeModelStream)
		if !<-closeObserved {
			t.Fatal("Close() observed a live derived context on failure")
		}
		if stream.nextCalls != 1 || stream.closeCalls != 1 {
			t.Fatalf("counts = (%d, %d), want (1, 1)", stream.nextCalls, stream.closeCalls)
		}
	})

	t.Run("success closes while derived context is live then cancels", func(t *testing.T) {
		var streamCtx context.Context
		closeObserved := make(chan bool, 1)
		stream := &controlledStream{
			next: func(ctx context.Context) (StreamEvent, error) {
				streamCtx = ctx
				return StreamEvent{Type: StreamEventCompleted}, nil
			},
			close: func() error {
				closeObserved <- streamCtx.Err() == nil
				return nil
			},
		}
		runner, _ := NewTurnRunner(&controlledModel{stream: func(context.Context, ModelRequest) (ModelStream, error) { return stream, nil }})
		emitter, _ := NewEmitter(&controlledSink{}, validRunnerCorrelation())
		got, err := runner.Run(context.Background(), runnerRequest(), emitter)
		if err != nil || got.Text != "" {
			t.Fatalf("Run() = (%#v, %v), want empty success", got, err)
		}
		if !<-closeObserved {
			t.Fatal("Close() did not observe a live derived context on success")
		}
		select {
		case <-streamCtx.Done():
		default:
			t.Fatal("derived context was not canceled before Run returned")
		}
		if stream.nextCalls != 1 || stream.closeCalls != 1 {
			t.Fatalf("counts = (%d, %d), want (1, 1)", stream.nextCalls, stream.closeCalls)
		}
	})

	for _, closeErr := range []error{nil, errors.New("close failure")} {
		name := "parent cancellation during successful close"
		if closeErr != nil {
			name += " with close error"
		}
		t.Run(name, func(t *testing.T) {
			closeEntered := make(chan struct{})
			releaseClose := make(chan struct{})
			stream := &controlledStream{
				next: func(context.Context) (StreamEvent, error) { return StreamEvent{Type: StreamEventCompleted}, nil },
				close: func() error {
					close(closeEntered)
					<-releaseClose
					return closeErr
				},
			}
			runner, _ := NewTurnRunner(&controlledModel{stream: func(context.Context, ModelRequest) (ModelStream, error) { return stream, nil }})
			emitter, _ := NewEmitter(&controlledSink{}, validRunnerCorrelation())
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan runOutcome, 1)
			go func() { result <- runRunner(runner, ctx, emitter) }()
			awaitSignal(t, closeEntered, "Close() entry")
			cancel()
			close(releaseClose)
			outcome := awaitOutcome(t, result)
			assertRunFailure(t, outcome.result, outcome.err, CodeCanceled)
			if !errors.Is(outcome.err, context.Canceled) {
				t.Fatalf("Run() error = %v, does not retain cancellation cause", outcome.err)
			}
			if closeErr != nil && !errors.Is(outcome.err, closeErr) {
				t.Fatalf("Run() error = %v, does not retain close cause", outcome.err)
			}
			if stream.nextCalls != 1 || stream.closeCalls != 1 {
				t.Fatalf("counts = (%d, %d), want (1, 1)", stream.nextCalls, stream.closeCalls)
			}
		})
	}
}

func TestTurnRunnerPreservesOrdinaryWrappedDependencyErrors(t *testing.T) {
	leaf := errors.New("leaf")
	closeLeaf := errors.New("close leaf")
	wrapped := fmt.Errorf("dependency wrapper: %w", leaf)
	wrappedClose := fmt.Errorf("close wrapper: %w", closeLeaf)
	cases := []struct {
		name      string
		config    testkit.ScriptedModelConfig
		wantCode  ErrorCode
		wantNext  int
		wantClose int
		causes    []error
	}{
		{"startup without cleanup", testkit.ScriptedModelConfig{StartupError: wrapped}, CodeModelStartup, 0, 0, []error{wrapped, leaf}},
		{"startup with cleanup", testkit.ScriptedModelConfig{StartupError: wrapped, ReturnStreamOnStartupError: true, CloseError: wrappedClose}, CodeModelStartup, 0, 1, []error{wrapped, leaf, wrappedClose, closeLeaf}},
		{"next without cleanup", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: wrapped}}}, CodeModelStream, 1, 1, []error{wrapped, leaf}},
		{"next with cleanup", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: wrapped}}, CloseError: wrappedClose}, CodeModelStream, 1, 1, []error{wrapped, leaf, wrappedClose, closeLeaf}},
		{"close only", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventCompleted}}}, CloseError: wrappedClose}, CodeModelStream, 1, 1, []error{wrappedClose, closeLeaf}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, tc.config)
			emitter, _ := NewEmitter(&testkit.RecordingSink{}, validRunnerCorrelation())
			runner, _ := NewTurnRunner(model)
			got, err := runner.Run(context.Background(), runnerRequest(), emitter)
			assertRunFailure(t, got, err, tc.wantCode)
			for _, cause := range tc.causes {
				if !errors.Is(err, cause) {
					t.Fatalf("Run() error = %v, does not retain exact cause %v", err, cause)
				}
			}
			assertRunnerCounts(t, model, tc.wantNext, tc.wantClose)
		})
	}
}

func TestTurnRunnerNormalizesTypedNilUnwrappersSafely(t *testing.T) {
	var typedNilSingle error = (*panicSingleUnwrapper)(nil)
	var typedNilMulti error = (*panicMultiUnwrapper)(nil)
	cases := []struct {
		name      string
		config    testkit.ScriptedModelConfig
		wantCode  ErrorCode
		wantNext  int
		wantClose int
	}{
		{"startup single unwrap", testkit.ScriptedModelConfig{StartupError: typedNilSingle}, CodeModelStartup, 0, 0},
		{"next multi unwrap", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: typedNilMulti}}}, CodeModelStream, 1, 1},
		{"close single unwrap", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventCompleted}}}, CloseError: typedNilSingle}, CodeModelStream, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, tc.config)
			emitter, _ := NewEmitter(&testkit.RecordingSink{}, validRunnerCorrelation())
			runner, _ := NewTurnRunner(model)
			got, err := runner.Run(context.Background(), runnerRequest(), emitter)
			assertRunFailure(t, got, err, tc.wantCode)
			if errors.Unwrap(err) == nil {
				t.Fatal("Run() error lost its fallback cause")
			}
			assertRunnerCounts(t, model, tc.wantNext, tc.wantClose)
		})
	}
}

func TestTurnRunnerNormalizesJoinedTypedNilUnwrappersSafely(t *testing.T) {
	var typedNilSingle error = (*panicSingleUnwrapper)(nil)
	var typedNilMulti error = (*panicMultiUnwrapper)(nil)
	rawCause := errors.New("raw cause after typed nil")
	closeCause := errors.New("close cause")
	cases := []struct {
		name      string
		nextErr   error
		closeErr  error
		wantCode  ErrorCode
		wantCause error
	}{
		{"typed nil before eof", errors.Join(typedNilMulti, io.EOF), nil, CodeInvalidStream, nil},
		{"typed nil before ordinary cause", errors.Join(typedNilSingle, rawCause), nil, CodeModelStream, rawCause},
		{"typed nil before engine cause with cleanup", errors.Join(typedNilMulti, &Error{Code: CodeDelivery, Cause: rawCause}), closeCause, CodeModelStream, rawCause},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{
				Steps:      []testkit.ScriptedStep{{Err: tc.nextErr}},
				CloseError: tc.closeErr,
			})
			emitter, _ := NewEmitter(&testkit.RecordingSink{}, validRunnerCorrelation())
			runner, _ := NewTurnRunner(model)
			got, err := runner.Run(context.Background(), runnerRequest(), emitter)
			assertRunFailure(t, got, err, tc.wantCode)
			if tc.wantCause != nil && !errors.Is(err, tc.wantCause) {
				t.Fatalf("Run() error = %v, does not retain raw cause", err)
			}
			if tc.closeErr != nil && !errors.Is(err, tc.closeErr) {
				t.Fatalf("Run() error = %v, does not retain close cause", err)
			}
			if IsCode(err, CodeDelivery) {
				t.Fatalf("Run() error = %v, retains nested delivery Engine code", err)
			}
			assertRunnerCounts(t, model, 1, 1)
		})
	}
}

func TestTurnRunnerCancellationWinsAtDependencyReturnBoundaries(t *testing.T) {
	t.Run("stream nil and error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		model := &controlledModel{stream: func(context.Context, ModelRequest) (ModelStream, error) {
			cancel()
			return nil, errors.New("startup")
		}}
		runner, _ := NewTurnRunner(model)
		sink := &controlledSink{}
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		got, err := runner.Run(ctx, runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeCanceled)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	})

	t.Run("stream nonnil and error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &controlledStream{next: func(context.Context) (StreamEvent, error) { return StreamEvent{}, nil }, close: func() error { return nil }}
		model := &controlledModel{stream: func(context.Context, ModelRequest) (ModelStream, error) {
			cancel()
			return stream, errors.New("startup")
		}}
		runner, _ := NewTurnRunner(model)
		emitter, _ := NewEmitter(&controlledSink{}, validRunnerCorrelation())
		got, err := runner.Run(ctx, runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeCanceled)
		if stream.nextCalls != 0 || stream.closeCalls != 1 {
			t.Fatalf("counts = (%d, %d), want (0, 1)", stream.nextCalls, stream.closeCalls)
		}
	})

	t.Run("event return before processing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &controlledStream{
			next: func(context.Context) (StreamEvent, error) {
				cancel()
				return StreamEvent{Type: StreamEventTextDelta, Text: "not accumulated"}, nil
			},
			close: func() error { return nil },
		}
		sinkCalls := 0
		runner, _ := NewTurnRunner(&controlledModel{stream: func(context.Context, ModelRequest) (ModelStream, error) { return stream, nil }})
		emitter, _ := NewEmitter(&controlledSink{emit: func(context.Context, RuntimeEvent) error { sinkCalls++; return nil }}, validRunnerCorrelation())
		got, err := runner.Run(ctx, runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeCanceled)
		if sinkCalls != 1 || stream.nextCalls != 1 || stream.closeCalls != 1 {
			t.Fatalf("counts = sink=%d next=%d close=%d, want (1, 1, 1)", sinkCalls, stream.nextCalls, stream.closeCalls)
		}
	})

	t.Run("sink return after delta delivery", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &controlledStream{
			next: func(context.Context) (StreamEvent, error) {
				return StreamEvent{Type: StreamEventTextDelta, Text: "not accumulated"}, nil
			},
			close: func() error { return nil },
		}
		sinkCalls := 0
		sink := &controlledSink{emit: func(_ context.Context, event RuntimeEvent) error {
			sinkCalls++
			if event.Type == RuntimeModelTextDelta {
				cancel()
				return errors.New("sink canceled parent")
			}
			return nil
		}}
		runner, _ := NewTurnRunner(&controlledModel{stream: func(context.Context, ModelRequest) (ModelStream, error) { return stream, nil }})
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		got, err := runner.Run(ctx, runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeCanceled)
		if sinkCalls != 2 || stream.nextCalls != 1 || stream.closeCalls != 1 {
			t.Fatalf("counts = sink=%d next=%d close=%d, want (2, 1, 1)", sinkCalls, stream.nextCalls, stream.closeCalls)
		}
	})
}

func TestTurnRunnerRejectsInvalidEventsAndBoundsBeforeDelivery(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	cases := []struct {
		name         string
		steps        []testkit.ScriptedStep
		limit        int
		wantCode     ErrorCode
		wantNext     int
		wantAttempts int
	}{
		{"unknown event", []testkit.ScriptedStep{{Event: StreamEvent{Type: "unknown"}}}, 16, CodeInvalidStream, 1, 1},
		{"empty delta", []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventTextDelta}}}, 16, CodeInvalidStream, 1, 1},
		{"completed with text", []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventCompleted, Text: "not allowed"}}}, 16, CodeInvalidStream, 1, 1},
		{"invalid utf8 delta", []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventTextDelta, Text: invalidUTF8}}}, 16, CodeInvalidStream, 1, 1},
		{"one byte over", []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventTextDelta, Text: "abc"}}}, 2, CodeOutputLimit, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: tc.steps})
			sink := &testkit.RecordingSink{}
			emitter, _ := NewEmitter(sink, validRunnerCorrelation())
			runner, _ := NewTurnRunner(model)
			request := runnerRequest()
			request.MaxAssistantBytes = tc.limit
			got, err := runner.Run(context.Background(), request, emitter)
			assertRunFailure(t, got, err, tc.wantCode)
			assertRunnerCounts(t, model, tc.wantNext, 1)
			assertAttemptCounts(t, sink, tc.wantAttempts, tc.wantAttempts)
		})
	}

	t.Run("exact byte limit", func(t *testing.T) {
		model := scriptedModel(t, []string{"你", "好"})
		sink := &testkit.RecordingSink{}
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		runner, _ := NewTurnRunner(model)
		got, err := runner.Run(context.Background(), RunRequest{ModelRequest: runnerRequest().ModelRequest, MaxAssistantBytes: len([]byte("你好"))}, emitter)
		if err != nil || got.Text != "你好" {
			t.Fatalf("Run() = (%#v, %v), want exact output", got, err)
		}
		assertRunnerCounts(t, model, 3, 1)
		assertAttemptCounts(t, sink, 3, 3)
	})

	t.Run("empty completed output", func(t *testing.T) {
		model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventCompleted}}}})
		sink := &testkit.RecordingSink{}
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		runner, _ := NewTurnRunner(model)
		got, err := runner.Run(context.Background(), runnerRequest(), emitter)
		if err != nil || got.Text != "" {
			t.Fatalf("Run() = (%#v, %v), want empty success", got, err)
		}
		assertRunnerCounts(t, model, 1, 1)
		assertAttemptCounts(t, sink, 1, 1)
	})
}

func TestTurnRunnerDeliveryFailureAndCancellation(t *testing.T) {
	t.Run("started delivery failure owns stream", func(t *testing.T) {
		model := scriptedModel(t, nil)
		sink := &testkit.RecordingSink{FailOrdinal: 1}
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		runner, _ := NewTurnRunner(model)
		got, err := runner.Run(context.Background(), runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeDelivery)
		assertRunnerCounts(t, model, 0, 1)
		assertAttemptCounts(t, sink, 1, 0)
	})

	t.Run("delta delivery failure is not accumulated", func(t *testing.T) {
		model := scriptedModel(t, []string{"not delivered"})
		sink := &testkit.RecordingSink{FailOrdinal: 2}
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		runner, _ := NewTurnRunner(model)
		got, err := runner.Run(context.Background(), runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeDelivery)
		assertRunnerCounts(t, model, 1, 1)
		assertAttemptCounts(t, sink, 2, 1)
		if attempts := sink.Attempts(); attempts[1].Text != "not delivered" {
			t.Fatalf("failed delta attempt = %#v", attempts[1])
		}
	})

	t.Run("canceled before stream", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		model := scriptedModel(t, nil)
		sink := &testkit.RecordingSink{}
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		runner, _ := NewTurnRunner(model)
		got, err := runner.Run(ctx, runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeCanceled)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled cause", err)
		}
		assertRunnerCounts(t, model, 0, 0)
		assertAttemptCounts(t, sink, 0, 0)
	})

	t.Run("canceled in next", func(t *testing.T) {
		entered := make(chan struct{})
		model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{WaitForCancel: true, Entered: entered}}})
		sink := &testkit.RecordingSink{}
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		runner, _ := NewTurnRunner(model)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan struct {
			result RunResult
			err    error
		}, 1)
		go func() {
			got, err := runner.Run(ctx, runnerRequest(), emitter)
			result <- struct {
				result RunResult
				err    error
			}{got, err}
		}()
		<-entered
		cancel()
		got := <-result
		assertRunFailure(t, got.result, got.err, CodeCanceled)
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled cause", got.err)
		}
		assertRunnerCounts(t, model, 1, 1)
		assertAttemptCounts(t, sink, 1, 1)
	})
}

func TestTurnRunnerRetainsPrimaryAndCloseCauses(t *testing.T) {
	startup := errors.New("startup")
	next := errors.New("next")
	closeErr := errors.New("close")
	cases := []struct {
		name     string
		config   testkit.ScriptedModelConfig
		limit    int
		want     ErrorCode
		wantNext int
		primary  error
		sink     *testkit.RecordingSink
	}{
		{"startup", testkit.ScriptedModelConfig{StartupError: startup, ReturnStreamOnStartupError: true, CloseError: closeErr}, 16, CodeModelStartup, 0, startup, &testkit.RecordingSink{}},
		{"next", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Err: next}}, CloseError: closeErr}, 16, CodeModelStream, 1, next, &testkit.RecordingSink{}},
		{"invalid event", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: StreamEvent{Type: "bad"}}}, CloseError: closeErr}, 16, CodeInvalidStream, 1, nil, &testkit.RecordingSink{}},
		{"limit", testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventTextDelta, Text: "two"}}}, CloseError: closeErr}, 1, CodeOutputLimit, 1, nil, &testkit.RecordingSink{}},
		{"delivery", testkit.ScriptedModelConfig{CloseError: closeErr}, 16, CodeDelivery, 0, nil, &testkit.RecordingSink{FailOrdinal: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, tc.config)
			emitter, _ := NewEmitter(tc.sink, validRunnerCorrelation())
			runner, _ := NewTurnRunner(model)
			request := runnerRequest()
			request.MaxAssistantBytes = tc.limit
			got, err := runner.Run(context.Background(), request, emitter)
			assertRunFailure(t, got, err, tc.want)
			if tc.primary != nil && !errors.Is(err, tc.primary) {
				t.Fatalf("Run() error = %v, does not retain primary cause %v", err, tc.primary)
			}
			if !errors.Is(err, closeErr) {
				t.Fatalf("Run() error = %v, does not retain close cause %v", err, closeErr)
			}
			assertRunnerCounts(t, model, tc.wantNext, 1)
		})
	}

	t.Run("close failure after completed", func(t *testing.T) {
		model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: StreamEvent{Type: StreamEventCompleted}}}, CloseError: closeErr})
		sink := &testkit.RecordingSink{}
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		runner, _ := NewTurnRunner(model)
		got, err := runner.Run(context.Background(), runnerRequest(), emitter)
		assertRunFailure(t, got, err, CodeModelStream)
		if !errors.Is(err, closeErr) {
			t.Fatalf("Run() error = %v, does not retain close cause", err)
		}
		assertRunnerCounts(t, model, 1, 1)
		assertAttemptCounts(t, sink, 1, 1)
	})

	t.Run("cancellation keeps its cause beside close failure", func(t *testing.T) {
		entered := make(chan struct{})
		model, _ := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{WaitForCancel: true, Entered: entered}}, CloseError: closeErr})
		sink := &testkit.RecordingSink{}
		emitter, _ := NewEmitter(sink, validRunnerCorrelation())
		runner, _ := NewTurnRunner(model)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := runner.Run(ctx, runnerRequest(), emitter)
			result <- err
		}()
		<-entered
		cancel()
		err := <-result
		assertRunFailure(t, RunResult{}, err, CodeCanceled)
		if !errors.Is(err, context.Canceled) || !errors.Is(err, closeErr) {
			t.Fatalf("Run() error = %v, want cancellation and close causes", err)
		}
		assertRunnerCounts(t, model, 1, 1)
		assertAttemptCounts(t, sink, 1, 1)
	})
}

func scriptedModel(t *testing.T, deltas []string) *testkit.ScriptedModel {
	t.Helper()
	steps := make([]testkit.ScriptedStep, 0, len(deltas)+1)
	for _, delta := range deltas {
		steps = append(steps, testkit.ScriptedStep{Event: StreamEvent{Type: StreamEventTextDelta, Text: delta}})
	}
	steps = append(steps, testkit.ScriptedStep{Event: StreamEvent{Type: StreamEventCompleted}})
	model, err := testkit.NewScriptedModel(runnerRequest().ModelRequest, testkit.ScriptedModelConfig{Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func runnerRequest() RunRequest {
	return RunRequest{ModelRequest: ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"}, MaxAssistantBytes: 64}
}

func validRunnerCorrelation() Correlation {
	return Correlation{SessionID: domain.SessionID("session-1"), TurnID: domain.TurnID("turn-1"), ItemID: domain.ItemID("item-1"), CommandID: domain.CommandID("command-1")}
}

func assertRunFailure(t *testing.T, got RunResult, err error, want ErrorCode) {
	t.Helper()
	if !IsCode(err, want) {
		t.Fatalf("Run() error = %v, want code %q", err, want)
	}
	var engineErr *Error
	if !errors.As(err, &engineErr) || engineErr.Code != want {
		t.Fatalf("Run() outer error = %#v, want code %q", engineErr, want)
	}
	if got != (RunResult{}) {
		t.Fatalf("Run() result = %#v, want zero result", got)
	}
}

func assertRunnerCounts(t *testing.T, model *testkit.ScriptedModel, wantNext, wantClose int) {
	t.Helper()
	if got := model.NextCalls(); got != wantNext {
		t.Fatalf("NextCalls() = %d, want %d", got, wantNext)
	}
	if got := model.CloseCalls(); got != wantClose {
		t.Fatalf("CloseCalls() = %d, want %d", got, wantClose)
	}
}

func assertAttemptCounts(t *testing.T, sink *testkit.RecordingSink, wantAttempts, wantDelivered int) {
	t.Helper()
	if got := len(sink.Attempts()); got != wantAttempts {
		t.Fatalf("attempt count = %d, want %d", got, wantAttempts)
	}
	if got := len(sink.Delivered()); got != wantDelivered {
		t.Fatalf("delivered count = %d, want %d", got, wantDelivered)
	}
}

func assertRuntimeEvents(t *testing.T, got, want []RuntimeEvent) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

type nilRunnerModel struct{}

func (*nilRunnerModel) Stream(context.Context, ModelRequest) (ModelStream, error) { return nil, nil }

type controlledModel struct {
	stream func(context.Context, ModelRequest) (ModelStream, error)
}

func (model *controlledModel) Stream(ctx context.Context, request ModelRequest) (ModelStream, error) {
	return model.stream(ctx, request)
}

type controlledStream struct {
	next       func(context.Context) (StreamEvent, error)
	close      func() error
	nextCalls  int
	closeCalls int
}

func (stream *controlledStream) Next(ctx context.Context) (StreamEvent, error) {
	stream.nextCalls++
	return stream.next(ctx)
}

func (stream *controlledStream) Close() error {
	stream.closeCalls++
	return stream.close()
}

type controlledSink struct {
	emit func(context.Context, RuntimeEvent) error
}

func (sink *controlledSink) Emit(ctx context.Context, event RuntimeEvent) error {
	if sink.emit != nil {
		return sink.emit(ctx, event)
	}
	return nil
}

type panicSingleUnwrapper struct{}

func (*panicSingleUnwrapper) Error() string { return "panic single unwrap" }
func (*panicSingleUnwrapper) Unwrap() error { panic("typed-nil single Unwrap called") }

type panicMultiUnwrapper struct{}

func (*panicMultiUnwrapper) Error() string   { return "panic multi unwrap" }
func (*panicMultiUnwrapper) Unwrap() []error { panic("typed-nil multi Unwrap called") }

type runOutcome struct {
	result RunResult
	err    error
}

func runRunner(runner *TurnRunner, ctx context.Context, emitter *Emitter) runOutcome {
	result, err := runner.Run(ctx, runnerRequest(), emitter)
	return runOutcome{result: result, err: err}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func awaitOutcome(t *testing.T, outcome <-chan runOutcome) runOutcome {
	t.Helper()
	select {
	case got := <-outcome:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run()")
		return runOutcome{}
	}
}
