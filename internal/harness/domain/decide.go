package domain

func Decide(state Session, command Command) ([]UncommittedEvent, error) {
	switch command := command.(type) {
	case CreateSession:
		return decideCreateSession(state, command)
	case StartTurn:
		return decideStartTurn(state, command)
	case CompleteTurn:
		return decideCompleteTurn(state, command)
	case FailTurn:
		return decideFailTurn(state, command)
	case InterruptTurn:
		return decideInterruptTurn(state, command)
	case CloseSession:
		return decideCloseSession(state, command)
	default:
		return nil, domainError(CodeInvalidCommand, "command type cannot be decided")
	}
}

func decideCloseSession(state Session, command CloseSession) ([]UncommittedEvent, error) {
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
	if state.ActiveTurnID != "" {
		return nil, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return []UncommittedEvent{{Event: SessionClosed{}}}, nil
}

func decideCreateSession(state Session, command CreateSession) ([]UncommittedEvent, error) {
	if !state.isPristine() {
		return nil, domainError(CodeSessionAlreadyExists, "session already exists")
	}
	if _, err := ParseSessionID(string(command.SessionID)); err != nil {
		return nil, err
	}
	if !hasRequiredText(command.WorkspaceRoot) {
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
	if !hasRequiredText(command.Input) {
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

func decideCompleteTurn(state Session, command CompleteTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningTurn(state, command.SessionID, command.TurnID); err != nil {
		return nil, err
	}
	return []UncommittedEvent{{Event: TurnCompleted{TurnID: command.TurnID}}}, nil
}

func decideFailTurn(state Session, command FailTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningTurn(state, command.SessionID, command.TurnID); err != nil {
		return nil, err
	}
	if !hasRequiredText(command.Code) {
		return nil, domainError(CodeInvalidCommand, "failure code is required")
	}
	if !hasRequiredText(command.Message) {
		return nil, domainError(CodeInvalidCommand, "failure message is required")
	}
	return []UncommittedEvent{{Event: TurnFailed{
		TurnID:  command.TurnID,
		Code:    command.Code,
		Message: command.Message,
	}}}, nil
}

func decideInterruptTurn(state Session, command InterruptTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningTurn(state, command.SessionID, command.TurnID); err != nil {
		return nil, err
	}
	if !hasRequiredText(command.Reason) {
		return nil, domainError(CodeInvalidCommand, "interruption reason is required")
	}
	return []UncommittedEvent{{Event: TurnInterrupted{
		TurnID: command.TurnID,
		Reason: command.Reason,
	}}}, nil
}

func requireRunningTurn(state Session, sessionID SessionID, turnID TurnID) (Turn, error) {
	if !state.Exists() {
		return Turn{}, domainError(CodeSessionNotFound, "session not found")
	}
	if _, err := ParseSessionID(string(sessionID)); err != nil {
		return Turn{}, err
	}
	if sessionID != state.ID {
		return Turn{}, domainError(CodeInvalidCommand, "command session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return Turn{}, domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return Turn{}, domainError(CodeInvalidCommand, "session is not active")
	}
	if _, err := ParseTurnID(string(turnID)); err != nil {
		return Turn{}, err
	}
	if state.ActiveTurnID == "" {
		return Turn{}, domainError(CodeTurnNotRunning, "no turn is running")
	}
	if state.ActiveTurnID != turnID {
		return Turn{}, domainError(CodeTurnMismatch, "command turn ID does not match active turn")
	}
	turn, ok := state.Turns[state.ActiveTurnID]
	if !ok || turn.Status != TurnStatusRunning {
		return Turn{}, domainError(CodeTurnNotRunning, "active turn is not running")
	}
	return turn, nil
}
