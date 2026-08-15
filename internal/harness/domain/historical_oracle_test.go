package domain

import (
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

// Test-only frozen v1 full-history oracle. Production Session is compact.
type HistoricalItem struct {
	ID        ItemID
	TurnID    TurnID
	Kind      ItemKind
	Status    ItemStatus
	Payload   ItemPayload
	StartedAt time.Time
	EndedAt   time.Time
	Terminal  *ItemTerminal
}

func (item HistoricalItem) Clone() HistoricalItem {
	clone := item
	if item.Payload != nil {
		clone.Payload = item.Payload.cloneItemPayload()
	}
	if item.Terminal != nil {
		terminal := *item.Terminal
		clone.Terminal = &terminal
	}
	return clone
}

type HistoricalTurn struct {
	ID           TurnID
	Status       TurnStatus
	Input        string
	StartedAt    time.Time
	EndedAt      time.Time
	FailureCode  string
	FailureText  string
	InterruptWhy string
	ActiveItemID ItemID
	ItemOrder    []ItemID
	Items        map[ItemID]HistoricalItem
}

func (turn HistoricalTurn) Clone() HistoricalTurn {
	clone := turn
	if turn.ItemOrder != nil {
		clone.ItemOrder = make([]ItemID, len(turn.ItemOrder))
		copy(clone.ItemOrder, turn.ItemOrder)
	}
	if turn.Items != nil {
		clone.Items = make(map[ItemID]HistoricalItem, len(turn.Items))
		for id, item := range turn.Items {
			clone.Items[id] = item.Clone()
		}
	}
	return clone
}

type HistoricalSession struct {
	ID            SessionID
	Status        SessionStatus
	Version       uint64
	WorkspaceRoot string
	ActiveTurnID  TurnID
	TurnOrder     []TurnID
	Turns         map[TurnID]HistoricalTurn
}

func (s HistoricalSession) Exists() bool { return s.ID != "" }

func (s HistoricalSession) Clone() HistoricalSession {
	clone := s
	if s.TurnOrder != nil {
		clone.TurnOrder = make([]TurnID, len(s.TurnOrder))
		copy(clone.TurnOrder, s.TurnOrder)
	}
	if s.Turns != nil {
		clone.Turns = make(map[TurnID]HistoricalTurn, len(s.Turns))
		for id, turn := range s.Turns {
			clone.Turns[id] = turn.Clone()
		}
	}
	return clone
}

func (s HistoricalSession) isPristine() bool {
	return s.ID == "" &&
		s.Status == "" &&
		s.Version == 0 &&
		s.WorkspaceRoot == "" &&
		s.ActiveTurnID == "" &&
		s.TurnOrder == nil &&
		s.Turns == nil
}

func HistoricalApply(state HistoricalSession, record RecordedEvent) (HistoricalSession, error) {
	if record.SchemaVersion != schemaVersion {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "unsupported schema version")
	}
	if record.ID == "" || record.SessionID == "" {
		return HistoricalSession{}, domainError(CodeInvalidID, "event and session IDs are required")
	}
	if record.CommandID == "" {
		return HistoricalSession{}, domainError(CodeInvalidCommand, "command ID is required")
	}
	if _, err := ParseEventID(string(record.ID)); err != nil {
		return HistoricalSession{}, domainError(CodeInvalidID, "event ID is invalid")
	}
	if _, err := ParseSessionID(string(record.SessionID)); err != nil {
		return HistoricalSession{}, domainError(CodeInvalidID, "session ID is invalid")
	}
	if _, err := ParseCommandID(string(record.CommandID)); err != nil {
		return HistoricalSession{}, domainError(CodeInvalidCommand, "command ID is invalid")
	}
	if record.OccurredAt.IsZero() {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "event timestamp is required")
	}
	if err := validateRecordedEventIdentityAndTimestamp(record); err != nil {
		return HistoricalSession{}, err
	}
	if state.Version == math.MaxUint64 {
		return HistoricalSession{}, domainError(CodeSequenceMismatch, "session version cannot advance")
	}
	record.OccurredAt = record.OccurredAt.UTC()
	if _, ok := record.Event.(SessionCreated); ok && !state.isPristine() {
		return HistoricalSession{}, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if record.Sequence != state.Version+1 {
		return HistoricalSession{}, domainError(CodeSequenceMismatch, "event sequence does not follow session version")
	}

	switch event := record.Event.(type) {
	case SessionCreated:
		return historical_applySessionCreated(state, record, event)
	case TurnStarted:
		return historical_applyTurnStarted(state, record, event)
	case TurnCompleted:
		return historical_applyTurnCompleted(state, record, event)
	case TurnFailed:
		return historical_applyTurnFailed(state, record, event)
	case TurnInterrupted:
		return historical_applyTurnInterrupted(state, record, event)
	case AssistantMessageStarted:
		return historical_applyAssistantMessageStarted(state, record, event)
	case AssistantMessageCompleted:
		return historical_applyAssistantMessageCompleted(state, record, event)
	case AssistantMessageFailed:
		return historical_applyAssistantMessageFailed(state, record, event)
	case AssistantMessageInterrupted:
		return historical_applyAssistantMessageInterrupted(state, record, event)
	case SessionClosed:
		return historical_applySessionClosed(state, record)
	default:
		return HistoricalSession{}, domainError(CodeInvalidEvent, "event type cannot be applied")
	}
}

