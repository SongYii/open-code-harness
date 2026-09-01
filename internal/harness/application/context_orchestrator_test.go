package application_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// --- fakes: a faked ContextCheckpointStore (Task 13's port, faked here per
// the implementation plan's own Task 9 note) and a scripted ContextSummarizer. ---

type fakeCheckpointStore struct {
	mu         sync.Mutex
	checkpoint *domain.ContextCheckpointRecord
}

func (store *fakeCheckpointStore) LoadLatestContextCheckpoint(ctx context.Context, sessionID domain.SessionID) (application.ContextCheckpointLookup, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.checkpoint == nil {
		return application.ContextCheckpointLookup{Status: application.ContextCheckpointLookupNone}, nil
	}
	return application.ContextCheckpointLookup{Status: application.ContextCheckpointLookupFound, Checkpoint: *store.checkpoint}, nil
}

func (store *fakeCheckpointStore) set(record domain.ContextCheckpointRecord) {
	store.mu.Lock()
	defer store.mu.Unlock()
	clone := record
	store.checkpoint = &clone
}

type scriptedSummarizer struct {
	mu    sync.Mutex
	calls []application.ContextSummarizeRequest
	text  string
	err   error
}

func (summarizer *scriptedSummarizer) Summarize(ctx context.Context, request application.ContextSummarizeRequest) (application.ContextSummarizeResult, error) {
	summarizer.mu.Lock()
	defer summarizer.mu.Unlock()
	summarizer.calls = append(summarizer.calls, request)
	if summarizer.err != nil {
		return application.ContextSummarizeResult{}, summarizer.err
	}
	return application.ContextSummarizeResult{Text: summarizer.text}, nil
}

func (summarizer *scriptedSummarizer) callCount() int {
	summarizer.mu.Lock()
	defer summarizer.mu.Unlock()
	return len(summarizer.calls)
}

// validSummaryText renders a minimal but structurally valid
// och_context_summary_v1 document -- the exact 8 required headings, in
// order, each with brief content -- short enough that it is reliably
// smaller than the multi-turn history a test compacts.
func validSummaryText() string {
	return strings.Join([]string{
		"## Objective", "Ship the context engine.",
		"## User Constraints", "None.",
		"## Established Facts", "The session has several prior turns.",
		"## Work Completed", "Prior turns ran successfully.",
		"## Files and Commands", "None.",
		"## Open Work", "None.",
		"## Risks and Unknowns", "None.",
		"## Continuation", "Proceed with the next turn.",
	}, "\n")
}

