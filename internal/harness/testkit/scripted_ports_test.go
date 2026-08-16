package testkit_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestScriptedRunnerRecordsAndExhausts(t *testing.T) {
	want := tools.CommandResult{ExitCode: 0, Output: "ok\n"}
	runner := testkit.NewScriptedRunner(testkit.ScriptedRun{Result: want})
	spec := tools.CommandSpec{Argv: []string{"go", "test"}, Cwd: "/workspace", Timeout: time.Second, MaxBytes: 64}
	got, err := runner.Run(context.Background(), spec)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Run() = %#v, %v", got, err)
	}
	spec.Argv[0] = "mutated"
	calls := runner.Calls()
	if !reflect.DeepEqual(calls[0].Argv, []string{"go", "test"}) {
		t.Fatalf("Calls() lost defensive argv copy: %#v", calls[0].Argv)
	}
	if _, err := runner.Run(context.Background(), spec); err == nil {
		t.Fatal("expected exhausted script")
	}
}

func TestScriptedRunnerCancel(t *testing.T) {
	runner := testkit.NewScriptedRunner(testkit.ScriptedRun{WaitForCancel: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, tools.CommandSpec{Argv: []string{"sleep"}})
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() cancel error = %v", err)
	}
}

func TestScriptedApproverAndDenyApprover(t *testing.T) {
	approver := testkit.NewScriptedApprover(
		testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: true}},
		testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: false}},
	)
	req := tools.ApprovalRequest{SessionID: "session-1", TurnID: "turn-1", ApprovalID: "approval-1", Name: "write_file", CallID: "call-1", Reason: "write_requires_approval"}
	got, err := approver.Decide(context.Background(), req)
	if err != nil || !got.Granted {
		t.Fatalf("first Decide() = %#v, %v", got, err)
	}
	got, err = approver.Decide(context.Background(), req)
	if err != nil || got.Granted {
		t.Fatalf("second Decide() = %#v, %v", got, err)
	}
	if len(approver.Calls()) != 2 {
		t.Fatalf("Calls() = %#v", approver.Calls())
	}
	denied, err := tools.DenyApprover{}.Decide(context.Background(), req)
	if err != nil || denied.Granted {
		t.Fatalf("DenyApprover = %#v, %v", denied, err)
	}
}
