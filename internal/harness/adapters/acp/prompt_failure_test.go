package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestCommittedPromptFailureCannotBecomeSuccessfulCancellation(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	entered := make(chan struct{})
	fake.run = func(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
		close(entered)
		select {
		case <-ctx.Done():
		case <-time.After(acpTestRendezvousTimeout):
			return application.RunTurnResult{}, errors.New("cancel not delivered")
		}
		return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusFailed, TerminalCommitted: true}, &application.Error{Category: application.CategoryInternal, Code: application.CodeExecTeardownUnproven, TerminalCommitted: true, Cause: errors.Join(ctx.Err(), tools.ErrCommandTeardownUnproven)}
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(t.Context(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		_ = awaitError(t, done, "server exit")
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	id := created["result"].(map[string]any)["sessionId"].(string)
	params, _ := json.Marshal(map[string]any{"sessionId": id, "prompt": []map[string]string{{"text": "fixture"}}})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":`+string(params)+`}`)
	awaitSignal(t, entered, "prompt entry")
	cancelParams, _ := json.Marshal(map[string]string{"sessionId": id})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","method":"session/cancel","params":`+string(cancelParams)+`}`)
	response := readJSON(t, clientIn)
	if response["id"] != float64(3) || response["error"] == nil || response["result"] != nil {
		t.Fatalf("durable failed prompt was relabeled as cancellation: %#v", response)
	}
	rpcErr := response["error"].(map[string]any)
	if rpcErr["code"] != float64(codeInternalError) || rpcErr["message"] != promptFailedMessage {
		t.Fatalf("unsafe failure details: %#v", rpcErr)
	}
}

func TestCommittedPromptTerminalStatusWinsOverLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		status    domain.TurnStatus
		committed bool
		reason    string
		ok        bool
	}{
		{domain.TurnStatusCompleted, true, stopReasonEndTurn, true},
		{domain.TurnStatusFailed, true, "", false},
		{domain.TurnStatusInterrupted, true, stopReasonCancelled, true},
		{domain.TurnStatusRunning, false, stopReasonCancelled, true},
	} {
		reason, ok := stopReason(application.RunTurnResult{Status: test.status, TerminalCommitted: test.committed}, context.Canceled, ctx)
		if reason != test.reason || ok != test.ok {
			t.Fatalf("status=%s committed=%v reason=%s ok=%v", test.status, test.committed, reason, ok)
		}
	}
}
