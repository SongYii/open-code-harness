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

	eventType, data, err := MarshalEventPayload(record.Event)
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

// MarshalEventPayload returns the canonical event type and JSON payload used by
// recorded-event encoding. The returned payload is a defensive copy.
func MarshalEventPayload(event Event) (eventType string, payload []byte, err error) {
	data, eventType, err := marshalEvent(event)
	if err != nil {
		return "", nil, err
	}
	return eventType, append([]byte(nil), data...), nil
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
		if !hasRequiredText(event.WorkspaceRoot) {
			return nil, "", invalidEventError("workspace root is required")
		}
		return marshalEventData(event, EventSessionCreated)
	case TurnStarted:
		if err := validateTurnID(event.TurnID); err != nil {
			return nil, "", err
		}
		if !hasRequiredText(event.Input) {
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
		if !hasRequiredText(event.Code) || !hasRequiredText(event.Message) {
			return nil, "", invalidEventError("failure code and message are required")
		}
		return marshalEventData(event, EventTurnFailed)
	case TurnInterrupted:
		if err := validateTurnID(event.TurnID); err != nil {
			return nil, "", err
		}
		if !hasRequiredText(event.Reason) {
			return nil, "", invalidEventError("interruption reason is required")
		}
		return marshalEventData(event, EventTurnInterrupted)
	case SessionClosed:
		return marshalEventData(event, EventSessionClosed)
	case SessionDeleted:
		return marshalEventData(event, EventSessionDeleted)
	case AssistantMessageStarted:
		if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventAssistantMessageStarted)
	case AssistantMessageCompleted:
		if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
			return nil, "", err
		}
		if !utf8.ValidString(event.Text) {
			return nil, "", invalidEventError("assistant message text must be valid UTF-8")
		}
		if err := validateToolCallOffers(event.ToolCalls, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventAssistantMessageCompleted)
	case AssistantMessageFailed:
		if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
			return nil, "", err
		}
		if _, err := validateItemTerminal(event.Code, event.Message); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventAssistantMessageFailed)
	case AssistantMessageInterrupted:
		if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
			return nil, "", err
		}
		if _, err := validateItemTerminal(event.Code, event.Message); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventAssistantMessageInterrupted)
	case ModelRequestRecorded:
		if err := validateModelRequestPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventModelRequestRecorded)
	case ModelUsageRecorded:
		if err := validateModelUsagePayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventModelUsageRecorded)
	case ToolCallStarted:
		if err := validateToolCallStartedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventToolCallStarted)
	case ToolCallCompleted:
		if err := validateToolCallCompletedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventToolCallCompleted)
	case ToolCallFailed:
		if err := validateToolCallFailedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventToolCallFailed)
	case ToolCallInterrupted:
		if err := validateToolCallInterruptedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventToolCallInterrupted)
	case PolicyDecisionRecorded:
		if err := validatePolicyDecisionPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventPolicyDecisionRecorded)
	case ApprovalRequested:
		if err := validateApprovalRequestedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventApprovalRequested)
	case ApprovalResolved:
		if err := validateApprovalResolvedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventApprovalResolved)
	case ContextCompactionStarted:
		if err := validateContextCompactionStartedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventContextCompactionStarted)
	case ContextCompactionCompleted:
		if err := validateContextCompactionCompletedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventContextCompactionCompleted)
	case ContextCompactionFailed:
		if err := validateContextCompactionFailedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventContextCompactionFailed)
	case ContextPreparedRecorded:
		if err := validateContextPreparedPayload(event, CodeInvalidEvent); err != nil {
			return nil, "", err
		}
		return marshalEventData(event, EventContextPreparedRecorded)
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
	var required []string
	var optional []string
	switch eventType {
	case EventSessionCreated:
		event = SessionCreated{}
		required = []string{"workspaceRoot"}
	case EventTurnStarted:
		event = TurnStarted{}
		required = []string{"turnID", "input"}
	case EventTurnCompleted:
		event = TurnCompleted{}
		required = []string{"turnID"}
	case EventTurnFailed:
		event = TurnFailed{}
		required = []string{"turnID", "code", "message"}
	case EventTurnInterrupted:
		event = TurnInterrupted{}
		required = []string{"turnID", "reason"}
	case EventSessionClosed:
		event = SessionClosed{}
		required = []string{}
	case EventSessionDeleted:
		event = SessionDeleted{}
		required = []string{}
	case EventAssistantMessageStarted:
		event = AssistantMessageStarted{}
		required = []string{"turnID", "itemID"}
	case EventAssistantMessageCompleted:
		event = AssistantMessageCompleted{}
		required = []string{"turnID", "itemID", "text"}
		optional = []string{"toolCalls"}
	case EventAssistantMessageFailed:
		event = AssistantMessageFailed{}
		required = []string{"turnID", "itemID", "code", "message"}
	case EventAssistantMessageInterrupted:
		event = AssistantMessageInterrupted{}
		required = []string{"turnID", "itemID", "code", "message"}
	case EventModelRequestRecorded:
		event = ModelRequestRecorded{}
		required = modelRequestRecordedKeys()
		optional = []string{"tools", "purpose", "attemptIndex", "contextDecisionID"}
	case EventModelUsageRecorded:
		event = ModelUsageRecorded{}
		required = modelUsageRecordedKeys()
		optional = []string{"attemptIndex"}
	case EventToolCallStarted:
		event = ToolCallStarted{}
		required = []string{"turnID", "itemID", "callID", "name", "arguments", "stepIndex"}
	case EventToolCallCompleted:
		event = ToolCallCompleted{}
		required = []string{"turnID", "itemID", "callID", "content", "truncated"}
	case EventToolCallFailed:
		event = ToolCallFailed{}
		required = []string{"turnID", "itemID", "callID", "code", "message"}
	case EventToolCallInterrupted:
		event = ToolCallInterrupted{}
		required = []string{"turnID", "itemID", "callID", "code", "message"}
	case EventPolicyDecisionRecorded:
		event = PolicyDecisionRecorded{}
		required = []string{"turnID", "itemID", "callID", "name", "effect", "ruleID", "reason"}
	case EventApprovalRequested:
		event = ApprovalRequested{}
		required = []string{"turnID", "itemID", "approvalID", "callID", "name", "reason"}
	case EventApprovalResolved:
		event = ApprovalResolved{}
		required = []string{"turnID", "itemID", "approvalID", "decision"}
	case EventContextCompactionStarted:
		event = ContextCompactionStarted{}
		required = []string{"id", "trigger", "strategy", "baseSourceHead", "sourceSchema", "meterID"}
		optional = []string{"priorCheckpointID", "promptVersion", "plannedRoute"}
	case EventContextCompactionCompleted:
		event = ContextCompactionCompleted{}
		required = []string{"id", "checkpoint"}
	case EventContextCompactionFailed:
		event = ContextCompactionFailed{}
		required = []string{"id", "code", "message"}
	case EventContextPreparedRecorded:
		event = ContextPreparedRecorded{}
		required = []string{
			"turnID", "itemID", "attemptIndex", "contextDecisionID", "trigger",
			"sourceHeadVersion", "budgetHardInput", "budgetTrigger", "budgetTarget",
			"estimatedMessageTokens", "estimatedToolSchemaTokens", "estimatedTotalTokens",
			"meterID", "serializedEnvelopeBytes",
		}
		optional = []string{
			"checkpointID", "checkpointKind", "rawTailFromSequence", "rawTailThroughSequence",
			"usageAnchorApplied", "usageAnchorTokens", "prunedToolResultCount",
		}
	default:
		return nil, invalidEventError("unsupported event type")
	}
	if err := validateJSONObjectKeys(data, required, optional); err != nil {
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
	case SessionDeleted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case AssistantMessageStarted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case AssistantMessageCompleted:
		if err := validateOptionalObjectArray(data, "toolCalls", []string{"id", "name", "arguments"}); err != nil {
			return nil, err
		}
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case AssistantMessageFailed:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case AssistantMessageInterrupted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ModelRequestRecorded:
		if err := validateModelRequestMessagesJSON(data); err != nil {
			return nil, err
		}
		if err := validateModelRequestToolsJSON(data); err != nil {
			return nil, err
		}
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ModelUsageRecorded:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ToolCallStarted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ToolCallCompleted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ToolCallFailed:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ToolCallInterrupted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case PolicyDecisionRecorded:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ApprovalRequested:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ApprovalResolved:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ContextCompactionStarted:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ContextCompactionCompleted:
		if err := validateContextCheckpointRecordJSON(data); err != nil {
			return nil, err
		}
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ContextCompactionFailed:
		if err := decoder.Decode(&target); err != nil {
			return nil, invalidEventError("invalid event data")
		}
		event = target
	case ContextPreparedRecorded:
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
	return validateJSONObjectKeys(data, requiredKeys, nil)
}

