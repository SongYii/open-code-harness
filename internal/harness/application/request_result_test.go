package application

import (
	"errors"
	"strconv"
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
		{name: "wrong turn", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			r[2].Event = domain.AssistantMessageStarted{TurnID: "turn-wrong", ItemID: "item-1"}
			return r
		}},
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
		{name: "duplicate admission", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			cloneStart, cloneItem := r[1], r[2]
			cloneStart.ID, cloneItem.ID = "event-6", "event-7"
			cloneStart.Sequence, cloneItem.Sequence = 6, 7
			return append(r, cloneStart, cloneItem)
		}},
		{name: "pre-admission terminal", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			terminal, turn, start, item := r[3], r[4], r[1], r[2]
			terminal.ID, turn.ID, start.ID, item.ID = "event-6", "event-7", "event-2", "event-3"
			terminal.Sequence, turn.Sequence, start.Sequence, item.Sequence = 2, 3, 4, 5
			return []domain.RecordedEvent{r[0], terminal, turn, start, item}
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

func TestReconstructRequestResultAcceptsExactRequestShapes(t *testing.T) {
	// CommandID always matches for events Application appended in this RunTurn.
	// Reconstruction therefore collects by CommandID, not by turn/item identity.
	for _, test := range []struct {
		name     string
		request  bool
		usage    bool
		terminal domain.Event
		turn     domain.Event
		status   domain.TurnStatus
		text     string
		wantLen  int
	}{
		{name: "running scripted", status: domain.TurnStatusRunning, wantLen: 2},
		{name: "running HTTP", request: true, status: domain.TurnStatusRunning, wantLen: 3},
		{name: "terminal scripted", terminal: domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}, turn: domain.TurnCompleted{TurnID: "turn-1"}, status: domain.TurnStatusCompleted, text: "done", wantLen: 4},
		{name: "terminal HTTP without usage", request: true, terminal: domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}, turn: domain.TurnCompleted{TurnID: "turn-1"}, status: domain.TurnStatusCompleted, text: "done", wantLen: 5},
		{name: "terminal HTTP with usage", request: true, usage: true, terminal: domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}, turn: domain.TurnCompleted{TurnID: "turn-1"}, status: domain.TurnStatusCompleted, text: "done", wantLen: 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			record, records := requestViewWithCompanions(t, test.request, test.usage, test.terminal, test.turn)
			got, err := ReconstructRequestResult(record, records)
			if err != nil || got.Status != test.status || got.Text != test.text || got.TerminalCommitted != (test.status != domain.TurnStatusRunning) || len(got.Records) != test.wantLen {
				t.Fatalf("result=%#v err=%v want len=%d status=%q", got, err, test.wantLen, test.status)
			}
		})
	}
}

func TestReconstructRequestResultRejectsMisplacedCompanions(t *testing.T) {
	// CommandID always matches for events Application appended in this RunTurn.
	for _, test := range []struct {
		name   string
		mutate func([]domain.RecordedEvent) []domain.RecordedEvent
	}{
		{name: "usage after terminal", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			usage := r[4]
			copy(r[4:], r[5:])
			r[len(r)-1] = usage
			r[4].Sequence, r[5].Sequence, r[6].Sequence = 5, 6, 7
			return r
		}},
		{name: "extra request", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			clone := r[3]
			clone.ID = "event-8"
			clone.Sequence = 8
			return append(r, clone)
		}},
		{name: "unknown same-CommandID type", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			return append(r, domain.RecordedEvent{SchemaVersion: 1, ID: "event-8", CommandID: "command-1", SessionID: "session-1", Sequence: 8, OccurredAt: r[0].OccurredAt, Event: domain.SessionClosed{}})
		}},
		{name: "mismatched request ids", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			event := r[3].Event.(domain.ModelRequestRecorded)
			event.ItemID = "item-other"
			r[3].Event = event
			return r
		}},
		{name: "usage on running request", mutate: func(r []domain.RecordedEvent) []domain.RecordedEvent {
			return r[:5]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			record, records := requestViewWithCompanions(t, true, true, domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}, domain.TurnCompleted{TurnID: "turn-1"})
			if _, err := ReconstructRequestResult(record, test.mutate(records)); !IsStoreCode(err, StoreCodeCorrupt) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func requestViewWithCompanions(t *testing.T, request, usage bool, terminal domain.Event, turnTerminal domain.Event) (CommandRequestRecord, []domain.RecordedEvent) {
	t.Helper()
	when := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	digest, err := DigestRunTurnRequestV1("session-1", "input")
	if err != nil {
		t.Fatal(err)
	}
	record := CommandRequestRecord{RunTurnRequestID: "request-1", RequestDigest: digest, SessionID: "session-1", CommandID: "command-1", TurnID: "turn-1", ItemID: "item-1", AdmissionAppendID: "append-1"}
	records := []domain.RecordedEvent{
		{SchemaVersion: 1, ID: "event-1", CommandID: "command-0", SessionID: "session-1", Sequence: 1, OccurredAt: when, Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}},
		{SchemaVersion: 1, ID: "event-2", CommandID: "command-1", SessionID: "session-1", Sequence: 2, OccurredAt: when, Event: domain.TurnStarted{TurnID: "turn-1", Input: "input"}},
		{SchemaVersion: 1, ID: "event-3", CommandID: "command-1", SessionID: "session-1", Sequence: 3, OccurredAt: when, Event: domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}},
	}
	nextID := 4
	if request {
		records = append(records, domain.RecordedEvent{SchemaVersion: 1, ID: domain.EventID("event-" + strconv.Itoa(nextID)), CommandID: "command-1", SessionID: "session-1", Sequence: uint64(nextID), OccurredAt: when, Event: domain.ModelRequestRecorded{
			TurnID: "turn-1", ItemID: "item-1", AdapterFamily: "openai_compat", ModelID: "test-model", EndpointID: "api.example.com",
			NativeTools: "unsupported", Images: "unsupported", StructuredOutput: "unsupported", ReasoningFields: "unsupported", PromptCache: "unsupported",
			IncludeUsage: true, Messages: []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: "input"}},
		}})
		nextID++
	}
	if usage {
		records = append(records, domain.RecordedEvent{SchemaVersion: 1, ID: domain.EventID("event-" + strconv.Itoa(nextID)), CommandID: "command-1", SessionID: "session-1", Sequence: uint64(nextID), OccurredAt: when, Event: domain.ModelUsageRecorded{
			TurnID: "turn-1", ItemID: "item-1", InputTokens: 1, OutputTokens: 2, FinishReason: domain.FinishReasonStop,
		}})
		nextID++
	}
	if terminal != nil {
		records = append(records,
			domain.RecordedEvent{SchemaVersion: 1, ID: domain.EventID("event-" + strconv.Itoa(nextID)), CommandID: "command-1", SessionID: "session-1", Sequence: uint64(nextID), OccurredAt: when, Event: terminal},
			domain.RecordedEvent{SchemaVersion: 1, ID: domain.EventID("event-" + strconv.Itoa(nextID+1)), CommandID: "command-1", SessionID: "session-1", Sequence: uint64(nextID + 1), OccurredAt: when, Event: turnTerminal},
		)
	}
	return record, records
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
