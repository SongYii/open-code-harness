package domain

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecordedEventJSONRoundTrip(t *testing.T) {
	t.Parallel()

	record := RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID("event-1"),
		CommandID:     CommandID("command-1"),
		SessionID:     SessionID("session-1"),
		Sequence:      1,
		OccurredAt:    time.Date(2026, 8, 11, 1, 2, 3, 456000000, time.FixedZone("offset", 8*60*60)),
		Event:         SessionCreated{WorkspaceRoot: "/workspace"},
	}

	encoded, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatalf("MarshalRecordedEvent() error = %v", err)
	}
	want := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`
	if string(encoded) != want {
		t.Fatalf("encoded = %s\nwant = %s", encoded, want)
	}

	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil {
		t.Fatalf("UnmarshalRecordedEvent() error = %v", err)
	}
	record.OccurredAt = record.OccurredAt.UTC()
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestMarshalEventPayloadIsCanonical(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		event     Event
		wantType  string
		wantBytes string
	}{
		{"session created", SessionCreated{WorkspaceRoot: "/workspace"}, EventSessionCreated, `{"workspaceRoot":"/workspace"}`},
		{"turn started", TurnStarted{TurnID: "turn-1", Input: "inspect"}, EventTurnStarted, `{"turnID":"turn-1","input":"inspect"}`},
		{"turn completed", TurnCompleted{TurnID: "turn-1"}, EventTurnCompleted, `{"turnID":"turn-1"}`},
		{"turn failed", TurnFailed{TurnID: "turn-1", Code: "provider_error", Message: "failed"}, EventTurnFailed, `{"turnID":"turn-1","code":"provider_error","message":"failed"}`},
		{"turn interrupted", TurnInterrupted{TurnID: "turn-1", Reason: "canceled"}, EventTurnInterrupted, `{"turnID":"turn-1","reason":"canceled"}`},
		{"session closed", SessionClosed{}, EventSessionClosed, `{}`},
		{"assistant started", AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}, EventAssistantMessageStarted, `{"turnID":"turn-1","itemID":"item-1"}`},
		{"assistant completed", AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "你好"}, EventAssistantMessageCompleted, `{"turnID":"turn-1","itemID":"item-1","text":"你好"}`},
		{"assistant failed", AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: "failed"}, EventAssistantMessageFailed, `{"turnID":"turn-1","itemID":"item-1","code":"provider_error","message":"failed"}`},
		{"assistant interrupted", AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "canceled", Message: ""}, EventAssistantMessageInterrupted, `{"turnID":"turn-1","itemID":"item-1","code":"canceled","message":""}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			typeName, payload, err := MarshalEventPayload(test.event)
			if err != nil {
				t.Fatalf("MarshalEventPayload() error = %v", err)
			}
			if typeName != test.wantType {
				t.Fatalf("type = %q, want %q", typeName, test.wantType)
			}
			if string(payload) != test.wantBytes {
				t.Fatalf("payload = %s, want %s", payload, test.wantBytes)
			}
			payload[0] = 'X'
			_, repeated, err := MarshalEventPayload(test.event)
			if err != nil || string(repeated) != test.wantBytes {
				t.Fatalf("MarshalEventPayload() returned non-defensive payload = %q, error = %v", repeated, err)
			}
		})
	}
}

func TestMarshalEventPayloadRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()

	invalid := string([]byte{0xff})
	tests := []struct {
		name  string
		event Event
	}{
		{"session created workspace root", SessionCreated{WorkspaceRoot: invalid}},
		{"turn started turn ID", TurnStarted{TurnID: TurnID(invalid), Input: "input"}},
		{"turn started input", TurnStarted{TurnID: "turn-1", Input: invalid}},
		{"turn completed turn ID", TurnCompleted{TurnID: TurnID(invalid)}},
		{"turn failed turn ID", TurnFailed{TurnID: TurnID(invalid), Code: "code", Message: "message"}},
		{"turn failed code", TurnFailed{TurnID: "turn-1", Code: invalid, Message: "message"}},
		{"turn failed message", TurnFailed{TurnID: "turn-1", Code: "code", Message: invalid}},
		{"turn interrupted turn ID", TurnInterrupted{TurnID: TurnID(invalid), Reason: "reason"}},
		{"turn interrupted reason", TurnInterrupted{TurnID: "turn-1", Reason: invalid}},
		{"assistant started turn ID", AssistantMessageStarted{TurnID: TurnID(invalid), ItemID: "item-1"}},
		{"assistant started item ID", AssistantMessageStarted{TurnID: "turn-1", ItemID: ItemID(invalid)}},
		{"assistant completed turn ID", AssistantMessageCompleted{TurnID: TurnID(invalid), ItemID: "item-1", Text: "text"}},
		{"assistant completed item ID", AssistantMessageCompleted{TurnID: "turn-1", ItemID: ItemID(invalid), Text: "text"}},
		{"assistant completed text", AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: invalid}},
		{"assistant failed turn ID", AssistantMessageFailed{TurnID: TurnID(invalid), ItemID: "item-1", Code: "code", Message: "message"}},
		{"assistant failed item ID", AssistantMessageFailed{TurnID: "turn-1", ItemID: ItemID(invalid), Code: "code", Message: "message"}},
		{"assistant failed code", AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: invalid, Message: "message"}},
		{"assistant failed message", AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "code", Message: invalid}},
		{"assistant interrupted turn ID", AssistantMessageInterrupted{TurnID: TurnID(invalid), ItemID: "item-1", Code: "code", Message: "message"}},
		{"assistant interrupted item ID", AssistantMessageInterrupted{TurnID: "turn-1", ItemID: ItemID(invalid), Code: "code", Message: "message"}},
		{"assistant interrupted code", AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: invalid, Message: "message"}},
		{"assistant interrupted message", AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "code", Message: invalid}},
		// SessionClosed has no text or identifier field to make invalid.
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := MarshalEventPayload(test.event)
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("MarshalEventPayload() error = %v, want code %q", err, CodeInvalidEvent)
			}
		})
	}
}

func TestUnmarshalRecordedEventRejectsInvalidWire(t *testing.T) {
	t.Parallel()

	valid := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "unknown top-level field",
			input: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"},"extra":true}`,
		},
		{
			name:  "unknown payload field",
			input: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace","extra":true}}`,
		},
		{
			name:  "unknown schema version",
			input: `{"schemaVersion":2,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`,
		},
		{
			name:  "unknown event type",
			input: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.unknown","data":{}}`,
		},
		{
			name:  "zero sequence",
			input: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":0,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`,
		},
		{
			name:  "padded ID",
			input: `{"schemaVersion":1,"id":" event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`,
		},
		{
			name:  "invalid timestamp",
			input: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"not-a-timestamp","type":"session.created","data":{"workspaceRoot":"/workspace"}}`,
		},
		{
			name:  "trailing JSON value",
			input: valid + ` {}`,
		},
		{
			name:  "non-object payload",
			input: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.closed","data":null}`,
		},
		{
			name:  "missing required ID",
			input: `{"schemaVersion":1,"commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`,
		},
		{
			name:  "missing payload",
			input: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created"}`,
		},
		{
			name:  "envelope type mismatch",
			input: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":"1","occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`,
		},
		{
			name:  "payload type mismatch",
			input: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"turn.started","data":{"turnID":"turn-1","input":1}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := UnmarshalRecordedEvent([]byte(test.input))
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("UnmarshalRecordedEvent() error = %v, want code %q", err, CodeInvalidEvent)
			}
		})
	}
}

func TestUnmarshalRecordedEventRejectsNonStrictJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
	}{
		{
			name:  "case-variant envelope key",
			input: []byte(`{"SchemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`),
		},
		{
			name:  "duplicate envelope key",
			input: []byte(`{"schemaVersion":1,"id":"event-1","id":"event-2","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`),
		},
		{
			name:  "case-variant session-created payload key",
			input: []byte(`{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"WorkspaceRoot":"/workspace"}}`),
		},
		{
			name:  "case-variant turn-started payload key",
			input: []byte(`{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"turn.started","data":{"turnId":"turn-1","input":"inspect"}}`),
		},
		{
			name:  "duplicate session-created payload key",
			input: []byte(`{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace","workspaceRoot":"/other"}}`),
		},
		{
			name:  "duplicate turn-started payload key",
			input: []byte(`{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"turn.started","data":{"turnID":"turn-1","input":"inspect","input":"replace"}}`),
		},
		{
			name: "invalid raw UTF-8 in metadata",
			input: invalidUTF8JSON(
				`{"schemaVersion":1,"id":"event-`,
				`","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`,
			),
		},
		{
			name: "invalid raw UTF-8 in payload",
			input: invalidUTF8JSON(
				`{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/work`,
				`space"}}`,
			),
		},
		{
			name:  "lone high surrogate in metadata",
			input: []byte(`{"schemaVersion":1,"id":"event-\ud800","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`),
		},
		{
			name:  "lone low surrogate in metadata",
			input: []byte(`{"schemaVersion":1,"id":"event-\udc00","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`),
		},
		{
			name:  "mispaired surrogate in payload",
			input: []byte(`{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/work\ud800\u0041space"}}`),
		},
		{
			name:  "lone surrogate in representative turn payload",
			input: []byte(`{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"turn.started","data":{"turnID":"turn-1","input":"inspect\ud800"}}`),
		},
		{
			name:  "comma fractional timestamp",
			input: []byte(`{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03,456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := UnmarshalRecordedEvent(test.input)
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("UnmarshalRecordedEvent() error = %v, want code %q", err, CodeInvalidEvent)
			}
		})
	}
}

func TestUnmarshalRecordedEventAcceptsRFC3339NanoTimestampForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		timestamp string
		wantUTC   string
	}{
		{name: "whole seconds UTC", timestamp: "2026-08-11T01:02:03Z", wantUTC: "2026-08-11T01:02:03Z"},
		{name: "fraction and positive offset", timestamp: "2026-08-11T01:02:03.1+08:00", wantUTC: "2026-08-10T17:02:03.1Z"},
		{name: "nanoseconds and negative offset", timestamp: "2026-08-11T01:02:03.123456789-07:30", wantUTC: "2026-08-11T08:32:03.123456789Z"},
		{name: "maximum positive offset", timestamp: "2026-08-11T01:02:03+23:59", wantUTC: "2026-08-10T01:03:03Z"},
		{name: "maximum negative offset", timestamp: "2026-08-11T01:02:03-23:59", wantUTC: "2026-08-12T01:01:03Z"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"` + test.timestamp + `","type":"session.created","data":{"workspaceRoot":"/workspace"}}`
			record, err := UnmarshalRecordedEvent([]byte(input))
			if err != nil {
				t.Fatalf("UnmarshalRecordedEvent() error = %v", err)
			}
			if got := record.OccurredAt.Format(time.RFC3339Nano); got != test.wantUTC {
				t.Fatalf("OccurredAt = %q, want %q", got, test.wantUTC)
			}
		})
	}
}

