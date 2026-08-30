package engine

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestEmitterDoesNotWrapExhaustedOrdinal(t *testing.T) {
	sink := &runtimeTestSink{}
	emitter, err := NewEmitter(sink, runtimeTestCorrelation())
	if err != nil {
		t.Fatal(err)
	}
	emitter.nextOrdinal = math.MaxUint64
	err = emitter.Emit(context.Background(), RuntimePayload{Type: RuntimeModelStreamStarted})
	if !IsCode(err, CodeDelivery) {
		t.Fatalf("Emit() = %v, want delivery", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("sink saw %#v, want no exhaustion attempt", sink.events)
	}
}

func TestEmitterExhaustionFollowsValidationAndCancellation(t *testing.T) {
	emitter, _ := NewEmitter(&runtimeTestSink{}, runtimeTestCorrelation())
	emitter.nextOrdinal = math.MaxUint64
	if err := emitter.Emit(context.Background(), RuntimePayload{Type: RuntimeModelTextDelta}); !IsCode(err, CodeInvalidRequest) {
		t.Fatalf("invalid Emit() = %v, want invalid_request", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := emitter.Emit(ctx, RuntimePayload{Type: RuntimeModelStreamStarted}); !IsCode(err, CodeCanceled) {
		t.Fatalf("canceled Emit() = %v, want canceled", err)
	}
}

func TestEmitterCarriesArgumentsOnlyOnModelToolCall(t *testing.T) {
	sink := &acceptingRuntimeTestSink{}
	emitter, err := NewEmitter(sink, runtimeTestCorrelation())
	if err != nil {
		t.Fatal(err)
	}
	if err := emitter.Emit(context.Background(), RuntimePayload{Type: RuntimeModelToolCall, Text: "read_file:call-1", Arguments: `{"path":"a.go"}`}); err != nil {
		t.Fatalf("Emit() = %v, want accepted", err)
	}
	if got := sink.events[0].Arguments; got != `{"path":"a.go"}` {
		t.Fatalf("Arguments = %q, want the raw tool call arguments", got)
	}
	if err := emitter.Emit(context.Background(), RuntimePayload{Type: RuntimeModelTextDelta, Text: "hi", Arguments: "should not be set here"}); !IsCode(err, CodeInvalidRequest) {
		t.Fatalf("Emit() = %v, want invalid_request for Arguments on a non-tool-call type", err)
	}
}

func TestEmitterCarriesContentOnlyOnToolExecutionOutcomes(t *testing.T) {
	sink := &acceptingRuntimeTestSink{}
	emitter, err := NewEmitter(sink, runtimeTestCorrelation())
	if err != nil {
		t.Fatal(err)
	}
	if err := emitter.Emit(context.Background(), RuntimePayload{Type: RuntimeToolExecutionCompleted, Text: "read_file:call-1", Content: "file contents"}); err != nil {
		t.Fatalf("Emit() = %v, want accepted", err)
	}
	if got := sink.events[0].Content; got != "file contents" {
		t.Fatalf("Content = %q, want the tool result text", got)
	}
	if err := emitter.Emit(context.Background(), RuntimePayload{Type: RuntimeToolExecutionFailed, Code: "policy_denied", Content: "denied by policy"}); err != nil {
		t.Fatalf("Emit() = %v, want accepted", err)
	}
	if got := sink.events[1].Content; got != "denied by policy" {
		t.Fatalf("Content = %q, want the failure message", got)
	}
	if err := emitter.Emit(context.Background(), RuntimePayload{Type: RuntimeToolExecutionStarted, Text: "read_file:call-1", Content: "should not be set here"}); !IsCode(err, CodeInvalidRequest) {
		t.Fatalf("Emit() = %v, want invalid_request for Content on tool.execution.started", err)
	}
}

type acceptingRuntimeTestSink struct{ events []RuntimeEvent }

func (sink *acceptingRuntimeTestSink) Emit(_ context.Context, event RuntimeEvent) error {
	sink.events = append(sink.events, event)
	return nil
}

type runtimeTestSink struct{ events []RuntimeEvent }

func (sink *runtimeTestSink) Emit(_ context.Context, event RuntimeEvent) error {
	sink.events = append(sink.events, event)
	return errors.New("should not be called when exhausted")
}
func runtimeTestCorrelation() Correlation {
	return Correlation{SessionID: "session", TurnID: "turn", ItemID: "item", CommandID: "command"}
}
