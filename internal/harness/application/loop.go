package application

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/redact"
)

func LoggedEnvelopeBudget(maxAssistant, maxToolCallsPerStep int) int {
	if maxAssistant < 0 {
		maxAssistant = 0
	}
	if maxToolCallsPerStep < 0 {
		maxToolCallsPerStep = 0
	}
	return maxAssistant + maxToolCallsPerStep*MaxToolResultBytes + loggedEnvelopeToolSchemaSlack
}

type ownedTurn struct {
	request       RunTurnRequest
	lease         *executionLease
	commandID     domain.CommandID
	emitter       *engine.Emitter
	state         domain.Session
	result        RunTurnResult
	projection    *turnProjection
	assistantItem domain.ItemID
	toolCallID    string
	toolName      string
	toolItemID    domain.ItemID
	approvalID    domain.ApprovalID
	started       map[domain.ItemID]struct{}
	executed      map[domain.ItemID]struct{}
	stepIndex     uint32
	// attemptIndex and overflowRecoveries are Context-Engine-aware-only
	// (implementation plan Task 10, design §13.4/§15.3): attemptIndex is
	// the current assistant item's 1-based attempt counter (bumped by an
	// overflow retry, reset to 1 by each new Step); overflowRecoveries
	// counts overflow recoveries used so far in this Turn, in-memory only
	// for the lifetime of one RunTurn call, checked against
	// Config.Context.MaxOverflowRecoveriesPerTurn.
	attemptIndex       uint32
	overflowRecoveries uint32
}

type turnProjection struct {
	messages    []domain.ModelPromptMessage
	suffixStart int
	names       map[string]string
}

func newTurnProjection(input string) *turnProjection {
	return newTurnProjectionWithPrefix(nil, input)
}

func newTurnProjectionWithPrefix(prefix []domain.ModelPromptMessage, input string) *turnProjection {
	messages := make([]domain.ModelPromptMessage, 0, len(prefix)+1)
	messages = append(messages, clonePromptMessages(prefix)...)
	messages = append(messages, domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: input})
	return &turnProjection{
		messages:    messages,
		suffixStart: len(messages),
		names:       make(map[string]string),
	}
}

// newTurnProjectionFromMessages wraps an already-complete message list
// (a Context-Engine-prepared envelope) verbatim, with no synthetic
// trailing input message appended -- unlike newTurnProjectionWithPrefix,
// which always appends one. Mid-turn preparation (implementation plan
// Task 10) has no fresh user input to append; the envelope PrepareContext
// already materialized (checkpoint/tail through the last committed Tool
// Result) is exactly what the next dispatch must send.
func newTurnProjectionFromMessages(messages []domain.ModelPromptMessage) *turnProjection {
	cloned := clonePromptMessages(messages)
	return &turnProjection{messages: cloned, suffixStart: len(cloned), names: make(map[string]string)}
}

// projectPriorTurns folds committed events from earlier turns into model
// messages. The compact Session aggregate discards completed turns; the
// event log is the authority. The current turn is excluded because its
// user input is appended separately.
func projectPriorTurns(records []domain.RecordedEvent, current domain.TurnID) []domain.ModelPromptMessage {
	var messages []domain.ModelPromptMessage
	names := make(map[string]string)
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.TurnStarted:
			if event.TurnID == current || event.Input == "" {
				continue
			}
			messages = append(messages, domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: event.Input})
		case domain.AssistantMessageCompleted:
			if event.TurnID == current {
				continue
			}
			if len(event.ToolCalls) == 0 && event.Text == "" {
				continue
			}
			messages = append(messages, domain.ModelPromptMessage{
				Role:      domain.PromptRoleAssistant,
				Text:      event.Text,
				ToolCalls: cloneToolCallOffers(event.ToolCalls),
			})
		case domain.ToolCallStarted:
			if event.TurnID == current {
				continue
			}
			names[event.CallID] = event.Name
		case domain.ToolCallCompleted:
			if event.TurnID == current {
				continue
			}
			messages = append(messages, domain.ModelPromptMessage{
				Role:       domain.PromptRoleTool,
				Text:       event.Content,
				ToolCallID: event.CallID,
				Name:       names[event.CallID],
			})
		case domain.ToolCallFailed:
			if event.TurnID == current {
				continue
			}
			messages = append(messages, domain.ModelPromptMessage{
				Role:       domain.PromptRoleTool,
				Text:       event.Message,
				ToolCallID: event.CallID,
				Name:       names[event.CallID],
			})
		}
	}
	return messages
}

