package eval

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// errACPSubprocessUnsupportedOnWindows is returned (never on Unix, where
// acpProcessSupported is true) by RunACPAttempt and by
// acp_process_windows.go's own startACPProcess stub — the one error value
// both paths share, defined here rather than per-platform file so it
// compiles and resolves identically regardless of GOOS.
var errACPSubprocessUnsupportedOnWindows = errors.New("eval: acp_subprocess is not supported on this platform (Windows)")

// defaultACPStderrBytes bounds one launch's captured stderr (design §19's
// own stderr evidence bound; this package's own default until a caller
// supplies EvalSetLimits.StderrBytes through ACPLaunchConfig).
const defaultACPStderrBytes = 8 << 20

// ACPLaunchConfig is everything RunACPAttempt needs beyond the Scenario/
// Subject/Attempt facts RunAttempt itself takes (design §16): one
// resolved, hashed och binary shared across every Attempt in a run
// (ResolveACPBinary), the bounded startup/shutdown/stderr limits this
// launch enforces, and the escalation ladder's own grace periods.
type ACPLaunchConfig struct {
	Binary          ACPBinaryIdentity
	StderrLimit     int64
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
	ShutdownGrades  ACPShutdownGrades
}

func (config ACPLaunchConfig) withDefaults() ACPLaunchConfig {
	if config.StderrLimit <= 0 {
		config.StderrLimit = defaultACPStderrBytes
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = DefaultProcessStartup
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = DefaultShutdownGrace
	}
	config.ShutdownGrades = config.ShutdownGrades.withDefaults()
	return config
}

// RunACPAttempt drives one Attempt's Scenario actions against a real,
// independently spawned och -acp subprocess (design §16), mirroring
// RunAttempt's own contract and ExecutionOutcome shape as closely as an
// entirely different execution surface allows: evidence collection
// (CollectEvidence) reads the same canonical SQLite database format
// either executor produces and needs no changes to drive an ACP Attempt.
//
// matcher is this Attempt's compiled ApprovalScript (design §7), wired
// into every launched connection's session/request_permission handler
// via NewACPPermissionHandler and reset per prompt action exactly like
// the in-process executor. compact has no ACP wire method yet (Task 14's
// own scope); reaching one here is recorded as infra_failed/
// unsupported_action, exactly like the in-process executor's own default
// branch for a genuinely unimplemented action.
//
// On Windows, RunACPAttempt refuses immediately (acpProcessSupported is
// false there) — no process is ever spawned.
func RunACPAttempt(ctx context.Context, attemptID AttemptID, subject Subject, directories AttemptRootDirectories, scenario Scenario, launch ACPLaunchConfig, matcher *ApprovalMatcher) (ExecutionOutcome, error) {
	if !acpProcessSupported {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: %w", errACPSubprocessUnsupportedOnWindows)
	}
	if ctx == nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: context is required")
	}
	if err := scenario.Validate(); err != nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: %w", err)
	}
	launch = launch.withDefaults()
	started := time.Now().UTC()

	state := &acpExecutionState{pending: make(map[ActionID]*acpPendingPrompt), permissionHandler: NewACPPermissionHandler(matcher)}

	argv, err := NormalizedArgv(subject)
	if err != nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: %w", err)
	}
	fullArgv := append([]string{
		"-acp",
		"-workspace", directories.Workspace,
		"-database", AttemptDatabasePath(directories),
		"-runtime-id", launchRuntimeID(attemptID, state.launchOrdinal),
		"-audit-dir", directories.Audit,
	}, argv...)

	env, err := BuildChildEnvironment(subject)
	if err != nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: %w", err)
	}

	process, err := startACPProcess(launch.Binary.Path, fullArgv, env, directories.Workspace, launch.StderrLimit)
	if err != nil {
		return ExecutionOutcome{WriterStopped: true, Outcome: infraFailedOutcome(attemptID, started, "acp_spawn_failed", err)}, err
	}
	state.process = process
	state.conn = newACPConnection(process.stdout, process.stdin, state.permissionHandler)

	initCtx, cancel := context.WithTimeout(ctx, launch.StartupTimeout)
	_, err = state.conn.initialize(initCtx)
	cancel()
	if err != nil {
		closeAndReapACP(state.conn, state.process, launch.ShutdownTimeout)
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: initialize: %w", err)
	}

	sessionCtx, cancel := context.WithTimeout(ctx, launch.StartupTimeout)
	sessionID, err := state.conn.newSession(sessionCtx, directories.Workspace)
	cancel()
	if err != nil {
		closeAndReapACP(state.conn, state.process, launch.ShutdownTimeout)
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: new session: %w", err)
	}
	state.sessionID = sessionID

	hasCanceller := make(map[ActionID]bool)
	for _, action := range scenario.Actions {
		if action.Type == ActionCancel {
			hasCanceller[action.Cancel.TargetActionID] = true
		}
	}

	var outcome Outcome
	terminal := false
	for _, action := range scenario.Actions {
		outcome, terminal = runACPAction(ctx, state, action, hasCanceller, matcher, attemptID, started, launch, subject, directories)
		if terminal {
			break
		}
	}
	drainACPPending(state, launch.ShutdownGrades)

	if terminal {
		stopped := closeAndReapACP(state.conn, state.process, launch.ShutdownTimeout)
		return ExecutionOutcome{SessionID: state.sessionID, WriterStopped: stopped, Outcome: outcome}, nil
	}

	completed := Outcome{
		FormatVersion:    FormatVersion,
		Schema:           SchemaOutcome,
		AttemptID:        attemptID,
		Status:           OutcomeCompleted,
		Code:             "ok",
		Message:          "every scenario action completed",
		StartedAt:        started,
		EndedAt:          time.Now().UTC(),
		CollectionStatus: CollectionNotStarted,
		TerminalSession: &TerminalSessionFacts{
			SessionID: state.sessionID,
			TurnCount: state.turnCount,
			Open:      true,
		},
	}
	stopped := closeAndReapACP(state.conn, state.process, launch.ShutdownTimeout)
	return ExecutionOutcome{SessionID: state.sessionID, WriterStopped: stopped, Outcome: completed}, nil
}