func validateJSONObjectKeys(data []byte, requiredKeys, optionalKeys []string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return invalidEventError("JSON value must be an object")
	}

	seen := make(map[string]struct{}, len(requiredKeys)+len(optionalKeys))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return invalidEventError("invalid JSON object key")
		}
		key, ok := token.(string)
		if !ok || (!containsString(requiredKeys, key) && !containsString(optionalKeys, key)) {
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
	for _, key := range requiredKeys {
		if _, exists := seen[key]; !exists {
			return invalidEventError("JSON object is missing a required key")
		}
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
	if !isASCIIDigit(value[index+1]) || !isASCIIDigit(value[index+2]) ||
		!isASCIIDigit(value[index+4]) || !isASCIIDigit(value[index+5]) {
		return false
	}
	offsetHour := int(value[index+1]-'0')*10 + int(value[index+2]-'0')
	offsetMinute := int(value[index+4]-'0')*10 + int(value[index+5]-'0')
	return offsetHour < 24 && offsetMinute < 60
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

func validateAssistantMessageIDs(turnID TurnID, itemID ItemID) error {
	if err := validateTurnID(turnID); err != nil {
		return err
	}
	if _, err := ParseItemID(string(itemID)); err != nil {
		return invalidEventError("item ID is invalid")
	}
	return nil
}

func modelRequestRecordedKeys() []string {
	return []string{
		"turnID", "itemID", "adapterFamily", "modelID", "endpointID",
		"nativeTools", "images", "structuredOutput", "reasoningFields", "promptCache",
		"contextWindowTokens", "maxOutputTokens", "includeUsage", "maxTokensField", "messages",
	}
}

func modelUsageRecordedKeys() []string {
	return []string{
		"turnID", "itemID", "inputTokens", "outputTokens", "cachedInputTokens",
		"latencyMs", "finishReason", "providerRequestID",
	}
}

func validateModelRequestSpec(spec ModelRequestSpec) error {
	return validateModelRequestBody(
		spec.AdapterFamily, spec.ModelID, spec.EndpointID,
		spec.NativeTools, spec.Images, spec.StructuredOutput,
		spec.ReasoningFields, spec.PromptCache, spec.MaxTokensField,
		spec.Messages, spec.Tools, CodeInvalidCommand,
	)
}

func validateModelRequestPayload(event ModelRequestRecorded, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	// Empty Purpose is equivalent to ModelRequestPurposeConversation, so
	// every ModelRequestRecorded constructed before this field existed
	// remains valid; only a genuinely unrecognized non-empty value fails.
	switch event.Purpose {
	case "", ModelRequestPurposeConversation, ModelRequestPurposeCompaction:
	default:
		return domainError(code, "model request purpose is invalid")
	}
	if event.ContextDecisionID != "" {
		if _, err := ParseContextDecisionID(string(event.ContextDecisionID)); err != nil {
			return domainError(code, "context decision ID is invalid")
		}
	}
	return validateModelRequestBody(
		event.AdapterFamily, event.ModelID, event.EndpointID,
		event.NativeTools, event.Images, event.StructuredOutput,
		event.ReasoningFields, event.PromptCache, event.MaxTokensField,
		event.Messages, event.Tools, code,
	)
}

func validateModelRequestBody(
	adapterFamily, modelID, endpointID, nativeTools, images, structuredOutput, reasoningFields, promptCache, maxTokensField string,
	messages []ModelPromptMessage,
	tools []ToolSchema,
	code ErrorCode,
) error {
	for _, value := range []string{adapterFamily, modelID, endpointID, nativeTools, images, structuredOutput, reasoningFields, promptCache, maxTokensField} {
		if !utf8.ValidString(value) {
			return domainError(code, "model request field must be valid UTF-8")
		}
	}
	if err := validateModelPromptMessages(messages, code); err != nil {
		return err
	}
	return validateToolSchemas(tools, code)
}

func validateModelPromptMessages(messages []ModelPromptMessage, code ErrorCode) error {
	if len(messages) == 0 {
		return domainError(code, "model request messages are required")
	}
	for _, message := range messages {
		if !utf8.ValidString(message.Text) {
			return domainError(code, "model prompt text must be valid UTF-8")
		}
		switch message.Role {
		case PromptRoleSystem, PromptRoleUser:
			if len(message.ToolCalls) != 0 || message.ToolCallID != "" || message.Name != "" {
				return domainError(code, "model prompt fields are invalid for role")
			}
		case PromptRoleAssistant:
			if message.ToolCallID != "" || message.Name != "" {
				return domainError(code, "model prompt fields are invalid for role")
			}
			if err := validateToolCallOffers(message.ToolCalls, code); err != nil {
				return err
			}
		case PromptRoleTool:
			if len(message.ToolCalls) != 0 {
				return domainError(code, "model prompt fields are invalid for role")
			}
			if !hasRequiredText(message.ToolCallID) || !hasRequiredText(message.Name) {
				return domainError(code, "tool prompt requires toolCallID and name")
			}
			if !utf8.ValidString(message.ToolCallID) || !utf8.ValidString(message.Name) {
				return domainError(code, "model prompt field must be valid UTF-8")
			}
		default:
			return domainError(code, "model prompt role is invalid")
		}
	}
	return nil
}

func validateModelUsagePayload(event ModelUsageRecorded, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	switch event.FinishReason {
	case "", FinishReasonStop, FinishReasonLength, FinishReasonUnknown, FinishReasonToolCalls:
	default:
		return domainError(code, "finish reason is invalid")
	}
	if !utf8.ValidString(event.FinishReason) || !utf8.ValidString(event.ProviderRequestID) {
		return domainError(code, "model usage field must be valid UTF-8")
	}
	return nil
}

func validateModelRequestMessagesJSON(data json.RawMessage) error {
	var parent struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &parent); err != nil {
		return invalidEventError("invalid event data")
	}
	if len(parent.Messages) == 0 {
		return invalidEventError("model request messages are required")
	}
	for _, message := range parent.Messages {
		if err := validateJSONObjectKeys(message, []string{"role", "text"}, []string{"toolCalls", "toolCallID", "name"}); err != nil {
			return err
		}
		if err := validateOptionalObjectArray(message, "toolCalls", []string{"id", "name", "arguments"}); err != nil {
			return err
		}
	}
	return nil
}

func validateModelRequestToolsJSON(data json.RawMessage) error {
	if err := validateOptionalObjectArray(data, "tools", []string{"name", "description", "inputSchema"}); err != nil {
		return err
	}
	fields, err := jsonObjectFields(data)
	if err != nil {
		return err
	}
	raw, ok := fields["tools"]
	if !ok {
		return nil
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		return invalidEventError("invalid event data")
	}
	for _, tool := range tools {
		fields, err := jsonObjectFields(tool)
		if err != nil {
			return err
		}
		schema, ok := fields["inputSchema"]
		if !ok || !isJSONObject(schema) {
			return invalidEventError("tool inputSchema must be an object")
		}
	}
	return nil
}

func validateToolSchemas(schemas []ToolSchema, code ErrorCode) error {
	for _, schema := range schemas {
		if !hasRequiredText(schema.Name) || !utf8.ValidString(schema.Description) {
			return domainError(code, "tool schema name and description are invalid")
		}
		if !isJSONObject(schema.InputSchema) {
			return domainError(code, "tool inputSchema must be an object")
		}
	}
	return nil
}

func validateToolCallOffers(offers []ToolCallOffer, code ErrorCode) error {
	seen := make(map[string]struct{}, len(offers))
	for _, offer := range offers {
		if !hasRequiredText(offer.ID) || !hasRequiredText(offer.Name) {
			return domainError(code, "tool call id and name are required")
		}
		if !utf8.ValidString(offer.ID) || !utf8.ValidString(offer.Name) || !utf8.ValidString(offer.Arguments) {
			return domainError(code, "tool call field must be valid UTF-8")
		}
		if _, exists := seen[offer.ID]; exists {
			return domainError(code, "tool call ids must be unique")
		}
		seen[offer.ID] = struct{}{}
	}
	return nil
}

func validateToolCallStartedPayload(event ToolCallStarted, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	if !hasRequiredText(event.CallID) || !hasRequiredText(event.Name) {
		return domainError(code, "tool call id and name are required")
	}
	if !utf8.ValidString(event.CallID) || !utf8.ValidString(event.Name) || !utf8.ValidString(event.Arguments) {
		return domainError(code, "tool call field must be valid UTF-8")
	}
	if event.StepIndex == 0 {
		return domainError(code, "tool call stepIndex must be 1-based")
	}
	return nil
}

func validateToolCallCompletedPayload(event ToolCallCompleted, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	if !hasRequiredText(event.CallID) || !utf8.ValidString(event.CallID) || !utf8.ValidString(event.Content) {
		return domainError(code, "tool call completion fields are invalid")
	}
	return nil
}

func validateToolCallFailedPayload(event ToolCallFailed, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	if !hasRequiredText(event.CallID) || !hasRequiredText(event.Code) || !utf8.ValidString(event.CallID) || !utf8.ValidString(event.Message) {
		return domainError(code, "tool call failure fields are invalid")
	}
	return nil
}

func validateToolCallInterruptedPayload(event ToolCallInterrupted, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	if !hasRequiredText(event.CallID) || !hasRequiredText(event.Code) || !utf8.ValidString(event.CallID) || !utf8.ValidString(event.Message) {
		return domainError(code, "tool call interruption fields are invalid")
	}
	return nil
}

func validatePolicyDecisionPayload(event PolicyDecisionRecorded, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	switch event.Effect {
	case PolicyEffectAllow, PolicyEffectDeny, PolicyEffectRequireApproval:
	default:
		return domainError(code, "policy effect is invalid")
	}
	if !hasRequiredText(event.CallID) || !hasRequiredText(event.Name) || !hasRequiredText(event.RuleID) || !hasRequiredText(event.Reason) {
		return domainError(code, "policy decision fields are required")
	}
	if !utf8.ValidString(event.CallID) || !utf8.ValidString(event.Name) || !utf8.ValidString(event.RuleID) || !utf8.ValidString(event.Reason) {
		return domainError(code, "policy decision field must be valid UTF-8")
	}
	return nil
}

func validateApprovalRequestedPayload(event ApprovalRequested, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	if _, err := ParseApprovalID(string(event.ApprovalID)); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "approval ID is invalid")
		}
		return invalidEventError("approval ID is invalid")
	}
	if !hasRequiredText(event.CallID) || !hasRequiredText(event.Name) || !hasRequiredText(event.Reason) {
		return domainError(code, "approval request fields are required")
	}
	if !utf8.ValidString(event.CallID) || !utf8.ValidString(event.Name) || !utf8.ValidString(event.Reason) {
		return domainError(code, "approval request field must be valid UTF-8")
	}
	return nil
}

