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

	got, err := HistoricalApply(HistoricalSession{}, record)
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

	_, err := HistoricalApply(HistoricalSession{}, RecordedEvent{
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

	_, err := HistoricalApply(HistoricalSession{Version: 1}, RecordedEvent{
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
		state  HistoricalSession
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
			got, err := HistoricalApply(test.state, test.record)
			if !IsCode(err, test.code) {
				t.Fatalf("Apply() state = %#v, error = %v, want code %q", got, err, test.code)
			}
		})
	}
}

func TestApplyRejectsSessionVersionOverflow(t *testing.T) {
	t.Parallel()

	state := HistoricalSession{
		ID:            "session-1",
		Status:        SessionStatusActive,
		Version:       math.MaxUint64,
		WorkspaceRoot: "/workspace",
		Turns:         map[TurnID]HistoricalTurn{},
	}
	before := state.Clone()
	record := RecordedEvent{
		SchemaVersion: 1, ID: "event-overflow", CommandID: "command-overflow", SessionID: "session-1",
		Sequence: 0, OccurredAt: time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC), Event: SessionClosed{},
	}

	got, err := HistoricalApply(state, record)
	if !IsCode(err, CodeSequenceMismatch) {
		t.Fatalf("Apply() state = %#v, error = %v, want code %q", got, err, CodeSequenceMismatch)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Apply() mutated input: got %#v, want %#v", state, before)
	}
}

func TestApplyRejectsInvalidUTF8MetadataIDs(t *testing.T) {
	t.Parallel()

	invalid := "identifier-\xff"
	tests := []struct {
		name   string
		mutate func(*RecordedEvent)
		code   ErrorCode
	}{
		{name: "event ID", mutate: func(record *RecordedEvent) { record.ID = EventID(invalid) }, code: CodeInvalidID},
		{name: "session ID", mutate: func(record *RecordedEvent) { record.SessionID = SessionID(invalid) }, code: CodeInvalidID},
		{name: "command ID", mutate: func(record *RecordedEvent) { record.CommandID = CommandID(invalid) }, code: CodeInvalidCommand},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := recordedForTest(HistoricalSession{}, SessionCreated{WorkspaceRoot: "/workspace"})
			test.mutate(&record)
			state, err := HistoricalApply(HistoricalSession{}, record)
			if !IsCode(err, test.code) {
				t.Fatalf("Apply() state = %#v, error = %v, want code %q", state, err, test.code)
			}
		})
	}
}

func TestApplyRejectsInvalidUTF8EventPayloads(t *testing.T) {
	t.Parallel()

	invalid := "value-\xff"
	tests := []struct {
		name  string
		state HistoricalSession
		event Event
	}{
		{name: "workspace root", event: SessionCreated{WorkspaceRoot: invalid}},
		{name: "turn input", state: activeSessionForTest(t), event: TurnStarted{TurnID: "turn-1", Input: invalid}},
		{name: "failure code", state: runningTurnForTest(t), event: TurnFailed{TurnID: "turn-1", Code: invalid, Message: "provider failed"}},
		{name: "failure message", state: runningTurnForTest(t), event: TurnFailed{TurnID: "turn-1", Code: "provider_error", Message: invalid}},
		{name: "interruption reason", state: runningTurnForTest(t), event: TurnInterrupted{TurnID: "turn-1", Reason: invalid}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := recordedForTest(test.state, test.event)
			state, err := HistoricalApply(test.state, record)
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("Apply() state = %#v, error = %v, want code %q", state, err, CodeInvalidEvent)
			}
		})
	}
}

func TestApplyTurnStarted(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	record := recordedForTest(state, TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	})

	got, err := HistoricalApply(state, record)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Version != 2 || got.ActiveTurnID != TurnID("turn-1") {
		t.Fatalf("Apply() state = %#v", got)
	}
	if !reflect.DeepEqual(got.TurnOrder, []TurnID{TurnID("turn-1")}) {
		t.Fatalf("Apply() turn order = %#v, want %#v", got.TurnOrder, []TurnID{TurnID("turn-1")})
	}
	wantTurn := HistoricalTurn{
		ID: TurnID("turn-1"), Status: TurnStatusRunning,
		Input: "inspect repository", StartedAt: record.OccurredAt,
		ItemOrder: []ItemID{}, Items: map[ItemID]HistoricalItem{},
	}
	if !reflect.DeepEqual(got.Turns[TurnID("turn-1")], wantTurn) {
		t.Fatalf("Apply() turn = %#v, want %#v", got.Turns[TurnID("turn-1")], wantTurn)
	}
}

