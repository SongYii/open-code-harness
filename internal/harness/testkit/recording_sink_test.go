package testkit_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestRecordingSinkRecordsAttemptsBeforeOneShotFailure(t *testing.T) {
	sink := &testkit.RecordingSink{FailOrdinal: 2, Failure: errors.New("injected")}
	events := []engine.RuntimeEvent{{Ordinal: 1, Type: engine.RuntimeModelStreamStarted}, {Ordinal: 2, Type: engine.RuntimeModelTextDelta, Text: "first"}, {Ordinal: 2, Type: engine.RuntimeModelTextDelta, Text: "second"}}
	for i, event := range events {
		err := sink.Emit(context.Background(), event)
		if (err != nil) != (i == 1) {
			t.Fatalf("Emit(%d) error = %v, want failure=%t", i, err, i == 1)
		}
	}
	if got, want := sink.Attempts(), events; !reflect.DeepEqual(got, want) {
		t.Fatalf("Attempts() = %#v, want %#v", got, want)
	}
	wantDelivered := []engine.RuntimeEvent{events[0], events[2]}
	if got := sink.Delivered(); !reflect.DeepEqual(got, wantDelivered) {
		t.Fatalf("Delivered() = %#v, want %#v", got, wantDelivered)
	}
	attempts := sink.Attempts()
	attempts[0].Type = "changed"
	if got := sink.Attempts()[0].Type; got != engine.RuntimeModelStreamStarted {
		t.Fatalf("Attempts() snapshot leaked mutation: %q", got)
	}
}
