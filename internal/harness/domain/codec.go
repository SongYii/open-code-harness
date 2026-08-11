package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode/utf8"
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
	if err := validateStrictJSONStrings(data); err != nil {
		return RecordedEvent{}, err
	}
	if err := validateStrictJSONObject(data,
		"schemaVersion", "id", "commandId", "sessionId",
		"sequence", "occurredAt", "type", "data",
	); err != nil {
		return RecordedEvent{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var wire recordedEventWire
	if err := decoder.Decode(&wire); err != nil {
		return RecordedEvent{}, invalidEventError("invalid event envelope")
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return RecordedEvent{}, err
	}

	occurredAt, err := parseRecordedTimestamp(wire.OccurredAt)
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
		if strings.TrimSpace(event.Input) == "" {
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
	var event Event
	var keys []string
	switch eventType {
	case EventSessionCreated:
		event = SessionCreated{}
		keys = []string{"workspaceRoot"}
	case EventTurnStarted:
		event = TurnStarted{}
		keys = []string{"turnID", "input"}
	case EventTurnCompleted:
		event = TurnCompleted{}
		keys = []string{"turnID"}
	case EventTurnFailed:
		event = TurnFailed{}
		keys = []string{"turnID", "code", "message"}
	case EventTurnInterrupted:
		event = TurnInterrupted{}
		keys = []string{"turnID", "reason"}
	case EventSessionClosed:
		event = SessionClosed{}
		keys = []string{}
	default:
		return nil, invalidEventError("unsupported event type")
	}
	if err := validateStrictJSONObject(data, keys...); err != nil {
		return nil, err
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
	if err := validateRecordedEventIdentityAndTimestamp(record); err != nil {
		return err
	}
	if record.Sequence == 0 {
		return invalidEventError("event sequence must be positive")
	}
	return nil
}

func validateRecordedEventIdentityAndTimestamp(record RecordedEvent) error {
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
	if record.OccurredAt.IsZero() {
		return invalidEventError("event timestamp is required")
	}
	formatted := record.OccurredAt.UTC().Format(time.RFC3339Nano)
	if _, err := parseRecordedTimestamp(formatted); err != nil {
		return invalidEventError("event timestamp is outside RFC3339 range")
	}
	return nil
}

func validateStrictJSONObject(data []byte, requiredKeys ...string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return invalidEventError("JSON value must be an object")
	}

	seen := make(map[string]struct{}, len(requiredKeys))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return invalidEventError("invalid JSON object key")
		}
		key, ok := token.(string)
		if !ok || !containsString(requiredKeys, key) {
			return invalidEventError("JSON object contains an unknown key")
		}
		if _, exists := seen[key]; exists {
			return invalidEventError("JSON object contains a duplicate key")
		}
		seen[key] = struct{}{}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return invalidEventError("invalid JSON object value")
		}
	}

	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return invalidEventError("invalid JSON object")
	}
	if len(seen) != len(requiredKeys) {
		return invalidEventError("JSON object is missing a required key")
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return err
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateStrictJSONStrings(data []byte) error {
	if !utf8.Valid(data) {
		return invalidEventError("JSON contains invalid UTF-8")
	}

	for index := 0; index < len(data); {
		if data[index] != '"' {
			index++
			continue
		}
		index++
		for {
			if index >= len(data) {
				return invalidEventError("unterminated JSON string")
			}
			switch data[index] {
			case '"':
				index++
				goto nextString
			case '\\':
				next, err := consumeStrictJSONEscape(data, index)
				if err != nil {
					return err
				}
				index = next
			default:
				if data[index] < 0x20 {
					return invalidEventError("JSON string contains a control character")
				}
				_, size := utf8.DecodeRune(data[index:])
				index += size
			}
		}
	nextString:
	}
	return nil
}

func consumeStrictJSONEscape(data []byte, index int) (int, error) {
	if index+1 >= len(data) {
		return 0, invalidEventError("incomplete JSON escape")
	}
	switch data[index+1] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return index + 2, nil
	case 'u':
		codeUnit, ok := parseHexCodeUnit(data, index+2)
		if !ok {
			return 0, invalidEventError("invalid JSON Unicode escape")
		}
		if codeUnit >= 0xdc00 && codeUnit <= 0xdfff {
			return 0, invalidEventError("JSON contains an unpaired low surrogate")
		}
		if codeUnit < 0xd800 || codeUnit > 0xdbff {
			return index + 6, nil
		}
		if index+12 > len(data) || data[index+6] != '\\' || data[index+7] != 'u' {
			return 0, invalidEventError("JSON contains an unpaired high surrogate")
		}
		low, ok := parseHexCodeUnit(data, index+8)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return 0, invalidEventError("JSON contains a mispaired surrogate")
		}
		return index + 12, nil
	default:
		return 0, invalidEventError("invalid JSON escape")
	}
}

func parseHexCodeUnit(data []byte, index int) (uint16, bool) {
	if index+4 > len(data) {
		return 0, false
	}
	var value uint16
	for _, digit := range data[index : index+4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func parseRecordedTimestamp(value string) (time.Time, error) {
	if !hasRFC3339NanoGrammar(value) {
		return time.Time{}, invalidEventError("invalid event timestamp")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, invalidEventError("invalid event timestamp")
	}
	return parsed, nil
}

func hasRFC3339NanoGrammar(value string) bool {
	if len(value) < len("2006-01-02T15:04:05Z") {
		return false
	}
	for _, index := range []int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
		if !isASCIIDigit(value[index]) {
			return false
		}
	}
	if value[4] != '-' || value[7] != '-' || value[10] != 'T' || value[13] != ':' || value[16] != ':' {
		return false
	}

	index := 19
	if index < len(value) && value[index] == '.' {
		fractionStart := index + 1
		index = fractionStart
		for index < len(value) && isASCIIDigit(value[index]) {
			index++
		}
		if digits := index - fractionStart; digits < 1 || digits > 9 {
			return false
		}
	}
	if index == len(value)-1 && value[index] == 'Z' {
		return true
	}
	if len(value)-index != 6 || (value[index] != '+' && value[index] != '-') || value[index+3] != ':' {
		return false
	}
	return isASCIIDigit(value[index+1]) && isASCIIDigit(value[index+2]) &&
		isASCIIDigit(value[index+4]) && isASCIIDigit(value[index+5])
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
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
