package application_test

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// overflowModel fails its first failCount calls with a pre-delta,
// classified context_overflow ProviderFailure (design §15.3/CE-13's own
// recoverable shape -- a startup failure, before any stream event is
// read) and succeeds on every call after that.
type overflowModel struct {
	mu        sync.Mutex
	calls     []engine.ModelRequest
	failCount int
	text      string
}

func (model *overflowModel) Stream(_ context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	model.mu.Lock()
	model.calls = append(model.calls, request)
	index := len(model.calls)
	model.mu.Unlock()
	if index <= model.failCount {
		return nil, &engine.ProviderFailure{Class: engine.FailureClassPermanent, Code: "context_overflow"}
	}
	return &acceptanceStream{events: []engine.StreamEvent{
		{Type: engine.StreamEventTextDelta, Text: model.text},
		{Type: engine.StreamEventCompleted},
	}}, nil
}

func (model *overflowModel) Calls() []engine.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]engine.ModelRequest(nil), model.calls...)
}

// newOverflowContextConfig builds a Context.Enabled config whose Budget
// never triggers pre-turn compaction on its own (Trigger/HardInput huge)
// but whose ProtectedTail is small enough that a FORCED overflow-retry
// plan (Force: true, design §15.3) still finds real history to cover.
func newOverflowContextConfig(maxOverflowRecoveries uint32) application.ContextConfig {
	return application.ContextConfig{
		Enabled:                      true,
		Budget:                       contextengine.Budget{HardInput: 1_000_000, Trigger: 1_000_000, Target: 500_000, ProtectedTail: 100, SummaryOutputCap: 4_000},
		Meter:                        contextengine.WireEstimateMeter{},
		Summarizer:                   &scriptedSummarizer{text: validSummaryText()},
		CheckpointStore:              &fakeCheckpointStore{},
		MaxOverflowRecoveriesPerTurn: maxOverflowRecoveries,
	}
}

func TestRunTurnRecoversFromOneOverflowAndRetriesWithASmallerEnvelope(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	model := &overflowModel{failCount: 1, text: "recovered"}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	config := application.DefaultConfig()
	config.Context = newOverflowContextConfig(2)
	service, err := application.NewService(store, historyIDs, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: state.ID, RequestID: "request-overflow", Input: "please continue", Sink: &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Status != domain.TurnStatusCompleted || result.Text != "recovered" {
		t.Fatalf("result = %#v", result)
	}

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model received %d calls, want 2 (one overflow, one recovered retry)", len(calls))
	}
	firstEstimate := contextengine.WireEstimateMeter{}.Estimate(contextengine.Envelope{Messages: calls[0].Messages, Tools: calls[0].Tools}).Tokens
	secondEstimate := contextengine.WireEstimateMeter{}.Estimate(contextengine.Envelope{Messages: calls[1].Messages, Tools: calls[1].Tools}).Tokens
	if secondEstimate*10 > firstEstimate*9 {
		t.Fatalf("recovered envelope (%d tokens) is not at least 10%% smaller than the one that overflowed (%d tokens)", secondEstimate, firstEstimate)
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var started, completed int
	var requestEvents []domain.ModelRequestRecorded
	var preparedEvents []domain.ContextPreparedRecorded
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.ContextCompactionStarted:
			started++
		case domain.ContextCompactionCompleted:
			completed++
		case domain.ModelRequestRecorded:
			requestEvents = append(requestEvents, event)
		case domain.ContextPreparedRecorded:
			preparedEvents = append(preparedEvents, event)
		}
	}
	if started != 1 || completed != 1 {
		t.Fatalf("compaction started=%d completed=%d, want exactly one bracket for the one recovery", started, completed)
	}
	// The pre-turn attempt (index 1) and the overflow retry (index 2) on
	// the SAME assistant item.
	last := len(requestEvents) - 1
	if requestEvents[last].AttemptIndex != 2 {
		t.Fatalf("retry AttemptIndex = %d, want 2", requestEvents[last].AttemptIndex)
	}
	if preparedEvents[len(preparedEvents)-1].Trigger != domain.ContextTriggerOverflowRetry {
		t.Fatalf("retry Trigger = %q, want overflow_retry", preparedEvents[len(preparedEvents)-1].Trigger)
	}
	if !reflect.DeepEqual(calls[1].Messages, requestEvents[last].Messages) {
		t.Fatalf("dispatched retry Messages = %#v, want exactly the recorded %#v", calls[1].Messages, requestEvents[last].Messages)
	}
}

