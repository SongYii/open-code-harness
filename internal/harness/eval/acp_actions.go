package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ACPShutdownGrades bounds each stage of design §16's exact escalation
// ladder: session/cancel -> wait cancel grace -> close stdin -> wait
// shutdown grace -> SIGTERM owned process group -> wait final grace ->
// SIGKILL owned process group -> reap. Every grace period is independent;
// a stage that resolves before its own grace elapses moves on
// immediately rather than waiting out the full period.
type ACPShutdownGrades struct {
	CancelGrace   time.Duration
	ShutdownGrace time.Duration
	FinalGrace    time.Duration

	// RelaunchGrace bounds runACPRestart's own successor-join retry after
	// an abrupt (interrupt/kill) restart. A relaunched writer identifies
	// itself with a new runtime ID (launchRuntimeID never repeats one
	// across a restart), and internal/harness/adapters/sqlite's own
	// single-writer fencing lease (lease.go's AcquireLease) refuses a new
	// runtime ID's own acquisition until the prior holder's lease
	// naturally expires -- which only happens immediately for a graceful
	// clean_shutdown exit (the prior writer releases its own lease on the
	// way out); an interrupt/kill exit never gets the chance to release
	// it, so the successor must simply wait out the remainder of that
	// lease's own duration (this repository's own default, verified by
	// reading internal/harness/adapters/sqlite/config.go, is 30s). This is
	// a real, verified property of the runtime's own lease design, not a
	// workaround for a bug in it.
	RelaunchGrace time.Duration
}

func (grades ACPShutdownGrades) withDefaults() ACPShutdownGrades {
	if grades.CancelGrace <= 0 {
		grades.CancelGrace = DefaultCancellationGrace
	}
	if grades.ShutdownGrace <= 0 {
		grades.ShutdownGrace = DefaultShutdownGrace
	}
	if grades.FinalGrace <= 0 {
		grades.FinalGrace = DefaultShutdownGrace
	}
	if grades.RelaunchGrace <= 0 {
		grades.RelaunchGrace = defaultACPRelaunchGrace
	}
	return grades
}

// defaultACPRelaunchGrace comfortably exceeds
// internal/harness/adapters/sqlite's own default 30s writer lease duration
// (config.go's defaultLeaseDuration), with margin for this package's own
// relaunch retry polling overhead.
const defaultACPRelaunchGrace = 40 * time.Second

// acpRelaunchPollInterval is how often runACPRestart retries spawning a
// successor while its lease acquisition is refused -- frequent enough that
// a successful join is observed promptly once the prior lease actually
// expires, without hammering the filesystem with spawn attempts.
const acpRelaunchPollInterval = 500 * time.Millisecond

// acpExecutionState is the mutable state RunACPAttempt threads through its
// action loop, mirroring inprocess.go's own executionState shape for the
// ACP surface: the currently live process/connection (reassigned by a
// restart), a monotonic launch ordinal for deriving each relaunch's
// distinct runtime ID, and any prompt actions currently running
// asynchronously because a later action in the same Scenario cancels
// them.
type acpExecutionState struct {
	process       *acpProcess
	conn          *acpConnection
	sessionID     string
	launchOrdinal int
	turnCount     int
	pending       map[ActionID]*acpPendingPrompt

	// permissionHandler is reused unchanged across a restart's relaunch —
	// the same ApprovalMatcher-bound handler a fresh Connection wires in.
	permissionHandler func(context.Context, json.RawMessage) (json.RawMessage, error)
}

// acpCancelStage names which rung of the escalation ladder actually
// stopped the in-flight prompt call — recorded for evidence and for
// deciding whether the Attempt can continue (design §16).
type acpCancelStage string

const (
	acpCancelStageSessionCancel acpCancelStage = "session_cancel"
	acpCancelStageStdinClose    acpCancelStage = "stdin_close"
	acpCancelStageSigterm       acpCancelStage = "sigterm"
	acpCancelStageSigkill       acpCancelStage = "sigkill"
	acpCancelStageUnreaped      acpCancelStage = "unreaped"
)

type acpCancelResult struct {
	stage        acpCancelStage
	processAlive bool // true only when stage == acpCancelStageSessionCancel: the writer never needed to be torn down
	promptErr    error
}

// escalateCancel implements design §16's exact escalation for one
// Scenario cancel action targeting a still-pending prompt: session/cancel
// first (the writer keeps running if this alone resolves the prompt),
// escalating through a graceful stdin close, SIGTERM, and finally SIGKILL
// of the owned process group only if the prompt does not resolve within
// each stage's own grace period. Every stage beyond session/cancel tears
// down the writer entirely — a Scenario that needed to escalate that far
// cannot continue past this action, matching design's own "loss of
// ownership/reap proof is indeterminate, not success" principle applied
// to the same ladder.
func escalateCancel(process *acpProcess, conn *acpConnection, sessionID string, pending *acpPendingPrompt, grades ACPShutdownGrades) acpCancelResult {
	_ = conn.cancel(sessionID)
	select {
	case outcome := <-pending.done:
		return acpCancelResult{stage: acpCancelStageSessionCancel, processAlive: true, promptErr: outcome.err}
	case <-time.After(grades.CancelGrace):
	}

	_ = process.stdin.Close()
	select {
	case outcome := <-pending.done:
		_, _ = process.waitTimeout(grades.ShutdownGrace)
		_ = conn.close()
		return acpCancelResult{stage: acpCancelStageStdinClose, promptErr: outcome.err}
	case <-time.After(grades.ShutdownGrace):
	}

	_ = process.killProcessGroup(acpSignalTerm)
	select {
	case outcome := <-pending.done:
		_, _ = process.waitTimeout(grades.FinalGrace)
		_ = conn.close()
		return acpCancelResult{stage: acpCancelStageSigterm, promptErr: outcome.err}
	case <-time.After(grades.FinalGrace):
	}

	_ = process.killProcessGroup(acpSignalKill)
	select {
	case outcome := <-pending.done:
		_, _ = process.waitTimeout(grades.FinalGrace)
		_ = conn.close()
		return acpCancelResult{stage: acpCancelStageSigkill, promptErr: outcome.err}
	case <-time.After(grades.FinalGrace):
		_ = conn.close()
		return acpCancelResult{stage: acpCancelStageUnreaped}
	}
}