func TestUnmarshalRecordedEventRejectsRFC3339OffsetOutsideBounds(t *testing.T) {
	t.Parallel()

	for _, offset := range []string{"+24:00", "-24:00", "+00:60", "-00:60", "+23:60", "-23:60"} {
		t.Run(offset, func(t *testing.T) {
			input := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03` + offset + `","type":"session.created","data":{"workspaceRoot":"/workspace"}}`
			_, err := UnmarshalRecordedEvent([]byte(input))
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("UnmarshalRecordedEvent() offset %q error = %v, want code %q", offset, err, CodeInvalidEvent)
			}
		})
	}
}

func invalidUTF8JSON(prefix, suffix string) []byte {
	data := append([]byte(prefix), 0xff)
	return append(data, suffix...)
}

func TestSessionLifecycleFixtureUsesCanonicalCodecRecords(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/session_lifecycle.jsonl")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 6 {
		t.Fatalf("fixture line count = %d, want 6", len(lines))
	}
	want := []RecordedEvent{
		fixtureRecord(1, SessionCreated{WorkspaceRoot: "/workspace"}),
		fixtureRecord(2, TurnStarted{TurnID: "turn-1", Input: "first turn"}),
		fixtureRecord(3, TurnCompleted{TurnID: "turn-1"}),
		fixtureRecord(4, TurnStarted{TurnID: "turn-2", Input: "second turn"}),
		fixtureRecord(5, TurnInterrupted{TurnID: "turn-2", Reason: "user_cancelled"}),
		fixtureRecord(6, SessionClosed{}),
	}
	for index, line := range lines {
		record, err := UnmarshalRecordedEvent([]byte(line))
		if err != nil {
			t.Fatalf("fixture line %d error = %v", index+1, err)
		}
		if !reflect.DeepEqual(record, want[index]) {
			t.Fatalf("fixture line %d record = %#v, want %#v", index+1, record, want[index])
		}
		encoded, err := MarshalRecordedEvent(record)
		if err != nil {
			t.Fatalf("fixture line %d re-marshal error = %v", index+1, err)
		}
		if string(encoded) != line {
			t.Fatalf("fixture line %d re-marshal = %s, want exact %s", index+1, encoded, line)
		}
	}

	state, err := HistoricalReplay(want)
	if err != nil {
		t.Fatalf("Replay() fixture error = %v", err)
	}
	if state.Status != SessionStatusClosed || state.Version != 6 || state.ActiveTurnID != "" || len(state.Turns) != 2 {
		t.Fatalf("Replay() fixture state = %#v", state)
	}
}

