package domain

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestReplayCompactDiscardsTerminalTranscript(t *testing.T) {
	records := fixtureRecords(t, "testdata/assistant_lifecycle.jsonl")
	got, err := ReplayCompact(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != uint64(len(records)) || got.ActiveTurn != nil {
		t.Fatalf("compact state = %#v", got)
	}
	if reflect.TypeOf(got).NumField() > 6 {
		t.Fatalf("compact state unexpectedly grew: %#v", got)
	}
	for index := 0; index < reflect.TypeOf(got).NumField(); index++ {
		field := reflect.TypeOf(got).Field(index)
		if field.Type.Kind() == reflect.Map || field.Type.Kind() == reflect.Slice {
			t.Fatalf("compact state retains a collection in field %q", field.Name)
		}
	}
}

func TestApplyCompactRetainsOnlyActiveTurnAndItem(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, TurnStarted{TurnID: "turn-1", Input: "inspect"})
	state = applyCompactRecord(t, state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})

	if state.ActiveTurn == nil || state.ActiveTurn.ID != "turn-1" || state.ActiveTurn.ActiveItem == nil {
		t.Fatalf("compact state = %#v", state)
	}
	if item := state.ActiveTurn.ActiveItem; item.ID != "item-1" || item.TurnID != "turn-1" || item.Kind != ItemKindAssistantMessage {
		t.Fatalf("compact item = %#v", item)
	}
}

func TestApplyCompactRejectsWrongTerminalIdentity(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, TurnStarted{TurnID: "turn-1", Input: "inspect"})
	state = applyCompactRecord(t, state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
	before := state.Clone()

	got, err := ApplyCompact(state, compactRecord(state, AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-other", Text: "done"}))
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("ApplyCompact() = (%#v, %v), want invalid event", got, err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("ApplyCompact() mutated input: got %#v, want %#v", state, before)
	}
}

func TestApplyCompactTerminalizationIsIrreversible(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, TurnStarted{TurnID: "turn-1", Input: "inspect"})
	state = applyCompactRecord(t, state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
	state = applyCompactRecord(t, state, AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "discard me"})
	if state.ActiveTurn == nil || state.ActiveTurn.ActiveItem != nil {
		t.Fatalf("compact item terminal state = %#v", state)
	}
	state = applyCompactRecord(t, state, TurnCompleted{TurnID: "turn-1"})
	if state.ActiveTurn != nil {
		t.Fatalf("compact turn terminal state = %#v", state)
	}

	_, err := ApplyCompact(state, compactRecord(state, TurnCompleted{TurnID: "turn-1"}))
	if !IsCode(err, CodeTurnNotRunning) {
		t.Fatalf("ApplyCompact() terminal replay error = %v, want %q", err, CodeTurnNotRunning)
	}
}

func TestApplyCompactClosesIdleSession(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, SessionClosed{})
	if state.Status != SessionStatusClosed || state.ActiveTurn != nil {
		t.Fatalf("closed compact state = %#v", state)
	}
	_, err := ApplyCompact(state, compactRecord(state, TurnStarted{TurnID: "turn-1", Input: "late"}))
	if !IsCode(err, CodeSessionClosed) {
		t.Fatalf("ApplyCompact() after close error = %v, want %q", err, CodeSessionClosed)
	}
}

func TestApplyCompactRejectsInvalidSequence(t *testing.T) {
	state := compactActiveSession(t)
	record := compactRecord(state, TurnStarted{TurnID: "turn-1", Input: "inspect"})
	record.Sequence++
	_, err := ApplyCompact(state, record)
	if !IsCode(err, CodeSequenceMismatch) {
		t.Fatalf("ApplyCompact() error = %v, want %q", err, CodeSequenceMismatch)
	}
}

func TestCompactSessionCloneIsolatesPointers(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, TurnStarted{TurnID: "turn-1", Input: "inspect"})
	state = applyCompactRecord(t, state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})

	clone := state.Clone()
	clone.ActiveTurn.Input = "changed"
	clone.ActiveTurn.ActiveItem.ID = "item-changed"
	if state.ActiveTurn.Input != "inspect" || state.ActiveTurn.ActiveItem.ID != "item-1" {
		t.Fatalf("Clone() leaked pointer mutation: original %#v, clone %#v", state, clone)
	}
}

func fixtureRecords(t *testing.T, path string) []RecordedEvent {
	t.Helper()
	data, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	records, err := DecodeJSONL(data)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func compactActiveSession(t *testing.T) CompactSession {
	t.Helper()
	state, err := ApplyCompact(CompactSession{}, compactRecord(CompactSession{}, SessionCreated{WorkspaceRoot: "/workspace"}))
	if err != nil {
		t.Fatalf("create compact session: %v", err)
	}
	return state
}

func applyCompactRecord(t *testing.T, state CompactSession, event Event) CompactSession {
	t.Helper()
	next, err := ApplyCompact(state, compactRecord(state, event))
	if err != nil {
		t.Fatalf("ApplyCompact(%T) error = %v", event, err)
	}
	return next
}

func compactRecord(state CompactSession, event Event) RecordedEvent {
	sequence := state.Version + 1
	return RecordedEvent{
		SchemaVersion: schemaVersion,
		ID:            EventID("compact-event-" + string(rune('a'+sequence))),
		CommandID:     CommandID("compact-command-" + string(rune('a'+sequence))),
		SessionID:     compactSessionID(state),
		Sequence:      sequence,
		OccurredAt:    time.Date(2026, 8, 13, 1, 2, int(sequence), 0, time.UTC),
		Event:         event,
	}
}

func compactSessionID(state CompactSession) SessionID {
	if state.ID == "" {
		return "compact-session"
	}
	return state.ID
}
