// Package enginescenariotest defines the reusable executable Engine scenario
// contract for application/store/model compositions.
package enginescenariotest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// Step describes one adapter-neutral model-stream action. A Factory translates
// it into the concrete model adapter used by its harness.
type Step struct {
	Event         engine.StreamEvent
	Err           error
	WaitForCancel bool
	Entered       chan<- struct{}
	Release       <-chan struct{}
}

// ModelBehavior describes the deterministic model behavior for one Scenario.
type ModelBehavior struct {
	Steps        []Step
	StartupError error
}

// ErrorExpectation is the exact outer Application error contract.
type ErrorExpectation struct {
	Category          application.ErrorCategory
	Code              string
	TerminalCommitted bool
}

// DurableTerminalExpectation is the exact terminal data replayed for both the
// assistant Item and its owning Turn.
type DurableTerminalExpectation struct {
	Code    string
	Message string
}

// RuntimeExpectation is the exact payload attempted or delivered to a sink.
type RuntimeExpectation struct {
	Ordinal uint64
	Type    engine.RuntimeEventType
	Text    string
	Code    string
}

// Scenario configures one deterministic executable-path case.
type Scenario struct {
	Name                 string
	Input                string
	Model                ModelBehavior
	MaxBytes             int
	CancelDuringStream   bool
	SinkFailOrdinal      uint64
	WantStatus           domain.TurnStatus
	WantError            *ErrorExpectation
	WantText             string
	WantDurableTerminal  *DurableTerminalExpectation
	WantRuntimeAttempts  []RuntimeExpectation
	WantRuntimeDelivered []RuntimeExpectation
}

// Harness is one composition of the real application and Engine ports.
// RuntimeAttempts includes every sink call, including a failed delivery;
// RuntimeDelivered includes only calls accepted by the sink.
type Harness struct {
	Service          *application.Service
	Store            application.EventStore
	Sink             engine.RuntimeSink
	RuntimeAttempts  func() []engine.RuntimeEvent
	RuntimeDelivered func() []engine.RuntimeEvent
}

// Factory constructs a fresh harness for one Scenario.
type Factory func(*testing.T, Scenario) Harness

