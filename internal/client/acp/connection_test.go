package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// fakeAgent drives the other end of a Connection under test directly at
// the raw NDJSON level — not through a second Connection — so a test can
// script exactly what the "agent" sends and when, including deliberately
// never responding.
type fakeAgent struct {
	t       *testing.T
	writer  *frameWriter
	scanner *bufio.Scanner
}

func newFakeAgent(t *testing.T, r io.Reader, w io.Writer) *fakeAgent {
	t.Helper()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
	return &fakeAgent{t: t, writer: newFrameWriter(w), scanner: scanner}
}

// next reads and parses the next line the client under test sent.
func (a *fakeAgent) next() message {
	a.t.Helper()
	if !a.scanner.Scan() {
		a.t.Fatalf("fakeAgent: no more lines from client (scanner err: %v)", a.scanner.Err())
	}
	var m message
	if err := json.Unmarshal(a.scanner.Bytes(), &m); err != nil {
		a.t.Fatalf("fakeAgent: unmarshal %q: %v", a.scanner.Bytes(), err)
	}
	return m
}

func (a *fakeAgent) respond(id json.RawMessage, result json.RawMessage) {
	a.t.Helper()
	if err := a.writer.writeMessage(message{JSONRPC: jsonRPCVersion, ID: id, Result: result}); err != nil {
		a.t.Fatalf("fakeAgent: respond: %v", err)
	}
}

func (a *fakeAgent) notify(method string, params json.RawMessage) {
	a.t.Helper()
	if err := a.writer.writeMessage(message{JSONRPC: jsonRPCVersion, Method: method, Params: params}); err != nil {
		a.t.Fatalf("fakeAgent: notify: %v", err)
	}
}

func (a *fakeAgent) call(id json.RawMessage, method string, params json.RawMessage) {
	a.t.Helper()
	if err := a.writer.writeMessage(message{JSONRPC: jsonRPCVersion, ID: id, Method: method, Params: params}); err != nil {
		a.t.Fatalf("fakeAgent: call: %v", err)
	}
}

// recordingHandler records every notification and answers every
// permission request with a scripted, channel-delivered decision so a
// test can control exactly when (or whether) it answers.
type recordingHandler struct {
	updates  chan json.RawMessage
	decision chan json.RawMessage // send one value per HandleRequestPermission call to answer it
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{
		updates:  make(chan json.RawMessage, 16),
		decision: make(chan json.RawMessage, 16),
	}
}

func (h *recordingHandler) HandleSessionUpdate(params json.RawMessage) {
	h.updates <- params
}

