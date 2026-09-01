package application_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// newManualCompactionService builds a Context.Enabled Service whose
// Budget is deliberately generous (well below any real Trigger pressure)
// so a test can isolate design §15.4's own "manual compaction attempts a
// cut even below Trigger, via Force: true" behavior from ordinary
// automatic-pressure compaction (already covered by
// context_orchestrator_test.go).
func newManualCompactionService(t *testing.T, store application.EventStore, ids application.IDGenerator, model engine.Model, summarizer application.ContextSummarizer, checkpointStore application.ContextCheckpointStore) *application.Service {
	t.Helper()
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	config := application.DefaultConfig()
	config.Context = application.ContextConfig{
		Enabled:         true,
		Budget:          contextengine.Budget{HardInput: 1_000_000, Trigger: 1_000_000, Target: 500_000, ProtectedTail: 1, SummaryOutputCap: 4_000},
		Meter:           contextengine.WireEstimateMeter{},
		Summarizer:      summarizer,
		CheckpointStore: checkpointStore,
	}
	service, err := application.NewService(store, ids, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// TestCompactSessionSummarizeTimeoutBoundsAHangingSummarizer proves
// ContextConfig.CompactionTimeout (design §21/§8) actually bounds a
// summarizer call: a summarizer that blocks forever without the caller's
// own context ever being canceled must still fail within
// CompactionTimeout, not hang indefinitely.
func TestCompactSessionSummarizeTimeoutBoundsAHangingSummarizer(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	summarizer := &blockingSummarizer{started: make(chan struct{}), release: make(chan struct{})}
	runner, err := engine.NewTurnRunner(&acceptanceSuccessModel{text: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	config := application.DefaultConfig()
	config.Context = application.ContextConfig{
		Enabled:           true,
		Budget:            contextengine.Budget{HardInput: 1_000_000, Trigger: 1_000_000, Target: 500_000, ProtectedTail: 1, SummaryOutputCap: 4_000},
		Meter:             contextengine.WireEstimateMeter{},
		Summarizer:        summarizer,
		CheckpointStore:   &fakeCheckpointStore{},
		CompactionTimeout: 50 * time.Millisecond,
	}
	service, err := application.NewService(store, historyIDs, testkit.FixedClock{Time: acceptanceTime}, runner,
		application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("CompactSession() with a hanging summarizer = nil error, want a timeout failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CompactSession() did not return within 5s; CompactionTimeout did not bound the summarizer call")
	}
}

func TestCompactSessionManualSummarySucceedsBelowTrigger(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	model := &acceptanceSuccessModel{text: "unused"}
	service := newManualCompactionService(t, store, historyIDs, model, summarizer, &fakeCheckpointStore{})

	result, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID})
	if err != nil {
		t.Fatalf("CompactSession() error = %v", err)
	}
	if !result.Ran || result.CheckpointKind != string(contextengine.CheckpointKindRollingSummary) || result.CheckpointID == "" {
		t.Fatalf("result = %#v", result)
	}
	if summarizer.callCount() != 1 {
		t.Fatalf("summarizer calls = %d, want 1", summarizer.callCount())
	}
	if calls := model.Calls(); len(calls) != 0 {
		t.Fatalf("conversation model received %d calls, want 0: manual compaction must never dispatch a conversation attempt", len(calls))
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var started domain.ContextCompactionStarted
	var completed domain.ContextCompactionCompleted
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.ContextCompactionStarted:
			started = event
		case domain.ContextCompactionCompleted:
			completed = event
		}
	}
	if started.Trigger != domain.ContextTriggerManual || started.Strategy != domain.ContextStrategySummary {
		t.Fatalf("started = %#v", started)
	}
	if completed.Checkpoint.ID != result.CheckpointID {
		t.Fatalf("completed checkpoint ID = %q, want %q", completed.Checkpoint.ID, result.CheckpointID)
	}
}

func TestCompactSessionManualResetSucceeds(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	service := newManualCompactionService(t, store, historyIDs, &acceptanceSuccessModel{}, summarizer, &fakeCheckpointStore{})

	result, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID, Strategy: domain.ContextStrategyReset})
	if err != nil {
		t.Fatalf("CompactSession() error = %v", err)
	}
	if !result.Ran || result.CheckpointKind != string(contextengine.CheckpointKindSourceTailReset) {
		t.Fatalf("result = %#v", result)
	}
	if summarizer.callCount() != 0 {
		t.Fatalf("summarizer calls = %d, want 0: a reset request must never call the summarizer", summarizer.callCount())
	}
}

// TestCompactSessionResetCheckpointDigestSurvivesIndependentVerification is
// the regression test for a real bug buildResetCheckpoint had until this
// task: its Coverage.SourceDigest was left at its seed value, never
// actually extended over the newly covered canonical records, even though
// ThroughSequence correctly advanced. Every prior reset test used
// fakeCheckpointStore (blind storage, no verification) or relied only on
// ValidateSuccessor (structural checks, never a canonical-content
// recomputation), so this was never caught until a genuinely verifying
// ContextCheckpointStore -- here the memory adapter's own independent
// full-rescan verification (adapters/memory/context_checkpoint.go) -- was
// used for a reset compaction for the first time.
func TestCompactSessionResetCheckpointDigestSurvivesIndependentVerification(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	checkpointStore, ok := store.(application.ContextCheckpointStore)
	if !ok {
		t.Fatal("buildHistorySession's store does not implement ContextCheckpointStore; this test needs a real, verifying store")
	}
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	service := newManualCompactionService(t, store, historyIDs, &acceptanceSuccessModel{}, summarizer, checkpointStore)

	result, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID, Strategy: domain.ContextStrategyReset})
	if err != nil {
		t.Fatalf("CompactSession() error = %v", err)
	}
	if !result.Ran || result.CheckpointKind != string(contextengine.CheckpointKindSourceTailReset) {
		t.Fatalf("result = %#v", result)
	}

	lookup, err := checkpointStore.LoadLatestContextCheckpoint(context.Background(), state.ID)
	if err != nil {
		t.Fatalf("LoadLatestContextCheckpoint independently re-verifying the reset checkpoint's digest: %v", err)
	}
	if lookup.Status != application.ContextCheckpointLookupFound || lookup.Checkpoint.ID != result.CheckpointID {
		t.Fatalf("lookup = %+v, want Found matching the reported checkpoint ID %q", lookup, result.CheckpointID)
	}
}

