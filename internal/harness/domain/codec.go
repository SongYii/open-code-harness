package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
)

type recordedEventWire struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            EventID         `json:"id"`
	CommandID     CommandID       `json:"commandId"`
	SessionID     SessionID       `json:"sessionId"`
	Sequence      uint64          `json:"sequence"`
	OccurredAt    string          `json:"occurredAt"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data"`
}

func MarshalRecordedEvent(record RecordedEvent) ([]byte, error) {
	if err := validateRecordedEventMetadata(record); err != nil {
		return nil, err
	}

	data, eventType, err := marshalEvent(record.Event)
	if err != nil {
		return nil, err
	}

	return json.Marshal(recordedEventWire{
		SchemaVersion: record.SchemaVersion,
		ID:            record.ID,
		CommandID:     record.CommandID,
		SessionID:     record.SessionID,
		Sequence:      record.Sequence,
		OccurredAt:    record.OccurredAt.UTC().Format(time.RFC3339Nano),
		Type:          eventType,
		Data:          data,
	})
}

func UnmarshalRecordedEvent(data []byte) (RecordedEvent, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var wire recordedEventWire
	if err := decoder.Decode(&wire); err != nil {
		return RecordedEvent{}, invalidEventError("invalid event envelope")
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return RecordedEvent{}, err
	}

	occurredAt, err := time.Parse(time.RFC3339Nano, wire.OccurredAt)
	if err != nil {
		return RecordedEvent{}, invalidEventError("invalid event timestamp")
	}
	record := RecordedEvent{
		SchemaVersion: wire.SchemaVersion,
		ID:            wire.ID,
		CommandID:     wire.CommandID,
		SessionID:     wire.SessionID,
		Sequence:      wire.Sequence,
		OccurredAt:    occurredAt.UTC(),
	}
	if err := validateRecordedEventMetadata(record); err != nil {
		return RecordedEvent{}, err
	}

	event, err := unmarshalEvent(wire.Type, wire.Data)
	if err != nil {
		return RecordedEvent{}, err
	}
	record.Event = event
	return record, nil
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return invalidEventError("event envelope has trailing JSON")
	}
	return nil
}

func marshalEvent(event Event) (json.RawMessage, string, error) {
	switch event := event.(type) {
	case SessionCreated:
		if strings.TrimSpace(event.WorkspaceRoot) == "" {
			return nil, "", invalidEventError("workspace root is required")
		}
		return marshalEventData(event, EventSessionCreated)
	case TurnStarted:
		if err := validateTurnID(event.TurnID); err != nil {
			return nil, "", err
		}
		if event.Input == "" {
			return nil, "", invalidEventError("turn input is required")
		}
		return marshalEventData(event, EventTurnStarted)
	case TurnCompleted:
		if err := validateTurnID(event.TurnID); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventTurnCompleted)
	case TurnFailed:
		if err := validateTurnID(event.TurnID); err != nil {
			return nil, "", err
		}
		if strings.TrimSpace(event.Code) == "" || strings.TrimSpace(event.Message) == "" {
			return nil, "", invalidEventError("failure code and message are required")
		}
		return marshalEventData(event, EventTurnFailed)
	case TurnInterrupted:
		if err := validateTurnID(event.TurnID); err != nil {
			return nil, "", err
		}
		if strings.TrimSpace(event.Reason) == "" {
			return nil, "", invalidEventError("interruption reason is required")
		}
		return marshalEventData(event, EventTurnInterrupted)
	case SessionClosed:
		return marshalEventData(event, EventSessionClosed)
	default:
		return nil, "", invalidEventError("unsupported event type")
	}
}

func marshalEventData(event Event, eventType string) (json.RawMessage, string, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, "", invalidEventError("invalid event data")
	}
	return data, eventType, nil
}

func unmarshalEvent(eventType string, data json.RawMessage) (Event, error) {
	trimmedData := bytes.TrimSpace(data)
	if len(trimmedData) == 0 || trimmedData[0] != '{' {
		return nil, invalidEventError("event data must be an object")
	}

	var event Event
	switch eventType {
	case EventSessionCreated:
		event = SessionCreated{}
	case EventTurnStarted:
		event = TurnStarted{}
	case EventTurnCompleted:
		event = TurnCompleted{}
	case EventTurnFailed:
		event = TurnFailed{}
	case EventTurnInterrupted:
		event = TurnInterrupted{}
	case EventSessionClosed:
		event = SessionClosed{}
	default:
		return nil, invalidEventError("unsupported event type")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	switch target := event.(type) {
	case SessionCreated:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case TurnStarted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case TurnCompleted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case TurnFailed:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case TurnInterrupted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case SessionClosed:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return nil, err
	}
	if _, _, err := marshalEvent(event); err != nil {
		return nil, err
	}
	return event, nil
}

func validateRecordedEventMetadata(record RecordedEvent) error {
	if record.SchemaVersion != schemaVersion {
		return invalidEventError("unsupported schema version")
	}
	if _, err := ParseEventID(string(record.ID)); err != nil {
		return invalidEventError("event ID is invalid")
	}
	if _, err := ParseCommandID(string(record.CommandID)); err != nil {
		return invalidEventError("command ID is invalid")
	}
	if _, err := ParseSessionID(string(record.SessionID)); err != nil {
		return invalidEventError("session ID is invalid")
	}
	if record.Sequence == 0 {
		return invalidEventError("event sequence must be positive")
	}
	if record.OccurredAt.IsZero() {
		return invalidEventError("event timestamp is required")
	}
	return nil
}

func validateTurnID(turnID TurnID) error {
	if _, err := ParseTurnID(string(turnID)); err != nil {
		return invalidEventError("turn ID is invalid")
	}
	return nil
}

func invalidEventError(message string) error {
	return domainError(CodeInvalidEvent, message)
}
