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
	case AssistantMessageStarted:
		return event, nil
	case AssistantMessageCompleted:
		return event, nil
	case AssistantMessageFailed:
		return event, nil
	case AssistantMessageInterrupted:
		return event, nil
	default:
		return nil, domainError(CodeInvalidEvent, "event type cannot be cloned")
	}
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
