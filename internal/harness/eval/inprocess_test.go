package eval

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/composition"
)

// newCompletionFixture is a minimal OpenAI-compatible SSE fixture server: it
// answers every request with one non-tool-calling text completion. This
// mirrors internal/harness/composition/end_to_end_test.go's own
// newTwoStepProvider wire format, simplified to a single response, since
// eval's tests cannot reach that package's unexported test helpers.
func newCompletionFixture(t *testing.T, text string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		writeSSE(w, flusher, fmt.Sprintf(`{"choices":[{"delta":{"content":%q},"finish_reason":null}]}`, text))
		writeSSE(w, flusher, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		writeSSE(w, flusher, "[DONE]")
	}))
	t.Cleanup(server.Close)
	return server
}

// newFailingFixture answers every request with an HTTP error, so RunTurn
// fails without any ctx cancellation involved -- the shape that should
// classify as OutcomeSubjectFailed rather than infra or indeterminate.
func newFailingFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		http.Error(w, "fixture: provider refused the request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	return server
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, payload string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher != nil {
		flusher.Flush()
	}
}

func testSubject(t *testing.T, endpoint string) Subject {
	t.Helper()
	subject := validSubject()
	subject.Provider.NormalizedEndpoint = endpoint
	subject.Provider.CredentialEnvVar = "OCH_EVAL_TEST_API_KEY"
	subject.Provider.Lane = ProviderLaneFixture
	subject.Policy.SandboxPolicy = SandboxPolicyUnsandboxedAllowed
	t.Setenv(subject.Provider.CredentialEnvVar, "eval-inprocess-test-key")
	return subject
}

func TestBuildConfigMapsSubjectFields(t *testing.T) {
	subject := testSubject(t, "https://provider.invalid/v1")
	directories, err := NewAttemptRoot(t.TempDir(), mustAttemptID(t))
	if err != nil {
		t.Fatalf("NewAttemptRoot: %v", err)
	}
	attemptID := mustAttemptID(t)

	config, err := BuildConfig(subject, directories, attemptID)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if config.WorkspaceRoot != directories.Workspace {
		t.Fatalf("WorkspaceRoot = %q, want %q", config.WorkspaceRoot, directories.Workspace)
	}
	if config.DatabasePath != filepath.Join(directories.Database, "harness.db") {
		t.Fatalf("DatabasePath = %q, want %s/harness.db", config.DatabasePath, directories.Database)
	}
	if config.AuditDirectory != directories.Audit {
		t.Fatalf("AuditDirectory = %q, want %q", config.AuditDirectory, directories.Audit)
	}
	if config.RuntimeID != string(attemptID) {
		t.Fatalf("RuntimeID = %q, want %q", config.RuntimeID, attemptID)
	}
	if config.Provider.BaseURL != subject.Provider.NormalizedEndpoint {
		t.Fatalf("Provider.BaseURL = %q, want %q", config.Provider.BaseURL, subject.Provider.NormalizedEndpoint)
	}
	if !config.Provider.AllowInsecureLoopback {
		t.Fatal("Provider.AllowInsecureLoopback = false for a fixture-lane Subject, want true")
	}
	if !config.AllowUnsandboxedExec {
		t.Fatal("AllowUnsandboxedExec = false for SandboxPolicyUnsandboxedAllowed, want true")
	}
}

func TestBuildConfigRejectsInvalidSubject(t *testing.T) {
	subject := testSubject(t, "https://provider.invalid/v1")
	subject.Provider.ModelID = ""
	directories, err := NewAttemptRoot(t.TempDir(), mustAttemptID(t))
	if err != nil {
		t.Fatalf("NewAttemptRoot: %v", err)
	}
	if _, err := BuildConfig(subject, directories, mustAttemptID(t)); err == nil {
		t.Fatal("BuildConfig accepted an invalid Subject")
	}
}

