package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestExecutionRegistryElectsOwnerAndCleansAfterDetach(t *testing.T) {
	registry := newExecutionRegistry()
	digest := Digest{1}
	owner, first, err := registry.acquire("request-1", "session-1", digest)
	if err != nil || !first {
		t.Fatalf("first acquire = %#v, %t, %v", owner, first, err)
	}
	waiter, second, err := registry.acquire("request-1", "session-1", digest)
	if err != nil || second || owner.entry != waiter.entry {
		t.Fatalf("second acquire split ownership: %#v, %t, %v", waiter, second, err)
	}
	if got := registry.unresolvedForSession("session-1"); got != 1 {
		t.Fatalf("unresolved = %d, want 1", got)
	}
	result := RunTurnResult{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Status: domain.TurnStatusCompleted, TerminalCommitted: true}
	owner.publish(result, nil)
	if got, err := waiter.wait(context.Background()); err != nil || got.TurnID != result.TurnID {
		t.Fatalf("waiter result = %#v, %v", got, err)
	}
	owner.release()
	if got := registry.unresolvedForSession("session-1"); got != 1 {
		t.Fatalf("owner release cleaned before waiter detached: %d", got)
	}
	waiter.release()
	if got := registry.unresolvedForSession("session-1"); got != 0 {
		t.Fatalf("unresolved after all detach = %d, want 0", got)
	}
}

