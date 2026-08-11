package domain

import (
	"reflect"
	"testing"
)

func TestDecideCreateSession(t *testing.T) {
	t.Parallel()

	events, err := Decide(Session{}, CreateSession{
		SessionID: SessionID("session-1"), WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	want := []UncommittedEvent{{Event: SessionCreated{WorkspaceRoot: "/workspace"}}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Decide() = %#v, want %#v", events, want)
	}
}

func TestDecideCreateSessionRejectsExistingSession(t *testing.T) {
	t.Parallel()

	_, err := Decide(activeSessionForTest(t), CreateSession{
		SessionID: SessionID("session-2"), WorkspaceRoot: "/workspace",
	})
	if !IsCode(err, CodeSessionAlreadyExists) {
		t.Fatalf("Decide() error = %v, want code %q", err, CodeSessionAlreadyExists)
	}
}

func TestDecideStartTurn(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	events, err := Decide(state, StartTurn{
		SessionID: state.ID, TurnID: TurnID("turn-1"), Input: "inspect repository",
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	want := []UncommittedEvent{{Event: TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	}}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Decide() = %#v, want %#v", events, want)
	}
}

func TestDecideStartTurnRejectsInvalidState(t *testing.T) {
	t.Parallel()

	closed := activeSessionForTest(t)
	closed.Status = SessionStatusClosed
	running := runningTurnForTest(t)

	tests := []struct {
		name  string
		state Session
		cmd   StartTurn
		code  ErrorCode
	}{
		{
			name:  "session ID mismatch",
			state: activeSessionForTest(t),
			cmd:   StartTurn{SessionID: SessionID("session-2"), TurnID: TurnID("turn-1"), Input: "inspect repository"},
			code:  CodeInvalidCommand,
		},
		{
			name:  "closed session",
			state: closed,
			cmd:   StartTurn{SessionID: closed.ID, TurnID: TurnID("turn-1"), Input: "inspect repository"},
			code:  CodeSessionClosed,
		},
		{
			name:  "duplicate turn ID",
			state: running,
			cmd:   StartTurn{SessionID: running.ID, TurnID: TurnID("turn-1"), Input: "inspect repository"},
			code:  CodeTurnAlreadyExists,
		},
		{
			name:  "second active turn",
			state: running,
			cmd:   StartTurn{SessionID: running.ID, TurnID: TurnID("turn-2"), Input: "inspect repository"},
			code:  CodeTurnAlreadyRunning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decide(tt.state, tt.cmd)
			if !IsCode(err, tt.code) {
				t.Fatalf("Decide() error = %v, want code %q", err, tt.code)
			}
		})
	}
}

func TestDecideStartTurnRejectsBlankInput(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	_, err := Decide(state, StartTurn{
		SessionID: state.ID, TurnID: TurnID("turn-1"), Input: "  ",
	})
	if !IsCode(err, CodeInvalidCommand) {
		t.Fatalf("Decide() error = %v, want code %q", err, CodeInvalidCommand)
	}
}