// Run executes the common success, failure, cancellation, output-boundary,
// delivery, replay, and runtime-correlation contract.
func Run(t *testing.T, factory Factory) {
	t.Helper()
	if factory == nil {
		t.Fatal("enginescenariotest: factory is required")
	}
	providerFailure := errors.New("scenario provider failure")
	tests := []Scenario{
		{
			Name:       "success",
			Input:      "inspect repository",
			Model:      ModelBehavior{Steps: successSteps("你", "好\n")},
			WantStatus: domain.TurnStatusCompleted,
			WantText:   "你好\n",
			WantRuntimeAttempts: runtimeExpectations(
				runtime(engine.RuntimeModelStreamStarted, "", ""),
				runtime(engine.RuntimeModelTextDelta, "你", ""),
				runtime(engine.RuntimeModelTextDelta, "好\n", ""),
				runtime(engine.RuntimeAppendCompleted, "", ""),
				runtime(engine.RuntimeModelStreamCompleted, "", ""),
			),
		},
		{
			Name:       "startup failure",
			Input:      "inspect repository",
			Model:      ModelBehavior{StartupError: providerFailure},
			WantStatus: domain.TurnStatusFailed,
			WantError: &ErrorExpectation{
				Category: application.CategoryModel, Code: "model_startup", TerminalCommitted: true,
			},
			WantDurableTerminal: &DurableTerminalExpectation{Code: "model_startup", Message: "model failed before streaming"},
			WantRuntimeAttempts: runtimeExpectations(
				runtime(engine.RuntimeAppendCompleted, "", ""),
				runtime(engine.RuntimeModelStreamFailed, "", "model_startup"),
			),
		},
		{
			Name:  "mid-stream failure",
			Input: "inspect repository",
			Model: ModelBehavior{Steps: []Step{
				{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "partial"}},
				{Err: providerFailure},
			}},
			WantStatus: domain.TurnStatusFailed,
			WantError: &ErrorExpectation{
				Category: application.CategoryModel, Code: "model_stream", TerminalCommitted: true,
			},
			WantDurableTerminal: &DurableTerminalExpectation{Code: "model_stream", Message: "model stream failed"},
			WantRuntimeAttempts: runtimeExpectations(
				runtime(engine.RuntimeModelStreamStarted, "", ""),
				runtime(engine.RuntimeModelTextDelta, "partial", ""),
				runtime(engine.RuntimeAppendCompleted, "", ""),
				runtime(engine.RuntimeModelStreamFailed, "", "model_stream"),
			),
		},
		{
			Name:               "cancellation",
			Input:              "inspect repository",
			Model:              ModelBehavior{Steps: []Step{{WaitForCancel: true}}},
			CancelDuringStream: true,
			WantStatus:         domain.TurnStatusInterrupted,
			WantError: &ErrorExpectation{
				Category: application.CategoryCanceled, Code: "canceled", TerminalCommitted: true,
			},
			WantDurableTerminal: &DurableTerminalExpectation{Code: domain.InterruptionCallerCanceled},
			WantRuntimeAttempts: runtimeExpectations(runtime(engine.RuntimeModelStreamStarted, "", "")),
		},
		{
			Name:       "output exactly at limit",
			Input:      "inspect repository",
			Model:      ModelBehavior{Steps: successSteps("你好")},
			MaxBytes:   len([]byte("你好")),
			WantStatus: domain.TurnStatusCompleted,
			WantText:   "你好",
			WantRuntimeAttempts: runtimeExpectations(
				runtime(engine.RuntimeModelStreamStarted, "", ""),
				runtime(engine.RuntimeModelTextDelta, "你好", ""),
				runtime(engine.RuntimeAppendCompleted, "", ""),
				runtime(engine.RuntimeModelStreamCompleted, "", ""),
			),
		},
		{
			Name:       "output one byte over",
			Input:      "inspect repository",
			Model:      ModelBehavior{Steps: successSteps("abc")},
			MaxBytes:   2,
			WantStatus: domain.TurnStatusFailed,
			WantError: &ErrorExpectation{
				Category: application.CategoryOutputLimit, Code: "output_limit", TerminalCommitted: true,
			},
			WantDurableTerminal: &DurableTerminalExpectation{Code: "output_limit", Message: "assistant output exceeded limit"},
			WantRuntimeAttempts: runtimeExpectations(
				runtime(engine.RuntimeModelStreamStarted, "", ""),
				runtime(engine.RuntimeAppendCompleted, "", ""),
				runtime(engine.RuntimeModelStreamFailed, "", "output_limit"),
			),
		},
		{
			Name:            "sink delivery failure",
			Input:           "inspect repository",
			Model:           ModelBehavior{Steps: successSteps("unaccepted")},
			SinkFailOrdinal: 1,
			WantStatus:      domain.TurnStatusInterrupted,
			WantError: &ErrorExpectation{
				Category: application.CategoryDelivery, Code: "runtime_delivery_failed", TerminalCommitted: true,
			},
			WantDurableTerminal: &DurableTerminalExpectation{Code: domain.InterruptionDeliveryFailed},
			WantRuntimeAttempts: runtimeExpectations(
				runtime(engine.RuntimeModelStreamStarted, "", ""),
				runtime(engine.RuntimeAppendCompleted, "", ""),
				runtime(engine.RuntimeModelStreamInterrupted, "", domain.InterruptionDeliveryFailed),
			),
			WantRuntimeDelivered: runtimeExpectations(
				runtimeAt(2, engine.RuntimeAppendCompleted, "", ""),
				runtimeAt(3, engine.RuntimeModelStreamInterrupted, "", domain.InterruptionDeliveryFailed),
			),
		},
	}

	for _, scenario := range tests {
		if scenario.WantRuntimeDelivered == nil {
			scenario.WantRuntimeDelivered = append([]RuntimeExpectation(nil), scenario.WantRuntimeAttempts...)
		}
		t.Run(scenario.Name, func(t *testing.T) { runScenario(t, factory, scenario) })
	}
}

