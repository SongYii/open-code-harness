package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// maxCompactorStdoutBytes bounds how much of the compactor's own stdout
// (one small JSON object, design CE-14's own compactSessionOutput shape)
// this package ever retains — generous for that shape, never unbounded.
const maxCompactorStdoutBytes = 64 * 1024

// leaseHeldStderrMarker is the exact, verified substring
// internal/harness/runtime's own ErrLeaseHeld.Error() produces
// ("runtime host: database lease is held by live runtime %q"), read
// directly from host.go rather than assumed, and printed to stderr by
// cmd/och/main.go's own `fmt.Fprintln(os.Stderr, "och:", err)` on any
// fatal error. This is the one signal available to a process that only
// ever observes another och invocation's exit code and captured stderr —
// eval never imports internal/harness/adapters/sqlite or
// internal/harness/runtime itself (design §5's own one-way boundary) — so
// pattern-matching this specific, own-verified text is how a known lease
// conflict is told apart from any other relaunch failure.
const leaseHeldStderrMarker = "database lease is held by live runtime"

// acpCompactorResult mirrors cmd/och/main.go's own compactSessionOutput
// JSON shape (an independent copy, not an import: cmd/och is package
// main, and eval owns its own decoding exactly like acp_wire.go owns its
// own copy of the ACP wire shapes). Only the fields this task's own
// success/failure classification needs are decoded; the rest of the
// object is accepted and ignored by encoding/json's own default
// tolerance of unrecognized fields it is not asked to reject here (unlike
// this package's own frozen documents, a compactor's transient stdout is
// not a canonical wire document design §6 governs).
type acpCompactorResult struct {
	Ran bool `json:"ran"`
}

// runACPActionCompact implements design §16's compact action for the ACP
// subprocess executor as Task 14's own lease-safe transaction: the
// current writer must release the single-writer runtime lease (a clean,
// proven reap) before `och compact-session` can acquire it, and that
// compactor must itself have exited (proven, released) before a
// successor writer relaunches and resumes the same Session via
// session/load. Every one of the three processes this action may launch
// gets its own distinct, monotonically-ordered runtime ID
// (state.launchOrdinal), recorded only as an Attempt/evidence fact
// (design §16) -- never reused, and this function never signals a PID it
// read back from lease or database state, only process handles it holds
// itself (state.process, the compactor's own *acpProcess, and the
// successor's).
func runACPActionCompact(ctx context.Context, state *acpExecutionState, action ScenarioAction, attemptID AttemptID, started time.Time, launch ACPLaunchConfig, subject Subject, directories AttemptRootDirectories) (Outcome, bool) {
	// Phase 1: the current writer must cleanly release the lease before
	// the compactor may even attempt to acquire it.
	_ = state.conn.close()
	_ = state.process.stdin.Close()
	stopped, waitErr := state.process.waitTimeout(launch.ShutdownTimeout)
	if !stopped {
		return Outcome{
			FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
			Status: OutcomeIndeterminate, Code: "acp_shutdown_unproven",
			Message:   "the current writer's reap could not be proven before compaction; the compactor was never launched",
			StartedAt: started, EndedAt: time.Now().UTC(), CollectionStatus: CollectionNotStarted,
		}, true
	}
	if waitErr != nil {
		return infraFailedOutcome(attemptID, started, "acp_shutdown_failed", waitErr), true
	}

	// Phase 2: launch `och compact-session` under its own distinct
	// runtime ID, wait for it to exit, and only ever treat a proven,
	// clean (exit 0) reap as the lease having actually been released.
	state.launchOrdinal++
	compactorRuntimeID := launchRuntimeID(attemptID, state.launchOrdinal)
	argv, err := NormalizedArgv(subject)
	if err != nil {
		return infraFailedOutcome(attemptID, started, "acp_compact_argv_failed", err), true
	}
	compactorArgv := append([]string{
		"compact-session",
		"-workspace", directories.Workspace,
		"-database", AttemptDatabasePath(directories),
		"-runtime-id", compactorRuntimeID,
		"-audit-dir", directories.Audit,
		"-session", state.sessionID,
		"-strategy", action.Compact.Strategy,
	}, argv...)
	if action.Compact.Focus != "" {
		compactorArgv = append(compactorArgv, "-focus", action.Compact.Focus)
	}
	env, err := BuildChildEnvironment(subject)
	if err != nil {
		return infraFailedOutcome(attemptID, started, "acp_compact_env_failed", err), true
	}

	compactor, err := runACPCompactor(launch.Binary.Path, compactorArgv, env, directories.Workspace, launch.StderrLimit, launch.ShutdownTimeout)
	if err != nil {
		return infraFailedOutcome(attemptID, started, "acp_compactor_failed", err), true
	}
	var result acpCompactorResult
	if err := json.Unmarshal(bytes.TrimSpace(compactor.stdout), &result); err != nil {
		return infraFailedOutcome(attemptID, started, "acp_compactor_output_undecodable",
			fmt.Errorf("decode compactor stdout: %w (stderr=%s)", err, compactor.stderr)), true
	}

	// Phase 3: a successor writer may relaunch only once the compactor's
	// own reap is proven; a lease conflict here despite that proof is a
	// distinct, unexpected fact (design's own "known clean reap followed
	// by ErrLeaseHeld" case) from any other relaunch failure.
	state.launchOrdinal++
	writerRuntimeID := launchRuntimeID(attemptID, state.launchOrdinal)
	writerArgv := append([]string{
		"-acp",
		"-workspace", directories.Workspace,
		"-database", AttemptDatabasePath(directories),
		"-runtime-id", writerRuntimeID,
		"-audit-dir", directories.Audit,
	}, argv...)
	process, err := startACPProcess(launch.Binary.Path, writerArgv, env, directories.Workspace, launch.StderrLimit)
	if err != nil {
		return infraFailedOutcome(attemptID, started, "acp_compact_relaunch_failed", err), true
	}
	conn := newACPConnection(process.stdout, process.stdin, state.permissionHandler)
	initCtx, cancel := context.WithTimeout(ctx, launch.StartupTimeout)
	_, initErr := conn.initialize(initCtx)
	cancel()
	if initErr != nil {
		stderrSnapshot := process.stderr.Bytes()
		closeAndReapACP(conn, process, launch.ShutdownTimeout)
		if bytes.Contains(stderrSnapshot, []byte(leaseHeldStderrMarker)) {
			return infraFailedOutcome(attemptID, started, "runtime_lease_not_released",
				fmt.Errorf("successor writer refused: %s (stderr=%s)", initErr, stderrSnapshot)), true
		}
		return Outcome{
			FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
			Status: OutcomeIndeterminate, Code: "acp_compact_relaunch_unproven",
			Message: boundedRedactedMessage(fmt.Sprintf(
				"successor writer failed to initialize after a proven-clean compactor reap, for a reason other than a known lease conflict: %s", initErr)),
			StartedAt: started, EndedAt: time.Now().UTC(), CollectionStatus: CollectionNotStarted,
		}, true
	}

	loadCtx, cancel := context.WithTimeout(ctx, launch.StartupTimeout)
	loadErr := conn.loadSession(loadCtx, state.sessionID, directories.Workspace)
	cancel()
	if loadErr != nil {
		closeAndReapACP(conn, process, launch.ShutdownTimeout)
		return infraFailedOutcome(attemptID, started, "acp_compact_load_session_failed", loadErr), true
	}

	state.process = process
	state.conn = conn
	return Outcome{}, false
}

