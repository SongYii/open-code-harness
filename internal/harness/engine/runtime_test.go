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

type runtimeTestSink struct{ events []RuntimeEvent }

func (sink *runtimeTestSink) Emit(_ context.Context, event RuntimeEvent) error {
	sink.events = append(sink.events, event)
	return errors.New("should not be called when exhausted")
}
func runtimeTestCorrelation() Correlation {
	return Correlation{SessionID: "session", TurnID: "turn", ItemID: "item", CommandID: "command"}
}
