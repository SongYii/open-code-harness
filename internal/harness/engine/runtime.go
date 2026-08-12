package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type RuntimeEventType string

const (
	RuntimeModelStreamStarted     RuntimeEventType = "model.stream.started"
	RuntimeModelTextDelta         RuntimeEventType = "model.text.delta"
	RuntimeModelStreamCompleted   RuntimeEventType = "model.stream.completed"
	RuntimeModelStreamFailed      RuntimeEventType = "model.stream.failed"
	RuntimeModelStreamInterrupted RuntimeEventType = "model.stream.interrupted"
	RuntimeAppendCompleted        RuntimeEventType = "append.completed"
)

type Correlation struct {
	SessionID domain.SessionID
	TurnID    domain.TurnID
	ItemID    domain.ItemID
	CommandID domain.CommandID
}

type RuntimeEvent struct {
	Correlation
	Ordinal uint64
	Type    RuntimeEventType
	Text    string
	Code    string
}

// RuntimePayload excludes correlation and ordinal: Emitter owns both values.
type RuntimePayload struct {
	Type RuntimeEventType
	Text string
	Code string
}

// RuntimeSink is the shared concurrency boundary. Calls are synchronous and
// inline, providing backpressure to a single Emitter.
type RuntimeSink interface {
	Emit(context.Context, RuntimeEvent) error
}

// Emitter is run-scoped, non-copyable after use, and not safe for concurrent
// calls. It creates no goroutines or channels.
type Emitter struct {
	_           noCopy
	sink        RuntimeSink
	correlation Correlation
	nextOrdinal uint64
}

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

func NewEmitter(sink RuntimeSink, correlation Correlation) (*Emitter, error) {
	if isNil(sink) || !validCorrelation(correlation) {
		return nil, &Error{Code: CodeInvalidRequest}
	}
	return &Emitter{sink: sink, correlation: correlation}, nil
}

// Emit validates a caller payload before assigning an ordinal. A sink failure
// consumes its ordinal; an already-canceled context does not.
func (emitter *Emitter) Emit(ctx context.Context, payload RuntimePayload) error {
	if emitter == nil || !validPayload(payload) {
		return &Error{Code: CodeInvalidRequest}
	}
	if cause := ctx.Err(); cause != nil {
		return &Error{Code: CodeCanceled, Cause: cause}
	}
	if emitter.nextOrdinal == math.MaxUint64 {
		return &Error{Code: CodeDelivery, Cause: errRuntimeOrdinalExhausted}
	}
	emitter.nextOrdinal++
	event := RuntimeEvent{Correlation: emitter.correlation, Ordinal: emitter.nextOrdinal, Type: payload.Type, Text: payload.Text, Code: payload.Code}
	if err := emitter.sink.Emit(ctx, event); err != nil {
		if cause := ctx.Err(); cause != nil {
			return &Error{Code: CodeCanceled, Cause: cause}
		}
		return &Error{Code: CodeDelivery, Cause: err}
	}
	return nil
}

var errRuntimeOrdinalExhausted = errors.New("runtime ordinal exhausted")

func validCorrelation(correlation Correlation) bool {
	_, sessionErr := domain.ParseSessionID(string(correlation.SessionID))
	_, turnErr := domain.ParseTurnID(string(correlation.TurnID))
	_, itemErr := domain.ParseItemID(string(correlation.ItemID))
	_, commandErr := domain.ParseCommandID(string(correlation.CommandID))
	return sessionErr == nil && turnErr == nil && itemErr == nil && commandErr == nil
}

func validPayload(payload RuntimePayload) bool {
	switch payload.Type {
	case RuntimeModelStreamStarted, RuntimeModelStreamCompleted, RuntimeAppendCompleted:
		return payload.Text == "" && payload.Code == ""
	case RuntimeModelTextDelta:
		return payload.Text != "" && utf8.ValidString(payload.Text) && payload.Code == ""
	case RuntimeModelStreamFailed, RuntimeModelStreamInterrupted:
		return payload.Text == "" && validStableCode(payload.Code)
	default:
		return false
	}
}

func validStableCode(code string) bool {
	if len(code) == 0 || len(code) > 64 || code[0] < 'a' || code[0] > 'z' {
		return false
	}
	for index := 1; index < len(code); index++ {
		if !(code[index] >= 'a' && code[index] <= 'z' || code[index] >= '0' && code[index] <= '9' || code[index] == '_') {
			return false
		}
	}
	return true
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	}
	return false
}
