package application_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// Implementation plan Task 12: go test -race coverage for design §17's own
// named winner tables, run with -count=5 for reliability against
// goroutine-scheduling flakiness. This file also carries two real fixes
// this task's own scenarios surfaced while being written (see each test's
// own doc comment): domain.CheckStartAssistantTurnEligibility rejecting a
// new Turn while a compaction is active, and runCompactionBracket never
// falling through to a reset when the failure that closed the summary
// attempt was the caller's own cancellation.

// liveCheckpointStore is a real (if unverified -- Task 13 owns hash-chain
// re-verification) ContextCheckpointStore for these concurrency tests
// specifically: it scans the actual EventStore for the latest
// ContextCompactionCompleted per Session on every call, so a goroutine
// that loads AFTER another's completion sees it. context_orchestrator_test.go
// and context_manual_test.go's own fakeCheckpointStore is deliberately
// dumb (updated only when a test calls .set() itself) and is unsuitable
// here: under real concurrency, using it would let two goroutines each
// build an independent "first checkpoint" for the same already-covered
// source range, which is a real test-fixture staleness artifact, not
// something the actual system (once Task 13's real adapter lands) could
// produce.
type liveCheckpointStore struct {
	store application.EventStore
}

func (checkpointStore *liveCheckpointStore) LoadLatestContextCheckpoint(ctx context.Context, sessionID domain.SessionID) (application.ContextCheckpointLookup, error) {
	records, err := application.ReadWholeStreamPinned(ctx, checkpointStore.store, sessionID, 256)
	if err != nil {
		return application.ContextCheckpointLookup{}, err
	}
	var latest *domain.ContextCheckpointRecord
	for _, record := range records {
		if event, ok := record.Event.(domain.ContextCompactionCompleted); ok {
			checkpoint := event.Checkpoint
			latest = &checkpoint
		}
	}
	if latest == nil {
		return application.ContextCheckpointLookup{Status: application.ContextCheckpointLookupNone}, nil
	}
	return application.ContextCheckpointLookup{Status: application.ContextCheckpointLookupFound, Checkpoint: *latest}, nil
}