func historical_applySessionClosed(state HistoricalSession, record RecordedEvent) (HistoricalSession, error) {
	if !state.Exists() {
		return HistoricalSession{}, domainError(CodeSessionNotFound, "session not found")
	}
	if record.SessionID != state.ID {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "event session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return HistoricalSession{}, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "session is not active")
	}
	if state.ActiveTurnID != "" {
		return HistoricalSession{}, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}

	next := state.Clone()
	next.Status = SessionStatusClosed
	next.ActiveTurnID = ""
	next.Version = record.Sequence
	return next, nil
}

func historical_applyTurnStarted(state HistoricalSession, record RecordedEvent, event TurnStarted) (HistoricalSession, error) {
	if !state.Exists() {
		return HistoricalSession{}, domainError(CodeSessionNotFound, "session not found")
	}
	if record.SessionID != state.ID {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "event session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return HistoricalSession{}, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "session is not active")
	}
	if _, err := ParseTurnID(string(event.TurnID)); err != nil {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "turn ID is invalid")
	}
	if !hasRequiredText(event.Input) {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "turn input is required")
	}
	if _, exists := state.Turns[event.TurnID]; exists {
		return HistoricalSession{}, domainError(CodeTurnAlreadyExists, "turn already exists")
	}
	if state.ActiveTurnID != "" {
		return HistoricalSession{}, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}

	next := state.Clone()
	if next.Turns == nil {
		next.Turns = make(map[TurnID]HistoricalTurn)
	}
	next.Turns[event.TurnID] = HistoricalTurn{
		ID:        event.TurnID,
		Status:    TurnStatusRunning,
		Input:     event.Input,
		StartedAt: record.OccurredAt,
		ItemOrder: make([]ItemID, 0),
		Items:     make(map[ItemID]HistoricalItem),
	}
	next.ActiveTurnID = event.TurnID
	next.TurnOrder = append(next.TurnOrder, event.TurnID)
	next.Version = record.Sequence
	return next, nil
}

func historical_applyTurnCompleted(state HistoricalSession, record RecordedEvent, event TurnCompleted) (HistoricalSession, error) {
	turn, err := historical_requireRunningTurnForEvent(state, record.SessionID, event.TurnID)
	if err != nil {
		return HistoricalSession{}, err
	}
	if err := historical_rejectRunningItemForEvent(turn, "turn cannot complete while an item is running"); err != nil {
		return HistoricalSession{}, err
	}
	return historical_applyTerminalTurn(state, record, turn, TurnStatusCompleted, "", "", "")
}

func historical_applyTurnFailed(state HistoricalSession, record RecordedEvent, event TurnFailed) (HistoricalSession, error) {
	if !hasRequiredText(event.Code) {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "failure code is required")
	}
	if !hasRequiredText(event.Message) {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "failure message is required")
	}
	turn, err := historical_requireRunningTurnForEvent(state, record.SessionID, event.TurnID)
	if err != nil {
		return HistoricalSession{}, err
	}
	if err := historical_rejectRunningItemForEvent(turn, "turn cannot fail while an item is running"); err != nil {
		return HistoricalSession{}, err
	}
	return historical_applyTerminalTurn(state, record, turn, TurnStatusFailed, event.Code, event.Message, "")
}