func TestApplyTurnStartedDoesNotMutateInputState(t *testing.T) {
	state := activeSessionForTest(t)
	before := state.Clone()

	_, err := HistoricalApply(state, recordedForTest(state, TurnStarted{
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

	state := HistoricalSession{
		ID:            SessionID("session-1"),
		Status:        SessionStatusActive,
		Version:       1,
		WorkspaceRoot: "/workspace",
	}
	record := recordedForTest(state, TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	})

	got, err := HistoricalApply(state, record)
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
		want  HistoricalTurn
	}{
		{
			name:  "complete",
			event: TurnCompleted{TurnID: TurnID("turn-1")},
			want:  HistoricalTurn{ID: TurnID("turn-1"), Status: TurnStatusCompleted, Input: "inspect repository"},
		},
		{
			name:  "fail",
			event: TurnFailed{TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"},
			want:  HistoricalTurn{ID: TurnID("turn-1"), Status: TurnStatusFailed, Input: "inspect repository", FailureCode: "provider_rate_limit", FailureText: "retry budget exhausted"},
		},
		{
			name:  "interrupt",
			event: TurnInterrupted{TurnID: TurnID("turn-1"), Reason: "user_cancelled"},
			want:  HistoricalTurn{ID: TurnID("turn-1"), Status: TurnStatusInterrupted, Input: "inspect repository", InterruptWhy: "user_cancelled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := runningTurnForTest(t)
			record := recordedForTest(state, tt.event)
			got, err := HistoricalApply(state, record)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if got.Version != record.Sequence || got.ActiveTurnID != "" {
				t.Fatalf("Apply() state = %#v", got)
			}
			want := tt.want
			want.StartedAt = state.Turns[TurnID("turn-1")].StartedAt
			want.EndedAt = record.OccurredAt
			want.ItemOrder = []ItemID{}
			want.Items = map[ItemID]HistoricalItem{}
			if !reflect.DeepEqual(got.Turns[TurnID("turn-1")], want) {
				t.Fatalf("Apply() turn = %#v, want %#v", got.Turns[TurnID("turn-1")], want)
			}
		})
	}
}

func TestApplyTerminalRejectsMismatchedRunningTurn(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	before := state.Clone()
	_, err := HistoricalApply(state, recordedForTest(state, TurnCompleted{TurnID: TurnID("turn-2")}))
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
	completed, err := HistoricalApply(state, recordedForTest(state, TurnCompleted{TurnID: TurnID("turn-1")}))
	if err != nil {
		t.Fatalf("apply completion: %v", err)
	}
	before := completed.Clone()

	for _, event := range []Event{
		TurnFailed{TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"},
		TurnInterrupted{TurnID: TurnID("turn-1"), Reason: "user_cancelled"},
	} {
		_, err := HistoricalApply(completed, recordedForTest(completed, event))
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
	got, err := HistoricalApply(state, record)
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
	_, err := HistoricalApply(state, recordedForTest(state, SessionClosed{}))
	if !IsCode(err, CodeTurnAlreadyRunning) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeTurnAlreadyRunning)
	}
}

func TestApplyAssistantMessageLifecycle(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	started := recordedForTest(state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
	state, err := HistoricalApply(state, started)
	if err != nil {
		t.Fatalf("start item: %v", err)
	}

	completed := recordedForTest(state, AssistantMessageCompleted{
		TurnID: "turn-1", ItemID: "item-1", Text: "你好, exact bytes\n",
	})
	state, err = HistoricalApply(state, completed)
	if err != nil {
		t.Fatalf("complete item: %v", err)
	}

	turn := state.Turns["turn-1"]
	item := turn.Items["item-1"]
	payload, ok := item.Payload.(AssistantMessagePayload)
	if !ok || item.Status != ItemStatusCompleted || payload.Text != "你好, exact bytes\n" {
		t.Fatalf("item = %#v", item)
	}
	if item.ID != "item-1" || item.TurnID != "turn-1" || item.Kind != ItemKindAssistantMessage {
		t.Fatalf("item identity = %#v", item)
	}
	if !item.StartedAt.Equal(started.OccurredAt) || !item.EndedAt.Equal(completed.OccurredAt) || item.Terminal != nil {
		t.Fatalf("item lifecycle metadata = %#v", item)
	}
	if turn.ActiveItemID != "" || !reflect.DeepEqual(turn.ItemOrder, []ItemID{"item-1"}) {
		t.Fatalf("turn item state = %#v", turn)
	}
}

func TestApplyAssistantMessageStartedRejectsTimestampBeforeTurnStart(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	before := state.Clone()
	record := recordedForTest(state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
	record.OccurredAt = time.Date(2026, 8, 11, 0, 0, 1, 0, time.UTC)

	_, err := HistoricalApply(state, record)
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeInvalidEvent)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Apply() mutated input: got %#v want %#v", state, before)
	}
}

func TestApplyAssistantMessageTerminalRejectsTimestampBeforeItemStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event Event
	}{
		{name: "completed", event: AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}},
		{name: "failed", event: AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: "safe"}},
		{name: "interrupted", event: AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "caller_canceled", Message: ""}},
		{name: "request abandoned", event: AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: InterruptionRequestAbandoned, Message: ""}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := runningTurnForTest(t)
			started := recordedForTest(state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
			started.OccurredAt = time.Date(2026, 8, 11, 0, 0, 4, 0, time.UTC)
			state, err := HistoricalApply(state, started)
			if err != nil {
				t.Fatalf("start item: %v", err)
			}
			before := state.Clone()
			terminal := recordedForTest(state, test.event)
			terminal.OccurredAt = time.Date(2026, 8, 11, 0, 0, 3, 0, time.UTC)

			_, err = HistoricalApply(state, terminal)
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("Apply() error = %v, want code %q", err, CodeInvalidEvent)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatalf("Apply() mutated input: got %#v want %#v", state, before)
			}
		})
	}
}

