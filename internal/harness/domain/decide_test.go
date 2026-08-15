package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestStartAssistantTurnEligibilityIsSharedWithDecide(t *testing.T) {
	t.Parallel()

	closed := activeSessionForTest(t)
	var err error
	closed, err = Apply(closed, recordedForTest(closed, SessionClosed{}))
	if err != nil {
		t.Fatalf("close fixture session: %v", err)
	}
	malformed := activeSessionForTest(t)
	malformed.TurnOrder = []TurnID{"turn-missing"}
	malformedContainers := activeSessionForTest(t)
	malformedContainers.TurnOrder = nil
	malformedContainers.Turns = nil
	running := runningTurnForTest(t)
	withCompletedItem := terminalAssistantItemForTest(t)
	withCompletedItem, err = Apply(withCompletedItem, recordedForTest(withCompletedItem, TurnCompleted{TurnID: "turn-1"}))
	if err != nil {
		t.Fatalf("complete fixture turn: %v", err)
	}
	malformedItemTimeline := withCompletedItem.Clone()
	turn := malformedItemTimeline.Turns["turn-1"]
	item := turn.Items["item-1"]
	item.StartedAt = turn.StartedAt.Add(-time.Second)
	turn.Items["item-1"] = item
	malformedItemTimeline.Turns["turn-1"] = turn
	invalidTimestamp := withCompletedItem.Clone()
	turn = invalidTimestamp.Turns["turn-1"]
	turn.EndedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	invalidTimestamp.Turns["turn-1"] = turn
	impossibleVersion := activeSessionForTest(t)
	impossibleVersion.Version = 2

	tests := []struct {
		name  string
		state Session
		code  ErrorCode
	}{
		{name: "active", state: activeSessionForTest(t)},
		{name: "closed", state: closed, code: CodeSessionClosed},
		{name: "running turn", state: running, code: CodeTurnAlreadyRunning},
		{name: "completed item is eligible", state: withCompletedItem},
		{name: "malformed", state: malformed, code: CodeInvalidCommand},
		{name: "malformed containers", state: malformedContainers, code: CodeInvalidCommand},
		{name: "malformed item timeline", state: malformedItemTimeline, code: CodeInvalidCommand},
		{name: "timestamp outside RFC3339", state: invalidTimestamp, code: CodeInvalidCommand},
		{name: "impossible version", state: impossibleVersion, code: CodeInvalidCommand},
		{name: "missing", state: Session{}, code: CodeSessionNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eligibilityErr := CheckStartAssistantTurnEligibility(test.state)
			_, decideErr := Decide(test.state, StartAssistantTurn{
				SessionID: test.state.ID,
				TurnID:    "turn-new",
				ItemID:    "item-new",
				Input:     "inspect repository",
			})
			if !IsCode(eligibilityErr, test.code) && !(test.code == "" && eligibilityErr == nil) {
				t.Fatalf("CheckStartAssistantTurnEligibility() error = %v, want code %q", eligibilityErr, test.code)
			}
			if !IsCode(decideErr, test.code) && !(test.code == "" && decideErr == nil) {
				t.Fatalf("Decide() error = %v, want code %q", decideErr, test.code)
			}
		})
	}
}

