package runtime

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
)

// recordingStore delegates to a real store while capturing every append
// request it is asked to persist.
type recordingStore struct {
	application.EventStore
	mu       sync.Mutex
	requests []application.AppendRequest
}

func (s *recordingStore) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	s.record(request)
	return s.EventStore.Append(ctx, request)
}

func (s *recordingStore) record(request application.AppendRequest) {
	s.mu.Lock()
	s.requests = append(s.requests, request)
	s.mu.Unlock()
}

func (s *recordingStore) captured() []application.AppendRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]application.AppendRequest(nil), s.requests...)
}

// crashOnFirst records every append it observes, but fails the first one
// without touching durable state, simulating a crash between request
// construction and commit.
type crashOnFirst struct {
	*recordingStore
	mu     sync.Mutex
	failed bool
}

func (s *crashOnFirst) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	s.record(request)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.failed {
		s.failed = true
		return application.CommitReceipt{}, errors.New("injected crash before commit")
	}
	return s.recordingStore.EventStore.Append(ctx, request)
}

// loseAcknowledgement commits through to the real store but reports failure
// to the caller, simulating a crash between COMMIT and its acknowledgement.
type loseAcknowledgement struct {
	application.EventStore
}

func (s *loseAcknowledgement) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	receipt, err := s.EventStore.Append(ctx, request)
	if err == nil {
		return application.CommitReceipt{}, errors.New("injected lost acknowledgement")
	}
	return receipt, err
}

// The recovery facts must be a pure function of the replayed log: the
// recovery AppendID is deterministic by construction, so two attempts that
// observe the same stream must build byte-identical requests — timestamps
// included — or the exact-retry resolution degrades into
// AppendIdentityMismatch. Before the stream-derived stamp this test failed
// with two different wall-clock OccurredAt values.
func TestRecoveryAppendIsByteStableAcrossRestartAttempts(t *testing.T) {
	store := openHostStore(t)
	seedCrashedAssistantItem(t, store)
	recorder := &recordingStore{EventStore: store}
	wrapped := &crashOnFirst{recordingStore: recorder}

	first := &reconciler{store: wrapped, authority: hostAuthority(store)}
	appended, err := first.reconcileSession(context.Background(), "session-crash")
	if err == nil {
		t.Fatal("first attempt unexpectedly succeeded; injection did not fire")
	}
	if appended {
		t.Fatal("failed first attempt must not report an appended recovery")
	}

	second := &reconciler{store: wrapped, authority: hostAuthority(store)}
	appended, err = second.reconcileSession(context.Background(), "session-crash")
	if err != nil {
		t.Fatalf("retry after crashed attempt: %v", err)
	}
	if !appended {
		t.Fatal("retry must complete the pending recovery")
	}

	captured := recorder.captured()
	if len(captured) != 2 {
		t.Fatalf("captured %d append requests, want 2", len(captured))
	}
	if captured[0].AppendID != captured[1].AppendID {
		t.Fatalf("append IDs differ across restart attempts: %s vs %s", captured[0].AppendID, captured[1].AppendID)
	}
	if !reflect.DeepEqual(captured[0].Events, captured[1].Events) {
		t.Fatalf("recovery events are not byte-stable across restart attempts:\n first: %+v\nsecond: %+v",
			captured[0].Events, captured[1].Events)
	}
	digestA, err := application.DigestAppendRequest(captured[0])
	if err != nil {
		t.Fatal(err)
	}
	digestB, err := application.DigestAppendRequest(captured[1])
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestB {
		t.Fatal("request digests differ across restart attempts; exact-retry resolution would reject the retry")
	}
	for _, event := range captured[1].Events {
		if !event.OccurredAt.Equal(testTime) {
			t.Fatalf("recovery event stamped %v, want the stream-derived %v", event.OccurredAt, testTime)
		}
	}
}

// A lost acknowledgement after a successful commit must resolve through
// replay on the next launch: the recovery pair is already durable, so the
// second pass observes no running turn and appends nothing. This is the
// realistic restart path; byte-identity of a retried Append is covered by
// TestRecoveryAppendIsByteStableAcrossRestartAttempts.
func TestRecoveryLostAcknowledgementResolvesThroughReplay(t *testing.T) {
	store := openHostStore(t)
	seedCrashedAssistantItem(t, store)
	losing := &loseAcknowledgement{EventStore: store}

	first := &reconciler{store: losing, authority: hostAuthority(store)}
	if _, err := first.reconcileSession(context.Background(), "session-crash"); err == nil {
		t.Fatal("lost acknowledgement was not reported as a failure")
	}

	second := &reconciler{store: store, authority: hostAuthority(store)}
	appended, err := second.reconcileSession(context.Background(), "session-crash")
	if err != nil {
		t.Fatalf("reconciliation after a lost acknowledgement: %v", err)
	}
	if appended {
		t.Fatal("durable recovery pair was appended twice")
	}
	records := readAllRuntime(t, store, "session-crash")
	if len(records) != 5 {
		t.Fatalf("records = %d, want the 3 seeded plus one recovery pair", len(records))
	}
}
