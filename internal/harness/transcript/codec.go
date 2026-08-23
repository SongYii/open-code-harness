// Package transcript encodes the experimental och.session.transcript JSONL export.
package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const (
	Schema                = "och.session.transcript"
	FormatVersion         = 1
	TypeSnapshot          = "transcript.snapshot"
	TypeComplete          = "transcript.complete"
	StabilityExperimental = "experimental"

	CodeUnsupportedEventType     = "unsupported_event_type"
	CodeLineLimit                = "line_limit"
	CodeInvalidLine              = "invalid_line"
	CodeUnsupportedFormatVersion = "unsupported_format_version"

	maxLineBytes = 2 << 20
	// 9-digit fraction keeps nanoseconds present on the wire (RFC3339Nano goldens).
	timestampLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func IsCode(err error, code string) bool {
	var target *Error
	return errors.As(err, &target) && target.Code == code
}

type Line struct {
	FormatVersion int             `json:"formatVersion"`
	Schema        string          `json:"schema"`
	SessionID     string          `json:"sessionId"`
	EventID       string          `json:"eventId"`
	CommandID     string          `json:"commandId"`
	Sequence      uint64          `json:"sequence"`
	OccurredAt    string          `json:"occurredAt"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
}

type SnapshotLine struct {
	FormatVersion int             `json:"formatVersion"`
	Schema        string          `json:"schema"`
	SessionID     string          `json:"sessionId"`
	OccurredAt    string          `json:"occurredAt"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
}

type CompleteLine struct {
	FormatVersion int             `json:"formatVersion"`
	Schema        string          `json:"schema"`
	SessionID     string          `json:"sessionId"`
	OccurredAt    string          `json:"occurredAt"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
}

type Decoded struct {
	Line     *Line
	Snapshot *SnapshotLine
	Complete *CompleteLine
}

type snapshotPayload struct {
	HeadSequence uint64 `json:"headSequence"`
	Open         bool   `json:"open"`
	Running      bool   `json:"running"`
	Stability    string `json:"stability"`
}

type completePayload struct {
	HeadSequence uint64 `json:"headSequence"`
	FactLines    uint64 `json:"factLines"`
	Open         bool   `json:"open"`
	Running      bool   `json:"running"`
}

type sessionCreatedPayload struct {
	WorkspaceRoot string `json:"workspaceRoot"`
}

type turnStartedPayload struct {
	TurnID string `json:"turnID"`
	Input  string `json:"input"`
}

type turnIDPayload struct {
	TurnID string `json:"turnID"`
}

type turnFailedPayload struct {
	TurnID  string `json:"turnID"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type turnInterruptedPayload struct {
	TurnID string `json:"turnID"`
	Reason string `json:"reason"`
}

type assistantStartedPayload struct {
	TurnID    string `json:"turnID"`
	ItemID    string `json:"itemID"`
	StepIndex uint32 `json:"stepIndex"`
	StepRef   string `json:"stepRef"`
}

type assistantCompletedPayload struct {
	TurnID    string                 `json:"turnID"`
	ItemID    string                 `json:"itemID"`
	StepIndex uint32                 `json:"stepIndex"`
	StepRef   string                 `json:"stepRef"`
	Text      string                 `json:"text"`
	ToolCalls []domain.ToolCallOffer `json:"toolCalls,omitempty"`
}

type assistantFailedPayload struct {
	TurnID    string `json:"turnID"`
	ItemID    string `json:"itemID"`
	StepIndex uint32 `json:"stepIndex"`
	StepRef   string `json:"stepRef"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type usagePayload struct {
	TurnID            string `json:"turnID"`
	ItemID            string `json:"itemID"`
	InputTokens       uint64 `json:"inputTokens"`
	OutputTokens      uint64 `json:"outputTokens"`
	CachedInputTokens uint64 `json:"cachedInputTokens"`
	LatencyMs         uint64 `json:"latencyMs"`
	FinishReason      string `json:"finishReason"`
	ProviderRequestID string `json:"providerRequestID"`
}

type toolStartedPayload struct {
	TurnID    string `json:"turnID"`
	ItemID    string `json:"itemID"`
	CallID    string `json:"callID"`
	StepIndex uint32 `json:"stepIndex"`
	StepRef   string `json:"stepRef"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolCompletedPayload struct {
	TurnID    string `json:"turnID"`
	ItemID    string `json:"itemID"`
	CallID    string `json:"callID"`
	StepIndex uint32 `json:"stepIndex"`
	StepRef   string `json:"stepRef"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type toolFailedPayload struct {
	TurnID    string `json:"turnID"`
	ItemID    string `json:"itemID"`
	CallID    string `json:"callID"`
	StepIndex uint32 `json:"stepIndex"`
	StepRef   string `json:"stepRef"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type approvalRequestedPayload struct {
	TurnID     string `json:"turnID"`
	ItemID     string `json:"itemID"`
	ApprovalID string `json:"approvalID"`
	CallID     string `json:"callID"`
	Name       string `json:"name"`
	Reason     string `json:"reason"`
}

type approvalResolvedPayload struct {
	TurnID     string `json:"turnID"`
	ItemID     string `json:"itemID"`
	ApprovalID string `json:"approvalID"`
	Decision   string `json:"decision"`
}

func MarshalLine(line Line) ([]byte, error) {
	if err := validateFactHeader(line); err != nil {
		return nil, err
	}
	return marshalEncoded(line, line.Payload)
}

func MarshalSnapshot(line SnapshotLine) ([]byte, error) {
	if err := validateIntegrityHeader(line.FormatVersion, line.Schema, line.Type, TypeSnapshot); err != nil {
		return nil, err
	}
	return marshalEncoded(line, line.Payload)
}

func MarshalComplete(line CompleteLine) ([]byte, error) {
	if err := validateIntegrityHeader(line.FormatVersion, line.Schema, line.Type, TypeComplete); err != nil {
		return nil, err
	}
	return marshalEncoded(line, line.Payload)
}

func UnmarshalLine(data []byte) (Decoded, error) {
	typ, version, err := peekTypeAndVersion(data)
	if err != nil {
		return Decoded{}, err
	}
	if version != FormatVersion {
		return Decoded{}, unsupportedFormatVersion()
	}
	switch typ {
	case TypeSnapshot:
		return decodeSnapshot(data)
	case TypeComplete:
		return decodeComplete(data)
	default:
		return decodeFact(data, typ)
	}
}

func DecodeSkipsUnknown(data []byte) (Decoded, bool, error) {
	typ, version, err := peekTypeAndVersion(data)
	if err != nil {
		return Decoded{}, false, err
	}
	if version != FormatVersion {
		return Decoded{}, false, unsupportedFormatVersion()
	}
	if typ == "" {
		return Decoded{}, false, invalidLine("missing type")
	}
	if typ == TypeSnapshot || typ == TypeComplete {
		decoded, err := UnmarshalLine(data)
		return decoded, false, err
	}
	if !knownFactType(typ) {
		return Decoded{}, true, nil
	}
	decoded, err := UnmarshalLine(data)
	return decoded, false, err
}

func ProjectRecord(record domain.RecordedEvent, steps map[domain.TurnID]uint32) (Line, bool, error) {
	switch event := record.Event.(type) {
	case domain.SessionCreated:
		return makeLine(record, domain.EventSessionCreated, sessionCreatedPayload{WorkspaceRoot: event.WorkspaceRoot})
	case domain.SessionClosed:
		return makeLine(record, domain.EventSessionClosed, struct{}{})
	case domain.TurnStarted:
		return makeLine(record, domain.EventTurnStarted, turnStartedPayload{TurnID: string(event.TurnID), Input: event.Input})
	case domain.TurnCompleted:
		return makeLine(record, domain.EventTurnCompleted, turnIDPayload{TurnID: string(event.TurnID)})
	case domain.TurnFailed:
		return makeLine(record, domain.EventTurnFailed, turnFailedPayload{TurnID: string(event.TurnID), Code: event.Code, Message: event.Message})
	case domain.TurnInterrupted:
		return makeLine(record, domain.EventTurnInterrupted, turnInterruptedPayload{TurnID: string(event.TurnID), Reason: event.Reason})
	case domain.AssistantMessageStarted:
		if steps == nil {
			return Line{}, false, invalidLine("steps map is required")
		}
		next := steps[event.TurnID] + 1
		steps[event.TurnID] = next
		return makeLine(record, domain.EventAssistantMessageStarted, assistantStartedPayload{
			TurnID:    string(event.TurnID),
			ItemID:    string(event.ItemID),
			StepIndex: next,
			StepRef:   stepRef(event.TurnID, next),
		})
	case domain.AssistantMessageCompleted:
		index := steps[event.TurnID]
		return makeLine(record, domain.EventAssistantMessageCompleted, assistantCompletedPayload{
			TurnID:    string(event.TurnID),
			ItemID:    string(event.ItemID),
			StepIndex: index,
			StepRef:   stepRef(event.TurnID, index),
			Text:      event.Text,
			ToolCalls: event.ToolCalls,
		})
	case domain.AssistantMessageFailed:
		index := steps[event.TurnID]
		return makeLine(record, domain.EventAssistantMessageFailed, assistantFailedPayload{
			TurnID:    string(event.TurnID),
			ItemID:    string(event.ItemID),
			StepIndex: index,
			StepRef:   stepRef(event.TurnID, index),
			Code:      event.Code,
			Message:   event.Message,
		})
	case domain.AssistantMessageInterrupted:
		index := steps[event.TurnID]
		return makeLine(record, domain.EventAssistantMessageInterrupted, assistantFailedPayload{
			TurnID:    string(event.TurnID),
			ItemID:    string(event.ItemID),
			StepIndex: index,
			StepRef:   stepRef(event.TurnID, index),
			Code:      event.Code,
			Message:   event.Message,
		})
	case domain.ModelUsageRecorded:
		return makeLine(record, domain.EventModelUsageRecorded, usagePayload{
			TurnID:            string(event.TurnID),
			ItemID:            string(event.ItemID),
			InputTokens:       event.InputTokens,
			OutputTokens:      event.OutputTokens,
			CachedInputTokens: event.CachedInputTokens,
			LatencyMs:         event.LatencyMs,
			FinishReason:      event.FinishReason,
			ProviderRequestID: event.ProviderRequestID,
		})
	case domain.ToolCallStarted:
		return makeLine(record, domain.EventToolCallStarted, toolStartedPayload{
			TurnID:    string(event.TurnID),
			ItemID:    string(event.ItemID),
			CallID:    event.CallID,
			StepIndex: event.StepIndex,
			StepRef:   stepRef(event.TurnID, event.StepIndex),
			Name:      event.Name,
			Arguments: event.Arguments,
		})
	case domain.ToolCallCompleted:
		index := steps[event.TurnID]
		return makeLine(record, domain.EventToolCallCompleted, toolCompletedPayload{
			TurnID:    string(event.TurnID),
			ItemID:    string(event.ItemID),
			CallID:    event.CallID,
			StepIndex: index,
			StepRef:   stepRef(event.TurnID, index),
			Content:   event.Content,
			Truncated: event.Truncated,
		})
	case domain.ToolCallFailed:
		index := steps[event.TurnID]
		return makeLine(record, domain.EventToolCallFailed, toolFailedPayload{
			TurnID:    string(event.TurnID),
			ItemID:    string(event.ItemID),
			CallID:    event.CallID,
			StepIndex: index,
			StepRef:   stepRef(event.TurnID, index),
			Code:      event.Code,
			Message:   event.Message,
		})
	case domain.ToolCallInterrupted:
		index := steps[event.TurnID]
		return makeLine(record, domain.EventToolCallInterrupted, toolFailedPayload{
			TurnID:    string(event.TurnID),
			ItemID:    string(event.ItemID),
			CallID:    event.CallID,
			StepIndex: index,
			StepRef:   stepRef(event.TurnID, index),
			Code:      event.Code,
			Message:   event.Message,
		})
	case domain.ApprovalRequested:
		return makeLine(record, domain.EventApprovalRequested, approvalRequestedPayload{
			TurnID:     string(event.TurnID),
			ItemID:     string(event.ItemID),
			ApprovalID: string(event.ApprovalID),
			CallID:     event.CallID,
			Name:       event.Name,
			Reason:     event.Reason,
		})
	case domain.ApprovalResolved:
		return makeLine(record, domain.EventApprovalResolved, approvalResolvedPayload{
			TurnID:     string(event.TurnID),
			ItemID:     string(event.ItemID),
			ApprovalID: string(event.ApprovalID),
			Decision:   event.Decision,
		})
	case domain.ModelRequestRecorded, domain.PolicyDecisionRecorded:
		return Line{}, false, nil
	default:
		return Line{}, false, &Error{Code: CodeUnsupportedEventType, Message: "unsupported event type"}
	}
}

func makeLine(record domain.RecordedEvent, typ string, payload any) (Line, bool, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Line{}, false, invalidLine("invalid payload")
	}
	return Line{
		FormatVersion: FormatVersion,
		Schema:        Schema,
		SessionID:     string(record.SessionID),
		EventID:       string(record.ID),
		CommandID:     string(record.CommandID),
		Sequence:      record.Sequence,
		OccurredAt:    formatTimestamp(record.OccurredAt),
		Type:          typ,
		Payload:       raw,
	}, true, nil
}

func stepRef(turnID domain.TurnID, stepIndex uint32) string {
	return string(turnID) + "/" + strconv.FormatUint(uint64(stepIndex), 10)
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(timestampLayout)
}

func marshalEncoded(line any, payload json.RawMessage) ([]byte, error) {
	if err := ensureObjectPayload(payload); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		return nil, invalidLine("invalid transcript line")
	}
	if bytes.Contains(encoded, []byte{'\n'}) {
		return nil, invalidLine("encoded line contains a newline")
	}
	if len(encoded) > maxLineBytes {
		return nil, &Error{Code: CodeLineLimit, Message: "encoded line exceeds 2 MiB"}
	}
	return encoded, nil
}

func validateFactHeader(line Line) error {
	if line.FormatVersion != FormatVersion {
		return unsupportedFormatVersion()
	}
	if line.Schema != Schema {
		return invalidLine("invalid schema")
	}
	if line.Type == TypeSnapshot || line.Type == TypeComplete || !knownFactType(line.Type) {
		return invalidLine("invalid fact type")
	}
	return nil
}

func validateIntegrityHeader(version int, schema, gotType, wantType string) error {
	if version != FormatVersion {
		return unsupportedFormatVersion()
	}
	if schema != Schema {
		return invalidLine("invalid schema")
	}
	if gotType != wantType {
		return invalidLine("invalid type")
	}
	return nil
}

func peekTypeAndVersion(data []byte) (string, int, error) {
	if !utf8.Valid(data) {
		return "", 0, invalidLine("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var peek struct {
		FormatVersion int    `json:"formatVersion"`
		Type          string `json:"type"`
	}
	if err := decoder.Decode(&peek); err != nil {
		return "", 0, invalidLine("invalid transcript line")
	}
	return peek.Type, peek.FormatVersion, nil
}

func decodeSnapshot(data []byte) (Decoded, error) {
	if err := validateJSONObjectKeys(data, integrityEnvelopeKeys, nil); err != nil {
		return Decoded{}, err
	}
	var line SnapshotLine
	if err := decodeStrict(data, &line); err != nil {
		return Decoded{}, err
	}
	if err := validateIntegrityHeader(line.FormatVersion, line.Schema, line.Type, TypeSnapshot); err != nil {
		return Decoded{}, err
	}
	if err := validateJSONObjectKeys(line.Payload, snapshotPayloadKeys, nil); err != nil {
		return Decoded{}, err
	}
	return Decoded{Snapshot: &line}, nil
}

func decodeComplete(data []byte) (Decoded, error) {
	if err := validateJSONObjectKeys(data, integrityEnvelopeKeys, nil); err != nil {
		return Decoded{}, err
	}
	var line CompleteLine
	if err := decodeStrict(data, &line); err != nil {
		return Decoded{}, err
	}
	if err := validateIntegrityHeader(line.FormatVersion, line.Schema, line.Type, TypeComplete); err != nil {
		return Decoded{}, err
	}
	if err := validateJSONObjectKeys(line.Payload, completePayloadKeys, nil); err != nil {
		return Decoded{}, err
	}
	return Decoded{Complete: &line}, nil
}

func decodeFact(data []byte, typ string) (Decoded, error) {
	if !knownFactType(typ) {
		return Decoded{}, &Error{Code: CodeUnsupportedEventType, Message: "unsupported event type"}
	}
	if err := validateJSONObjectKeys(data, factEnvelopeKeys, nil); err != nil {
		return Decoded{}, err
	}
	var line Line
	if err := decodeStrict(data, &line); err != nil {
		return Decoded{}, err
	}
	if err := validateFactHeader(line); err != nil {
		return Decoded{}, err
	}
	required, optional, ok := factPayloadKeys(line.Type)
	if !ok {
		return Decoded{}, &Error{Code: CodeUnsupportedEventType, Message: "unsupported event type"}
	}
	if err := validateJSONObjectKeys(line.Payload, required, optional); err != nil {
		return Decoded{}, err
	}
	return Decoded{Line: &line}, nil
}

func decodeStrict(data []byte, dest any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return invalidLine("invalid transcript line")
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return err
	}
	return nil
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return invalidLine("line has trailing JSON")
	}
	return nil
}

func ensureObjectPayload(payload json.RawMessage) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return invalidLine("payload must be a JSON object")
	}
	return nil
}