func (projection *turnProjection) Messages() []domain.ModelPromptMessage {
	return clonePromptMessages(projection.messages)
}

func (projection *turnProjection) Suffix() []domain.ModelPromptMessage {
	if projection == nil || projection.suffixStart >= len(projection.messages) {
		return nil
	}
	return clonePromptMessages(projection.messages[projection.suffixStart:])
}

func (projection *turnProjection) applyRecords(records []domain.RecordedEvent) {
	if projection == nil {
		return
	}
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.AssistantMessageCompleted:
			if len(event.ToolCalls) == 0 {
				continue
			}
			projection.suffixStart = len(projection.messages)
			projection.messages = append(projection.messages, domain.ModelPromptMessage{
				Role:      domain.PromptRoleAssistant,
				Text:      event.Text,
				ToolCalls: cloneToolCallOffers(event.ToolCalls),
			})
		case domain.ToolCallStarted:
			projection.names[event.CallID] = event.Name
		case domain.ToolCallCompleted:
			projection.messages = append(projection.messages, domain.ModelPromptMessage{
				Role:       domain.PromptRoleTool,
				Text:       event.Content,
				ToolCallID: event.CallID,
				Name:       projection.names[event.CallID],
			})
		case domain.ToolCallFailed:
			projection.messages = append(projection.messages, domain.ModelPromptMessage{
				Role:       domain.PromptRoleTool,
				Text:       event.Message,
				ToolCallID: event.CallID,
				Name:       projection.names[event.CallID],
			})
		}
	}
}

func (service *Service) catalogEnabled() bool {
	return service != nil && catalogHasSpecs(service.catalog)
}

// runAfterAdmission dispatches the first Provider attempt (and, when a
// Tool Catalog is configured, the rest of the Step loop) once admission is
// durable. contextPrefix/usedContextEngine let a Context-Engine-aware
// admission (Task 9 Step 2's runTurnOwnedWithContextEngine) hand this
// function the already-prepared, already-recorded history prefix instead
// of the legacy projectPriorTurns re-derivation -- the ONLY way to satisfy
// the Global Constraint that what gets dispatched exactly matches what
// admission just durably recorded. When usedContextEngine is false (every
// existing caller), behavior is byte-for-byte the pre-Context-Engine path.
func (service *Service) runAfterAdmission(ctx context.Context, request RunTurnRequest, lease *executionLease, state domain.Session, result RunTurnResult, commandID domain.CommandID, emitter *engine.Emitter, contextPrefix []domain.ModelPromptMessage, usedContextEngine bool) (RunTurnResult, error) {
	owned := &ownedTurn{
		request:       request,
		lease:         lease,
		commandID:     commandID,
		emitter:       emitter,
		state:         state,
		result:        result,
		assistantItem: result.ItemID,
		started:       make(map[domain.ItemID]struct{}),
		executed:      make(map[domain.ItemID]struct{}),
	}
	if usedContextEngine {
		owned.projection = newTurnProjectionWithPrefix(contextPrefix, request.Input)
		owned.attemptIndex = 1
		return service.runStepLoop(ctx, owned)
	}
	if !service.catalogEnabled() {
		return service.runSingleAttempt(ctx, owned)
	}
	if err := contextError(ctx); err != nil {
		return service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
	}
	records, err := ReadWholeStreamPinned(ctx, service.store, request.SessionID, 256)
	if err != nil {
		return cloneRunTurnResult(owned.result), err
	}
	owned.projection = newTurnProjectionWithPrefix(projectPriorTurns(records, result.TurnID), request.Input)
	return service.runStepLoop(ctx, owned)
}