// buildHistorySession creates a Session and runs turnCount real Turns
// through it (each a full RunTurn admission+dispatch, matching how a real
// session accumulates history), returning the store, the replayed Session
// state, and a Scan over it so a test can derive Budget thresholds from
// the real token cost of what it built, rather than guessing byte counts.
func buildHistorySession(t *testing.T, turnCount int) (application.EventStore, domain.Session, contextengine.ScanResult, *testkit.SequenceIDs) {
	t.Helper()
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	store, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	// The same ids generator is reused as deps.IDs by every caller of this
	// helper (never a fresh testkit.NewSequenceIDs()): two independent
	// generators writing AppendIDs into the same store both start their
	// counters at 1, so a fresh one would collide with an AppendID this
	// helper's own history-building already used.
	ids := testkit.NewSequenceIDs()
	longAssistantText := strings.Repeat("assistant response covering prior work in detail. ", 6)
	service := newAcceptanceService(t, store, ids, &acceptanceSuccessModel{text: longAssistantText})
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < turnCount; index++ {
		input := fmt.Sprintf("turn %d: please continue working on the long-running task and describe the current state in detail.", index)
		_, err := service.RunTurn(context.Background(), application.RunTurnRequest{
			SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("request-%d", index)),
			Input: input, Sink: &testkit.RecordingSink{},
		})
		if err != nil {
			t.Fatalf("RunTurn(%d) error = %v", index, err)
		}
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	state, err := domain.Replay(records)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := contextengine.Scan(context.Background(), testPageSource{store: store}, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	return store, state, scan, ids
}

// testPageSource is this test file's own contextengine.PageSource adapter
// over the real EventStore -- used only to let a test learn the real token
// cost of the history it built (to derive robust Budget thresholds), the
// same mechanical translation application's own unexported
// contextEventStorePageSource performs in production.
type testPageSource struct {
	store application.EventStore
}

func (source testPageSource) ReadPage(ctx context.Context, sessionID domain.SessionID, request contextengine.PageRequest) (contextengine.PageResult, error) {
	page, err := source.store.ReadStream(ctx, application.ReadStreamRequest{
		SessionID: sessionID, AfterSequence: request.AfterSequence, Limit: request.Limit, HeadVersion: request.HeadVersion,
	})
	if err != nil {
		return contextengine.PageResult{}, err
	}
	return contextengine.PageResult{Records: page.Records, HeadVersion: page.HeadVersion, NextAfterSequence: page.NextAfterSequence, End: page.End}, nil
}

func TestPrepareContextBelowTriggerNeverOpensACompactionBracket(t *testing.T) {
	store, state, scan, historyIDs := buildHistorySession(t, 2)
	fullEstimate := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
	deps := application.ContextOrchestratorDeps{
		Store: store, IDs: historyIDs, Clock: testkit.FixedClock{Time: acceptanceTime},
		Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
		CheckpointStore: &fakeCheckpointStore{}, Summarizer: &scriptedSummarizer{text: validSummaryText()},
		Meter:  contextengine.WireEstimateMeter{},
		Budget: contextengine.Budget{HardInput: fullEstimate * 10, Trigger: fullEstimate * 10, Target: fullEstimate * 5, ProtectedTail: fullEstimate, SummaryOutputCap: 200},
	}
	result, err := application.PrepareContext(context.Background(), deps, state, application.PrepareContextInput{
		SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
	})
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	if result.CompactionRan {
		t.Fatal("PrepareContext() ran a compaction bracket below trigger")
	}
	if result.State.ContextCompaction != nil {
		t.Fatalf("state.ContextCompaction = %#v, want nil", result.State.ContextCompaction)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if _, ok := record.Event.(domain.ContextCompactionStarted); ok {
			t.Fatal("a context.compaction.started event was appended below trigger")
		}
	}
}

func TestPrepareContextAboveTriggerBuildsAndCommitsARollingSummaryCheckpoint(t *testing.T) {
	store, state, scan, historyIDs := buildHistorySession(t, 6)
	fullEstimate := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	deps := application.ContextOrchestratorDeps{
		Store: store, IDs: historyIDs, Clock: testkit.FixedClock{Time: acceptanceTime},
		Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
		CheckpointStore: &fakeCheckpointStore{}, Summarizer: summarizer, Meter: contextengine.WireEstimateMeter{},
		Budget: contextengine.Budget{
			HardInput: fullEstimate * 10, Trigger: fullEstimate / 4, Target: fullEstimate / 8,
			ProtectedTail: fullEstimate / 20, SummaryOutputCap: 400,
		},
	}
	result, err := application.PrepareContext(context.Background(), deps, state, application.PrepareContextInput{
		SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
	})
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	if !result.CompactionRan {
		t.Fatal("PrepareContext() did not run a compaction bracket above trigger")
	}
	if result.State.ContextCompaction != nil {
		t.Fatalf("state.ContextCompaction = %#v, want cleared after completion", result.State.ContextCompaction)
	}
	if result.Prepared.CheckpointKind != contextengine.CheckpointKindRollingSummary {
		t.Fatalf("CheckpointKind = %q, want rolling_summary_v1", result.Prepared.CheckpointKind)
	}
	if summarizer.callCount() != 1 {
		t.Fatalf("summarizer calls = %d, want 1", summarizer.callCount())
	}
	if result.Prepared.EstimatedTotalTokens > deps.Budget.HardInput {
		t.Fatalf("EstimatedTotalTokens = %d, exceeds HardInput %d", result.Prepared.EstimatedTotalTokens, deps.Budget.HardInput)
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var sawStarted, sawCompleted bool
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.ContextCompactionStarted:
			if sawStarted {
				t.Fatal("more than one context.compaction.started appended for one bracket")
			}
			sawStarted = true
			if event.Strategy != domain.ContextStrategySummary {
				t.Fatalf("Strategy = %q, want summary", event.Strategy)
			}
		case domain.ContextCompactionCompleted:
			sawCompleted = true
			if event.Checkpoint.PreviousCheckpointID != "" {
				t.Fatalf("PreviousCheckpointID = %q, want empty for a Session's first checkpoint", event.Checkpoint.PreviousCheckpointID)
			}
			if event.Checkpoint.Summary != validSummaryText() {
				t.Fatalf("Checkpoint.Summary = %q, want the redacted validated summary", event.Checkpoint.Summary)
			}
		case domain.ContextCompactionFailed:
			t.Fatalf("unexpected context.compaction.failed: %#v", event)
		}
	}
	if !sawStarted || !sawCompleted {
		t.Fatalf("sawStarted=%t sawCompleted=%t, want both", sawStarted, sawCompleted)
	}
}