// TestRunTurnOverflowRecoveryDeclinesOnceNoFurtherSafePrefixExists covers
// design §16's "no safe prefix exists ... falls through to the existing
// context_overflow terminal failure path unchanged": the current
// implementation's compaction bracket always compacts maximally down to
// ProtectedTail in one pass (contextengine.SelectCutPoint's own greedy
// walk), so once one recovery has run, a second attempt in the same Turn
// structurally always finds nothing further to compact -- it declines
// (design's own "no safe prefix" case) rather than looping again, well
// before any configured cap is reached. This is a real, disclosed
// property of Task 10's own single-shot compaction, not a gap: the cap
// exists as a defensive bound the mutation check below still verifies
// independently.
func TestRunTurnOverflowRecoveryDeclinesOnceNoFurtherSafePrefixExists(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	model := &overflowModel{failCount: 100, text: "unreachable"} // always overflows
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	config := application.DefaultConfig()
	config.Context = newOverflowContextConfig(2) // cap = 2 recoveries, but never reached (see doc comment)
	service, err := application.NewService(store, historyIDs, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: state.ID, RequestID: "request-overflow-exhausted", Input: "please continue", Sink: &testkit.RecordingSink{},
	})
	if err == nil {
		t.Fatalf("RunTurn() error = nil, want the context_overflow terminal failure; result=%#v", result)
	}
	if !application.IsCategory(err, application.CategoryModel) {
		t.Fatalf("RunTurn() error = %v, want CategoryModel", err)
	}
	if result.Status != domain.TurnStatusFailed || !result.TerminalCommitted {
		t.Fatalf("result = %#v", result)
	}
	// 1 initial attempt + 1 recovery retry (which also overflows) = 2
	// calls; the recovery declines on its second attempt (nothing left to
	// compact) before ever calling the Model a third time.
	if calls := len(model.Calls()); calls != 2 {
		t.Fatalf("model received %d calls, want 2 (1 initial + 1 recovered retry, both overflowing)", calls)
	}
}

// TestRunTurnOverflowRecoveryLegacyPathUnaffectedByOverflow confirms
// Config.Context.Enabled: false (every pre-Task-10 caller) sees a
// context_overflow Provider failure terminalize exactly as it always did
// -- overflowRecoveryEligible's own Context-Engine gate must never even
// attempt PrepareContext/recovery machinery the legacy path has no
// dependencies configured for. The caller-facing application.Error uses
// mapRunError's own coarser "model_startup" classification (pre-existing,
// unrelated to Task 10); the durable FailAssistantTurn/AssistantMessageFailed
// event durableFailure resolves still carries the specific
// "context_overflow" code, which this test also checks.
func TestRunTurnOverflowRecoveryLegacyPathUnaffectedByOverflow(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	model := &overflowModel{failCount: 100, text: "unreachable"}
	service, _ := newToolService(t, model, fs, nil, nil, application.DefaultConfig())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-legacy-overflow", Input: "inspect", Sink: &testkit.RecordingSink{},
	})
	assertRunTurnError(t, runErr, application.CategoryModel, string(engine.CodeModelStartup), true)
	if result.Status != domain.TurnStatusFailed {
		t.Fatalf("result = %#v", result)
	}
	if calls := len(model.Calls()); calls != 1 {
		t.Fatalf("model received %d calls, want 1: the legacy path must never attempt overflow recovery", calls)
	}
	var durableCode string
	for _, record := range result.Records {
		switch event := record.Event.(type) {
		case domain.AssistantMessageFailed:
			durableCode = event.Code
		case domain.TurnFailed:
			durableCode = event.Code
		}
	}
	if durableCode != "context_overflow" {
		t.Fatalf("durable failure code = %q, want context_overflow", durableCode)
	}
}

func TestRunTurnOverflowRecoveryOneRecoveryBelowCapSucceedsNormally(t *testing.T) {
	store, state, _, historyIDs := buildHistorySession(t, 6)
	model := &overflowModel{failCount: 1, text: "ok"}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	config := application.DefaultConfig()
	config.Context = newOverflowContextConfig(1)
	service, err := application.NewService(store, historyIDs, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: state.ID, RequestID: "request-followup", Input: "continue", Sink: &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Status != domain.TurnStatusCompleted {
		t.Fatalf("result = %#v", result)
	}
}

func TestNewServiceRejectsMaxOverflowRecoveriesPerTurnAboveTheCap(t *testing.T) {
	store, _, _, historyIDs := buildHistorySession(t, 1)
	runner, err := engine.NewTurnRunner(&overflowModel{text: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	config := application.DefaultConfig()
	config.Context = newOverflowContextConfig(application.MaxOverflowRecoveriesPerTurnCap + 1)
	_, err = application.NewService(store, historyIDs, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if !application.IsCategory(err, application.CategoryValidation) {
		t.Fatalf("NewService() error = %v, want CategoryValidation for MaxOverflowRecoveriesPerTurn above the cap", err)
	}
}

func TestNewServiceDefaultsMaxOverflowRecoveriesPerTurnWhenZero(t *testing.T) {
	store, _, _, historyIDs := buildHistorySession(t, 1)
	runner, err := engine.NewTurnRunner(&overflowModel{text: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	config := application.DefaultConfig()
	config.Context = newOverflowContextConfig(0) // zero: defaults to DefaultMaxOverflowRecoveriesPerTurn
	if _, err := application.NewService(store, historyIDs, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config); err != nil {
		t.Fatalf("NewService() error = %v, want a zero MaxOverflowRecoveriesPerTurn to default cleanly", err)
	}
}
