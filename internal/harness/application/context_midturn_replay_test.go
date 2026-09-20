package application_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestMidTurnCompactionDurableReplay(t *testing.T) {
	// Calibrate against the real meter and tool schema, then place the trigger
	// strictly between the initial and post-tool envelopes in a fresh session.
	first, second := runMidTurnReplayFixture(t, 1_000_000, false)
	if second <= first+1 {
		t.Fatal("tool fixture did not grow the envelope")
	}
	runMidTurnReplayFixture(t, first+(second-first)/2, true)
}

func runMidTurnReplayFixture(t *testing.T, trigger uint64, compact bool) (uint64, uint64) {
	t.Helper()
	store, state, _, ids := buildHistorySession(t, 6)
	files := testkit.NewMemFS("/workspace")
	// Grow the envelope enough to trigger, but not so much that the protected
	// in-flight tool result makes a 10% whole-request reduction impossible.
	files.AddFile("large.txt", []byte(strings.Repeat("bounded tool result\n", 200)))
	model := newSequenceModel(
		[]engine.StreamEvent{{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "read-once", Name: tools.NameReadFile, Arguments: `{"path":"large.txt"}`}}, {Type: engine.StreamEventCompleted}},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "done"}, {Type: engine.StreamEventCompleted}},
	)
	catalog, err := tools.NewCatalog(tools.DefaultWorkspaceSpecs())
	if err != nil {
		t.Fatal(err)
	}
	config := newContextAwareToolConfig(catalog, files, testkit.NewScriptedRunner(), nil)
	config.Context.Budget.Trigger = trigger
	config.Context.Budget.Target = trigger * 8 / 10
	config.Context.Budget.ProtectedTail = 100
	if compact {
		config.Context.Budget.Target = 200
	} // Cover enough old turns to amortize summary framing.
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	newService := func() *application.Service {
		s, err := application.NewService(store, ids, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	request := application.RunTurnRequest{SessionID: state.ID, RequestID: "midturn-replay", Input: "read the large file", Sink: &testkit.RecordingSink{}}
	result, err := newService().RunTurn(context.Background(), request)
	if err != nil || result.Status != domain.TurnStatusCompleted {
		t.Fatalf("mid-turn execution: %v", err)
	}
	before, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var preparations []domain.ContextPreparedRecorded
	starts, ends, toolsDone := 0, 0, 0
	for _, record := range before {
		switch e := record.Event.(type) {
		case domain.ContextPreparedRecorded:
			preparations = append(preparations, e)
		case domain.ContextCompactionStarted:
			starts++
			if e.Trigger != domain.ContextTriggerMidTurn {
				t.Fatal("fixture compacted before tool execution")
			}
		case domain.ContextCompactionCompleted:
			ends++
		case domain.ContextCompactionFailed:
			t.Logf("fixture compaction failure: code=%s message=%s", e.Code, e.Message)
		case domain.ToolCallCompleted:
			toolsDone++
		}
	}
	if len(preparations) != 2 || toolsDone != 1 || len(model.Calls()) != 2 {
		t.Fatal("fixture missed two-step tool path")
	}
	if compact && (starts != 1 || ends != 1) || !compact && (starts != 0 || ends != 0) {
		t.Fatalf("compaction bracket starts=%d ends=%d want compact=%t", starts, ends, compact)
	}
	if compact {
		summaries := config.Context.Summarizer.(*scriptedSummarizer).callCount()
		replayed, err := newService().RunTurn(context.Background(), request)
		if err != nil || replayed.Status != result.Status || replayed.Text != result.Text || !replayed.TerminalCommitted {
			t.Fatalf("mid-turn durable replay: %v", err)
		}
		after, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
		if err != nil || !reflect.DeepEqual(before, after) || len(model.Calls()) != 2 || config.Context.Summarizer.(*scriptedSummarizer).callCount() != summaries {
			t.Fatalf("replay reran model/summary or changed history: %v", err)
		}
	}
	return preparations[0].EstimatedTotalTokens, preparations[1].EstimatedTotalTokens
}
