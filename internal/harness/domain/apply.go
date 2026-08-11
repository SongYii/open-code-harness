package domain

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
	if record.OccurredAt.IsZero() {
		return Session{}, domainError(CodeInvalidEvent, "event timestamp is required")
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
	default:
		return Session{}, domainError(CodeInvalidEvent, "event type cannot be applied")
	}
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
	if event.Input == "" {
		return Session{}, domainError(CodeInvalidEvent, "turn input is required")
	}
	if _, exists := state.Turns[event.TurnID]; exists {
		return Session{}, domainError(CodeTurnAlreadyExists, "turn already exists")
	}
	if state.ActiveTurnID != "" {
		return Session{}, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}

	next := state.Clone()
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

func applySessionCreated(state Session, record RecordedEvent, event SessionCreated) (Session, error) {
	if !state.isPristine() {
		return Session{}, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if event.WorkspaceRoot == "" {
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