func historical_applyTurnInterrupted(state HistoricalSession, record RecordedEvent, event TurnInterrupted) (HistoricalSession, error) {
	if !hasRequiredText(event.Reason) {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "interruption reason is required")
	}
	turn, err := historical_requireRunningTurnForEvent(state, record.SessionID, event.TurnID)
	if err != nil {
		return HistoricalSession{}, err
	}
	if err := historical_rejectRunningItemForEvent(turn, "turn cannot be interrupted while an item is running"); err != nil {
		return HistoricalSession{}, err
	}
	return historical_applyTerminalTurn(state, record, turn, TurnStatusInterrupted, "", "", event.Reason)
}

func historical_requireRunningTurnForEvent(state HistoricalSession, sessionID SessionID, turnID TurnID) (HistoricalTurn, error) {
	if !state.Exists() {
		return HistoricalTurn{}, domainError(CodeSessionNotFound, "session not found")
	}
	if sessionID != state.ID {
		return HistoricalTurn{}, domainError(CodeInvalidEvent, "event session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return HistoricalTurn{}, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return HistoricalTurn{}, domainError(CodeInvalidEvent, "session is not active")
	}
	if _, err := ParseTurnID(string(turnID)); err != nil {
		return HistoricalTurn{}, domainError(CodeInvalidEvent, "turn ID is invalid")
	}
	if state.ActiveTurnID == "" {
		return HistoricalTurn{}, domainError(CodeTurnNotRunning, "no turn is running")
	}
	if state.ActiveTurnID != turnID {
		return HistoricalTurn{}, domainError(CodeTurnMismatch, "event turn ID does not match active turn")
	}
	turn, ok := state.Turns[state.ActiveTurnID]
	if !ok || turn.Status != TurnStatusRunning {
		return HistoricalTurn{}, domainError(CodeTurnNotRunning, "active turn is not running")
	}
	if turn.ID != state.ActiveTurnID {
		return HistoricalTurn{}, domainError(CodeInvalidEvent, "active turn identity is invalid")
	}
	if err := historical_validateTurnItems(turn); err != nil {
		return HistoricalTurn{}, err
	}
	return turn, nil
}

func historical_applyTerminalTurn(state HistoricalSession, record RecordedEvent, turn HistoricalTurn, status TurnStatus, failureCode, failureText, interruptWhy string) (HistoricalSession, error) {
	if record.OccurredAt.Before(turn.StartedAt) {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "turn terminal timestamp precedes turn start")
	}
	for _, item := range turn.Items {
		if !item.EndedAt.IsZero() && record.OccurredAt.Before(item.EndedAt) {
			return HistoricalSession{}, domainError(CodeInvalidEvent, "turn terminal timestamp precedes an item end")
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

func historical_applyAssistantMessageStarted(state HistoricalSession, record RecordedEvent, event AssistantMessageStarted) (HistoricalSession, error) {
	turn, err := historical_requireRunningTurnForEvent(state, record.SessionID, event.TurnID)
	if err != nil {
		return HistoricalSession{}, err
	}
	if _, err := ParseItemID(string(event.ItemID)); err != nil {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "item ID is invalid")
	}
	if turn.ActiveItemID != "" {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "an item is already running")
	}
	if _, exists := turn.Items[event.ItemID]; exists {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "item already exists")
	}
	if record.OccurredAt.Before(turn.StartedAt) {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "item start timestamp precedes turn start")
	}
	for _, item := range turn.Items {
		if !item.EndedAt.IsZero() && record.OccurredAt.Before(item.EndedAt) {
			return HistoricalSession{}, domainError(CodeInvalidEvent, "item start timestamp precedes an item end")
		}
	}

	next := state.Clone()
	turn = next.Turns[event.TurnID]
	if turn.Items == nil {
		turn.Items = make(map[ItemID]HistoricalItem)
	}
	turn.Items[event.ItemID] = HistoricalItem{
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

func historical_applyAssistantMessageCompleted(state HistoricalSession, record RecordedEvent, event AssistantMessageCompleted) (HistoricalSession, error) {
	if !utf8.ValidString(event.Text) {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "assistant message text must be valid UTF-8")
	}
	return historical_applyTerminalAssistantMessage(state, record, event.TurnID, event.ItemID, ItemStatusCompleted, AssistantMessagePayload{Text: event.Text}, nil)
}

