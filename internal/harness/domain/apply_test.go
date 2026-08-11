package domain

import (
	"math"
	"reflect"
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

func TestApplyRejectsRecordedEventsOutsideCodecContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		state  Session
		record RecordedEvent
		code   ErrorCode
	}{
		{
			name: "padded event ID",
			record: RecordedEvent{
				SchemaVersion: 1, ID: " event-1", CommandID: "command-1", SessionID: "session-1",
				Sequence: 1, OccurredAt: time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
				Event: SessionCreated{WorkspaceRoot: "/workspace"},
			},
			code: CodeInvalidID,
		},
		{
			name: "padded command ID",
			record: RecordedEvent{
				SchemaVersion: 1, ID: "event-1", CommandID: "command-1 ", SessionID: "session-1",
				Sequence: 1, OccurredAt: time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
				Event: SessionCreated{WorkspaceRoot: "/workspace"},
			},
			code: CodeInvalidCommand,
		},
		{
			name: "padded session ID",
			record: RecordedEvent{
				SchemaVersion: 1, ID: "event-1", CommandID: "command-1", SessionID: "session-1 ",
				Sequence: 1, OccurredAt: time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
				Event: SessionCreated{WorkspaceRoot: "/workspace"},
			},
			code: CodeInvalidID,
		},
		{
			name: "timestamp outside RFC3339 range",
			record: RecordedEvent{
				SchemaVersion: 1, ID: "event-1", CommandID: "command-1", SessionID: "session-1",
				Sequence: 1, OccurredAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
				Event: SessionCreated{WorkspaceRoot: "/workspace"},
			},
			code: CodeInvalidEvent,
		},
		{
			name: "whitespace-only workspace root",
			record: RecordedEvent{
				SchemaVersion: 1, ID: "event-1", CommandID: "command-1", SessionID: "session-1",
				Sequence: 1, OccurredAt: time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
				Event: SessionCreated{WorkspaceRoot: " \t "},
			},
			code: CodeInvalidEvent,
		},
		{
			name:  "whitespace-only turn input",
			state: activeSessionForTest(t),
			record: RecordedEvent{
				SchemaVersion: 1, ID: "event-2", CommandID: "command-2", SessionID: "session-1",
				Sequence: 2, OccurredAt: time.Date(2026, 8, 11, 1, 2, 4, 0, time.UTC),
				Event: TurnStarted{TurnID: "turn-1", Input: " \t "},
			},
			code: CodeInvalidEvent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Apply(test.state, test.record)
			if !IsCode(err, test.code) {
				t.Fatalf("Apply() state = %#v, error = %v, want code %q", got, err, test.code)
			}
		})
	}
}

func TestApplyRejectsSessionVersionOverflow(t *testing.T) {
	t.Parallel()

	state := Session{
		ID:            "session-1",
		Status:        SessionStatusActive,
		Version:       math.MaxUint64,
		WorkspaceRoot: "/workspace",
		Turns:         map[TurnID]Turn{},
	}
	before := state.Clone()
	record := RecordedEvent{
		SchemaVersion: 1, ID: "event-overflow", CommandID: "command-overflow", SessionID: "session-1",
		Sequence: 0, OccurredAt: time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC), Event: SessionClosed{},
	}

	got, err := Apply(state, record)
	if !IsCode(err, CodeSequenceMismatch) {
		t.Fatalf("Apply() state = %#v, error = %v, want code %q", got, err, CodeSequenceMismatch)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Apply() mutated input: got %#v, want %#v", state, before)
	}
}

func TestApplyTurnStarted(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	record := recordedForTest(state, TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	})

	got, err := Apply(state, record)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Version != 2 || got.ActiveTurnID != TurnID("turn-1") {
		t.Fatalf("Apply() state = %#v", got)
	}
	if !reflect.DeepEqual(got.TurnOrder, []TurnID{TurnID("turn-1")}) {
		t.Fatalf("Apply() turn order = %#v, want %#v", got.TurnOrder, []TurnID{TurnID("turn-1")})
	}
	wantTurn := Turn{
		ID: TurnID("turn-1"), Status: TurnStatusRunning,
		Input: "inspect repository", StartedAt: record.OccurredAt,
	}
	if got.Turns[TurnID("turn-1")] != wantTurn {
		t.Fatalf("Apply() turn = %#v, want %#v", got.Turns[TurnID("turn-1")], wantTurn)
	}
}