func (service *Service) runSingleAttempt(ctx context.Context, owned *ownedTurn) (RunTurnResult, error) {
	runResult, err := service.runner.Run(ctx, engine.RunRequest{
		ModelRequest: engine.ModelRequest{
			SessionID: owned.result.SessionID,
			TurnID:    owned.result.TurnID,
			ItemID:    owned.assistantItem,
			Input:     owned.request.Input,
		},
		MaxAssistantBytes: service.config.MaxAssistantBytes,
	}, owned.emitter)
	if err != nil {
		cleanupBase := context.WithoutCancel(ctx)
		cleanupCtx, cancel := context.WithTimeout(cleanupBase, service.config.TerminalCommitTimeout)
		defer cancel()
		return service.terminalizeExecutionFailure(cleanupCtx, ctx, owned.state, owned.result, owned.assistantItem, owned.commandID, owned.emitter, owned.lease, mapRunError(err), err, runResult.Stats)
	}
	return service.completeAssistantTurn(ctx, owned, runResult)
}

func (service *Service) runStepLoop(ctx context.Context, owned *ownedTurn) (RunTurnResult, error) {
	for step := 1; step <= service.config.MaxSteps; step++ {
		owned.stepIndex = uint32(step)
		if err := service.ensureProjectionUnderCap(owned); err != nil {
			return service.failOwnedTurn(ctx, owned, CodeEnvelopeLimit, displayFailureSentence(CodeEnvelopeLimit))
		}
		if err := contextError(ctx); err != nil {
			return service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
		}
		runResult, err := service.runProviderAttempt(ctx, owned, engine.RunRequest{
			ModelRequest: engine.ModelRequest{
				SessionID: owned.result.SessionID,
				TurnID:    owned.result.TurnID,
				ItemID:    owned.assistantItem,
				Input:     owned.request.Input,
				Messages:  owned.projection.Messages(),
				Tools:     service.catalog.Schemas(),
			},
			MaxAssistantBytes: service.config.MaxAssistantBytes,
		})
		if err != nil {
			cleanupBase := context.WithoutCancel(ctx)
			cleanupCtx, cancel := context.WithTimeout(cleanupBase, service.config.TerminalCommitTimeout)
			defer cancel()
			return service.terminalizeExecutionFailure(cleanupCtx, ctx, owned.state, owned.result, owned.assistantItem, owned.commandID, owned.emitter, owned.lease, mapRunError(err), err, runResult.Stats)
		}
		if len(runResult.ToolCalls) == 0 {
			return service.completeAssistantTurn(ctx, owned, runResult)
		}
		if len(runResult.ToolCalls) > service.config.MaxToolCallsPerStep {
			return service.failOwnedTurn(ctx, owned, string(engine.CodeInvalidStream), displayFailureSentence(string(engine.CodeInvalidStream)))
		}
		runResult.Text = redact.Text(runResult.Text)
		decided, err := service.decideTurnTerminal(owned.state, owned.result.SessionID, owned.result.TurnID, owned.assistantItem, runResult.Stats, domain.CompleteAssistantMessage{
			SessionID: owned.result.SessionID,
			TurnID:    owned.result.TurnID,
			ItemID:    owned.assistantItem,
			Text:      runResult.Text,
			ToolCalls: toolCallOffers(runResult.ToolCalls),
		})
		if err != nil {
			return cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
		}
		if err := service.commitStepAppend(ctx, owned, decided); err != nil {
			return cloneRunTurnResult(owned.result), err
		}
		if err := contextError(ctx); err != nil {
			return service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
		}
		for _, call := range runResult.ToolCalls {
			done, result, execErr := service.executeOneTool(ctx, owned, call)
			if done {
				return result, execErr
			}
			if err := contextError(ctx); err != nil {
				return service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
			}
		}
		if step == service.config.MaxSteps {
			return service.failOwnedTurn(ctx, owned, CodeStepLimit, displayFailureSentence(CodeStepLimit))
		}
		if service.contextEnabled() {
			done, result, err := service.startNextStepWithContextEngine(ctx, owned)
			if done {
				return result, err
			}
			continue
		}
		itemID, err := service.newItemID(ctx)
		if err != nil {
			if contextError(ctx) != nil {
				return service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
			}
			return service.failOwnedTurn(ctx, owned, "model_failure", displayFailureSentence("model_failure"))
		}
		owned.assistantItem = itemID
		startEvents, err := service.decideStartAssistantStep(owned)
		if err != nil {
			return cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
		}
		if err := service.commitStepAppend(ctx, owned, startEvents); err != nil {
			return cloneRunTurnResult(owned.result), err
		}
	}
	return service.failOwnedTurn(ctx, owned, CodeStepLimit, displayFailureSentence(CodeStepLimit))
}

