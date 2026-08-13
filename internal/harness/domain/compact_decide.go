package domain

// DecideCompact independently decides commands from the bounded aggregate.
func DecideCompact(state CompactSession, command Command) ([]UncommittedEvent, error) {
	switch command := command.(type) {
	case CreateSession:
		return decideCompactCreateSession(state, command)
	case StartTurn:
		return decideCompactStartTurn(state, command)
	case StartAssistantTurn:
		return decideCompactStartAssistantTurn(state, command)
	case CompleteTurn:
		return decideCompactCompleteTurn(state, command)
	case FailTurn:
		return decideCompactFailTurn(state, command)
	case InterruptTurn:
		return decideCompactInterruptTurn(state, command)
	case StartAssistantMessage:
		return decideCompactStartAssistantMessage(state, command)
	case CompleteAssistantTurn:
		return decideCompactCompleteAssistantTurn(state, command)
	case FailAssistantTurn:
		return decideCompactFailAssistantTurn(state, command)
	case InterruptAssistantTurn:
		return decideCompactInterruptAssistantTurn(state, command)
	case CloseSession:
		return decideCompactCloseSession(state, command)
	default:
		return nil, domainError(CodeInvalidCommand, "command type cannot be decided")
	}
}

// CheckStartAssistantTurnEligibilityCompact reports whether a new atomic
// assistant turn may be admitted from bounded state.
func CheckStartAssistantTurnEligibilityCompact(state CompactSession) error {
	if !state.Exists() {
		return domainError(CodeSessionNotFound, "session not found")
	}
	if err := validateCompactSession(state); err != nil {
		return err
	}
	if state.Status == SessionStatusClosed {
		return domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return domainError(CodeInvalidCommand, "session is not active")
	}
	if state.ActiveTurn != nil {
		if state.ActiveTurn.ActiveItem != nil {
			return domainError(CodeItemAlreadyRunning, "an item is already running")
		}
		return domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return nil
}

func decideCompactCreateSession(state CompactSession, command CreateSession) ([]UncommittedEvent, error) {
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

func decideCompactStartTurn(state CompactSession, command StartTurn) ([]UncommittedEvent, error) {
	if err := requireCompactSessionForCommand(state, command.SessionID); err != nil {
		return nil, err
	}
	if err := validateCommandTurnID(command.TurnID); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Input, "turn input is required"); err != nil {
		return nil, err
	}
	if state.ActiveTurn != nil {
		return nil, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return startTurnEvents(command.TurnID, command.Input), nil
}

func decideCompactStartAssistantTurn(state CompactSession, command StartAssistantTurn) ([]UncommittedEvent, error) {
	if err := CheckStartAssistantTurnEligibilityCompact(state); err != nil {
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
	return startAssistantTurnEvents(command.TurnID, command.ItemID, command.Input), nil
}

func decideCompactCompleteTurn(state CompactSession, command CompleteTurn) ([]UncommittedEvent, error) {
	turn, err := requireCompactRunningTurnForCommand(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if turn.ActiveItem != nil {
		return nil, domainError(CodeItemAlreadyRunning, "an item is already running")
	}
	return completeTurnEvents(command.TurnID), nil
}

func decideCompactFailTurn(state CompactSession, command FailTurn) ([]UncommittedEvent, error) {
	turn, err := requireCompactRunningTurnForCommand(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if turn.ActiveItem != nil {
		return nil, domainError(CodeItemAlreadyRunning, "an item is already running")
	}
	if err := validateCommandText(command.Code, "failure code is required"); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Message, "failure message is required"); err != nil {
		return nil, err
	}
	return failTurnEvents(command.TurnID, command.Code, command.Message), nil
}

func decideCompactInterruptTurn(state CompactSession, command InterruptTurn) ([]UncommittedEvent, error) {
	turn, err := requireCompactRunningTurnForCommand(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if turn.ActiveItem != nil {
		return nil, domainError(CodeItemAlreadyRunning, "an item is already running")
	}
	if err := validateCommandText(command.Reason, "interruption reason is required"); err != nil {
		return nil, err
	}
	return interruptTurnEvents(command.TurnID, command.Reason), nil
}

func decideCompactStartAssistantMessage(state CompactSession, command StartAssistantMessage) ([]UncommittedEvent, error) {
	turn, err := requireCompactRunningTurnForCommand(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := validateCommandItemID(command.ItemID); err != nil {
		return nil, err
	}
	if turn.ActiveItem != nil {
		return nil, domainError(CodeItemAlreadyRunning, "an item is already running")
	}
	return startAssistantMessageEvents(command.TurnID, command.ItemID), nil
}

func decideCompactCompleteAssistantTurn(state CompactSession, command CompleteAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireCompactRunningItemForCommand(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Text, "assistant message text must be valid UTF-8"); err != nil {
		return nil, err
	}
	return completeAssistantTurnEvents(command.TurnID, command.ItemID, command.Text), nil
}

func decideCompactFailAssistantTurn(state CompactSession, command FailAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireCompactRunningItemForCommand(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
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

func decideCompactInterruptAssistantTurn(state CompactSession, command InterruptAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireCompactRunningItemForCommand(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Code, "interruption code is required"); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Message, "interruption message must be valid UTF-8"); err != nil {
		return nil, err
	}
	return interruptAssistantTurnEvents(command.TurnID, command.ItemID, command.Code, command.Message), nil
}

func decideCompactCloseSession(state CompactSession, command CloseSession) ([]UncommittedEvent, error) {
	if err := requireCompactSessionForCommand(state, command.SessionID); err != nil {
		return nil, err
	}
	if state.ActiveTurn != nil {
		return nil, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return closeSessionEvents(), nil
}

func requireCompactSessionForCommand(state CompactSession, sessionID SessionID) error {
	if !state.Exists() {
		return domainError(CodeSessionNotFound, "session not found")
	}
	if err := validateCommandSessionID(sessionID); err != nil {
		return err
	}
	if sessionID != state.ID {
		return domainError(CodeInvalidCommand, "command session ID does not match state")
	}
	if state.Status == SessionStatusClosed {
		return domainError(CodeSessionClosed, "session is closed")
	}
	if state.Status != SessionStatusActive {
		return domainError(CodeInvalidCommand, "session is not active")
	}
	return nil
}

func requireCompactRunningTurnForCommand(state CompactSession, sessionID SessionID, turnID TurnID) (CompactTurn, error) {
	if err := requireCompactSessionForCommand(state, sessionID); err != nil {
		return CompactTurn{}, err
	}
	if err := validateCommandTurnID(turnID); err != nil {
		return CompactTurn{}, err
	}
	if state.ActiveTurn == nil {
		return CompactTurn{}, domainError(CodeTurnNotRunning, "no turn is running")
	}
	if state.ActiveTurn.ID != turnID {
		return CompactTurn{}, domainError(CodeTurnMismatch, "command turn ID does not match active turn")
	}
	return *state.ActiveTurn, nil
}

func requireCompactRunningItemForCommand(state CompactSession, sessionID SessionID, turnID TurnID, itemID ItemID) (CompactItem, error) {
	turn, err := requireCompactRunningTurnForCommand(state, sessionID, turnID)
	if err != nil {
		return CompactItem{}, err
	}
	if err := validateCommandItemID(itemID); err != nil {
		return CompactItem{}, err
	}
	if turn.ActiveItem == nil {
		return CompactItem{}, domainError(CodeItemNotRunning, "no item is running")
	}
	if turn.ActiveItem.ID != itemID {
		return CompactItem{}, domainError(CodeItemMismatch, "command item ID does not match active item")
	}
	return *turn.ActiveItem, nil
}

func validateCompactSession(state CompactSession) error {
	if _, err := ParseSessionID(string(state.ID)); err != nil || state.Version == 0 || !hasRequiredText(state.WorkspaceRoot) {
		return domainError(CodeInvalidCommand, "session structure is invalid")
	}
	if state.Status != SessionStatusActive && state.Status != SessionStatusClosed {
		return domainError(CodeInvalidCommand, "session status is invalid")
	}
	if state.ActiveTurn == nil {
		return nil
	}
	turn := state.ActiveTurn
	if state.Status != SessionStatusActive || turn.ID == "" || !hasRequiredText(turn.Input) || !validStateTimestamp(turn.StartedAt) || !validStateTimestamp(turn.LastTransitionAt) || turn.LastTransitionAt.Before(turn.StartedAt) {
		return domainError(CodeInvalidCommand, "active turn structure is invalid")
	}
	if _, err := ParseTurnID(string(turn.ID)); err != nil {
		return domainError(CodeInvalidCommand, "active turn structure is invalid")
	}
	if turn.ActiveItem == nil {
		return nil
	}
	item := turn.ActiveItem
	if item.ID == "" || item.TurnID != turn.ID || item.Kind != ItemKindAssistantMessage || !validStateTimestamp(item.StartedAt) || item.StartedAt.Before(turn.LastTransitionAt) {
		return domainError(CodeInvalidCommand, "active item structure is invalid")
	}
	if _, err := ParseItemID(string(item.ID)); err != nil {
		return domainError(CodeInvalidCommand, "active item structure is invalid")
	}
	return nil
}
