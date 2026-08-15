package domain

import (
	"math"
	"unicode/utf8"
)

const schemaVersion = 1

// Apply independently applies one event to the bounded aggregate.
func Apply(state Session, record RecordedEvent) (Session, error) {
	if err := validateRecord(record); err != nil {
		return Session{}, err
	}
	if state.Version == math.MaxUint64 {
		return Session{}, domainError(CodeSequenceMismatch, "event sequence does not follow session version")
	}
	if _, ok := record.Event.(SessionCreated); ok && !state.isPristine() {
		return Session{}, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if record.Sequence != state.Version+1 {
		return Session{}, domainError(CodeSequenceMismatch, "event sequence does not follow session version")
	}
	record.OccurredAt = record.OccurredAt.UTC()

	switch event := record.Event.(type) {
	case SessionCreated:
		return applySessionCreated(state, record, event)
	case TurnStarted:
		return applyTurnStarted(state, record, event)
	case TurnCompleted:
		return applyTerminalTurn(state, record, event.TurnID)
	case TurnFailed:
		if !hasRequiredText(event.Code) || !hasRequiredText(event.Message) {
			return Session{}, domainError(CodeInvalidEvent, "turn failure details are required")
		}
		return applyTerminalTurn(state, record, event.TurnID)
	case TurnInterrupted:
		if !hasRequiredText(event.Reason) {
			return Session{}, domainError(CodeInvalidEvent, "interruption reason is required")
		}
		return applyTerminalTurn(state, record, event.TurnID)
	case AssistantMessageStarted:
		return applyAssistantMessageStarted(state, record, event)
	case AssistantMessageCompleted:
		if !utf8.ValidString(event.Text) {
			return Session{}, domainError(CodeInvalidEvent, "assistant message text must be valid UTF-8")
		}
		return applyTerminalItem(state, record, event.TurnID, event.ItemID)
	case AssistantMessageFailed:
		if !hasRequiredText(event.Code) || !utf8.ValidString(event.Message) {
			return Session{}, domainError(CodeInvalidEvent, "item terminal details are invalid")
		}
		return applyTerminalItem(state, record, event.TurnID, event.ItemID)
	case AssistantMessageInterrupted:
		if !hasRequiredText(event.Code) || !utf8.ValidString(event.Message) {
			return Session{}, domainError(CodeInvalidEvent, "item terminal details are invalid")
		}
		return applyTerminalItem(state, record, event.TurnID, event.ItemID)
	case SessionClosed:
		return applySessionClosed(state, record)
	default:
		return Session{}, domainError(CodeInvalidEvent, "event type cannot be applied")
	}
}

func validateRecord(record RecordedEvent) error {
	if record.SchemaVersion != schemaVersion {
		return domainError(CodeInvalidEvent, "unsupported schema version")
	}
	if record.ID == "" || record.SessionID == "" {
		return domainError(CodeInvalidID, "event and session IDs are required")
	}
	if record.CommandID == "" {
		return domainError(CodeInvalidCommand, "command ID is required")
	}
	if _, err := ParseEventID(string(record.ID)); err != nil {
		return domainError(CodeInvalidID, "event ID is invalid")
	}
	if _, err := ParseSessionID(string(record.SessionID)); err != nil {
		return domainError(CodeInvalidID, "session ID is invalid")
	}
	if _, err := ParseCommandID(string(record.CommandID)); err != nil {
		return domainError(CodeInvalidCommand, "command ID is invalid")
	}
	if record.OccurredAt.IsZero() {
		return domainError(CodeInvalidEvent, "event timestamp is required")
	}
	if err := validateRecordedEventIdentityAndTimestamp(record); err != nil {
		return domainError(CodeInvalidEvent, "event metadata is invalid")
	}
	return nil
}

func applySessionCreated(state Session, record RecordedEvent, event SessionCreated) (Session, error) {
	if !state.isPristine() {
		return Session{}, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if !hasRequiredText(event.WorkspaceRoot) {
		return Session{}, domainError(CodeInvalidEvent, "workspace root is required")
	}
	return Session{ID: record.SessionID, Status: SessionStatusActive, Version: record.Sequence, WorkspaceRoot: event.WorkspaceRoot}, nil
}

func applySessionClosed(state Session, record RecordedEvent) (Session, error) {
	if err := requireActiveSession(state, record.SessionID); err != nil {
		return Session{}, err
	}
	if state.ActiveTurn != nil {
		return Session{}, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	next := state.Clone()
	next.Status = SessionStatusClosed
	next.Version = record.Sequence
	return next, nil
}

func applyTurnStarted(state Session, record RecordedEvent, event TurnStarted) (Session, error) {
	if err := requireActiveSession(state, record.SessionID); err != nil {
		return Session{}, err
	}
	if _, err := ParseTurnID(string(event.TurnID)); err != nil {
		return Session{}, domainError(CodeInvalidEvent, "turn ID is invalid")
	}
	if !hasRequiredText(event.Input) {
		return Session{}, domainError(CodeInvalidEvent, "turn input is required")
	}
	if state.ActiveTurn != nil {
		return Session{}, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	next := state.Clone()
	next.ActiveTurn = &Turn{ID: event.TurnID, Input: event.Input, StartedAt: record.OccurredAt, LastTransitionAt: record.OccurredAt}
	next.Version = record.Sequence
	return next, nil
}

func applyTerminalTurn(state Session, record RecordedEvent, turnID TurnID) (Session, error) {
	turn, err := requireRunningTurnForEvent(state, record.SessionID, turnID)
	if err != nil {
		return Session{}, err
	}
	if turn.ActiveItem != nil {
		return Session{}, domainError(CodeInvalidEvent, "turn cannot complete while an item is running")
	}
	if record.OccurredAt.Before(turn.LastTransitionAt) {
		return Session{}, domainError(CodeInvalidEvent, "turn terminal timestamp precedes turn start")
	}
	next := state.Clone()
	next.ActiveTurn = nil
	next.Version = record.Sequence
	return next, nil
}

func applyAssistantMessageStarted(state Session, record RecordedEvent, event AssistantMessageStarted) (Session, error) {
	turn, err := requireRunningTurnForEvent(state, record.SessionID, event.TurnID)
	if err != nil {
		return Session{}, err
	}
	if _, err := ParseItemID(string(event.ItemID)); err != nil {
		return Session{}, domainError(CodeInvalidEvent, "item ID is invalid")
	}
	if turn.ActiveItem != nil {
		return Session{}, domainError(CodeInvalidEvent, "an item is already running")
	}
	if record.OccurredAt.Before(turn.LastTransitionAt) {
		return Session{}, domainError(CodeInvalidEvent, "item start timestamp precedes turn start")
	}
	next := state.Clone()
	next.ActiveTurn.ActiveItem = &Item{ID: event.ItemID, TurnID: event.TurnID, Kind: ItemKindAssistantMessage, StartedAt: record.OccurredAt}
	next.Version = record.Sequence
	return next, nil
}

func applyTerminalItem(state Session, record RecordedEvent, turnID TurnID, itemID ItemID) (Session, error) {
	item, err := requireRunningItemForEvent(state, record.SessionID, turnID, itemID)
	if err != nil {
		return Session{}, err
	}
	if record.OccurredAt.Before(item.StartedAt) {
		return Session{}, domainError(CodeInvalidEvent, "item terminal timestamp precedes item start")
	}
	next := state.Clone()
	next.ActiveTurn.ActiveItem = nil
	next.ActiveTurn.LastTransitionAt = record.OccurredAt
	next.Version = record.Sequence
	return next, nil
}

func requireActiveSession(state Session, sessionID SessionID) error {
	if !state.Exists() {
		return domainError(CodeSessionNotFound, "session not found")
	}
	if sessionID != state.ID {
		return domainError(CodeInvalidEvent, "event session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return domainError(CodeInvalidEvent, "session is not active")
	}
	return nil
}

func requireRunningTurnForEvent(state Session, sessionID SessionID, turnID TurnID) (Turn, error) {
	if err := requireActiveSession(state, sessionID); err != nil {
		return Turn{}, err
	}
	if _, err := ParseTurnID(string(turnID)); err != nil {
		return Turn{}, domainError(CodeInvalidEvent, "turn ID is invalid")
	}
	if state.ActiveTurn == nil {
		return Turn{}, domainError(CodeTurnNotRunning, "no turn is running")
	}
	if state.ActiveTurn.ID != turnID {
		return Turn{}, domainError(CodeTurnMismatch, "event turn ID does not match active turn")
	}
	return *state.ActiveTurn, nil
}

func requireRunningItemForEvent(state Session, sessionID SessionID, turnID TurnID, itemID ItemID) (Item, error) {
	turn, err := requireRunningTurnForEvent(state, sessionID, turnID)
	if err != nil {
		return Item{}, err
	}
	if _, err := ParseItemID(string(itemID)); err != nil {
		return Item{}, domainError(CodeInvalidEvent, "item ID is invalid")
	}
	if turn.ActiveItem == nil || turn.ActiveItem.ID != itemID {
		return Item{}, domainError(CodeInvalidEvent, "event item ID does not match active item")
	}
	return *turn.ActiveItem, nil
}

func validateItemTerminal(code, message string) (*ItemTerminal, error) {
	if !hasRequiredText(code) {
		return nil, domainError(CodeInvalidEvent, "item terminal code is required")
	}
	if !utf8.ValidString(message) {
		return nil, domainError(CodeInvalidEvent, "item terminal message must be valid UTF-8")
	}
	return &ItemTerminal{Code: code, Message: message}, nil
}
