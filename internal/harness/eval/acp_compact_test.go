//go:build unix

package eval

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRunACPActionCompactRunsFullLeaseTransactionAgainstRealOchBinary(t *testing.T) {
	ochBin := buildOchBinary(t)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID, directories := acpTestDirectories(t)

	scenario := runnerScenario("acp-compact-transaction")
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "before compaction"),
		{ID: "compact-1", Type: ActionCompact, Compact: &CompactAction{Strategy: "reset"}},
		newEchoScenarioAction("prompt-2", "after compaction"),
	}
	scenario.ApprovalScript = nil
	scenario.RequiredCapabilities = []string{"prompt", "compact"}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunACPAttempt(ctx, attemptID, subject, directories, scenario, ACPLaunchConfig{Binary: binary}, matcher)
	if err != nil {
		t.Fatalf("RunACPAttempt() error = %v", err)
	}
	if execution.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q (message: %s)", execution.Outcome.Status, OutcomeCompleted, execution.Outcome.Message)
	}
	if !execution.WriterStopped {
		t.Fatal("WriterStopped = false, want true")
	}
	if execution.SessionID == "" {
		t.Fatal("SessionID is empty")
	}
	if server.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2: one before compaction, one after (compact-session itself never calls the provider)", server.calls.Load())
	}
}

// TestRunACPActionCompactReportsUnprovenShutdownForAnUnresponsiveWriter
// drives runACPActionCompact directly against an acpchild "writer" that
// never exits on stdin close (Phase 1 of the lease transaction), proving
// the compactor is never even launched when the current writer's own
// reap cannot be proven.
func TestRunACPActionCompactReportsUnprovenShutdownForAnUnresponsiveWriter(t *testing.T) {
	process, conn, sessionID := startEscalationChild(t, "ignore-signals")
	// Cleanup runs via defer (not sequentially after the assertions below)
	// so a failing assertion still force-kills this child rather than
	// leaking it -- runACPActionCompact never touches state.process on
	// this path, by design (an unproven writer is left exactly as found
	// for later inspection, not silently torn down further), so this
	// test itself owns reaping it either way.
	defer func() {
		_ = process.killProcessGroup(acpSignalKill)
		_, _ = process.waitTimeout(5 * time.Second)
		if processExists(process.pid()) {
			t.Error("process leaked after cleanup")
		}
	}()
	state := &acpExecutionState{
		process: process, conn: conn, sessionID: sessionID,
		permissionHandler: discardPermissionHandler,
		pending:           make(map[ActionID]*acpPendingPrompt),
	}
	attemptID := testAttemptID(t)
	launch := ACPLaunchConfig{ShutdownTimeout: 300 * time.Millisecond}.withDefaults()

	outcome, terminal := runACPActionCompact(context.Background(), state,
		ScenarioAction{ID: "compact-1", Type: ActionCompact, Compact: &CompactAction{Strategy: "reset"}},
		attemptID, time.Now(), launch, Subject{}, AttemptRootDirectories{})
	if !terminal {
		t.Fatal("terminal = false, want true: an unproven writer reap must end the Attempt")
	}
	if outcome.Status != OutcomeIndeterminate || outcome.Code != "acp_shutdown_unproven" {
		t.Fatalf("outcome = %+v, want indeterminate/acp_shutdown_unproven", outcome)
	}
}

