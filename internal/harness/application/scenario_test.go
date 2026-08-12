package application_test

import (
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
		store, err := memory.NewEventStore(testkit.FixedClock{Time: acceptanceTime}, ids)
		if err != nil {
			t.Fatal(err)
		}
		model, err := testkit.NewScriptedModel(
			engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: scenario.Input},
			testkit.ScriptedModelConfig{Steps: scenario.Steps, StartupError: scenario.StartupError},
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
		service, err := application.NewService(store, ids, runner, config)
		if err != nil {
			t.Fatal(err)
		}
		sink := &testkit.RecordingSink{FailOrdinal: scenario.SinkFailOrdinal}
		return enginescenariotest.Harness{
			Service:       service,
			Store:         store,
			Sink:          sink,
			RuntimeEvents: sink.Attempts,
		}
	})
}

func TestRunTurnSuccessFixtureMatchesLiveTrace(t *testing.T) {
	fixtureFile, err := os.Open("testdata/run_turn_success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureFile.Close()
	fixture, err := domain.DecodeJSONL(fixtureFile)
	if err != nil {
		t.Fatal(err)
	}

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
	item := fixtureState.Turns[result.TurnID].Items[result.ItemID]
	payload, ok := item.Payload.(domain.AssistantMessagePayload)
	if !ok || fixtureState.Status != domain.SessionStatusActive ||
		fixtureState.Turns[result.TurnID].Status != domain.TurnStatusCompleted ||
		item.Status != domain.ItemStatusCompleted || payload.Text != "你好\n" || result.Text != payload.Text {
		t.Fatalf("fixture replay = %#v, result = %#v", fixtureState, result)
	}
}

func runExactSuccessTrace(t *testing.T) ([]domain.RecordedEvent, application.RunTurnResult) {
	t.Helper()
	ids := testkit.NewSequenceIDs()
	store, err := memory.NewEventStore(testkit.FixedClock{Time: acceptanceTime}, ids)
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
	service, err := application.NewService(store, ids, runner, application.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID,
		Input:     "inspect repository",
		Sink:      &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Load(context.Background(), created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return records, result
}