func runScenario(t *testing.T, factory Factory, scenario Scenario) {
	t.Helper()
	scenario.Model.Steps = append([]Step(nil), scenario.Model.Steps...)
	var entered chan struct{}
	if scenario.CancelDuringStream {
		entered = make(chan struct{}, 1)
		blockingStep := -1
		for index := range scenario.Model.Steps {
			if scenario.Model.Steps[index].WaitForCancel || scenario.Model.Steps[index].Release != nil {
				blockingStep = index
				break
			}
		}
		if blockingStep < 0 {
			t.Fatal("cancellation scenario requires a blocking scripted step")
		}
		scenario.Model.Steps[blockingStep].Entered = entered
	}
	harness := factory(t, scenario)
	if harness.Service == nil || isNil(harness.Store) || isNil(harness.Sink) ||
		harness.RuntimeAttempts == nil || harness.RuntimeDelivered == nil {
		t.Fatal("enginescenariotest: factory returned an incomplete harness")
	}
	created, err := harness.Service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if scenario.CancelDuringStream {
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	var got outcome
	if scenario.CancelDuringStream {
		done := make(chan outcome, 1)
		go func() {
			result, runErr := harness.Service.RunTurn(ctx, application.RunTurnRequest{
				SessionID: created.SessionID, Input: scenario.Input, Sink: harness.Sink,
			})
			done <- outcome{result: result, err: runErr}
		}()
		<-entered
		cancel()
		got = <-done
	} else {
		got.result, got.err = harness.Service.RunTurn(ctx, application.RunTurnRequest{
			SessionID: created.SessionID, Input: scenario.Input, Sink: harness.Sink,
		})
	}

	assertApplicationError(t, got.err, scenario.WantError)
	if got.result.Status != scenario.WantStatus || got.result.Text != scenario.WantText || !got.result.TerminalCommitted || len(got.result.Records) != 4 {
		t.Fatalf("RunTurn() result = %#v, want status=%s text=%q terminal=true", got.result, scenario.WantStatus, scenario.WantText)
	}

	records, err := harness.Store.Load(context.Background(), created.SessionID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(records) != 5 || !reflect.DeepEqual(records[1:], got.result.Records) {
		t.Fatalf("durable records = %#v, result records = %#v", records, got.result.Records)
	}
	state, err := domain.Replay(records)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	turn, exists := state.Turns[got.result.TurnID]
	if !exists || turn.Status != scenario.WantStatus || turn.ActiveItemID != "" || state.ActiveTurnID != "" {
		t.Fatalf("replayed turn = %#v, session = %#v", turn, state)
	}
	item, exists := turn.Items[got.result.ItemID]
	if !exists || item.Status != itemStatusForTurn(scenario.WantStatus) {
		t.Fatalf("replayed item = %#v", item)
	}
	payload, ok := item.Payload.(domain.AssistantMessagePayload)
	if !ok || payload.Text != scenario.WantText {
		t.Fatalf("replayed assistant payload = %#v, want text %q", item.Payload, scenario.WantText)
	}
	assertDurableTerminal(t, turn, item, scenario.WantDurableTerminal)

	attempts := harness.RuntimeAttempts()
	delivered := harness.RuntimeDelivered()
	assertRuntimeEvents(t, "attempts", attempts, scenario.WantRuntimeAttempts, got.result)
	assertRuntimeEvents(t, "delivered", delivered, scenario.WantRuntimeDelivered, got.result)
}

func assertApplicationError(t *testing.T, got error, want *ErrorExpectation) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("RunTurn() error = %v, want nil", got)
		}
		return
	}
	applicationError, ok := got.(*application.Error)
	if !ok || applicationError == nil {
		t.Fatalf("RunTurn() outer error = %#v, want *application.Error", got)
	}
	if applicationError.Category != want.Category || applicationError.Code != want.Code ||
		applicationError.TerminalCommitted != want.TerminalCommitted {
		t.Fatalf("RunTurn() outer error = %#v, want category=%s code=%q terminal=%t", applicationError, want.Category, want.Code, want.TerminalCommitted)
	}
}

