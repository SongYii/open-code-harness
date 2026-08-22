package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/application/enginescenariotest"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

var scenarioTime = time.Date(2026, 8, 19, 6, 7, 8, 900_000_000, time.UTC)

// TestEngineScenarioContract runs the Engine scenario suite against the
// durable store, unchanged.
//
// The suite previously ran only against adapters/memory, which made every
// scenario a memory-adapter guarantee rather than an Application guarantee.
// Anything that holds for one and not the other is a defect the suite could
// not see. The harness differs from the in-memory one in exactly two places:
// the store is a real database in a temporary directory, and the writer
// authority comes from the lease the store acquired at open rather than from
// a literal, since SQLite is the authority on its own fencing token.
func TestEngineScenarioContract(t *testing.T) {
	enginescenariotest.Run(t, func(t *testing.T, scenario enginescenariotest.Scenario) enginescenariotest.Harness {
		t.Helper()
		config := tempStoreConfig(t)
		config.RuntimeID = "scenario-runtime"
		store, err := Open(context.Background(), config)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })

		model, err := testkit.NewScriptedModel(
			engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: scenario.Input},
			testkit.ScriptedModelConfig{
				Steps:        scenarioSteps(scenario.Model.Steps),
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
		appConfig := application.DefaultConfig()
		if scenario.MaxBytes > 0 {
			appConfig.MaxAssistantBytes = scenario.MaxBytes
		}
		service, err := application.NewService(
			store,
			testkit.NewSequenceIDs(),
			testkit.FixedClock{Time: scenarioTime},
			runner,
			store.Authority(),
			appConfig,
		)
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

func scenarioSteps(steps []enginescenariotest.Step) []testkit.ScriptedStep {
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
