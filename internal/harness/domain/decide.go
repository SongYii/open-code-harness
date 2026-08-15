package domain

import (
	"time"
	"unicode/utf8"
)

func validStateTimestamp(timestamp time.Time) bool {
	return validateRecordedEventIdentityAndTimestamp(RecordedEvent{
		SchemaVersion: schemaVersion,
		ID:            "event-state-validation",
		CommandID:     "command-state-validation",
		SessionID:     "session-state-validation",
		OccurredAt:    timestamp,
	}) == nil
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

func startAssistantTurnEvents(turnID TurnID, itemID ItemID, input string, request *ModelRequestSpec) []UncommittedEvent {
	events := []UncommittedEvent{
		{Event: TurnStarted{TurnID: turnID, Input: input}},
		{Event: AssistantMessageStarted{TurnID: turnID, ItemID: itemID}},
	}
	if request != nil {
		events = append(events, UncommittedEvent{Event: modelRequestRecordedFromSpec(turnID, itemID, *request)})
	}
	return events
}

func modelRequestRecordedFromSpec(turnID TurnID, itemID ItemID, spec ModelRequestSpec) ModelRequestRecorded {
	return ModelRequestRecorded{
		TurnID:              turnID,
		ItemID:              itemID,
		AdapterFamily:       spec.AdapterFamily,
		ModelID:             spec.ModelID,
		EndpointID:          spec.EndpointID,
		NativeTools:         spec.NativeTools,
		Images:              spec.Images,
		StructuredOutput:    spec.StructuredOutput,
		ReasoningFields:     spec.ReasoningFields,
		PromptCache:         spec.PromptCache,
		ContextWindowTokens: spec.ContextWindowTokens,
		MaxOutputTokens:     spec.MaxOutputTokens,
		IncludeUsage:        spec.IncludeUsage,
		MaxTokensField:      spec.MaxTokensField,
		Messages:            cloneModelPromptMessages(spec.Messages),
	}
}

func recordModelUsageEvents(event ModelUsageRecorded) []UncommittedEvent {
	return []UncommittedEvent{{Event: event}}
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

// Decide independently decides commands from the bounded aggregate.
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
	case RecordModelUsage:
		return decideRecordModelUsage(state, command)
	case CloseSession:
		return decideCloseSession(state, command)
	default:
		return nil, domainError(CodeInvalidCommand, "command type cannot be decided")
	}
}

// CheckStartAssistantTurnEligibility reports whether a new atomic
// assistant turn may be admitted from bounded state.
func CheckStartAssistantTurnEligibility(state Session) error {
	if !state.Exists() {
		return domainError(CodeSessionNotFound, "session not found")
	}
	if err := validateSession(state); err != nil {
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
	if err := requireSessionForCommand(state, command.SessionID); err != nil {
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
	if err := validateStartAssistantTurnRequest(command); err != nil {
		return nil, err
	}
	return startAssistantTurnEvents(command.TurnID, command.ItemID, command.Input, command.Request), nil
}

func decideRecordModelUsage(state Session, command RecordModelUsage) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemForCommand(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if err := validateModelUsagePayload(command.ModelUsageRecorded, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return recordModelUsageEvents(command.ModelUsageRecorded), nil
}

func validateStartAssistantTurnRequest(command StartAssistantTurn) error {
	if command.Request == nil {
		return nil
	}
	if err := validateModelRequestSpec(*command.Request); err != nil {
		return err
	}
	if len(command.Request.Messages) != 1 || command.Request.Messages[0].Role != PromptRoleUser || command.Request.Messages[0].Text != command.Input {
		return domainError(CodeInvalidCommand, "model request messages must equal the turn input")
	}
	return nil
}

func decideCompleteTurn(state Session, command CompleteTurn) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurnForCommand(state, command.SessionID, command.TurnID)
	if err != nil {
		return nil, err
	}
	if turn.ActiveItem != nil {
		return nil, domainError(CodeItemAlreadyRunning, "an item is already running")
	}
	return completeTurnEvents(command.TurnID), nil
}

func decideFailTurn(state Session, command FailTurn) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurnForCommand(state, command.SessionID, command.TurnID)
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

func decideInterruptTurn(state Session, command InterruptTurn) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurnForCommand(state, command.SessionID, command.TurnID)
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

func decideStartAssistantMessage(state Session, command StartAssistantMessage) ([]UncommittedEvent, error) {
	turn, err := requireRunningTurnForCommand(state, command.SessionID, command.TurnID)
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

func decideCompleteAssistantTurn(state Session, command CompleteAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemForCommand(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Text, "assistant message text must be valid UTF-8"); err != nil {
		return nil, err
	}
	return completeAssistantTurnEvents(command.TurnID, command.ItemID, command.Text), nil
}

func decideFailAssistantTurn(state Session, command FailAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemForCommand(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
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
	if _, err := requireRunningItemForCommand(state, command.SessionID, command.TurnID, command.ItemID); err != nil {
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

func decideCloseSession(state Session, command CloseSession) ([]UncommittedEvent, error) {
	if err := requireSessionForCommand(state, command.SessionID); err != nil {
		return nil, err
	}
	if state.ActiveTurn != nil {
		return nil, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return closeSessionEvents(), nil
}

func requireSessionForCommand(state Session, sessionID SessionID) error {
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

func requireRunningTurnForCommand(state Session, sessionID SessionID, turnID TurnID) (Turn, error) {
	if err := requireSessionForCommand(state, sessionID); err != nil {
		return Turn{}, err
	}
	if err := validateCommandTurnID(turnID); err != nil {
		return Turn{}, err
	}
	if state.ActiveTurn == nil {
		return Turn{}, domainError(CodeTurnNotRunning, "no turn is running")
	}
	if state.ActiveTurn.ID != turnID {
		return Turn{}, domainError(CodeTurnMismatch, "command turn ID does not match active turn")
	}
	return *state.ActiveTurn, nil
}

func requireRunningItemForCommand(state Session, sessionID SessionID, turnID TurnID, itemID ItemID) (Item, error) {
	turn, err := requireRunningTurnForCommand(state, sessionID, turnID)
	if err != nil {
		return Item{}, err
	}
	if err := validateCommandItemID(itemID); err != nil {
		return Item{}, err
	}
	if turn.ActiveItem == nil {
		return Item{}, domainError(CodeItemNotRunning, "no item is running")
	}
	if turn.ActiveItem.ID != itemID {
		return Item{}, domainError(CodeItemMismatch, "command item ID does not match active item")
	}
	return *turn.ActiveItem, nil
}

func validateSession(state Session) error {
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