func validateApprovalResolvedPayload(event ApprovalResolved, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	if _, err := ParseApprovalID(string(event.ApprovalID)); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "approval ID is invalid")
		}
		return invalidEventError("approval ID is invalid")
	}
	switch event.Decision {
	case ApprovalDecisionGranted, ApprovalDecisionDenied, ApprovalDecisionTimeout, ApprovalDecisionCanceled:
	default:
		return domainError(code, "approval decision is invalid")
	}
	return nil
}

func validateOptionalObjectArray(data json.RawMessage, key string, elemKeys []string) error {
	fields, err := jsonObjectFields(data)
	if err != nil {
		return err
	}
	raw, ok := fields[key]
	if !ok {
		return nil
	}
	if !isJSONArray(raw) {
		return invalidEventError("JSON array is required")
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return invalidEventError("invalid event data")
	}
	for _, element := range elements {
		if err := validateStrictJSONObject(element, elemKeys...); err != nil {
			return err
		}
	}
	return nil
}

func jsonObjectFields(data json.RawMessage) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, invalidEventError("JSON value must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, invalidEventError("invalid JSON object key")
		}
		key, ok := token.(string)
		if !ok {
			return nil, invalidEventError("invalid JSON object key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, invalidEventError("invalid JSON object value")
		}
		fields[key] = value
	}
	return fields, nil
}