func assertDurableTerminal(t *testing.T, turn domain.Turn, item domain.Item, want *DurableTerminalExpectation) {
	t.Helper()
	if want == nil {
		if item.Terminal != nil || turn.FailureCode != "" || turn.FailureText != "" || turn.InterruptWhy != "" {
			t.Fatalf("durable terminal = item:%#v turn:%#v, want no terminal detail", item.Terminal, turn)
		}
		return
	}
	if item.Terminal == nil || item.Terminal.Code != want.Code || item.Terminal.Message != want.Message {
		t.Fatalf("item terminal = %#v, want code=%q message=%q", item.Terminal, want.Code, want.Message)
	}
	if turn.Status == domain.TurnStatusFailed {
		if turn.FailureCode != want.Code || turn.FailureText != want.Message || turn.InterruptWhy != "" {
			t.Fatalf("failed turn terminal = %#v, want code=%q message=%q", turn, want.Code, want.Message)
		}
	} else if turn.Status == domain.TurnStatusInterrupted {
		if turn.InterruptWhy != want.Code || turn.FailureCode != "" || turn.FailureText != "" {
			t.Fatalf("interrupted turn terminal = %#v, want reason=%q", turn, want.Code)
		}
	}
}

func assertRuntimeEvents(t *testing.T, label string, got []engine.RuntimeEvent, want []RuntimeExpectation, result application.RunTurnResult) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("runtime %s count = %d, want %d\ngot: %#v\nwant: %#v", label, len(got), len(want), got, want)
	}
	commandID := result.Records[0].CommandID
	for index, event := range got {
		expected := want[index]
		if event.Type != expected.Type || event.Text != expected.Text || event.Code != expected.Code {
			t.Fatalf("runtime %s[%d] payload = %#v, want %#v", label, index, event, expected)
		}
		if event.Ordinal != expected.Ordinal || event.SessionID != result.SessionID || event.TurnID != result.TurnID ||
			event.ItemID != result.ItemID || event.CommandID != commandID {
			t.Fatalf("runtime %s[%d] correlation/ordinal = %#v", label, index, event)
		}
	}
}

func successSteps(deltas ...string) []Step {
	steps := make([]Step, 0, len(deltas)+1)
	for _, delta := range deltas {
		steps = append(steps, Step{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: delta}})
	}
	return append(steps, Step{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}})
}

func runtime(eventType engine.RuntimeEventType, text, code string) RuntimeExpectation {
	return RuntimeExpectation{Type: eventType, Text: text, Code: code}
}

func runtimeAt(ordinal uint64, eventType engine.RuntimeEventType, text, code string) RuntimeExpectation {
	return RuntimeExpectation{Ordinal: ordinal, Type: eventType, Text: text, Code: code}
}

func runtimeExpectations(events ...RuntimeExpectation) []RuntimeExpectation {
	for index := range events {
		if events[index].Ordinal == 0 {
			events[index].Ordinal = uint64(index + 1)
		}
	}
	return events
}

func itemStatusForTurn(status domain.TurnStatus) domain.ItemStatus {
	switch status {
	case domain.TurnStatusCompleted:
		return domain.ItemStatusCompleted
	case domain.TurnStatusFailed:
		return domain.ItemStatusFailed
	case domain.TurnStatusInterrupted:
		return domain.ItemStatusInterrupted
	default:
		return ""
	}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
