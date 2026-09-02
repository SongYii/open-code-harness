package application

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// defaultContextPageLimit mirrors ReadWholeStreamPinned's own bound
// (loop.go) so a Scan-driven read behaves like every other pinned-head
// stream read this package already performs.
const defaultContextPageLimit = 256

// defaultCompactionCleanupTimeout mirrors DefaultTerminalCommitTimeout
// (service.go): the bound a failCompaction cleanup append gets once
// detached from a canceled caller context.
const defaultCompactionCleanupTimeout = DefaultTerminalCommitTimeout

// defaultSummarizeTimeout mirrors DefaultCompactionTimeout (service.go):
// the bound one summarizer call within a compaction bracket gets when
// ContextOrchestratorDeps.SummarizeTimeout is unset.
const defaultSummarizeTimeout = DefaultCompactionTimeout

// maxCompactionSummaryOutputBytes is the byte cap Collect enforces on one
// summarization call, matching contextengine's own 256 KiB absolute
// summary cap (summarizer_validation.go's maxSummaryBytes) rather than
// inventing a second constant for the same limit.
const maxCompactionSummaryOutputBytes = 256 * 1024

// contextEventStorePageSource adapts the real EventStore to
// contextengine.PageSource (design §6.2): a thin, mechanical translation,
// exactly as contextengine's planner.go anticipates.
type contextEventStorePageSource struct {
	store EventStore
}

func (source contextEventStorePageSource) ReadPage(ctx context.Context, sessionID domain.SessionID, request contextengine.PageRequest) (contextengine.PageResult, error) {
	page, err := source.store.ReadStream(ctx, ReadStreamRequest{
		SessionID: sessionID, AfterSequence: request.AfterSequence, Limit: request.Limit, HeadVersion: request.HeadVersion,
	})
	if err != nil {
		return contextengine.PageResult{}, err
	}
	return contextengine.PageResult{Records: page.Records, HeadVersion: page.HeadVersion, NextAfterSequence: page.NextAfterSequence, End: page.End}, nil
}

// EngineContextSummarizer implements ContextSummarizer over one shared
// *engine.TurnRunner (design §6.3): it calls engine.Model directly through
// TurnRunner.Collect, the "shared bounded stream collector" — the same
// stream lifecycle and closed engine.ProviderFailure taxonomy conversation
// attempts already use, but text-only, with no Tools, never entering
// RunTurn or emitting assistant deltas.
type EngineContextSummarizer struct {
	runner *engine.TurnRunner
}

var _ ContextSummarizer = (*EngineContextSummarizer)(nil)

// NewEngineContextSummarizer constructs a ContextSummarizer around an
// existing *engine.TurnRunner. Reusing the same runner the conversation
// path already holds (Service.runner) means a compaction attempt goes
// through the identical Model/credential/transport as a normal attempt —
// design §18's deliberate "no second Provider" choice.
func NewEngineContextSummarizer(runner *engine.TurnRunner) (*EngineContextSummarizer, error) {
	if runner == nil {
		return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
	}
	return &EngineContextSummarizer{runner: runner}, nil
}

func (summarizer *EngineContextSummarizer) Summarize(ctx context.Context, request ContextSummarizeRequest) (ContextSummarizeResult, error) {
	if summarizer == nil || summarizer.runner == nil {
		return ContextSummarizeResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	result, err := summarizer.runner.Collect(ctx, engine.CollectRequest{
		ModelRequest: engine.ModelRequest{
			SessionID:       request.SessionID,
			TurnID:          request.TurnID,
			ItemID:          request.ItemID,
			Input:           request.Content,
			Purpose:         engine.ModelRequestPurposeCompaction,
			MaxOutputTokens: request.MaxOutputTokens,
		},
		MaxOutputBytes: request.MaxOutputBytes,
	})
	if err != nil {
		return ContextSummarizeResult{}, err
	}
	var usage engine.TokenUsage
	if result.Stats.Usage != nil {
		usage = *result.Stats.Usage
	}
	return ContextSummarizeResult{Text: result.Text, Usage: usage}, nil
}

// ContextOrchestratorDeps bundles everything context preparation needs.
// Fields are explicit rather than read off *Service so this implementation
// plan Task 9 Step 1 delivery is fully exercised by this package's own
// tests standalone (calling PrepareContext directly), before a later,
// separately committed change wires it into RunTurn's actual
// admission/dispatch flow.
//
// That later wiring is deliberately not part of this change: design §13.4
// requires the admission batch's context.prepared + model.request.recorded
// pair to describe exactly what was dispatched, and turn.go's admission
// path (runTurnOwned) still commits the pre-existing, unrestructured
// admission-time ModelRequestRecorded via modelRequestSpec for a first
// attempt. Calling PrepareContext from RunTurn today, before that
// restructuring lands, would either silently diverge from what is durably
// recorded for the first attempt or require this orchestrator to special-
// case "ignore my own better envelope for attempt 1 only" — both worse
// than shipping this orchestrator complete and tested on its own, and
// wiring it into turn.go/loop.go together as one later, atomic change.
type ContextOrchestratorDeps struct {
	Store           EventStore
	IDs             IDGenerator
	Clock           Clock
	Authority       AuthoritySource
	CheckpointStore ContextCheckpointStore
	Summarizer      ContextSummarizer
	Meter           contextengine.Meter
	Budget          contextengine.Budget
	// PageLimit bounds each Scan page; zero uses defaultContextPageLimit.
	PageLimit uint32
	// CleanupTimeout bounds the context.compaction.failed append issued
	// when a compaction bracket must close after its own ctx was
	// canceled (design's "manual cancellation closes a started bracket
	// as failed within the configured cleanup timeout"); zero uses
	// defaultCompactionCleanupTimeout. It is applied via
	// context.WithoutCancel, mirroring Service.config.TerminalCommitTimeout's
	// own established pattern (turn.go's terminalizeExecutionFailure).
	CleanupTimeout time.Duration
	// AppendResolutionTimeout/AppendResolutionMaxOperations bound
	// ResolveAppendIntent's own retry loop when one of this orchestrator's
	// compaction-bracket appends (Start/Complete/Fail) returns an unknown
	// commit outcome -- mirroring Service.config's own
	// AppendResolutionTimeout/AppendResolutionMaxOperations
	// (service.appendResolutionConfig()) so a compaction append is
	// resolved exactly as durably as every other append this package
	// makes, never left permanently uncertain. Zero uses
	// DefaultAppendResolutionTimeout/DefaultAppendResolutionMaxOperations.
	AppendResolutionTimeout       time.Duration
	AppendResolutionMaxOperations uint32
	// SummarizeTimeout bounds one summarizer call within a compaction
	// bracket (design §21/§8's own CompactionTimeout config, distinct
	// from CleanupTimeout above). Zero uses defaultSummarizeTimeout.
	SummarizeTimeout time.Duration
	// MaxPrunedToolResultsPerRequest mirrors ContextConfig's own field of
	// the same name: how many oversized retained Tool Results
	// PrepareContext's final Materialize call may replace with
	// ProjectToolResult's marker-framed excerpt (design §10). Zero
	// disables pruning entirely.
	MaxPrunedToolResultsPerRequest uint32
}

func (deps ContextOrchestratorDeps) valid() bool {
	return !isNilValue(deps.Store) && !isNilValue(deps.IDs) && !isNilValue(deps.Clock) && !isNilValue(deps.Authority) &&
		!isNilValue(deps.CheckpointStore) && !isNilValue(deps.Summarizer) && !isNilValue(deps.Meter)
}

func (deps ContextOrchestratorDeps) cleanupTimeout() time.Duration {
	if deps.CleanupTimeout <= 0 {
		return defaultCompactionCleanupTimeout
	}
	return deps.CleanupTimeout
}

// cleanupContext detaches from ctx's own cancellation (which may be why a
// failCompaction call is happening at all) while still bounding the
// append attempt, exactly as turn.go's own terminal-commit cleanup paths
// do (context.WithoutCancel + a configured timeout).
func cleanupContext(ctx context.Context, deps ContextOrchestratorDeps) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), deps.cleanupTimeout())
}

