//go:build unix

package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRunACPAttemptScriptedDenyBlocksTheToolWithoutFailingTheAttempt is the
// ACP-executor mirror of inprocess_test.go's own
// TestRunAttemptScriptedDenyBlocksTheToolWithoutFailingTheAttempt: proves
// NewACPPermissionHandler's wire adaptation, driven end-to-end through a
// real och -acp subprocess's own session/request_permission call, actually
// blocks a scripted deny rather than only the pure-JSON unit tests in
// acp_permission_test.go.
func TestRunACPAttemptScriptedDenyBlocksTheToolWithoutFailingTheAttempt(t *testing.T) {
	ochBin := buildOchBinary(t)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	server := newApprovalProvider(t)
	subject := testSubject(t, server.Server)
	attemptID, directories := acpTestDirectories(t)

	scenario := runnerScenario("acp-approval-deny")
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "write the file"),
	}
	scenario.ApprovalScript = []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "write_file", Answer: ApprovalDeny},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunACPAttempt(ctx, attemptID, subject, directories, scenario, ACPLaunchConfig{Binary: binary}, matcher)
	if err != nil {
		t.Fatalf("RunACPAttempt() error = %v", err)
	}
	if execution.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q: a scripted denial is expected Subject behavior, not an infra/subject failure (message: %s)",
			execution.Outcome.Status, OutcomeCompleted, execution.Outcome.Message)
	}
	if !execution.WriterStopped {
		t.Fatal("WriterStopped = false, want true")
	}
	if _, err := os.ReadFile(filepath.Join(directories.Workspace, "output.txt")); err == nil {
		t.Fatal("write_file ran despite a scripted deny delivered over the real ACP wire")
	}
	observations := matcher.Observations()
	if len(observations) != 1 || observations[0].Answer != ApprovalDeny || observations[0].Violation != "" || observations[0].ToolName != "write_file" {
		t.Fatalf("matcher observations = %+v, want exactly one clean scripted deny for write_file", observations)
	}
}

// TestRunACPAttemptCancelResolvesViaSessionCancelAndContinues is the
// ACP-executor mirror of inprocess_test.go's own
// TestRunAttemptCancelInterruptsInFlightPromptAndContinues: the real
// och -acp agent cancels its own in-flight provider call on session/cancel
// (internal/harness/adapters/acp/server.go's handleNotification), so this
// proves escalateCancel's mildest session_cancel rung resolves against a
// real subprocess without ever tearing it down, and the Scenario's next
// prompt action still runs against the same session afterward.
func TestRunACPAttemptCancelResolvesViaSessionCancelAndContinues(t *testing.T) {
	ochBin := buildOchBinary(t)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	server := newBlockingUnlessMarkedProvider(t, "after cancel")
	subject := testSubject(t, server.Server)
	attemptID, directories := acpTestDirectories(t)

	scenario := runnerScenario("acp-cancel-continue")
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "this call is never released except by cancellation"),
		{ID: "cancel-1", Type: ActionCancel, Cancel: &CancelAction{TargetActionID: "prompt-1"}},
		newEchoScenarioAction("prompt-2", "after cancel"),
	}
	scenario.ApprovalScript = nil

	// A defensive bound: if cancellation were broken, prompt-1 would hang
	// forever, and this test would time out rather than pass, instead of
	// hanging the whole suite.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunACPAttempt(ctx, attemptID, subject, directories, scenario, ACPLaunchConfig{Binary: binary}, matcher)
	if err != nil {
		t.Fatalf("RunACPAttempt() error = %v", err)
	}
	if execution.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q: a scripted cancel resolved by session/cancel alone is expected Scenario behavior (message: %s)",
			execution.Outcome.Status, OutcomeCompleted, execution.Outcome.Message)
	}
	if !execution.WriterStopped {
		t.Fatal("WriterStopped = false, want true")
	}
	// prompt-1's own request may or may not ever reach the network: a
	// cancel this close behind its prompt action can race the agent's own
	// dispatch. Only prompt-2's request -- matched by content, not call
	// order -- is guaranteed.
	if server.calls.Load() < 1 {
		t.Fatalf("provider calls = %d, want at least 1: prompt-2's request", server.calls.Load())
	}
}

