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

func validContextCompactionStarted(id domain.ContextCompactionID, trigger string) domain.ContextCompactionStarted {
	return domain.ContextCompactionStarted{
		ID: id, Trigger: trigger, Strategy: domain.ContextStrategySummary,
		MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1",
	}
}

// TestReconcileClosesDanglingCompactionWithoutActiveTurn covers a manual or
// pre-turn compaction that crashed with no active Turn (design §14.4) --
// session_heads status stays idle throughout, so this session is only ever
// discovered as a candidate through SessionsWithActiveCompaction, not
// ActiveSessions.
func TestReconcileClosesDanglingCompactionWithoutActiveTurn(t *testing.T) {
	store := openHostStore(t)
	hostAppend(t, store, application.AppendRequest{
		AppendID: "append-manual-compaction", SessionID: "session-manual-compaction", ExpectedVersion: 0,
		CommandID: "command-manual-compaction", Authority: hostAuthority(store),
		Events: []application.ProposedEvent{
			proposed("event-mc-1", domain.SessionCreated{WorkspaceRoot: "/w"}),
			proposed("event-mc-2", domain.TurnStarted{TurnID: "turn-mc", Input: "hi"}),
			proposed("event-mc-3", domain.TurnCompleted{TurnID: "turn-mc"}),
			proposed("event-mc-4", validContextCompactionStarted("compaction-mc", domain.ContextTriggerManual)),
		},
	})

	rec := &reconciler{store: store, authority: hostAuthority(store)}
	appended, err := rec.reconcileSession(context.Background(), "session-manual-compaction")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !appended {
		t.Fatal("dangling compaction was not closed")
	}
	records := readAllRuntime(t, store, "session-manual-compaction")
	if len(records) != 5 {
		t.Fatalf("records = %d, want 5", len(records))
	}
	failed, ok := records[4].Event.(domain.ContextCompactionFailed)
	if !ok {
		t.Fatalf("fifth record = %T, want ContextCompactionFailed", records[4].Event)
	}
	if failed.ID != "compaction-mc" || failed.Code != runtimeRecoveredCode {
		t.Fatalf("compaction failure = %+v, want ID compaction-mc code %s", failed, runtimeRecoveredCode)
	}

	// Confirm the session is no longer permanently blocked: the Task 12
	// eligibility guard (rejecting a new Turn while a compaction is
	// active) rejects until this recovery clears ContextCompaction.
	state, err := domain.Replay(records)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if state.ContextCompaction != nil {
		t.Fatal("compaction still active after recovery")
	}
}

func TestReconcileCompactionOnlyRecoveryIsIdempotent(t *testing.T) {
	store := openHostStore(t)
	hostAppend(t, store, application.AppendRequest{
		AppendID: "append-manual-compaction-idem", SessionID: "session-manual-compaction-idem", ExpectedVersion: 0,
		CommandID: "command-manual-compaction-idem", Authority: hostAuthority(store),
		Events: []application.ProposedEvent{
			proposed("event-mci-1", domain.SessionCreated{WorkspaceRoot: "/w"}),
			proposed("event-mci-2", validContextCompactionStarted("compaction-mci", domain.ContextTriggerManual)),
		},
	})
	rec := &reconciler{store: store, authority: hostAuthority(store)}
	if _, err := rec.reconcileSession(context.Background(), "session-manual-compaction-idem"); err != nil {
		t.Fatal(err)
	}
	appended, err := rec.reconcileSession(context.Background(), "session-manual-compaction-idem")
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if appended {
		t.Fatal("second reconciliation appended again; duplicate compaction failure")
	}
	if got := len(readAllRuntime(t, store, "session-manual-compaction-idem")); got != 3 {
		t.Fatalf("records after second reconcile = %d, want 3", got)
	}
}

// TestReconcileClosesCompactionBeforeTerminalizingEnclosingTurn covers a
// mid-turn or overflow-retry compaction, active inside a running Turn, that
// crashed. Design §14.4 requires the compaction close before the Turn's own
// terminal events -- proven here by exact event ordering, not merely that
// replay succeeds (Apply itself does not gate TurnInterrupted on
// ContextCompaction state, so an ordering bug would not fail replay).
func TestReconcileClosesCompactionBeforeTerminalizingEnclosingTurn(t *testing.T) {
	store := openHostStore(t)
	hostAppend(t, store, application.AppendRequest{
		AppendID: "append-midturn-compaction", SessionID: "session-midturn-compaction", ExpectedVersion: 0,
		CommandID: "command-midturn-compaction", Authority: hostAuthority(store),
		Events: []application.ProposedEvent{
			proposed("event-mt-1", domain.SessionCreated{WorkspaceRoot: "/w"}),
			proposed("event-mt-2", domain.TurnStarted{TurnID: "turn-mt", Input: "hi"}),
			proposed("event-mt-3", domain.AssistantMessageStarted{TurnID: "turn-mt", ItemID: "item-mt"}),
			proposed("event-mt-4", validContextCompactionStarted("compaction-mt", domain.ContextTriggerMidTurn)),
		},
	})

	rec := &reconciler{store: store, authority: hostAuthority(store)}
	appended, err := rec.reconcileSession(context.Background(), "session-midturn-compaction")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !appended {
		t.Fatal("nothing was reconciled")
	}
	records := readAllRuntime(t, store, "session-midturn-compaction")
	if len(records) != 7 {
		t.Fatalf("records = %d, want 7", len(records))
	}
	failed, ok := records[4].Event.(domain.ContextCompactionFailed)
	if !ok || failed.ID != "compaction-mt" {
		t.Fatalf("fifth record = %+v, want ContextCompactionFailed(compaction-mt)", records[4].Event)
	}
	item, ok := records[5].Event.(domain.AssistantMessageInterrupted)
	if !ok || item.ItemID != "item-mt" {
		t.Fatalf("sixth record = %+v, want AssistantMessageInterrupted(item-mt)", records[5].Event)
	}
	turn, ok := records[6].Event.(domain.TurnInterrupted)
	if !ok || turn.TurnID != "turn-mt" {
		t.Fatalf("seventh record = %+v, want TurnInterrupted(turn-mt)", records[6].Event)
	}
	if _, err := domain.Replay(records); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestRecoveryCompactionAppendIDDeterministic(t *testing.T) {
	first := recoveryCompactionAppendID("session-a", "compaction-a")
	if first != recoveryCompactionAppendID("session-a", "compaction-a") {
		t.Fatal("recovery compaction AppendID is not deterministic")
	}
	if first == recoveryCompactionAppendID("session-b", "compaction-a") {
		t.Fatal("session does not change the AppendID")
	}
	if first == recoveryCompactionAppendID("session-a", "compaction-b") {
		t.Fatal("compaction ID does not change the AppendID")
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
