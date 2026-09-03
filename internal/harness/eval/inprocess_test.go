package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testSubject builds a valid Subject wired to server: fixture lane, an
// insecure loopback endpoint (real HTTP against an in-process
// httptest.Server, exactly as internal/harness/composition's own end-to-end
// tests do), and unsandboxed exec so these tests build a working Assembly
// on any host regardless of whether a sandbox backend is available.
func testSubject(t testing.TB, server *httptest.Server) Subject {
	t.Helper()
	subject := validSubject()
	subject.Provider.NormalizedEndpoint = server.URL
	subject.Provider.Lane = ProviderLaneFixture
	subject.Provider.CredentialEnvVar = "OCH_EVAL_TEST_API_KEY"
	subject.Policy.SandboxPolicy = SandboxPolicyUnsandboxedAllowed
	t.Setenv(subject.Provider.CredentialEnvVar, "test-key")
	return subject
}

func testAttemptID(t testing.TB) AttemptID {
	t.Helper()
	attemptID, err := NewAttemptID()
	if err != nil {
		t.Fatalf("NewAttemptID: %v", err)
	}
	return attemptID
}

func testDirectories(t testing.TB, attemptID AttemptID) AttemptRootDirectories {
	t.Helper()
	directories, err := NewAttemptRoot(t.TempDir(), attemptID)
	if err != nil {
		t.Fatalf("NewAttemptRoot: %v", err)
	}
	return directories
}

func newEchoScenarioAction(id ActionID, text string) ScenarioAction {
	return ScenarioAction{ID: id, Type: ActionPrompt, Prompt: &PromptAction{Text: text}}
}

// echoProvider answers every request with a fixed, tool-free assistant
// message, so a prompt action always reaches TurnStatusCompleted without
// any policy/approval involvement.
type echoProvider struct {
	*httptest.Server
	calls atomic.Int32
}

func newEchoProvider(t testing.TB) *echoProvider {
	t.Helper()
	provider := &echoProvider{}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider.calls.Add(1)
		writeSSE(w, `{"choices":[{"delta":{"content":"done"},"finish_reason":null}]}`)
		writeSSE(w, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		writeSSE(w, "[DONE]")
	}))
	t.Cleanup(provider.Server.Close)
	return provider
}

// approvalProvider answers its first request with a write_file tool call
// (design's approval-required risk class under policy.ModeDefault) and
// every later request with a plain answer, so the test can prove the
// scripted ApprovalMatcher -- not a bypass -- decided the tool call.
type approvalProvider struct {
	*httptest.Server
	calls atomic.Int32
}

func newApprovalProvider(t testing.TB) *approvalProvider {
	t.Helper()
	provider := &approvalProvider{}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if provider.calls.Add(1) == 1 {
			writeSSE(w, fmt.Sprintf(
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_write","function":{"name":"write_file","arguments":%s}}]},"finish_reason":null}]}`,
				jsonString(`{"path":"output.txt","content":"hello"}`)))
			writeSSE(w, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
			writeSSE(w, "[DONE]")
			return
		}
		writeSSE(w, `{"choices":[{"delta":{"content":"acknowledged"},"finish_reason":null}]}`)
		writeSSE(w, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		writeSSE(w, "[DONE]")
	}))
	t.Cleanup(provider.Server.Close)
	return provider
}

// blockingUnlessMarkedProvider blocks every request until its own ctx ends,
// except a request whose body contains marker, which is answered
// immediately. Matching on content rather than call order matters: a
// prompt canceled fast enough can be interrupted by Service.RunTurn's own
// ctx check before it ever reaches the network at all (a legitimate,
// clean "canceled before starting" outcome, not a bug), so a
// canceled prompt's request may never arrive here. Only the marked
// request -- the one this test still needs a real answer for -- is
// guaranteed to reach the network.
type blockingUnlessMarkedProvider struct {
	*httptest.Server
	marker string
	calls  atomic.Int32
}

func newBlockingUnlessMarkedProvider(t *testing.T, marker string) *blockingUnlessMarkedProvider {
	t.Helper()
	provider := &blockingUnlessMarkedProvider{marker: marker}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider.calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if !strings.Contains(string(body), provider.marker) {
			<-r.Context().Done()
			return
		}
		writeSSE(w, `{"choices":[{"delta":{"content":"resumed"},"finish_reason":null}]}`)
		writeSSE(w, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		writeSSE(w, "[DONE]")
	}))
	t.Cleanup(provider.Server.Close)
	return provider
}

