package application

import (
	"errors"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestReconstructContextPreparedRequest(t *testing.T) {
	prepared := domain.ContextPreparedRecorded{TurnID: "turn-1", ItemID: "item-1", AttemptIndex: 1, ContextDecisionID: "decision-1", Trigger: domain.ContextTriggerPreTurn, MeterID: "fixture"}
	request := modelRequestRecordedForTest("item-1", []domain.ModelPromptMessage{{Role: "user", Text: "input"}})
	request.ContextDecisionID, request.AttemptIndex = prepared.ContextDecisionID, 1
	start := []domain.Event{domain.TurnStarted{TurnID: "turn-1", Input: "input"}, domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}}
	for _, interrupted := range []bool{false, true} {
		name := "completed"
		if interrupted {
			name = "process_crash"
		}
		t.Run(name, func(t *testing.T) {
			events := append(append([]domain.Event{}, start...), prepared, request)
			want := domain.TurnStatusCompleted
			if interrupted {
				want = domain.TurnStatusInterrupted
				events = append(events, domain.AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "process_crash"}, domain.TurnInterrupted{TurnID: "turn-1", Reason: "process_crash"})
			} else {
				events = append(events, domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}, domain.TurnCompleted{TurnID: "turn-1"})
			}
			record, records := requestViewFromEvents(t, events)
			result, err := ReconstructRequestResult(record, records)
			if err != nil || result.Status != want || !result.TerminalCommitted {
				t.Fatalf("context request reconstruction: status=%s err=%v cause=%v", result.Status, err, errors.Unwrap(err))
			}
		})
	}
	for _, test := range []struct {
		name       string
		companions []domain.Event
	}{
		{"duplicate", []domain.Event{prepared, prepared, request}},
		{"after_request", []domain.Event{request, prepared}},
		{"wrong_item", []domain.Event{func() domain.Event { e := prepared; e.ItemID = "foreign-item"; return e }(), request}},
		{"wrong_turn", []domain.Event{func() domain.Event { e := prepared; e.TurnID = "foreign-turn"; return e }(), request}},
		{"wrong_decision", []domain.Event{prepared, func() domain.Event { e := request; e.ContextDecisionID = "foreign-decision"; return e }()}},
		{"wrong_attempt", []domain.Event{prepared, func() domain.Event { e := request; e.AttemptIndex = 2; return e }()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := append(append([]domain.Event{}, start...), test.companions...)
			record, records := requestViewFromEvents(t, events)
			for _, record := range records {
				if _, err := domain.MarshalRecordedEvent(record); err != nil {
					t.Fatalf("invalid fixture before lifecycle check: %v", err)
				}
			}
			if _, err := ReconstructRequestResult(record, records); !IsStoreCode(err, StoreCodeCorrupt) {
				t.Fatalf("invalid context companion accepted: %v", err)
			}
		})
	}
}

func TestReconstructLegacyProcessCrash(t *testing.T) {
	record, records := validRequestView(t, domain.AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "process_crash"}, domain.TurnInterrupted{TurnID: "turn-1", Reason: "process_crash"})
	result, err := ReconstructRequestResult(record, records)
	if err != nil || result.Status != domain.TurnStatusInterrupted || !result.TerminalCommitted {
		t.Fatalf("legacy crash reconstruction: %v", err)
	}
}