func TestPrepareContextRollingSuccessorContinuesTheDigestChainFromThePriorCheckpoint(t *testing.T) {
	store, state, scan, historyIDs := buildHistorySession(t, 10)
	fullEstimate := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
	checkpointStore := &fakeCheckpointStore{}
	deps := application.ContextOrchestratorDeps{
		Store: store, IDs: historyIDs, Clock: testkit.FixedClock{Time: acceptanceTime},
		Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
		CheckpointStore: checkpointStore, Summarizer: &scriptedSummarizer{text: validSummaryText()}, Meter: contextengine.WireEstimateMeter{},
		Budget: contextengine.Budget{
			HardInput: fullEstimate * 10, Trigger: fullEstimate / 4, Target: fullEstimate / 8,
			ProtectedTail: fullEstimate / 20, SummaryOutputCap: 400,
		},
	}
	input := application.PrepareContextInput{
		SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
	}
	first, err := application.PrepareContext(context.Background(), deps, state, input)
	if err != nil {
		t.Fatalf("first PrepareContext() error = %v", err)
	}
	if !first.CompactionRan || first.Prepared.CheckpointID == "" {
		t.Fatalf("first PrepareContext() did not build a checkpoint: %#v", first)
	}
	firstRecords, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var firstCheckpoint domain.ContextCheckpointRecord
	for _, record := range firstRecords {
		if event, ok := record.Event.(domain.ContextCompactionCompleted); ok {
			firstCheckpoint = event.Checkpoint
		}
	}
	checkpointStore.set(firstCheckpoint)

	// Run more real Turns on the same Session/store so the second round has
	// genuinely new post-checkpoint source events to cover -- otherwise the
	// entire post-checkpoint tail may already fit within ProtectedTail and
	// SelectCutPoint would cover nothing new regardless of Trigger.
	longAssistantText := strings.Repeat("assistant response covering more prior work in detail. ", 6)
	moreTurnsService := newAcceptanceService(t, store, historyIDs, &acceptanceSuccessModel{text: longAssistantText})
	for index := 0; index < 10; index++ {
		if _, err := moreTurnsService.RunTurn(context.Background(), application.RunTurnRequest{
			SessionID: state.ID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("second-round-request-%d", index)),
			Input: fmt.Sprintf("second round turn %d: continue the long-running task with more detail.", index),
			Sink:  &testkit.RecordingSink{},
		}); err != nil {
			t.Fatalf("second-round RunTurn(%d) error = %v", index, err)
		}
	}

	// Force a second round even though the (small) remaining tail alone is
	// below trigger, by tightening Trigger far below what any post-
	// checkpoint tail could avoid -- this test cares about the digest
	// chain continuing correctly, not about re-deriving pressure.
	deps.Budget.Trigger = 1
	preSecondRecords, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	secondState, err := domain.Replay(preSecondRecords)
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.PrepareContext(context.Background(), deps, secondState, input)
	if err != nil {
		t.Fatalf("second PrepareContext() error = %v", err)
	}
	if !second.CompactionRan {
		t.Fatal("second PrepareContext() did not run a rolling-successor compaction")
	}
	secondRecords, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var secondCheckpoint domain.ContextCheckpointRecord
	for _, record := range secondRecords {
		if event, ok := record.Event.(domain.ContextCompactionCompleted); ok {
			secondCheckpoint = event.Checkpoint
		}
	}
	if secondCheckpoint.PreviousCheckpointID != firstCheckpoint.ID {
		t.Fatalf("PreviousCheckpointID = %q, want %q (the first checkpoint)", secondCheckpoint.PreviousCheckpointID, firstCheckpoint.ID)
	}
	if secondCheckpoint.ThroughSequence <= firstCheckpoint.ThroughSequence {
		t.Fatalf("ThroughSequence did not advance: first=%d second=%d", firstCheckpoint.ThroughSequence, secondCheckpoint.ThroughSequence)
	}
	if secondCheckpoint.CoveredEventCount <= firstCheckpoint.CoveredEventCount {
		t.Fatalf("CoveredEventCount did not advance: first=%d second=%d", firstCheckpoint.CoveredEventCount, secondCheckpoint.CoveredEventCount)
	}
	if secondCheckpoint.SourceDigestHex == firstCheckpoint.SourceDigestHex {
		t.Fatal("second checkpoint's digest equals the first's despite covering more source events")
	}

	// Independently recompute the expected chained digest -- continuing
	// from the FIRST checkpoint's own digest and extending only over the
	// newly covered records -- and require an exact match. This is the
	// assertion this test's own mutation check (restarting the chain from
	// D0 instead of the prior checkpoint's digest) must fail: a plain
	// "digests differ" check alone cannot catch that mutation, since any
	// two different source ranges hash differently regardless of seed.
	firstDigestBytes, err := hex.DecodeString(firstCheckpoint.SourceDigestHex)
	if err != nil || len(firstDigestBytes) != 32 {
		t.Fatalf("invalid first checkpoint digest hex %q: %v", firstCheckpoint.SourceDigestHex, err)
	}
	var seed [32]byte
	copy(seed[:], firstDigestBytes)
	newlyCovered := readRecordsRange(t, secondRecords, firstCheckpoint.ThroughSequence, secondCheckpoint.ThroughSequence)
	expectedDigest, _, err := contextengine.ExtendSourceDigestOverRecords(seed, newlyCovered)
	if err != nil {
		t.Fatal(err)
	}
	if secondCheckpoint.SourceDigestHex != hex.EncodeToString(expectedDigest[:]) {
		t.Fatalf("second checkpoint digest = %s, want %s (the chain continued from the first checkpoint's own digest)", secondCheckpoint.SourceDigestHex, hex.EncodeToString(expectedDigest[:]))
	}
}