func TestApplyTurnTerminalRejectsTimestampBeforeLatestItemEnd(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	started := recordedForTest(state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
	started.OccurredAt = time.Date(2026, 8, 11, 0, 0, 3, 0, time.UTC)
	state, err := HistoricalApply(state, started)
	if err != nil {
		t.Fatalf("start item: %v", err)
	}
	completed := recordedForTest(state, AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"})
	completed.OccurredAt = time.Date(2026, 8, 11, 0, 0, 5, 0, time.UTC)
	state, err = HistoricalApply(state, completed)
	if err != nil {
		t.Fatalf("complete item: %v", err)
	}
	before := state.Clone()
	terminal := recordedForTest(state, TurnCompleted{TurnID: "turn-1"})
	terminal.OccurredAt = time.Date(2026, 8, 11, 0, 0, 4, 0, time.UTC)

	_, err = HistoricalApply(state, terminal)
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeInvalidEvent)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Apply() mutated input: got %#v want %#v", state, before)
	}
}

func TestApplyAssistantMessageTerminalEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		event        Event
		status       ItemStatus
		terminalCode string
		message      string
	}{
		{name: "failed", event: AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: "safe display"}, status: ItemStatusFailed, terminalCode: "provider_error", message: "safe display"},
		{name: "interrupted", event: AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "caller_canceled", Message: ""}, status: ItemStatusInterrupted, terminalCode: "caller_canceled"},
		{name: "request abandoned", event: AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: InterruptionRequestAbandoned, Message: ""}, status: ItemStatusInterrupted, terminalCode: InterruptionRequestAbandoned},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := runningTurnForTest(t)
			var err error
			state, err = HistoricalApply(state, recordedForTest(state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}))
			if err != nil {
				t.Fatalf("start item: %v", err)
			}
			record := recordedForTest(state, test.event)
			state, err = HistoricalApply(state, record)
			if err != nil {
				t.Fatalf("terminal item: %v", err)
			}
			item := state.Turns["turn-1"].Items["item-1"]
			payload, ok := item.Payload.(AssistantMessagePayload)
			if !ok || payload.Text != "" || item.Status != test.status {
				t.Fatalf("item = %#v", item)
			}
			if item.Terminal == nil || item.Terminal.Code != test.terminalCode || item.Terminal.Message != test.message {
				t.Fatalf("terminal = %#v", item.Terminal)
			}
			if !item.EndedAt.Equal(record.OccurredAt) || state.Turns["turn-1"].ActiveItemID != "" {
				t.Fatalf("terminal lifecycle = %#v", item)
			}
		})
	}
}

