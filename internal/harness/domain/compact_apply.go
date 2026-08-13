package domain

import (
	"fmt"
	"math"
	"unicode/utf8"
)

// ApplyCompact independently applies one event to the bounded aggregate.
func ApplyCompact(state CompactSession, record RecordedEvent) (CompactSession, error) {
	if err := validateCompactRecord(record); err != nil {
		return CompactSession{}, err
	}
	if state.Version == math.MaxUint64 || record.Sequence != state.Version+1 {
		return CompactSession{}, domainError(CodeSequenceMismatch, "event sequence does not follow session version")
	}
	record.OccurredAt = record.OccurredAt.UTC()

	switch event := record.Event.(type) {
	case SessionCreated:
		return applyCompactSessionCreated(state, record, event)
	case TurnStarted:
		return applyCompactTurnStarted(state, record, event)
	case TurnCompleted:
		return applyCompactTerminalTurn(state, record, event.TurnID)
	case TurnFailed:
		if !hasRequiredText(event.Code) || !hasRequiredText(event.Message) {
			return CompactSession{}, domainError(CodeInvalidEvent, "turn failure details are required")
		}
		return applyCompactTerminalTurn(state, record, event.TurnID)
	case TurnInterrupted:
		if !hasRequiredText(event.Reason) {
			return CompactSession{}, domainError(CodeInvalidEvent, "interruption reason is required")
		}
		return applyCompactTerminalTurn(state, record, event.TurnID)
	case AssistantMessageStarted:
		return applyCompactAssistantMessageStarted(state, record, event)
	case AssistantMessageCompleted:
		if !utf8.ValidString(event.Text) {
			return CompactSession{}, domainError(CodeInvalidEvent, "assistant message text must be valid UTF-8")
		}
		return applyCompactTerminalItem(state, record, event.TurnID, event.ItemID)
	case AssistantMessageFailed:
		if !hasRequiredText(event.Code) || !utf8.ValidString(event.Message) {
			return CompactSession{}, domainError(CodeInvalidEvent, "item terminal details are invalid")
		}
		return applyCompactTerminalItem(state, record, event.TurnID, event.ItemID)
	case AssistantMessageInterrupted:
		if !hasRequiredText(event.Code) || !utf8.ValidString(event.Message) {
			return CompactSession{}, domainError(CodeInvalidEvent, "item terminal details are invalid")
		}
		return applyCompactTerminalItem(state, record, event.TurnID, event.ItemID)
	case SessionClosed:
		return applyCompactSessionClosed(state, record)
	default:
		return CompactSession{}, domainError(CodeInvalidEvent, "event type cannot be applied")
	}
}

// ReplayCompact independently replays records without retaining terminal data.
func ReplayCompact(records []RecordedEvent) (CompactSession, error) {
	var state CompactSession
	for _, record := range records {
		next, err := ApplyCompact(state, record)
		if err != nil {
			return CompactSession{}, fmt.Errorf("replay sequence %d: %w", record.Sequence, err)
		}
		state = next
	}
	return state, nil
}