func historical_applyAssistantMessageFailed(state HistoricalSession, record RecordedEvent, event AssistantMessageFailed) (HistoricalSession, error) {
	terminal, err := historical_validateItemTerminal(event.Code, event.Message)
	if err != nil {
		return HistoricalSession{}, err
	}
	return historical_applyTerminalAssistantMessage(state, record, event.TurnID, event.ItemID, ItemStatusFailed, AssistantMessagePayload{}, terminal)
}

func historical_applyAssistantMessageInterrupted(state HistoricalSession, record RecordedEvent, event AssistantMessageInterrupted) (HistoricalSession, error) {
	terminal, err := historical_validateItemTerminal(event.Code, event.Message)
	if err != nil {
		return HistoricalSession{}, err
	}
	return historical_applyTerminalAssistantMessage(state, record, event.TurnID, event.ItemID, ItemStatusInterrupted, AssistantMessagePayload{}, terminal)
}

func historical_applyTerminalAssistantMessage(state HistoricalSession, record RecordedEvent, turnID TurnID, itemID ItemID, status ItemStatus, payload ItemPayload, terminal *ItemTerminal) (HistoricalSession, error) {
	turn, item, err := historical_requireRunningItemForEvent(state, record.SessionID, turnID, itemID)
	if err != nil {
		return HistoricalSession{}, err
	}
	if record.OccurredAt.Before(item.StartedAt) {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "item terminal timestamp precedes item start")
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

func historical_rejectRunningItemForEvent(turn HistoricalTurn, message string) error {
	if turn.ActiveItemID != "" {
		return domainError(CodeInvalidEvent, message)
	}
	return nil
}

func historical_requireRunningItemForEvent(state HistoricalSession, sessionID SessionID, turnID TurnID, itemID ItemID) (HistoricalTurn, HistoricalItem, error) {
	turn, err := historical_requireRunningTurnForEvent(state, sessionID, turnID)
	if err != nil {
		return HistoricalTurn{}, HistoricalItem{}, err
	}
	if _, err := ParseItemID(string(itemID)); err != nil {
		return HistoricalTurn{}, HistoricalItem{}, domainError(CodeInvalidEvent, "item ID is invalid")
	}
	if turn.ActiveItemID == "" || turn.ActiveItemID != itemID {
		return HistoricalTurn{}, HistoricalItem{}, domainError(CodeInvalidEvent, "event item ID does not match active item")
	}
	item, exists := turn.Items[itemID]
	if !exists || item.Status != ItemStatusRunning {
		return HistoricalTurn{}, HistoricalItem{}, domainError(CodeInvalidEvent, "active item is not running")
	}
	return turn, item, nil
}

func historical_validateItemTerminal(code, message string) (*ItemTerminal, error) {
	if !hasRequiredText(code) {
		return nil, domainError(CodeInvalidEvent, "item terminal code is required")
	}
	if !utf8.ValidString(message) {
		return nil, domainError(CodeInvalidEvent, "item terminal message must be valid UTF-8")
	}
	return &ItemTerminal{Code: code, Message: message}, nil
}

func historical_validateTurnItems(turn HistoricalTurn) error {
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
			if _, err := historical_validateItemTerminal(item.Terminal.Code, item.Terminal.Message); err != nil {
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

func historical_applySessionCreated(state HistoricalSession, record RecordedEvent, event SessionCreated) (HistoricalSession, error) {
	if !state.isPristine() {
		return HistoricalSession{}, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if !hasRequiredText(event.WorkspaceRoot) {
		return HistoricalSession{}, domainError(CodeInvalidEvent, "workspace root is required")
	}

	return HistoricalSession{
		ID:            record.SessionID,
		Status:        SessionStatusActive,
		Version:       record.Sequence,
		WorkspaceRoot: event.WorkspaceRoot,
		TurnOrder:     make([]TurnID, 0),
		Turns:         make(map[TurnID]HistoricalTurn),
	}, nil
}

func HistoricalDecide(state HistoricalSession, command Command) ([]UncommittedEvent, error) {
	switch command := command.(type) {
	case CreateSession:
		return historical_decideCreateSession(state, command)
	case StartTurn:
		return historical_decideStartTurn(state, command)
	case StartAssistantTurn:
		return historical_decideStartAssistantTurn(state, command)
	case CompleteTurn:
		return historical_decideCompleteTurn(state, command)
	case FailTurn:
		return historical_decideFailTurn(state, command)
	case InterruptTurn:
		return historical_decideInterruptTurn(state, command)
	case StartAssistantMessage:
		return historical_decideStartAssistantMessage(state, command)
	case CompleteAssistantTurn:
		return historical_decideCompleteAssistantTurn(state, command)
	case FailAssistantTurn:
		return historical_decideFailAssistantTurn(state, command)
	case InterruptAssistantTurn:
		return historical_decideInterruptAssistantTurn(state, command)
	case CloseSession:
		return historical_decideCloseSession(state, command)
	default:
		return nil, domainError(CodeInvalidCommand, "command type cannot be decided")
	}
}

func historicalCheckStartAssistantTurnEligibility(state HistoricalSession) error {
	if !state.Exists() {
		return domainError(CodeSessionNotFound, "session not found")
	}
	if err := historical_validateSessionStructure(state); err != nil {
		return err
	}
	if state.Status == SessionStatusClosed {
		return domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return domainError(CodeInvalidCommand, "session is not active")
	}
	if state.ActiveTurnID != "" {
		turn := state.Turns[state.ActiveTurnID]
		if turn.ActiveItemID != "" {
			return domainError(CodeItemAlreadyRunning, "an item is already running")
		}
		return domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return nil
}

func historical_decideStartAssistantTurn(state HistoricalSession, command StartAssistantTurn) ([]UncommittedEvent, error) {
	if err := historicalCheckStartAssistantTurnEligibility(state); err != nil {
		return nil, err
	}
	if err := validateCommandSessionID(command.SessionID); err != nil {
		return nil, err
	}
	if command.SessionID != state.ID {
		return nil, domainError(CodeInvalidCommand, "command session ID does not match state")
	}
	if err := validateCommandTurnID(command.TurnID); err != nil {
		return nil, err
	}
	if err := validateCommandItemID(command.ItemID); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Input, "turn input is required"); err != nil {
		return nil, err
	}
	if _, exists := state.Turns[command.TurnID]; exists {
		return nil, domainError(CodeTurnAlreadyExists, "turn already exists")
	}
	for _, turn := range state.Turns {
		if _, exists := turn.Items[command.ItemID]; exists {
			return nil, domainError(CodeItemAlreadyExists, "item already exists")
		}
	}
	return startAssistantTurnEvents(command.TurnID, command.ItemID, command.Input), nil
}

func historical_validateSessionStructure(state HistoricalSession) error {
	if _, err := ParseSessionID(string(state.ID)); err != nil || state.Version == 0 || !hasRequiredText(state.WorkspaceRoot) {
		return domainError(CodeInvalidCommand, "session structure is invalid")
	}
	if state.Status != SessionStatusActive && state.Status != SessionStatusClosed {
		return domainError(CodeInvalidCommand, "session status is invalid")
	}
	if state.TurnOrder == nil || state.Turns == nil {
		return domainError(CodeInvalidCommand, "session turn containers are invalid")
	}
	seen := make(map[TurnID]struct{}, len(state.TurnOrder))
	runningTurnID := TurnID("")
	expectedVersion := uint64(1) // session.created
	advanceVersion := func() bool {
		if expectedVersion == math.MaxUint64 {
			return false
		}
		expectedVersion++
		return true
	}
	for _, turnID := range state.TurnOrder {
		if _, duplicate := seen[turnID]; duplicate {
			return domainError(CodeInvalidCommand, "turn order contains a duplicate")
		}
		seen[turnID] = struct{}{}
		if _, exists := state.Turns[turnID]; !exists {
			return domainError(CodeInvalidCommand, "turn order references a missing turn")
		}
	}
	if len(seen) != len(state.Turns) {
		return domainError(CodeInvalidCommand, "turn order does not cover every turn")
	}
	for key, turn := range state.Turns {
		if key != turn.ID {
			return domainError(CodeInvalidCommand, "turn identity is invalid")
		}
		if _, err := ParseTurnID(string(turn.ID)); err != nil || !hasRequiredText(turn.Input) || !historical_validStateTimestamp(turn.StartedAt) {
			return domainError(CodeInvalidCommand, "turn structure is invalid")
		}
		if !turn.EndedAt.IsZero() && !historical_validStateTimestamp(turn.EndedAt) {
			return domainError(CodeInvalidCommand, "turn terminal timestamp is invalid")
		}
		if !advanceVersion() { // turn.started
			return domainError(CodeInvalidCommand, "session version shape overflows")
		}
		if turn.ItemOrder == nil || turn.Items == nil {
			return domainError(CodeInvalidCommand, "turn item containers are invalid")
		}
		if err := historical_validateTurnItems(turn); err != nil {
			return domainError(CodeInvalidCommand, "turn item structure is invalid")
		}
		var lastItemEnd time.Time
		for index, itemID := range turn.ItemOrder {
			item := turn.Items[itemID]
			if !historical_validStateTimestamp(item.StartedAt) || (!item.EndedAt.IsZero() && !historical_validStateTimestamp(item.EndedAt)) {
				return domainError(CodeInvalidCommand, "item timestamp is invalid")
			}
			if item.StartedAt.Before(turn.StartedAt) || (!lastItemEnd.IsZero() && item.StartedAt.Before(lastItemEnd)) {
				return domainError(CodeInvalidCommand, "turn item timeline is invalid")
			}
			if item.Status == ItemStatusRunning && index != len(turn.ItemOrder)-1 {
				return domainError(CodeInvalidCommand, "running item is not last in turn order")
			}
			if !item.EndedAt.IsZero() {
				lastItemEnd = item.EndedAt
			}
			if !advanceVersion() { // assistant.message.started
				return domainError(CodeInvalidCommand, "session version shape overflows")
			}
			if item.Status != ItemStatusRunning && !advanceVersion() { // one HistoricalItem terminal event
				return domainError(CodeInvalidCommand, "session version shape overflows")
			}
		}
		switch turn.Status {
		case TurnStatusRunning:
			if runningTurnID != "" || !turn.EndedAt.IsZero() || turn.FailureCode != "" || turn.FailureText != "" || turn.InterruptWhy != "" {
				return domainError(CodeInvalidCommand, "running turn structure is invalid")
			}
			runningTurnID = turn.ID
		case TurnStatusCompleted:
			if turn.EndedAt.IsZero() || turn.EndedAt.Before(turn.StartedAt) || turn.FailureCode != "" || turn.FailureText != "" || turn.InterruptWhy != "" || turn.ActiveItemID != "" {
				return domainError(CodeInvalidCommand, "completed turn structure is invalid")
			}
		case TurnStatusFailed:
			if turn.EndedAt.IsZero() || turn.EndedAt.Before(turn.StartedAt) || !hasRequiredText(turn.FailureCode) || !hasRequiredText(turn.FailureText) || turn.InterruptWhy != "" || turn.ActiveItemID != "" {
				return domainError(CodeInvalidCommand, "failed turn structure is invalid")
			}
		case TurnStatusInterrupted:
			if turn.EndedAt.IsZero() || turn.EndedAt.Before(turn.StartedAt) || !hasRequiredText(turn.InterruptWhy) || turn.FailureCode != "" || turn.FailureText != "" || turn.ActiveItemID != "" {
				return domainError(CodeInvalidCommand, "interrupted turn structure is invalid")
			}
		default:
			return domainError(CodeInvalidCommand, "turn status is invalid")
		}
		if turn.Status != TurnStatusRunning && !lastItemEnd.IsZero() && turn.EndedAt.Before(lastItemEnd) {
			return domainError(CodeInvalidCommand, "turn ended before its final item")
		}
		if turn.Status != TurnStatusRunning && !advanceVersion() { // one HistoricalTurn terminal event
			return domainError(CodeInvalidCommand, "session version shape overflows")
		}
	}
	if runningTurnID == "" {
		if state.ActiveTurnID != "" {
			return domainError(CodeInvalidCommand, "active turn ID exists without a running turn")
		}
	} else if state.ActiveTurnID != runningTurnID {
		return domainError(CodeInvalidCommand, "active turn ID does not identify the running turn")
	}
	if state.Status == SessionStatusClosed && runningTurnID != "" {
		return domainError(CodeInvalidCommand, "closed session contains a running turn")
	}
	if state.Status == SessionStatusClosed && !advanceVersion() { // session.closed
		return domainError(CodeInvalidCommand, "session version shape overflows")
	}
	if state.Version != expectedVersion {
		return domainError(CodeInvalidCommand, "session version does not match lifecycle structure")
	}
	return nil
}

func historical_validStateTimestamp(timestamp time.Time) bool {
	return validateRecordedEventIdentityAndTimestamp(RecordedEvent{
		SchemaVersion: schemaVersion,
		ID:            "event-state-validation",
		CommandID:     "command-state-validation",
		SessionID:     "session-state-validation",
		OccurredAt:    timestamp,
	}) == nil
}

func historical_decideCloseSession(state HistoricalSession, command CloseSession) ([]UncommittedEvent, error) {
	if !state.Exists() {
		return nil, domainError(CodeSessionNotFound, "session not found")
	}
	if err := validateCommandSessionID(command.SessionID); err != nil {
		return nil, err
	}
	if command.SessionID != state.ID {
		return nil, domainError(CodeInvalidCommand, "command session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return nil, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return nil, domainError(CodeInvalidCommand, "session is not active")
	}
	if state.ActiveTurnID != "" {
		return nil, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return closeSessionEvents(), nil
}

func historical_decideCreateSession(state HistoricalSession, command CreateSession) ([]UncommittedEvent, error) {
	if !state.isPristine() {
		return nil, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if err := validateCommandSessionID(command.SessionID); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.WorkspaceRoot, "workspace root is required"); err != nil {
		return nil, err
	}
	return createSessionEvents(command.WorkspaceRoot), nil
}

func historical_decideStartTurn(state HistoricalSession, command StartTurn) ([]UncommittedEvent, error) {
	if !state.Exists() {
		return nil, domainError(CodeSessionNotFound, "session not found")
	}
	if err := validateCommandSessionID(command.SessionID); err != nil {
		return nil, err
	}
	if command.SessionID != state.ID {
		return nil, domainError(CodeInvalidCommand, "command session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return nil, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return nil, domainError(CodeInvalidCommand, "session is not active")
	}
	if err := validateCommandTurnID(command.TurnID); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Input, "turn input is required"); err != nil {
		return nil, err
	}
	if _, exists := state.Turns[command.TurnID]; exists {
		return nil, domainError(CodeTurnAlreadyExists, "turn already exists")
	}
	if state.ActiveTurnID != "" {
		return nil, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return startTurnEvents(command.TurnID, command.Input), nil
}

func historical_decideCompleteTurn(state HistoricalSession, command CompleteTurn) ([]UncommittedEvent, error) {
	turn, err := historical_requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := historical_rejectRunningItem(turn); err != nil {
		return nil, err
	}
	return completeTurnEvents(command.TurnID), nil
}

func historical_decideFailTurn(state HistoricalSession, command FailTurn) ([]UncommittedEvent, error) {
	turn, err := historical_requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := historical_rejectRunningItem(turn); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Code, "failure code is required"); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Message, "failure message is required"); err != nil {
		return nil, err
	}
	return failTurnEvents(command.TurnID, command.Code, command.Message), nil
}

func historical_decideInterruptTurn(state HistoricalSession, command InterruptTurn) ([]UncommittedEvent, error) {
	turn, err := historical_requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := historical_rejectRunningItem(turn); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Reason, "interruption reason is required"); err != nil {
		return nil, err
	}
	return interruptTurnEvents(command.TurnID, command.Reason), nil
}

func historical_decideStartAssistantMessage(state HistoricalSession, command StartAssistantMessage) ([]UncommittedEvent, error) {
	turn, err := historical_requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := validateCommandItemID(command.ItemID); err != nil {
		return nil, err
	}
	if _, exists := turn.Items[command.ItemID]; exists {
		return nil, domainError(CodeItemAlreadyExists, "item already exists")
	}
	if err := historical_rejectRunningItem(turn); err != nil {
		return nil, err
	}
	return startAssistantMessageEvents(command.TurnID, command.ItemID), nil
}

func historical_decideCompleteAssistantTurn(state HistoricalSession, command CompleteAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := historical_requireRunningItem(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Text, "assistant message text must be valid UTF-8"); err != nil {
		return nil, err
	}
	return completeAssistantTurnEvents(command.TurnID, command.ItemID, command.Text), nil
}

func historical_decideFailAssistantTurn(state HistoricalSession, command FailAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := historical_requireRunningItem(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Code, "failure code is required"); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Message, "failure message is required"); err != nil {
		return nil, err
	}
	return failAssistantTurnEvents(command.TurnID, command.ItemID, command.Code, command.Message), nil
}

func historical_decideInterruptAssistantTurn(state HistoricalSession, command InterruptAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := historical_requireRunningItem(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if err := historical_validateAssistantInterruptionCode(command.Code); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Message, "interruption message must be valid UTF-8"); err != nil {
		return nil, err
	}
	return interruptAssistantTurnEvents(command.TurnID, command.ItemID, command.Code, command.Message), nil
}

func historical_validateAssistantInterruptionCode(code string) error {
	if err := validateCommandText(code, "interruption code is required"); err != nil {
		return err
	}
	switch code {
	case InterruptionCallerCanceled, InterruptionDeliveryFailed, InterruptionRequestAbandoned:
		return nil
	default:
		return domainError(CodeInvalidCommand, "interruption code is not allowed")
	}
}

func historical_rejectRunningItem(turn HistoricalTurn) error {
	if turn.ActiveItemID != "" {
		return domainError(CodeItemAlreadyRunning, "an item is already running")
	}
	return nil
}

func historical_requireRunningItem(state HistoricalSession, sessionID SessionID, turnID TurnID, itemID ItemID) (HistoricalItem, error) {
	turn, err := historical_requireRunningTurn(state, sessionID, turnID)
	if err != nil {
		return HistoricalItem{}, err
	}
	if err := validateCommandItemID(itemID); err != nil {
		return HistoricalItem{}, err
	}
	if turn.ActiveItemID == "" {
		return HistoricalItem{}, domainError(CodeItemNotRunning, "no item is running")
	}
	if turn.ActiveItemID != itemID {
		return HistoricalItem{}, domainError(CodeItemMismatch, "command item ID does not match active item")
	}
	item, exists := turn.Items[itemID]
	if !exists || item.Status != ItemStatusRunning {
		return HistoricalItem{}, domainError(CodeItemNotRunning, "active item is not running")
	}
	return item, nil
}

func historical_requireRunningTurn(state HistoricalSession, sessionID SessionID, turnID TurnID) (HistoricalTurn, error) {
	if !state.Exists() {
		return HistoricalTurn{}, domainError(CodeSessionNotFound, "session not found")
	}
	if err := validateCommandSessionID(sessionID); err != nil {
		return HistoricalTurn{}, err
	}
	if sessionID != state.ID {
		return HistoricalTurn{}, domainError(CodeInvalidCommand, "command session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return HistoricalTurn{}, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return HistoricalTurn{}, domainError(CodeInvalidCommand, "session is not active")
	}
	if err := validateCommandTurnID(turnID); err != nil {
		return HistoricalTurn{}, err
	}
	if state.ActiveTurnID == "" {
		return HistoricalTurn{}, domainError(CodeTurnNotRunning, "no turn is running")
	}
	if state.ActiveTurnID != turnID {
		return HistoricalTurn{}, domainError(CodeTurnMismatch, "command turn ID does not match active turn")
	}
	turn, ok := state.Turns[state.ActiveTurnID]
	if !ok || turn.Status != TurnStatusRunning {
		return HistoricalTurn{}, domainError(CodeTurnNotRunning, "active turn is not running")
	}
	return turn, nil
}

func HistoricalReplay(records []RecordedEvent) (HistoricalSession, error) {
	var state HistoricalSession
	for _, record := range records {
		next, err := HistoricalApply(state, record)
		if err != nil {
			return HistoricalSession{}, fmt.Errorf("replay sequence %d: %w", record.Sequence, err)
		}
		state = next
	}
	return state, nil
}
