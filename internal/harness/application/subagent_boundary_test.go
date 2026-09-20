package application_test

import (
	"context"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// The delegation contract says the child dispatch guard rejects "hidden
// write, exec, MCP, and recursive calls". Until these two tests, only the
// write half was proven, and both remaining halves turned out to be
// unguarded by any test at all:
//
//   - Deleting the `owned.state.Parent != nil` recursion check from
//     invokeDelegateTask broke nothing in ./internal/harness/... or ./cmd/...
//   - Adding tools.NameExec to childDispatchAllowed broke nothing either:
//     TestDelegateTaskCreatesReadOnlyDurableChild asserts the child's
//     *schema* is exactly read_file/list_dir, so a dispatch-only widening
//     never reaches it.
//
// What each new test is worth, stated precisely rather than as "both
// mutations now fail":
//
//   - The exec test is independently load-bearing. Under the dispatch-only
//     widening it fails, and the failure is informative: exec then falls
//     through to approval_denied rather than the capability denial, so the
//     test asserts the specific code instead of merely "something failed".
//   - The recursion test proves the combined property and not either
//     barrier alone. It stays green when either the dispatch allowlist or
//     the Parent check is removed, and fails only when both are. That is
//     because delegate_task is already outside childDispatchAllowed, which
//     makes the Parent check unreachable in production today -- real
//     defence in depth, but not two separately provable barriers. No test
//     here should be read as isolating the inner check.

// TestChildSessionRejectsExecAtDispatch is the exec half of the dispatch
// claim. The child is offered exec directly by the model, bypassing the
// schema projection exactly as a forged or stale tool call would.
func TestChildSessionRejectsExecAtDispatch(t *testing.T) {
	workspaceFS := testkit.NewMemFS("/workspace")
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-exec", Name: tools.NameExec, Arguments: `{"argv":["sh","-c","printf ran > escaped.txt"]}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "exec refused"}, {Type: engine.StreamEventCompleted}},
	)
	config := application.DefaultConfig()
	config.Subagents.Enabled = true
	service, store := newToolService(t, model, workspaceFS, nil, nil, config)
	parent, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.CreateSession(context.Background(), application.CreateSessionRequest{
		WorkspaceRoot: "/workspace",
		Parent:        &domain.SessionParent{SessionID: parent.SessionID, TurnID: "parent-turn", ItemID: "parent-item", CallID: "parent-call"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: child.SessionID, RequestID: "request-child-exec", Input: "try running a command", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatal(err)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, child.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	failed := lastToolFailed(records)
	if failed.Code != application.CodeSubagentCapabilityDenied || failed.Message != application.ToolTextSubagentCapabilityDenied {
		t.Fatalf("tool failure = %#v, want the capability denial the contract promises for exec", failed)
	}
}

// TestChildSessionCannotDelegateAgain is the recursion half. A child that
// could delegate would spawn an unbounded tree of Sessions, each with its
// own timeout, from one parent tool call the operator approved once.
func TestChildSessionCannotDelegateAgain(t *testing.T) {
	workspaceFS := testkit.NewMemFS("/workspace")
	model := newSequenceModel(
		[]engine.StreamEvent{
			{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{ID: "call-nested", Name: tools.NameDelegateTask, Arguments: `{"task":"delegate from inside a child"}`}},
			{Type: engine.StreamEventCompleted},
		},
		[]engine.StreamEvent{{Type: engine.StreamEventTextDelta, Text: "nested delegation refused"}, {Type: engine.StreamEventCompleted}},
	)
	config := application.DefaultConfig()
	config.Subagents.Enabled = true
	service, store := newToolService(t, model, workspaceFS, nil, nil, config)
	parent, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.CreateSession(context.Background(), application.CreateSessionRequest{
		WorkspaceRoot: "/workspace",
		Parent:        &domain.SessionParent{SessionID: parent.SessionID, TurnID: "parent-turn", ItemID: "parent-item", CallID: "parent-call"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: child.SessionID, RequestID: "request-child-nested", Input: "try delegating again", Sink: &testkit.RecordingSink{},
	}); err != nil {
		t.Fatal(err)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, child.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	failed := lastToolFailed(records)
	if failed.Code != application.CodeSubagentCapabilityDenied || failed.Message != application.ToolTextSubagentCapabilityDenied {
		t.Fatalf("tool failure = %#v, want the capability denial that stops a delegation tree", failed)
	}
}