func TestRecordedEventJSONUsesCanonicalEncodingForAllPayloads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record RecordedEvent
		want   string
	}{
		{
			name:   "session created",
			record: canonicalCodecRecord(1, SessionCreated{WorkspaceRoot: "/workspace"}),
			want:   `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`,
		},
		{
			name:   "turn started",
			record: canonicalCodecRecord(2, TurnStarted{TurnID: "turn-1", Input: "hello"}),
			want:   `{"schemaVersion":1,"id":"event-2","commandId":"command-2","sessionId":"session-1","sequence":2,"occurredAt":"2026-08-11T01:02:03Z","type":"turn.started","data":{"turnID":"turn-1","input":"hello"}}`,
		},
		{
			name:   "turn completed",
			record: canonicalCodecRecord(3, TurnCompleted{TurnID: "turn-1"}),
			want:   `{"schemaVersion":1,"id":"event-3","commandId":"command-3","sessionId":"session-1","sequence":3,"occurredAt":"2026-08-11T01:02:03Z","type":"turn.completed","data":{"turnID":"turn-1"}}`,
		},
		{
			name:   "turn failed",
			record: canonicalCodecRecord(4, TurnFailed{TurnID: "turn-1", Code: "provider_error", Message: "provider failed"}),
			want:   `{"schemaVersion":1,"id":"event-4","commandId":"command-4","sessionId":"session-1","sequence":4,"occurredAt":"2026-08-11T01:02:03Z","type":"turn.failed","data":{"turnID":"turn-1","code":"provider_error","message":"provider failed"}}`,
		},
		{
			name:   "turn interrupted",
			record: canonicalCodecRecord(5, TurnInterrupted{TurnID: "turn-1", Reason: "user_cancelled"}),
			want:   `{"schemaVersion":1,"id":"event-5","commandId":"command-5","sessionId":"session-1","sequence":5,"occurredAt":"2026-08-11T01:02:03Z","type":"turn.interrupted","data":{"turnID":"turn-1","reason":"user_cancelled"}}`,
		},
		{
			name:   "session closed",
			record: canonicalCodecRecord(6, SessionClosed{}),
			want:   `{"schemaVersion":1,"id":"event-6","commandId":"command-6","sessionId":"session-1","sequence":6,"occurredAt":"2026-08-11T01:02:03Z","type":"session.closed","data":{}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := MarshalRecordedEvent(test.record)
			if err != nil {
				t.Fatalf("MarshalRecordedEvent() error = %v", err)
			}
			if string(encoded) != test.want {
				t.Fatalf("encoded = %s\\nwant = %s", encoded, test.want)
			}
			decoded, err := UnmarshalRecordedEvent(encoded)
			if err != nil {
				t.Fatalf("UnmarshalRecordedEvent() error = %v", err)
			}
			if !reflect.DeepEqual(decoded, test.record) {
				t.Fatalf("decoded = %#v, want %#v", decoded, test.record)
			}
		})
	}
}

func canonicalCodecRecord(sequence uint64, event Event) RecordedEvent {
	return RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID("event-" + strconv.FormatUint(sequence, 10)),
		CommandID:     CommandID("command-" + strconv.FormatUint(sequence, 10)),
		SessionID:     "session-1",
		Sequence:      sequence,
		OccurredAt:    time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
		Event:         event,
	}
}

func fixtureRecord(sequence uint64, event Event) RecordedEvent {
	return RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID("event-" + strconv.FormatUint(sequence, 10)),
		CommandID:     CommandID("command-" + strconv.FormatUint(sequence, 10)),
		SessionID:     "session-fixture",
		Sequence:      sequence,
		OccurredAt:    time.Date(2026, 8, 11, 1, 2, int(sequence)+2, 0, time.UTC),
		Event:         event,
	}
}

func TestMarshalRecordedEventRejectsTimestampOutsideRFC3339YearRange(t *testing.T) {
	t.Parallel()

	record := codecTestRecord(SessionCreated{WorkspaceRoot: "/workspace"})
	record.OccurredAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err := MarshalRecordedEvent(record)
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("MarshalRecordedEvent() error = %v, want code %q", err, CodeInvalidEvent)
	}
}

func TestMarshalRecordedEventRejectsInvalidUTF8MetadataIDs(t *testing.T) {
	t.Parallel()

	invalid := "identifier-\xff"
	tests := []struct {
		name   string
		mutate func(*RecordedEvent)
	}{
		{name: "event ID", mutate: func(record *RecordedEvent) { record.ID = EventID(invalid) }},
		{name: "session ID", mutate: func(record *RecordedEvent) { record.SessionID = SessionID(invalid) }},
		{name: "command ID", mutate: func(record *RecordedEvent) { record.CommandID = CommandID(invalid) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := codecTestRecord(SessionCreated{WorkspaceRoot: "/workspace"})
			test.mutate(&record)
			encoded, err := MarshalRecordedEvent(record)
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("MarshalRecordedEvent() = %q, error = %v, want code %q", encoded, err, CodeInvalidEvent)
			}
		})
	}
}

func TestMarshalRecordedEventRejectsInvalidUTF8EventPayloads(t *testing.T) {
	t.Parallel()

	invalid := "value-\xff"
	for _, test := range []struct {
		name  string
		event Event
	}{
		{name: "workspace root", event: SessionCreated{WorkspaceRoot: invalid}},
		{name: "turn input", event: TurnStarted{TurnID: "turn-1", Input: invalid}},
		{name: "failure code", event: TurnFailed{TurnID: "turn-1", Code: invalid, Message: "provider failed"}},
		{name: "failure message", event: TurnFailed{TurnID: "turn-1", Code: "provider_error", Message: invalid}},
		{name: "interruption reason", event: TurnInterrupted{TurnID: "turn-1", Reason: invalid}},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := MarshalRecordedEvent(codecTestRecord(test.event))
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("MarshalRecordedEvent() = %q, error = %v, want code %q", encoded, err, CodeInvalidEvent)
			}
		})
	}
}

func TestRecordedEventRejectsWhitespaceOnlyTurnInput(t *testing.T) {
	t.Parallel()

	record := codecTestRecord(TurnStarted{TurnID: "turn-1", Input: " \t "})
	if _, err := MarshalRecordedEvent(record); !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("MarshalRecordedEvent() error = %v, want code %q", err, CodeInvalidEvent)
	}

	input := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"turn.started","data":{"turnID":"turn-1","input":" \t "}}`
	if _, err := UnmarshalRecordedEvent([]byte(input)); !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("UnmarshalRecordedEvent() error = %v, want code %q", err, CodeInvalidEvent)
	}
}

