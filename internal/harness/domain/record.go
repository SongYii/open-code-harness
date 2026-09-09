package domain

import "time"

type UncommittedEvent struct {
	Event Event
}

type RecordedEvent struct {
	SchemaVersion int
	ID            EventID
	CommandID     CommandID
	SessionID     SessionID
	Sequence      uint64
	OccurredAt    time.Time
	Event         Event
}

func CloneEvent(event Event) (Event, error) {
	switch event := event.(type) {
	case SessionCreated:
		return event, nil
	case TurnStarted:
		return event, nil
	case TurnCompleted:
		return event, nil
	case TurnFailed:
		return event, nil
	case TurnInterrupted:
		return event, nil
	case SessionClosed:
		return event, nil
	case SessionDeleted:
		return event, nil
	case AssistantMessageStarted:
		return event, nil
	case AssistantMessageCompleted:
		cloned := event
		cloned.ToolCalls = cloneToolCallOffers(event.ToolCalls)
		return cloned, nil
	case AssistantMessageFailed:
		return event, nil
	case AssistantMessageInterrupted:
		return event, nil
	case ModelRequestRecorded:
		cloned := event
		cloned.Messages = cloneModelPromptMessages(event.Messages)
		cloned.Tools = cloneToolSchemas(event.Tools)
		return cloned, nil
	case ModelUsageRecorded:
		return event, nil
	case ToolCallStarted:
		return event, nil
	case ToolCallCompleted:
		return event, nil
	case ToolCallFailed:
		return event, nil
	case ToolCallInterrupted:
		return event, nil
	case PolicyDecisionRecorded:
		return event, nil
	case ApprovalRequested:
		return event, nil
	case ApprovalResolved:
		return event, nil
	case ContextCompactionStarted:
		return event, nil
	case ContextCompactionCompleted:
		cloned := event
		cloned.Checkpoint = cloneContextCheckpointRecord(event.Checkpoint)
		return cloned, nil
	case ContextCompactionFailed:
		return event, nil
	case ContextPreparedRecorded:
		return event, nil
	case WorkspaceInstructionsRecorded:
		return cloneWorkspaceInstructionsRecorded(event), nil
	default:
		return nil, domainError(CodeInvalidEvent, "event type cannot be cloned")
	}
}

func cloneContextCheckpointRecord(record ContextCheckpointRecord) ContextCheckpointRecord {
	cloned := record
	if record.InstructionSnapshot != nil {
		snapshot := *record.InstructionSnapshot
		snapshot.Sources = append([]InstructionSource(nil), record.InstructionSnapshot.Sources...)
		cloned.InstructionSnapshot = &snapshot
	}
	return cloned
}

func cloneWorkspaceInstructionsRecorded(event WorkspaceInstructionsRecorded) WorkspaceInstructionsRecorded {
	cloned := event
	cloned.Discovered = append([]InstructionScope(nil), event.Discovered...)
	cloned.Changes = append([]InstructionChange(nil), event.Changes...)
	cloned.Diagnostics = append([]InstructionDiagnostic(nil), event.Diagnostics...)
	return cloned
}

func cloneModelPromptMessages(messages []ModelPromptMessage) []ModelPromptMessage {
	if messages == nil {
		return nil
	}
	cloned := make([]ModelPromptMessage, len(messages))
	for index, message := range messages {
		cloned[index] = message
		cloned[index].ToolCalls = cloneToolCallOffers(message.ToolCalls)
	}
	return cloned
}

func cloneToolCallOffers(offers []ToolCallOffer) []ToolCallOffer {
	if offers == nil {
		return nil
	}
	cloned := make([]ToolCallOffer, len(offers))
	copy(cloned, offers)
	return cloned
}

func cloneToolSchemas(schemas []ToolSchema) []ToolSchema {
	if schemas == nil {
		return nil
	}
	cloned := make([]ToolSchema, len(schemas))
	for index, schema := range schemas {
		cloned[index] = schema
		if schema.InputSchema != nil {
			cloned[index].InputSchema = append([]byte(nil), schema.InputSchema...)
		}
	}
	return cloned
}

func CloneRecordedEvents(records []RecordedEvent) ([]RecordedEvent, error) {
	cloned := make([]RecordedEvent, len(records))
	for index, record := range records {
		event, err := CloneEvent(record.Event)
		if err != nil {
			return nil, err
		}
		cloned[index] = record
		cloned[index].Event = event
	}
	return cloned, nil
}