func (deps ContextOrchestratorDeps) appendResolutionConfig() AppendResolutionConfig {
	timeout := deps.AppendResolutionTimeout
	if timeout <= 0 {
		timeout = DefaultAppendResolutionTimeout
	}
	maxOperations := deps.AppendResolutionMaxOperations
	if maxOperations == 0 {
		maxOperations = DefaultAppendResolutionMaxOperations
	}
	return AppendResolutionConfig{Timeout: timeout, MaxOperations: maxOperations}
}

func (deps ContextOrchestratorDeps) pageLimit() uint32 {
	if deps.PageLimit == 0 {
		return defaultContextPageLimit
	}
	return deps.PageLimit
}

func (deps ContextOrchestratorDeps) summarizeTimeout() time.Duration {
	if deps.SummarizeTimeout <= 0 {
		return defaultSummarizeTimeout
	}
	return deps.SummarizeTimeout
}

// PrepareContextInput is one request for Context Engine preparation
// (design §15.1/§15.2/§15.4). TurnID/ItemID must already be allocated by
// the caller (the design's own pre-turn flow allocates them before
// planning) even when Trigger is pre_turn/manual and no Turn has been
// admitted yet — they are reserved identifiers here, not yet-committed
// ones.
type PrepareContextInput struct {
	SessionID    domain.SessionID
	TurnID       domain.TurnID
	ItemID       domain.ItemID
	Trigger      string
	CurrentInput domain.ModelPromptMessage
	Tools        []domain.ToolSchema
	// Force skips the Budget.Trigger comparison and always attempts a
	// cut (contextengine.PlanInput.Force). Provider overflow recovery
	// (implementation plan Task 10, design §15.3) sets this: a Provider
	// just rejected the request as too large, which the deterministic
	// meter's own estimate may not have predicted as being over Trigger.
	Force bool
}

// PrepareContextResult is PrepareContext's output: the Session as of
// afterward (advanced past any compaction bracket that ran), whether a
// compaction bracket ran at all, and the materialized envelope plus
// evidence a caller folds into its own ContextPreparedRecorded/
// ModelRequestRecorded pair (design §7.4) once it has allocated an
// AttemptIndex and ContextDecisionID.
type PrepareContextResult struct {
	State             domain.Session
	CompactionRan     bool
	SourceHeadVersion uint64
	Prepared          contextengine.PreparedContext
}

