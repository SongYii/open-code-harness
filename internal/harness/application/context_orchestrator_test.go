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
	"github.com/SongYii/open-code-harness/internal/harness/engine"
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
	scan, err := contextengine.Scan(context.Background(), testPageSource{store: store}, created.SessionID, 256, 0)
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

// readCountingStore wraps a real EventStore, recording every AfterSequence
// value and record count ReadStream is actually called with. Used below to
// prove PrepareContext, once a usable checkpoint exists, never asks the
// store to read from before that checkpoint's own coverage -- the
// regression test for this milestone's own most significant disclosed
// limitation (context-engine-evidence.md "Remaining blockers" #3): before
// the fix, contextengine.Scan always started at AfterSequence 0 regardless
// of any existing checkpoint.
type readCountingStore struct {
	application.EventStore
	mu             sync.Mutex
	afterSequences []uint64
	recordsRead    int
}

func (store *readCountingStore) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	page, err := store.EventStore.ReadStream(ctx, request)
	store.mu.Lock()
	store.afterSequences = append(store.afterSequences, request.AfterSequence)
	store.recordsRead += len(page.Records)
	store.mu.Unlock()
	return page, err
}

func (store *readCountingStore) reset() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.afterSequences = nil
	store.recordsRead = 0
}

func (store *readCountingStore) minAfterSequence() uint64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	min := uint64(0)
	for i, value := range store.afterSequences {
		if i == 0 || value < min {
			min = value
		}
	}
	return min
}

func TestPrepareContextResumesScanFromCheckpointRatherThanStreamStart(t *testing.T) {
	rawStore, state, scan, historyIDs := buildHistorySession(t, 20)
	store := &readCountingStore{EventStore: rawStore}
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

	// A handful more real Turns so the second round has genuine new
	// post-checkpoint history, small relative to the 20-Turn history
	// already committed before the checkpoint.
	longAssistantText := strings.Repeat("assistant response covering more prior work in detail. ", 6)
	moreTurnsService := newAcceptanceService(t, store, historyIDs, &acceptanceSuccessModel{text: longAssistantText})
	for index := 0; index < 3; index++ {
		if _, err := moreTurnsService.RunTurn(context.Background(), application.RunTurnRequest{
			SessionID: state.ID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("second-round-request-%d", index)),
			Input: fmt.Sprintf("second round turn %d: continue the long-running task with more detail.", index),
			Sink:  &testkit.RecordingSink{},
		}); err != nil {
			t.Fatalf("second-round RunTurn(%d) error = %v", index, err)
		}
	}

	preSecondRecords, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	secondState, err := domain.Replay(preSecondRecords)
	if err != nil {
		t.Fatal(err)
	}
	totalRecordsSoFar := len(preSecondRecords)

	store.reset()
	second, err := application.PrepareContext(context.Background(), deps, secondState, input)
	if err != nil {
		t.Fatalf("second PrepareContext() error = %v", err)
	}
	if second.CompactionRan {
		// A small post-checkpoint tail alone should stay below Trigger
		// (unlike the digest-chain-continuation test above, this test
		// leaves Trigger at its original, generous value): it exists to
		// prove Scan's own read pattern, not to force a second compaction.
		t.Fatalf("second PrepareContext() unexpectedly ran a compaction bracket over a small below-trigger tail")
	}

	if got := store.minAfterSequence(); got < firstCheckpoint.ThroughSequence {
		t.Fatalf("ReadStream was called with AfterSequence=%d during the second PrepareContext, want every call >= the checkpoint's own ThroughSequence=%d (a resumed scan must never re-read pre-checkpoint history)",
			got, firstCheckpoint.ThroughSequence)
	}
	if store.recordsRead >= totalRecordsSoFar {
		t.Fatalf("second PrepareContext read %d records, want fewer than the %d records the whole session has accumulated (a resumed scan must not re-read the full history)",
			store.recordsRead, totalRecordsSoFar)
	}
}

// usageReportingModel is a scripted engine.Model reporting a fixed,
// caller-controlled Usage on every completed attempt -- used below to
// fabricate a real ModelRequestRecorded/ModelUsageRecorded pair with a
// deliberately inflated ObservedInputTokens, so a test can prove design
// §8's non-lowering usage anchor actually changes PrepareContext's own
// Trigger decision, not merely that contextengine.EvaluateUsageAnchor is
// correct in isolation (already covered by contextengine's own
// budget_test.go).
type usageReportingModel struct {
	text  string
	usage engine.TokenUsage
}

