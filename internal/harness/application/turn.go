package application

import (
	"context"
	"errors"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

type RunTurnRequest struct {
	SessionID domain.SessionID
	RequestID domain.RunTurnRequestID
	Input     string
	Sink      engine.RuntimeSink
}
type RunTurnResult struct {
	SessionID         domain.SessionID
	TurnID            domain.TurnID
	ItemID            domain.ItemID
	Status            domain.TurnStatus
	Text              string
	TerminalCommitted bool
	DeliveryWarning   error
	Records           []domain.RecordedEvent
}

func (service *Service) RunTurn(ctx context.Context, request RunTurnRequest) (result RunTurnResult, returnErr error) {
	if service == nil || !validRunTurnRequest(request) {
		return RunTurnResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if err := contextError(ctx); err != nil {
		return RunTurnResult{}, err
	}
	requestDigest, err := DigestRunTurnRequestV1(request.SessionID, request.Input)
	if err != nil {
		return RunTurnResult{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	lookup, err := service.store.FindCommandRequest(ctx, FindCommandRequestRequest{RunTurnRequestID: request.RequestID, SessionID: request.SessionID, RequestDigest: requestDigest})
	if !isNilValue(err) {
		return RunTurnResult{}, mapV2StoreError(ctx, err, "read")
	}
	if err := lookup.Validate(); err != nil {
		return RunTurnResult{}, storeContractViolation(err)
	}
	switch lookup.Kind {
	case CommandRequestLookupFound:
		return service.runTurnFound(ctx, request, requestDigest, *lookup.Record, true)
	case CommandRequestLookupIdentityMismatch:
		return RunTurnResult{}, applicationError(CategoryConflict, CodeCommandIdentityMismatch, false, nil)
	}
	lease, owner, acquireErr := service.executions.acquire(request.RequestID, request.SessionID, requestDigest)
	if acquireErr != nil {
		if errors.Is(acquireErr, errSessionUnresolved) {
			return RunTurnResult{}, appendOutcomeUnknown(acquireErr)
		}
		return RunTurnResult{}, applicationError(CategoryConflict, CodeCommandIdentityMismatch, false, acquireErr)
	}
	if !owner {
		defer lease.release()
		return lease.wait(ctx)
	}
	defer func() {
		if !isAppendOutcomeUnknown(returnErr) {
			_ = lease.publish(result, returnErr)
		}
		lease.release()
	}()
	return service.runTurnOwned(ctx, request, requestDigest, lease)
}

func (service *Service) runTurnOwned(ctx context.Context, request RunTurnRequest, requestDigest Digest, lease *executionLease) (RunTurnResult, error) {

	state, err := service.LoadSession(ctx, request.SessionID)
	if err != nil {
		return RunTurnResult{}, err
	}
	if err := domain.CheckStartAssistantTurnEligibility(state); err != nil {
		return RunTurnResult{}, applicationError(CategoryValidation, "domain_rejected", false, err)
	}
	turnID, sourceErr := service.ids.NewTurnID()
	if mapped := generatedIDError(ctx, sourceErr); mapped != nil {
		return RunTurnResult{}, mapped
	}
	if _, err := domain.ParseTurnID(string(turnID)); err != nil {
		return RunTurnResult{}, applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	itemID, sourceErr := service.ids.NewItemID()
	if mapped := generatedIDError(ctx, sourceErr); mapped != nil {
		return RunTurnResult{}, mapped
	}
	if _, err := domain.ParseItemID(string(itemID)); err != nil {
		return RunTurnResult{}, applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	commandID, sourceErr := service.ids.NewCommandID()
	if mapped := generatedIDError(ctx, sourceErr); mapped != nil {
		return RunTurnResult{}, mapped
	}
	if _, err := domain.ParseCommandID(string(commandID)); err != nil {
		return RunTurnResult{}, applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	emitter, err := engine.NewEmitter(request.Sink, engine.Correlation{SessionID: request.SessionID, TurnID: turnID, ItemID: itemID, CommandID: commandID})
	if err != nil {
		return RunTurnResult{}, applicationError(CategoryInternal, "emitter_construction_failed", false, err)
	}
	var schemas []domain.ToolSchema
	if service.catalogEnabled() {
		schemas = service.catalog.Schemas()
	}
	decided, err := domain.Decide(state, domain.StartAssistantTurn{SessionID: request.SessionID, TurnID: turnID, ItemID: itemID, Input: request.Input, Request: modelRequestSpec(service.config.RequestIdentity, request.Input, schemas)})
	if err != nil {
		return RunTurnResult{}, applicationError(CategoryValidation, "domain_rejected", false, err)
	}
	admission := &CommandAdmission{RunTurnRequestID: request.RequestID, RequestDigest: requestDigest, TurnID: turnID, ItemID: itemID}
	admissionIntent, err := BuildAppendIntent(service.clock, service.ids, service.authority, request.SessionID, state.Version, commandID, admission, decided)
	if err != nil {
		return RunTurnResult{}, err
	}
	if err := lease.retainIntent(admissionIntent); err != nil {
		return RunTurnResult{}, storeContractViolation(err)
	}
	runningState, admissionRecords, err := CommitAppendIntent(ctx, service.store, state, admissionIntent)
	if err != nil {
		if isAppendOutcomeUnknown(err) {
			if retainErr := lease.retainUnknown(executionPhaseAdmissionUnknown); retainErr != nil {
				return RunTurnResult{}, storeContractViolation(retainErr)
			}
			return service.resolveAdmissionUnknown(ctx, request, requestDigest, lease, state, admissionIntent, commandID, emitter)
		}
		if IsStoreCode(err, StoreCodeCommandRequestConflict) {
			lookup, lookupErr := service.store.FindCommandRequest(ctx, FindCommandRequestRequest{RunTurnRequestID: request.RequestID, SessionID: request.SessionID, RequestDigest: requestDigest})
			if !isNilValue(lookupErr) {
				return RunTurnResult{}, mapV2StoreError(ctx, lookupErr, "read")
			}
			if validateErr := lookup.Validate(); validateErr != nil {
				return RunTurnResult{}, storeContractViolation(validateErr)
			}
			if lookup.Kind == CommandRequestLookupFound {
				return service.runTurnFound(ctx, request, requestDigest, *lookup.Record, false)
			}
			if lookup.Kind == CommandRequestLookupIdentityMismatch {
				return RunTurnResult{}, applicationError(CategoryConflict, CodeCommandIdentityMismatch, false, nil)
			}
			return RunTurnResult{}, storeContractViolation(errors.New("command request conflict relookup was not found"))
		}
		return RunTurnResult{}, err
	}
	if err := lease.setPhase(executionPhaseRunning); err != nil {
		return RunTurnResult{}, storeContractViolation(err)
	}
	runningResult := RunTurnResult{SessionID: request.SessionID, TurnID: turnID, ItemID: itemID, Status: domain.TurnStatusRunning, Records: admissionRecords}
	return service.runAfterAdmission(ctx, request, lease, runningState, runningResult, commandID, emitter)
}

func (service *Service) runTurnFound(ctx context.Context, request RunTurnRequest, digest Digest, record CommandRequestRecord, attachLocal bool) (RunTurnResult, error) {
	if record.RunTurnRequestID != request.RequestID || record.SessionID != request.SessionID || record.RequestDigest != digest {
		return RunTurnResult{}, storeContractViolation(errors.New("found command record does not match lookup identity"))
	}
	records, err := ReadWholeStreamPinned(ctx, service.store, request.SessionID, 256)
	if err != nil {
		return RunTurnResult{}, err
	}
	result, err := ReconstructRequestResult(record, records)
	if err != nil {
		return RunTurnResult{}, mapV2StoreError(ctx, err, "read")
	}
	if result.Status != domain.TurnStatusRunning {
		return result, durableRequestTerminalError(result)
	}
	if !attachLocal {
		return RunTurnResult{}, applicationError(CategoryConflict, CodeReconciliationRequired, false, nil)
	}
	lease, attached := service.executions.attachExisting(request.RequestID, request.SessionID, digest)
	if !attached {
		return RunTurnResult{}, applicationError(CategoryConflict, CodeReconciliationRequired, false, nil)
	}
	defer lease.release()
	return lease.wait(ctx)
}

func durableRequestTerminalError(result RunTurnResult) error {
	if !result.TerminalCommitted {
		return nil
	}
	// turn event wins: tool cancel has no assistant terminal
	switch event := requestOutcomeEvent(result.Records).(type) {
	case domain.TurnFailed:
		return applicationError(CategoryModel, event.Code, true, nil)
	case domain.TurnInterrupted:
		return applicationError(CategoryCanceled, event.Reason, true, nil)
	case domain.AssistantMessageFailed:
		return applicationError(CategoryModel, event.Code, true, nil)
	case domain.AssistantMessageInterrupted:
		return applicationError(CategoryCanceled, event.Code, true, nil)
	default:
		return nil
	}
}

func (service *Service) resolveAdmissionUnknown(ctx context.Context, request RunTurnRequest, requestDigest Digest, lease *executionLease, state domain.Session, intent AppendIntent, commandID domain.CommandID, emitter *engine.Emitter) (RunTurnResult, error) {
	resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.config.AppendResolutionTimeout)
	defer cancel()
	receipt, err := ResolveAppendIntent(resolveCtx, service.store, intent, service.appendResolutionConfig())
	if err != nil {
		return RunTurnResult{}, err
	}
	runningState, admissionRecords, err := ApplyCommittedIntent(state, intent, receipt)
	if err != nil {
		return RunTurnResult{}, err
	}
	admission := intent.Request.Admission
	if admission == nil {
		return RunTurnResult{}, storeContractViolation(errors.New("resolved admission missing record"))
	}
	runningResult := RunTurnResult{SessionID: request.SessionID, TurnID: admission.TurnID, ItemID: admission.ItemID, Status: domain.TurnStatusRunning, Records: admissionRecords}
	if ctx.Err() != nil {
		return service.abandonAdmittedTurn(ctx, lease, runningState, runningResult, commandID, emitter)
	}
	if err := lease.resumeAfterResolvedAdmission(); err != nil {
		return RunTurnResult{}, storeContractViolation(err)
	}
	return service.runAfterAdmission(ctx, request, lease, runningState, runningResult, commandID, emitter)
}

func (service *Service) abandonAdmittedTurn(ctx context.Context, lease *executionLease, state domain.Session, runningResult RunTurnResult, commandID domain.CommandID, emitter *engine.Emitter) (RunTurnResult, error) {
	if err := lease.setPhase(executionPhaseCancelWon); err != nil {
		return cloneRunTurnResult(runningResult), storeContractViolation(err)
	}
	decided, err := domain.Decide(state, domain.InterruptAssistantTurn{
		SessionID: runningResult.SessionID, TurnID: runningResult.TurnID, ItemID: runningResult.ItemID,
		Code: domain.InterruptionRequestAbandoned, Message: "",
	})
	if err != nil {
		return cloneRunTurnResult(runningResult), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	if err := lease.setPhase(executionPhaseTerminalInFlight); err != nil {
		return cloneRunTurnResult(runningResult), storeContractViolation(err)
	}
	intent, err := BuildAppendIntent(service.clock, service.ids, service.authority, runningResult.SessionID, state.Version, commandID, nil, decided)
	if err != nil {
		return cloneRunTurnResult(runningResult), err
	}
	if err := lease.retainIntent(intent); err != nil {
		return cloneRunTurnResult(runningResult), storeContractViolation(err)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.config.TerminalCommitTimeout)
	defer cancel()
	_, terminalRecords, err := CommitAppendIntent(cleanupCtx, service.store, state, intent)
	if err != nil {
		if isAppendOutcomeUnknown(err) || cleanupCtx.Err() != nil {
			if retainErr := lease.retainUnknown(executionPhaseTerminalUnknown); retainErr != nil {
				return cloneRunTurnResult(runningResult), storeContractViolation(retainErr)
			}
			return service.resolveTerminalUnknown(ctx, lease, state, runningResult, intent, runningResult.Records, emitter)
		}
		return cloneRunTurnResult(runningResult), err
	}
	result := runningResult
	result.Status = domain.TurnStatusInterrupted
	result.TerminalCommitted = true
	result.Records = concatenateRunTurnRecords(runningResult.Records, terminalRecords)
	committed := applicationError(CategoryCanceled, domain.InterruptionRequestAbandoned, true, ctx.Err())
	if err := lease.publishRetained(result, committed); err != nil {
		_ = lease.publish(result, committed)
	}
	return cloneRunTurnResult(result), committed
}

func (service *Service) resolveTerminalUnknown(ctx context.Context, lease *executionLease, state domain.Session, runningResult RunTurnResult, intent AppendIntent, prior []domain.RecordedEvent, emitter *engine.Emitter) (RunTurnResult, error) {
	resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.config.AppendResolutionTimeout)
	defer cancel()
	receipt, err := ResolveAppendIntent(resolveCtx, service.store, intent, service.appendResolutionConfig())
	if err != nil {
		return cloneRunTurnResult(runningResult), err
	}
	_, terminalRecords, err := ApplyCommittedIntent(state, intent, receipt)
	if err != nil {
		return cloneRunTurnResult(runningResult), err
	}
	result := runningResult
	result.Records = concatenateRunTurnRecords(prior, terminalRecords)
	result.TerminalCommitted = true
	status, text, code, ok := classifyProposedTerminal(intent.Request.Events)
	if !ok {
		return cloneRunTurnResult(result), storeContractViolation(errors.New("resolved terminal intent has no terminal event"))
	}
	result.Status = status
	result.Text = text
	switch status {
	case domain.TurnStatusCompleted:
		if err := lease.publishRetained(result, nil); err != nil {
			_ = lease.publish(result, nil)
		}
		if emitErr := emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeAppendCompleted}); emitErr != nil {
			return runTurnDeliveryFailure(result, emitErr)
		}
		if emitErr := emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeModelStreamCompleted}); emitErr != nil {
			return runTurnDeliveryFailure(result, emitErr)
		}
		return cloneRunTurnResult(result), nil
	case domain.TurnStatusFailed:
		committed := applicationError(CategoryModel, code, true, nil)
		if err := lease.publishRetained(result, committed); err != nil {
			_ = lease.publish(result, committed)
		}
		return cloneRunTurnResult(result), committed
	case domain.TurnStatusInterrupted:
		committed := applicationError(CategoryCanceled, code, true, nil)
		if err := lease.publishRetained(result, committed); err != nil {
			_ = lease.publish(result, committed)
		}
		return cloneRunTurnResult(result), committed
	default:
		return cloneRunTurnResult(result), storeContractViolation(errors.New("resolved terminal intent has no terminal event"))
	}
}

func classifyProposedTerminal(events []ProposedEvent) (domain.TurnStatus, string, string, bool) {
	var text, code string
	var status domain.TurnStatus
	var hasTurn bool
	for _, proposed := range events {
		switch event := proposed.Event.(type) {
		case domain.AssistantMessageCompleted:
			text = event.Text
		case domain.TurnCompleted:
			status = domain.TurnStatusCompleted
			hasTurn = true
		case domain.TurnFailed:
			status = domain.TurnStatusFailed
			code = event.Code
			hasTurn = true
		case domain.TurnInterrupted:
			status = domain.TurnStatusInterrupted
			code = event.Reason
			hasTurn = true
		case domain.AssistantMessageFailed:
			if !hasTurn {
				status = domain.TurnStatusFailed
				code = event.Code
			}
		case domain.AssistantMessageInterrupted:
			if !hasTurn {
				status = domain.TurnStatusInterrupted
				code = event.Code
			}
		case domain.ToolCallFailed:
			if !hasTurn {
				status = domain.TurnStatusFailed
				code = event.Code
			}
		case domain.ToolCallInterrupted:
			if !hasTurn {
				status = domain.TurnStatusInterrupted
				code = event.Code
			}
		}
	}
	if hasTurn {
		if status != domain.TurnStatusCompleted {
			text = ""
		}
		return status, text, code, true
	}
	switch itemTerminalFromProposed(events).(type) {
	case domain.AssistantMessageCompleted:
		return domain.TurnStatusCompleted, text, "", true
	case domain.AssistantMessageFailed:
		return domain.TurnStatusFailed, "", code, true
	case domain.AssistantMessageInterrupted:
		return domain.TurnStatusInterrupted, "", code, true
	default:
		return "", "", "", false
	}
}

func (service *Service) reloadDurableWinner(ctx context.Context, request RunTurnRequest, digest Digest) (RunTurnResult, error) {
	lookup, err := service.store.FindCommandRequest(ctx, FindCommandRequestRequest{RunTurnRequestID: request.RequestID, SessionID: request.SessionID, RequestDigest: digest})
	if !isNilValue(err) {
		return RunTurnResult{}, mapV2StoreError(ctx, err, "read")
	}
	if err := lookup.Validate(); err != nil {
		return RunTurnResult{}, storeContractViolation(err)
	}
	if lookup.Kind != CommandRequestLookupFound {
		return RunTurnResult{}, storeContractViolation(errors.New("cas loser could not reload durable winner"))
	}
	return service.runTurnFound(ctx, request, digest, *lookup.Record, false)
}

func isAppendOutcomeUnknown(err error) bool {
	var applicationErr *Error
	return errors.As(err, &applicationErr) && applicationErr != nil && applicationErr.Code == "append_outcome_unknown"
}

func (service *Service) terminalizeExecutionFailure(cleanupCtx context.Context, deliveryCtx context.Context, runningState domain.Session, runningResult RunTurnResult, itemID domain.ItemID, commandID domain.CommandID, emitter *engine.Emitter, lease *executionLease, primary *Error, executionCause error, stats engine.AttemptStats) (RunTurnResult, error) {
	terminalCommand, status, terminalSignal, stableCode := terminalCommandForExecution(runningResult, itemID, primary)
	decided, err := service.decideTurnTerminal(runningState, runningResult.SessionID, runningResult.TurnID, itemID, stats, terminalCommand)
	if err != nil {
		return cloneRunTurnResult(runningResult), applicationError(CategoryInternal, "domain_transition_failed", false, errors.Join(executionCause, err))
	}
	if err := lease.setPhase(executionPhaseTerminalInFlight); err != nil {
		return cloneRunTurnResult(runningResult), storeContractViolation(err)
	}
	terminalIntent, err := BuildAppendIntent(service.clock, service.ids, service.authority, runningResult.SessionID, runningState.Version, commandID, nil, decided)
	if err != nil {
		return cloneRunTurnResult(runningResult), err
	}
	if err := lease.retainIntent(terminalIntent); err != nil {
		return cloneRunTurnResult(runningResult), storeContractViolation(err)
	}
	_, terminalRecords, err := CommitAppendIntent(cleanupCtx, service.store, runningState, terminalIntent)
	if err != nil {
		if isAppendOutcomeUnknown(err) {
			if retainErr := lease.retainUnknown(executionPhaseTerminalUnknown); retainErr != nil {
				return cloneRunTurnResult(runningResult), storeContractViolation(retainErr)
			}
			return service.resolveTerminalUnknown(deliveryCtx, lease, runningState, runningResult, terminalIntent, runningResult.Records, emitter)
		}
		if cleanupCtx.Err() != nil && IsCategory(err, CategoryCanceled) {
			err = applicationError(CategoryPersistence, "append_failed", false, err)
		}
		return cloneRunTurnResult(runningResult), terminalizationError(err, executionCause)
	}
	terminalResult := runningResult
	terminalResult.Status = status
	terminalResult.Text = ""
	terminalResult.TerminalCommitted = true
	terminalResult.Records = concatenateRunTurnRecords(runningResult.Records, terminalRecords)
	committedError := applicationError(primary.Category, primary.Code, true, executionCause)
	if err := emitter.Emit(deliveryCtx, engine.RuntimePayload{Type: engine.RuntimeAppendCompleted}); err != nil {
		return terminalExecutionDeliveryFailure(terminalResult, committedError, err)
	}
	if err := emitter.Emit(deliveryCtx, engine.RuntimePayload{Type: terminalSignal, Code: stableCode}); err != nil {
		return terminalExecutionDeliveryFailure(terminalResult, committedError, err)
	}
	return cloneRunTurnResult(terminalResult), committedError
}

func terminalCommandForExecution(result RunTurnResult, itemID domain.ItemID, primary *Error) (domain.Command, domain.TurnStatus, engine.RuntimeEventType, string) {
	if primary.Category == CategoryCanceled {
		return domain.InterruptAssistantTurn{SessionID: result.SessionID, TurnID: result.TurnID, ItemID: itemID, Code: domain.InterruptionCallerCanceled}, domain.TurnStatusInterrupted, engine.RuntimeModelStreamInterrupted, domain.InterruptionCallerCanceled
	}
	if primary.Category == CategoryDelivery {
		return domain.InterruptAssistantTurn{SessionID: result.SessionID, TurnID: result.TurnID, ItemID: itemID, Code: domain.InterruptionDeliveryFailed}, domain.TurnStatusInterrupted, engine.RuntimeModelStreamInterrupted, domain.InterruptionDeliveryFailed
	}
	code, message := durableFailure(primary)
	return domain.FailAssistantTurn{SessionID: result.SessionID, TurnID: result.TurnID, ItemID: itemID, Code: code, Message: message}, domain.TurnStatusFailed, engine.RuntimeModelStreamFailed, code
}
func durableFailure(primary *Error) (string, string) {
	var failure *engine.ProviderFailure
	if primary != nil && errors.As(primary.Cause, &failure) && failure != nil && allowedFailureCode(failure.Code) {
		return failure.Code, displayFailureSentence(failure.Code)
	}
	if primary == nil {
		return "model_failure", displayFailureSentence("model_failure")
	}
	switch primary.Code {
	case string(engine.CodeModelStartup), string(engine.CodeModelStream), string(engine.CodeOutputLimit), string(engine.CodeInvalidStream):
		return primary.Code, displayFailureSentence(primary.Code)
	default:
		return "model_failure", displayFailureSentence("model_failure")
	}
}

func displayFailureSentence(code string) string {
	switch code {
	case "provider_auth":
		return "provider rejected credentials"
	case "provider_quota":
		return "provider quota exhausted"
	case "provider_rate_limit":
		return "provider rate limited"
	case "provider_transient":
		return "provider temporarily unavailable"
	case "provider_permanent":
		return "provider rejected the request"
	case "capability_mismatch":
		return "provider returned an unsupported capability"
	case "context_overflow":
		return "provider context window exceeded"
	case "empty_response":
		return "provider returned an empty completion"
	case string(engine.CodeModelStartup):
		return "model failed before streaming"
	case string(engine.CodeModelStream):
		return "model stream failed"
	case string(engine.CodeOutputLimit):
		return "assistant output exceeded limit"
	case string(engine.CodeInvalidStream):
		return "model stream violated contract"
	case CodeStepLimit:
		return "turn exceeded the step limit"
	case CodeEnvelopeLimit:
		return "request envelope exceeded the size limit"
	default:
		return "model failed"
	}
}

func (service *Service) decideTurnTerminal(state domain.Session, sessionID domain.SessionID, turnID domain.TurnID, itemID domain.ItemID, stats engine.AttemptStats, terminal domain.Command) ([]domain.UncommittedEvent, error) {
	var events []domain.UncommittedEvent
	if service.config.RequestIdentity != nil && observedAttemptStats(stats) && assistantItemActive(state, itemID) {
		usage, err := domain.Decide(state, domain.RecordModelUsage{
			SessionID:          sessionID,
			ModelUsageRecorded: modelUsageFromStats(turnID, itemID, stats),
		})
		if err != nil {
			return nil, err
		}
		events = append(events, usage...)
	}
	decided, err := domain.Decide(state, terminal)
	if err != nil {
		return nil, err
	}
	return append(events, decided...), nil
}

func assistantItemActive(state domain.Session, itemID domain.ItemID) bool {
	item := activeItem(state)
	return item != nil && item.Kind == domain.ItemKindAssistantMessage && item.ID == itemID
}

func observedAttemptStats(stats engine.AttemptStats) bool {
	return stats.Usage != nil || stats.FinishReason != "" || stats.ProviderRequestID != "" || stats.LatencyMs > 0
}

func modelUsageFromStats(turnID domain.TurnID, itemID domain.ItemID, stats engine.AttemptStats) domain.ModelUsageRecorded {
	recorded := domain.ModelUsageRecorded{
		TurnID:            turnID,
		ItemID:            itemID,
		LatencyMs:         stats.LatencyMs,
		FinishReason:      stats.FinishReason,
		ProviderRequestID: stats.ProviderRequestID,
	}
	if stats.Usage != nil {
		recorded.InputTokens = stats.Usage.InputTokens
		recorded.OutputTokens = stats.Usage.OutputTokens
		recorded.CachedInputTokens = stats.Usage.CachedInputTokens
	}
	return recorded
}

func modelRequestSpec(identity *engine.RequestIdentity, input string, toolSchemas []domain.ToolSchema) *domain.ModelRequestSpec {
	if identity == nil {
		return nil
	}
	return &domain.ModelRequestSpec{
		AdapterFamily:       identity.AdapterFamily,
		ModelID:             identity.ModelID,
		EndpointID:          identity.EndpointID,
		NativeTools:         string(identity.Profile.NativeTools),
		Images:              string(identity.Profile.Images),
		StructuredOutput:    string(identity.Profile.StructuredOutput),
		ReasoningFields:     string(identity.Profile.ReasoningFields),
		PromptCache:         string(identity.Profile.PromptCache),
		ContextWindowTokens: identity.Profile.ContextWindowTokens,
		MaxOutputTokens:     identity.Profile.MaxOutputTokens,
		IncludeUsage:        identity.IncludeUsage,
		MaxTokensField:      identity.MaxTokensField,
		Messages:            []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: input}},
		Tools:               toolSchemas,
	}
}

