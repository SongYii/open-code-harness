package application_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// newContextAwareService builds a Service with the Context Engine enabled
// (implementation plan Task 9 Step 2's opt-in Config.Context), a generous
// Budget so ordinary short test Turns never trigger compaction pressure
// (that machinery is already covered by context_orchestrator_test.go),
// and the given model as the sole conversation Provider.
func newContextAwareService(t *testing.T, store application.EventStore, ids application.IDGenerator, model engine.Model) *application.Service {
	t.Helper()
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	summarizer := &scriptedSummarizer{text: validSummaryText()}
	config := application.DefaultConfig()
	config.Context = application.ContextConfig{
		Enabled:         true,
		Budget:          contextengine.Budget{HardInput: 1_000_000, Trigger: 900_000, Target: 500_000, ProtectedTail: 100_000, SummaryOutputCap: 4_000},
		Meter:           contextengine.WireEstimateMeter{},
		Summarizer:      summarizer,
		CheckpointStore: &fakeCheckpointStore{},
	}
	service, err := application.NewService(store, ids, testkit.FixedClock{Time: acceptanceTime}, runner, application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}, config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestRunTurnWithContextEngineAdmissionBatchAndDispatchedEnvelopeMatch(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	store, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	ids := testkit.NewSequenceIDs()
	model := &acceptanceSuccessModel{text: "hello there"}
	service := newContextAwareService(t, store, ids, model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-1", Input: "hi", Sink: &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Status != domain.TurnStatusCompleted || result.Text != "hello there" {
		t.Fatalf("result = %#v", result)
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{
		domain.EventSessionCreated,
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventContextPreparedRecorded,
		domain.EventModelRequestRecorded,
		domain.EventAssistantMessageCompleted,
		domain.EventTurnCompleted,
	}
	if got := acceptanceEventTypes(records); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}

	var recordedRequest domain.ModelRequestRecorded
	var recordedPrepared domain.ContextPreparedRecorded
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.ModelRequestRecorded:
			recordedRequest = event
		case domain.ContextPreparedRecorded:
			recordedPrepared = event
		}
	}

	if recordedRequest.Purpose != string(engine.ModelRequestPurposeConversation) {
		t.Fatalf("Purpose = %q, want conversation", recordedRequest.Purpose)
	}
	if recordedRequest.AttemptIndex != 1 {
		t.Fatalf("AttemptIndex = %d, want 1", recordedRequest.AttemptIndex)
	}
	if recordedRequest.ContextDecisionID == "" || recordedRequest.ContextDecisionID != recordedPrepared.ContextDecisionID {
		t.Fatalf("ContextDecisionID mismatch: request=%q prepared=%q", recordedRequest.ContextDecisionID, recordedPrepared.ContextDecisionID)
	}
	if recordedPrepared.Trigger != domain.ContextTriggerPreTurn {
		t.Fatalf("Trigger = %q, want pre_turn", recordedPrepared.Trigger)
	}
	if recordedPrepared.MeterID != contextengine.WireEstimateMeterID {
		t.Fatalf("MeterID = %q, want %q", recordedPrepared.MeterID, contextengine.WireEstimateMeterID)
	}

	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("model received %d calls, want 1", len(calls))
	}
	// The Global Constraint this task's own plan requires: the recorded
	// request's Messages/Tools equal what engine.Model actually received,
	// byte for byte.
	if !reflect.DeepEqual(calls[0].Messages, recordedRequest.Messages) {
		t.Fatalf("dispatched Messages = %#v, want exactly the recorded Messages %#v", calls[0].Messages, recordedRequest.Messages)
	}
	if !reflect.DeepEqual(calls[0].Tools, recordedRequest.Tools) {
		t.Fatalf("dispatched Tools = %#v, want exactly the recorded Tools %#v", calls[0].Tools, recordedRequest.Tools)
	}
	wantMessages := []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: "hi"}}
	if !reflect.DeepEqual(recordedRequest.Messages, wantMessages) {
		t.Fatalf("recorded Messages = %#v, want %#v", recordedRequest.Messages, wantMessages)
	}
}

func TestRunTurnWithContextEngineSecondTurnCarriesPriorHistoryRegardlessOfCatalog(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	store, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	ids := testkit.NewSequenceIDs()
	model := &acceptanceSuccessModel{text: "second reply"}
	service := newContextAwareService(t, store, ids, model)
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-1", Input: "first", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatalf("first RunTurn() error = %v", err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "request-2", Input: "second", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatalf("second RunTurn() error = %v", err)
	}

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model received %d calls, want 2", len(calls))
	}
	// This is design defect #1's own closure: history is carried into the
	// second Turn's dispatched Messages exactly the same way whether or
	// not a Tool Catalog is configured (none is, here) -- no longer only
	// true when catalogEnabled() happens to be true, as loop.go's old
	// runSingleAttempt path required.
	wantSecondMessages := []domain.ModelPromptMessage{
		{Role: domain.PromptRoleUser, Text: "first"},
		{Role: domain.PromptRoleAssistant, Text: "second reply"},
		{Role: domain.PromptRoleUser, Text: "second"},
	}
	if !reflect.DeepEqual(calls[1].Messages, wantSecondMessages) {
		t.Fatalf("second call Messages = %#v, want %#v", calls[1].Messages, wantSecondMessages)
	}
}

// orderAssertingModel wraps acceptanceSuccessModel and fails the test the
// instant Stream is called unless the admission batch's context.prepared
// and model.request.recorded events are already durably committed --
// proving no Provider call happens before that append commits (this
// task's own plan-required mutation check target).
type orderAssertingModel struct {
	*acceptanceSuccessModel
	t         *testing.T
	store     application.EventStore
	sessionID domain.SessionID
}

func (model *orderAssertingModel) Stream(ctx context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	model.t.Helper()
	records, err := application.ReadWholeStreamPinned(ctx, model.store, model.sessionID, 256)
	if err != nil {
		model.t.Fatalf("orderAssertingModel: read stream: %v", err)
	}
	var sawPrepared, sawRequest bool
	for _, record := range records {
		switch record.Event.(type) {
		case domain.ContextPreparedRecorded:
			sawPrepared = true
		case domain.ModelRequestRecorded:
			sawRequest = true
		}
	}
	if !sawPrepared || !sawRequest {
		model.t.Fatalf("Model.Stream called before context.prepared/model.request.recorded committed (prepared=%t request=%t)", sawPrepared, sawRequest)
	}
	return model.acceptanceSuccessModel.Stream(ctx, request)
}

func TestRunTurnWithContextEngineNeverDispatchesBeforeAdmissionCommits(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1}
	store, err := memory.NewEventStore(authority)
	if err != nil {
		t.Fatal(err)
	}
	ids := testkit.NewSequenceIDs()
	base := &acceptanceSuccessModel{text: "ok"}
	// orderAssertingModel needs a real Session ID to read the stream
	// against, which does not exist until CreateSession runs -- so build
	// a throwaway service around the base model to create the Session
	// first, then run the actual Turn through a second service (same
	// store/ids) wired to the order-asserting wrapper.
	seed := newContextAwareService(t, store, ids, base)
	created, err := seed.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	model := &orderAssertingModel{acceptanceSuccessModel: base, t: t, store: store, sessionID: created.SessionID}
	checked := newContextAwareService(t, store, ids, model)

	result, err := checked.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("request-%d", 1)), Input: "hi", Sink: &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Status != domain.TurnStatusCompleted {
		t.Fatalf("result = %#v", result)
	}
}
