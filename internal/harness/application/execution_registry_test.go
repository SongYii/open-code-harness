package application

import (
	"context"
	"errors"
	"testing"

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