// PrepareContext implements design §15.1's pre-turn (and §15.2/§15.4's
// mid-turn/manual, which share the same planning/bracket/materialize
// shape) preparation, up to and including any compaction bracket's own
// context.compaction.* appends. It never appends context.prepared or
// model.request.recorded itself (those require a running assistant item,
// per domain's decideRecordContextPreparation, and belong to the caller's
// own admission batch), and it never dispatches a Provider attempt.
func PrepareContext(ctx context.Context, deps ContextOrchestratorDeps, state domain.Session, input PrepareContextInput) (PrepareContextResult, error) {
	if err := contextError(ctx); err != nil {
		return PrepareContextResult{}, err
	}
	if !deps.valid() {
		return PrepareContextResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if _, err := domain.ParseSessionID(string(input.SessionID)); err != nil {
		return PrepareContextResult{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	if !validContextTrigger(input.Trigger) {
		return PrepareContextResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}

	previous, err := loadUsableCheckpoint(ctx, deps, input.SessionID)
	if err != nil {
		return PrepareContextResult{}, err
	}

	source := contextEventStorePageSource{store: deps.Store}
	scan, err := contextengine.Scan(ctx, source, input.SessionID, deps.pageLimit())
	if err != nil {
		return PrepareContextResult{}, mapContextEngineScanError(err)
	}

	units := scan.Units
	if previous != nil {
		units = unitsAfter(units, previous.Coverage.ThroughSequence)
	}
	plan, err := contextengine.SelectCutPoint(contextengine.PlanInput{
		Units: units, Budget: deps.Budget, Meter: deps.Meter, Tools: input.Tools, CurrentInput: input.CurrentInput, Force: input.Force,
	})
	if err != nil {
		return PrepareContextResult{}, mapContextEngineScanError(err)
	}

	if previous != nil {
		retainedTailTokens := deps.Meter.EstimateMessages(unitMessages(plan.RetainedUnits))
		replayErr := contextengine.ValidateCheckpointReplay(contextengine.ReplayValidationInput{
			Checkpoint: *previous, SessionID: input.SessionID, PinnedHeadSequence: scan.HeadVersion,
			SourceDigestProof: true, CurrentBudget: deps.Budget, RetainedTailTokens: retainedTailTokens,
		})
		if replayErr != nil {
			// A previously valid checkpoint failed replay validation (most
			// commonly a smaller route after a model switch). It is never
			// silently trusted: fall back to planning the complete,
			// uncheckpointed history from scratch (design §14.3).
			previous = nil
			plan, err = contextengine.SelectCutPoint(contextengine.PlanInput{
				Units: scan.Units, Budget: deps.Budget, Meter: deps.Meter, Tools: input.Tools, CurrentInput: input.CurrentInput, Force: input.Force,
			})
			if err != nil {
				return PrepareContextResult{}, mapContextEngineScanError(err)
			}
		}
	}

	result := PrepareContextResult{State: state, SourceHeadVersion: scan.HeadVersion}
	activeCheckpoint := previous

	if plan.NeedsCompaction {
		nextState, checkpoint, ran, bracketErr := runCompactionBracket(ctx, deps, state, input, scan.HeadVersion, previous, plan)
		if bracketErr != nil {
			return PrepareContextResult{}, bracketErr
		}
		state = nextState
		result.State = state
		result.CompactionRan = ran
		if checkpoint != nil {
			activeCheckpoint = checkpoint
		}
	}

	// When a checkpoint is active (whether it was already valid, or this
	// round just built/rolled one forward), plan.RetainedUnits is already
	// exclusive of whatever the checkpoint now covers, so it alone is the
	// raw tail to send. With no usable checkpoint at all -- none ever
	// existed, or compaction could not produce one and the request still
	// fits under HardInput uncompacted (design §16's "summary failure
	// below hard budget" policy) -- send the complete, uncompacted unit
	// set instead.
	retainedUnits := plan.RetainedUnits
	if activeCheckpoint == nil {
		retainedUnits = scan.Units
	}

	var checkpointArg *contextengine.ContextCheckpoint
	if activeCheckpoint != nil {
		checkpointArg = activeCheckpoint
	}
	result.Prepared = contextengine.Materialize(contextengine.MaterializeInput{
		Checkpoint: checkpointArg, RetainedTail: retainedUnits, CurrentInput: input.CurrentInput, Tools: input.Tools, Meter: deps.Meter,
		ProtectedTail: deps.Budget.ProtectedTail, MaxPrunedToolResults: deps.MaxPrunedToolResultsPerRequest, HardInput: deps.Budget.HardInput,
	})
	return result, nil
}

func validContextTrigger(trigger string) bool {
	switch trigger {
	case domain.ContextTriggerPreTurn, domain.ContextTriggerManual, domain.ContextTriggerMidTurn, domain.ContextTriggerOverflowRetry:
		return true
	default:
		return false
	}
}

func loadUsableCheckpoint(ctx context.Context, deps ContextOrchestratorDeps, sessionID domain.SessionID) (*contextengine.ContextCheckpoint, error) {
	lookup, err := deps.CheckpointStore.LoadLatestContextCheckpoint(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if lookup.Status != ContextCheckpointLookupFound {
		return nil, nil
	}
	checkpoint, err := checkpointFromRecord(sessionID, lookup.Checkpoint)
	if err != nil {
		return nil, applicationError(CategoryInternal, CodeContextCheckpointInvalid, false, err)
	}
	return &checkpoint, nil
}

func unitsAfter(units []contextengine.ContextUnit, throughSequence uint64) []contextengine.ContextUnit {
	var kept []contextengine.ContextUnit
	for _, unit := range units {
		if unit.LastSequence > throughSequence {
			kept = append(kept, unit)
		}
	}
	return kept
}

// currentInputMessage mirrors contextengine's own unexported
// currentInputMessages helper (planner.go): a not-yet-committed
// CurrentInputUnit is only a real message when it carries a Role or Text,
// matching design §7.1's CurrentInputUnit semantics.
func currentInputMessage(message domain.ModelPromptMessage) []domain.ModelPromptMessage {
	if message.Role == "" && message.Text == "" {
		return nil
	}
	return []domain.ModelPromptMessage{message}
}

func unitMessages(units []contextengine.ContextUnit) []domain.ModelPromptMessage {
	var messages []domain.ModelPromptMessage
	for _, unit := range units {
		messages = append(messages, unit.Messages...)
	}
	return messages
}

func countTurnUnits(units []contextengine.ContextUnit) uint64 {
	var count uint64
	for _, unit := range units {
		if unit.Kind == contextengine.UnitKindTurn {
			count++
		}
	}
	return count
}

// runCompactionBracket implements design §15's compaction bracket: exactly
// one Start->{Completed|Failed} pair per attempted strategy. It tries
// "summary" first; if that fails and the uncompacted request truly exceeds
// HardInput (not merely Trigger), it tries the deterministic "reset"
// fallback once (design §12, §16's failure policy table). Below HardInput,
// a summary failure is left as a logged failed bracket and the caller
// proceeds uncompacted (ran=false, checkpoint=nil) — never silently
// retried into a reset it does not need.
func runCompactionBracket(ctx context.Context, deps ContextOrchestratorDeps, state domain.Session, input PrepareContextInput, headVersion uint64, previous *contextengine.ContextCheckpoint, plan contextengine.PlanResult) (domain.Session, *contextengine.ContextCheckpoint, bool, error) {
	if len(plan.CoveredUnits) == 0 {
		// Nothing safe to cover (design's context_nothing_to_compact):
		// never open a bracket for an empty prefix.
		return state, nil, false, nil
	}

	compactionID, err := deps.IDs.NewContextCompactionID()
	if err != nil {
		return domain.Session{}, nil, false, applicationError(CategoryInternal, "id_generation_failed", false, err)
	}
	nextState, err := startCompaction(ctx, deps, state, input, compactionID, domain.ContextStrategySummary, headVersion, previous)
	if err != nil {
		return domain.Session{}, nil, false, err
	}
	state = nextState

	checkpoint, summaryErr := buildSummaryCheckpoint(ctx, deps, state.ID, input, headVersion, previous, plan, compactionID)
	if summaryErr == nil {
		completedState, completeErr := completeCompaction(ctx, deps, state, input.SessionID, compactionID, *checkpoint)
		if completeErr != nil {
			return domain.Session{}, nil, false, completeErr
		}
		return completedState, checkpoint, true, nil
	}

	// The caller's own cancellation must never silently become a reset
	// (Global Constraint, design §26/§12's ResetEligibility.CallerCanceled):
	// checked here, before the fail append, so a summary failure caused by
	// ctx itself being canceled is recorded as such and the reset ladder
	// below is skipped outright, never merely made less likely.
	callerCanceled := contextError(ctx) != nil

	cleanupCtx, cancel := cleanupContext(ctx, deps)
	failedState, failErr := failCompaction(cleanupCtx, deps, state, input.SessionID, compactionID, summaryFailureCode(summaryErr), safeFailureMessage(summaryErr))
	cancel()
	if failErr != nil {
		return domain.Session{}, nil, false, failErr
	}
	state = failedState

	if callerCanceled {
		return state, nil, false, nil
	}

	uncompactedEstimate := deps.Meter.Estimate(contextengine.Envelope{
		Messages: append(unitMessages(mergeUnits(previous, plan)), currentInputMessage(input.CurrentInput)...), Tools: input.Tools,
	}).Tokens
	if uncompactedEstimate <= deps.Budget.HardInput {
		// Below hard budget: log-and-proceed, per design §16.
		return state, nil, false, nil
	}
	if !resetEligible(deps, input, previous, plan, uncompactedEstimate, callerCanceled) {
		// No safe fallback exists; the caller proceeds uncompacted and a
		// downstream hard-budget rejection (outside this orchestrator's
		// Step 1 scope) is the caller's problem to raise.
		return state, nil, false, nil
	}

	resetCompactionID, err := deps.IDs.NewContextCompactionID()
	if err != nil {
		return domain.Session{}, nil, false, applicationError(CategoryInternal, "id_generation_failed", false, err)
	}
	state, err = startCompaction(ctx, deps, state, input, resetCompactionID, domain.ContextStrategyReset, headVersion, previous)
	if err != nil {
		return domain.Session{}, nil, false, err
	}
	resetCheckpoint, err := buildResetCheckpoint(ctx, deps, headVersion, input, previous, plan, resetCompactionID)
	if err != nil {
		cleanupCtx, cancel := cleanupContext(ctx, deps)
		failedState, failErr := failCompaction(cleanupCtx, deps, state, input.SessionID, resetCompactionID, CodeContextCheckpointInvalid, "deterministic reset checkpoint could not be constructed")
		cancel()
		if failErr != nil {
			return domain.Session{}, nil, false, failErr
		}
		return failedState, nil, false, nil
	}
	completedState, err := completeCompaction(ctx, deps, state, input.SessionID, resetCompactionID, *resetCheckpoint)
	if err != nil {
		return domain.Session{}, nil, false, err
	}
	return completedState, resetCheckpoint, true, nil
}

// mergeUnits returns the units an uncompacted request would carry: when no
// checkpoint exists yet, that is plan.CoveredUnits+plan.RetainedUnits
// (everything Scan found); when one does, PrepareContext already limited
// planning to post-checkpoint units, so the same concatenation is exactly
// the newly covered plus retained portion, which is what "proceed
// uncompacted" sends this round (the existing checkpoint, if any, still
// stands and is not itself re-sent raw).
func mergeUnits(previous *contextengine.ContextCheckpoint, plan contextengine.PlanResult) []contextengine.ContextUnit {
	merged := make([]contextengine.ContextUnit, 0, len(plan.CoveredUnits)+len(plan.RetainedUnits))
	merged = append(merged, plan.CoveredUnits...)
	merged = append(merged, plan.RetainedUnits...)
	return merged
}

func resetEligible(deps ContextOrchestratorDeps, input PrepareContextInput, previous *contextengine.ContextCheckpoint, plan contextengine.PlanResult, uncompactedEstimate uint64, callerCanceled bool) bool {
	if len(plan.CoveredUnits) == 0 {
		return false
	}
	marker := contextengine.BuildResetMarker("pending", plan.CoveredThroughSequence)
	resetMessages := append([]domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: marker}}, unitMessages(plan.RetainedUnits)...)
	resetMessages = append(resetMessages, currentInputMessage(input.CurrentInput)...)
	resetEstimate := deps.Meter.Estimate(contextengine.Envelope{Messages: resetMessages, Tools: input.Tools}).Tokens
	return contextengine.ResetEligible(contextengine.ResetEligibility{
		HardLimitExceeded:         uncompactedEstimate > deps.Budget.HardInput,
		RollingSummaryUnavailable: true,
		SafeCoveredPrefixExists:   true,
		ResetFitsHardInput:        resetEstimate <= deps.Budget.HardInput,
		// CallerCanceled alone forces ineligibility regardless of the other
		// four fields (design's own Global Constraint) -- runCompactionBracket
		// already returns before ever reaching this call when true; this
		// parameter is the second, independent guard over the same
		// invariant, not the only one.
		CallerCanceled: callerCanceled,
	})
}

func startCompaction(ctx context.Context, deps ContextOrchestratorDeps, state domain.Session, input PrepareContextInput, compactionID domain.ContextCompactionID, strategy string, headVersion uint64, previous *contextengine.ContextCheckpoint) (domain.Session, error) {
	priorCheckpointID := ""
	if previous != nil {
		priorCheckpointID = previous.ID
	}
	started := domain.ContextCompactionStarted{
		ID: compactionID, Trigger: input.Trigger, Strategy: strategy, BaseSourceHead: headVersion,
		PriorCheckpointID: priorCheckpointID, PromptVersion: contextengine.SummaryPromptVersion,
		SourceSchema: contextengine.SourceSchemaVersion, MeterID: deps.Meter.ID(),
	}
	events, err := domain.Decide(state, domain.StartContextCompaction{SessionID: input.SessionID, ContextCompactionStarted: started})
	if err != nil {
		return domain.Session{}, mapDomainDecideError(err)
	}
	nextState, _, err := appendCompactOrchestrator(ctx, deps, input.SessionID, state, events)
	if err != nil {
		return domain.Session{}, err
	}
	return nextState, nil
}

func completeCompaction(ctx context.Context, deps ContextOrchestratorDeps, state domain.Session, sessionID domain.SessionID, compactionID domain.ContextCompactionID, checkpoint contextengine.ContextCheckpoint) (domain.Session, error) {
	completed := domain.ContextCompactionCompleted{ID: compactionID, Checkpoint: recordFromCheckpoint(checkpoint)}
	events, err := domain.Decide(state, domain.CompleteContextCompaction{SessionID: sessionID, ContextCompactionCompleted: completed})
	if err != nil {
		return domain.Session{}, mapDomainDecideError(err)
	}
	nextState, _, err := appendCompactOrchestrator(ctx, deps, sessionID, state, events)
	if err != nil {
		return domain.Session{}, err
	}
	return nextState, nil
}

func failCompaction(ctx context.Context, deps ContextOrchestratorDeps, state domain.Session, sessionID domain.SessionID, compactionID domain.ContextCompactionID, code, message string) (domain.Session, error) {
	failed := domain.ContextCompactionFailed{ID: compactionID, Code: code, Message: message}
	events, err := domain.Decide(state, domain.FailContextCompaction{SessionID: sessionID, ContextCompactionFailed: failed})
	if err != nil {
		return domain.Session{}, mapDomainDecideError(err)
	}
	nextState, _, err := appendCompactOrchestrator(ctx, deps, sessionID, state, events)
	if err != nil {
		return domain.Session{}, err
	}
	return nextState, nil
}

// appendCompactOrchestrator mirrors append.go's own appendCompact helper
// but over ContextOrchestratorDeps rather than *Service, since this
// orchestrator does not hold a *Service (see ContextOrchestratorDeps's own
// doc comment for why).
func appendCompactOrchestrator(ctx context.Context, deps ContextOrchestratorDeps, sessionID domain.SessionID, state domain.Session, events []domain.UncommittedEvent) (domain.Session, []domain.RecordedEvent, error) {
	commandID, err := deps.IDs.NewCommandID()
	if err != nil {
		return domain.Session{}, nil, applicationError(CategoryInternal, "id_generation_failed", false, err)
	}
	intent, err := BuildAppendIntent(deps.Clock, deps.IDs, deps.Authority.CurrentAuthority(), sessionID, state.Version, commandID, nil, events)
	if err != nil {
		return domain.Session{}, nil, err
	}
	nextState, records, err := CommitAppendIntent(ctx, deps.Store, state, intent)
	if err == nil {
		return nextState, records, nil
	}
	if !isAppendOutcomeUnknown(err) {
		return domain.Session{}, nil, err
	}
	// design §16: "a completion-append unknown outcome is resolved before
	// any further summarization is attempted" -- this compaction-bracket
	// append (Start, Complete, or Fail) is never left permanently
	// uncertain, mirroring turn.go's own resolveAdmissionUnknown pattern.
	resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deps.appendResolutionConfig().Timeout)
	defer cancel()
	receipt, resolveErr := ResolveAppendIntent(resolveCtx, deps.Store, intent, deps.appendResolutionConfig())
	if resolveErr != nil {
		return domain.Session{}, nil, resolveErr
	}
	return ApplyCommittedIntent(state, intent, receipt)
}

func buildSummaryCheckpoint(ctx context.Context, deps ContextOrchestratorDeps, sessionID domain.SessionID, input PrepareContextInput, headVersion uint64, previous *contextengine.ContextCheckpoint, plan contextengine.PlanResult, compactionID domain.ContextCompactionID) (*contextengine.ContextCheckpoint, error) {
	return buildSummaryCheckpointWithFocus(ctx, deps, sessionID, input, headVersion, previous, plan, compactionID, "")
}

// buildSummaryCheckpointWithFocus is buildSummaryCheckpoint plus an
// optional manual focus string (design §11.1/§15.4) rendered into its own
// "MANUAL FOCUS" prompt section -- additional data a human operator wants
// emphasized, never able to change the required output schema. The
// automatic paths (Tasks 9/10) always call buildSummaryCheckpoint with
// focus == "".
func buildSummaryCheckpointWithFocus(ctx context.Context, deps ContextOrchestratorDeps, sessionID domain.SessionID, input PrepareContextInput, headVersion uint64, previous *contextengine.ContextCheckpoint, plan contextengine.PlanResult, compactionID domain.ContextCompactionID, focus string) (*contextengine.ContextCheckpoint, error) {
	content := renderSummaryPrompt(previous, plan.CoveredUnits, focus)
	contentTokens := deps.Meter.EstimateMessages([]domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: content}})
	if contentTokens > deps.Budget.HardInput {
		// Disclosed Step 1 scope narrowing: this implementation performs
		// single-shot summarization only (SummaryChunks is always 1 when
		// it succeeds). Content too large for one summarizer call is
		// treated as "rolling summary unavailable" and left to the
		// deterministic reset fallback (design §12), never split into
		// multiple chunked summarizer calls — a real, disclosed gap
		// relative to the design's own multi-chunk rolling successor,
		// closed by a follow-up change if a benchmark shows it matters.
		return nil, fmt.Errorf("%s: source material for one summarizer call exceeds hard input budget", CodeContextSummaryFailed)
	}

	summarizeCtx, cancelSummarize := context.WithTimeout(ctx, deps.summarizeTimeout())
	summarizeResult, err := deps.Summarizer.Summarize(summarizeCtx, ContextSummarizeRequest{
		SessionID: sessionID, TurnID: input.TurnID, ItemID: input.ItemID, Content: content,
		MaxOutputTokens: uint32(minUint64(deps.Budget.SummaryOutputCap, 1<<31)), MaxOutputBytes: maxCompactionSummaryOutputBytes,
	})
	cancelSummarize()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", CodeContextSummaryFailed, err)
	}

	coveredThroughSequence := plan.CoveredThroughSequence
	coveredRecords, err := readSourceRecordsRange(ctx, deps.Store, sessionID, headVersion, priorThroughSequence(previous), coveredThroughSequence)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", CodeContextCheckpointInvalid, err)
	}
	seed := contextengine.InitialSourceDigest()
	priorCoveredEventCount := uint64(0)
	priorCoveredTurnCount := uint64(0)
	previousID := ""
	if previous != nil {
		seed = previous.Coverage.SourceDigest
		priorCoveredEventCount = previous.Coverage.CoveredEventCount
		priorCoveredTurnCount = previous.Coverage.CoveredTurnCount
		previousID = previous.ID
	}
	digest, newCount, err := contextengine.ExtendSourceDigestOverRecords(seed, coveredRecords)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", CodeContextCheckpointInvalid, err)
	}

	retainedTailTokens := deps.Meter.EstimateMessages(unitMessages(plan.RetainedUnits))
	coveredSourceTokens := deps.Meter.EstimateMessages(unitMessages(plan.CoveredUnits))
	prePassMessages := append(unitMessages(mergeUnits(previous, plan)), currentInputMessage(input.CurrentInput)...)
	prePassTokens := deps.Meter.Estimate(contextengine.Envelope{Messages: prePassMessages, Tools: input.Tools}).Tokens

	postPassMessages := append([]domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: summarizeResult.Text}}, unitMessages(plan.RetainedUnits)...)
	postPassMessages = append(postPassMessages, currentInputMessage(input.CurrentInput)...)
	postPassTokens := deps.Meter.Estimate(contextengine.Envelope{Messages: postPassMessages, Tools: input.Tools}).Tokens

	validation := contextengine.ValidateSummary(contextengine.SummaryValidationInput{
		RawOutput: summarizeResult.Text, TerminatedNormally: true, ContainsToolCall: false, ContainsNonText: false,
		SummaryOutputCap: deps.Budget.SummaryOutputCap, Meter: deps.Meter,
		PrePassRequestTokens: prePassTokens, PostPassRequestTokens: postPassTokens, HardInput: deps.Budget.HardInput,
		CoveredSourceTokens: coveredSourceTokens,
	})
	if !validation.Valid {
		return nil, fmt.Errorf("%s: %s", CodeContextSummaryInvalid, validation.FailureReason)
	}

	checkpointID, err := newCheckpointID(compactionID)
	if err != nil {
		return nil, err
	}
	checkpointTokens := deps.Meter.EstimateMessages([]domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: validation.RedactedText}})
	checkpoint := contextengine.ContextCheckpoint{
		ID: checkpointID, SessionID: sessionID, Kind: contextengine.CheckpointKindRollingSummary,
		SourceSchema: contextengine.SourceSchemaVersion, SummaryFormat: contextengine.SummaryFormatVersion, PromptVersion: contextengine.SummaryPromptVersion,
		Coverage: contextengine.Coverage{
			CoveredEventCount: priorCoveredEventCount + newCount,
			CoveredTurnCount:  priorCoveredTurnCount + countTurnUnits(plan.CoveredUnits),
			ThroughSequence:   coveredThroughSequence,
			SourceDigest:      digest,
		},
		PreviousCheckpointID: previousID, Summary: validation.RedactedText,
		TokensBefore: prePassTokens, CheckpointTokens: checkpointTokens, RetainedTailTokens: retainedTailTokens,
		EstimatedRequestTokens: postPassTokens, SummarizerUsage: summarizeResult.Usage.OutputTokens, SummaryChunks: 1,
	}
	if err := contextengine.ValidateSuccessor(previous, checkpoint); err != nil {
		return nil, fmt.Errorf("%s: %w", CodeContextCheckpointInvalid, err)
	}
	return &checkpoint, nil
}