func (service *Service) completeAssistantTurn(ctx context.Context, owned *ownedTurn, runResult engine.RunResult) (RunTurnResult, error) {
	runResult.Text = redact.Text(runResult.Text)
	decided, err := service.decideTurnTerminal(owned.state, owned.result.SessionID, owned.result.TurnID, owned.assistantItem, runResult.Stats, domain.CompleteAssistantTurn{
		SessionID: owned.result.SessionID,
		TurnID:    owned.result.TurnID,
		ItemID:    owned.assistantItem,
		Text:      runResult.Text,
	})
	if err != nil {
		return cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	result, err := service.commitTerminalAppend(ctx, ctx, owned, decided, domain.TurnStatusCompleted, runResult.Text, nil)
	if err != nil {
		return result, err
	}
	if err := owned.emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeAppendCompleted}); err != nil {
		return runTurnDeliveryFailure(result, err)
	}
	if err := owned.emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeModelStreamCompleted}); err != nil {
		return runTurnDeliveryFailure(result, err)
	}
	return cloneRunTurnResult(result), nil
}

func (service *Service) decideStartAssistantStep(owned *ownedTurn) ([]domain.UncommittedEvent, error) {
	start, err := domain.Decide(owned.state, domain.StartAssistantMessage{
		SessionID: owned.result.SessionID,
		TurnID:    owned.result.TurnID,
		ItemID:    owned.assistantItem,
	})
	if err != nil {
		return nil, err
	}
	preview := owned.state.Clone()
	if preview.ActiveTurn == nil {
		return nil, errors.New("missing active turn")
	}
	preview.ActiveTurn.ActiveItem = &domain.Item{ID: owned.assistantItem, TurnID: owned.result.TurnID, Kind: domain.ItemKindAssistantMessage}
	recorded := service.stepRequestRecorded(owned.result.TurnID, owned.assistantItem, owned.projection.Suffix())
	requestEvents, err := domain.Decide(preview, domain.RecordModelRequest{SessionID: owned.result.SessionID, ModelRequestRecorded: recorded})
	if err != nil {
		return nil, err
	}
	return append(start, requestEvents...), nil
}

// startNextStepWithContextEngine is Task 10's mid-turn preparation (design
// §15.2): after every Tool Result event for a Step has committed (the
// caller, runStepLoop, only reaches here once its own tool-execution loop
// finishes) and before the next assistant item dispatches, it allocates
// the next Item/decision IDs, plans at a pinned post-tool head via
// PrepareContext (Trigger: mid_turn, no CurrentInput -- there is no new
// user text mid-turn), compacts only if that plan needs it, and appends
// the atomic assistant.message.started + context.prepared +
// model.request.recorded batch design §13.4 names for a subsequent Step
// (never a fourth turn.started -- the Turn is already running). It
// mirrors turn.go's contextAdmissionEvents/runTurnOwnedWithContextEngine
// for the Step case; the done bool matches executeOneTool's own calling
// convention (true = terminal, the caller returns result/err verbatim).
func (service *Service) startNextStepWithContextEngine(ctx context.Context, owned *ownedTurn) (bool, RunTurnResult, error) {
	itemID, err := service.newItemID(ctx)
	if err != nil {
		if contextError(ctx) != nil {
			result, cancelErr := service.cancelOwnedTurn(ctx, owned, domain.InterruptionCallerCanceled)
			return true, result, cancelErr
		}
		result, failErr := service.failOwnedTurn(ctx, owned, "model_failure", displayFailureSentence("model_failure"))
		return true, result, failErr
	}
	owned.assistantItem = itemID

	prepared, err := PrepareContext(ctx, service.contextOrchestratorDeps(), owned.state, PrepareContextInput{
		SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: itemID,
		Trigger: domain.ContextTriggerMidTurn, Tools: service.catalog.Schemas(),
	})
	if err != nil {
		return true, cloneRunTurnResult(owned.result), err
	}
	owned.state = prepared.State

	decisionID, sourceErr := service.ids.NewContextDecisionID()
	if mapped := generatedIDError(ctx, sourceErr); mapped != nil {
		return true, cloneRunTurnResult(owned.result), mapped
	}
	if _, err := domain.ParseContextDecisionID(string(decisionID)); err != nil {
		return true, cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}

	decided, err := midTurnStepEvents(owned.state, owned.result.SessionID, owned.result.TurnID, itemID, service.config.RequestIdentity, prepared, decisionID)
	if err != nil {
		return true, cloneRunTurnResult(owned.result), err
	}
	if err := service.commitStepAppend(ctx, owned, decided); err != nil {
		return true, cloneRunTurnResult(owned.result), err
	}
	owned.projection = newTurnProjectionFromMessages(prepared.Prepared.Envelope.Messages)
	owned.attemptIndex = 1
	return false, owned.result, nil
}

