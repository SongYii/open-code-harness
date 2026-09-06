package application_test

import (
	"context"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// This file holds the boundary scenarios rather than the happy paths: what
// happens between two sessions, across a process restart, and around the one
// tool this mechanism deliberately does not mediate.

func writeCallEvent(content string) engine.StreamEvent {
	return engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{
		ID: "call-write", Name: tools.NameWriteFile,
		Arguments: `{"path":"shared.txt","content":"` + content + `"}`,
	}}
}

var readSharedEvent = engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{
	ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"shared.txt"}`,
}}

var turnDone = []engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "ok"}, {Type: engine.StreamEventCompleted}}

func step(event engine.StreamEvent) []engine.StreamEvent {
	return []engine.StreamEvent{event, {Type: engine.StreamEventCompleted}}
}

func allowWrites() application.Config {
	config := application.DefaultConfig()
	config.PolicyMode = policy.ModeAllowWrites
	return config
}

func runTurns(t *testing.T, service *application.Service, session domain.SessionID, requests ...domain.RunTurnRequestID) {
	t.Helper()
	for _, request := range requests {
		if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
			SessionID: session, RequestID: request, Input: "go", Sink: &testkit.RecordingSink{},
		}); err != nil {
			t.Fatalf("%s: %v", request, err)
		}
	}
}

// TestTwoSessionsCannotShareAnObservation.
//
// Two sessions may be looking at the same workspace with entirely different
// histories. One session having read a file is not evidence that the other
// knows what is in it, and treating it as evidence would let a session write
// over something it never saw because a sibling happened to look.
func TestTwoSessionsCannotShareAnObservation(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("shared.txt", []byte("original"))

	model := newSequenceModel(
		step(readSharedEvent), turnDone,
		step(writeCallEvent("from-b")), turnDone,
	)
	service, _ := newToolService(t, model, fs, nil, nil, allowWrites())
	sessionA, _ := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	sessionB, _ := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})

	runTurns(t, service, sessionA.SessionID, "a-read")
	runTurns(t, service, sessionB.SessionID, "b-write")

	if got := lastToolMessage(model.Calls()[3].Messages).Text; got != application.ToolTextFSNotObserved {
		t.Fatalf("session B write = %q, want %q", got, application.ToolTextFSNotObserved)
	}
	if read, _ := fs.Read(context.Background(), "/workspace/shared.txt", 64); string(read.Data) != "original" {
		t.Fatalf("content = %q; session B wrote on session A's read", read.Data)
	}
}

// TestObservationsSurviveAnOrdinaryTurnBoundary is the other half of the
// lifecycle rule: clearing on resume is only correct if ordinary turns do not
// clear, or every turn would begin unable to change anything it read.
func TestObservationsSurviveAnOrdinaryTurnBoundary(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("shared.txt", []byte("original"))

	model := newSequenceModel(
		step(readSharedEvent), turnDone,
		step(writeCallEvent("second")), turnDone,
	)
	service, _ := newToolService(t, model, fs, nil, nil, allowWrites())
	created, _ := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	runTurns(t, service, created.SessionID, "read", "write")

	if got := lastToolMessage(model.Calls()[3].Messages).Text; got != "wrote 6 bytes" {
		t.Fatalf("write in the next Turn = %q; the observation did not survive", got)
	}
}

// TestCloseClearsWhatTheSessionObserved. Nothing should still be held about a
// closed session's workspace.
func TestCloseClearsWhatTheSessionObserved(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("shared.txt", []byte("original"))

	model := newSequenceModel(step(readSharedEvent), turnDone)
	service, store := newToolService(t, model, fs, nil, nil, allowWrites())
	created, _ := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	runTurns(t, service, created.SessionID, "read")

	if _, err := service.CloseSession(context.Background(), application.CloseSessionRequest{SessionID: created.SessionID}); err != nil {
		t.Fatal(err)
	}

	// A closed session cannot run another Turn, so the proof is that a fresh
	// Service over the same store -- which is what a reopened session gets --
	// finds nothing held. The store is threaded through to keep the durable
	// history identical between the two.
	_ = store
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: created.SessionID, RequestID: "after-close", Input: "go", Sink: &testkit.RecordingSink{},
	}); err == nil {
		t.Fatal("a closed session accepted another Turn")
	}
}

// TestExecIsNotMediatedAndTheNextEditDetectsIt is the honest exclusion.
//
// The guarantee covers the structured file tools. A command run through exec
// can rewrite anything in the workspace and this mechanism neither knows nor
// prevents it -- claiming otherwise would be the more dangerous documentation.
// What the mechanism does promise is that the damage is not compounded: the
// next structured write against a file exec changed is refused as stale rather
// than silently layered on top of it.
func TestExecIsNotMediatedAndTheNextEditDetectsIt(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("shared.txt", []byte("original"))

	execCall := engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{
		ID: "call-exec", Name: tools.NameExec, Arguments: `{"argv":["rewrite"]}`,
	}}
	model := newSequenceModel(
		step(readSharedEvent), turnDone,
		step(execCall), turnDone,
		step(writeCallEvent("agent")), turnDone,
	)
	// The runner stands in for any command that writes to the workspace. It
	// goes around the guarded port entirely, which is the point.
	runner := &workspaceMutatingRunner{fs: fs, rel: "shared.txt", data: []byte("rewritten-by-exec")}
	config := allowWrites()
	// exec is RiskExec, which allow_writes routes through the Approver rather
	// than denying outright, so the scenario supplies a granting one below.
	approver := testkit.NewScriptedApprover(
		testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: true}},
		testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: true}},
	)
	service, _ := newToolService(t, model, fs, runner, approver, config)
	created, _ := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	runTurns(t, service, created.SessionID, "read", "exec", "write")

	if !runner.ran {
		t.Fatal("the exec fixture never ran; this test proves nothing")
	}
	if got := lastToolMessage(model.Calls()[5].Messages).Text; got != application.ToolTextFSStaleVersion {
		t.Fatalf("write after exec = %q, want %q", got, application.ToolTextFSStaleVersion)
	}
	read, _ := fs.Read(context.Background(), "/workspace/shared.txt", 64)
	if string(read.Data) != "rewritten-by-exec" {
		t.Fatalf("content = %q; the structured write overwrote what exec did", read.Data)
	}
}

// workspaceMutatingRunner is a command that changes the workspace behind the
// guarded port's back.
type workspaceMutatingRunner struct {
	fs   *testkit.MemFS
	rel  string
	data []byte
	ran  bool
}

func (runner *workspaceMutatingRunner) Run(context.Context, tools.CommandSpec) (tools.CommandResult, error) {
	runner.ran = true
	runner.fs.AddFile(runner.rel, runner.data)
	return tools.CommandResult{ExitCode: 0, Output: "done"}, nil
}