func validateJSONObjectKeys(data []byte, requiredKeys, optionalKeys []string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return invalidLine("JSON value must be an object")
	}
	seen := make(map[string]struct{}, len(requiredKeys)+len(optionalKeys))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return invalidLine("invalid JSON object key")
		}
		key, ok := token.(string)
		if !ok || (!containsString(requiredKeys, key) && !containsString(optionalKeys, key)) {
			return invalidLine("JSON object contains an unknown key")
		}
		if _, exists := seen[key]; exists {
			return invalidLine("JSON object contains a duplicate key")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return invalidLine("invalid JSON object value")
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return invalidLine("invalid JSON object")
	}
	for _, key := range requiredKeys {
		if _, exists := seen[key]; !exists {
			return invalidLine("JSON object is missing a required key")
		}
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

func knownFactType(typ string) bool {
	_, _, ok := factPayloadKeys(typ)
	return ok
}

func factPayloadKeys(typ string) (required, optional []string, ok bool) {
	switch typ {
	case domain.EventSessionCreated:
		return []string{"workspaceRoot"}, nil, true
	case domain.EventSessionClosed:
		return []string{}, nil, true
	case domain.EventTurnStarted:
		return []string{"turnID", "input"}, nil, true
	case domain.EventTurnCompleted:
		return []string{"turnID"}, nil, true
	case domain.EventTurnFailed:
		return []string{"turnID", "code", "message"}, nil, true
	case domain.EventTurnInterrupted:
		return []string{"turnID", "reason"}, nil, true
	case domain.EventAssistantMessageStarted:
		return []string{"turnID", "itemID", "stepIndex", "stepRef"}, nil, true
	case domain.EventAssistantMessageCompleted:
		return []string{"turnID", "itemID", "stepIndex", "stepRef", "text"}, []string{"toolCalls"}, true
	case domain.EventAssistantMessageFailed, domain.EventAssistantMessageInterrupted:
		return []string{"turnID", "itemID", "stepIndex", "stepRef", "code", "message"}, nil, true
	case domain.EventModelUsageRecorded:
		return []string{"turnID", "itemID", "inputTokens", "outputTokens", "cachedInputTokens", "latencyMs", "finishReason", "providerRequestID"}, nil, true
	case domain.EventToolCallStarted:
		return []string{"turnID", "itemID", "callID", "stepIndex", "stepRef", "name", "arguments"}, nil, true
	case domain.EventToolCallCompleted:
		return []string{"turnID", "itemID", "callID", "stepIndex", "stepRef", "content", "truncated"}, nil, true
	case domain.EventToolCallFailed, domain.EventToolCallInterrupted:
		return []string{"turnID", "itemID", "callID", "stepIndex", "stepRef", "code", "message"}, nil, true
	case domain.EventApprovalRequested:
		return []string{"turnID", "itemID", "approvalID", "callID", "name", "reason"}, nil, true
	case domain.EventApprovalResolved:
		return []string{"turnID", "itemID", "approvalID", "decision"}, nil, true
	default:
		return nil, nil, false
	}
}

func invalidLine(message string) error {
	return &Error{Code: CodeInvalidLine, Message: message}
}

func unsupportedFormatVersion() error {
	return &Error{Code: CodeUnsupportedFormatVersion, Message: "unsupported format version"}
}

var (
	factEnvelopeKeys = []string{
		"formatVersion", "schema", "sessionId", "eventId", "commandId", "sequence", "occurredAt", "type", "payload",
	}
	integrityEnvelopeKeys = []string{
		"formatVersion", "schema", "sessionId", "occurredAt", "type", "payload",
	}
	snapshotPayloadKeys = []string{"headSequence", "open", "running", "stability"}
	completePayloadKeys = []string{"headSequence", "factLines", "open", "running"}
)