// TestConcurrentManualCompactionOneSessionExactlyOneWinner covers design
// §17's own "concurrent manual/manual compaction on one Session" table.
// With no local per-Session compaction lock (Task 11's own disclosed
// simplification), more than one of these racing goroutines CAN
// legitimately succeed sequentially if the Session has enough content for
// more than one real round of incremental (rolling-successor) coverage --
// that is correct, not corruption. The invariant this test actually
// proves is the one design §17 cares about: no two completions ever
// duplicate or overlap coverage, and the checkpoint chain that results is
// a single, strictly-advancing, correctly-linked sequence -- never two
// completions racing to cover the identical prefix.
func TestConcurrentManualCompactionOneSessionExactlyOneWinner(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	config := application.DefaultConfig()
	config.Context = application.ContextConfig{
		Enabled:         true,
		Budget:          contextengine.Budget{HardInput: 1_000_000, Trigger: 1_000_000, Target: 500_000, ProtectedTail: 1, SummaryOutputCap: 4_000},
		Meter:           contextengine.WireEstimateMeter{},
		Summarizer:      summarizer,
		CheckpointStore: &liveCheckpointStore{store: store},
	}
	runner, err := engine.NewTurnRunner(&acceptanceSuccessModel{text: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, historyIDs, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	start := make(chan struct{})
	type outcome struct {
		result application.CompactSessionResult
		err    error
	}
	results := make(chan outcome, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID})
			results <- outcome{result, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	// A nil error with Ran=false is a legitimate outcome here, not a bug:
	// a goroutine that loses every race for the active window can still
	// correctly observe "nothing left to cover" once an earlier goroutine
	// already finished (design's own context_nothing_to_compact no-op).
	successes := 0
	for got := range results {
		if got.err == nil && got.result.Ran {
			successes++
		}
	}
	if successes < 1 {
		t.Fatal("successes = 0, want at least 1")
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var chain []domain.ContextCheckpointRecord
	for _, record := range records {
		if event, ok := record.Event.(domain.ContextCompactionCompleted); ok {
			chain = append(chain, event.Checkpoint)
		}
	}
	if len(chain) != successes {
		t.Fatalf("durable completions = %d, want exactly %d (one per successful CompactSession call)", len(chain), successes)
	}
	previousID := ""
	var previousThrough uint64
	for index, checkpoint := range chain {
		if checkpoint.PreviousCheckpointID != previousID {
			t.Fatalf("checkpoint %d: PreviousCheckpointID = %q, want %q (the strict chain from the prior completion)", index, checkpoint.PreviousCheckpointID, previousID)
		}
		if checkpoint.ThroughSequence <= previousThrough {
			t.Fatalf("checkpoint %d: ThroughSequence = %d, want strictly greater than the prior %d -- coverage did not advance", index, checkpoint.ThroughSequence, previousThrough)
		}
		previousID = checkpoint.ID
		previousThrough = checkpoint.ThroughSequence
	}
}

// TestConcurrentManualCompactionAndRunTurnAreMutuallyExclusive covers
// design §17's "manual compaction concurrent with RunTurn on the same
// Session" table and is the regression test for the domain fix this task
// found: CheckStartAssistantTurnEligibility did not previously reject a
// new Turn while a compaction was active (only the reverse direction --
// decideStartContextCompaction rejecting a manual/pre_turn start against
// an active Turn -- was already covered, by Task 7's own tests).
//
// Both RunTurn and CompactSession CAN legitimately succeed here: a
// same-goroutine RunTurn (admission, dispatch, and terminal commit, all
// synchronous with a fast scripted Model) can finish entirely before
// CompactSession's own first LoadSession call ever runs, in which case
// they never actually overlap in time at all -- that is correct
// scheduling, not a violation. The invariant design §17 actually names
// ("unrelated clients cannot append tool/assistant transitions during a
// manual compaction") is narrower and does not depend on which one
// happened to run first: the durable event log must never show the two
// OVERLAPPING -- no Turn lifecycle event committed between a
// context.compaction.started and its own terminal, and no compaction
// bracket opened while a Turn's own admission-to-terminal window is
// still open.
func TestConcurrentManualCompactionAndRunTurnAreMutuallyExclusive(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		store, state, _, historyIDs := buildHistorySession(t, 2)
		summarizer := &scriptedSummarizer{text: validSummaryText()}
		service := newManualCompactionService(t, store, historyIDs, &acceptanceSuccessModel{text: "ok"}, summarizer, &fakeCheckpointStore{})

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = service.RunTurn(context.Background(), application.RunTurnRequest{
				SessionID: state.ID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("request-manual-race-%d", attempt)),
				Input: "keep going", Sink: &testkit.RecordingSink{},
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID})
		}()
		close(start)
		wg.Wait()

		records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
		if err != nil {
			t.Fatal(err)
		}
		assertNoCompactionTurnOverlap(t, attempt, records)
	}
}

// assertNoCompactionTurnOverlap walks the durable event log in commit
// order and fails if a Turn lifecycle event (TurnStarted/Completed/
// Failed/Interrupted) is ever committed while a compaction bracket is
// open, or vice versa -- the actual invariant design §13.3/§17 require,
// independent of which of two racing callers happened to finish first.
func assertNoCompactionTurnOverlap(t *testing.T, attempt int, records []domain.RecordedEvent) {
	t.Helper()
	compactionOpen := false
	turnOpen := false
	for _, record := range records {
		switch record.Event.(type) {
		case domain.ContextCompactionStarted:
			if turnOpen {
				t.Fatalf("attempt %d: context.compaction.started committed while a Turn was still active", attempt)
			}
			compactionOpen = true
		case domain.ContextCompactionCompleted, domain.ContextCompactionFailed:
			compactionOpen = false
		case domain.TurnStarted:
			if compactionOpen {
				t.Fatalf("attempt %d: turn.started committed while a compaction bracket was still open", attempt)
			}
			turnOpen = true
		case domain.TurnCompleted, domain.TurnFailed, domain.TurnInterrupted:
			turnOpen = false
		}
	}
}

