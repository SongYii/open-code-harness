package application

import (
	"errors"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestReconstructRequestResultRejectsWrongAdmissionPair(t *testing.T) {
	record, records := validRequestView(t, nil, nil)
	records[2].Event = domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-wrong"}
	if _, err := ReconstructRequestResult(record, records); !IsStoreCode(err, StoreCodeCorrupt) {
		t.Fatalf("error = %v, want store corrupt", err)
	}
}

func TestDurableRequestTerminalErrorPreservesFailureAndInterruptionCode(t *testing.T) {
	for _, test := range []struct {
		name     string
		event    domain.Event
		category ErrorCategory
		code     string
	}{
		{name: "failed", event: domain.AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "model_stream"}, category: CategoryModel, code: "model_stream"},
		{name: "interrupted", event: domain.AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "caller_canceled"}, category: CategoryCanceled, code: "caller_canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := RunTurnResult{TerminalCommitted: true, Records: []domain.RecordedEvent{{Event: test.event}, {}}}
			var appErr *Error
			if err := durableRequestTerminalError(result); !errors.As(err, &appErr) || appErr.Category != test.category || appErr.Code != test.code || !appErr.TerminalCommitted {
				t.Fatalf("durable terminal error = %#v", err)
			}
		})
	}
}

func TestReconstructRequestResultRejectsAdversarialTerminalPairs(t *testing.T) {
	when := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	digest, err := DigestRunTurnRequestV1("session-1", "input")
	if err != nil {
		t.Fatal(err)
	}
	record := CommandRequestRecord{RunTurnRequestID: "request-1", RequestDigest: digest, SessionID: "session-1", CommandID: "command-1", TurnID: "turn-1", ItemID: "item-1", AdmissionAppendID: "append-1"}
	base := []domain.RecordedEvent{
		{SchemaVersion: 1, ID: "event-1", CommandID: "command-0", SessionID: "session-1", Sequence: 1, OccurredAt: when, Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}},
		{SchemaVersion: 1, ID: "event-2", CommandID: "command-1", SessionID: "session-1", Sequence: 2, OccurredAt: when, Event: domain.TurnStarted{TurnID: "turn-1", Input: "input"}},
		{SchemaVersion: 1, ID: "event-3", CommandID: "command-1", SessionID: "session-1", Sequence: 3, OccurredAt: when, Event: domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}},
	}
	for _, test := range []struct {
		name     string
		terminal domain.Event
		turn     domain.Event
	}{
		{name: "unknown failure code", terminal: domain.AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "leak", Message: "unsafe"}, turn: domain.TurnFailed{TurnID: "turn-1", Code: "leak", Message: "unsafe"}},
		{name: "failed message mismatch", terminal: domain.AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "model_stream", Message: "one"}, turn: domain.TurnFailed{TurnID: "turn-1", Code: "model_stream", Message: "two"}},
		{name: "interruption reason mismatch", terminal: domain.AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "caller_canceled"}, turn: domain.TurnInterrupted{TurnID: "turn-1", Reason: "delivery_failed"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			records := append([]domain.RecordedEvent(nil), base...)
			records = append(records, domain.RecordedEvent{SchemaVersion: 1, ID: "event-4", CommandID: "command-1", SessionID: "session-1", Sequence: 4, OccurredAt: when, Event: test.terminal}, domain.RecordedEvent{SchemaVersion: 1, ID: "event-5", CommandID: "command-1", SessionID: "session-1", Sequence: 5, OccurredAt: when, Event: test.turn})
			if _, err := ReconstructRequestResult(record, records); !IsStoreCode(err, StoreCodeCorrupt) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReconstructRequestResultAcceptsEachDurableLifecycle(t *testing.T) {
	for _, test := range []struct {
		name     string
		terminal domain.Event
		turn     domain.Event
		status   domain.TurnStatus
		text     string
	}{
		{name: "running", status: domain.TurnStatusRunning},
		{name: "completed", terminal: domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}, turn: domain.TurnCompleted{TurnID: "turn-1"}, status: domain.TurnStatusCompleted, text: "done"},
		{name: "failed", terminal: domain.AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "model_stream", Message: "failed"}, turn: domain.TurnFailed{TurnID: "turn-1", Code: "model_stream", Message: "failed"}, status: domain.TurnStatusFailed},
		{name: "interrupted", terminal: domain.AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "caller_canceled"}, turn: domain.TurnInterrupted{TurnID: "turn-1", Reason: "caller_canceled"}, status: domain.TurnStatusInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			record, records := validRequestView(t, test.terminal, test.turn)
			got, err := ReconstructRequestResult(record, records)
			if err != nil || got.Status != test.status || got.Text != test.text || got.TerminalCommitted != (test.status != domain.TurnStatusRunning) {
				t.Fatalf("result=%#v err=%v", got, err)
			}
		})
	}
}