func isJSONArray(data json.RawMessage) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func isJSONObject(data json.RawMessage) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func invalidEventError(message string) error {
	return domainError(CodeInvalidEvent, message)
}

func validateContextCompactionID(id ContextCompactionID) error {
	if _, err := ParseContextCompactionID(string(id)); err != nil {
		return invalidEventError("context compaction ID is invalid")
	}
	return nil
}

func validateContextTrigger(trigger string) error {
	switch trigger {
	case ContextTriggerPreTurn, ContextTriggerManual, ContextTriggerMidTurn, ContextTriggerOverflowRetry:
		return nil
	default:
		return invalidEventError("context compaction trigger is invalid")
	}
}

func validateContextStrategy(strategy string) error {
	switch strategy {
	case ContextStrategySummary, ContextStrategyReset:
		return nil
	default:
		return invalidEventError("context compaction strategy is invalid")
	}
}

func validateContextCompactionStartedPayload(event ContextCompactionStarted, code ErrorCode) error {
	if err := validateContextCompactionID(event.ID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "context compaction ID is invalid")
		}
		return err
	}
	if err := validateContextTrigger(event.Trigger); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "context compaction trigger is invalid")
		}
		return err
	}
	if err := validateContextStrategy(event.Strategy); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "context compaction strategy is invalid")
		}
		return err
	}
	if !hasRequiredText(event.SourceSchema) || !hasRequiredText(event.MeterID) {
		return domainError(code, "context compaction source schema and meter ID are required")
	}
	for _, value := range []string{event.PriorCheckpointID, event.PromptVersion, event.SourceSchema, event.MeterID, event.PlannedRoute} {
		if !utf8.ValidString(value) {
			return domainError(code, "context compaction field must be valid UTF-8")
		}
	}
	return nil
}

