package domain

import (
	"time"
	"unicode/utf8"
)

func Decide(state Session, command Command) ([]UncommittedEvent, error) {
	switch command := command.(type) {
	case CreateSession:
		return decideCreateSession(state, command)
	case StartTurn:
		return decideStartTurn(state, command)
	case StartAssistantTurn:
		return decideStartAssistantTurn(state, command)
	case CompleteTurn:
		return decideCompleteTurn(state, command)
	case FailTurn:
		return decideFailTurn(state, command)
	case InterruptTurn:
		return decideInterruptTurn(state, command)
	case StartAssistantMessage:
		return decideStartAssistantMessage(state, command)
	case CompleteAssistantTurn:
		return decideCompleteAssistantTurn(state, command)
	case FailAssistantTurn:
		return decideFailAssistantTurn(state, command)
	case InterruptAssistantTurn:
		return decideInterruptAssistantTurn(state, command)
	case CloseSession:
		return decideCloseSession(state, command)
	default:
		return nil, domainError(CodeInvalidCommand, "command type cannot be decided")
	}
}

// CheckStartAssistantTurnEligibility validates the complete existing Session
// structure and reports whether a new assistant Turn may be admitted. It does
// not inspect request input or identifiers that have not yet been generated.
func CheckStartAssistantTurnEligibility(state Session) error {
	if !state.Exists() {
		return domainError(CodeSessionNotFound, "session not found")
	}
	if err := validateSessionStructure(state); err != nil {
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

func decideStartAssistantTurn(state Session, command StartAssistantTurn) ([]UncommittedEvent, error) {
	if err := CheckStartAssistantTurnEligibility(state); err != nil {
		return nil, err
	}
	if _, err := ParseSessionID(string(command.SessionID)); err != nil {
		return nil, err
	}
	if command.SessionID != state.ID {
		return nil, domainError(CodeInvalidCommand, "command session ID does not match state")
	}
	if _, err := ParseTurnID(string(command.TurnID)); err != nil {
		return nil, err
	}
	if _, err := ParseItemID(string(command.ItemID)); err != nil {
		return nil, err
	}
	if !hasRequiredText(command.Input) {
		return nil, domainError(CodeInvalidCommand, "turn input is required")
	}
	if _, exists := state.Turns[command.TurnID]; exists {
		return nil, domainError(CodeTurnAlreadyExists, "turn already exists")
	}
	for _, turn := range state.Turns {
		if _, exists := turn.Items[command.ItemID]; exists {
			return nil, domainError(CodeItemAlreadyExists, "item already exists")
		}
	}
	return []UncommittedEvent{
		{Event: TurnStarted{TurnID: command.TurnID, Input: command.Input}},
		{Event: AssistantMessageStarted{TurnID: command.TurnID, ItemID: command.ItemID}},
	}, nil
}

func validateSessionStructure(state Session) error {
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
		if _, err := ParseTurnID(string(turn.ID)); err != nil || !hasRequiredText(turn.Input) || turn.StartedAt.IsZero() {
			return domainError(CodeInvalidCommand, "turn structure is invalid")
		}
		if turn.ItemOrder == nil || turn.Items == nil {
			return domainError(CodeInvalidCommand, "turn item containers are invalid")
		}
		if err := validateTurnItems(turn); err != nil {
			return domainError(CodeInvalidCommand, "turn item structure is invalid")
		}
		var lastItemEnd time.Time
		for index, itemID := range turn.ItemOrder {
			item := turn.Items[itemID]
			if item.StartedAt.Before(turn.StartedAt) || (!lastItemEnd.IsZero() && item.StartedAt.Before(lastItemEnd)) {
				return domainError(CodeInvalidCommand, "turn item timeline is invalid")
			}
			if item.Status == ItemStatusRunning && index != len(turn.ItemOrder)-1 {
				return domainError(CodeInvalidCommand, "running item is not last in turn order")
			}
			if !item.EndedAt.IsZero() {
				lastItemEnd = item.EndedAt
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
	return nil
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
	turn, err := requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := rejectRunningItem(turn); err != nil {
		return nil, err
	}
	return []UncommittedEvent{{Event: TurnCompleted{TurnID: command.TurnID}}}, nil
}

func decideFailTurn(state Session, command FailTurn) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := rejectRunningItem(turn); err != nil {
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
	turn, err := requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := rejectRunningItem(turn); err != nil {
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

func decideStartAssistantMessage(state Session, command StartAssistantMessage) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if _, err := ParseItemID(string(command.ItemID)); err != nil {
		return nil, err
	}
	if _, exists := turn.Items[command.ItemID]; exists {
		return nil, domainError(CodeItemAlreadyExists, "item already exists")
	}
	if err := rejectRunningItem(turn); err != nil {
		return nil, err
	}
	return []UncommittedEvent{{Event: AssistantMessageStarted{
		TurnID: command.TurnID,
		ItemID: command.ItemID,
	}}}, nil
}

func decideCompleteAssistantTurn(state Session, command CompleteAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItem(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if !utf8.ValidString(command.Text) {
		return nil, domainError(CodeInvalidCommand, "assistant message text must be valid UTF-8")
	}
	return []UncommittedEvent{
		{Event: AssistantMessageCompleted{TurnID: command.TurnID, ItemID: command.ItemID, Text: command.Text}},
		{Event: TurnCompleted{TurnID: command.TurnID}},
	}, nil
}

func decideFailAssistantTurn(state Session, command FailAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItem(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if !hasRequiredText(command.Code) {
		return nil, domainError(CodeInvalidCommand, "failure code is required")
	}
	if !hasRequiredText(command.Message) {
		return nil, domainError(CodeInvalidCommand, "failure message is required")
	}
	return []UncommittedEvent{
		{Event: AssistantMessageFailed{TurnID: command.TurnID, ItemID: command.ItemID, Code: command.Code, Message: command.Message}},
		{Event: TurnFailed{TurnID: command.TurnID, Code: command.Code, Message: command.Message}},
	}, nil
}

func decideInterruptAssistantTurn(state Session, command InterruptAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItem(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if !hasRequiredText(command.Code) {
		return nil, domainError(CodeInvalidCommand, "interruption code is required")
	}
	if !utf8.ValidString(command.Message) {
		return nil, domainError(CodeInvalidCommand, "interruption message must be valid UTF-8")
	}
	return []UncommittedEvent{
		{Event: AssistantMessageInterrupted{TurnID: command.TurnID, ItemID: command.ItemID, Code: command.Code, Message: command.Message}},
		{Event: TurnInterrupted{TurnID: command.TurnID, Reason: command.Code}},
	}, nil
}

func rejectRunningItem(turn Turn) error {
	if turn.ActiveItemID != "" {
		return domainError(CodeItemAlreadyRunning, "an item is already running")
	}
	return nil
}

func requireRunningItem(state Session, sessionID SessionID, turnID TurnID, itemID ItemID) (Item, error) {
	turn, err := requireRunningTurn(state, sessionID, turnID)
	if err != nil {
		return Item{}, err
	}
	if _, err := ParseItemID(string(itemID)); err != nil {
		return Item{}, err
	}
	if turn.ActiveItemID == "" {
		return Item{}, domainError(CodeItemNotRunning, "no item is running")
	}
	if turn.ActiveItemID != itemID {
		return Item{}, domainError(CodeItemMismatch, "command item ID does not match active item")
	}
	item, exists := turn.Items[itemID]
	if !exists || item.Status != ItemStatusRunning {
		return Item{}, domainError(CodeItemNotRunning, "active item is not running")
	}
	return item, nil
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