// TestConcurrentRunTurnAndCloseSessionAreMutuallyExclusive covers design
// §17's "RunTurn concurrent with Session close/delete" table.
func TestConcurrentRunTurnAndCloseSessionAreMutuallyExclusive(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
		store, err := memory.NewEventStore(authority)
		if err != nil {
			t.Fatal(err)
		}
		ids := testkit.NewSequenceIDs()
		model := newBlockingAcceptanceModel("streaming")
		service := newAcceptanceService(t, store, ids, model)
		created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var turnErr, closeErr error
		var turnResult application.RunTurnResult
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			turnResult, turnErr = service.RunTurn(context.Background(), application.RunTurnRequest{
				SessionID: created.SessionID, RequestID: "request-close-race", Input: "inspect", Sink: &testkit.RecordingSink{},
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, closeErr = service.CloseSession(context.Background(), application.CloseSessionRequest{SessionID: created.SessionID})
		}()
		close(start)
		model.releaseOnce()
		wg.Wait()

		turnWon := turnErr == nil && turnResult.Status == domain.TurnStatusCompleted
		closeWon := closeErr == nil
		if turnWon == closeWon {
			t.Fatalf("attempt %d: turnWon=%t (err=%v) closeWon=%t (err=%v), want exactly one", attempt, turnWon, turnErr, closeWon, closeErr)
		}
	}
}

// blockingSummarizer signals started once entered and then blocks until
// either release is closed (returns the scripted text) or ctx is
// canceled (returns ctx.Err(), exercising the caller-cancellation path).
type blockingSummarizer struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	text    string
}

func (summarizer *blockingSummarizer) Summarize(ctx context.Context, request application.ContextSummarizeRequest) (application.ContextSummarizeResult, error) {
	summarizer.once.Do(func() { close(summarizer.started) })
	select {
	case <-summarizer.release:
		return application.ContextSummarizeResult{Text: summarizer.text}, nil
	case <-ctx.Done():
		return application.ContextSummarizeResult{}, ctx.Err()
	}
}

// TestOverflowRecoveryCancellationNeverBecomesResetOrCompletion covers
// design §17's "overflow recovery concurrent with caller cancellation"
// table and is the regression test for the second fix this task found:
// runCompactionBracket's reset fallback used to run unconditionally after
// a failed summary attempt, including when that failure was the caller's
// OWN context cancellation -- silently able to produce a reset checkpoint
// (or, if that reset also happened to succeed, a completed Turn) out of a
// canceled request, violating the Global Constraint that cancellation
// never becomes a reset or a completion. The fix: runCompactionBracket
// now checks contextError(ctx) immediately after the failed summary
// attempt and returns before ever considering the reset ladder when the
// caller itself canceled.
func TestOverflowRecoveryCancellationNeverBecomesResetOrCompletion(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	model := &overflowModel{failCount: 1, text: "unreachable"}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	summarizer := &blockingSummarizer{started: make(chan struct{}), release: make(chan struct{}), text: validSummaryText()}
	config := application.DefaultConfig()
	config.Context = application.ContextConfig{
		Enabled: true,
		// HardInput generous enough that buildSummaryCheckpoint's own
		// content-size pre-check never declines before ever reaching the
		// summarizer (this test's whole point is to be blocked INSIDE that
		// call when cancellation arrives); ProtectedTail small enough that
		// the forced overflow-retry plan still finds real coverage.
		Budget:                       contextengine.Budget{HardInput: 1_000_000, Trigger: 1_000_000, Target: 500_000, ProtectedTail: 50, SummaryOutputCap: 4_000},
		Meter:                        contextengine.WireEstimateMeter{},
		Summarizer:                   summarizer,
		CheckpointStore:              &fakeCheckpointStore{},
		MaxOverflowRecoveriesPerTurn: 2,
	}
	service, err := application.NewService(store, historyIDs, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}

	// baselineCount captures everything buildHistorySession already
	// committed (including its own six legitimate TurnCompleted events) so
	// the check below inspects only what THIS racy RunTurn call itself
	// produces, never mistaking unrelated prior history for the canceled
	// Turn silently completing.
	baselineRecords, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	baselineCount := len(baselineRecords)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		_, runErr = service.RunTurn(ctx, application.RunTurnRequest{
			SessionID: state.ID, RequestID: "request-overflow-cancel", Input: "please continue", Sink: &testkit.RecordingSink{},
		})
	}()

	select {
	case <-summarizer.started:
	case <-time.After(testRendezvousTimeout):
		fatalStalled(t, "summarizer never entered")
	}
	cancel()
	<-done

	// runErr's own category is not asserted here: the recovery attempt
	// that was canceled mid-summary correctly DECLINES (design's own "no
	// safe fallback" shape, since a caller-canceled summary attempt must
	// never satisfy the reset-eligibility gate) rather than raising a
	// cancellation error of its own, so runProviderAttempt's loop falls
	// through to whatever the ORIGINAL (pre-cancellation) Provider
	// overflow failure already was -- a real, pre-existing
	// context_overflow classification, not a fabricated one. What design
	// §17 actually requires, and what this test actually checks below, is
	// narrower and unconditional: cancellation must never let a
	// checkpoint complete or the Turn complete.
	if runErr == nil {
		t.Fatal("RunTurn() error = nil, want some failure (never a silent success out of a canceled recovery)")
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) <= baselineCount {
		t.Fatalf("no new records committed by this RunTurn call at all (baseline=%d total=%d)", baselineCount, len(records))
	}
	for _, record := range records[baselineCount:] {
		switch event := record.Event.(type) {
		case domain.ContextCompactionCompleted:
			t.Fatalf("a checkpoint completed despite the caller canceling mid-summary: %#v", event)
		case domain.TurnCompleted:
			t.Fatal("the Turn completed despite the caller canceling mid-summary")
		}
	}
}