func validateCompactRecord(record RecordedEvent) error {
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

func applyCompactSessionCreated(state CompactSession, record RecordedEvent, event SessionCreated) (CompactSession, error) {
	if !state.isPristine() {
		return CompactSession{}, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if !hasRequiredText(event.WorkspaceRoot) {
		return CompactSession{}, domainError(CodeInvalidEvent, "workspace root is required")
	}
	return CompactSession{ID: record.SessionID, Status: SessionStatusActive, Version: record.Sequence, WorkspaceRoot: event.WorkspaceRoot}, nil
}

func applyCompactSessionClosed(state CompactSession, record RecordedEvent) (CompactSession, error) {
	if err := requireCompactActiveSession(state, record.SessionID); err != nil {
		return CompactSession{}, err
	}
	if state.ActiveTurn != nil {
		return CompactSession{}, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	next := state.Clone()
	next.Status = SessionStatusClosed
	next.Version = record.Sequence
	return next, nil
}

func applyCompactTurnStarted(state CompactSession, record RecordedEvent, event TurnStarted) (CompactSession, error) {
	if err := requireCompactActiveSession(state, record.SessionID); err != nil {
		return CompactSession{}, err
	}
	if _, err := ParseTurnID(string(event.TurnID)); err != nil {
		return CompactSession{}, domainError(CodeInvalidEvent, "turn ID is invalid")
	}
	if !hasRequiredText(event.Input) {
		return CompactSession{}, domainError(CodeInvalidEvent, "turn input is required")
	}
	if state.ActiveTurn != nil {
		return CompactSession{}, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	next := state.Clone()
	next.ActiveTurn = &CompactTurn{ID: event.TurnID, Input: event.Input, StartedAt: record.OccurredAt}
	next.Version = record.Sequence
	return next, nil
}

func applyCompactTerminalTurn(state CompactSession, record RecordedEvent, turnID TurnID) (CompactSession, error) {
	turn, err := requireCompactRunningTurn(state, record.SessionID, turnID)
	if err != nil {
		return CompactSession{}, err
	}
	if turn.ActiveItem != nil {
		return CompactSession{}, domainError(CodeInvalidEvent, "turn cannot complete while an item is running")
	}
	if record.OccurredAt.Before(turn.StartedAt) {
		return CompactSession{}, domainError(CodeInvalidEvent, "turn terminal timestamp precedes turn start")
	}
	next := state.Clone()
	next.ActiveTurn = nil
	next.Version = record.Sequence
	return next, nil
}

func applyCompactAssistantMessageStarted(state CompactSession, record RecordedEvent, event AssistantMessageStarted) (CompactSession, error) {
	turn, err := requireCompactRunningTurn(state, record.SessionID, event.TurnID)
	if err != nil {
		return CompactSession{}, err
	}
	if _, err := ParseItemID(string(event.ItemID)); err != nil {
		return CompactSession{}, domainError(CodeInvalidEvent, "item ID is invalid")
	}
	if turn.ActiveItem != nil {
		return CompactSession{}, domainError(CodeInvalidEvent, "an item is already running")
	}
	if record.OccurredAt.Before(turn.StartedAt) {
		return CompactSession{}, domainError(CodeInvalidEvent, "item start timestamp precedes turn start")
	}
	next := state.Clone()
	next.ActiveTurn.ActiveItem = &CompactItem{ID: event.ItemID, TurnID: event.TurnID, Kind: ItemKindAssistantMessage, StartedAt: record.OccurredAt}
	next.Version = record.Sequence
	return next, nil
}

func applyCompactTerminalItem(state CompactSession, record RecordedEvent, turnID TurnID, itemID ItemID) (CompactSession, error) {
	item, err := requireCompactRunningItem(state, record.SessionID, turnID, itemID)
	if err != nil {
		return CompactSession{}, err
	}
	if record.OccurredAt.Before(item.StartedAt) {
		return CompactSession{}, domainError(CodeInvalidEvent, "item terminal timestamp precedes item start")
	}
	next := state.Clone()
	next.ActiveTurn.ActiveItem = nil
	next.Version = record.Sequence
	return next, nil
}

func requireCompactActiveSession(state CompactSession, sessionID SessionID) error {
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

func requireCompactRunningTurn(state CompactSession, sessionID SessionID, turnID TurnID) (CompactTurn, error) {
	if err := requireCompactActiveSession(state, sessionID); err != nil {
		return CompactTurn{}, err
	}
	if _, err := ParseTurnID(string(turnID)); err != nil {
		return CompactTurn{}, domainError(CodeInvalidEvent, "turn ID is invalid")
	}
	if state.ActiveTurn == nil {
		return CompactTurn{}, domainError(CodeTurnNotRunning, "no turn is running")
	}
	if state.ActiveTurn.ID != turnID {
		return CompactTurn{}, domainError(CodeTurnMismatch, "event turn ID does not match active turn")
	}
	return *state.ActiveTurn, nil
}

func requireCompactRunningItem(state CompactSession, sessionID SessionID, turnID TurnID, itemID ItemID) (CompactItem, error) {
	turn, err := requireCompactRunningTurn(state, sessionID, turnID)
	if err != nil {
		return CompactItem{}, err
	}
	if _, err := ParseItemID(string(itemID)); err != nil {
		return CompactItem{}, domainError(CodeInvalidEvent, "item ID is invalid")
	}
	if turn.ActiveItem == nil || turn.ActiveItem.ID != itemID {
		return CompactItem{}, domainError(CodeInvalidEvent, "event item ID does not match active item")
	}
	return *turn.ActiveItem, nil
}
