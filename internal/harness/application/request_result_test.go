package application

import (
	"errors"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestReconstructRequestResultRejectsWrongAdmissionPair(t *testing.T) {
	record := CommandRequestRecord{RunTurnRequestID: "request-1", RequestDigest: Digest{1}, SessionID: "session-1", CommandID: "command-1", TurnID: "turn-1", ItemID: "item-1", AdmissionAppendID: "append-1"}
	when := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	records := []domain.RecordedEvent{
		{SchemaVersion: 1, ID: "event-1", CommandID: "command-1", SessionID: "session-1", Sequence: 2, OccurredAt: when, Event: domain.TurnStarted{TurnID: "turn-1", Input: "input"}},
		{SchemaVersion: 1, ID: "event-2", CommandID: "command-1", SessionID: "session-1", Sequence: 3, OccurredAt: when, Event: domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-wrong"}},
	}
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
