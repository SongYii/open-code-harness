package application_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/workspacefs"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestFileMutationObservationIsSessionScopedAndSurvivesOrdinaryTurns(t *testing.T) {
	files, root := newMutationScenarioFS(t)
	abs := filepath.Join(root, "state.txt")
	if err := os.WriteFile(abs, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := newSequenceModel(
		toolCall("a-read", tools.NameReadFile, `{"path":"state.txt"}`),
		completedText("a-read-complete"),
		completedText("ordinary-next-turn"),
		toolCall("b-edit", tools.NameEditFile, `{"path":"state.txt","old_string":"before","new_string":"session-b"}`),
		completedText("b-complete"),
		toolCall("a-edit", tools.NameEditFile, `{"path":"state.txt","old_string":"before","new_string":"session-a"}`),
		completedText("a-edit-complete"),
	)
	config := application.DefaultConfig()
	config.PolicyMode = policy.ModeAllowWrites
	service, _ := newToolService(t, model, files, nil, nil, config)
	sessionA := createMutationSession(t, service, root)
	sessionB := createMutationSession(t, service, root)

	runMutationTurn(t, service, sessionA, "request-a-read", "read")
	runMutationTurn(t, service, sessionA, "request-a-ordinary", "continue")
	resultB := runMutationTurn(t, service, sessionB, "request-b-edit", "edit")
	if failed := lastToolFailed(resultB.Records); failed.Code != string(tools.CodeFilesystemNotObserved) {
		t.Fatalf("session B edit failure = %#v, want fs_not_observed", failed)
	}
	resultA := runMutationTurn(t, service, sessionA, "request-a-edit", "edit")
	if failed := lastToolFailed(resultA.Records); failed.Code != "" {
		t.Fatalf("session A edit unexpectedly failed: %#v", failed)
	}
	assertMutationFile(t, abs, "session-a")
}