// midTurnStepEvents decides design §13.4's subsequent-Step admission
// batch (assistant.message.started + context.prepared +
// model.request.recorded) as one ordered slice for one BuildAppendIntent
// call, via commitStepAppend. It mirrors turn.go's contextAdmissionEvents
// exactly except it starts from StartAssistantMessage (the Turn already
// exists) instead of StartAssistantTurn.
func midTurnStepEvents(state domain.Session, sessionID domain.SessionID, turnID domain.TurnID, itemID domain.ItemID, identity *engine.RequestIdentity, prepared PrepareContextResult, decisionID domain.ContextDecisionID) ([]domain.UncommittedEvent, error) {
	startEvents, err := domain.Decide(state, domain.StartAssistantMessage{SessionID: sessionID, TurnID: turnID, ItemID: itemID})
	if err != nil {
		return nil, applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	preview := state.Clone()
	if preview.ActiveTurn == nil {
		return nil, applicationError(CategoryInternal, "domain_transition_failed", false, errors.New("missing active turn"))
	}
	preview.ActiveTurn.ActiveItem = &domain.Item{ID: itemID, TurnID: turnID, Kind: domain.ItemKindAssistantMessage}

	contextAndRequestEvents, err := contextPreparationAndRequestEvents(preview, sessionID, turnID, itemID, identity, prepared, domain.ContextTriggerMidTurn, 1, decisionID)
	if err != nil {
		return nil, err
	}
	return append(startEvents, contextAndRequestEvents...), nil
}

func (service *Service) stepRequestRecorded(turnID domain.TurnID, itemID domain.ItemID, messages []domain.ModelPromptMessage) domain.ModelRequestRecorded {
	identity := service.config.RequestIdentity
	recorded := domain.ModelRequestRecorded{
		TurnID: turnID, ItemID: itemID,
		Messages: clonePromptMessages(messages),
		Tools:    service.catalog.Schemas(),
	}
	if identity == nil {
		return recorded
	}
	recorded.AdapterFamily = identity.AdapterFamily
	recorded.ModelID = identity.ModelID
	recorded.EndpointID = identity.EndpointID
	recorded.NativeTools = string(identity.Profile.NativeTools)
	recorded.Images = string(identity.Profile.Images)
	recorded.StructuredOutput = string(identity.Profile.StructuredOutput)
	recorded.ReasoningFields = string(identity.Profile.ReasoningFields)
	recorded.PromptCache = string(identity.Profile.PromptCache)
	recorded.ContextWindowTokens = identity.Profile.ContextWindowTokens
	recorded.MaxOutputTokens = identity.Profile.MaxOutputTokens
	recorded.IncludeUsage = identity.IncludeUsage
	recorded.MaxTokensField = identity.MaxTokensField
	return recorded
}

func (service *Service) ensureProjectionUnderCap(owned *ownedTurn) error {
	size, err := serializedProjectionBytes(owned.projection.Messages(), service.catalog.Schemas())
	if err != nil {
		return err
	}
	if size > MaxProjectionBytes {
		return errProjectionOverCap
	}
	return nil
}

var errProjectionOverCap = errors.New("projection exceeds envelope cap")

func serializedProjectionBytes(messages []domain.ModelPromptMessage, toolSchemas []domain.ToolSchema) (int, error) {
	payload, err := json.Marshal(struct {
		Messages []domain.ModelPromptMessage `json:"messages"`
		Tools    []domain.ToolSchema         `json:"tools"`
	}{Messages: messages, Tools: toolSchemas})
	if err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (service *Service) commitStepAppend(ctx context.Context, owned *ownedTurn, events []domain.UncommittedEvent) error {
	if err := owned.lease.setPhase(executionPhaseStepAppendInFlight); err != nil {
		return storeContractViolation(err)
	}
	intent, err := BuildAppendIntent(service.clock, service.ids, service.authority.CurrentAuthority(), owned.result.SessionID, owned.state.Version, owned.commandID, nil, events)
	if err != nil {
		return err
	}
	if err := owned.lease.retainIntent(intent); err != nil {
		return storeContractViolation(err)
	}
	next, records, err := CommitAppendIntent(ctx, service.store, owned.state, intent)
	if err != nil {
		if isAppendOutcomeUnknown(err) {
			if retainErr := owned.lease.retainUnknown(executionPhaseStepAppendUnknown); retainErr != nil {
				return storeContractViolation(retainErr)
			}
			return service.resolveStepAppendUnknown(ctx, owned, intent)
		}
		return err
	}
	if err := owned.lease.setPhase(executionPhaseRunning); err != nil {
		return storeContractViolation(err)
	}
	owned.state = next
	owned.result.Records = concatenateRunTurnRecords(owned.result.Records, records)
	owned.projection.applyRecords(records)
	service.markCommittedToolFacts(owned, records)
	return nil
}

func (service *Service) resolveStepAppendUnknown(ctx context.Context, owned *ownedTurn, intent AppendIntent) error {
	resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.config.AppendResolutionTimeout)
	defer cancel()
	receipt, err := ResolveAppendIntent(resolveCtx, service.store, intent, service.appendResolutionConfig())
	if err != nil {
		return err
	}
	next, records, err := ApplyCommittedIntent(owned.state, intent, receipt)
	if err != nil {
		return err
	}
	if err := owned.lease.resumeAfterResolvedStepAppend(); err != nil {
		return storeContractViolation(err)
	}
	owned.state = next
	owned.result.Records = concatenateRunTurnRecords(owned.result.Records, records)
	owned.projection.applyRecords(records)
	service.markCommittedToolFacts(owned, records)
	return nil
}

func (service *Service) markCommittedToolFacts(owned *ownedTurn, records []domain.RecordedEvent) {
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.ToolCallStarted:
			owned.started[event.ItemID] = struct{}{}
		}
	}
}