func writeSSE(w http.ResponseWriter, payload string) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher != nil {
		flusher.Flush()
	}
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

func TestRunAttemptHappyPathCompletesAllActions(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "hello"),
		{ID: "compact-1", Type: ActionCompact, Compact: &CompactAction{Strategy: "reset"}},
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "output.txt"}},
	}
	scenario.ApprovalScript = nil

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	result, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}
	if result.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q (message: %s)", result.Outcome.Status, OutcomeCompleted, result.Outcome.Message)
	}
	if !result.WriterStopped {
		t.Fatal("WriterStopped = false, want true after a normal completion")
	}
	if result.Outcome.TerminalSession == nil || result.Outcome.TerminalSession.TurnCount != 1 {
		t.Fatalf("TerminalSession = %+v, want TurnCount 1", result.Outcome.TerminalSession)
	}
	if result.Outcome.TerminalSession.Open != true || result.Outcome.TerminalSession.Running {
		t.Fatalf("TerminalSession = %+v, want an open, non-running session", result.Outcome.TerminalSession)
	}
	if server.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", server.calls.Load())
	}
}

func TestRunAttemptWiresScriptedApproverIntoTheAssembly(t *testing.T) {
	server := newApprovalProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "write the file"),
	}
	scenario.ApprovalScript = []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "write_file", Answer: ApprovalAllow},
	}

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	result, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}
	if result.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q (message: %s)", result.Outcome.Status, OutcomeCompleted, result.Outcome.Message)
	}
	if !result.WriterStopped {
		t.Fatal("WriterStopped = false, want true")
	}
	if server.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 (tool call, then the answer)", server.calls.Load())
	}
	written, err := os.ReadFile(filepath.Join(directories.Workspace, "output.txt"))
	if err != nil {
		t.Fatalf("the scripted allow should have let write_file run: %v", err)
	}
	if string(written) != "hello" {
		t.Fatalf("written content = %q, want %q", written, "hello")
	}
	observations := matcher.Observations()
	if len(observations) != 1 || observations[0].Answer != ApprovalAllow || observations[0].Violation != "" {
		t.Fatalf("matcher observations = %+v, want exactly one clean allow", observations)
	}
}

func TestRunAttemptScriptedDenyBlocksTheToolWithoutFailingTheAttempt(t *testing.T) {
	server := newApprovalProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "write the file"),
	}
	scenario.ApprovalScript = []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "write_file", Answer: ApprovalDeny},
	}

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	result, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}
	if result.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q: a scripted denial is expected Subject behavior, not an infra/subject failure (message: %s)",
			result.Outcome.Status, OutcomeCompleted, result.Outcome.Message)
	}
	if _, err := os.ReadFile(filepath.Join(directories.Workspace, "output.txt")); err == nil {
		t.Fatal("write_file ran despite a scripted deny")
	}
}

func TestRunAttemptCancelInterruptsInFlightPromptAndContinues(t *testing.T) {
	server := newBlockingUnlessMarkedProvider(t, "after cancel")
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "this call is never released except by cancellation"),
		{ID: "cancel-1", Type: ActionCancel, Cancel: &CancelAction{TargetActionID: "prompt-1"}},
		newEchoScenarioAction("prompt-2", "after cancel"),
	}
	scenario.ApprovalScript = nil

	// A defensive bound: if cancellation were broken, prompt-1 would hang
	// forever (nothing else ever releases it), and this test would time
	// out rather than pass, instead of hanging the whole suite.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	result, err := RunAttempt(ctx, attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}
	if result.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q: a scripted cancel is expected Scenario behavior (message: %s)",
			result.Outcome.Status, OutcomeCompleted, result.Outcome.Message)
	}
	if !result.WriterStopped {
		t.Fatal("WriterStopped = false, want true")
	}
	if result.Outcome.TerminalSession == nil || result.Outcome.TerminalSession.TurnCount != 2 {
		t.Fatalf("TerminalSession = %+v, want TurnCount 2 (the canceled prompt and the one after it)", result.Outcome.TerminalSession)
	}
	// prompt-1's own request may or may not ever reach the network: a
	// cancel this close behind its prompt action can interrupt
	// Service.RunTurn before it dispatches anything, which is a
	// legitimate, clean "canceled before starting" outcome. Only
	// prompt-2's request -- matched by content, not call order -- is
	// guaranteed.
	if server.calls.Load() < 1 {
		t.Fatalf("provider calls = %d, want at least 1: prompt-2's request", server.calls.Load())
	}
}