// acpCompactorOutcome is one compact-session invocation's own observed
// facts: its captured stdout/stderr and whether it was proven to exit
// zero.
type acpCompactorOutcome struct {
	stdout []byte
	stderr []byte
}

// runACPCompactor spawns `och compact-session`, drains its stdout
// (bounded) concurrently with waiting for it to exit (compact-session
// never reads stdin, so it is closed immediately), and treats anything
// other than a proven, clean exit as a hard failure — including forcibly
// reaping (SIGKILL to the owned process group) a compactor that overran
// timeout, so this function never leaves a process behind regardless of
// outcome.
func runACPCompactor(binaryPath string, argv, env []string, workingDir string, stderrLimit int64, timeout time.Duration) (acpCompactorOutcome, error) {
	process, err := startACPProcess(binaryPath, argv, env, workingDir, stderrLimit)
	if err != nil {
		return acpCompactorOutcome{}, fmt.Errorf("start compactor: %w", err)
	}
	_ = process.stdin.Close()

	stdoutCh := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(process.stdout, maxCompactorStdoutBytes))
		stdoutCh <- data
	}()

	exited, waitErr := process.waitTimeout(timeout)
	if !exited {
		_ = process.killProcessGroup(acpSignalKill)
		_, _ = process.waitTimeout(DefaultShutdownGrace)
	}

	var stdout []byte
	select {
	case stdout = <-stdoutCh:
	case <-time.After(DefaultShutdownGrace):
		// The process is already proven exited or forcibly killed above,
		// so its stdout pipe's write end is closed either way; this is a
		// defensive bound only, never expected to fire.
	}
	stderr := process.stderr.Bytes()

	if !exited {
		return acpCompactorOutcome{stdout: stdout, stderr: stderr}, fmt.Errorf("compactor did not exit within %s (stderr=%s)", timeout, stderr)
	}
	if waitErr != nil {
		return acpCompactorOutcome{stdout: stdout, stderr: stderr}, fmt.Errorf("compactor exited with error: %w (stderr=%s)", waitErr, stderr)
	}
	return acpCompactorOutcome{stdout: stdout, stderr: stderr}, nil
}