// runACPAction drives one action and reports whether it terminated the
// Attempt, mirroring inprocess.go's runAction shape for the ACP surface.
// A prompt action with a later canceler launches asynchronously and never
// terminates on its own; its resolution happens when the loop reaches the
// matching cancel action.
func runACPAction(ctx context.Context, state *acpExecutionState, action ScenarioAction, hasCanceller map[ActionID]bool, matcher *ApprovalMatcher, attemptID AttemptID, started time.Time, launch ACPLaunchConfig, subject Subject, directories AttemptRootDirectories) (Outcome, bool) {
	switch action.Type {
	case ActionPrompt:
		matcher.BeginPrompt(action.ID)
		if hasCanceller[action.ID] {
			pending, err := state.conn.promptAsync(ctx, state.sessionID, action.Prompt.Text)
			if err != nil {
				return infraFailedOutcome(attemptID, started, "acp_prompt_failed", err), true
			}
			state.pending[action.ID] = pending
			return Outcome{}, false
		}
		return runACPSyncPrompt(ctx, state, action, attemptID, started)
	case ActionCancel:
		return runACPCancel(state, action, attemptID, started, launch.ShutdownGrades)
	case ActionRestart:
		return runACPActionRestart(ctx, state, action, attemptID, started, launch, subject, directories)
	case ActionCompact:
		return runACPActionCompact(ctx, state, action, attemptID, started, launch, subject, directories)
	case ActionCollect:
		// Declared workspace path or verifier fact is validated and
		// captured by evidence collection after shutdown (design §14),
		// identically to the in-process executor.
		return Outcome{}, false
	default:
		return Outcome{
			FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
			Status: OutcomeInfraFailed, Code: "unsupported_action",
			Message: boundedRedactedMessage(fmt.Sprintf(
				"the acp_subprocess executor does not yet drive %q actions", action.Type)),
			StartedAt: started, EndedAt: time.Now().UTC(), CollectionStatus: CollectionNotStarted,
		}, true
	}
}

