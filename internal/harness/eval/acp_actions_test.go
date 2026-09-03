//go:build unix

package eval

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

// quickGrades keeps escalateCancel's own grace periods short enough for a
// fast test suite while still resolving deterministically against
// acpchild's controllable behavior. These grace periods ARE the mechanism
// under test, not incidental synchronization the test adds on top, so
// shortening them here (rather than reaching for a fixed sleep elsewhere)
// is the correct way to keep this suite fast without being flaky.
func quickGrades() ACPShutdownGrades {
	return ACPShutdownGrades{
		CancelGrace:   300 * time.Millisecond,
		ShutdownGrace: 300 * time.Millisecond,
		FinalGrace:    3 * time.Second,
	}
}

// startEscalationChild builds acpchild, launches it in mode, and drives
// initialize/session.new so the returned connection and session are ready
// for a test to start a prompt and cancel it.
func startEscalationChild(t *testing.T, mode string) (*acpProcess, *acpConnection, string) {
	t.Helper()
	child := buildACPChild(t)
	process, err := startACPProcess(child, []string{"-mode", mode}, os.Environ(), t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("startACPProcess(%s): %v", mode, err)
	}
	conn := newACPConnection(process.stdout, process.stdin, discardPermissionHandler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sessionID, err := conn.newSession(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	return process, conn, sessionID
}

// processExists reports whether pid still refers to a live (including
// zombie, i.e. not yet reaped) process, via the same signal-0 probe
// syscall.Kill supports for exactly this purpose. A leaked child from a
// prior test would still answer this even after this package's own
// process.isReaped() bookkeeping is confused, so it is a check independent
// of the production code under test.
func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TestEscalateCancelSessionCancelResolvesWithoutTearingDownProcess(t *testing.T) {
	process, conn, sessionID := startEscalationChild(t, "cancel-aware")
	defer func() {
		_ = process.stdin.Close()
		_, _ = process.waitTimeout(5 * time.Second)
	}()

	pending, err := conn.promptAsync(context.Background(), sessionID, "hello")
	if err != nil {
		t.Fatalf("promptAsync: %v", err)
	}
	result := escalateCancel(process, conn, sessionID, pending, quickGrades())

	if result.stage != acpCancelStageSessionCancel {
		t.Fatalf("stage = %q, want %q", result.stage, acpCancelStageSessionCancel)
	}
	if !result.processAlive {
		t.Fatal("processAlive = false, want true: session/cancel alone should resolve this rung")
	}
	if result.promptErr != nil {
		t.Fatalf("promptErr = %v, want nil: the child answered \"cancelled\" cleanly", result.promptErr)
	}
	if process.isReaped() {
		t.Fatal("isReaped() = true, want false: the writer must still be running")
	}
	if !processExists(process.pid()) {
		t.Fatal("process no longer exists despite processAlive = true")
	}
}

func TestEscalateCancelStdinCloseResolvesChildThatNeverAnswers(t *testing.T) {
	process, conn, sessionID := startEscalationChild(t, "prompt-hang")

	pending, err := conn.promptAsync(context.Background(), sessionID, "hello")
	if err != nil {
		t.Fatalf("promptAsync: %v", err)
	}
	result := escalateCancel(process, conn, sessionID, pending, quickGrades())

	if result.stage != acpCancelStageStdinClose {
		t.Fatalf("stage = %q, want %q", result.stage, acpCancelStageStdinClose)
	}
	if result.processAlive {
		t.Fatal("processAlive = true, want false: this rung tears the writer down")
	}
	if result.promptErr == nil {
		t.Fatal("promptErr = nil, want a connection-closed error: the child never actually answered")
	}
	if !process.isReaped() {
		t.Fatal("isReaped() = false, want true: escalateCancel must wait out the reap before returning")
	}
	if processExists(process.pid()) {
		t.Fatal("process leaked: still exists after escalateCancel reported it reaped")
	}
}

func TestEscalateCancelSigtermResolvesChildThatSurvivesStdinClose(t *testing.T) {
	process, conn, sessionID := startEscalationChild(t, "prompt-hang-survive-close")

	pending, err := conn.promptAsync(context.Background(), sessionID, "hello")
	if err != nil {
		t.Fatalf("promptAsync: %v", err)
	}
	result := escalateCancel(process, conn, sessionID, pending, quickGrades())

	if result.stage != acpCancelStageSigterm {
		t.Fatalf("stage = %q, want %q", result.stage, acpCancelStageSigterm)
	}
	if result.processAlive {
		t.Fatal("processAlive = true, want false")
	}
	if !process.isReaped() {
		t.Fatal("isReaped() = false, want true")
	}
	if processExists(process.pid()) {
		t.Fatal("process leaked: still exists after escalateCancel reached SIGTERM")
	}
}

func TestEscalateCancelSigkillResolvesChildThatIgnoresSigterm(t *testing.T) {
	process, conn, sessionID := startEscalationChild(t, "ignore-signals")

	pending, err := conn.promptAsync(context.Background(), sessionID, "hello")
	if err != nil {
		t.Fatalf("promptAsync: %v", err)
	}
	result := escalateCancel(process, conn, sessionID, pending, quickGrades())

	if result.stage != acpCancelStageSigkill {
		t.Fatalf("stage = %q, want %q: SIGTERM/SIGINT are ignored, only SIGKILL can end this child", result.stage, acpCancelStageSigkill)
	}
	if result.processAlive {
		t.Fatal("processAlive = true, want false")
	}
	if !process.isReaped() {
		t.Fatal("isReaped() = false, want true")
	}
	if processExists(process.pid()) {
		t.Fatal("process leaked: still exists after escalateCancel reached SIGKILL")
	}
}

// TestEscalateCancelAlreadyResolvedPromptReturnsImmediately proves the
// "already exited/answered" race: when the pending prompt's outcome is
// already sitting in its buffered channel before escalateCancel is ever
// called (e.g. the Turn finished on its own a moment before the cancel
// action ran), escalateCancel's own session/cancel rung must observe it
// immediately rather than waiting out the full CancelGrace period.
func TestEscalateCancelAlreadyResolvedPromptReturnsImmediately(t *testing.T) {
	process, conn, sessionID := startEscalationChild(t, "normal")
	defer func() {
		_ = process.stdin.Close()
		_, _ = process.waitTimeout(5 * time.Second)
	}()

	pending := &acpPendingPrompt{done: make(chan acpPromptOutcome, 1)}
	pending.done <- acpPromptOutcome{stopReason: acpStopReasonEndTurn}

	start := time.Now()
	result := escalateCancel(process, conn, sessionID, pending, quickGrades())
	elapsed := time.Since(start)

	if result.stage != acpCancelStageSessionCancel || !result.processAlive {
		t.Fatalf("result = %+v, want session_cancel/alive for an already-resolved prompt", result)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("escalateCancel took %s for an already-resolved prompt, want it to return immediately instead of waiting out CancelGrace (%s)", elapsed, quickGrades().CancelGrace)
	}
}

func TestDrainACPPendingResolvesEveryEntryAndClearsThePendingMap(t *testing.T) {
	process, conn, sessionID := startEscalationChild(t, "cancel-aware")
	defer func() {
		_ = process.stdin.Close()
		_, _ = process.waitTimeout(5 * time.Second)
	}()

	pending, err := conn.promptAsync(context.Background(), sessionID, "hello")
	if err != nil {
		t.Fatalf("promptAsync: %v", err)
	}
	state := &acpExecutionState{
		process:   process,
		conn:      conn,
		sessionID: sessionID,
		pending:   map[ActionID]*acpPendingPrompt{"prompt-1": pending},
	}

	drainACPPending(state, quickGrades())

	if len(state.pending) != 0 {
		t.Fatalf("state.pending = %+v, want empty after drain", state.pending)
	}
	if process.isReaped() {
		t.Fatal("isReaped() = true, want false: cancel-aware resolves without tearing the process down")
	}
}

func TestDrainACPPendingLeavesNoProcessBehindForAnUnresponsiveChild(t *testing.T) {
	process, conn, sessionID := startEscalationChild(t, "ignore-signals")

	pending, err := conn.promptAsync(context.Background(), sessionID, "hello")
	if err != nil {
		t.Fatalf("promptAsync: %v", err)
	}
	state := &acpExecutionState{
		process:   process,
		conn:      conn,
		sessionID: sessionID,
		pending:   map[ActionID]*acpPendingPrompt{"prompt-1": pending},
	}

	drainACPPending(state, quickGrades())

	if len(state.pending) != 0 {
		t.Fatalf("state.pending = %+v, want empty after drain", state.pending)
	}
	if !process.isReaped() {
		t.Fatal("isReaped() = false, want true: drain must escalate all the way to SIGKILL for this child")
	}
	if processExists(process.pid()) {
		t.Fatal("process leaked: still exists after drainACPPending returned")
	}
}
