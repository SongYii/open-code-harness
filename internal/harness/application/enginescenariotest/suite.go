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
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// Scenario configures one deterministic executable-path case.
type Scenario struct {
	Name               string
	Input              string
	Steps              []testkit.ScriptedStep
	StartupError       error
	MaxBytes           int
	CancelDuringStream bool
	SinkFailOrdinal    uint64
	WantStatus         domain.TurnStatus
	WantCategory       application.ErrorCategory
	WantText           string
}

// Harness is one composition of the real application and Engine ports.
type Harness struct {
	Service       *application.Service
	Store         application.EventStore
	Sink          engine.RuntimeSink
	RuntimeEvents func() []engine.RuntimeEvent
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
	tests := []struct {
		scenario    Scenario
		wantRuntime []engine.RuntimeEventType
	}{
		{
			scenario: Scenario{
				Name:       "success",
				Input:      "inspect repository",
				Steps:      successSteps("你", "好\n"),
				WantStatus: domain.TurnStatusCompleted,
				WantText:   "你好\n",
			},
			wantRuntime: []engine.RuntimeEventType{
				engine.RuntimeModelStreamStarted,
				engine.RuntimeModelTextDelta,
				engine.RuntimeModelTextDelta,
				engine.RuntimeAppendCompleted,
				engine.RuntimeModelStreamCompleted,
			},
		},
		{
			scenario: Scenario{
				Name:         "startup failure",
				Input:        "inspect repository",
				StartupError: providerFailure,
				WantStatus:   domain.TurnStatusFailed,
				WantCategory: application.CategoryModel,
			},
			wantRuntime: []engine.RuntimeEventType{
				engine.RuntimeAppendCompleted,
				engine.RuntimeModelStreamFailed,
			},
		},
		{
			scenario: Scenario{
				Name:  "mid-stream failure",
				Input: "inspect repository",
				Steps: []testkit.ScriptedStep{
					{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "partial"}},
					{Err: providerFailure},
				},
				WantStatus:   domain.TurnStatusFailed,
				WantCategory: application.CategoryModel,
			},
			wantRuntime: []engine.RuntimeEventType{
				engine.RuntimeModelStreamStarted,
				engine.RuntimeModelTextDelta,
				engine.RuntimeAppendCompleted,
				engine.RuntimeModelStreamFailed,
			},
		},
		{
			scenario: Scenario{
				Name:               "cancellation",
				Input:              "inspect repository",
				Steps:              []testkit.ScriptedStep{{WaitForCancel: true}},
				CancelDuringStream: true,
				WantStatus:         domain.TurnStatusInterrupted,
				WantCategory:       application.CategoryCanceled,
			},
			wantRuntime: []engine.RuntimeEventType{engine.RuntimeModelStreamStarted},
		},
		{
			scenario: Scenario{
				Name:       "output exactly at limit",
				Input:      "inspect repository",
				Steps:      successSteps("你好"),
				MaxBytes:   len([]byte("你好")),
				WantStatus: domain.TurnStatusCompleted,
				WantText:   "你好",
			},
			wantRuntime: []engine.RuntimeEventType{
				engine.RuntimeModelStreamStarted,
				engine.RuntimeModelTextDelta,
				engine.RuntimeAppendCompleted,
				engine.RuntimeModelStreamCompleted,
			},
		},
		{
			scenario: Scenario{
				Name:         "output one byte over",
				Input:        "inspect repository",
				Steps:        successSteps("abc"),
				MaxBytes:     2,
				WantStatus:   domain.TurnStatusFailed,
				WantCategory: application.CategoryOutputLimit,
			},
			wantRuntime: []engine.RuntimeEventType{
				engine.RuntimeModelStreamStarted,
				engine.RuntimeAppendCompleted,
				engine.RuntimeModelStreamFailed,
			},
		},
		{
			scenario: Scenario{
				Name:            "sink delivery failure",
				Input:           "inspect repository",
				Steps:           successSteps("unaccepted"),
				SinkFailOrdinal: 1,
				WantStatus:      domain.TurnStatusInterrupted,
				WantCategory:    application.CategoryDelivery,
			},
			wantRuntime: []engine.RuntimeEventType{
				engine.RuntimeModelStreamStarted,
				engine.RuntimeAppendCompleted,
				engine.RuntimeModelStreamInterrupted,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.scenario.Name, func(t *testing.T) {
			runScenario(t, factory, test.scenario, test.wantRuntime)
		})
	}
}

func runScenario(t *testing.T, factory Factory, scenario Scenario, wantRuntime []engine.RuntimeEventType) {
	t.Helper()
	scenario.Steps = append([]testkit.ScriptedStep(nil), scenario.Steps...)
	var entered chan struct{}
	if scenario.CancelDuringStream {
		entered = make(chan struct{}, 1)
		blockingStep := -1
		for index := range scenario.Steps {
			if scenario.Steps[index].WaitForCancel || scenario.Steps[index].Release != nil {
				blockingStep = index
				break
			}
		}
		if blockingStep < 0 {
			t.Fatal("cancellation scenario requires a blocking scripted step")
		}
		scenario.Steps[blockingStep].Entered = entered
	}
	harness := factory(t, scenario)
	if harness.Service == nil || isNil(harness.Store) || isNil(harness.Sink) || harness.RuntimeEvents == nil {
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
				SessionID: created.SessionID,
				Input:     scenario.Input,
				Sink:      harness.Sink,
			})
			done <- outcome{result: result, err: runErr}
		}()
		<-entered
		cancel()
		got = <-done
	} else {
		got.result, got.err = harness.Service.RunTurn(ctx, application.RunTurnRequest{
			SessionID: created.SessionID,
			Input:     scenario.Input,
			Sink:      harness.Sink,
		})
	}

	if scenario.WantCategory == "" {
		if got.err != nil {
			t.Fatalf("RunTurn() error = %v", got.err)
		}
	} else {
		if !application.IsCategory(got.err, scenario.WantCategory) {
			t.Fatalf("RunTurn() error = %v, want category %s", got.err, scenario.WantCategory)
		}
		var applicationError *application.Error
		if !errors.As(got.err, &applicationError) || applicationError == nil || !applicationError.TerminalCommitted {
			t.Fatalf("RunTurn() error = %#v, want committed terminal error", got.err)
		}
	}
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

	runtimeEvents := harness.RuntimeEvents()
	if gotTypes := runtimeTypes(runtimeEvents); !reflect.DeepEqual(gotTypes, wantRuntime) {
		t.Fatalf("runtime types = %v, want %v", gotTypes, wantRuntime)
	}
	commandID := got.result.Records[0].CommandID
	for index, event := range runtimeEvents {
		if event.Ordinal != uint64(index+1) || event.SessionID != got.result.SessionID ||
			event.TurnID != got.result.TurnID || event.ItemID != got.result.ItemID || event.CommandID != commandID {
			t.Fatalf("runtime event[%d] correlation/ordinal = %#v", index, event)
		}
	}
}

func successSteps(deltas ...string) []testkit.ScriptedStep {
	steps := make([]testkit.ScriptedStep, 0, len(deltas)+1)
	for _, delta := range deltas {
		steps = append(steps, testkit.ScriptedStep{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: delta}})
	}
	return append(steps, testkit.ScriptedStep{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}})
}

func runtimeTypes(events []engine.RuntimeEvent) []engine.RuntimeEventType {
	types := make([]engine.RuntimeEventType, len(events))
	for index, event := range events {
		types[index] = event.Type
	}
	return types
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
