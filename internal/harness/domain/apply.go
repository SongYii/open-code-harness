package domain

import (
	"math"
	"unicode/utf8"
)

const schemaVersion = 1

func Apply(state Session, record RecordedEvent) (Session, error) {
	if record.SchemaVersion != schemaVersion {
		return Session{}, domainError(CodeInvalidEvent, "unsupported schema version")
	}
	if record.ID == "" || record.SessionID == "" {
		return Session{}, domainError(CodeInvalidID, "event and session IDs are required")
	}
	if record.CommandID == "" {
		return Session{}, domainError(CodeInvalidCommand, "command ID is required")
	}
	if _, err := ParseEventID(string(record.ID)); err != nil {
		return Session{}, domainError(CodeInvalidID, "event ID is invalid")
	}
	if _, err := ParseSessionID(string(record.SessionID)); err != nil {
		return Session{}, domainError(CodeInvalidID, "session ID is invalid")
	}
	if _, err := ParseCommandID(string(record.CommandID)); err != nil {
		return Session{}, domainError(CodeInvalidCommand, "command ID is invalid")
	}
	if record.OccurredAt.IsZero() {
		return Session{}, domainError(CodeInvalidEvent, "event timestamp is required")
	}
	if err := validateRecordedEventIdentityAndTimestamp(record); err != nil {
		return Session{}, err
	}
	if state.Version == math.MaxUint64 {
		return Session{}, domainError(CodeSequenceMismatch, "session version cannot advance")
	}
	record.OccurredAt = record.OccurredAt.UTC()
	if _, ok := record.Event.(SessionCreated); ok && !state.isPristine() {
		return Session{}, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if record.Sequence != state.Version+1 {
		return Session{}, domainError(CodeSequenceMismatch, "event sequence does not follow session version")
	}

	switch event := record.Event.(type) {
	case SessionCreated:
		return applySessionCreated(state, record, event)
	case TurnStarted:
		return applyTurnStarted(state, record, event)
	case TurnCompleted:
		return applyTurnCompleted(state, record, event)
	case TurnFailed:
		return applyTurnFailed(state, record, event)
	case TurnInterrupted:
		return applyTurnInterrupted(state, record, event)
	case AssistantMessageStarted:
		return applyAssistantMessageStarted(state, record, event)
	case AssistantMessageCompleted:
		return applyAssistantMessageCompleted(state, record, event)
	case AssistantMessageFailed:
		return applyAssistantMessageFailed(state, record, event)
	case AssistantMessageInterrupted:
		return applyAssistantMessageInterrupted(state, record, event)
	case SessionClosed:
		return applySessionClosed(state, record)
	default:
		return Session{}, domainError(CodeInvalidEvent, "event type cannot be applied")
	}
}

func applySessionClosed(state Session, record RecordedEvent) (Session, error) {
	if !state.Exists() {
		return Session{}, domainError(CodeSessionNotFound, "session not found")
	}
	if record.SessionID != state.ID {
		return Session{}, domainError(CodeInvalidEvent, "event session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return Session{}, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return Session{}, domainError(CodeInvalidEvent, "session is not active")
	}
	if state.ActiveTurnID != "" {
		return Session{}, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}

	next := state.Clone()
	next.Status = SessionStatusClosed
	next.ActiveTurnID = ""
	next.Version = record.Sequence
	return next, nil
}

func applyTurnStarted(state Session, record RecordedEvent, event TurnStarted) (Session, error) {
	if !state.Exists() {
		return Session{}, domainError(CodeSessionNotFound, "session not found")
	}
	if record.SessionID != state.ID {
		return Session{}, domainError(CodeInvalidEvent, "event session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return Session{}, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return Session{}, domainError(CodeInvalidEvent, "session is not active")
	}
	if _, err := ParseTurnID(string(event.TurnID)); err != nil {
		return Session{}, domainError(CodeInvalidEvent, "turn ID is invalid")
	}
	if !hasRequiredText(event.Input) {
		return Session{}, domainError(CodeInvalidEvent, "turn input is required")
	}
	if _, exists := state.Turns[event.TurnID]; exists {
		return Session{}, domainError(CodeTurnAlreadyExists, "turn already exists")
	}
	if state.ActiveTurnID != "" {
		return Session{}, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}

	next := state.Clone()
	if next.Turns == nil {
		next.Turns = make(map[TurnID]Turn)
	}
	next.Turns[event.TurnID] = Turn{
		ID:        event.TurnID,
		Status:    TurnStatusRunning,
		Input:     event.Input,
		StartedAt: record.OccurredAt,
		ItemOrder: make([]ItemID, 0),
		Items:     make(map[ItemID]Item),
	}
	next.ActiveTurnID = event.TurnID
	next.TurnOrder = append(next.TurnOrder, event.TurnID)
	next.Version = record.Sequence
	return next, nil
}

func applyTurnCompleted(state Session, record RecordedEvent, event TurnCompleted) (Session, error) {
	turn, err := requireRunningTurnForEvent(state, record.SessionID, event.TurnID)
	if err != nil {
		return Session{}, err
	}
	if turn.ActiveItemID != "" {
		return Session{}, domainError(CodeInvalidEvent, "turn cannot complete while an item is running")
	}
	return applyTerminalTurn(state, record, turn, TurnStatusCompleted, "", "", "")
}

func applyTurnFailed(state Session, record RecordedEvent, event TurnFailed) (Session, error) {
	if !hasRequiredText(event.Code) {
		return Session{}, domainError(CodeInvalidEvent, "failure code is required")
	}
	if !hasRequiredText(event.Message) {
		return Session{}, domainError(CodeInvalidEvent, "failure message is required")
	}
	turn, err := requireRunningTurnForEvent(state, record.SessionID, event.TurnID)
	if err != nil {
		return Session{}, err
	}
	if turn.ActiveItemID != "" {
		return Session{}, domainError(CodeInvalidEvent, "turn cannot fail while an item is running")
	}
	return applyTerminalTurn(state, record, turn, TurnStatusFailed, event.Code, event.Message, "")
}

func applyTurnInterrupted(state Session, record RecordedEvent, event TurnInterrupted) (Session, error) {
	if !hasRequiredText(event.Reason) {
		return Session{}, domainError(CodeInvalidEvent, "interruption reason is required")
	}
	turn, err := requireRunningTurnForEvent(state, record.SessionID, event.TurnID)
	if err != nil {
		return Session{}, err
	}
	if turn.ActiveItemID != "" {
		return Session{}, domainError(CodeInvalidEvent, "turn cannot be interrupted while an item is running")
	}
	return applyTerminalTurn(state, record, turn, TurnStatusInterrupted, "", "", event.Reason)
}

func requireRunningTurnForEvent(state Session, sessionID SessionID, turnID TurnID) (Turn, error) {
	if !state.Exists() {
		return Turn{}, domainError(CodeSessionNotFound, "session not found")
	}
	if sessionID != state.ID {
		return Turn{}, domainError(CodeInvalidEvent, "event session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return Turn{}, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return Turn{}, domainError(CodeInvalidEvent, "session is not active")
	}
	if _, err := ParseTurnID(string(turnID)); err != nil {
		return Turn{}, domainError(CodeInvalidEvent, "turn ID is invalid")
	}
	if state.ActiveTurnID == "" {
		return Turn{}, domainError(CodeTurnNotRunning, "no turn is running")
	}
	if state.ActiveTurnID != turnID {
		return Turn{}, domainError(CodeTurnMismatch, "event turn ID does not match active turn")
	}
	turn, ok := state.Turns[state.ActiveTurnID]
	if !ok || turn.Status != TurnStatusRunning {
		return Turn{}, domainError(CodeTurnNotRunning, "active turn is not running")
	}
	if turn.ID != state.ActiveTurnID {
		return Turn{}, domainError(CodeInvalidEvent, "active turn identity is invalid")
	}
	if err := validateTurnItems(turn); err != nil {
		return Turn{}, err
	}
	return turn, nil
}

func applyTerminalTurn(state Session, record RecordedEvent, turn Turn, status TurnStatus, failureCode, failureText, interruptWhy string) (Session, error) {
	if record.OccurredAt.Before(turn.StartedAt) {
		return Session{}, domainError(CodeInvalidEvent, "turn terminal timestamp precedes turn start")
	}
	for _, item := range turn.Items {
		if !item.EndedAt.IsZero() && record.OccurredAt.Before(item.EndedAt) {
			return Session{}, domainError(CodeInvalidEvent, "turn terminal timestamp precedes an item end")
		}
	}

	next := state.Clone()
	turn.Status = status
	turn.EndedAt = record.OccurredAt
	turn.FailureCode = failureCode
	turn.FailureText = failureText
	turn.InterruptWhy = interruptWhy
	next.Turns[turn.ID] = turn
	next.ActiveTurnID = ""
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
	if turn.ActiveItemID != "" {
		return Session{}, domainError(CodeInvalidEvent, "an item is already running")
	}
	if _, exists := turn.Items[event.ItemID]; exists {
		return Session{}, domainError(CodeInvalidEvent, "item already exists")
	}
	if record.OccurredAt.Before(turn.StartedAt) {
		return Session{}, domainError(CodeInvalidEvent, "item start timestamp precedes turn start")
	}
	for _, item := range turn.Items {
		if !item.EndedAt.IsZero() && record.OccurredAt.Before(item.EndedAt) {
			return Session{}, domainError(CodeInvalidEvent, "item start timestamp precedes an item end")
		}
	}

	next := state.Clone()
	turn = next.Turns[event.TurnID]
	if turn.Items == nil {
		turn.Items = make(map[ItemID]Item)
	}
	turn.Items[event.ItemID] = Item{
		ID:        event.ItemID,
		TurnID:    event.TurnID,
		Kind:      ItemKindAssistantMessage,
		Status:    ItemStatusRunning,
		Payload:   AssistantMessagePayload{},
		StartedAt: record.OccurredAt,
	}
	turn.ActiveItemID = event.ItemID
	turn.ItemOrder = append(turn.ItemOrder, event.ItemID)
	next.Turns[event.TurnID] = turn
	next.Version = record.Sequence
	return next, nil
}

func applyAssistantMessageCompleted(state Session, record RecordedEvent, event AssistantMessageCompleted) (Session, error) {
	if !utf8.ValidString(event.Text) {
		return Session{}, domainError(CodeInvalidEvent, "assistant message text must be valid UTF-8")
	}
	return applyTerminalAssistantMessage(state, record, event.TurnID, event.ItemID, ItemStatusCompleted, AssistantMessagePayload{Text: event.Text}, nil)
}

func applyAssistantMessageFailed(state Session, record RecordedEvent, event AssistantMessageFailed) (Session, error) {
	terminal, err := validateItemTerminal(event.Code, event.Message)
	if err != nil {
		return Session{}, err
	}
	return applyTerminalAssistantMessage(state, record, event.TurnID, event.ItemID, ItemStatusFailed, AssistantMessagePayload{}, terminal)
}

func applyAssistantMessageInterrupted(state Session, record RecordedEvent, event AssistantMessageInterrupted) (Session, error) {
	terminal, err := validateItemTerminal(event.Code, event.Message)
	if err != nil {
		return Session{}, err
	}
	return applyTerminalAssistantMessage(state, record, event.TurnID, event.ItemID, ItemStatusInterrupted, AssistantMessagePayload{}, terminal)
}

func applyTerminalAssistantMessage(state Session, record RecordedEvent, turnID TurnID, itemID ItemID, status ItemStatus, payload ItemPayload, terminal *ItemTerminal) (Session, error) {
	turn, err := requireRunningTurnForEvent(state, record.SessionID, turnID)
	if err != nil {
		return Session{}, err
	}
	if _, err := ParseItemID(string(itemID)); err != nil {
		return Session{}, domainError(CodeInvalidEvent, "item ID is invalid")
	}
	if turn.ActiveItemID == "" || turn.ActiveItemID != itemID {
		return Session{}, domainError(CodeInvalidEvent, "event item ID does not match active item")
	}
	item, exists := turn.Items[itemID]
	if !exists || item.Status != ItemStatusRunning {
		return Session{}, domainError(CodeInvalidEvent, "active item is not running")
	}
	if record.OccurredAt.Before(item.StartedAt) {
		return Session{}, domainError(CodeInvalidEvent, "item terminal timestamp precedes item start")
	}

	next := state.Clone()
	turn = next.Turns[turnID]
	item = turn.Items[itemID]
	item.Status = status
	item.Payload = payload
	item.EndedAt = record.OccurredAt
	item.Terminal = terminal
	turn.Items[itemID] = item
	turn.ActiveItemID = ""
	next.Turns[turnID] = turn
	next.Version = record.Sequence
	return next, nil
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

func validateTurnItems(turn Turn) error {
	seen := make(map[ItemID]struct{}, len(turn.ItemOrder))
	for _, itemID := range turn.ItemOrder {
		if _, exists := seen[itemID]; exists {
			return domainError(CodeInvalidEvent, "turn item order contains a duplicate")
		}
		seen[itemID] = struct{}{}
		if _, exists := turn.Items[itemID]; !exists {
			return domainError(CodeInvalidEvent, "turn item order references a missing item")
		}
	}
	if len(seen) != len(turn.Items) {
		return domainError(CodeInvalidEvent, "turn item order does not cover all items")
	}

	var runningItemID ItemID
	for key, item := range turn.Items {
		if key != item.ID || item.TurnID != turn.ID {
			return domainError(CodeInvalidEvent, "turn item identity or ownership is invalid")
		}
		if _, err := ParseItemID(string(item.ID)); err != nil {
			return domainError(CodeInvalidEvent, "turn contains an invalid item ID")
		}
		payload, ok := item.Payload.(AssistantMessagePayload)
		if item.Kind != ItemKindAssistantMessage || !ok || payload.ItemKind() != item.Kind {
			return domainError(CodeInvalidEvent, "turn item kind or payload is invalid")
		}
		if !utf8.ValidString(payload.Text) || item.StartedAt.IsZero() {
			return domainError(CodeInvalidEvent, "assistant message payload or start time is invalid")
		}

		switch item.Status {
		case ItemStatusRunning:
			if runningItemID != "" || !item.EndedAt.IsZero() || item.Terminal != nil || payload.Text != "" {
				return domainError(CodeInvalidEvent, "running item state is invalid")
			}
			runningItemID = item.ID
		case ItemStatusCompleted:
			if item.EndedAt.IsZero() || item.EndedAt.Before(item.StartedAt) || item.Terminal != nil {
				return domainError(CodeInvalidEvent, "completed item state is invalid")
			}
		case ItemStatusFailed, ItemStatusInterrupted:
			if item.EndedAt.IsZero() || item.EndedAt.Before(item.StartedAt) || payload.Text != "" || item.Terminal == nil {
				return domainError(CodeInvalidEvent, "terminal item state is invalid")
			}
			if _, err := validateItemTerminal(item.Terminal.Code, item.Terminal.Message); err != nil {
				return err
			}
		default:
			return domainError(CodeInvalidEvent, "turn item status is invalid")
		}
	}
	if runningItemID == "" {
		if turn.ActiveItemID != "" {
			return domainError(CodeInvalidEvent, "active item ID exists without a running item")
		}
	} else if turn.ActiveItemID != runningItemID {
		return domainError(CodeInvalidEvent, "active item ID does not identify the running item")
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

	return Session{
		ID:            record.SessionID,
		Status:        SessionStatusActive,
		Version:       record.Sequence,
		WorkspaceRoot: event.WorkspaceRoot,
		TurnOrder:     make([]TurnID, 0),
		Turns:         make(map[TurnID]Turn),
	}, nil
}

func domainError(code ErrorCode, message string) error {
	return &DomainError{Code: code, Message: message}
}
