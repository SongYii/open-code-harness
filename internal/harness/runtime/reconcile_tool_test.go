package runtime

import (
	"context"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// Neither a missing policy decision nor an allowed decision proves whether a
// tool's external side effect happened. Recovery records interruption, never a
// made-up success/failure result, using the active tool's original CallID.
func TestReconcileInterruptedTool(t *testing.T) {
	for _, decided := range []bool{false, true} {
		name := "before_policy"
		if decided {
			name = "after_policy"
		}
		t.Run(name, func(t *testing.T) {
			store := openHostStore(t)
			seedCrashedAssistantItem(t, store)
			events := []application.ProposedEvent{
				proposed("event-tool-offer", domain.AssistantMessageCompleted{TurnID: "turn-crash", ItemID: "item-crash", Text: "calling", ToolCalls: []domain.ToolCallOffer{{ID: "original-call", Name: "write_file", Arguments: `{"path":"effect.txt","content":"x"}`}}}),
				proposed("event-tool-start", domain.ToolCallStarted{TurnID: "turn-crash", ItemID: "tool-item", CallID: "original-call", Name: "write_file", Arguments: `{"path":"effect.txt","content":"x"}`, StepIndex: 1}),
			}
			if decided {
				events = append(events, proposed("event-tool-policy", domain.PolicyDecisionRecorded{TurnID: "turn-crash", ItemID: "tool-item", CallID: "original-call", Name: "write_file", Effect: "allow", RuleID: "fixture", Reason: "fixture"}))
			}
			hostAppend(t, store, application.AppendRequest{AppendID: "append-tool-crash", SessionID: "session-crash", ExpectedVersion: 3, CommandID: "command-crash", Authority: hostAuthority(store), Events: events})
			before := readAllRuntime(t, store, "session-crash")
			if _, err := domain.Replay(before); err != nil {
				t.Fatalf("invalid seed: %v", err)
			}
			rec := &reconciler{store: store, authority: hostAuthority(store)}
			if appended, err := rec.reconcileSession(context.Background(), "session-crash"); err != nil || !appended {
				t.Fatalf("tool recovery: appended=%t err=%v", appended, err)
			}
			after := readAllRuntime(t, store, "session-crash")
			if len(after) != len(before)+2 || !reflect.DeepEqual(before, after[:len(before)]) {
				t.Fatal("recovery changed history or emitted wrong number of events")
			}
			interrupted, ok := after[len(before)].Event.(domain.ToolCallInterrupted)
			if !ok || interrupted.ItemID != "tool-item" || interrupted.CallID != "original-call" || interrupted.Code != processCrashCode {
				t.Fatalf("wrong tool terminal: %+v", after[len(before)].Event)
			}
			state, err := domain.Replay(after)
			if err != nil || state.ActiveTurn != nil {
				t.Fatalf("recovered stream invalid/active: %v", err)
			}
			if appended, err := rec.reconcileSession(context.Background(), "session-crash"); err != nil || appended {
				t.Fatalf("second recovery: appended=%t err=%v", appended, err)
			}
			if !reflect.DeepEqual(after, readAllRuntime(t, store, "session-crash")) {
				t.Fatal("repeated recovery changed bytes")
			}
		})
	}
}