func validateContextCheckpointKind(kind string) error {
	switch kind {
	case ContextCheckpointKindRollingSummary, ContextCheckpointKindSourceTailReset:
		return nil
	default:
		return invalidEventError("context checkpoint kind is invalid")
	}
}

// validateContextCheckpointRecord checks structural validity only (non-
// blank required fields, valid UTF-8, a supported kind) — it does not
// re-derive contextengine's own successor-lineage or digest-chain
// verification (ValidateSuccessor, the ContextCheckpointStore contract),
// which are Application/Task 9's responsibility using contextengine's own
// functions before a command ever reaches Decide.
func validateContextCheckpointRecord(checkpoint ContextCheckpointRecord, code ErrorCode) error {
	if !hasRequiredText(checkpoint.ID) {
		return domainError(code, "checkpoint ID is required")
	}
	if err := validateContextCheckpointKind(checkpoint.Kind); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "checkpoint kind is invalid")
		}
		return err
	}
	if !hasRequiredText(checkpoint.SourceSchema) {
		return domainError(code, "checkpoint source schema is required")
	}
	if !hasRequiredText(checkpoint.SourceDigestHex) {
		return domainError(code, "checkpoint source digest is required")
	}
	if checkpoint.Kind == ContextCheckpointKindRollingSummary && strings.TrimSpace(checkpoint.Summary) == "" {
		return domainError(code, "rolling summary checkpoint requires summary text")
	}
	for _, value := range []string{
		checkpoint.ID, checkpoint.SourceSchema, checkpoint.SummaryFormat, checkpoint.PromptVersion,
		checkpoint.SourceDigestHex, checkpoint.PreviousCheckpointID, checkpoint.Summary,
		checkpoint.Limitations, checkpoint.SummarizerRoute,
	} {
		if !utf8.ValidString(value) {
			return domainError(code, "checkpoint field must be valid UTF-8")
		}
	}
	return nil
}