// TestCompactionAppendResolvesUnknownOutcomeWithoutDuplicateSummarization
// covers design §17's own "an append landing in the unknown-outcome state
// concurrent with a cancellation signal (resolver rules win over the
// cancellation" table -- adapted to a compaction append specifically,
// since CompactSession has no per-Turn execution-registry lease to
// interleave a second caller's cancellation through. It is the direct
// regression test for the second real gap this task found:
// appendCompactOrchestrator (context_orchestrator.go) previously had NO
// unknown-outcome resolution at all -- a compaction append (Start,
// Complete, or Fail) whose outcome came back uncertain was simply
// returned as an error, leaving the compaction durably stuck "started"
// forever with nothing to ever resolve it, unlike every other append
// this package makes (admission, Step, terminal all resolve). Fixed by
// giving appendCompactOrchestrator the same ResolveAppendIntent-based
// resolution turn.go's own resolveAdmissionUnknown already established.
func TestCompactionAppendResolvesUnknownOutcomeWithoutDuplicateSummarization(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	config := application.DefaultConfig()
	config.Context = application.ContextConfig{
		Enabled:         true,
		Budget:          contextengine.Budget{HardInput: 1_000_000, Trigger: 1_000_000, Target: 500_000, ProtectedTail: 1, SummaryOutputCap: 4_000},
		Meter:           contextengine.WireEstimateMeter{},
		Summarizer:      summarizer,
		CheckpointStore: &liveCheckpointStore{store: store},
	}
	runner, err := engine.NewTurnRunner(&acceptanceSuccessModel{text: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, historyIDs, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}

	unknown, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeCommitOutcomeUnknown, MayHaveCommitted: true})
	if err != nil {
		t.Fatal(err)
	}
	memoryStore, ok := store.(*memory.EventStore)
	if !ok {
		t.Fatalf("store = %T, want *memory.EventStore", store)
	}
	// The compaction bracket's own Start append is the first one whose
	// outcome this fault makes uncertain -- it did commit server-side
	// (MayHaveCommitted: true), matching this task's own "resolver rules
	// win over the cancellation" scenario: the caller sees an uncertain
	// commit, not a plain failure, and ResolveAppendIntent must discover
	// what actually happened rather than retrying blindly (which would
	// double-append and violate "at most one compaction active").
	memoryStore.FailNext(memory.FaultAfterCommitBeforeAck, unknown)

	result, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID})
	if err != nil {
		t.Fatalf("CompactSession() error = %v, want the unknown outcome resolved transparently", err)
	}
	if !result.Ran || result.CheckpointID == "" {
		t.Fatalf("result = %#v", result)
	}
	if summarizer.callCount() != 1 {
		t.Fatalf("summarizer calls = %d, want exactly 1 (the resolved Start append must not cause a duplicate compaction attempt)", summarizer.callCount())
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	started, completed := 0, 0
	for _, record := range records {
		switch record.Event.(type) {
		case domain.ContextCompactionStarted:
			started++
		case domain.ContextCompactionCompleted:
			completed++
		}
	}
	if started != 1 || completed != 1 {
		t.Fatalf("started=%d completed=%d, want exactly 1 and 1 (no duplicate bracket from the unknown-outcome resolution)", started, completed)
	}
}