func TestRunAttemptCleanShutdownRestartReopensAssemblyAndLoadsSameSession(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "before restart"),
		{ID: "restart-1", Type: ActionRestart, Restart: &RestartAction{Mode: RestartModeCleanShutdown}},
		newEchoScenarioAction("prompt-2", "after restart"),
	}
	scenario.ApprovalScript = nil

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	result, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}
	if result.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q (message: %s)", result.Outcome.Status, OutcomeCompleted, result.Outcome.Message)
	}
	if !result.WriterStopped {
		t.Fatal("WriterStopped = false, want true")
	}
	if result.Outcome.TerminalSession == nil || result.Outcome.TerminalSession.TurnCount != 2 {
		t.Fatalf("TerminalSession = %+v, want TurnCount 2: both prompts ran against the same Session across the restart", result.Outcome.TerminalSession)
	}
	if server.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2: one before restart, one after", server.calls.Load())
	}
}

func TestRunAttemptRejectsAbruptRestartModes(t *testing.T) {
	for _, mode := range []RestartMode{RestartModeInterrupt, RestartModeKill} {
		t.Run(string(mode), func(t *testing.T) {
			server := newEchoProvider(t)
			subject := testSubject(t, server.Server)
			attemptID := testAttemptID(t)
			directories := testDirectories(t, attemptID)

			scenario := validScenario()
			scenario.Actions = []ScenarioAction{
				newEchoScenarioAction("prompt-1", "before restart"),
				{ID: "restart-1", Type: ActionRestart, Restart: &RestartAction{Mode: mode}},
			}
			scenario.ApprovalScript = nil
			scenario.RequiredCapabilities = []string{"prompt", "restart_" + string(mode)}

			matcher := NewApprovalMatcher(scenario.ApprovalScript)
			result, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
			if err != nil {
				t.Fatalf("RunAttempt() error = %v", err)
			}
			if result.Outcome.Status != OutcomeInfraFailed || result.Outcome.Code != "unsupported_restart_mode" {
				t.Fatalf("Outcome = %+v, want infra_failed/unsupported_restart_mode", result.Outcome)
			}
			if !result.WriterStopped {
				t.Fatal("WriterStopped = false, want true: the Assembly opened for prompt-1 must still be closed on this terminal path")
			}
		})
	}
}

func TestRunAttemptCompositionOpenFailureReturnsInfraFailedAndAnError(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)
	// A workspace that does not exist makes composition.Open itself fail,
	// before any Assembly is ever created.
	directories.Workspace = filepath.Join(directories.Root, "absent-workspace")

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{newEchoScenarioAction("prompt-1", "hello")}
	scenario.ApprovalScript = nil

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	result, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err == nil {
		t.Fatal("RunAttempt() error = nil, want non-nil: nothing durable happened yet")
	}
	if result.Outcome.Status != OutcomeInfraFailed {
		t.Fatalf("Outcome.Status = %q, want %q", result.Outcome.Status, OutcomeInfraFailed)
	}
	if !result.WriterStopped {
		t.Fatal("WriterStopped = false, want true: no Assembly was ever opened")
	}
}

func TestRunAttemptNeverReusesRuntimeIDAcrossARestart(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "before restart"),
		{ID: "restart-1", Type: ActionRestart, Restart: &RestartAction{Mode: RestartModeCleanShutdown}},
		newEchoScenarioAction("prompt-2", "after restart"),
	}
	scenario.ApprovalScript = nil

	first := launchRuntimeID("attempt-1", 0)
	second := launchRuntimeID("attempt-1", 1)
	if first == second {
		t.Fatalf("launchRuntimeID produced the same ID for two launches: %q", first)
	}

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	if _, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher); err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}
}
