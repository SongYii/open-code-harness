package domain

import "math"

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
	return applyTerminalTurn(state, record, turn, TurnStatusCompleted, "", "", ""), nil
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
	return applyTerminalTurn(state, record, turn, TurnStatusFailed, event.Code, event.Message, ""), nil
}

func applyTurnInterrupted(state Session, record RecordedEvent, event TurnInterrupted) (Session, error) {
	if !hasRequiredText(event.Reason) {
		return Session{}, domainError(CodeInvalidEvent, "interruption reason is required")
	}
	turn, err := requireRunningTurnForEvent(state, record.SessionID, event.TurnID)
	if err != nil {
		return Session{}, err
	}
	return applyTerminalTurn(state, record, turn, TurnStatusInterrupted, "", "", event.Reason), nil
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
	return turn, nil
}

func applyTerminalTurn(state Session, record RecordedEvent, turn Turn, status TurnStatus, failureCode, failureText, interruptWhy string) Session {
	next := state.Clone()
	turn.Status = status
	turn.EndedAt = record.OccurredAt
	turn.FailureCode = failureCode
	turn.FailureText = failureText
	turn.InterruptWhy = interruptWhy
	next.Turns[turn.ID] = turn
	next.ActiveTurnID = ""
	next.Version = record.Sequence
	return next
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