func validateContextCompactionCompletedPayload(event ContextCompactionCompleted, code ErrorCode) error {
	if err := validateContextCompactionID(event.ID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "context compaction ID is invalid")
		}
		return err
	}
	return validateContextCheckpointRecord(event.Checkpoint, code)
}

func validateContextCompactionFailedPayload(event ContextCompactionFailed, code ErrorCode) error {
	if err := validateContextCompactionID(event.ID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "context compaction ID is invalid")
		}
		return err
	}
	if !hasRequiredText(event.Code) || !hasRequiredText(event.Message) {
		return domainError(code, "context compaction failure code and message are required")
	}
	if !utf8.ValidString(event.Message) {
		return domainError(code, "context compaction failure message must be valid UTF-8")
	}
	return nil
}

func validateContextPreparedPayload(event ContextPreparedRecorded, code ErrorCode) error {
	if err := validateAssistantMessageIDs(event.TurnID, event.ItemID); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "turn ID is invalid")
		}
		return err
	}
	if err := validateContextTrigger(event.Trigger); err != nil {
		if code == CodeInvalidCommand {
			return domainError(CodeInvalidCommand, "context compaction trigger is invalid")
		}
		return err
	}
	if event.ContextDecisionID != "" {
		if _, err := ParseContextDecisionID(string(event.ContextDecisionID)); err != nil {
			return domainError(code, "context decision ID is invalid")
		}
	}
	if (event.CheckpointID == "") != (event.CheckpointKind == "") {
		return domainError(code, "checkpoint ID and kind must both be set or both be empty")
	}
	if event.CheckpointKind != "" {
		if err := validateContextCheckpointKind(event.CheckpointKind); err != nil {
			if code == CodeInvalidCommand {
				return domainError(CodeInvalidCommand, "checkpoint kind is invalid")
			}
			return err
		}
	}
	if !utf8.ValidString(event.CheckpointID) || !utf8.ValidString(event.MeterID) {
		return domainError(code, "context preparation field must be valid UTF-8")
	}
	return nil
}

// validateContextCheckpointRecordJSON strictly validates the nested
// "checkpoint" object's own keys, matching how validateModelRequestMessagesJSON
// validates ModelRequestRecorded's nested "messages" array — the generic
// top-level validateJSONObjectKeys pass does not descend into nested
// objects.
func validateContextCheckpointRecordJSON(data json.RawMessage) error {
	fields, err := jsonObjectFields(data)
	if err != nil {
		return err
	}
	checkpoint, ok := fields["checkpoint"]
	if !ok {
		return invalidEventError("checkpoint is required")
	}
	return validateJSONObjectKeys(checkpoint,
		[]string{"id", "kind", "sourceSchema", "coveredEventCount", "coveredTurnCount", "throughSequence", "sourceDigestHex", "tokensBefore", "checkpointTokens", "retainedTailTokens", "estimatedRequestTokens"},
		[]string{"summaryFormat", "promptVersion", "previousCheckpointID", "summary", "limitations", "summarizerRoute", "summarizerUsage", "summaryChunks", "prunedToolResultCount"},
	)
}