func buildResetCheckpoint(ctx context.Context, deps ContextOrchestratorDeps, headVersion uint64, input PrepareContextInput, previous *contextengine.ContextCheckpoint, plan contextengine.PlanResult, compactionID domain.ContextCompactionID) (*contextengine.ContextCheckpoint, error) {
	checkpointID, err := newCheckpointID(compactionID)
	if err != nil {
		return nil, err
	}
	marker := contextengine.BuildResetMarker(checkpointID, plan.CoveredThroughSequence)
	seed := contextengine.InitialSourceDigest()
	priorCoveredEventCount := uint64(0)
	priorCoveredTurnCount := uint64(0)
	previousID := ""
	if previous != nil {
		seed = previous.Coverage.SourceDigest
		priorCoveredEventCount = previous.Coverage.CoveredEventCount
		priorCoveredTurnCount = previous.Coverage.CoveredTurnCount
		previousID = previous.ID
	}
	// The reset marker states no fact about covered content, so its own
	// digest chain step is never extended by fabricated text -- but
	// coverage itself still advances over the newly covered units' own
	// real canonical records, exactly as a rolling summary's own digest
	// does: the digest identifies *which canonical events* are covered,
	// independent of what (if any) derived text a checkpoint carries. A
	// digest that never actually advances (this function's own bug until
	// this task found it via a real store's independent hash-chain
	// re-verification -- every prior test used either a fake checkpoint
	// store or ValidateSuccessor's own structural-only check, neither of
	// which recomputes a digest from canonical content) would claim
	// coverage through a real sequence while proving nothing about it,
	// and a genuinely verifying store correctly rejects it as corrupt.
	coveredRecords, err := readSourceRecordsRange(ctx, deps.Store, input.SessionID, headVersion, priorThroughSequence(previous), plan.CoveredThroughSequence)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", CodeContextCheckpointInvalid, err)
	}
	digest, newCount, err := contextengine.ExtendSourceDigestOverRecords(seed, coveredRecords)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", CodeContextCheckpointInvalid, err)
	}

	retainedTailTokens := deps.Meter.EstimateMessages(unitMessages(plan.RetainedUnits))
	prePassMessages := append(unitMessages(mergeUnits(previous, plan)), currentInputMessage(input.CurrentInput)...)
	prePassTokens := deps.Meter.Estimate(contextengine.Envelope{Messages: prePassMessages, Tools: input.Tools}).Tokens
	markerTokens := deps.Meter.EstimateMessages([]domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: marker}})
	postPassMessages := append([]domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: marker}}, unitMessages(plan.RetainedUnits)...)
	postPassMessages = append(postPassMessages, currentInputMessage(input.CurrentInput)...)
	postPassTokens := deps.Meter.Estimate(contextengine.Envelope{Messages: postPassMessages, Tools: input.Tools}).Tokens

	checkpoint := contextengine.ContextCheckpoint{
		ID: checkpointID, SessionID: input.SessionID, Kind: contextengine.CheckpointKindSourceTailReset,
		SourceSchema: contextengine.SourceSchemaVersion,
		Coverage: contextengine.Coverage{
			CoveredEventCount: priorCoveredEventCount + newCount,
			CoveredTurnCount:  priorCoveredTurnCount + countTurnUnits(plan.CoveredUnits),
			ThroughSequence:   plan.CoveredThroughSequence,
			SourceDigest:      digest,
		},
		PreviousCheckpointID: previousID, Limitations: "deterministic reset: no summary of the omitted history was produced",
		TokensBefore: prePassTokens, CheckpointTokens: markerTokens, RetainedTailTokens: retainedTailTokens,
		EstimatedRequestTokens: postPassTokens, SummaryChunks: 0,
	}
	if err := contextengine.ValidateSuccessor(previous, checkpoint); err != nil {
		return nil, fmt.Errorf("%s: %w", CodeContextCheckpointInvalid, err)
	}
	return &checkpoint, nil
}