func (service *Service) failOwnedTurn(ctx context.Context, owned *ownedTurn, code, message string) (RunTurnResult, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.config.TerminalCommitTimeout)
	defer cancel()
	decided, err := domain.Decide(owned.state, service.failCommand(owned, code, message))
	if err != nil {
		return cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	return service.commitTerminalAppend(cleanupCtx, ctx, owned, decided, domain.TurnStatusFailed, "", applicationError(CategoryModel, code, true, nil))
}

func (service *Service) cancelOwnedTurn(ctx context.Context, owned *ownedTurn, code string) (RunTurnResult, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.config.TerminalCommitTimeout)
	defer cancel()
	decided, err := domain.Decide(owned.state, service.interruptCommand(owned, code))
	if err != nil {
		return cloneRunTurnResult(owned.result), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	return service.commitTerminalAppend(cleanupCtx, ctx, owned, decided, domain.TurnStatusInterrupted, "", applicationError(CategoryCanceled, code, true, ctx.Err()))
}

func (service *Service) failCommand(owned *ownedTurn, code, message string) domain.Command {
	if item := activeItem(owned.state); item != nil {
		if item.Kind == domain.ItemKindToolCall {
			return domain.FailToolTurn{
				SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: item.ID,
				CallID: owned.toolCallID, Code: code, Message: message,
			}
		}
		return domain.FailAssistantTurn{
			SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: item.ID,
			Code: code, Message: message,
		}
	}
	return domain.FailTurn{SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, Code: code, Message: message}
}

