package application_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/telemetry"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestTelemetryTurnTopologyContainsOnlyMetadata(t *testing.T) {
	const canary = "PROMPT CANARY secret=must-not-enter-telemetry"
	store := newTurnMemoryStore(t)
	model, err := testkit.NewScriptedModel(
		engine.ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: canary},
		testkit.ScriptedModelConfig{Steps: []testkit.ScriptedStep{{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &memory.Telemetry{}
	config := application.DefaultConfig()
	config.Telemetry = recorder
	service := newTurnServiceWithConfig(t, store, testkit.NewSequenceIDs(), model, config)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace/canary-secret"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-telemetry", Input: canary, Sink: &testkit.RecordingSink{},
	})
	if err != nil || result.Status != domain.TurnStatusCompleted {
		t.Fatalf("RunTurn() = (%#v, %v)", result, err)
	}

	records := recorder.Records()
	if strings.Contains(fmt.Sprintf("%#v", records), canary) || strings.Contains(fmt.Sprintf("%#v", records), "canary-secret") {
		t.Fatalf("telemetry contained prompt or path content: %#v", records)
	}
	var turnID uint64
	var childKinds = map[telemetry.Kind]int{}
	for _, record := range records {
		if record.Start.Kind == telemetry.KindTurn {
			turnID = record.ID
			if record.Parent != 0 || record.End.Outcome != telemetry.OutcomeOK {
				t.Fatalf("turn span = %#v, want successful root", record)
			}
		}
	}
	if turnID == 0 {
		t.Fatalf("records = %#v, want turn span", records)
	}
	for _, record := range records {
		if record.Parent == turnID {
			childKinds[record.Start.Kind]++
		}
	}
	if childKinds[telemetry.KindModelRequest] != 1 || childKinds[telemetry.KindStoreAppend] != 2 {
		t.Fatalf("turn child kinds = %#v, want one model request and two logical appends", childKinds)
	}
}
