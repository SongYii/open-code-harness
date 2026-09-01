package application

import (
	"context"
	"errors"

	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// contextOverflowFailureCode is the durable Provider failure code
// openaicompat/classify.go:103 assigns a pre-delta capacity rejection.
// Only this exact code, on a startup failure (before any stream event
// was read), is eligible for overflow recovery (design §15.3, CE-13).
const contextOverflowFailureCode = "context_overflow"

// runProviderAttempt runs one Provider attempt and, when the Context
// Engine is enabled, transparently recovers from a pre-delta
// context_overflow failure (design §15.3) before giving up: it forces an
// overflow plan against the latest committed source, requires at least a
// 10% estimated reduction, commits a new context.prepared +
// model.request.recorded pair with the next attempt index, and retries
// the Provider once per recovery, up to the configured per-Turn cap. Any
// other failure, a canceled caller, an ineligible/declined recovery, or
// cap exhaustion returns the LAST attempt's own error unchanged, ready
// for the caller's existing terminalizeExecutionFailure path -- exactly
// as if this function were absent.
func (service *Service) runProviderAttempt(ctx context.Context, owned *ownedTurn, request engine.RunRequest) (engine.RunResult, error) {
	runResult, err := service.runner.Run(ctx, request, owned.emitter)
	for err != nil && service.overflowRecoveryEligible(ctx, owned, err) {
		recovered, ok, recoverErr := service.recoverFromOverflow(ctx, owned, request)
		if recoverErr != nil {
			return engine.RunResult{}, recoverErr
		}
		if !ok {
			return runResult, err
		}
		owned.overflowRecoveries++
		request = recovered
		runResult, err = service.runner.Run(ctx, request, owned.emitter)
	}
	return runResult, err
}

func (service *Service) overflowRecoveryEligible(ctx context.Context, owned *ownedTurn, err error) bool {
	if !service.contextEnabled() {
		return false
	}
	if contextError(ctx) != nil {
		return false
	}
	if owned.overflowRecoveries >= service.maxOverflowRecoveriesPerTurn() {
		return false
	}
	return isPreDeltaContextOverflow(err)
}

// isPreDeltaContextOverflow reports whether err is exactly an
// engine.CodeModelStartup failure (produced before any stream delta or
// Tool Call was read -- runner.go's own Run never reaches its read loop
// on a startup failure) whose classified ProviderFailure durable code is
// "context_overflow". Any other failure -- including a context_overflow
// that somehow occurred mid-stream, which the design explicitly excludes
// (CE-13) -- is not recoverable here.
func isPreDeltaContextOverflow(err error) bool {
	var engineErr *engine.Error
	if !errors.As(err, &engineErr) || engineErr == nil || engineErr.Code != engine.CodeModelStartup {
		return false
	}
	var failure *engine.ProviderFailure
	return errors.As(engineErr.Cause, &failure) && failure != nil && failure.Code == contextOverflowFailureCode
}

// recoverFromOverflow implements one overflow-recovery attempt (design
// §15.3). ok=false (err=nil) means the caller should fall through to the
// original Provider failure unchanged: either no safe source prefix
// exists to cover (PrepareContext's own compaction bracket found nothing
// to compact even when forced), or the resulting envelope is not at
// least 10% smaller than the one that just overflowed. A non-nil err is
// a real infrastructure failure (ID generation, domain rejection, or the
// append itself) that must propagate honestly rather than being silently
// folded into the overflow failure.
func (service *Service) recoverFromOverflow(ctx context.Context, owned *ownedTurn, failedRequest engine.RunRequest) (engine.RunRequest, bool, error) {
	meter := service.config.Context.Meter
	previousTokens := meter.Estimate(contextengine.Envelope{Messages: failedRequest.Messages, Tools: failedRequest.Tools}).Tokens

	prepared, err := PrepareContext(ctx, service.contextOrchestratorDeps(), owned.state, PrepareContextInput{
		SessionID: owned.result.SessionID, TurnID: owned.result.TurnID, ItemID: owned.assistantItem,
		Trigger: domain.ContextTriggerOverflowRetry, Tools: failedRequest.Tools, Force: true,
	})
	if err != nil {
		return engine.RunRequest{}, false, err
	}
	owned.state = prepared.State
	if !prepared.CompactionRan {
		// No safe prefix existed to force a cut over -- design's own
		// "no safe prefix exists" decline, not an error.
		return engine.RunRequest{}, false, nil
	}

	newTokens := prepared.Prepared.EstimatedTotalTokens
	// At least 10% smaller than the request that just overflowed,
	// expressed as integer arithmetic (post*10 <= pre*9) to avoid
	// floating point, matching contextengine.ValidateSummary's own
	// shrink-check convention.
	if newTokens*10 > previousTokens*9 {
		return engine.RunRequest{}, false, nil
	}

	decisionID, sourceErr := service.ids.NewContextDecisionID()
	if mapped := generatedIDError(ctx, sourceErr); mapped != nil {
		return engine.RunRequest{}, false, mapped
	}
	if _, err := domain.ParseContextDecisionID(string(decisionID)); err != nil {
		return engine.RunRequest{}, false, applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}

	preview := owned.state.Clone()
	if preview.ActiveTurn == nil || preview.ActiveTurn.ActiveItem == nil {
		return engine.RunRequest{}, false, applicationError(CategoryInternal, "domain_transition_failed", false, errors.New("missing active item for overflow retry"))
	}
	attemptIndex := owned.attemptIndex + 1
	decided, err := contextPreparationAndRequestEvents(preview, owned.result.SessionID, owned.result.TurnID, owned.assistantItem, service.config.RequestIdentity, prepared, domain.ContextTriggerOverflowRetry, attemptIndex, decisionID)
	if err != nil {
		return engine.RunRequest{}, false, err
	}
	if err := service.commitStepAppend(ctx, owned, decided); err != nil {
		return engine.RunRequest{}, false, err
	}
	owned.attemptIndex = attemptIndex
	owned.projection = newTurnProjectionFromMessages(prepared.Prepared.Envelope.Messages)

	return engine.RunRequest{
		ModelRequest: engine.ModelRequest{
			SessionID: failedRequest.SessionID, TurnID: failedRequest.TurnID, ItemID: failedRequest.ItemID,
			Input: failedRequest.Input, Messages: owned.projection.Messages(), Tools: failedRequest.Tools,
		},
		MaxAssistantBytes: failedRequest.MaxAssistantBytes,
	}, true, nil
}