// TestRunACPActionCompactFailsWhenAnotherLiveWriterHoldsTheLease proves
// design's "no compaction beside live writer" requirement: a real,
// separately-started och -acp process holding directories' own database
// lease under a runtime ID this compact action never uses makes the
// compactor's own composition.Open call fail, and runACPActionCompact
// reports that as infra_failed rather than silently proceeding. The
// "current writer" this compact action itself closes is acpchild (which
// never touches the database at all), isolating this test to exactly
// Phase 2's own lease-conflict classification rather than racing a real
// writer's own shutdown against the external holder's startup.
func TestRunACPActionCompactFailsWhenAnotherLiveWriterHoldsTheLease(t *testing.T) {
	ochBin := buildOchBinary(t)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID, directories := acpTestDirectories(t)

	argv, err := NormalizedArgv(subject)
	if err != nil {
		t.Fatalf("NormalizedArgv: %v", err)
	}
	env, err := BuildChildEnvironment(subject)
	if err != nil {
		t.Fatalf("BuildChildEnvironment: %v", err)
	}
	holderArgv := append([]string{
		"-acp",
		"-workspace", directories.Workspace,
		"-database", AttemptDatabasePath(directories),
		"-runtime-id", "external-lease-holder",
		"-audit-dir", directories.Audit,
	}, argv...)
	holder, err := startACPProcess(binary.Path, holderArgv, env, directories.Workspace, 1<<20)
	if err != nil {
		t.Fatalf("startACPProcess(holder): %v", err)
	}
	defer func() {
		_ = holder.stdin.Close()
		_, _ = holder.waitTimeout(5 * time.Second)
	}()
	holderConn := newACPConnection(holder.stdout, holder.stdin, discardPermissionHandler)
	defer func() { _ = holderConn.close() }()
	initCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := holderConn.initialize(initCtx); err != nil {
		cancel()
		t.Fatalf("holder initialize: %v", err)
	}
	cancel()
	// composition.Open (and so lease acquisition) happens before ServeACP
	// ever answers initialize, so a successful initialize response here
	// already proves the holder has acquired and is continuously holding
	// the database's own lease.

	process, conn, sessionID := startEscalationChild(t, "normal")
	state := &acpExecutionState{
		process: process, conn: conn, sessionID: sessionID,
		permissionHandler: discardPermissionHandler,
		pending:           make(map[ActionID]*acpPendingPrompt),
	}
	launch := ACPLaunchConfig{Binary: binary}.withDefaults()

	outcome, terminal := runACPActionCompact(context.Background(), state,
		ScenarioAction{ID: "compact-1", Type: ActionCompact, Compact: &CompactAction{Strategy: "reset"}},
		attemptID, time.Now(), launch, subject, directories)
	if !terminal {
		t.Fatal("terminal = false, want true: the compactor must not silently proceed beside a live writer")
	}
	if outcome.Status != OutcomeInfraFailed || outcome.Code != "acp_compactor_failed" {
		t.Fatalf("outcome = %+v, want infra_failed/acp_compactor_failed", outcome)
	}
}

func TestRunACPCompactorSurfacesNonZeroExit(t *testing.T) {
	child := buildACPChild(t)
	_, err := runACPCompactor(child, []string{"-mode", "exit-nonzero"}, os.Environ(), t.TempDir(), 1<<20, 5*time.Second)
	if err == nil {
		t.Fatal("runACPCompactor() error = nil, want a non-zero-exit error")
	}
}

func TestRunACPCompactorKillsAndReportsATimedOutCompactor(t *testing.T) {
	child := buildACPChild(t)
	const timeout = 300 * time.Millisecond
	outcome, err := runACPCompactor(child, []string{"-mode", "hang"}, os.Environ(), t.TempDir(), 1<<20, timeout)
	if err == nil {
		t.Fatal("runACPCompactor() error = nil, want a timeout error for a compactor that never exits")
	}
	_ = outcome
}

func TestRunACPCompactorCapturesCleanExitWithNoError(t *testing.T) {
	child := buildACPChild(t)
	outcome, err := runACPCompactor(child, nil, os.Environ(), t.TempDir(), 1<<20, 5*time.Second)
	if err != nil {
		t.Fatalf("runACPCompactor() error = %v, want nil for a clean exit", err)
	}
	if len(outcome.stdout) != 0 {
		t.Fatalf("stdout = %q, want empty: this child never received any request to answer", outcome.stdout)
	}
}