// countCoveredEvents mirrors ExtendSourceDigestOverRecords's own count
// semantics (one per IsSourceEvent record) but over already-projected
// units rather than raw records, since a reset checkpoint's digest chain
// is deliberately not extended (no fabricated text to hash), yet its
// CoveredEventCount evidence must still reflect what was actually folded
// in. A ContextUnit's own Messages count is not the same as its source
// event count (a StepUnit's several terminal results collapse from
// several source events into several messages 1:1, but a TurnUnit is one
// source event yielding one message) -- counting Messages here is exactly
// right because IsSourceEvent's six event types each contribute exactly
// one projected message (source.go/projector.go), never zero or more than
// one.
func countCoveredEvents(units []contextengine.ContextUnit) uint64 {
	var count uint64
	for _, unit := range units {
		count += uint64(len(unit.Messages))
	}
	return count
}

func priorThroughSequence(previous *contextengine.ContextCheckpoint) uint64 {
	if previous == nil {
		return 0
	}
	return previous.Coverage.ThroughSequence
}

func newCheckpointID(compactionID domain.ContextCompactionID) (string, error) {
	if strings.TrimSpace(string(compactionID)) == "" {
		return "", applicationError(CategoryInternal, "id_generation_failed", false, nil)
	}
	return "checkpoint-" + string(compactionID), nil
}

