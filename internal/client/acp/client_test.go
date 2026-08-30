package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler Handler) (*Client, *fakeAgent) {
	t.Helper()
	clientReadsFromAgent, agentWritesToClient := io.Pipe()
	agentReadsFromClient, clientWritesToAgent := io.Pipe()
	client, err := NewClient(clientReadsFromAgent, clientWritesToAgent, handler)
	if err != nil {
		t.Fatal(err)
	}
	agent := newFakeAgent(t, agentReadsFromClient, agentWritesToClient)
	t.Cleanup(func() { _ = client.Close() })
	return client, agent
}

func TestNewClientRejectsANilHandler(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	if _, err := NewClient(r, w, nil); err == nil {
		t.Fatal("NewClient(nil handler) err = nil, want an error")
	}
}

func TestClientInitializeDeclinesFsAndTerminalCapabilities(t *testing.T) {
	client, agent := newTestClient(t, newRecordingHandler())

	done := make(chan struct {
		info AgentInfo
		caps Capabilities
		err  error
	}, 1)
	go func() {
		info, caps, err := client.Initialize(context.Background())
		done <- struct {
			info AgentInfo
			caps Capabilities
			err  error
		}{info, caps, err}
	}()

	req := agent.next()
	if req.Method != "initialize" {
		t.Fatalf("agent received method %q, want initialize", req.Method)
	}
	if got := string(req.Params); got != `{"protocolVersion":1,"clientCapabilities":{}}` {
		t.Fatalf("initialize params = %s, want an empty clientCapabilities with no fs/terminal keys", got)
	}
	agent.respond(req.ID, json.RawMessage(`{"protocolVersion":1,"agentCapabilities":{"loadSession":true},"agentInfo":{"name":"och","version":"0.0.0"}}`))

	got := <-done
	if got.err != nil {
		t.Fatalf("Initialize() err = %v", got.err)
	}
	if got.info.Name != "och" || !got.caps.LoadSession {
		t.Fatalf("Initialize() = %#v, %#v, want the agent's info and capabilities", got.info, got.caps)
	}
}

func TestClientLoadSessionDeliversReplayedUpdatesBeforeReturning(t *testing.T) {
	handler := newRecordingHandler()
	client, agent := newTestClient(t, handler)

	done := make(chan error, 1)
	go func() { done <- client.LoadSession(context.Background(), "s1", "/ws") }()

	req := agent.next()
	if req.Method != "session/load" {
		t.Fatalf("agent received method %q, want session/load", req.Method)
	}
	// The agent replays history as session/update notifications before
	// responding to session/load itself.
	agent.notify(methodSessionUpdate, json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk"}}`))
	agent.respond(req.ID, json.RawMessage(`{}`))

	if err := <-done; err != nil {
		t.Fatalf("LoadSession() err = %v", err)
	}
	select {
	case <-handler.updates:
	default:
		t.Fatal("LoadSession() returned without the replayed session/update reaching the Handler")
	}
}

func TestClientPromptReturnsTheStopReason(t *testing.T) {
	client, agent := newTestClient(t, newRecordingHandler())

	done := make(chan struct {
		stopReason string
		err        error
	}, 1)
	go func() {
		stopReason, err := client.Prompt(context.Background(), "s1", "hello")
		done <- struct {
			stopReason string
			err        error
		}{stopReason, err}
	}()

	req := agent.next()
	if req.Method != "session/prompt" {
		t.Fatalf("agent received method %q, want session/prompt", req.Method)
	}
	agent.respond(req.ID, json.RawMessage(`{"stopReason":"end_turn"}`))

	got := <-done
	if got.err != nil {
		t.Fatalf("Prompt() err = %v", got.err)
	}
	if got.stopReason != "end_turn" {
		t.Fatalf("Prompt() stopReason = %q, want end_turn", got.stopReason)
	}
}

func TestClientCancelDuringAnInFlightPromptUnblocksItWithCancelled(t *testing.T) {
	client, agent := newTestClient(t, newRecordingHandler())

	done := make(chan struct {
		stopReason string
		err        error
	}, 1)
	go func() {
		stopReason, err := client.Prompt(context.Background(), "s1", "hello")
		done <- struct {
			stopReason string
			err        error
		}{stopReason, err}
	}()

	promptReq := agent.next()
	// io.Pipe is a fully synchronous rendezvous: Cancel's underlying write
	// blocks until the agent's read is already in flight, so read
	// concurrently with the call rather than only afterward.
	cancelReqCh := make(chan message, 1)
	go func() { cancelReqCh <- agent.next() }()
	if err := client.Cancel(context.Background(), "s1"); err != nil {
		t.Fatalf("Cancel() err = %v", err)
	}
	var cancelReq message
	select {
	case cancelReq = <-cancelReqCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the agent to receive session/cancel")
	}
	if !cancelReq.isNotification() || cancelReq.Method != "session/cancel" {
		t.Fatalf("agent received = %#v, want a session/cancel notification", cancelReq)
	}
	// The agent settles the in-flight prompt's own response with the
	// cancelled stop reason; Cancel itself never resolves Prompt directly.
	agent.respond(promptReq.ID, json.RawMessage(`{"stopReason":"cancelled"}`))

	got := <-done
	if got.err != nil {
		t.Fatalf("Prompt() err = %v", got.err)
	}
	if got.stopReason != "cancelled" {
		t.Fatalf("Prompt() stopReason = %q, want cancelled", got.stopReason)
	}
}

func TestClientPromptReturnsPromptlyOnContextCancellation(t *testing.T) {
	client, agent := newTestClient(t, newRecordingHandler())
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := client.Prompt(ctx, "s1", "hello")
		done <- err
	}()
	agent.next()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Prompt() err = %v, want it to wrap context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Prompt() did not return promptly after context cancellation")
	}
}