func itemTerminalEvent(records []domain.RecordedEvent) domain.Event {
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.AssistantMessageCompleted, domain.AssistantMessageFailed, domain.AssistantMessageInterrupted:
			return event
		}
	}
	return nil
}

func itemTerminalFromProposed(events []ProposedEvent) domain.Event {
	for _, proposed := range events {
		switch event := proposed.Event.(type) {
		case domain.AssistantMessageCompleted, domain.AssistantMessageFailed, domain.AssistantMessageInterrupted:
			return event
		}
	}
	return nil
}
func terminalizationError(terminalCause error, executionCause error) error {
	var terminal *Error
	if errors.As(terminalCause, &terminal) && terminal != nil {
		return applicationError(terminal.Category, terminal.Code, false, errors.Join(executionCause, terminalCause))
	}
	return applicationError(CategoryInternal, "terminalization_failed", false, errors.Join(executionCause, terminalCause))
}
func terminalExecutionDeliveryFailure(result RunTurnResult, primary *Error, cause error) (RunTurnResult, error) {
	result.DeliveryWarning = runtimeDeliveryCause(cause)
	return cloneRunTurnResult(result), applicationError(primary.Category, primary.Code, true, errors.Join(primary.Cause, result.DeliveryWarning))
}
func validRunTurnRequest(request RunTurnRequest) bool {
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil {
		return false
	}
	if _, err := domain.ParseRunTurnRequestID(string(request.RequestID)); err != nil {
		return false
	}
	return validRequiredText(request.Input) && !isNilValue(request.Sink)
}
func mapRunError(cause error) *Error {
	runnerError, ok := cause.(*engine.Error)
	if !ok || runnerError == nil {
		return applicationError(CategoryInternal, "engine_contract_violation", false, cause)
	}
	switch runnerError.Code {
	case engine.CodeCanceled:
		return applicationError(CategoryCanceled, "canceled", false, cause)
	case engine.CodeOutputLimit:
		return applicationError(CategoryOutputLimit, string(engine.CodeOutputLimit), false, cause)
	case engine.CodeDelivery:
		return applicationError(CategoryDelivery, "runtime_delivery_failed", false, cause)
	case engine.CodeInvalidRequest:
		return applicationError(CategoryInternal, "engine_contract_violation", false, cause)
	case engine.CodeModelStartup, engine.CodeModelStream, engine.CodeInvalidStream:
		return applicationError(CategoryModel, string(runnerError.Code), false, cause)
	default:
		return applicationError(CategoryInternal, "engine_contract_violation", false, cause)
	}
}
func runTurnDeliveryFailure(result RunTurnResult, cause error) (RunTurnResult, error) {
	result.DeliveryWarning = runtimeDeliveryCause(cause)
	return cloneRunTurnResult(result), applicationError(CategoryDelivery, "runtime_delivery_failed", true, result.DeliveryWarning)
}
func runtimeDeliveryCause(cause error) error {
	var engineError *engine.Error
	if errors.As(cause, &engineError) && engineError != nil && engineError.Cause != nil {
		return engineError.Cause
	}
	return cause
}
func concatenateRunTurnRecords(batches ...[]domain.RecordedEvent) []domain.RecordedEvent {
	count := 0
	for _, batch := range batches {
		count += len(batch)
	}
	combined := make([]domain.RecordedEvent, 0, count)
	for _, batch := range batches {
		combined = append(combined, batch...)
	}
	return combined
}
func cloneRunTurnResult(result RunTurnResult) RunTurnResult {
	clone := result
	if records, err := domain.CloneRecordedEvents(result.Records); err == nil {
		clone.Records = records
	} else {
		clone.Records = nil
	}
	return clone
}
