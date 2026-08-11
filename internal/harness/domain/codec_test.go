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
