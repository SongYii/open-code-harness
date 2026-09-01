package application

import (
	"context"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// maxCompactSessionFocusBytes is design §15.4's own bound on the optional
// manual-compaction focus string.
const maxCompactSessionFocusBytes = 4 * 1024

// CompactSessionRequest is one manual compaction request (design §15.4).
type CompactSessionRequest struct {
	SessionID domain.SessionID
	// Strategy is domain.ContextStrategySummary (the default, used when
	// empty) or domain.ContextStrategyReset. Unlike the automatic paths
	// (Tasks 9/10's own summary-then-reset ladder), manual compaction
	// never falls through from one strategy to the other: a failed
	// manual summary returns its own failure directly (design §16's
	// "manual summary instead returns its failure" rule).
	Strategy string
	// Focus is an optional operator-supplied string (bounded to 4 KiB
	// UTF-8) naming what the summary should emphasize. It is data inside
	// its own rendered prompt section (renderSummaryPrompt below), never
	// able to alter the required output schema -- design §11.1's own
	// "a manual focus string is data inside a dedicated field" rule.
	Focus string
}

// CompactSessionResult reports what Service.CompactSession did. Ran is
// false when nothing could be covered (design's own context_nothing_to_compact):
// a no-op, not an error.
type CompactSessionResult struct {
	Ran                    bool
	CheckpointID           string
	CheckpointKind         string
	CoveredEventCount      uint64
	CoveredTurnCount       uint64
	ThroughSequence        uint64
	TokensBefore           uint64
	CheckpointTokens       uint64
	EstimatedRequestTokens uint64
}

// CompactSession implements design §15.4's manual compaction flow. It
// requires an active, idle Session (no active Turn -- reuses Task 7's own
// StartContextCompaction eligibility, via the same startCompaction helper
// the automatic paths use) and requires service.contextEnabled(): manual
// compaction is exactly as dependent on a configured Context Engine as
// the automatic paths are.
//
// Disclosed scope: unlike RunTurn's executionRegistry, this method does
// not maintain its own local per-Session compaction lock (design §17's
// "Session-scoped compaction registry serializes manual and automatic
// compaction locally" is not built here). Correctness under concurrency
// is still fully guaranteed by the durable aggregate state: a concurrent
// second StartContextCompaction (manual or automatic) is rejected by
// Decide's own eligibility check (CodeCompactionAlreadyRunning) and Store
// CAS regardless. The only cost of this simplification is wasted local
// work (a redundant Scan/plan) under real local contention, which
// implementation plan Task 12's own dedicated concurrency race matrix is
// positioned to measure and close if it matters.
func (service *Service) CompactSession(ctx context.Context, request CompactSessionRequest) (CompactSessionResult, error) {
	if service == nil {
		return CompactSessionResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if err := contextError(ctx); err != nil {
		return CompactSessionResult{}, err
	}
	strategy := request.Strategy
	if strategy == "" {
		strategy = domain.ContextStrategySummary
	}
	if strategy != domain.ContextStrategySummary && strategy != domain.ContextStrategyReset {
		return CompactSessionResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil {
		return CompactSessionResult{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	if !utf8.ValidString(request.Focus) || len(request.Focus) > maxCompactSessionFocusBytes {
		return CompactSessionResult{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if !service.contextEnabled() {
		return CompactSessionResult{}, applicationError(CategoryValidation, "invalid_configuration", false, nil)
	}

	state, err := service.LoadSession(ctx, request.SessionID)
	if err != nil {
		return CompactSessionResult{}, err
	}

	deps := service.contextOrchestratorDeps()
	previous, err := loadUsableCheckpoint(ctx, deps, request.SessionID)
	if err != nil {
		return CompactSessionResult{}, err
	}
	source := contextEventStorePageSource{store: deps.Store}
	scan, err := contextengine.Scan(ctx, source, request.SessionID, deps.pageLimit())
	if err != nil {
		return CompactSessionResult{}, mapContextEngineScanError(err)
	}
	units := scan.Units
	if previous != nil {
		units = unitsAfter(units, previous.Coverage.ThroughSequence)
	}
	// Force: true -- design §15.4's own "below trigger, manual summary is
	// still allowed if a safe prefix exists." No CurrentInput/Tools:
	// manual compaction has no upcoming dispatch to plan around.
	plan, err := contextengine.SelectCutPoint(contextengine.PlanInput{Units: units, Budget: deps.Budget, Meter: deps.Meter, Force: true})
	if err != nil {
		return CompactSessionResult{}, mapContextEngineScanError(err)
	}
	if len(plan.CoveredUnits) == 0 {
		return CompactSessionResult{Ran: false}, nil
	}

	// buildSummaryCheckpoint/buildResetCheckpoint (context_orchestrator.go)
	// only use PrepareContextInput.TurnID/ItemID for the summarizer call's
	// own correlation IDs (design's compaction events never carry a
	// TurnID/ItemID at all) -- a manual compaction has no active Turn, so
	// these are synthetic, never persisted anywhere.
	syntheticTurnID, err := deps.IDs.NewTurnID()
	if err != nil {
		return CompactSessionResult{}, applicationError(CategoryInternal, "id_generation_failed", false, err)
	}
	syntheticItemID, err := deps.IDs.NewItemID()
	if err != nil {
		return CompactSessionResult{}, applicationError(CategoryInternal, "id_generation_failed", false, err)
	}
	input := PrepareContextInput{SessionID: request.SessionID, TurnID: syntheticTurnID, ItemID: syntheticItemID, Trigger: domain.ContextTriggerManual}

	compactionID, err := deps.IDs.NewContextCompactionID()
	if err != nil {
		return CompactSessionResult{}, applicationError(CategoryInternal, "id_generation_failed", false, err)
	}
	state, err = startCompaction(ctx, deps, state, input, compactionID, strategy, scan.HeadVersion, previous)
	if err != nil {
		return CompactSessionResult{}, err
	}

	var checkpoint *contextengine.ContextCheckpoint
	var buildErr error
	if strategy == domain.ContextStrategyReset {
		checkpoint, buildErr = buildResetCheckpoint(ctx, deps, scan.HeadVersion, input, previous, plan, compactionID)
	} else {
		checkpoint, buildErr = buildSummaryCheckpointWithFocus(ctx, deps, request.SessionID, input, scan.HeadVersion, previous, plan, compactionID, request.Focus)
	}
	if buildErr != nil {
		cleanupCtx, cancel := cleanupContext(ctx, deps)
		_, failErr := failCompaction(cleanupCtx, deps, state, request.SessionID, compactionID, summaryFailureCode(buildErr), safeFailureMessage(buildErr))
		cancel()
		if failErr != nil {
			return CompactSessionResult{}, failErr
		}
		// Manual summary/reset returns its own failure directly -- no
		// automatic ladder fallback (design §16).
		return CompactSessionResult{}, applicationError(CategoryModel, summaryFailureCode(buildErr), false, buildErr)
	}

	if _, err := completeCompaction(ctx, deps, state, request.SessionID, compactionID, *checkpoint); err != nil {
		return CompactSessionResult{}, err
	}
	return CompactSessionResult{
		Ran: true, CheckpointID: checkpoint.ID, CheckpointKind: string(checkpoint.Kind),
		CoveredEventCount: checkpoint.Coverage.CoveredEventCount, CoveredTurnCount: checkpoint.Coverage.CoveredTurnCount,
		ThroughSequence: checkpoint.Coverage.ThroughSequence, TokensBefore: checkpoint.TokensBefore,
		CheckpointTokens: checkpoint.CheckpointTokens, EstimatedRequestTokens: checkpoint.EstimatedRequestTokens,
	}, nil
}
