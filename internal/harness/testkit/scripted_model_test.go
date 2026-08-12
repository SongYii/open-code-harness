package testkit_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/engine/modeltest"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestScriptedModel(t *testing.T) {
	modeltest.Run(t, func(expected engine.ModelRequest, config modeltest.Config) modeltest.Probe {
		steps := make([]testkit.ScriptedStep, len(config.Steps))
		for i, step := range config.Steps {
			steps[i] = testkit.ScriptedStep{Event: step.Event, Err: step.Err, WaitForCancel: step.WaitForCancel}
		}
		model, err := testkit.NewScriptedModel(expected, testkit.ScriptedModelConfig{Steps: steps, StartupError: config.StartupError, ReturnStreamOnStartupError: config.ReturnStreamOnStartupError, ReturnNilStream: config.ReturnNilStream, CloseError: config.CloseError})
		if err != nil {
			t.Fatalf("NewScriptedModel() error = %v", err)
		}
		return model
	})
}

func TestScriptedModelSupportsEveryStartupPairAndDefensiveSnapshots(t *testing.T) {
	expected := engine.ModelRequest{SessionID: "session", TurnID: "turn", ItemID: "item", Input: "input"}
	startup := errors.New("startup")
	cases := []struct {
		name       string
		config     testkit.ScriptedModelConfig
		wantStream bool
		wantErr    bool
	}{
		{"normal", testkit.ScriptedModelConfig{}, true, false},
		{"nil stream", testkit.ScriptedModelConfig{ReturnNilStream: true}, false, false},
		{"startup error", testkit.ScriptedModelConfig{StartupError: startup}, false, true},
		{"stream and startup error", testkit.ScriptedModelConfig{StartupError: startup, ReturnStreamOnStartupError: true}, true, true},
		{"nil takes precedence", testkit.ScriptedModelConfig{StartupError: startup, ReturnStreamOnStartupError: true, ReturnNilStream: true}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, err := testkit.NewScriptedModel(expected, tc.config)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := model.Stream(context.Background(), expected)
			if (stream != nil) != tc.wantStream || (err != nil) != tc.wantErr {
				t.Fatalf("Stream() = (%v, %v), want stream=%t err=%t", stream, err, tc.wantStream, tc.wantErr)
			}
			if stream != nil {
				_ = stream.Close()
			}
		})
	}
	model, err := testkit.NewScriptedModel(expected, testkit.ScriptedModelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = model.Stream(context.Background(), expected)
	calls := model.Calls()
	calls[0].Input = "mutated"
	if got := model.Calls(); !reflect.DeepEqual(got, []engine.ModelRequest{expected}) {
		t.Fatalf("Calls() defensive snapshot = %#v, want %#v", got, []engine.ModelRequest{expected})
	}
}
