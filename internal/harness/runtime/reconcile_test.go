package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

var testTime = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

func openHostStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), sqlite.Config{
		Path:      filepath.Join(t.TempDir(), "host.db"),
		RuntimeID: "runtime-host-test",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func hostAppend(t *testing.T, store *sqlite.Store, request application.AppendRequest) application.CommitReceipt {
	t.Helper()
	receipt, err := store.Append(context.Background(), request)
	if err != nil {
		t.Fatalf("append %s: %v", request.AppendID, err)
	}
	return receipt
}

func proposed(id string, event domain.Event) application.ProposedEvent {
	return application.ProposedEvent{ID: domain.EventID(id), SchemaVersion: 1, OccurredAt: testTime, Event: event}
}

func hostAuthority(store *sqlite.Store) application.WriterAuthority {
	return store.Authority()
}

func seedCrashedAssistantItem(t *testing.T, store *sqlite.Store) {
	t.Helper()
	base := application.AppendRequest{
		AppendID: "append-crash-1", SessionID: "session-crash", ExpectedVersion: 0,
		CommandID: "command-crash", Authority: hostAuthority(store),
		Events: []application.ProposedEvent{
			proposed("event-crash-1", domain.SessionCreated{WorkspaceRoot: "/w"}),
			proposed("event-crash-2", domain.TurnStarted{TurnID: "turn-crash", Input: "hi"}),
			proposed("event-crash-3", domain.AssistantMessageStarted{TurnID: "turn-crash", ItemID: "item-crash"}),
		},
	}
	hostAppend(t, store, base)
}

func readAllRuntime(t *testing.T, store *sqlite.Store, session domain.SessionID) []domain.RecordedEvent {
	t.Helper()
	page, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: session, Limit: 256})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return page.Records
}

func TestReconcileClosesInterruptedAssistantItem(t *testing.T) {
	store := openHostStore(t)
	seedCrashedAssistantItem(t, store)

	rec := &reconciler{store: store, authority: hostAuthority(store)}
	appended, err := rec.reconcileSession(context.Background(), "session-crash")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !appended {
		t.Fatal("reconciliation appended nothing")
	}
	records := readAllRuntime(t, store, "session-crash")
	if len(records) != 5 {
		t.Fatalf("records = %d, want 5", len(records))
	}
	item := records[3].Event.(domain.AssistantMessageInterrupted)
	if item.Code != processCrashCode || item.ItemID != "item-crash" || item.TurnID != "turn-crash" {
		t.Fatalf("item interruption = %+v", item)
	}
	turn := records[4].Event.(domain.TurnInterrupted)
	if turn.Reason != processCrashCode || turn.TurnID != "turn-crash" {
		t.Fatalf("turn interruption = %+v", turn)
	}
	if records[4].CommandID != "command-crash" {
		t.Fatalf("lineage command = %s, want the original command-crash", records[4].CommandID)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	store := openHostStore(t)
	seedCrashedAssistantItem(t, store)
	rec := &reconciler{store: store, authority: hostAuthority(store)}
	if _, err := rec.reconcileSession(context.Background(), "session-crash"); err != nil {
		t.Fatal(err)
	}
	appended, err := rec.reconcileSession(context.Background(), "session-crash")
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if appended {
		t.Fatal("second reconciliation appended again; duplicate terminal pair")
	}
	if got := len(readAllRuntime(t, store, "session-crash")); got != 5 {
		t.Fatalf("records after second reconcile = %d, want 5", got)
	}
}

func TestRecoveryAppendIDDeterministic(t *testing.T) {
	first := recoveryAppendID("session-a", "turn-a", "item-a")
	if first != recoveryAppendID("session-a", "turn-a", "item-a") {
		t.Fatal("recovery AppendID is not deterministic")
	}
	if first == recoveryAppendID("session-a", "turn-a", noItemSentinel) {
		t.Fatal("no-item sentinel does not change the AppendID")
	}
	if first == recoveryAppendID("session-b", "turn-a", "item-a") {
		t.Fatal("session does not change the AppendID")
	}
}

func TestReconcileLegacyTurnWithoutItem(t *testing.T) {
	store := openHostStore(t)
	hostAppend(t, store, application.AppendRequest{
		AppendID: "append-legacy", SessionID: "session-legacy", ExpectedVersion: 0,
		CommandID: "command-legacy", Authority: hostAuthority(store),
		Events: []application.ProposedEvent{
			proposed("event-legacy-1", domain.SessionCreated{WorkspaceRoot: "/w"}),
			proposed("event-legacy-2", domain.TurnStarted{TurnID: "turn-legacy", Input: "hi"}),
		},
	})
	rec := &reconciler{store: store, authority: hostAuthority(store)}
	appended, err := rec.reconcileSession(context.Background(), "session-legacy")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !appended {
		t.Fatal("legacy turn was not closed")
	}
	records := readAllRuntime(t, store, "session-legacy")
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	if _, isItem := records[2].Event.(domain.TurnInterrupted); !isItem {
		t.Fatalf("third record = %T, want TurnInterrupted only", records[2].Event)
	}
}

func TestReconcileCleanStreamIsNoop(t *testing.T) {
	store := openHostStore(t)
	hostAppend(t, store, application.AppendRequest{
		AppendID: "append-clean", SessionID: "session-clean", ExpectedVersion: 0,
		CommandID: "command-clean", Authority: hostAuthority(store),
		Events: []application.ProposedEvent{
			proposed("event-clean-1", domain.SessionCreated{WorkspaceRoot: "/w"}),
			proposed("event-clean-2", domain.TurnStarted{TurnID: "turn-clean", Input: "hi"}),
			proposed("event-clean-3", domain.TurnCompleted{TurnID: "turn-clean"}),
		},
	})
	rec := &reconciler{store: store, authority: hostAuthority(store)}
	appended, err := rec.reconcileSession(context.Background(), "session-clean")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if appended {
		t.Fatal("clean stream was mutated")
	}
}

func TestActiveSessionsEnumeratesOnlyActive(t *testing.T) {
	store := openHostStore(t)
	seedCrashedAssistantItem(t, store) // leaves turn running → active
	hostAppend(t, store, application.AppendRequest{
		AppendID: "append-idle", SessionID: "session-idle", ExpectedVersion: 0,
		CommandID: "command-idle", Authority: hostAuthority(store),
		Events: []application.ProposedEvent{
			proposed("event-idle-1", domain.SessionCreated{WorkspaceRoot: "/w"}),
		},
	})
	sessions, err := store.ActiveSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0] != "session-crash" {
		t.Fatalf("active sessions = %v, want [session-crash]", sessions)
	}
}