func runACPSyncPrompt(ctx context.Context, state *acpExecutionState, action ScenarioAction, attemptID AttemptID, started time.Time) (Outcome, bool) {
	stopReason, err := state.conn.prompt(ctx, state.sessionID, action.Prompt.Text)
	if err != nil {
		// This connection has no public way yet to distinguish a
		// well-formed session/prompt error response (a genuine Turn
		// failure) from a connection/process-level failure. Any error
		// here is classified conservatively as infra_failed rather than
		// guessing which it was.
		return infraFailedOutcome(attemptID, started, "acp_prompt_failed", err), true
	}
	if stopReason != acpStopReasonEndTurn {
		return Outcome{
			FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
			Status: OutcomeSubjectFailed, Code: "acp_turn_not_completed",
			Message:   boundedRedactedMessage(fmt.Sprintf("turn ended with stop reason %q", stopReason)),
			StartedAt: started, EndedAt: time.Now().UTC(), CollectionStatus: CollectionNotStarted,
			TerminalSession: &TerminalSessionFacts{SessionID: state.sessionID, Open: true},
		}, true
	}
	state.turnCount++
	return Outcome{}, false
}

// runACPCancel resolves a cancel action against its already-async-launched
// target prompt via escalateCancel. A cancellation resolved by
// session/cancel alone is the Scenario's own declared behavior — like the
// in-process executor, it never terminates the Attempt on its own. Any
// stage that had to tear down the writer to resolve it does terminate the
// Attempt: there is no live writer left for any action after this one to
// run against.
func runACPCancel(state *acpExecutionState, action ScenarioAction, attemptID AttemptID, started time.Time, grades ACPShutdownGrades) (Outcome, bool) {
	target := action.Cancel.TargetActionID
	pending, ok := state.pending[target]
	if !ok {
		return infraFailedOutcome(attemptID, started, "cancel_target_not_pending",
			fmt.Errorf("no in-flight prompt %q to cancel", target)), true
	}
	result := escalateCancel(state.process, state.conn, state.sessionID, pending, grades)
	delete(state.pending, target)
	state.turnCount++

	if result.processAlive {
		return Outcome{}, false
	}
	if result.stage == acpCancelStageUnreaped {
		return Outcome{
			FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
			Status: OutcomeIndeterminate, Code: "acp_cancel_reap_unproven",
			Message:   "the writer's reap could not be proven after the full cancellation escalation ladder",
			StartedAt: started, EndedAt: time.Now().UTC(), CollectionStatus: CollectionNotStarted,
		}, true
	}
	return Outcome{
		FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
		Status: OutcomeIndeterminate, Code: "acp_cancel_escalated",
		Message: boundedRedactedMessage(fmt.Sprintf(
			"cancellation required escalating to %q; the writer was torn down and no further action in this Scenario could run", result.stage)),
		StartedAt: started, EndedAt: time.Now().UTC(), CollectionStatus: CollectionNotStarted,
		TerminalSession: &TerminalSessionFacts{SessionID: state.sessionID, Open: false},
	}, true
}

func runACPActionRestart(ctx context.Context, state *acpExecutionState, action ScenarioAction, attemptID AttemptID, started time.Time, launch ACPLaunchConfig, subject Subject, directories AttemptRootDirectories) (Outcome, bool) {
	// A restart while some other prompt is still pending asynchronously
	// (a later cancel action never reached) is not a shape a validated
	// Scenario produces — Scenario.Validate requires a cancel to name an
	// earlier prompt, and restart carries no such reference — so this
	// task does not special-case it beyond drainACPPending's own safety
	// net on every exit path.
	if err := runACPRestart(ctx, state, action.Restart.Mode, launch, launch.ShutdownGrades, subject, directories, attemptID); err != nil {
		return infraFailedOutcome(attemptID, started, "acp_restart_failed", err), true
	}
	return Outcome{}, false
}

const acpStopReasonEndTurn = "end_turn"

// closeAndReapACP implements design §16's normal shutdown: close stdin
// (the agent observes EOF and shuts down on its own, exactly like
// och -acp's own ServeACP loop), stop the connection's own read loop, and
// wait up to timeout for the process to actually exit and be reaped.
// stopped is true only once wait has actually returned — an Attempt's
// WriterStopped fact (and thus whether evidence collection may safely
// begin) is never set on anything weaker than that proof.
func closeAndReapACP(conn *acpConnection, process *acpProcess, timeout time.Duration) (stopped bool) {
	_ = process.stdin.Close()
	_ = conn.close()
	exited, _ := process.waitTimeout(timeout)
	return exited
}
