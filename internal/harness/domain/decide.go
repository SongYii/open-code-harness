package domain

import "strings"

func Decide(state Session, command Command) ([]UncommittedEvent, error) {
	switch command := command.(type) {
	case CreateSession:
		return decideCreateSession(state, command)
	case StartTurn:
		return decideStartTurn(state, command)
	default:
		return nil, domainError(CodeInvalidCommand, "command type cannot be decided")
	}
}

func decideCreateSession(state Session, command CreateSession) ([]UncommittedEvent, error) {
	if !state.isPristine() {
		return nil, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if _, err := ParseSessionID(string(command.SessionID)); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.WorkspaceRoot) == "" {
		return nil, domainError(CodeInvalidCommand, "workspace root is required")
	}
	return []UncommittedEvent{{Event: SessionCreated{WorkspaceRoot: command.WorkspaceRoot}}}, nil
}

func decideStartTurn(state Session, command StartTurn) ([]UncommittedEvent, error) {
	if !state.Exists() {
		return nil, domainError(CodeSessionNotFound, "session not found")
	}
	if _, err := ParseSessionID(string(command.SessionID)); err != nil {
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
	if _, err := ParseTurnID(string(command.TurnID)); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.Input) == "" {
		return nil, domainError(CodeInvalidCommand, "turn input is required")
	}
	if _, exists := state.Turns[command.TurnID]; exists {
		return nil, domainError(CodeTurnAlreadyExists, "turn already exists")
	}
	if state.ActiveTurnID != "" {
		return nil, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return []UncommittedEvent{{Event: TurnStarted{
		TurnID: command.TurnID,
		Input:  command.Input,
	}}}, nil
}
