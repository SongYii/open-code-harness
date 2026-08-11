package domain

import (
	"os"
	"reflect"
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
	wantTypes := []string{
		EventSessionCreated,
		EventTurnStarted,
		EventTurnCompleted,
		EventTurnStarted,
		EventTurnInterrupted,
		EventSessionClosed,
	}
	for index, line := range lines {
		record, err := UnmarshalRecordedEvent([]byte(line))
		if err != nil {
			t.Fatalf("fixture line %d error = %v", index+1, err)
		}
		if record.Sequence != uint64(index+1) || record.Event.EventType() != wantTypes[index] {
			t.Fatalf("fixture line %d record = %#v", index+1, record)
		}
	}
}

func TestRecordedEventJSONRoundTripsAllEventPayloads(t *testing.T) {
	t.Parallel()

	events := []Event{
		SessionCreated{WorkspaceRoot: "/workspace"},
		TurnStarted{TurnID: "turn-1", Input: "hello"},
		TurnCompleted{TurnID: "turn-1"},
		TurnFailed{TurnID: "turn-1", Code: "provider_error", Message: "provider failed"},
		TurnInterrupted{TurnID: "turn-1", Reason: "user_cancelled"},
		SessionClosed{},
	}
	for index, event := range events {
		record := RecordedEvent{
			SchemaVersion: 1,
			ID:            EventID("event-" + string(rune('1'+index))),
			CommandID:     CommandID("command-" + string(rune('1'+index))),
			SessionID:     "session-1",
			Sequence:      uint64(index + 1),
			OccurredAt:    time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
			Event:         event,
		}
		encoded, err := MarshalRecordedEvent(record)
		if err != nil {
			t.Fatalf("MarshalRecordedEvent(%T) error = %v", event, err)
		}
		decoded, err := UnmarshalRecordedEvent(encoded)
		if err != nil {
			t.Fatalf("UnmarshalRecordedEvent(%T) error = %v", event, err)
		}
		if !reflect.DeepEqual(decoded, record) {
			t.Fatalf("round trip %T = %#v, want %#v", event, decoded, record)
		}
	}
}
