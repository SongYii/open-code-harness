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
		return RunTurnResult{}, applicationError(CategoryConflict, CodeCommandIdentityMismatch, false, acquireErr)
	}
	if !owner {
		defer lease.release()
		return lease.wait(ctx)
	}
	defer func() {
		lease.publish(result, returnErr)
		lease.release()
	}()
	return service.runTurnOwned(ctx, request, requestDigest, lease)
}

func (service *Service) runTurnOwned(ctx context.Context, request RunTurnRequest, requestDigest Digest, lease *executionLease) (RunTurnResult, error) {

	state, err := service.LoadSession(ctx, request.SessionID)
	if err != nil {
		return RunTurnResult{}, err
	}
	if err := domain.CheckStartAssistantTurnEligibilityCompact(state); err != nil {
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
	decided, err := domain.DecideCompact(state, domain.StartAssistantTurn{SessionID: request.SessionID, TurnID: turnID, ItemID: itemID, Input: request.Input})
	if err != nil {
		return RunTurnResult{}, applicationError(CategoryValidation, "domain_rejected", false, err)
	}
	admission := &CommandAdmission{RunTurnRequestID: request.RequestID, RequestDigest: requestDigest, TurnID: turnID, ItemID: itemID}
	admissionIntent, err := BuildAppendIntent(service.clock, service.ids, service.authority, request.SessionID, state.Version, commandID, admission, decided)
	if err != nil {
		return RunTurnResult{}, err
	}
	lease.retainIntent(admissionIntent)
	runningState, admissionRecords, err := CommitAppendIntent(ctx, service.store, state, admissionIntent)
	if err != nil {
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
		}
		return RunTurnResult{}, err
	}
	lease.setPhase(executionPhaseRunning)
	runningResult := RunTurnResult{SessionID: request.SessionID, TurnID: turnID, ItemID: itemID, Status: domain.TurnStatusRunning, Records: admissionRecords}
	runResult, err := service.runner.Run(ctx, engine.RunRequest{ModelRequest: engine.ModelRequest{SessionID: request.SessionID, TurnID: turnID, ItemID: itemID, Input: request.Input}, MaxAssistantBytes: service.config.MaxAssistantBytes}, emitter)
	if err != nil {
		cleanupBase := context.WithoutCancel(ctx)
		cleanupCtx, cancel := context.WithTimeout(cleanupBase, service.config.TerminalCommitTimeout)
		defer cancel()
		return service.terminalizeExecutionFailure(cleanupCtx, ctx, runningState, runningResult, commandID, emitter, lease, mapRunError(err), err)
	}
	decided, err = domain.DecideCompact(runningState, domain.CompleteAssistantTurn{SessionID: request.SessionID, TurnID: turnID, ItemID: itemID, Text: runResult.Text})
	if err != nil {
		return cloneRunTurnResult(runningResult), applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}
	lease.setPhase(executionPhaseTerminalInFlight)
	terminalIntent, err := BuildAppendIntent(service.clock, service.ids, service.authority, request.SessionID, runningState.Version, commandID, nil, decided)
	if err != nil {
		return cloneRunTurnResult(runningResult), err
	}
	lease.retainIntent(terminalIntent)
	_, terminalRecords, err := CommitAppendIntent(ctx, service.store, runningState, terminalIntent)
	if err != nil {
		if ctx.Err() != nil && !isAppendOutcomeUnknown(err) {
			cleanupBase := context.WithoutCancel(ctx)
			cleanupCtx, cancel := context.WithTimeout(cleanupBase, service.config.TerminalCommitTimeout)
			defer cancel()
			primary := applicationError(CategoryCanceled, "canceled", false, errors.Join(ctx.Err(), err))
			return service.terminalizeExecutionFailure(cleanupCtx, ctx, runningState, runningResult, commandID, emitter, lease, primary, primary)
		}
		return cloneRunTurnResult(runningResult), err
	}
	completedResult := RunTurnResult{SessionID: request.SessionID, TurnID: turnID, ItemID: itemID, Status: domain.TurnStatusCompleted, Text: runResult.Text, TerminalCommitted: true, Records: concatenateRunTurnRecords(admissionRecords, terminalRecords)}
	if err := emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeAppendCompleted}); err != nil {
		return runTurnDeliveryFailure(completedResult, err)
	}
	if err := emitter.Emit(ctx, engine.RuntimePayload{Type: engine.RuntimeModelStreamCompleted}); err != nil {
		return runTurnDeliveryFailure(completedResult, err)
	}
	return cloneRunTurnResult(completedResult), nil
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
	if !result.TerminalCommitted || len(result.Records) < 2 {
		return nil
	}
	terminal := result.Records[len(result.Records)-2].Event
	switch event := terminal.(type) {
	case domain.AssistantMessageFailed:
		return applicationError(CategoryModel, event.Code, true, nil)
	case domain.AssistantMessageInterrupted:
		return applicationError(CategoryCanceled, event.Code, true, nil)
	default:
		return nil
	}
}

