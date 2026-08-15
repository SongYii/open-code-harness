package application_test

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/application/enginescenariotest"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

var acceptanceTime = time.Date(2026, 8, 12, 6, 7, 8, 900_000_000, time.UTC)

func TestEngineScenarioContract(t *testing.T) {
	enginescenariotest.Run(t, func(t *testing.T, scenario enginescenariotest.Scenario) enginescenariotest.Harness {
		t.Helper()
		ids := testkit.NewSequenceIDs()
		authority := application.WriterAuthority{RuntimeID: "scenario-runtime", FencingToken: 1}
		store, err := memory.NewEventStore(authority)
		if err != nil {
			t.Fatal(err)
		}
		model, err := testkit.NewScriptedModel(
			engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: scenario.Input},
			testkit.ScriptedModelConfig{
				Steps:        scriptedSteps(scenario.Model.Steps),
				StartupError: scenario.Model.StartupError,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		runner, err := engine.NewTurnRunner(model)
		if err != nil {
			t.Fatal(err)
		}
		config := application.DefaultConfig()
		if scenario.MaxBytes > 0 {
			config.MaxAssistantBytes = scenario.MaxBytes
		}
		service, err := application.NewService(store, ids, testkit.FixedClock{Time: acceptanceTime}, runner, authority, config)
		if err != nil {
			t.Fatal(err)
		}
		sink := &testkit.RecordingSink{FailOrdinal: scenario.SinkFailOrdinal}
		return enginescenariotest.Harness{
			Service:          service,
			Store:            store,
			Sink:             sink,
			RuntimeAttempts:  sink.Attempts,
			RuntimeDelivered: sink.Delivered,
		}
	})
}

func TestRunTurnSuccessFixtureMatchesLiveTrace(t *testing.T) {
	rawFixture, err := os.ReadFile("testdata/run_turn_success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := domain.DecodeJSONL(bytes.NewReader(rawFixture))
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalJSONLLines(t, rawFixture, fixture)

	live, result := runExactSuccessTrace(t)
	if !reflect.DeepEqual(live, fixture) {
		t.Fatalf("live trace differs from generated fixture\nlive: %#v\nfixture: %#v", live, fixture)
	}
	fixtureState, err := domain.Replay(fixture)
	if err != nil {
		t.Fatal(err)
	}
	liveState, err := domain.Replay(live)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(liveState, fixtureState) {
		t.Fatalf("replayed states differ\nlive: %#v\nfixture: %#v", liveState, fixtureState)
	}
	if fixtureState.Status != domain.SessionStatusActive || fixtureState.ActiveTurn != nil || result.Text != "你好\n" {
		t.Fatalf("fixture replay = %#v, result = %#v", fixtureState, result)
	}
}

func scriptedSteps(steps []enginescenariotest.Step) []testkit.ScriptedStep {
	translated := make([]testkit.ScriptedStep, len(steps))
	for index, step := range steps {
		translated[index] = testkit.ScriptedStep{
			Event:         step.Event,
			Err:           step.Err,
			WaitForCancel: step.WaitForCancel,
			Entered:       step.Entered,
			Release:       step.Release,
		}
	}
	return translated
}

func assertCanonicalJSONLLines(t *testing.T, raw []byte, records []domain.RecordedEvent) {
	t.Helper()
	lines := bytes.SplitAfter(raw, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) != len(records) {
		t.Fatalf("fixture JSONL lines = %d, decoded records = %d", len(lines), len(records))
	}
	for index, record := range records {
		canonical, err := domain.MarshalRecordedEvent(record)
		if err != nil {
			t.Fatalf("MarshalRecordedEvent(record %d) error = %v", index, err)
		}
		canonical = append(canonical, '\n')
		if !bytes.Equal(lines[index], canonical) {
			t.Fatalf("fixture line %d is not canonical JSONL\ngot:  %q\nwant: %q", index+1, lines[index], canonical)
		}
	}
}

func runExactSuccessTrace(t *testing.T) ([]domain.RecordedEvent, application.RunTurnResult) {
	t.Helper()
	ids := testkit.NewSequenceIDs()
	authority := application.WriterAuthority{RuntimeID: "scenario-runtime", FencingToken: 1}
	store, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	model, err := testkit.NewScriptedModel(
		engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect repository"},
		testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{
			{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "你"}},
			{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "好\n"}},
			{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, ids, testkit.FixedClock{Time: acceptanceTime}, runner, authority, application.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID,
		RequestID: "request-scenario",
		Input:     "inspect repository",
		Sink:      &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	return records, result
}