func TestDecideStartAssistantTurnReturnsAtomicOrderedBatch(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	before := state.Clone()
	events, err := Decide(state, StartAssistantTurn{
		SessionID: state.ID,
		TurnID:    "turn-atomic",
		ItemID:    "item-atomic",
		Input:     "inspect repository",
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	want := []UncommittedEvent{
		{Event: TurnStarted{TurnID: "turn-atomic", Input: "inspect repository"}},
		{Event: AssistantMessageStarted{TurnID: "turn-atomic", ItemID: "item-atomic"}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Decide() = %#v, want %#v", events, want)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Decide() mutated state: got %#v, want %#v", state, before)
	}

	now := time.Date(2026, 8, 12, 2, 3, 4, 0, time.UTC)
	for index, event := range events {
		state, err = Apply(state, RecordedEvent{
			SchemaVersion: 1,
			ID:            EventID("event-atomic-" + string(rune('1'+index))),
			CommandID:     "command-atomic",
			SessionID:     state.ID,
			Sequence:      state.Version + 1,
			OccurredAt:    now,
			Event:         event.Event,
		})
		if err != nil {
			t.Fatalf("Apply(event %d) error = %v", index, err)
		}
	}
	if state.Version != before.Version+2 || state.ActiveTurnID != "turn-atomic" {
		t.Fatalf("state after atomic batch = %#v", state)
	}
	turn := state.Turns["turn-atomic"]
	if turn.ActiveItemID != "item-atomic" || turn.Items["item-atomic"].Status != ItemStatusRunning {
		t.Fatalf("turn after atomic batch = %#v", turn)
	}
}

func TestDecideStartAssistantTurnRejectsInvalidCommandFields(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	tests := []struct {
		name string
		cmd  StartAssistantTurn
	}{
		{name: "session ID", cmd: StartAssistantTurn{SessionID: " session-1", TurnID: "turn-1", ItemID: "item-1", Input: "hello"}},
		{name: "session mismatch", cmd: StartAssistantTurn{SessionID: "session-2", TurnID: "turn-1", ItemID: "item-1", Input: "hello"}},
		{name: "turn ID", cmd: StartAssistantTurn{SessionID: state.ID, TurnID: " turn-1", ItemID: "item-1", Input: "hello"}},
		{name: "item ID", cmd: StartAssistantTurn{SessionID: state.ID, TurnID: "turn-1", ItemID: " item-1", Input: "hello"}},
		{name: "blank input", cmd: StartAssistantTurn{SessionID: state.ID, TurnID: "turn-1", ItemID: "item-1", Input: "  "}},
		{name: "invalid UTF-8 input", cmd: StartAssistantTurn{SessionID: state.ID, TurnID: "turn-1", ItemID: "item-1", Input: string([]byte{0xff})}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if events, err := Decide(state, test.cmd); err == nil || events != nil {
				t.Fatalf("Decide() = (%#v, %v), want rejection", events, err)
			}
		})
	}
}

func TestDecideStartAssistantTurnRejectsExistingTurnOrItem(t *testing.T) {
	t.Parallel()

	state := terminalAssistantItemForTest(t)
	state, err := Apply(state, recordedForTest(state, TurnCompleted{TurnID: "turn-1"}))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		cmd  StartAssistantTurn
		code ErrorCode
	}{
		{name: "turn", cmd: StartAssistantTurn{SessionID: state.ID, TurnID: "turn-1", ItemID: "item-2", Input: "hello"}, code: CodeTurnAlreadyExists},
		{name: "item", cmd: StartAssistantTurn{SessionID: state.ID, TurnID: "turn-2", ItemID: "item-1", Input: "hello"}, code: CodeItemAlreadyExists},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := state.Clone()
			events, err := Decide(state, test.cmd)
			if events != nil || !IsCode(err, test.code) {
				t.Fatalf("Decide() = (%#v, %v), want code %q", events, err, test.code)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatal("Decide() mutated state")
			}
		})
	}
}

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

func TestDecideRequestAbandonedInterruptsRunningAssistantTurn(t *testing.T) {
	t.Parallel()

	state := runningAssistantItemForTest(t)
	events, err := Decide(state, InterruptAssistantTurn{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1",
		Code: InterruptionRequestAbandoned, Message: "",
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	want := []UncommittedEvent{
		{Event: AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: InterruptionRequestAbandoned, Message: ""}},
		{Event: TurnInterrupted{TurnID: "turn-1", Reason: InterruptionRequestAbandoned}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Decide() = %#v, want %#v", events, want)
	}
}

func TestDecideRequestAbandonedRejectedAfterModelTerminal(t *testing.T) {
	t.Parallel()

	_, err := Decide(terminalAssistantItemForTest(t), InterruptAssistantTurn{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1",
		Code: InterruptionRequestAbandoned, Message: "",
	})
	if !IsCode(err, CodeItemNotRunning) {
		t.Fatalf("error = %v, want %q", err, CodeItemNotRunning)
	}
}

func TestDecideProcessCrashInterruptionIsRejected(t *testing.T) {
	t.Parallel()

	_, err := Decide(runningAssistantItemForTest(t), InterruptAssistantTurn{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1",
		Code: "process_crash", Message: "",
	})
	if !IsCode(err, CodeInvalidCommand) {
		t.Fatalf("error = %v, want %q", err, CodeInvalidCommand)
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
		{command: StartAssistantTurn{SessionID: "session-1"}, name: "assistant.turn.start"},
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