// drainACPPending cancels and waits for every prompt action still running
// asynchronously, regardless of how RunACPAttempt is about to return —
// inprocess.go's drainPending applied to the ACP surface. A validated
// Scenario's every canceler-targeted prompt is expected to already have
// been resolved by its matching cancel action in the main loop; this is
// the safety net for an early-terminating path that never reached that
// cancel action.
func drainACPPending(state *acpExecutionState, grades ACPShutdownGrades) {
	for id, pending := range state.pending {
		escalateCancel(state.process, state.conn, state.sessionID, pending, grades)
		delete(state.pending, id)
	}
}

// runACPRestart implements design §16's restart action for the ACP
// executor: clean_shutdown closes stdin and waits normally (mirroring
// Task 12's own end-of-Attempt shutdown); interrupt sends SIGINT and kill
// sends SIGKILL to the current owned process group, each followed by a
// bounded wait. In every mode, a successor is launched and loads the same
// session only once the prior writer's reap is proven — loss of that
// proof is the caller's own indeterminate fact, never silently treated as
// success.
func runACPRestart(ctx context.Context, state *acpExecutionState, mode RestartMode, launch ACPLaunchConfig, grades ACPShutdownGrades, subject Subject, directories AttemptRootDirectories, attemptID AttemptID) error {
	_ = state.conn.close()
	switch mode {
	case RestartModeCleanShutdown:
		_ = state.process.stdin.Close()
	case RestartModeInterrupt:
		_ = state.process.killProcessGroup(acpSignalInterrupt)
	case RestartModeKill:
		_ = state.process.killProcessGroup(acpSignalKill)
	default:
		return fmt.Errorf("eval: acp restart: unsupported mode %q", mode)
	}
	stopped, _ := state.process.waitTimeout(grades.FinalGrace)
	if !stopped {
		return fmt.Errorf("eval: acp restart: prior writer's reap was not proven within %s", grades.FinalGrace)
	}

	state.launchOrdinal++
	runtimeID := launchRuntimeID(attemptID, state.launchOrdinal)
	argv, err := NormalizedArgv(subject)
	if err != nil {
		return fmt.Errorf("eval: acp restart: %w", err)
	}
	fullArgv := append([]string{
		"-acp",
		"-workspace", directories.Workspace,
		"-database", AttemptDatabasePath(directories),
		"-runtime-id", runtimeID,
		"-audit-dir", directories.Audit,
	}, argv...)
	env, err := BuildChildEnvironment(subject)
	if err != nil {
		return fmt.Errorf("eval: acp restart: %w", err)
	}

	process, conn, err := relaunchACPSuccessor(ctx, launch, fullArgv, env, directories, state.permissionHandler, grades.RelaunchGrace)
	if err != nil {
		return fmt.Errorf("eval: acp restart: %w", err)
	}

	loadCtx, cancel := context.WithTimeout(ctx, launch.StartupTimeout)
	err = conn.loadSession(loadCtx, state.sessionID, directories.Workspace)
	cancel()
	if err != nil {
		closeAndReapACP(conn, process, launch.ShutdownTimeout)
		return fmt.Errorf("eval: acp restart: load session: %w", err)
	}

	state.process = process
	state.conn = conn
	return nil
}

// relaunchACPSuccessor spawns and initializes a fresh och -acp process,
// retrying the whole spawn+initialize sequence at acpRelaunchPollInterval
// until it succeeds or relaunchGrace elapses. A retry is necessary (not
// just tolerated) after an abrupt restart mode: the successor's own
// composition.Open call refuses to acquire the single-writer fencing lease
// under a new runtime ID until the prior holder's own lease naturally
// expires (ACPShutdownGrades.RelaunchGrace's own doc comment explains why
// in full) — so an initialize failure here is routinely just "the lease
// has not expired yet," not a genuine fault, and each failed attempt's
// process is fully reaped before the next spawn.
func relaunchACPSuccessor(ctx context.Context, launch ACPLaunchConfig, argv, env []string, directories AttemptRootDirectories, permissionHandler func(context.Context, json.RawMessage) (json.RawMessage, error), relaunchGrace time.Duration) (*acpProcess, *acpConnection, error) {
	deadline := time.Now().Add(relaunchGrace)
	var lastErr error
	for {
		process, err := startACPProcess(launch.Binary.Path, argv, env, directories.Workspace, launch.StderrLimit)
		if err != nil {
			lastErr = fmt.Errorf("relaunch: %w", err)
		} else {
			conn := newACPConnection(process.stdout, process.stdin, permissionHandler)
			initCtx, cancel := context.WithTimeout(ctx, launch.StartupTimeout)
			_, initErr := conn.initialize(initCtx)
			cancel()
			if initErr == nil {
				return process, conn, nil
			}
			lastErr = fmt.Errorf("initialize: %w", initErr)
			closeAndReapACP(conn, process, launch.ShutdownTimeout)
		}
		if !time.Now().Before(deadline) {
			return nil, nil, fmt.Errorf("successor did not join within %s: %w", relaunchGrace, lastErr)
		}
		select {
		case <-time.After(acpRelaunchPollInterval):
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("successor did not join before the Attempt's own context ended: %w", ctx.Err())
		}
	}
}