func TestApplyAssistantMessageRejectsInvalidTransitionsWithoutMutation(t *testing.T) {
	t.Parallel()

	running := runningTurnForTest(t)
	withItem, err := HistoricalApply(running, recordedForTest(running, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}))
	if err != nil {
		t.Fatalf("start item fixture: %v", err)
	}
	completed, err := HistoricalApply(withItem, recordedForTest(withItem, AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}))
	if err != nil {
		t.Fatalf("complete item fixture: %v", err)
	}

	tests := []struct {
		name  string
		state HistoricalSession
		event Event
	}{
		{name: "second active item", state: withItem, event: AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-2"}},
		{name: "duplicate item ID", state: completed, event: AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}},
		{name: "terminal wrong item", state: withItem, event: AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-2", Text: "done"}},
		{name: "terminal twice", state: completed, event: AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: "safe"}},
		{name: "blank terminal code", state: withItem, event: AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: " ", Message: "safe"}},
		{name: "invalid terminal message", state: withItem, event: AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: "bad-\xff"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.state.Clone()
			_, err := HistoricalApply(test.state, recordedForTest(test.state, test.event))
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("Apply() error = %v, want code %q", err, CodeInvalidEvent)
			}
			if !reflect.DeepEqual(test.state, before) {
				t.Fatalf("Apply() mutated input: got %#v want %#v", test.state, before)
			}
		})
	}
}

func TestApplyAssistantMessageRejectsMalformedTurnPreState(t *testing.T) {
	t.Parallel()

	valid := runningTurnForTest(t)
	valid, err := HistoricalApply(valid, recordedForTest(valid, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}))
	if err != nil {
		t.Fatalf("start item fixture: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*HistoricalTurn)
	}{
		{name: "duplicate order", mutate: func(turn *HistoricalTurn) { turn.ItemOrder = append(turn.ItemOrder, "item-1") }},
		{name: "order missing map item", mutate: func(turn *HistoricalTurn) { turn.ItemOrder = nil }},
		{name: "map key mismatch", mutate: func(turn *HistoricalTurn) {
			item := turn.Items["item-1"]
			delete(turn.Items, "item-1")
			turn.Items["item-2"] = item
			turn.ItemOrder[0] = "item-2"
		}},
		{name: "wrong owner", mutate: func(turn *HistoricalTurn) {
			item := turn.Items["item-1"]
			item.TurnID = "turn-2"
			turn.Items["item-1"] = item
		}},
		{name: "turn identity mismatch", mutate: func(turn *HistoricalTurn) {
			turn.ID = "turn-2"
			item := turn.Items["item-1"]
			item.TurnID = "turn-2"
			turn.Items["item-1"] = item
		}},
		{name: "active ID missing", mutate: func(turn *HistoricalTurn) { turn.ActiveItemID = "" }},
		{name: "running ended", mutate: func(turn *HistoricalTurn) {
			item := turn.Items["item-1"]
			item.EndedAt = time.Now()
			turn.Items["item-1"] = item
		}},
		{name: "running terminal", mutate: func(turn *HistoricalTurn) {
			item := turn.Items["item-1"]
			item.Terminal = &ItemTerminal{Code: "bad"}
			turn.Items["item-1"] = item
		}},
		{name: "payload kind mismatch", mutate: func(turn *HistoricalTurn) {
			item := turn.Items["item-1"]
			item.Kind = ItemKind("unknown")
			turn.Items["item-1"] = item
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := valid.Clone()
			turn := state.Turns["turn-1"]
			test.mutate(&turn)
			state.Turns["turn-1"] = turn
			before := state.Clone()
			_, err := HistoricalApply(state, recordedForTest(state, AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}))
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("Apply() error = %v, want code %q", err, CodeInvalidEvent)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatalf("Apply() mutated malformed input: got %#v want %#v", state, before)
			}
		})
	}
}

func TestApplyTerminalTurnRejectsActiveOrMalformedItem(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	state, err := HistoricalApply(state, recordedForTest(state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}))
	if err != nil {
		t.Fatalf("start item: %v", err)
	}
	before := state.Clone()
	_, err = HistoricalApply(state, recordedForTest(state, TurnCompleted{TurnID: "turn-1"}))
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeInvalidEvent)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Apply() mutated input: got %#v want %#v", state, before)
	}
}

func TestSessionCloneDeepCopiesTurnItems(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	state, err := HistoricalApply(state, recordedForTest(state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}))
	if err != nil {
		t.Fatalf("start item: %v", err)
	}
	state, err = HistoricalApply(state, recordedForTest(state, AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: "safe"}))
	if err != nil {
		t.Fatalf("fail item: %v", err)
	}
	before := state.Clone()
	clone := state.Clone()
	turn := clone.Turns["turn-1"]
	turn.ItemOrder[0] = "changed"
	item := turn.Items["item-1"]
	item.Payload = AssistantMessagePayload{Text: "changed"}
	item.Terminal.Code = "changed"
	turn.Items["item-1"] = item
	clone.Turns["turn-1"] = turn
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("mutating clone changed source: got %#v want %#v", state, before)
	}
}