func isAppendOutcomeUnknown(err error) bool {
	var applicationErr *Error
	return errors.As(err, &applicationErr) && applicationErr != nil && applicationErr.Code == "append_outcome_unknown"
}

func (service *Service) terminalizeExecutionFailure(cleanupCtx context.Context, deliveryCtx context.Context, runningState domain.CompactSession, runningResult RunTurnResult, commandID domain.CommandID, emitter *engine.Emitter, lease *executionLease, primary *Error, executionCause error) (RunTurnResult, error) {
	terminalCommand, status, terminalSignal, stableCode := terminalCommandForExecution(runningResult, primary)
	decided, err := domain.DecideCompact(runningState, terminalCommand)
	if err != nil {
		return cloneRunTurnResult(runningResult), applicationError(CategoryInternal, "domain_transition_failed", false, errors.Join(executionCause, err))
	}
	lease.setPhase(executionPhaseTerminalInFlight)
	terminalIntent, err := BuildAppendIntent(service.clock, service.ids, service.authority, runningResult.SessionID, runningState.Version, commandID, nil, decided)
	if err != nil {
		return cloneRunTurnResult(runningResult), err
	}
	lease.retainIntent(terminalIntent)
	_, terminalRecords, err := CommitAppendIntent(cleanupCtx, service.store, runningState, terminalIntent)
	if err != nil {
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

func terminalCommandForExecution(result RunTurnResult, primary *Error) (domain.Command, domain.TurnStatus, engine.RuntimeEventType, string) {
	if primary.Category == CategoryCanceled {
		return domain.InterruptAssistantTurn{SessionID: result.SessionID, TurnID: result.TurnID, ItemID: result.ItemID, Code: domain.InterruptionCallerCanceled}, domain.TurnStatusInterrupted, engine.RuntimeModelStreamInterrupted, domain.InterruptionCallerCanceled
	}
	if primary.Category == CategoryDelivery {
		return domain.InterruptAssistantTurn{SessionID: result.SessionID, TurnID: result.TurnID, ItemID: result.ItemID, Code: domain.InterruptionDeliveryFailed}, domain.TurnStatusInterrupted, engine.RuntimeModelStreamInterrupted, domain.InterruptionDeliveryFailed
	}
	code, message := durableFailure(primary)
	return domain.FailAssistantTurn{SessionID: result.SessionID, TurnID: result.TurnID, ItemID: result.ItemID, Code: code, Message: message}, domain.TurnStatusFailed, engine.RuntimeModelStreamFailed, code
}
func durableFailure(primary *Error) (string, string) {
	switch primary.Code {
	case string(engine.CodeModelStartup):
		return string(engine.CodeModelStartup), "model failed before streaming"
	case string(engine.CodeModelStream):
		return string(engine.CodeModelStream), "model stream failed"
	case string(engine.CodeOutputLimit):
		return string(engine.CodeOutputLimit), "assistant output exceeded limit"
	case string(engine.CodeInvalidStream):
		return string(engine.CodeInvalidStream), "model stream violated contract"
	default:
		return "model_failure", "model failed"
	}
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
