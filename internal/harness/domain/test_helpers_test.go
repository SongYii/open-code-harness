package domain

import (
	"fmt"
	"testing"
	"time"
)

func recordedForTest(state HistoricalSession, event Event) RecordedEvent {
	sequence := state.Version + 1
	return RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID(fmt.Sprintf("event-%d", sequence)),
		CommandID:     CommandID(fmt.Sprintf("command-%d", sequence)),
		SessionID:     SessionID("session-1"),
		Sequence:      sequence,
		OccurredAt:    time.Date(2026, 8, 11, 0, 0, int(sequence), 0, time.UTC),
		Event:         event,
	}
}

func activeSessionForTest(t *testing.T) HistoricalSession {
	t.Helper()
	state, err := HistoricalApply(HistoricalSession{}, recordedForTest(HistoricalSession{}, SessionCreated{WorkspaceRoot: "/workspace"}))
	if err != nil {
		t.Fatalf("create test session: %v", err)
	}
	return state
}

func runningTurnForTest(t *testing.T) HistoricalSession {
	t.Helper()
	state := activeSessionForTest(t)
	state, err := HistoricalApply(state, recordedForTest(state, TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	}))
	if err != nil {
		t.Fatalf("start test turn: %v", err)
	}
	return state
}