type mutableUnknownEvent struct{ Values []string }

func (mutableUnknownEvent) EventType() string { return "test.mutable" }

func TestApplyModelFactsAreVersionOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event Event
	}{
		{name: "request", event: validModelRequestRecorded("turn-1", "item-1", "inspect repository")},
		{name: "usage", event: validModelUsageRecorded("turn-1", "item-1")},
		{name: "usage zeros", event: ModelUsageRecorded{TurnID: "turn-1", ItemID: "item-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := runningAssistantItemForTest(t)
			before := state.Clone()
			record := recordedForTest(state, test.event)
			record.OccurredAt = state.Turns["turn-1"].Items["item-1"].StartedAt
			got, err := HistoricalApply(state, record)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if got.Version != record.Sequence {
				t.Fatalf("version = %d, want %d", got.Version, record.Sequence)
			}
			got.Version = before.Version
			if !reflect.DeepEqual(got, before) {
				t.Fatalf("version-only apply changed historical items: got %#v want %#v", got, before)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatalf("Apply() mutated input: got %#v want %#v", state, before)
			}
		})
	}
}

func TestApplyModelFactsRejectInvalidTransitions(t *testing.T) {
	t.Parallel()

	runningItem := runningAssistantItemForTest(t)
	idle := activeSessionForTest(t)
	completed := terminalAssistantItemForTest(t)
	tests := []struct {
		name  string
		state HistoricalSession
		event Event
	}{
		{name: "request before item start", state: runningItem, event: validModelRequestRecorded("turn-1", "item-1", "inspect repository")},
		{name: "usage after item terminal", state: completed, event: validModelUsageRecorded("turn-1", "item-1")},
		{name: "request without item", state: runningTurnForTest(t), event: validModelRequestRecorded("turn-1", "item-1", "inspect repository")},
		{name: "request idle session", state: idle, event: validModelRequestRecorded("turn-1", "item-1", "inspect repository")},
		{name: "usage wrong item", state: runningItem, event: validModelUsageRecorded("turn-1", "item-2")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := recordedForTest(test.state, test.event)
			if test.name == "request before item start" {
				record.OccurredAt = test.state.Turns["turn-1"].Items["item-1"].StartedAt.Add(-time.Second)
			}
			before := test.state.Clone()
			_, err := HistoricalApply(test.state, record)
			if !IsCode(err, CodeInvalidEvent) && !IsCode(err, CodeTurnNotRunning) {
				t.Fatalf("Apply() error = %v, want invalid event or turn not running", err)
			}
			if !reflect.DeepEqual(test.state, before) {
				t.Fatalf("Apply() mutated input: got %#v want %#v", test.state, before)
			}
		})
	}
}

func TestApplyToolCallLifecycle(t *testing.T) {
	t.Parallel()

	state := runningTurnForTest(t)
	started := recordedForTest(state, validToolCallStarted("turn-1", "item-tool"))
	state, err := HistoricalApply(state, started)
	if err != nil {
		t.Fatalf("start tool: %v", err)
	}
	if item := state.Turns["turn-1"].Items["item-tool"]; item.Kind != ItemKindToolCall || item.Status != ItemStatusRunning {
		t.Fatalf("started item = %#v", item)
	}

	policy := recordedForTest(state, PolicyDecisionRecorded{
		TurnID: "turn-1", ItemID: "item-tool", CallID: "call-1",
		Name: "read_file", Effect: PolicyEffectAllow, RuleID: "default.read", Reason: "in_workspace",
	})
	before := state.Clone()
	state, err = HistoricalApply(state, policy)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	state.Version = before.Version
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("policy apply changed historical items: got %#v want %#v", state, before)
	}
	state.Version = policy.Sequence

	completed := recordedForTest(state, ToolCallCompleted{
		TurnID: "turn-1", ItemID: "item-tool", CallID: "call-1", Content: "file body", Truncated: false,
	})
	state, err = HistoricalApply(state, completed)
	if err != nil {
		t.Fatalf("complete tool: %v", err)
	}
	item := state.Turns["turn-1"].Items["item-tool"]
	payload, ok := item.Payload.(ToolCallPayload)
	if !ok || item.Status != ItemStatusCompleted || payload.Content != "file body" || state.Turns["turn-1"].ActiveItemID != "" {
		t.Fatalf("completed item = %#v", item)
	}
	if state.ActiveTurnID != "turn-1" {
		t.Fatalf("turn should stay running, got %#v", state)
	}
}

