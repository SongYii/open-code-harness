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

func TestDecideCloseSession(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	events, err := Decide(state, CloseSession{SessionID: state.ID})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	want := []UncommittedEvent{{Event: SessionClosed{}}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Decide() = %#v, want %#v", events, want)
	}
}

func TestDecideCloseSessionRejectsRunningTurn(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	_, err := Decide(state, CloseSession{SessionID: state.ID})
	if !IsCode(err, CodeTurnAlreadyRunning) {
		t.Fatalf("Decide() error = %v, want code %q", err, CodeTurnAlreadyRunning)
	}
}

func TestClosedSessionRejectsNonCreateCommands(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	state.Status = SessionStatusClosed

	tests := []struct {
		name string
		cmd  Command
	}{
		{"start", StartTurn{SessionID: state.ID, TurnID: TurnID("turn-1"), Input: "inspect repository"}},
		{"complete", CompleteTurn{SessionID: state.ID, TurnID: TurnID("turn-1")}},
		{"fail", FailTurn{SessionID: state.ID, TurnID: TurnID("turn-1"), Code: "provider_error", Message: "provider failed"}},
		{"interrupt", InterruptTurn{SessionID: state.ID, TurnID: TurnID("turn-1"), Reason: "user_cancelled"}},
		{"close", CloseSession{SessionID: state.ID}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decide(state, tt.cmd)
			if !IsCode(err, CodeSessionClosed) {
				t.Fatalf("Decide() error = %v, want code %q", err, CodeSessionClosed)
			}
		})
	}
}

func TestTerminalTurnTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  Command
		want Event
	}{
		{"complete", CompleteTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1")}, TurnCompleted{TurnID: TurnID("turn-1")}},
		{"fail", FailTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"}, TurnFailed{TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"}},
		{"interrupt", InterruptTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1"), Reason: "user_cancelled"}, TurnInterrupted{TurnID: TurnID("turn-1"), Reason: "user_cancelled"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := runningTurnForTest(t)
			events, err := Decide(state, tt.cmd)
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if len(events) != 1 || !reflect.DeepEqual(events[0].Event, tt.want) {
				t.Fatalf("Decide() = %#v, want %#v", events, tt.want)
			}
		})
	}
}

func TestDecideTerminalRejectsInvalidCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state Session
		cmd   Command
		code  ErrorCode
	}{
		{
			name:  "wrong turn ID",
			state: runningTurnForTest(t),
			cmd:   CompleteTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-2")},
			code:  CodeTurnMismatch,
		},
		{
			name:  "no running turn",
			state: activeSessionForTest(t),
			cmd:   CompleteTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1")},
			code:  CodeTurnNotRunning,
		},
		{
			name:  "blank failure code",
			state: runningTurnForTest(t),
			cmd:   FailTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1"), Code: "  ", Message: "retry budget exhausted"},
			code:  CodeInvalidCommand,
		},
		{
			name:  "blank failure message",
			state: runningTurnForTest(t),
			cmd:   FailTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "  "},
			code:  CodeInvalidCommand,
		},
		{
			name:  "blank interruption reason",
			state: runningTurnForTest(t),
			cmd:   InterruptTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1"), Reason: "  "},
			code:  CodeInvalidCommand,
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