func (h *recordingHandler) HandleRequestPermission(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	select {
	case result := <-h.decision:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newTestConnection(t *testing.T, handler Handler) (*Connection, *fakeAgent) {
	t.Helper()
	clientReadsFromAgent, agentWritesToClient := io.Pipe()
	agentReadsFromClient, clientWritesToAgent := io.Pipe()
	conn := NewConnection(clientReadsFromAgent, clientWritesToAgent, handler)
	agent := newFakeAgent(t, agentReadsFromClient, agentWritesToClient)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, agent
}

func TestConnectionCallRoundTrip(t *testing.T) {
	conn, agent := newTestConnection(t, newRecordingHandler())

	done := make(chan struct {
		result json.RawMessage
		err    error
	}, 1)
	go func() {
		result, err := conn.Call(context.Background(), "initialize", map[string]int{"protocolVersion": 1})
		done <- struct {
			result json.RawMessage
			err    error
		}{result, err}
	}()

	req := agent.next()
	if req.Method != "initialize" || len(req.ID) == 0 {
		t.Fatalf("agent received = %#v, want an initialize request with an id", req)
	}
	agent.respond(req.ID, json.RawMessage(`{"protocolVersion":1}`))

	got := <-done
	if got.err != nil {
		t.Fatalf("Call() err = %v", got.err)
	}
	if string(got.result) != `{"protocolVersion":1}` {
		t.Fatalf("Call() result = %s, want the agent's response", got.result)
	}
}

func TestConnectionNotifyDoesNotWaitForAResponse(t *testing.T) {
	conn, agent := newTestConnection(t, newRecordingHandler())
	// io.Pipe is a fully synchronous rendezvous: frameWriter's Write call
	// blocks until something reads it, so the agent's read must already be
	// in flight concurrently with Notify, not started only afterward.
	got := make(chan message, 1)
	go func() { got <- agent.next() }()

	if err := conn.Notify("session/cancel", map[string]string{"sessionId": "s1"}); err != nil {
		t.Fatalf("Notify() err = %v", err)
	}
	select {
	case m := <-got:
		if !m.isNotification() || m.Method != "session/cancel" {
			t.Fatalf("agent received = %#v, want a session/cancel notification", m)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the agent to receive the notification")
	}
}

func TestConnectionDeliversSessionUpdateToHandler(t *testing.T) {
	handler := newRecordingHandler()
	_, agent := newTestConnection(t, handler)
	agent.notify(methodSessionUpdate, json.RawMessage(`{"sessionUpdate":"agent_message_chunk"}`))

	select {
	case params := <-handler.updates:
		if string(params) != `{"sessionUpdate":"agent_message_chunk"}` {
			t.Fatalf("HandleSessionUpdate params = %s, want the notification's params", params)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for HandleSessionUpdate")
	}
}

func TestConnectionAnswersRequestPermissionThroughHandler(t *testing.T) {
	handler := newRecordingHandler()
	_, agent := newTestConnection(t, handler)

	agent.call(json.RawMessage(`"perm-1"`), methodRequestPermission, json.RawMessage(`{"toolCallId":"tc1"}`))
	handler.decision <- json.RawMessage(`{"outcome":{"outcome":"selected","optionId":"allow-once"}}`)

	resp := agent.next()
	if resp.isRequest() || resp.isNotification() {
		t.Fatalf("agent received = %#v, want a response to its own request", resp)
	}
	if string(resp.ID) != `"perm-1"` {
		t.Fatalf("response id = %s, want to match the request id", resp.ID)
	}
	if string(resp.Result) != `{"outcome":{"outcome":"selected","optionId":"allow-once"}}` {
		t.Fatalf("response result = %s, want the handler's decision", resp.Result)
	}
}

func TestConnectionAnswersAnUnknownInboundMethodWithMethodNotFound(t *testing.T) {
	_, agent := newTestConnection(t, newRecordingHandler())
	agent.call(json.RawMessage(`"x1"`), "fs/read_text_file", json.RawMessage(`{}`))

	resp := agent.next()
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("response = %#v, want a method-not-found error", resp)
	}
}

func TestConnectionCallReturnsPromptlyOnContextCancellation(t *testing.T) {
	conn, agent := newTestConnection(t, newRecordingHandler())
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := conn.Call(ctx, "session/prompt", map[string]string{})
		done <- err
	}()
	agent.next() // drain the request so fakeAgent's writer isn't left blocked mid-test
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Call() err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call() did not return promptly after context cancellation")
	}
}

func TestConnectionCloseUnblocksAPendingCall(t *testing.T) {
	conn, agent := newTestConnection(t, newRecordingHandler())

	done := make(chan error, 1)
	go func() {
		_, err := conn.Call(context.Background(), "session/prompt", map[string]string{})
		done <- err
	}()
	agent.next() // the agent receives the call but deliberately never answers it

	if err := conn.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Call() err = nil after Close(), want an error")
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not unblock the pending Call")
	}
}

func TestConnectionCloseIsIdempotent(t *testing.T) {
	conn, _ := newTestConnection(t, newRecordingHandler())
	if err := conn.Close(); err != nil {
		t.Fatalf("first Close() err = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close() err = %v, want nil (idempotent)", err)
	}
}
