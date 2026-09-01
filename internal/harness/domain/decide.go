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
		Tools:               cloneToolSchemas(spec.Tools),
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

func completeAssistantMessageEvents(turnID TurnID, itemID ItemID, text string, toolCalls []ToolCallOffer) []UncommittedEvent {
	return []UncommittedEvent{{Event: AssistantMessageCompleted{
		TurnID: turnID, ItemID: itemID, Text: text, ToolCalls: cloneToolCallOffers(toolCalls),
	}}}
}

func completeAssistantTurnEvents(turnID TurnID, itemID ItemID, text string) []UncommittedEvent {
	return []UncommittedEvent{
		{Event: AssistantMessageCompleted{TurnID: turnID, ItemID: itemID, Text: text}},
		{Event: TurnCompleted{TurnID: turnID}},
	}
}

func startToolCallEvents(event ToolCallStarted) []UncommittedEvent {
	return []UncommittedEvent{{Event: event}}
}

func completeToolCallEvents(event ToolCallCompleted) []UncommittedEvent {
	return []UncommittedEvent{{Event: event}}
}

func failToolCallEvents(event ToolCallFailed) []UncommittedEvent {
	return []UncommittedEvent{{Event: event}}
}

func interruptToolTurnEvents(event ToolCallInterrupted, approval *ApprovalResolved) []UncommittedEvent {
	events := make([]UncommittedEvent, 0, 3)
	if approval != nil {
		events = append(events, UncommittedEvent{Event: *approval})
	}
	events = append(events,
		UncommittedEvent{Event: event},
		UncommittedEvent{Event: TurnInterrupted{TurnID: event.TurnID, Reason: event.Code}},
	)
	return events
}

func failToolTurnEvents(event ToolCallFailed) []UncommittedEvent {
	return []UncommittedEvent{
		{Event: event},
		{Event: TurnFailed{TurnID: event.TurnID, Code: event.Code, Message: event.Message}},
	}
}

func recordPolicyDecisionEvents(event PolicyDecisionRecorded) []UncommittedEvent {
	return []UncommittedEvent{{Event: event}}
}

func requestApprovalEvents(event ApprovalRequested) []UncommittedEvent {
	return []UncommittedEvent{{Event: event}}
}

func resolveApprovalEvents(event ApprovalResolved) []UncommittedEvent {
	return []UncommittedEvent{{Event: event}}
}

func recordModelRequestEvents(event ModelRequestRecorded) []UncommittedEvent {
	return []UncommittedEvent{{Event: event}}
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

func deleteSessionEvents() []UncommittedEvent {
	return []UncommittedEvent{{Event: SessionDeleted{}}}
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
	case CompleteAssistantMessage:
		return decideCompleteAssistantMessage(state, command)
	case CompleteAssistantTurn:
		return decideCompleteAssistantTurn(state, command)
	case FailAssistantTurn:
		return decideFailAssistantTurn(state, command)
	case InterruptAssistantTurn:
		return decideInterruptAssistantTurn(state, command)
	case RecordModelUsage:
		return decideRecordModelUsage(state, command)
	case RecordModelRequest:
		return decideRecordModelRequest(state, command)
	case StartToolCall:
		return decideStartToolCall(state, command)
	case CompleteToolCall:
		return decideCompleteToolCall(state, command)
	case FailToolCall:
		return decideFailToolCall(state, command)
	case InterruptToolTurn:
		return decideInterruptToolTurn(state, command)
	case FailToolTurn:
		return decideFailToolTurn(state, command)
	case RecordPolicyDecision:
		return decideRecordPolicyDecision(state, command)
	case RequestApproval:
		return decideRequestApproval(state, command)
	case ResolveApproval:
		return decideResolveApproval(state, command)
	case CloseSession:
		return decideCloseSession(state, command)
	case DeleteSession:
		return decideDeleteSession(state, command)
	case StartContextCompaction:
		return decideStartContextCompaction(state, command)
	case CompleteContextCompaction:
		return decideCompleteContextCompaction(state, command)
	case FailContextCompaction:
		return decideFailContextCompaction(state, command)
	case RecordContextPreparation:
		return decideRecordContextPreparation(state, command)
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
	if state.ContextCompaction != nil {
		return domainError(CodeCompactionAlreadyRunning, "a context compaction is already running")
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
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindAssistantMessage); err != nil {
		return nil, err
	}
	if err := validateModelUsagePayload(command.ModelUsageRecorded, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return recordModelUsageEvents(command.ModelUsageRecorded), nil
}

func decideRecordModelRequest(state Session, command RecordModelRequest) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindAssistantMessage); err != nil {
		return nil, err
	}
	if err := validateModelRequestPayload(command.ModelRequestRecorded, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return recordModelRequestEvents(command.ModelRequestRecorded), nil
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

func decideCompleteAssistantMessage(state Session, command CompleteAssistantMessage) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindAssistantMessage); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Text, "assistant message text must be valid UTF-8"); err != nil {
		return nil, err
	}
	if err := validateToolCallOffers(command.ToolCalls, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return completeAssistantMessageEvents(command.TurnID, command.ItemID, command.Text, command.ToolCalls), nil
}