func TestFileMutationObservationIsRuntimeLocalAcrossServiceReconstruction(t *testing.T) {
	files, root := newMutationScenarioFS(t)
	abs := filepath.Join(root, "state.txt")
	if err := os.WriteFile(abs, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := application.DefaultConfig()
	config.PolicyMode = policy.ModeAllowWrites
	store := newTurnMemoryStore(t)
	ids := testkit.NewSequenceIDs()
	firstModel := newSequenceModel(
		toolCall("read", tools.NameReadFile, `{"path":"state.txt"}`),
		completedText("read-complete"),
	)
	first := newMutationToolService(t, store, ids, firstModel, files, nil, nil, config)
	sessionID := createMutationSession(t, first, root)
	runMutationTurn(t, first, sessionID, "request-read", "read")

	secondModel := newSequenceModel(
		toolCall("edit", tools.NameEditFile, `{"path":"state.txt","old_string":"before","new_string":"after"}`),
		completedText("continued"),
	)
	second := newMutationToolService(t, store, ids, secondModel, files, nil, nil, config)
	result := runMutationTurn(t, second, sessionID, "request-new-service-edit", "edit")
	if failed := lastToolFailed(result.Records); failed.Code != string(tools.CodeFilesystemNotObserved) {
		t.Fatalf("new Service edit failure = %#v, want fs_not_observed", failed)
	}
	assertMutationFile(t, abs, "before")
}

func TestFileMutationLifecycleClearsObservationAfterResumeCloseAndDelete(t *testing.T) {
	for _, lifecycle := range []struct {
		name string
		act  func(*application.Service, domain.SessionID, string) error
	}{
		{name: "resume", act: func(service *application.Service, sessionID domain.SessionID, root string) error {
			_, err := service.ResumeSession(context.Background(), application.ResumeSessionRequest{SessionID: sessionID, WorkspaceRoot: root})
			return err
		}},
		{name: "close", act: func(service *application.Service, sessionID domain.SessionID, _ string) error {
			_, err := service.CloseSession(context.Background(), application.CloseSessionRequest{SessionID: sessionID})
			return err
		}},
		{name: "delete", act: func(service *application.Service, sessionID domain.SessionID, root string) error {
			return service.DeleteSession(context.Background(), application.DeleteSessionRequest{SessionID: sessionID, WorkspaceRoot: root})
		}},
	} {
		t.Run(lifecycle.name, func(t *testing.T) {
			files, root := newMutationScenarioFS(t)
			if err := os.WriteFile(filepath.Join(root, "state.txt"), []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			model := newSequenceModel(
				toolCall("read", tools.NameReadFile, `{"path":"state.txt"}`),
				completedText("read-complete"),
			)
			service, _ := newToolService(t, model, files, nil, nil, application.DefaultConfig())
			sessionID := createMutationSession(t, service, root)
			runMutationTurn(t, service, sessionID, "request-read", "read")
			if got := mutationObservationSessions(service); got != 1 {
				t.Fatalf("observation sessions before %s = %d, want 1", lifecycle.name, got)
			}
			if err := lifecycle.act(service, sessionID, root); err != nil {
				t.Fatalf("%s session: %v", lifecycle.name, err)
			}
			if got := mutationObservationSessions(service); got != 0 {
				t.Fatalf("observation sessions after %s = %d, want 0", lifecycle.name, got)
			}
		})
	}
}

func TestFileMutationExecBypassIsDetectedByFollowingStructuredEdit(t *testing.T) {
	files, root := newMutationScenarioFS(t)
	abs := filepath.Join(root, "state.txt")
	if err := os.WriteFile(abs, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := mutationCommandRunner{run: func(spec tools.CommandSpec) (tools.CommandResult, error) {
		if err := os.WriteFile(filepath.Join(spec.Cwd, "state.txt"), []byte("changed-by-exec"), 0o600); err != nil {
			return tools.CommandResult{}, err
		}
		return tools.CommandResult{ExitCode: 0}, nil
	}}
	model := newSequenceModel(
		toolCall("read", tools.NameReadFile, `{"path":"state.txt"}`),
		toolCall("exec", tools.NameExec, `{"argv":["fixture-mutate"]}`),
		toolCall("edit", tools.NameEditFile, `{"path":"state.txt","old_string":"changed-by-exec","new_string":"structured-edit"}`),
		completedText("continued"),
	)
	config := application.DefaultConfig()
	config.PolicyMode = policy.ModeAllowWrites
	service, _ := newToolService(t, model, files, commands, mutationApprover{}, config)
	sessionID := createMutationSession(t, service, root)
	result := runMutationTurn(t, service, sessionID, "request-exec-stale", "mutate")
	if failed := lastToolFailed(result.Records); failed.Code != string(tools.CodeFilesystemStaleVersion) || failed.Message != application.ToolTextFSStaleVersion {
		t.Fatalf("structured edit after exec failure = %#v, want bounded stale-version result", failed)
	}
	assertMutationFile(t, abs, "changed-by-exec")
}

func newMutationScenarioFS(t *testing.T) (*workspacefs.FileSystem, string) {
	t.Helper()
	root := t.TempDir()
	files, err := workspacefs.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return files, root
}

func newMutationToolService(t *testing.T, store application.EventStore, ids application.IDGenerator, model engine.Model, files tools.FileSystem, commands tools.CommandRunner, approver tools.Approver, base application.Config) *application.Service {
	t.Helper()
	catalog, err := tools.NewCatalog(tools.DefaultWorkspaceSpecs())
	if err != nil {
		t.Fatal(err)
	}
	if commands == nil {
		commands = testkit.NewScriptedRunner()
	}
	config := toolConfig(catalog, files, commands, approver)
	config.PolicyMode = base.PolicyMode
	return newTurnServiceWithConfig(t, store, ids, model, config)
}

func createMutationSession(t *testing.T, service *application.Service, root string) domain.SessionID {
	t.Helper()
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	return created.SessionID
}

func runMutationTurn(t *testing.T, service *application.Service, sessionID domain.SessionID, requestID domain.RunTurnRequestID, input string) application.RunTurnResult {
	t.Helper()
	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: sessionID, RequestID: requestID, Input: input, Sink: &testkit.RecordingSink{},
	})
	if err != nil {
		t.Fatalf("RunTurn(%s) error = %v", requestID, err)
	}
	return result
}

func toolCall(id, name, arguments string) []engine.StreamEvent {
	return []engine.StreamEvent{
		{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: id, Name: name, Arguments: arguments}},
		{Type: engine.StreamEventCompleted},
	}
}

func completedText(text string) []engine.StreamEvent {
	return []engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: text}, {Type: engine.StreamEventCompleted}}
}

func assertMutationFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file = %q, err=%v, want %q", got, err, want)
	}
}

// Observation state is deliberately private and has no public behavior after
// Close/Delete. This narrow white-box assertion proves successful lifecycle
// boundaries release that runtime-local state rather than persisting it.
func mutationObservationSessions(service *application.Service) int {
	serviceValue := reflect.ValueOf(service).Elem()
	observations := serviceValue.FieldByName("filesSeen").Elem()
	return observations.FieldByName("bySession").Len()
}

type mutationCommandRunner struct {
	run func(tools.CommandSpec) (tools.CommandResult, error)
}

func (runner mutationCommandRunner) Run(_ context.Context, spec tools.CommandSpec) (tools.CommandResult, error) {
	return runner.run(spec)
}

type mutationApprover struct{}

func (mutationApprover) Decide(context.Context, tools.ApprovalRequest) (tools.ApprovalAnswer, error) {
	return tools.ApprovalAnswer{Granted: true}, nil
}
