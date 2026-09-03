//go:build unix

package eval

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	ochBinaryBuildOnce sync.Once
	ochBinaryBuildPath string
	ochBinaryBuildErr  error
)

// buildOchBinary builds this repository's own real och binary from
// source into a temp directory once per test binary run, matching
// cmd/acp-client's own buildOchBinary helper — conformance here means
// driving an actual, independently built och -acp subprocess, not a
// fixture standing in for one.
func buildOchBinary(t testing.TB) string {
	t.Helper()
	ochBinaryBuildOnce.Do(func() {
		wd, err := os.Getwd() // .../internal/harness/eval
		if err != nil {
			ochBinaryBuildErr = err
			return
		}
		repoRoot := filepath.Join(wd, "..", "..", "..")
		dir, err := os.MkdirTemp("", "och-acp-build")
		if err != nil {
			ochBinaryBuildErr = err
			return
		}
		path := filepath.Join(dir, "och")
		build := exec.Command("go", "build", "-o", path, "./cmd/och")
		build.Dir = repoRoot
		if out, err := build.CombinedOutput(); err != nil {
			ochBinaryBuildErr = err
			t.Logf("go build ./cmd/och: %v\n%s", err, out)
			return
		}
		ochBinaryBuildPath = path
	})
	if ochBinaryBuildErr != nil {
		t.Fatalf("build och: %v", ochBinaryBuildErr)
	}
	return ochBinaryBuildPath
}

func acpTestDirectories(t *testing.T) (AttemptID, AttemptRootDirectories) {
	t.Helper()
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)
	return attemptID, directories
}

func TestRunACPAttemptHappyPathAgainstRealOchBinary(t *testing.T) {
	ochBin := buildOchBinary(t)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}

	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID, directories := acpTestDirectories(t)

	scenario := runnerScenario("acp-smoke")
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "hello"),
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{VerifierFact: "n/a"}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	execution, err := RunACPAttempt(ctx, attemptID, subject, directories, scenario, ACPLaunchConfig{Binary: binary}, NewApprovalMatcher(scenario.ApprovalScript))
	if err != nil {
		t.Fatalf("RunACPAttempt() error = %v", err)
	}
	if !execution.WriterStopped {
		t.Fatal("WriterStopped = false, want true after a normal shutdown")
	}
	if execution.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q (message: %s)", execution.Outcome.Status, OutcomeCompleted, execution.Outcome.Message)
	}
	if execution.SessionID == "" {
		t.Fatal("SessionID is empty")
	}
	if server.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", server.calls.Load())
	}

	// The database this subprocess wrote is a real, independent process's
	// own canonical SQLite file — CollectEvidence must be able to read it
	// cold exactly as it does for an in-process Attempt.
	documents := publishTestEvidenceDocuments(t, directories, attemptID, scenario, subject)
	outcome, manifest, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, documents, CollectionLimits{})
	if err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	if outcome.CollectionStatus != CollectionComplete {
		t.Fatalf("CollectionStatus = %q, want %q", outcome.CollectionStatus, CollectionComplete)
	}
	if len(manifest.Entries) == 0 {
		t.Fatal("manifest has no entries")
	}
}

func TestRunACPAttemptStartupTimeoutAgainstHangingChild(t *testing.T) {
	child := buildACPChild(t)
	binary, err := ResolveACPBinary(child)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	// acpchild ignores every och flag it does not itself define; -mode
	// hang is appended after NormalizedArgv's own flags by using a
	// Subject whose real flags acpchild simply does not parse -- flag
	// parsing failure would itself prove nothing here, so this test
	// launches acpchild directly through startACPProcess instead of
	// through RunACPAttempt's own NormalizedArgv-derived argv.
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	_, directories := acpTestDirectories(t)

	env, err := BuildChildEnvironment(subject)
	if err != nil {
		t.Fatalf("BuildChildEnvironment: %v", err)
	}
	process, err := startACPProcess(binary.Path, []string{"-mode", "hang"}, env, directories.Workspace, 1<<20)
	if err != nil {
		t.Fatalf("startACPProcess: %v", err)
	}
	defer func() {
		_ = process.stdin.Close()
		if exited, _ := process.waitTimeout(50 * time.Millisecond); !exited {
			killHungTestProcess(t, process)
		}
	}()

	start := time.Now()
	initCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = mustNewACPConnectionForTest(t, process).initialize(initCtx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Initialize() error = nil, want a timeout against a hanging child")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Initialize() took %s to time out, want it bounded by the context deadline", elapsed)
	}
}

// runACPAction's own "default" branch (unsupported_action) is defensive,
// pre-existing dead code identical in spirit to inprocess.go's own
// equivalent default branch: every v1 ScenarioActionType (prompt,
// compact, cancel, restart, collect) is implemented by both executors,
// and Scenario.Validate itself refuses any other action type before
// RunACPAttempt ever dispatches one, so there is no longer a validly
// constructed Scenario this package's own public entry point can drive
// into that branch — compact (Task 14) was the last one. There is
// deliberately no test for it here, matching inprocess_test.go's own
// choice not to test its analogous branch either.

// killHungTestProcess is test-only cleanup for a child this task's own
// production code has no way to terminate (no SIGTERM/SIGKILL escalation
// yet — Task 13's own scope): it exists purely so a test proving the
// "does not exit on its own" path never leaks the process it started.
func killHungTestProcess(t *testing.T, process *acpProcess) {
	t.Helper()
	if err := syscall.Kill(process.pid(), syscall.SIGKILL); err != nil {
		t.Logf("cleanup kill: %v", err)
		return
	}
	_, _ = process.waitTimeout(5 * time.Second)
}

// discardPermissionHandler answers no permission traffic — every test in
// this file only drives initialize, which never triggers a
// session/request_permission call.
func discardPermissionHandler(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func mustNewACPConnectionForTest(t *testing.T, process *acpProcess) *acpConnection {
	t.Helper()
	conn := newACPConnection(process.stdout, process.stdin, discardPermissionHandler)
	t.Cleanup(func() { _ = conn.close() })
	return conn
}

// TestRunACPAttemptMalformedFrameFromChild proves a child that writes a
// line on stdout that is not a valid JSON-RPC frame at all fails
// Initialize promptly and leaves no process behind, rather than hanging
// this executor or silently misinterpreting garbage as a real response.
func TestRunACPAttemptMalformedFrameFromChild(t *testing.T) {
	child := buildACPChild(t)
	binary, err := ResolveACPBinary(child)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	_, directories := acpTestDirectories(t)

	env, err := BuildChildEnvironment(subject)
	if err != nil {
		t.Fatalf("BuildChildEnvironment: %v", err)
	}
	process, err := startACPProcess(binary.Path, []string{"-mode", "malformed-frame"}, env, directories.Workspace, 1<<20)
	if err != nil {
		t.Fatalf("startACPProcess: %v", err)
	}
	defer func() {
		_ = process.stdin.Close()
		_, _ = process.waitTimeout(5 * time.Second)
	}()

	initCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = mustNewACPConnectionForTest(t, process).initialize(initCtx)
	if err == nil {
		t.Fatal("Initialize() error = nil, want a refusal after a malformed frame closed the connection")
	}

	// malformed-frame exits(0) on its own right after writing the bad
	// line; this must not leave a hung child even though this executor
	// never sent it anything.
	exited, _ := process.waitTimeout(5 * time.Second)
	if !exited {
		t.Fatal("child did not exit on its own after writing its malformed frame")
	}
}