// renderSummaryPrompt combines contextengine's own versioned prompt asset
// with a deterministically rendered "PREVIOUS CHECKPOINT" section (only
// when previous is a rolling summary; a reset checkpoint carries no
// summary text to roll forward) and a "SOURCE MATERIAL" section over the
// units this compaction covers. Rendering happens here, in Application,
// because ContextSummarizeRequest's own contract (ports.go) states this
// port owns no prompt assembly of its own.
func renderSummaryPrompt(previous *contextengine.ContextCheckpoint, covered []contextengine.ContextUnit, focus string) string {
	var b strings.Builder
	b.WriteString(contextengine.SummaryPrompt)
	if previous != nil && previous.Kind == contextengine.CheckpointKindRollingSummary && previous.Summary != "" {
		b.WriteString("\n\n## PREVIOUS CHECKPOINT\n\n")
		b.WriteString(previous.Summary)
	}
	if focus != "" {
		b.WriteString("\n\n## MANUAL FOCUS\n\n")
		b.WriteString(focus)
	}
	b.WriteString("\n\n## SOURCE MATERIAL\n\n")
	renderUnits(&b, covered)
	return b.String()
}

func renderUnits(b *strings.Builder, units []contextengine.ContextUnit) {
	for _, unit := range units {
		for _, message := range unit.Messages {
			fmt.Fprintf(b, "[%s", message.Role)
			if message.Name != "" {
				fmt.Fprintf(b, " name=%s", message.Name)
			}
			if message.ToolCallID != "" {
				fmt.Fprintf(b, " tool_call_id=%s", message.ToolCallID)
			}
			b.WriteString("]\n")
			b.WriteString(message.Text)
			b.WriteString("\n\n")
		}
	}
}

