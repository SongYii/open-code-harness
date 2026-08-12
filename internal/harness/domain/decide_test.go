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

func TestDecideStartAssistantMessageReturnsStartedEvent(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	events, err := Decide(state, StartAssistantMessage{
		SessionID: state.ID, TurnID: "turn-1", ItemID: "item-1",
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	want := []UncommittedEvent{{Event: AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Decide() = %#v, want %#v", events, want)
	}
}

func TestDecideCompleteAssistantTurnReturnsAtomicBatch(t *testing.T) {
	t.Parallel()

	state := runningAssistantItemForTest(t)
	events, err := Decide(state, CompleteAssistantTurn{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Text: "完成 ✅",
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	want := []UncommittedEvent{
		{Event: AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "完成 ✅"}},
		{Event: TurnCompleted{TurnID: "turn-1"}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Decide() = %#v, want %#v", events, want)
	}
}

func TestDecideFailAssistantTurnReturnsAtomicBatch(t *testing.T) {
	t.Parallel()

	state := runningAssistantItemForTest(t)
	events, err := Decide(state, FailAssistantTurn{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1",
		Code: "provider_error", Message: "provider unavailable",
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	want := []UncommittedEvent{
		{Event: AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: "provider unavailable"}},
		{Event: TurnFailed{TurnID: "turn-1", Code: "provider_error", Message: "provider unavailable"}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Decide() = %#v, want %#v", events, want)
	}
}

func TestDecideInterruptAssistantTurnReturnsAtomicBatch(t *testing.T) {
	t.Parallel()

	state := runningAssistantItemForTest(t)
	events, err := Decide(state, InterruptAssistantTurn{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1",
		Code: "caller_canceled", Message: "",
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	want := []UncommittedEvent{
		{Event: AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "caller_canceled", Message: ""}},
		{Event: TurnInterrupted{TurnID: "turn-1", Reason: "caller_canceled"}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Decide() = %#v, want %#v", events, want)
	}
}

func TestDecideAssistantCommandsExposeStableMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command Command
		name    string
	}{
		{command: StartAssistantMessage{SessionID: "session-1"}, name: "assistant.message.start"},
		{command: CompleteAssistantTurn{SessionID: "session-1"}, name: "assistant.turn.complete"},
		{command: FailAssistantTurn{SessionID: "session-1"}, name: "assistant.turn.fail"},
		{command: InterruptAssistantTurn{SessionID: "session-1"}, name: "assistant.turn.interrupt"},
	}

	for _, test := range tests {
		if got := test.command.CommandType(); got != test.name {
			t.Errorf("CommandType() = %q, want %q", got, test.name)
		}
		if got := test.command.TargetSessionID(); got != "session-1" {
			t.Errorf("TargetSessionID() = %q, want %q", got, SessionID("session-1"))
		}
	}
}

func TestDecideAssistantCommandsRejectInvalidItemState(t *testing.T) {
	t.Parallel()

	runningItem := runningAssistantItemForTest(t)
	completedItem := terminalAssistantItemForTest(t)
	tests := []struct {
		name  string
		state Session
		cmd   Command
		code  ErrorCode
	}{
		{
			name:  "wrong active item",
			state: runningItem,
			cmd:   CompleteAssistantTurn{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-2", Text: "done"},
			code:  CodeItemMismatch,
		},
		{
			name:  "duplicate item",
			state: completedItem,
			cmd:   StartAssistantMessage{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1"},
			code:  CodeItemAlreadyExists,
		},
		{
			name:  "second running item",
			state: runningItem,
			cmd:   StartAssistantMessage{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-2"},
			code:  CodeItemAlreadyRunning,
		},
		{
			name:  "item outside active turn",
			state: runningItem,
			cmd:   StartAssistantMessage{SessionID: "session-1", TurnID: "turn-2", ItemID: "item-2"},
			code:  CodeTurnMismatch,
		},
		{
			name:  "second terminal transition",
			state: completedItem,
			cmd:   CompleteAssistantTurn{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Text: "again"},
			code:  CodeItemNotRunning,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.state.Clone()
			events, err := Decide(test.state, test.cmd)
			if !IsCode(err, test.code) {
				t.Fatalf("Decide() events = %#v, error = %v, want code %q", events, err, test.code)
			}
			if events != nil {
				t.Fatalf("Decide() events = %#v, want nil", events)
			}
			if !reflect.DeepEqual(test.state, before) {
				t.Fatalf("Decide() mutated state: got %#v, want %#v", test.state, before)
			}
		})
	}
}

func TestTurnTerminalRejectsRunningItem(t *testing.T) {
	t.Parallel()

	state := runningAssistantItemForTest(t)
	tests := []Command{
		CompleteTurn{SessionID: "session-1", TurnID: "turn-1"},
		FailTurn{SessionID: "session-1", TurnID: "turn-1", Code: "provider_error", Message: "provider unavailable"},
		InterruptTurn{SessionID: "session-1", TurnID: "turn-1", Reason: "caller_canceled"},
	}

	for _, command := range tests {
		events, err := Decide(state, command)
		if !IsCode(err, CodeItemAlreadyRunning) {
			t.Errorf("Decide(%T) events = %#v, error = %v, want code %q", command, events, err, CodeItemAlreadyRunning)
		}
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

func TestDecideRejectsInvalidUTF8CommandFields(t *testing.T) {
	t.Parallel()

	invalid := "value-\xff"
	tests := []struct {
		name  string
		state Session
		cmd   Command
		code  ErrorCode
	}{
		{
			name: "create session ID",
			cmd:  CreateSession{SessionID: SessionID(invalid), WorkspaceRoot: "/workspace"},
			code: CodeInvalidID,
		},
		{
			name: "create workspace root",
			cmd:  CreateSession{SessionID: "session-1", WorkspaceRoot: invalid},
			code: CodeInvalidCommand,
		},
		{
			name:  "start session ID",
			state: activeSessionForTest(t),
			cmd:   StartTurn{SessionID: SessionID(invalid), TurnID: "turn-1", Input: "inspect"},
			code:  CodeInvalidID,
		},
		{
			name:  "start turn ID",
			state: activeSessionForTest(t),
			cmd:   StartTurn{SessionID: "session-1", TurnID: TurnID(invalid), Input: "inspect"},
			code:  CodeInvalidID,
		},
		{
			name:  "start input",
			state: activeSessionForTest(t),
			cmd:   StartTurn{SessionID: "session-1", TurnID: "turn-1", Input: invalid},
			code:  CodeInvalidCommand,
		},
		{
			name:  "failure code",
			state: runningTurnForTest(t),
			cmd:   FailTurn{SessionID: "session-1", TurnID: "turn-1", Code: invalid, Message: "provider failed"},
			code:  CodeInvalidCommand,
		},
		{
			name:  "failure message",
			state: runningTurnForTest(t),
			cmd:   FailTurn{SessionID: "session-1", TurnID: "turn-1", Code: "provider_error", Message: invalid},
			code:  CodeInvalidCommand,
		},
		{
			name:  "interruption reason",
			state: runningTurnForTest(t),
			cmd:   InterruptTurn{SessionID: "session-1", TurnID: "turn-1", Reason: invalid},
			code:  CodeInvalidCommand,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events, err := Decide(test.state, test.cmd)
			if !IsCode(err, test.code) {
				t.Fatalf("Decide() events = %#v, error = %v, want code %q", events, err, test.code)
			}
		})
	}
}

func runningAssistantItemForTest(t *testing.T) Session {
	t.Helper()
	state := runningTurnForTest(t)
	next, err := Apply(state, recordedForTest(state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}))
	if err != nil {
		t.Fatalf("start assistant item: %v", err)
	}
	return next
}

func terminalAssistantItemForTest(t *testing.T) Session {
	t.Helper()
	state := runningAssistantItemForTest(t)
	next, err := Apply(state, recordedForTest(state, AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}))
	if err != nil {
		t.Fatalf("complete assistant item: %v", err)
	}
	return next
}
