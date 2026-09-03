package eval

import "time"

// AttemptExecutionLimits are the EvalSetLimits fields Runner threads into
// one Attempt's execution and collection window (design §19). This slice
// enforces the coarse, always-available bound — a wall-clock deadline
// wrapping fixture copy, execution, and collection together — and the
// evidence-collection bounds Task 7's CollectEvidence already accepts.
//
// design §19's finer per-Turn/per-action bounds (TurnActionTime,
// ProcessStartup, CancellationGrace, ShutdownGrace) require threading a
// budget check deeper into RunAttempt's own action loop than Task 6's
// contract exposes today, and per-Turn token/frozen-price cost budgets
// require RunAttempt to report per-Turn usage back to a caller, which it
// does not yet do either. Both are deliberately not enforced by this
// slice — approximating them here in a way that could silently under- or
// over-enforce the design's own stated bounds would be worse than an
// honestly narrower Runner — and are left for a follow-up once RunAttempt
// grows the hooks they need.
type AttemptExecutionLimits struct {
	WallTime         time.Duration
	CollectionLimits CollectionLimits
}

// resolveAttemptExecutionLimits maps one EvalSet's limits (defaulted via
// EvalSetLimits.withDefaults, the same defaulting EvalSet.Validate already
// applies) onto one Attempt's execution/collection bounds.
func resolveAttemptExecutionLimits(setLimits EvalSetLimits) AttemptExecutionLimits {
	setLimits = setLimits.withDefaults()
	return AttemptExecutionLimits{
		WallTime: setLimits.AttemptWallTime,
		CollectionLimits: CollectionLimits{
			MaxWorkspaceFileBytes: setLimits.OneArtifactBytes,
			MaxTotalBytes:         setLimits.TotalArtifactBytes,
			MaxFiles:              setLimits.ArtifactFiles,
			Timeout:               setLimits.EvidenceCollectionTime,
		},
	}
}