func TestExecutionRegistryRejectsMismatchedIdentityAndWaiterCancellation(t *testing.T) {
	registry := newExecutionRegistry()
	owner, first, err := registry.acquire("request-1", "session-1", Digest{1})
	if err != nil || !first {
		t.Fatal(err)
	}
	if _, _, err := registry.acquire("request-1", "session-1", Digest{2}); !errors.Is(err, errExecutionIdentityMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	waiter, _, err := registry.acquire("request-1", "session-1", Digest{1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waiter.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v", err)
	}
	if owner.entry.terminal {
		t.Fatal("waiter cancellation canceled owner")
	}
	waiter.release()
	owner.publish(RunTurnResult{}, nil)
	owner.release()
}

func TestExecutionRegistryRetainsUnknownIntentAfterOwnerDetach(t *testing.T) {
	registry := newExecutionRegistry()
	owner, first, err := registry.acquire("request-unknown", "session-1", Digest{9})
	if err != nil || !first {
		t.Fatal(err)
	}
	intent := AppendIntent{Request: AppendRequestV2{AppendID: "append-1", SessionID: "session-1", CommandID: "command-1", Events: []ProposedEvent{{ID: "event-1", Event: domain.TurnStarted{TurnID: "turn-1", Input: "input"}}}}}
	if err := owner.retainIntent(intent); err != nil {
		t.Fatal(err)
	}
	if err := owner.retainUnknown(executionPhaseAdmissionUnknown); err != nil {
		t.Fatal(err)
	}
	owner.release()
	snapshot, ok := registry.snapshot("request-unknown")
	if !ok || snapshot.Phase != executionPhaseAdmissionUnknown || !snapshot.Retained || snapshot.Terminal || snapshot.Intent == nil || snapshot.Intent.Request.AppendID != intent.Request.AppendID || registry.unresolvedForSession("session-1") != 1 {
		t.Fatalf("unknown snapshot = %#v, present=%t unresolved=%d", snapshot, ok, registry.unresolvedForSession("session-1"))
	}
}

func TestExecutionRegistryRejectsWaiterAndReleasedOwnerMutation(t *testing.T) {
	registry := newExecutionRegistry()
	owner, _, err := registry.acquire("request-owner", "session-1", Digest{1})
	if err != nil {
		t.Fatal(err)
	}
	waiter, _, err := registry.acquire("request-owner", "session-1", Digest{1})
	if err != nil {
		t.Fatal(err)
	}
	if err := waiter.setPhase(executionPhaseRunning); err == nil {
		t.Fatal("waiter changed phase")
	}
	if err := waiter.publish(RunTurnResult{}, nil); err == nil {
		t.Fatal("waiter published")
	}
	owner.release()
	if err := owner.publish(RunTurnResult{}, nil); err == nil {
		t.Fatal("released owner published")
	}
	waiter.release()
}

func TestExecutionRegistryRejectsPhaseSkipsAndRegressions(t *testing.T) {
	registry := newExecutionRegistry()
	owner, _, err := registry.acquire("request-phase", "session-1", Digest{1})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.setPhase(executionPhaseTerminalInFlight); err == nil {
		t.Fatal("accepted phase skip")
	}
	if err := owner.setPhase(executionPhaseRunning); err != nil {
		t.Fatal(err)
	}
	if err := owner.setPhase(executionPhaseAdmissionInFlight); err == nil {
		t.Fatal("accepted phase regression")
	}
	if err := owner.setPhase(executionPhaseTerminalInFlight); err != nil {
		t.Fatal(err)
	}
	if err := owner.setPhase(executionPhaseTerminalUnknown); err == nil {
		t.Fatal("accepted unknown without retained intent")
	}
	owner.release()
}

func TestExecutionRegistryAllowsRetainedTerminalUnknownOnlyFromTerminalAppend(t *testing.T) {
	registry := newExecutionRegistry()
	owner, _, err := registry.acquire("request-terminal-unknown", "session-1", Digest{1})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.retainIntent(AppendIntent{Request: AppendRequestV2{AppendID: "append-1", SessionID: "session-1", CommandID: "command-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := owner.retainUnknown(executionPhaseTerminalUnknown); err == nil {
		t.Fatal("accepted terminal unknown before terminal append")
	}
	if err := owner.setPhase(executionPhaseRunning); err != nil {
		t.Fatal(err)
	}
	if err := owner.setPhase(executionPhaseTerminalInFlight); err != nil {
		t.Fatal(err)
	}
	if err := owner.retainUnknown(executionPhaseTerminalUnknown); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := registry.snapshot("request-terminal-unknown")
	if !ok || snapshot.Phase != executionPhaseTerminalUnknown || !snapshot.Retained {
		t.Fatalf("snapshot=%#v present=%t", snapshot, ok)
	}
	owner.release()
}

func TestExecutionRegistryThirtyTwoLeasesObserveOneLiveOwner(t *testing.T) {
	registry := newExecutionRegistry()
	start := make(chan struct{})
	type acquired struct {
		lease *executionLease
		owner bool
		err   error
	}
	acquiredCh := make(chan acquired, 32)
	for range 32 {
		go func() {
			<-start
			lease, owner, err := registry.acquire("request-many", "session-1", Digest{3})
			acquiredCh <- acquired{lease, owner, err}
		}()
	}
	close(start)
	var owner *executionLease
	leases := make([]*executionLease, 0, 32)
	for range 32 {
		select {
		case got := <-acquiredCh:
			if got.err != nil {
				t.Fatal(got.err)
			}
			leases = append(leases, got.lease)
			if got.owner {
				if owner != nil {
					t.Fatal("multiple owners")
				}
				owner = got.lease
			}
		case <-time.After(time.Second):
			t.Fatal("timed out acquiring leases")
		}
	}
	if owner == nil {
		t.Fatal("missing owner")
	}
	snapshot, ok := registry.snapshot("request-many")
	if !ok || snapshot.Leases != 32 {
		t.Fatalf("snapshot=%#v present=%t", snapshot, ok)
	}
	result := RunTurnResult{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Status: domain.TurnStatusCompleted, TerminalCommitted: true}
	if err := owner.publish(result, nil); err != nil {
		t.Fatal(err)
	}
	for _, lease := range leases {
		got, err := lease.wait(context.Background())
		if err != nil || got.SessionID != result.SessionID || got.TurnID != result.TurnID || got.ItemID != result.ItemID || got.Status != result.Status || !got.TerminalCommitted {
			t.Fatalf("result=%#v err=%v", got, err)
		}
		lease.release()
	}
	if registry.unresolvedForSession("session-1") != 0 {
		t.Fatal("entry leaked")
	}
}