func (model *usageReportingModel) Stream(_ context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	usage := model.usage
	events := []engine.StreamEvent{
		{Type: engine.StreamEventTextDelta, Text: model.text},
		{Type: engine.StreamEventCompleted, Usage: &usage},
	}
	return &acceptanceStream{events: events}, nil
}

func newUsageAnchorAwareService(t *testing.T, store application.EventStore, ids application.IDGenerator, model engine.Model, budget contextengine.Budget) *application.Service {
	t.Helper()
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	identity := validTurnRequestIdentity()
	config := application.DefaultConfig()
	config.RequestIdentity = &identity
	config.Context = application.ContextConfig{
		Enabled: true, Budget: budget, Meter: contextengine.WireEstimateMeter{},
		Summarizer: &scriptedSummarizer{text: validSummaryText()}, CheckpointStore: &fakeCheckpointStore{},
	}
	service, err := application.NewService(store, ids, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// runsCompactionOnNextTurn builds a fresh Session, runs 3 short Turns
// through it with usageTokens as every attempt's own reported
// Usage.InputTokens, then runs one more Turn and reports whether THAT
// admission's own pre_turn preparation ran a compaction bracket. Budget is
// deliberately generous enough that the plain wire estimate of this small,
// short-text history alone never crosses Trigger on its own -- only an
// eligible, sufically large usage anchor could force it.
// runsCompactionOnNextTurn returns whether the final Turn's own admission
// ran a compaction bracket, plus that same admission's own recorded
// ContextPreparedRecorded evidence (design §7.4/CE-04).
func runsCompactionOnNextTurn(t *testing.T, usageTokens uint64) (bool, domain.ContextPreparedRecorded) {
	t.Helper()
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	store, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	ids := testkit.NewSequenceIDs()
	model := &usageReportingModel{text: "short reply", usage: engine.TokenUsage{InputTokens: usageTokens}}
	budget := contextengine.Budget{
		HardInput: 1_000_000, Trigger: 50_000, Target: 20_000, ProtectedTail: 1, SummaryOutputCap: 400,
	}
	service := newUsageAnchorAwareService(t, store, ids, model, budget)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
			SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("request-%d", index)),
			Input: fmt.Sprintf("short turn %d", index), Sink: &testkit.RecordingSink{},
		}); err != nil {
			t.Fatalf("RunTurn(%d) error = %v", index, err)
		}
	}
	beforeRecords, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-final", Input: "one more short turn", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatalf("final RunTurn() error = %v", err)
	}
	afterRecords, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var ran bool
	var prepared domain.ContextPreparedRecorded
	for _, record := range afterRecords[len(beforeRecords):] {
		switch event := record.Event.(type) {
		case domain.ContextCompactionStarted:
			ran = true
		case domain.ContextPreparedRecorded:
			prepared = event
		}
	}
	return ran, prepared
}

func TestPrepareContextUsageAnchorForcesCompactionTheWireEstimateWouldHaveMissed(t *testing.T) {
	controlRan, controlPrepared := runsCompactionOnNextTurn(t, 5)
	if controlRan {
		t.Fatal("control case (small, realistic usage) unexpectedly ran a compaction bracket -- the wire estimate of this tiny history should stay below Trigger on its own")
	}
	if controlPrepared.UsageAnchorApplied {
		t.Fatal("control case: UsageAnchorApplied = true, want false (no anchor should have been needed)")
	}
	if controlPrepared.BudgetHardInput != 1_000_000 || controlPrepared.BudgetTrigger != 50_000 || controlPrepared.BudgetTarget != 20_000 {
		t.Fatalf("control case: recorded budget = {HardInput:%d Trigger:%d Target:%d}, want {1000000 50000 20000}",
			controlPrepared.BudgetHardInput, controlPrepared.BudgetTrigger, controlPrepared.BudgetTarget)
	}

	forcedRan, forcedPrepared := runsCompactionOnNextTurn(t, 200_000)
	if !forcedRan {
		t.Fatal("a usage anchor reporting 200,000 observed input tokens (far above Trigger=50,000) did not force a compaction bracket the plain wire estimate of this tiny history would have missed")
	}
	if !forcedPrepared.UsageAnchorApplied {
		t.Fatal("forced case: UsageAnchorApplied = false, want true (the anchor is exactly what forced this round's compaction)")
	}
	if forcedPrepared.UsageAnchorTokens <= 50_000 {
		t.Fatalf("forced case: UsageAnchorTokens = %d, want > Trigger (50000) -- it is what forced compaction", forcedPrepared.UsageAnchorTokens)
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
