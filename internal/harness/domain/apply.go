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
	default:
		return Session{}, domainError(CodeInvalidEvent, "event type cannot be applied")
	}
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
