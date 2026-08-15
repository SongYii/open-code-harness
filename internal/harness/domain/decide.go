package domain

import (
	"math"
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

func validateCommandSessionID(sessionID SessionID) error {
	_, err := ParseSessionID(string(sessionID))
	return err
}

func validateCommandTurnID(turnID TurnID) error {
	_, err := ParseTurnID(string(turnID))
	return err
}

func validateCommandItemID(itemID ItemID) error {
	_, err := ParseItemID(string(itemID))
	return err
}

func validateCommandText(value, message string) error {
	if !hasRequiredText(value) {
		return domainError(CodeInvalidCommand, message)
	}
	return nil
}

func validateCommandUTF8(value, message string) error {
	if !utf8.ValidString(value) {
		return domainError(CodeInvalidCommand, message)
	}
	return nil
}

func startTurnEvents(turnID TurnID, input string) []UncommittedEvent {
	return []UncommittedEvent{{Event: TurnStarted{TurnID: turnID, Input: input}}}
}

func startAssistantTurnEvents(turnID TurnID, itemID ItemID, input string) []UncommittedEvent {
	return []UncommittedEvent{
		{Event: TurnStarted{TurnID: turnID, Input: input}},
		{Event: AssistantMessageStarted{TurnID: turnID, ItemID: itemID}},
	}
}

func completeTurnEvents(turnID TurnID) []UncommittedEvent {
	return []UncommittedEvent{{Event: TurnCompleted{TurnID: turnID}}}
}

func failTurnEvents(turnID TurnID, code, message string) []UncommittedEvent {
	return []UncommittedEvent{{Event: TurnFailed{TurnID: turnID, Code: code, Message: message}}}
}

func interruptTurnEvents(turnID TurnID, reason string) []UncommittedEvent {
	return []UncommittedEvent{{Event: TurnInterrupted{TurnID: turnID, Reason: reason}}}
}

func startAssistantMessageEvents(turnID TurnID, itemID ItemID) []UncommittedEvent {
	return []UncommittedEvent{{Event: AssistantMessageStarted{TurnID: turnID, ItemID: itemID}}}
}

func completeAssistantTurnEvents(turnID TurnID, itemID ItemID, text string) []UncommittedEvent {
	return []UncommittedEvent{
		{Event: AssistantMessageCompleted{TurnID: turnID, ItemID: itemID, Text: text}},
		{Event: TurnCompleted{TurnID: turnID}},
	}
}

func failAssistantTurnEvents(turnID TurnID, itemID ItemID, code, message string) []UncommittedEvent {
	return []UncommittedEvent{
		{Event: AssistantMessageFailed{TurnID: turnID, ItemID: itemID, Code: code, Message: message}},
		{Event: TurnFailed{TurnID: turnID, Code: code, Message: message}},
	}
}

func interruptAssistantTurnEvents(turnID TurnID, itemID ItemID, code, message string) []UncommittedEvent {
	return []UncommittedEvent{
		{Event: AssistantMessageInterrupted{TurnID: turnID, ItemID: itemID, Code: code, Message: message}},
		{Event: TurnInterrupted{TurnID: turnID, Reason: code}},
	}
}

func closeSessionEvents() []UncommittedEvent {
	return []UncommittedEvent{{Event: SessionClosed{}}}
}

func createSessionEvents(workspaceRoot string) []UncommittedEvent {
	return []UncommittedEvent{{Event: SessionCreated{WorkspaceRoot: workspaceRoot}}}
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
		if _, err := ParseTurnID(string(turn.ID)); err != nil || !hasRequiredText(turn.Input) || !validStateTimestamp(turn.StartedAt) {
			return domainError(CodeInvalidCommand, "turn structure is invalid")
		}
		if !turn.EndedAt.IsZero() && !validStateTimestamp(turn.EndedAt) {
			return domainError(CodeInvalidCommand, "turn terminal timestamp is invalid")
		}
		if !advanceVersion() { // turn.started
			return domainError(CodeInvalidCommand, "session version shape overflows")
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
			if !validStateTimestamp(item.StartedAt) || (!item.EndedAt.IsZero() && !validStateTimestamp(item.EndedAt)) {
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
			if item.Status != ItemStatusRunning && !advanceVersion() { // one Item terminal event
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
		if turn.Status != TurnStatusRunning && !advanceVersion() { // one Turn terminal event
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

func validStateTimestamp(timestamp time.Time) bool {
	return validateRecordedEventIdentityAndTimestamp(RecordedEvent{
		SchemaVersion: schemaVersion,
		ID:            "event-state-validation",
		CommandID:     "command-state-validation",
		SessionID:     "session-state-validation",
		OccurredAt:    timestamp,
	}) == nil
}

func decideCloseSession(state Session, command CloseSession) ([]UncommittedEvent, error) {
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

func decideCreateSession(state Session, command CreateSession) ([]UncommittedEvent, error) {
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

func decideStartTurn(state Session, command StartTurn) ([]UncommittedEvent, error) {
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

func decideCompleteTurn(state Session, command CompleteTurn) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := rejectRunningItem(turn); err != nil {
		return nil, err
	}
	return completeTurnEvents(command.TurnID), nil
}

func decideFailTurn(state Session, command FailTurn) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := rejectRunningItem(turn); err != nil {
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

func decideInterruptTurn(state Session, command InterruptTurn) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := rejectRunningItem(turn); err != nil {
		return nil, err
	}
	if err := validateCommandText(command.Reason, "interruption reason is required"); err != nil {
		return nil, err
	}
	return interruptTurnEvents(command.TurnID, command.Reason), nil
}

func decideStartAssistantMessage(state Session, command StartAssistantMessage) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurn(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if err := validateCommandItemID(command.ItemID); err != nil {
		return nil, err
	}
	if _, exists := turn.Items[command.ItemID]; exists {
		return nil, domainError(CodeItemAlreadyExists, "item already exists")
	}
	if err := rejectRunningItem(turn); err != nil {
		return nil, err
	}
	return startAssistantMessageEvents(command.TurnID, command.ItemID), nil
}

func decideCompleteAssistantTurn(state Session, command CompleteAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItem(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Text, "assistant message text must be valid UTF-8"); err != nil {
		return nil, err
	}
	return completeAssistantTurnEvents(command.TurnID, command.ItemID, command.Text), nil
}

func decideFailAssistantTurn(state Session, command FailAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItem(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
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

func decideInterruptAssistantTurn(state Session, command InterruptAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItem(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if err := validateAssistantInterruptionCode(command.Code); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Message, "interruption message must be valid UTF-8"); err != nil {
		return nil, err
	}
	return interruptAssistantTurnEvents(command.TurnID, command.ItemID, command.Code, command.Message), nil
}

func validateAssistantInterruptionCode(code string) error {
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
	if err := validateCommandItemID(itemID); err != nil {
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
	if err := validateCommandSessionID(sessionID); err != nil {
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
	if err := validateCommandTurnID(turnID); err != nil {
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