func TestApplyAssistantMessageCompletedWithToolCallsLeavesTurnRunning(t *testing.T) {
	t.Parallel()

	state := runningAssistantItemForTest(t)
	record := recordedForTest(state, AssistantMessageCompleted{
		TurnID: "turn-1", ItemID: "item-1", Text: "calling", ToolCalls: []ToolCallOffer{validToolCallOffer()},
	})
	state, err := HistoricalApply(state, record)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	item := state.Turns["turn-1"].Items["item-1"]
	payload := item.Payload.(AssistantMessagePayload)
	if item.Status != ItemStatusCompleted || payload.Text != "calling" || len(payload.ToolCalls) != 1 {
		t.Fatalf("item = %#v payload = %#v", item, payload)
	}
	if state.ActiveTurnID != "turn-1" || state.Turns["turn-1"].ActiveItemID != "" {
		t.Fatalf("turn/item after tool-bearing complete = %#v", state.Turns["turn-1"])
	}
}

func TestApplyToolTerminals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		event  Event
		status ItemStatus
		code   string
	}{
		{name: "failed", event: ToolCallFailed{TurnID: "turn-1", ItemID: "item-tool", CallID: "call-1", Code: "policy_denied", Message: "policy denied this tool"}, status: ItemStatusFailed, code: "policy_denied"},
		{name: "interrupted", event: ToolCallInterrupted{TurnID: "turn-1", ItemID: "item-tool", CallID: "call-1", Code: InterruptionCallerCanceled, Message: ""}, status: ItemStatusInterrupted, code: InterruptionCallerCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := runningToolItemForTest(t)
			record := recordedForTest(state, test.event)
			state, err := HistoricalApply(state, record)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			item := state.Turns["turn-1"].Items["item-tool"]
			if item.Status != test.status || item.Terminal == nil || item.Terminal.Code != test.code {
				t.Fatalf("item = %#v", item)
			}
		})
	}
}

func TestCloneRecordedEventsDeepCopiesEventsAndRejectsUnknownTypes(t *testing.T) {
	t.Parallel()

	request := validModelRequestRecorded("turn-1", "item-1", "original")
	request.Tools = []ToolSchema{{Name: "read_file", Description: "read", InputSchema: []byte(`{"type":"object"}`)}}
	request.Messages = []ModelPromptMessage{{
		Role: PromptRoleAssistant, Text: "original",
		ToolCalls: []ToolCallOffer{{ID: "call-1", Name: "read_file", Arguments: "{}"}},
	}}
	completed := AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "original", ToolCalls: []ToolCallOffer{{ID: "call-1", Name: "read_file", Arguments: "{}"}}}
	records := []RecordedEvent{
		{Event: completed},
		{Event: request},
		{Event: validModelUsageRecorded("turn-1", "item-1")},
		{Event: validToolCallStarted("turn-1", "item-tool")},
	}
	cloned, err := CloneRecordedEvents(records)
	if err != nil {
		t.Fatalf("CloneRecordedEvents() error = %v", err)
	}
	cloned[0].Event.(AssistantMessageCompleted).ToolCalls[0].Name = "changed"
	cloned[1].Event.(ModelRequestRecorded).Messages[0].ToolCalls[0].Name = "changed"
	cloned[1].Event.(ModelRequestRecorded).Tools[0].Name = "changed"
	if records[0].Event.(AssistantMessageCompleted).ToolCalls[0].Name != "read_file" {
		t.Fatalf("mutating cloned toolCalls changed source = %#v", records)
	}
	if records[1].Event.(ModelRequestRecorded).Messages[0].ToolCalls[0].Name != "read_file" {
		t.Fatalf("mutating cloned request toolCalls changed source = %#v", records[1])
	}
	if records[1].Event.(ModelRequestRecorded).Tools[0].Name != "read_file" {
		t.Fatalf("mutating cloned tools changed source = %#v", records[1])
	}

	_, err = CloneRecordedEvents([]RecordedEvent{{Event: mutableUnknownEvent{Values: []string{"mutable"}}}})
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("CloneRecordedEvents() unknown event error = %v, want code %q", err, CodeInvalidEvent)
	}
}