func TestApplyTurnStartedDoesNotMutateInputState(t *testing.T) {
	state := activeSessionForTest(t)
	before := state.Clone()

	_, err := Apply(state, recordedForTest(state, TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	}))
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Apply() mutated input: got %#v want %#v", state, before)
	}
}

func TestApplyTurnStartedAllocatesTurnsForValidActiveSessionWithNilMap(t *testing.T) {
	t.Parallel()

	state := Session{
		ID:            SessionID("session-1"),
		Status:        SessionStatusActive,
		Version:       1,
		WorkspaceRoot: "/workspace",
	}
	record := recordedForTest(state, TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	})

	got, err := Apply(state, record)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if state.Turns != nil {
		t.Fatalf("Apply() mutated input Turns = %#v, want nil", state.Turns)
	}
	if got.Turns == nil || got.Turns[TurnID("turn-1")].Status != TurnStatusRunning {
		t.Fatalf("Apply() Turns = %#v, want allocated running turn", got.Turns)
	}
}

func TestApplyTerminalTurnEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event Event
		want  Turn
	}{
		{
			name:  "complete",
			event: TurnCompleted{TurnID: TurnID("turn-1")},
			want:  Turn{ID: TurnID("turn-1"), Status: TurnStatusCompleted, Input: "inspect repository"},
		},
		{
			name:  "fail",
			event: TurnFailed{TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"},
			want:  Turn{ID: TurnID("turn-1"), Status: TurnStatusFailed, Input: "inspect repository", FailureCode: "provider_rate_limit", FailureText: "retry budget exhausted"},
		},
		{
			name:  "interrupt",
			event: TurnInterrupted{TurnID: TurnID("turn-1"), Reason: "user_cancelled"},
			want:  Turn{ID: TurnID("turn-1"), Status: TurnStatusInterrupted, Input: "inspect repository", InterruptWhy: "user_cancelled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := runningTurnForTest(t)
			record := recordedForTest(state, tt.event)
			got, err := Apply(state, record)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if got.Version != record.Sequence || got.ActiveTurnID != "" {
				t.Fatalf("Apply() state = %#v", got)
			}
			want := tt.want
			want.StartedAt = state.Turns[TurnID("turn-1")].StartedAt
			want.EndedAt = record.OccurredAt
			if got.Turns[TurnID("turn-1")] != want {
				t.Fatalf("Apply() turn = %#v, want %#v", got.Turns[TurnID("turn-1")], want)
			}
		})
	}
}

func TestApplyTerminalRejectsMismatchedRunningTurn(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	before := state.Clone()
	_, err := Apply(state, recordedForTest(state, TurnCompleted{TurnID: TurnID("turn-2")}))
	if !IsCode(err, CodeTurnMismatch) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeTurnMismatch)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Apply() mutated input: got %#v want %#v", state, before)
	}
}

func TestApplyTerminalStatesAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	completed, err := Apply(state, recordedForTest(state, TurnCompleted{TurnID: TurnID("turn-1")}))
	if err != nil {
		t.Fatalf("apply completion: %v", err)
	}
	before := completed.Clone()

	for _, event := range []Event{
		TurnFailed{TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"},
		TurnInterrupted{TurnID: TurnID("turn-1"), Reason: "user_cancelled"},
	} {
		_, err := Apply(completed, recordedForTest(completed, event))
		if !IsCode(err, CodeTurnNotRunning) {
			t.Fatalf("Apply() error = %v, want code %q", err, CodeTurnNotRunning)
		}
		if !reflect.DeepEqual(completed, before) {
			t.Fatalf("Apply() mutated completed state: got %#v want %#v", completed, before)
		}
	}
}

func TestApplySessionClosed(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	record := recordedForTest(state, SessionClosed{})
	got, err := Apply(state, record)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Status != SessionStatusClosed || got.ActiveTurnID != "" || got.Version != record.Sequence {
		t.Fatalf("Apply() state = %#v", got)
	}
}

func TestApplySessionClosedRejectsRunningTurn(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	_, err := Apply(state, recordedForTest(state, SessionClosed{}))
	if !IsCode(err, CodeTurnAlreadyRunning) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeTurnAlreadyRunning)
	}
}