// TestRunACPAttemptRestartModesReopenAndLoadSameSession proves design
// §16's restart action against a real och -acp subprocess: a successor
// process is only launched once the prior writer's reap is proven, and the
// same ACP session ID is resumed via session/load so the Scenario's second
// prompt still runs in the same logical session.
//
// Only clean_shutdown and kill are exercised here as "must complete";
// interrupt is proven separately by
// TestRunACPAttemptInterruptRestartReportsUnprovenReapAgainstAnIdleAgent,
// which documents a real, verified limitation rather than asserting a
// success this agent build cannot currently deliver — see that test's own
// doc comment.
func TestRunACPAttemptRestartModesReopenAndLoadSameSession(t *testing.T) {
	for _, mode := range []RestartMode{RestartModeCleanShutdown, RestartModeKill} {
		t.Run(string(mode), func(t *testing.T) {
			ochBin := buildOchBinary(t)
			binary, err := ResolveACPBinary(ochBin)
			if err != nil {
				t.Fatalf("ResolveACPBinary: %v", err)
			}
			server := newEchoProvider(t)
			subject := testSubject(t, server.Server)
			attemptID, directories := acpTestDirectories(t)

			scenario := runnerScenario(ScenarioID("acp-restart-" + string(mode)))
			scenario.Actions = []ScenarioAction{
				newEchoScenarioAction("prompt-1", "before restart"),
				{ID: "restart-1", Type: ActionRestart, Restart: &RestartAction{Mode: mode}},
				newEchoScenarioAction("prompt-2", "after restart"),
			}
			scenario.ApprovalScript = nil
			scenario.RequiredCapabilities = []string{"prompt", "restart_" + string(mode)}

			// kill (unlike clean_shutdown) needs the full RelaunchGrace
			// bound: the successor cannot acquire the writer lease under
			// its own new runtime ID until the prior, abruptly-terminated
			// holder's lease naturally expires (ACPShutdownGrades.
			// RelaunchGrace's own doc comment explains why in full) --
			// this repository's own default writer lease is 30s.
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			matcher := NewApprovalMatcher(scenario.ApprovalScript)
			execution, err := RunACPAttempt(ctx, attemptID, subject, directories, scenario,
				ACPLaunchConfig{Binary: binary, ShutdownGrades: ACPShutdownGrades{FinalGrace: 5 * time.Second}}, matcher)
			if err != nil {
				t.Fatalf("RunACPAttempt() error = %v", err)
			}
			if execution.Outcome.Status != OutcomeCompleted {
				t.Fatalf("Outcome.Status = %q, want %q (message: %s)", execution.Outcome.Status, OutcomeCompleted, execution.Outcome.Message)
			}
			if !execution.WriterStopped {
				t.Fatal("WriterStopped = false, want true")
			}
			if server.calls.Load() != 2 {
				t.Fatalf("provider calls = %d, want 2: one before restart, one after", server.calls.Load())
			}
			if execution.SessionID == "" {
				t.Fatal("SessionID is empty")
			}
		})
	}
}

// TestRunACPAttemptInterruptRestartReportsUnprovenReapAgainstAnIdleAgent
// documents a real, verified limitation of the current och -acp agent
// rather than a bug in this task's own escalation logic: runACPRestart's
// RestartModeInterrupt sends SIGINT to the owned process group without
// closing stdin (design's own distinction between "abrupt signal" and
// "graceful protocol shutdown" restart shapes).
//
// This test used to assert the opposite outcome. internal/harness/adapters/
// acp's Serve decoded frames through a blocking read and only checked
// ctx.Err() between already-decoded frames, so an initialized but idle agent
// never observed signal.NotifyContext's cancellation: SIGINT left a real
// process running past any bound, while SIGKILL reaped it in well under a
// second. The correct behaviour then was to report infra_failed rather than a
// false completion, and this test pinned that.
//
// Serve now owns its input and closes it when its context is cancelled, which
// releases the blocked read, so SIGINT resolves through a normal shutdown. The
// prompt after the restart is what proves the successor Assembly loaded the
// same Session rather than merely starting a fresh process.
func TestRunACPAttemptInterruptRestartReapsAndRelaunchesAnIdleAgent(t *testing.T) {
	ochBin := buildOchBinary(t)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID, directories := acpTestDirectories(t)

	scenario := runnerScenario("acp-restart-interrupt-idle")
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "before restart"),
		{ID: "restart-1", Type: ActionRestart, Restart: &RestartAction{Mode: RestartModeInterrupt}},
		newEchoScenarioAction("prompt-2", "after restart"),
	}
	scenario.ApprovalScript = nil
	scenario.RequiredCapabilities = []string{"prompt", "restart_interrupt"}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunACPAttempt(ctx, attemptID, subject, directories, scenario,
		ACPLaunchConfig{Binary: binary, ShutdownGrades: ACPShutdownGrades{FinalGrace: 5 * time.Second}}, matcher)
	if err != nil {
		t.Fatalf("RunACPAttempt() error = %v", err)
	}
	if execution.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome = %+v, want completed: SIGINT must now reap an idle agent and relaunch", execution.Outcome)
	}
	if !execution.WriterStopped {
		t.Fatal("WriterStopped = false, want true")
	}
	// The prompt after the restart is what proves the successor Assembly
	// loaded the same Session rather than merely starting a fresh process.
	if execution.Outcome.TerminalSession == nil || execution.Outcome.TerminalSession.TurnCount < 2 {
		t.Fatalf("TerminalSession = %+v, want at least two turns across the restart",
			execution.Outcome.TerminalSession)
	}
}