func TestPrepareContextSummaryFailureBelowHardBudgetProceedsUncompacted(t *testing.T) {
	store, state, scan, historyIDs := buildHistorySession(t, 6)
	fullEstimate := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
	summarizer := &scriptedSummarizer{err: fmt.Errorf("provider unavailable")}
	deps := application.ContextOrchestratorDeps{
		Store: store, IDs: historyIDs, Clock: testkit.FixedClock{Time: acceptanceTime},
		Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
		CheckpointStore: &fakeCheckpointStore{}, Summarizer: summarizer, Meter: contextengine.WireEstimateMeter{},
		Budget: contextengine.Budget{
			HardInput: fullEstimate * 10, Trigger: fullEstimate / 4, Target: fullEstimate / 8,
			ProtectedTail: fullEstimate / 20, SummaryOutputCap: 400,
		},
	}
	result, err := application.PrepareContext(context.Background(), deps, state, application.PrepareContextInput{
		SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
	})
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	if result.CompactionRan {
		t.Fatal("CompactionRan = true, want false: below-hard-budget summary failure proceeds uncompacted, not with a checkpoint")
	}
	if result.Prepared.CheckpointID != "" {
		t.Fatalf("CheckpointID = %q, want empty", result.Prepared.CheckpointID)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var sawFailed bool
	for _, record := range records {
		if _, ok := record.Event.(domain.ContextCompactionFailed); ok {
			sawFailed = true
		}
		if _, ok := record.Event.(domain.ContextCompactionCompleted); ok {
			t.Fatal("a checkpoint was completed despite the summarizer failing")
		}
	}
	if !sawFailed {
		t.Fatal("no context.compaction.failed was appended for the failed summary attempt")
	}
}

func TestPrepareContextSummaryFailureAtHardBudgetFallsBackToDeterministicReset(t *testing.T) {
	store, state, scan, historyIDs := buildHistorySession(t, 6)
	fullEstimate := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
	summarizer := &scriptedSummarizer{err: fmt.Errorf("provider unavailable")}
	// A tight HardInput -- smaller than the full uncompacted history, but
	// still large enough for the fixed reset marker plus the protected
	// tail alone to fit -- forces the "summary failure at hard budget"
	// branch (design §16) without also starving the reset fallback itself.
	deps := application.ContextOrchestratorDeps{
		Store: store, IDs: historyIDs, Clock: testkit.FixedClock{Time: acceptanceTime},
		Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
		CheckpointStore: &fakeCheckpointStore{}, Summarizer: summarizer, Meter: contextengine.WireEstimateMeter{},
		Budget: contextengine.Budget{
			HardInput: fullEstimate / 2, Trigger: fullEstimate / 4, Target: fullEstimate / 8,
			ProtectedTail: fullEstimate / 20, SummaryOutputCap: 400,
		},
	}
	result, err := application.PrepareContext(context.Background(), deps, state, application.PrepareContextInput{
		SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
	})
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	if !result.CompactionRan {
		t.Fatal("CompactionRan = false, want true: a hard-budget summary failure should fall back to a deterministic reset")
	}
	if result.Prepared.CheckpointKind != contextengine.CheckpointKindSourceTailReset {
		t.Fatalf("CheckpointKind = %q, want source_tail_reset_v1", result.Prepared.CheckpointKind)
	}
	if result.Prepared.EstimatedTotalTokens > deps.Budget.HardInput {
		t.Fatalf("EstimatedTotalTokens = %d, exceeds HardInput %d", result.Prepared.EstimatedTotalTokens, deps.Budget.HardInput)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var startedCount, failedCount, completedCount int
	var lastCompleted domain.ContextCompactionCompleted
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.ContextCompactionStarted:
			startedCount++
		case domain.ContextCompactionFailed:
			failedCount++
		case domain.ContextCompactionCompleted:
			completedCount++
			lastCompleted = event
		}
	}
	if startedCount != 2 || failedCount != 1 || completedCount != 1 {
		t.Fatalf("started=%d failed=%d completed=%d, want 2/1/1 (summary attempt failed, reset attempt completed)", startedCount, failedCount, completedCount)
	}
	if lastCompleted.Checkpoint.Kind != string(contextengine.CheckpointKindSourceTailReset) {
		t.Fatalf("completed checkpoint kind = %q, want source_tail_reset_v1", lastCompleted.Checkpoint.Kind)
	}
	if strings.TrimSpace(lastCompleted.Checkpoint.Summary) != "" {
		t.Fatalf("reset checkpoint carries summary text %q, want none", lastCompleted.Checkpoint.Summary)
	}
}

