package domain

import (
	"testing"
	"time"
)

func TestApplySessionCreated(t *testing.T) {
	t.Parallel()

	record := RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID("event-1"),
		CommandID:     CommandID("command-1"),
		SessionID:     SessionID("session-1"),
		Sequence:      1,
		OccurredAt:    time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
		Event:         SessionCreated{WorkspaceRoot: "/workspace"},
	}

	got, err := Apply(Session{}, record)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.ID != SessionID("session-1") || got.Status != SessionStatusActive || got.Version != 1 {
		t.Fatalf("Apply() state = %#v", got)
	}
	if got.WorkspaceRoot != "/workspace" || len(got.Turns) != 0 {
		t.Fatalf("Apply() state = %#v", got)
	}
}

func TestApplyRejectsNonInitialSequence(t *testing.T) {
	t.Parallel()

	_, err := Apply(Session{}, RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID("event-2"),
		CommandID:     CommandID("command-2"),
		SessionID:     SessionID("session-1"),
		Sequence:      2,
		OccurredAt:    time.Now(),
		Event:         SessionCreated{WorkspaceRoot: "/workspace"},
	})
	if !IsCode(err, CodeSequenceMismatch) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeSequenceMismatch)
	}
}

func TestApplySessionCreatedRejectsNonPristineState(t *testing.T) {
	t.Parallel()

	_, err := Apply(Session{Version: 1}, RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID("event-3"),
		CommandID:     CommandID("command-3"),
		SessionID:     SessionID("session-1"),
		Sequence:      2,
		OccurredAt:    time.Now(),
		Event:         SessionCreated{WorkspaceRoot: "/workspace"},
	})
	if !IsCode(err, CodeSessionAlreadyExists) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeSessionAlreadyExists)
	}
}