func TestReconstructRequestResultRejectsEveryRelevantLifecycleCorruption(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]domain.RecordedEvent) []domain.RecordedEvent
	}{
		{name: "sequence gap", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent { r[2].Sequence++; return r }},
		{name: "codec corruption", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent { r[1].ID = " "; return r }},
		{name: "digest mismatch", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			r[1].Event = domain.TurnStarted{TurnID: "turn-1", Input: "other"}
			return r
		}},
		{name: "wrong command", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent { r[2].CommandID = "command-wrong"; return r }},
		{name: "wrong item", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			r[2].Event = domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-wrong"}
			return r
		}},
		{name: "dangling assistant terminal", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent { return r[:4] }},
		{name: "lone turn terminal", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			r[3].Event = domain.TurnCompleted{TurnID: "turn-1"}
			return r
		}},
		{name: "reversed terminal", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent { r[3], r[4] = r[4], r[3]; return r }},
		{name: "duplicate terminal", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			clone := r[3]
			clone.ID = "event-6"
			clone.Sequence = 6
			tail := r[4]
			tail.ID = "event-7"
			tail.Sequence = 7
			return append(r, clone, tail)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			record, records := validRequestView(t, domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}, domain.TurnCompleted{TurnID: "turn-1"})
			if _, err := ReconstructRequestResult(record, test.mutate(records)); !IsStoreCode(err, StoreCodeCorrupt) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func validRequestView(t *testing.T, terminal domain.Event, turnTerminal domain.Event) (CommandRequestRecord, []domain.RecordedEvent) {
	t.Helper()
	when := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	digest, err := DigestRunTurnRequestV1("session-1", "input")
	if err != nil {
		t.Fatal(err)
	}
	record := CommandRequestRecord{RunTurnRequestID: "request-1", RequestDigest: digest, SessionID: "session-1", CommandID: "command-1", TurnID: "turn-1", ItemID: "item-1", AdmissionAppendID: "append-1"}
	records := []domain.RecordedEvent{{SchemaVersion: 1, ID: "event-1", CommandID: "command-0", SessionID: "session-1", Sequence: 1, OccurredAt: when, Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}, {SchemaVersion: 1, ID: "event-2", CommandID: "command-1", SessionID: "session-1", Sequence: 2, OccurredAt: when, Event: domain.TurnStarted{TurnID: "turn-1", Input: "input"}}, {SchemaVersion: 1, ID: "event-3", CommandID: "command-1", SessionID: "session-1", Sequence: 3, OccurredAt: when, Event: domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}}}
	if terminal != nil {
		records = append(records, domain.RecordedEvent{SchemaVersion: 1, ID: "event-4", CommandID: "command-1", SessionID: "session-1", Sequence: 4, OccurredAt: when, Event: terminal}, domain.RecordedEvent{SchemaVersion: 1, ID: "event-5", CommandID: "command-1", SessionID: "session-1", Sequence: 5, OccurredAt: when, Event: turnTerminal})
	}
	return record, records
}