func TestPrepareContextRejectsInvalidTriggerAndMissingDependencies(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 1)
	validDeps := application.ContextOrchestratorDeps{
		Store: store, IDs: historyIDs, Clock: testkit.FixedClock{Time: acceptanceTime},
		Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
		CheckpointStore: &fakeCheckpointStore{}, Summarizer: &scriptedSummarizer{text: validSummaryText()},
		Meter:  contextengine.WireEstimateMeter{},
		Budget: contextengine.Budget{HardInput: 10_000, Trigger: 9_000, Target: 5_000, ProtectedTail: 1_000, SummaryOutputCap: 200},
	}
	input := application.PrepareContextInput{SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn}

	if _, err := application.PrepareContext(context.Background(), validDeps, state, application.PrepareContextInput{
		SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: "not-a-real-trigger",
	}); !application.IsCategory(err, application.CategoryValidation) {
		t.Fatalf("invalid trigger error = %v, want CategoryValidation", err)
	}

	missingSummarizer := validDeps
	missingSummarizer.Summarizer = nil
	if _, err := application.PrepareContext(context.Background(), missingSummarizer, state, input); !application.IsCategory(err, application.CategoryValidation) {
		t.Fatalf("missing summarizer error = %v, want CategoryValidation", err)
	}

	missingCheckpointStore := validDeps
	missingCheckpointStore.CheckpointStore = nil
	if _, err := application.PrepareContext(context.Background(), missingCheckpointStore, state, input); !application.IsCategory(err, application.CategoryValidation) {
		t.Fatalf("missing checkpoint store error = %v, want CategoryValidation", err)
	}
}

// readRecordsRange returns the records with afterSequence < Sequence <=
// throughSequence from an already-fetched, ordered record slice.
func readRecordsRange(t *testing.T, records []domain.RecordedEvent, afterSequence, throughSequence uint64) []domain.RecordedEvent {
	t.Helper()
	var kept []domain.RecordedEvent
	for _, record := range records {
		if record.Sequence > afterSequence && record.Sequence <= throughSequence {
			kept = append(kept, record)
		}
	}
	return kept
}

func flattenScanMessages(scan contextengine.ScanResult) []domain.ModelPromptMessage {
	var messages []domain.ModelPromptMessage
	for _, unit := range scan.Units {
		messages = append(messages, unit.Messages...)
	}
	return messages
}