func decideCompleteAssistantTurn(state Session, command CompleteAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindAssistantMessage); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Text, "assistant message text must be valid UTF-8"); err != nil {
		return nil, err
	}
	return completeAssistantTurnEvents(command.TurnID, command.ItemID, command.Text), nil
}

func decideFailAssistantTurn(state Session, command FailAssistantTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindAssistantMessage); err != nil {
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
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindAssistantMessage); err != nil {
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

func decideStartToolCall(state Session, command StartToolCall) ([]UncommittedEvent, error) {
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
	event := ToolCallStarted{
		TurnID: command.TurnID, ItemID: command.ItemID, CallID: command.CallID,
		Name: command.Name, Arguments: command.Arguments, StepIndex: command.StepIndex,
	}
	if err := validateToolCallStartedPayload(event, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return startToolCallEvents(event), nil
}

func decideCompleteToolCall(state Session, command CompleteToolCall) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindToolCall); err != nil {
		return nil, err
	}
	event := ToolCallCompleted{
		TurnID: command.TurnID, ItemID: command.ItemID, CallID: command.CallID,
		Content: command.Content, Truncated: command.Truncated,
	}
	if err := validateToolCallCompletedPayload(event, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return completeToolCallEvents(event), nil
}

func decideFailToolCall(state Session, command FailToolCall) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindToolCall); err != nil {
		return nil, err
	}
	event := ToolCallFailed{
		TurnID: command.TurnID, ItemID: command.ItemID, CallID: command.CallID,
		Code: command.Code, Message: command.Message,
	}
	if err := validateToolCallFailedPayload(event, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return failToolCallEvents(event), nil
}

func decideInterruptToolTurn(state Session, command InterruptToolTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindToolCall); err != nil {
		return nil, err
	}
	if err := validateAssistantInterruptionCode(command.Code); err != nil {
		return nil, err
	}
	if err := validateCommandUTF8(command.Message, "interruption message must be valid UTF-8"); err != nil {
		return nil, err
	}
	event := ToolCallInterrupted{
		TurnID: command.TurnID, ItemID: command.ItemID, CallID: command.CallID,
		Code: command.Code, Message: command.Message,
	}
	if err := validateToolCallInterruptedPayload(event, CodeInvalidCommand); err != nil {
		return nil, err
	}
	var approval *ApprovalResolved
	if command.ApprovalID != "" {
		resolved := ApprovalResolved{
			TurnID: command.TurnID, ItemID: command.ItemID,
			ApprovalID: command.ApprovalID, Decision: ApprovalDecisionCanceled,
		}
		if err := validateApprovalResolvedPayload(resolved, CodeInvalidCommand); err != nil {
			return nil, err
		}
		approval = &resolved
	}
	return interruptToolTurnEvents(event, approval), nil
}

func decideFailToolTurn(state Session, command FailToolTurn) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindToolCall); err != nil {
		return nil, err
	}
	event := ToolCallFailed{
		TurnID: command.TurnID, ItemID: command.ItemID, CallID: command.CallID,
		Code: command.Code, Message: command.Message,
	}
	if err := validateToolCallFailedPayload(event, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return failToolTurnEvents(event), nil
}

func decideRecordPolicyDecision(state Session, command RecordPolicyDecision) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindToolCall); err != nil {
		return nil, err
	}
	event := PolicyDecisionRecorded{
		TurnID: command.TurnID, ItemID: command.ItemID, CallID: command.CallID,
		Name: command.Name, Effect: command.Effect, RuleID: command.RuleID, Reason: command.Reason,
	}
	if err := validatePolicyDecisionPayload(event, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return recordPolicyDecisionEvents(event), nil
}

func decideRequestApproval(state Session, command RequestApproval) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindToolCall); err != nil {
		return nil, err
	}
	event := ApprovalRequested{
		TurnID: command.TurnID, ItemID: command.ItemID, ApprovalID: command.ApprovalID,
		CallID: command.CallID, Name: command.Name, Reason: command.Reason,
	}
	if err := validateApprovalRequestedPayload(event, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return requestApprovalEvents(event), nil
}