// readSourceRecordsRange reads exactly the canonical records with
// afterSequence < record.Sequence <= throughSequence, pinned to
// headVersion so it observes the same immutable stream Scan already did.
func readSourceRecordsRange(ctx context.Context, store EventStore, sessionID domain.SessionID, headVersion, afterSequence, throughSequence uint64) ([]domain.RecordedEvent, error) {
	if throughSequence <= afterSequence {
		return nil, nil
	}
	var all []domain.RecordedEvent
	after := afterSequence
	head := headVersion
	for {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		page, err := store.ReadStream(ctx, ReadStreamRequest{SessionID: sessionID, AfterSequence: after, Limit: defaultContextPageLimit, HeadVersion: &head})
		if err != nil {
			return nil, err
		}
		for _, record := range page.Records {
			if record.Sequence > throughSequence {
				return all, nil
			}
			all = append(all, record)
		}
		if page.End {
			return all, nil
		}
		after = page.NextAfterSequence
	}
}

func checkpointFromRecord(sessionID domain.SessionID, record domain.ContextCheckpointRecord) (contextengine.ContextCheckpoint, error) {
	digest, err := decodeDigestHex(record.SourceDigestHex)
	if err != nil {
		return contextengine.ContextCheckpoint{}, err
	}
	return contextengine.ContextCheckpoint{
		ID: record.ID, SessionID: sessionID, Kind: contextengine.CheckpointKind(record.Kind),
		SourceSchema: record.SourceSchema, SummaryFormat: record.SummaryFormat, PromptVersion: record.PromptVersion,
		Coverage: contextengine.Coverage{
			CoveredEventCount: record.CoveredEventCount, CoveredTurnCount: record.CoveredTurnCount,
			ThroughSequence: record.ThroughSequence, SourceDigest: digest,
		},
		PreviousCheckpointID: record.PreviousCheckpointID, Summary: record.Summary, Limitations: record.Limitations,
		TokensBefore: record.TokensBefore, CheckpointTokens: record.CheckpointTokens, RetainedTailTokens: record.RetainedTailTokens,
		EstimatedRequestTokens: record.EstimatedRequestTokens, SummarizerRoute: record.SummarizerRoute, SummarizerUsage: record.SummarizerUsage,
		SummaryChunks: record.SummaryChunks, PrunedToolResultCount: record.PrunedToolResultCount,
	}, nil
}

func recordFromCheckpoint(checkpoint contextengine.ContextCheckpoint) domain.ContextCheckpointRecord {
	return domain.ContextCheckpointRecord{
		ID: checkpoint.ID, Kind: string(checkpoint.Kind), SourceSchema: checkpoint.SourceSchema,
		SummaryFormat: checkpoint.SummaryFormat, PromptVersion: checkpoint.PromptVersion,
		CoveredEventCount: checkpoint.Coverage.CoveredEventCount, CoveredTurnCount: checkpoint.Coverage.CoveredTurnCount,
		ThroughSequence: checkpoint.Coverage.ThroughSequence, SourceDigestHex: hex.EncodeToString(checkpoint.Coverage.SourceDigest[:]),
		PreviousCheckpointID: checkpoint.PreviousCheckpointID, Summary: checkpoint.Summary, Limitations: checkpoint.Limitations,
		TokensBefore: checkpoint.TokensBefore, CheckpointTokens: checkpoint.CheckpointTokens, RetainedTailTokens: checkpoint.RetainedTailTokens,
		EstimatedRequestTokens: checkpoint.EstimatedRequestTokens, SummarizerRoute: checkpoint.SummarizerRoute, SummarizerUsage: checkpoint.SummarizerUsage,
		SummaryChunks: checkpoint.SummaryChunks, PrunedToolResultCount: checkpoint.PrunedToolResultCount,
	}
}