func TestAssistantMessageEventJSONRoundTripsCanonically(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{name: "started", event: AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}, want: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"assistant.message.started","data":{"turnID":"turn-1","itemID":"item-1"}}`},
		{name: "completed empty text", event: AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: ""}, want: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"assistant.message.completed","data":{"turnID":"turn-1","itemID":"item-1","text":""}}`},
		{name: "failed", event: AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: "safe"}, want: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"assistant.message.failed","data":{"turnID":"turn-1","itemID":"item-1","code":"provider_error","message":"safe"}}`},
		{name: "interrupted empty display", event: AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "caller_canceled", Message: ""}, want: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"assistant.message.interrupted","data":{"turnID":"turn-1","itemID":"item-1","code":"caller_canceled","message":""}}`},
		{name: "request abandoned", event: AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: InterruptionRequestAbandoned, Message: ""}, want: `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"assistant.message.interrupted","data":{"turnID":"turn-1","itemID":"item-1","code":"request_abandoned","message":""}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := codecTestRecord(test.event)
			encoded, err := MarshalRecordedEvent(record)
			if err != nil {
				t.Fatalf("MarshalRecordedEvent() error = %v", err)
			}
			if string(encoded) != test.want {
				t.Fatalf("encoded = %s\nwant = %s", encoded, test.want)
			}
			decoded, err := UnmarshalRecordedEvent(encoded)
			if err != nil {
				t.Fatalf("UnmarshalRecordedEvent() error = %v", err)
			}
			if !reflect.DeepEqual(decoded, record) {
				t.Fatalf("decoded = %#v, want %#v", decoded, record)
			}
		})
	}
}

func TestAssistantMessageEventJSONRejectsNonStrictPayloads(t *testing.T) {
	t.Parallel()

	prefix := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"`
	tests := []struct {
		name      string
		eventType string
		data      string
	}{
		{name: "started unknown", eventType: EventAssistantMessageStarted, data: `{"turnID":"turn-1","itemID":"item-1","extra":true}`},
		{name: "started missing", eventType: EventAssistantMessageStarted, data: `{"turnID":"turn-1"}`},
		{name: "started duplicate", eventType: EventAssistantMessageStarted, data: `{"turnID":"turn-1","itemID":"item-1","itemID":"item-2"}`},
		{name: "started wrong type", eventType: EventAssistantMessageStarted, data: `{"turnID":"turn-1","itemID":1}`},
		{name: "completed unknown", eventType: EventAssistantMessageCompleted, data: `{"turnID":"turn-1","itemID":"item-1","text":"","extra":true}`},
		{name: "completed missing", eventType: EventAssistantMessageCompleted, data: `{"turnID":"turn-1","itemID":"item-1"}`},
		{name: "completed duplicate", eventType: EventAssistantMessageCompleted, data: `{"turnID":"turn-1","itemID":"item-1","text":"a","text":"b"}`},
		{name: "completed wrong type", eventType: EventAssistantMessageCompleted, data: `{"turnID":"turn-1","itemID":"item-1","text":1}`},
		{name: "failed unknown", eventType: EventAssistantMessageFailed, data: `{"turnID":"turn-1","itemID":"item-1","code":"provider_error","message":"","extra":true}`},
		{name: "failed missing", eventType: EventAssistantMessageFailed, data: `{"turnID":"turn-1","itemID":"item-1","code":"provider_error"}`},
		{name: "failed duplicate", eventType: EventAssistantMessageFailed, data: `{"turnID":"turn-1","itemID":"item-1","code":"provider_error","code":"other","message":""}`},
		{name: "failed wrong type", eventType: EventAssistantMessageFailed, data: `{"turnID":"turn-1","itemID":"item-1","code":"provider_error","message":1}`},
		{name: "interrupted unknown", eventType: EventAssistantMessageInterrupted, data: `{"turnID":"turn-1","itemID":"item-1","code":"caller_canceled","message":"","extra":true}`},
		{name: "interrupted missing", eventType: EventAssistantMessageInterrupted, data: `{"turnID":"turn-1","itemID":"item-1","code":"caller_canceled"}`},
		{name: "interrupted duplicate", eventType: EventAssistantMessageInterrupted, data: `{"turnID":"turn-1","itemID":"item-1","code":"caller_canceled","message":"","message":"other"}`},
		{name: "interrupted wrong type", eventType: EventAssistantMessageInterrupted, data: `{"turnID":"turn-1","itemID":"item-1","code":1,"message":""}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := prefix + test.eventType + `","data":` + test.data + `}`
			_, err := UnmarshalRecordedEvent([]byte(input))
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("UnmarshalRecordedEvent() error = %v, want code %q", err, CodeInvalidEvent)
			}
		})
	}
}

func TestAssistantMessageEventJSONRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	invalid := "value-\xff"
	tests := []Event{
		AssistantMessageStarted{TurnID: "turn-1", ItemID: ItemID(invalid)},
		AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: invalid},
		AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: " ", Message: ""},
		AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: invalid},
		AssistantMessageInterrupted{TurnID: "turn-1", ItemID: " item-1", Code: "caller_canceled", Message: ""},
		AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: invalid, Message: ""},
	}

	for _, event := range tests {
		_, err := MarshalRecordedEvent(codecTestRecord(event))
		if !IsCode(err, CodeInvalidEvent) {
			t.Fatalf("MarshalRecordedEvent(%T) error = %v, want code %q", event, err, CodeInvalidEvent)
		}
	}
}

func codecTestRecord(event Event) RecordedEvent {
	return RecordedEvent{
		SchemaVersion: 1,
		ID:            "event-1",
		CommandID:     "command-1",
		SessionID:     "session-1",
		Sequence:      1,
		OccurredAt:    time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
		Event:         event,
	}
}