func decideResolveApproval(state Session, command ResolveApproval) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindToolCall); err != nil {
		return nil, err
	}
	event := ApprovalResolved{
		TurnID: command.TurnID, ItemID: command.ItemID,
		ApprovalID: command.ApprovalID, Decision: command.Decision,
	}
	if err := validateApprovalResolvedPayload(event, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return resolveApprovalEvents(event), nil
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

func decideDeleteSession(state Session, command DeleteSession) ([]UncommittedEvent, error) {
	if !state.Exists() {
		return nil, domainError(CodeSessionNotFound, "session not found")
	}
	if err := validateCommandSessionID(command.SessionID); err != nil {
		return nil, err
	}
	if command.SessionID != state.ID {
		return nil, domainError(CodeInvalidCommand, "command session ID does not match state")
	}
	if state.Status == SessionStatusDeleted {
		return nil, domainError(CodeSessionDeleted, "session is deleted")
	}
	if err := validateSession(state); err != nil {
		return nil, err
	}
	if state.ActiveTurn != nil {
		return nil, domainError(CodeTurnAlreadyRunning, "a turn is already running")
	}
	return deleteSessionEvents(), nil
}

func decideStartContextCompaction(state Session, command StartContextCompaction) ([]UncommittedEvent, error) {
	if err := requireSessionForCommand(state, command.SessionID); err != nil {
		return nil, err
	}
	if state.ContextCompaction != nil {
		return nil, domainError(CodeCompactionAlreadyRunning, "a context compaction is already running")
	}
	if err := validateContextCompactionStartedPayload(command.ContextCompactionStarted, CodeInvalidCommand); err != nil {
		return nil, err
	}
	switch command.Trigger {
	case ContextTriggerPreTurn, ContextTriggerManual:
		if state.ActiveTurn != nil {
			return nil, domainError(CodeTurnAlreadyRunning, "a turn is already running")
		}
	case ContextTriggerMidTurn, ContextTriggerOverflowRetry:
		if state.ActiveTurn == nil {
			return nil, domainError(CodeTurnNotRunning, "no turn is running")
		}
	}
	return []UncommittedEvent{{Event: command.ContextCompactionStarted}}, nil
}

func requireActiveContextCompactionForCommand(state Session, sessionID SessionID, id ContextCompactionID) error {
	if err := requireSessionForCommand(state, sessionID); err != nil {
		return err
	}
	if state.ContextCompaction == nil {
		return domainError(CodeCompactionNotRunning, "no context compaction is running")
	}
	if state.ContextCompaction.ID != id {
		return domainError(CodeCompactionMismatch, "command compaction ID does not match active compaction")
	}
	return nil
}

func decideCompleteContextCompaction(state Session, command CompleteContextCompaction) ([]UncommittedEvent, error) {
	if err := requireActiveContextCompactionForCommand(state, command.SessionID, command.ID); err != nil {
		return nil, err
	}
	if err := validateContextCompactionCompletedPayload(command.ContextCompactionCompleted, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return []UncommittedEvent{{Event: command.ContextCompactionCompleted}}, nil
}

func decideFailContextCompaction(state Session, command FailContextCompaction) ([]UncommittedEvent, error) {
	if err := requireActiveContextCompactionForCommand(state, command.SessionID, command.ID); err != nil {
		return nil, err
	}
	if err := validateContextCompactionFailedPayload(command.ContextCompactionFailed, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return []UncommittedEvent{{Event: command.ContextCompactionFailed}}, nil
}

func decideRecordContextPreparation(state Session, command RecordContextPreparation) ([]UncommittedEvent, error) {
	if _, err := requireRunningItemKindForCommand(state, command.SessionID, command.TurnID, command.ItemID, ItemKindAssistantMessage); err != nil {
		return nil, err
	}
	if err := validateContextPreparedPayload(command.ContextPreparedRecorded, CodeInvalidCommand); err != nil {
		return nil, err
	}
	return []UncommittedEvent{{Event: command.ContextPreparedRecorded}}, nil
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

func requireRunningItemKindForCommand(state Session, sessionID SessionID, turnID TurnID, itemID ItemID, kind ItemKind) (Item, error) {
	item, err := requireRunningItemForCommand(state, sessionID, turnID, itemID)
	if err != nil {
		return Item{}, err
	}
	if item.Kind != kind {
		return Item{}, domainError(CodeInvalidCommand, "active item kind does not match command")
	}
	return item, nil
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
	if item.ID == "" || item.TurnID != turn.ID || !validItemKind(item.Kind) || !validStateTimestamp(item.StartedAt) || item.StartedAt.Before(turn.LastTransitionAt) {
		return domainError(CodeInvalidCommand, "active item structure is invalid")
	}
	if _, err := ParseItemID(string(item.ID)); err != nil {
		return domainError(CodeInvalidCommand, "active item structure is invalid")
	}
	return nil
}