func (service *Service) interruptCommand(owned *ownedTurn, code string) domain.Command {
	if item := activeItem(owned.state); item != nil {
		if item.Kind == domain.ItemKindToolCall {
			return domain.InterruptToolTurn{
				SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: item.ID,
				CallID: owned.toolCallID, Code: code, ApprovalID: owned.approvalID,
			}
		}
		return domain.InterruptAssistantTurn{
			SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: item.ID, Code: code,
		}
	}
	return domain.InterruptTurn{SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, Reason: code}
}

func (service *Service) commitTerminalAppend(commitCtx, deliveryCtx context.Context, owned *ownedTurn, events []domain.UncommittedEvent, status domain.TurnStatus, text string, committed error) (RunTurnResult, error) {
	if err := owned.lease.setPhase(executionPhaseTerminalInFlight); err != nil {
		return cloneRunTurnResult(owned.result), storeContractViolation(err)
	}
	intent, err := BuildAppendIntent(service.clock, service.ids, service.authority.CurrentAuthority(), owned.result.SessionID, owned.state.Version, owned.commandID, nil, events)
	if err != nil {
		return cloneRunTurnResult(owned.result), err
	}
	if err := owned.lease.retainIntent(intent); err != nil {
		return cloneRunTurnResult(owned.result), storeContractViolation(err)
	}
	_, records, err := CommitAppendIntent(commitCtx, service.store, owned.state, intent)
	if err != nil {
		if isAppendOutcomeUnknown(err) || commitCtx.Err() != nil {
			if retainErr := owned.lease.retainUnknown(executionPhaseTerminalUnknown); retainErr != nil {
				return cloneRunTurnResult(owned.result), storeContractViolation(retainErr)
			}
			return service.resolveTerminalUnknown(deliveryCtx, owned.lease, owned.state, owned.result, intent, owned.result.Records, owned.emitter)
		}
		return cloneRunTurnResult(owned.result), err
	}
	owned.result.Status = status
	owned.result.Text = text
	owned.result.TerminalCommitted = true
	owned.result.Records = concatenateRunTurnRecords(owned.result.Records, records)
	return cloneRunTurnResult(owned.result), committed
}

func (service *Service) newItemID(ctx context.Context) (domain.ItemID, error) {
	itemID, sourceErr := service.ids.NewItemID()
	if mapped := generatedIDError(ctx, sourceErr); mapped != nil {
		return "", mapped
	}
	if _, err := domain.ParseItemID(string(itemID)); err != nil {
		return "", applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	return itemID, nil
}

func activeItem(state domain.Session) *domain.Item {
	if state.ActiveTurn == nil {
		return nil
	}
	return state.ActiveTurn.ActiveItem
}

func toolCallOffers(calls []engine.ToolCall) []domain.ToolCallOffer {
	offers := make([]domain.ToolCallOffer, len(calls))
	for index, call := range calls {
		offers[index] = domain.ToolCallOffer{ID: call.ID, Name: call.Name, Arguments: call.Arguments}
	}
	return offers
}

func cloneToolCallOffers(offers []domain.ToolCallOffer) []domain.ToolCallOffer {
	if offers == nil {
		return nil
	}
	return append([]domain.ToolCallOffer(nil), offers...)
}

func clonePromptMessages(messages []domain.ModelPromptMessage) []domain.ModelPromptMessage {
	if messages == nil {
		return nil
	}
	cloned := make([]domain.ModelPromptMessage, len(messages))
	for index, message := range messages {
		cloned[index] = message
		cloned[index].ToolCalls = cloneToolCallOffers(message.ToolCalls)
	}
	return cloned
}
