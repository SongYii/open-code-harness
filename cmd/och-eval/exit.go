package main

// Exit code classes (implementation plan Task 10). Stable across
// versions: a script gating CI on one of these must be able to rely on
// the number, not just "zero vs nonzero". A non-gating quality failure
// (a Score verdict that is not itself declared gating) is report data on
// stdout, not one of these exit classes — only an infrastructure problem
// or a required deterministic gate actually failing moves the exit code.
const (
	// exitOK means the requested command completed and produced its
	// report; for run, every Attempt reached a definite Outcome.
	exitOK = 0
	// exitInternal means this command itself failed in a way it cannot
	// classify into one of the stable classes below — a bug here, not a
	// fact about the evaluation.
	exitInternal = 1
	// exitValidation means the command's own flags, or a loaded document
	// (EvalSet/Scenario/Subject/Executor/Attempt), failed validation
	// before any Attempt could be created.
	exitValidation = 2
	// exitGateFailure means a required deterministic verifier's Score
	// verdict was Fail for at least one Attempt in report's scope.
	exitGateFailure = 3
	// exitInfraFailure means at least one Attempt's own Outcome was
	// infra_failed: fixture, spawn, storage, runner, host, or required
	// collection infrastructure failed (design §13).
	exitInfraFailure = 4
	// exitIndeterminate means at least one Attempt's own Outcome was
	// indeterminate and no more severe class above already applies.
	exitIndeterminate = 5
)