func TestCompactSessionFocusIsRenderedIntoTheSummarizerPrompt(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	service := newManualCompactionService(t, store, historyIDs, &acceptanceSuccessModel{}, summarizer, &fakeCheckpointStore{})

	focus := "prioritize the README changes"
	if _, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID, Focus: focus}); err != nil {
		t.Fatalf("CompactSession() error = %v", err)
	}
	if summarizer.callCount() != 1 {
		t.Fatalf("summarizer calls = %d, want 1", summarizer.callCount())
	}
	content := summarizer.calls[0].Content
	if !strings.Contains(content, "## MANUAL FOCUS") || !strings.Contains(content, focus) {
		t.Fatalf("summarizer content = %q, want a MANUAL FOCUS section containing %q", content, focus)
	}
}

func TestCompactSessionRejectedWhileATurnIsActive(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 1)
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	model := newBlockingAcceptanceModel("still running")
	service := newManualCompactionService(t, store, historyIDs, model, summarizer, &fakeCheckpointStore{})

	done := make(chan struct{})
	go func() {
		_, _ = service.RunTurn(context.Background(), application.RunTurnRequest{
			SessionID: state.ID, RequestID: "request-active-turn", Input: "keep going", Sink: &testkit.RecordingSink{},
		})
		close(done)
	}()
	await(t, model.started, "active turn to start streaming")

	_, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID})
	if err == nil {
		t.Fatal("CompactSession() error = nil, want rejection while a Turn is active")
	}

	model.releaseOnce()
	<-done
}

func TestCompactSessionNothingToCompactIsANoOpNotAnError(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	store, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	ids := testkit.NewSequenceIDs()
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	service := newManualCompactionService(t, store, ids, &acceptanceSuccessModel{}, summarizer, &fakeCheckpointStore{})
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	// A brand-new Session has nothing at all to cover.
	result, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: created.SessionID})
	if err != nil {
		t.Fatalf("CompactSession() error = %v, want a no-op result instead", err)
	}
	if result.Ran {
		t.Fatalf("result = %#v, want Ran=false", result)
	}
	if summarizer.callCount() != 0 {
		t.Fatalf("summarizer calls = %d, want 0", summarizer.callCount())
	}
}

func TestCompactSessionManualSummaryFailureReturnsItsOwnFailureWithoutResetFallback(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	summarizer := &scriptedSummarizer{err: fmt.Errorf("provider unavailable")}
	service := newManualCompactionService(t, store, historyIDs, &acceptanceSuccessModel{}, summarizer, &fakeCheckpointStore{})

	_, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID})
	if err == nil {
		t.Fatal("CompactSession() error = nil, want the summary failure to propagate directly")
	}
	if !application.IsCategory(err, application.CategoryModel) {
		t.Fatalf("CompactSession() error = %v, want CategoryModel", err)
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var sawFailed, sawCompleted, sawSecondStart bool
	startedCount := 0
	for _, record := range records {
		switch record.Event.(type) {
		case domain.ContextCompactionStarted:
			startedCount++
		case domain.ContextCompactionFailed:
			sawFailed = true
		case domain.ContextCompactionCompleted:
			sawCompleted = true
		}
	}
	sawSecondStart = startedCount > 1
	if !sawFailed || sawCompleted {
		t.Fatalf("sawFailed=%t sawCompleted=%t, want failed only", sawFailed, sawCompleted)
	}
	if sawSecondStart {
		t.Fatal("a second context.compaction.started was appended: manual mode must never fall through to a reset attempt")
	}
}

func TestCompactSessionValidatesRequestAndRequiresContextEngine(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 1)
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	service := newManualCompactionService(t, store, historyIDs, &acceptanceSuccessModel{}, summarizer, &fakeCheckpointStore{})

	if _, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID, Strategy: "not-a-real-strategy"}); !application.IsCategory(err, application.CategoryValidation) {
		t.Fatalf("invalid strategy error = %v, want CategoryValidation", err)
	}
	if _, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID, Focus: strings.Repeat("x", 4*1024+1)}); !application.IsCategory(err, application.CategoryValidation) {
		t.Fatalf("oversized focus error = %v, want CategoryValidation", err)
	}

	legacyStore := newTurnMemoryStore(t)
	legacyRunner, err := engine.NewTurnRunner(&acceptanceSuccessModel{})
	if err != nil {
		t.Fatal(err)
	}
	legacyService, err := application.NewService(legacyStore, testkit.NewSequenceIDs(), testkit.FixedClock{Time: acceptanceTime}, legacyRunner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, application.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyService.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: state.ID}); !application.IsCategory(err, application.CategoryValidation) {
		t.Fatalf("Context.Enabled:false error = %v, want CategoryValidation", err)
	}
}