func runFixtureAttempt(t *testing.T, endpoint string, scenario Scenario) (ExecutionOutcome, error) {
	t.Helper()
	subject := testSubject(t, endpoint)
	attemptID := mustAttemptID(t)
	directories, err := NewAttemptRoot(t.TempDir(), attemptID)
	if err != nil {
		t.Fatalf("NewAttemptRoot: %v", err)
	}
	config, err := BuildConfig(subject, directories, attemptID)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	return RunAttempt(context.Background(), attemptID, config, scenario)
}

func TestRunAttemptCompletesPromptThenCompact(t *testing.T) {
	server := newCompletionFixture(t, "the answer")
	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		{Type: ActionPrompt, Prompt: &PromptAction{Text: "what is the answer?"}},
		{Type: ActionCompact, Compact: &CompactAction{Strategy: "summary"}},
	}

	execution, err := runFixtureAttempt(t, server.URL, scenario)
	if err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if execution.Outcome.Status != OutcomeCompleted {
		t.Fatalf("Outcome.Status = %q, want %q (message: %s)", execution.Outcome.Status, OutcomeCompleted, execution.Outcome.Message)
	}
	if execution.SessionID == "" {
		t.Fatal("ExecutionOutcome.SessionID is empty")
	}
	if execution.Outcome.TerminalSession == nil {
		t.Fatal("Outcome.TerminalSession is nil, want the loaded terminal facts")
	}
	if execution.Outcome.TerminalSession.TurnCount != 1 {
		t.Fatalf("TerminalSession.TurnCount = %d, want 1", execution.Outcome.TerminalSession.TurnCount)
	}
	if execution.Outcome.TerminalSession.Running {
		t.Fatal("TerminalSession.Running = true after every action completed")
	}
	if execution.Outcome.EndedAt.Before(execution.Outcome.StartedAt) {
		t.Fatalf("EndedAt %v precedes StartedAt %v", execution.Outcome.EndedAt, execution.Outcome.StartedAt)
	}
	if err := execution.Outcome.Validate(); err != nil {
		t.Fatalf("returned Outcome fails its own Validate(): %v", err)
	}
}

func TestRunAttemptClassifiesProviderFailureAsSubjectFailed(t *testing.T) {
	server := newFailingFixture(t)
	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		{Type: ActionPrompt, Prompt: &PromptAction{Text: "hello"}},
	}

	execution, err := runFixtureAttempt(t, server.URL, scenario)
	if err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if execution.Outcome.Status != OutcomeSubjectFailed {
		t.Fatalf("Outcome.Status = %q, want %q", execution.Outcome.Status, OutcomeSubjectFailed)
	}
	if execution.Outcome.Message == "" {
		t.Fatal("Outcome.Message is empty")
	}
}

func TestRunAttemptRecordsUnsupportedActionAsInfraFailed(t *testing.T) {
	server := newCompletionFixture(t, "unused")
	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		{Type: ActionRestart, Restart: &RestartAction{}},
	}

	execution, err := runFixtureAttempt(t, server.URL, scenario)
	if err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if execution.Outcome.Status != OutcomeInfraFailed {
		t.Fatalf("Outcome.Status = %q, want %q", execution.Outcome.Status, OutcomeInfraFailed)
	}
	if execution.Outcome.Code != "unsupported_action" {
		t.Fatalf("Outcome.Code = %q, want %q", execution.Outcome.Code, "unsupported_action")
	}
}

func TestRunAttemptRejectsInvalidScenario(t *testing.T) {
	server := newCompletionFixture(t, "unused")
	invalid := validScenario()
	invalid.Actions = nil
	if _, err := runFixtureAttempt(t, server.URL, invalid); err == nil {
		t.Fatal("RunAttempt accepted an invalid Scenario")
	}
}

func TestRunAttemptRejectsNilContext(t *testing.T) {
	//nolint:staticcheck // passing a nil context is exactly what this asserts.
	if _, err := RunAttempt(nil, mustAttemptID(t), composition.Config{}, validScenario()); err == nil {
		t.Fatal("RunAttempt(nil, ...) succeeded, want error")
	}
}