func decodeDigestHex(value string) ([32]byte, error) {
	var digest [32]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return digest, fmt.Errorf("invalid source digest hex %q", value)
	}
	copy(digest[:], decoded)
	return digest, nil
}

func summaryFailureCode(err error) string {
	if strings.HasPrefix(err.Error(), CodeContextSummaryInvalid) {
		return CodeContextSummaryInvalid
	}
	return CodeContextSummaryFailed
}

// safeFailureMessage never includes raw model output or provider detail —
// design §13.2's "never embeds partial model output" for
// ContextCompactionFailed.
func safeFailureMessage(err error) string {
	code := summaryFailureCode(err)
	if code == CodeContextSummaryInvalid {
		return "summary output failed validation"
	}
	return "summary generation failed"
}

func mapDomainDecideError(err error) error {
	switch {
	case domain.IsCode(err, domain.CodeCompactionAlreadyRunning):
		return applicationError(CategoryConflict, CodeContextCompactionBusy, false, err)
	default:
		return applicationError(CategoryInternal, "domain_decide_failed", false, err)
	}
}

func mapContextEngineScanError(err error) error {
	switch {
	case errors.Is(err, contextengine.ErrProjectionInvalid):
		return applicationError(CategoryInternal, CodeContextProjectionInvalid, false, err)
	case errors.Is(err, contextengine.ErrHeadMismatch):
		return applicationError(CategoryInternal, "store_contract_violation", false, err)
	default:
		return err
	}
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// contextPreparationAndRequestEvents decides context.prepared +
// model.request.recorded as one ordered slice against preview (a state
// clone with the about-to-exist assistant Item already attached — see
// turn.go's contextAdmissionEvents and loop.go's midTurnStepEvents, both
// of which build preview themselves before calling this, since exactly
// how the Item comes to exist on preview differs between admission
// [StartAssistantTurn creates the Turn too] and a subsequent Step
// [StartAssistantMessage only]). Shared here so both call sites build the
// identical event pair the same way.
func contextPreparationAndRequestEvents(preview domain.Session, sessionID domain.SessionID, turnID domain.TurnID, itemID domain.ItemID, identity *engine.RequestIdentity, prepared PrepareContextResult, trigger string, attemptIndex uint32, decisionID domain.ContextDecisionID) ([]domain.UncommittedEvent, error) {
	contextPreparedRecorded := ContextPreparedRecordedFromResult(prepared, trigger, attemptIndex, decisionID, turnID, itemID)
	contextEvents, err := domain.Decide(preview, domain.RecordContextPreparation{SessionID: sessionID, ContextPreparedRecorded: contextPreparedRecorded})
	if err != nil {
		return nil, applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}

	modelRequestRecorded := ModelRequestRecordedFromEnvelope(identity, turnID, itemID, prepared.Prepared.Envelope, engine.ModelRequestPurposeConversation, attemptIndex, decisionID)
	requestEvents, err := domain.Decide(preview, domain.RecordModelRequest{SessionID: sessionID, ModelRequestRecorded: modelRequestRecorded})
	if err != nil {
		return nil, applicationError(CategoryInternal, "domain_transition_failed", false, err)
	}

	decided := make([]domain.UncommittedEvent, 0, len(contextEvents)+len(requestEvents))
	decided = append(decided, contextEvents...)
	decided = append(decided, requestEvents...)
	return decided, nil
}

// ContextPreparedRecordedFromResult builds design §7.4's per-attempt
// evidence from PrepareContext's own output. The caller (Task 9 Step 2's
// admission/mid-turn dispatch) supplies the trigger, AttemptIndex, and
// ContextDecisionID -- PrepareContextResult does not carry the trigger
// back, since the caller already gave it as PrepareContextInput.Trigger.
//
// SerializedEnvelopeBytes uses Prepared.ApproximateSerializedBytes: the
// design's own §7.4 wording asks for "the actual wire size Application's
// Provider Adapter produced," but this event commits as part of the
// admission batch strictly before that Adapter call is even allowed to
// happen (Global Constraint: no Provider call before context.prepared +
// model.request.recorded commit), so the true wire size cannot exist yet
// at the point this event is built. The deterministic approximation is
// the only value available at commit time; this is a disclosed, inherent
// property of committing evidence before dispatch, not an oversight.
func ContextPreparedRecordedFromResult(result PrepareContextResult, trigger string, attemptIndex uint32, decisionID domain.ContextDecisionID, turnID domain.TurnID, itemID domain.ItemID) domain.ContextPreparedRecorded {
	prepared := result.Prepared
	return domain.ContextPreparedRecorded{
		TurnID: turnID, ItemID: itemID, AttemptIndex: attemptIndex, ContextDecisionID: decisionID,
		Trigger: trigger, SourceHeadVersion: result.SourceHeadVersion,
		CheckpointID: prepared.CheckpointID, CheckpointKind: string(prepared.CheckpointKind),
		RawTailFromSequence: prepared.RetainedTailFromSequence, RawTailThroughSequence: prepared.RetainedTailThroughSequence,
		EstimatedMessageTokens: prepared.EstimatedMessageTokens, EstimatedToolSchemaTokens: prepared.EstimatedToolSchemaTokens,
		EstimatedTotalTokens: prepared.EstimatedTotalTokens, MeterID: prepared.MeterID,
		SerializedEnvelopeBytes: uint64(prepared.ApproximateSerializedBytes),
	}
}

// ModelRequestRecordedFromEnvelope builds a ModelRequestRecorded from a
// Context-Engine-prepared envelope and the active route's identity,
// mirroring loop.go's own stepRequestRecorded field-by-field mapping so a
// Context-Engine-aware request and a legacy one carry identical route
// metadata -- only Messages/Tools/Purpose/AttemptIndex/ContextDecisionID
// differ.
func ModelRequestRecordedFromEnvelope(identity *engine.RequestIdentity, turnID domain.TurnID, itemID domain.ItemID, envelope contextengine.Envelope, purpose engine.ModelRequestPurpose, attemptIndex uint32, decisionID domain.ContextDecisionID) domain.ModelRequestRecorded {
	recorded := domain.ModelRequestRecorded{
		TurnID: turnID, ItemID: itemID, Messages: envelope.Messages, Tools: envelope.Tools,
		Purpose: string(purpose), AttemptIndex: attemptIndex, ContextDecisionID: decisionID,
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
