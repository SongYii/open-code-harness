package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestFailedRollingSummaryRetainsAllHistoryAfterOldCheckpoint(t *testing.T) {
	ctx := context.Background()
	store, state, scan, ids := buildHistorySession(t, 8)
	full := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
	checkpoints := &fakeCheckpointStore{}
	deps := application.ContextOrchestratorDeps{Store: store, IDs: ids, Clock: testkit.FixedClock{Time: acceptanceTime}, Authority: application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, CheckpointStore: checkpoints, Summarizer: &scriptedSummarizer{text: validSummaryText()}, Meter: contextengine.WireEstimateMeter{}, Budget: contextengine.Budget{HardInput: full * 10, Trigger: full / 4, Target: full * 5, ProtectedTail: full / 2, SummaryOutputCap: 400}}
	input := application.PrepareContextInput{SessionID: state.ID, TurnID: "pending", ItemID: "pending", Trigger: domain.ContextTriggerPreTurn, Force: true, CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"}}
	first, err := application.PrepareContext(ctx, deps, state, input)
	if err != nil || !first.CompactionRan {
		t.Fatalf("first compaction: %v", err)
	}
	records, err := application.ReadWholeStreamPinned(ctx, store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if event, ok := record.Event.(domain.ContextCompactionCompleted); ok {
			copy := event.Checkpoint
			checkpoints.checkpoint = &copy
		}
	}
	if checkpoints.checkpoint == nil {
		t.Fatal("missing committed checkpoint")
	}
	tail, err := contextengine.Scan(ctx, testPageSource{store: store}, state.ID, 256, checkpoints.checkpoint.ThroughSequence)
	if err != nil {
		t.Fatal(err)
	}
	deps.Budget.ProtectedTail = full / 20
	proposed, err := contextengine.SelectCutPoint(contextengine.PlanInput{Units: tail.Units, Budget: deps.Budget, Meter: deps.Meter, Force: true})
	if err != nil || len(proposed.CoveredUnits) == 0 {
		t.Fatal("fixture does not propose a newer cut")
	}
	deps.Summarizer = &scriptedSummarizer{err: errors.New("provider unavailable")}
	second, err := application.PrepareContext(ctx, deps, first.State, input)
	if err != nil {
		t.Fatal(err)
	}
	if second.CompactionRan || second.Prepared.CheckpointID != first.Prepared.CheckpointID {
		t.Fatal("failed compaction replaced old checkpoint")
	}
	if !reflect.DeepEqual(second.Prepared.Envelope.Messages, first.Prepared.Envelope.Messages) {
		t.Fatal("speculative cut discarded post-checkpoint history")
	}
}
