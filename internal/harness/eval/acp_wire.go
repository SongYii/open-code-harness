package eval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// acpConnection is this package's own minimal ACP v1 client-role
// connection. It deliberately does not depend on internal/client/acp,
// which design's own ACP-native-client boundary (docs/superpowers/specs/
// 2026-08-30-acp-native-client-design.md §3) keeps permanently isolated
// from internal/harness/ in both directions — the architecture guard
// (TestClientPackagesAreIsolatedFromInternalHarness) enforces exactly
// this. It owns its own small NDJSON framing copy rather than share one,
// matching the precedent internal/client/acp's own wire.go already set
// for the identical reason relative to internal/harness/adapters/acp: the
// framing contract is small enough to own independently on every side of
// the wire that needs it.
//
// This connection implements only what RunACPAttempt needs (design §16's
// own supervision scope): initialize, session/new, session/prompt, and
// dispatching an incoming session/request_permission to a caller-supplied
// handler. session/update notifications are received and discarded —
// canonical evidence comes from the transcript/audit after shutdown, not
// live updates, the same principle discardSink applies for the in-process
// executor.
type acpConnection struct {
	writer  io.Writer
	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int
	waiters map[string]chan acpMessage
	closing bool

	handlePermission func(ctx context.Context, params json.RawMessage) (json.RawMessage, error)

	reader   io.Closer
	readDone chan struct{}
}

// acpMessage is the one JSON-RPC 2.0 envelope covering a request, a
// response, and a notification — which fields are set distinguishes the
// three, mirroring internal/client/acp's own wire.go shape exactly (same
// wire contract, independently owned copy).
type acpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *acpRPCError    `json:"error,omitempty"`
}

type acpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (err *acpRPCError) Error() string { return err.Message }

const acpMaxFrameBytes = 8 << 20

var errACPConnectionClosed = errors.New("eval: acp connection closed")

// newACPConnection starts the read loop immediately: r is the child's
// stdout, w is the child's stdin, handlePermission answers every incoming
// session/request_permission call.
func newACPConnection(r io.ReadCloser, w io.Writer, handlePermission func(context.Context, json.RawMessage) (json.RawMessage, error)) *acpConnection {
	conn := &acpConnection{
		writer:           w,
		waiters:          make(map[string]chan acpMessage),
		handlePermission: handlePermission,
		reader:           r,
		readDone:         make(chan struct{}),
	}
	go conn.readLoop(r)
	return conn
}

func (conn *acpConnection) readLoop(r io.Reader) {
	defer close(conn.readDone)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), acpMaxFrameBytes)
	var decodeErr error
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var m acpMessage
		if err := json.Unmarshal(line, &m); err != nil {
			decodeErr = fmt.Errorf("eval: acp: parse error: %w", err)
			break
		}
		conn.dispatch(m)
	}
	if decodeErr == nil {
		decodeErr = scanner.Err()
	}

	conn.mu.Lock()
	waiters := conn.waiters
	conn.waiters = nil
	conn.mu.Unlock()

	failure := errACPConnectionClosed
	if decodeErr != nil {
		failure = fmt.Errorf("eval: acp: %w", decodeErr)
	}
	for _, ch := range waiters {
		ch <- acpMessage{Error: &acpRPCError{Message: failure.Error()}}
		close(ch)
	}
}

// dispatch routes one decoded frame: a response completes a waiting Call;
// a session/request_permission request gets answered from a fresh
// goroutine (so a slow handler never blocks the read loop from receiving
// further frames); any other notification (session/update) is discarded.
func (conn *acpConnection) dispatch(m acpMessage) {
	if m.Method == "" && len(m.ID) > 0 {
		conn.mu.Lock()
		ch, ok := conn.waiters[string(m.ID)]
		if ok {
			delete(conn.waiters, string(m.ID))
		}
		conn.mu.Unlock()
		if ok {
			ch <- m
			close(ch)
		}
		return
	}
	if m.Method == "session/request_permission" && len(m.ID) > 0 {
		go conn.answerPermissionRequest(m)
		return
	}
	// Every other notification (session/update, and any request this
	// connection does not implement) is discarded — see the type's own
	// doc comment.
}

func (conn *acpConnection) answerPermissionRequest(m acpMessage) {
	result, err := conn.handlePermission(context.Background(), m.Params)
	if err != nil {
		_ = conn.writeMessage(acpMessage{JSONRPC: "2.0", ID: m.ID, Error: &acpRPCError{Code: -32603, Message: err.Error()}})
		return
	}
	_ = conn.writeMessage(acpMessage{JSONRPC: "2.0", ID: m.ID, Result: result})
}

// call sends one request and blocks for its response, or until ctx is
// done.
func (conn *acpConnection) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch, forget, err := conn.callAsync(method, params)
	if err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

// callAsync reserves a request ID and writes the request frame
// synchronously, before returning — the caller observes the write
// complete (or fail) before doing anything else, so a second call issued
// immediately afterward (a cancel notification racing a just-started
// prompt, for instance) has a deterministic wire order relative to this
// one. It returns a channel that receives the eventual response exactly
// once, and a forget function the caller must invoke if it stops waiting
// without draining that channel (a context timeout, e.g.), so the waiter
// entry does not leak.
func (conn *acpConnection) callAsync(method string, params any) (<-chan acpMessage, func(), error) {
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, nil, fmt.Errorf("eval: acp: encode %s params: %w", method, err)
	}
	conn.mu.Lock()
	if conn.waiters == nil {
		conn.mu.Unlock()
		return nil, nil, errACPConnectionClosed
	}
	conn.nextID++
	id := json.RawMessage(strconv.Itoa(conn.nextID))
	ch := make(chan acpMessage, 1)
	conn.waiters[string(id)] = ch
	conn.mu.Unlock()

	if err := conn.writeMessage(acpMessage{JSONRPC: "2.0", ID: id, Method: method, Params: payload}); err != nil {
		conn.forgetWaiter(string(id))
		return nil, nil, err
	}
	return ch, func() { conn.forgetWaiter(string(id)) }, nil
}

// notify sends an outbound JSON-RPC notification (no id, no response to
// wait for) — session/cancel's own wire shape (design §7/§16).
func (conn *acpConnection) notify(method string, params any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("eval: acp: encode %s params: %w", method, err)
	}
	return conn.writeMessage(acpMessage{JSONRPC: "2.0", Method: method, Params: payload})
}

func (conn *acpConnection) forgetWaiter(id string) {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.waiters != nil {
		delete(conn.waiters, id)
	}
}

func (conn *acpConnection) writeMessage(m acpMessage) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("eval: acp: encode frame: %w", err)
	}
	if len(payload)+1 > acpMaxFrameBytes {
		return fmt.Errorf("eval: acp: encoded frame exceeds %d bytes", acpMaxFrameBytes)
	}
	conn.writeMu.Lock()
	defer conn.writeMu.Unlock()
	_, err = conn.writer.Write(append(payload, '\n'))
	if err != nil {
		return fmt.Errorf("eval: acp: write frame: %w", err)
	}
	return nil
}

// close stops the read loop (by closing the reader out from under it, so
// its blocked Read returns) and waits for it to finish. Idempotent.
func (conn *acpConnection) close() error {
	conn.mu.Lock()
	alreadyClosing := conn.closing
	conn.closing = true
	conn.mu.Unlock()

	var err error
	if !alreadyClosing {
		err = conn.reader.Close()
	}
	<-conn.readDone
	return err
}

const acpProtocolVersion = 1

type acpInitializeParams struct {
	ProtocolVersion    int                   `json:"protocolVersion"`
	ClientCapabilities acpClientCapabilities `json:"clientCapabilities"`
}

type acpClientCapabilities struct{}

type acpInitializeResult struct {
	ProtocolVersion   int                  `json:"protocolVersion"`
	AgentCapabilities acpAgentCapabilities `json:"agentCapabilities"`
	AgentInfo         acpAgentInfo         `json:"agentInfo"`
}

type acpAgentCapabilities struct {
	LoadSession bool `json:"loadSession"`
}

type acpAgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// initialize performs the ACP handshake and returns the agent's own
// reported identity (design §11's ACPSubprocessIdentity.AgentName/
// AgentVersion).
func (conn *acpConnection) initialize(ctx context.Context) (acpAgentInfo, error) {
	raw, err := conn.call(ctx, "initialize", acpInitializeParams{ProtocolVersion: acpProtocolVersion})
	if err != nil {
		return acpAgentInfo{}, fmt.Errorf("eval: acp: initialize: %w", err)
	}
	var result acpInitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return acpAgentInfo{}, fmt.Errorf("eval: acp: initialize: decode result: %w", err)
	}
	return result.AgentInfo, nil
}

type acpSessionNewParams struct {
	Cwd string `json:"cwd"`
}

type acpSessionNewResult struct {
	SessionID string `json:"sessionId"`
}

func (conn *acpConnection) newSession(ctx context.Context, cwd string) (string, error) {
	raw, err := conn.call(ctx, "session/new", acpSessionNewParams{Cwd: cwd})
	if err != nil {
		return "", fmt.Errorf("eval: acp: session/new: %w", err)
	}
	var result acpSessionNewResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("eval: acp: session/new: decode result: %w", err)
	}
	return result.SessionID, nil
}

type acpSessionLoadParams struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

// loadSession resumes an existing session at cwd — design §16's restart
// modes load the same session on a successor Assembly only after the
// prior writer's reap is proven.
func (conn *acpConnection) loadSession(ctx context.Context, sessionID, cwd string) error {
	if _, err := conn.call(ctx, "session/load", acpSessionLoadParams{SessionID: sessionID, Cwd: cwd}); err != nil {
		return fmt.Errorf("eval: acp: session/load: %w", err)
	}
	return nil
}

type acpSessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

// cancel sends session/cancel as a fire-and-forget notification, matching
// this project's own agent-side cancellation semantics: the in-flight
// prompt call observes the resulting "cancelled" stop reason on its own
// pending response, not a separate signal from cancel.
func (conn *acpConnection) cancel(sessionID string) error {
	return conn.notify("session/cancel", acpSessionCancelParams{SessionID: sessionID})
}

type acpPromptParams struct {
	SessionID string           `json:"sessionId"`
	Prompt    []acpPromptBlock `json:"prompt"`
}

type acpPromptBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type acpPromptResult struct {
	StopReason string `json:"stopReason"`
}

// prompt sends one prompt and blocks for its terminal stop reason, or
// until ctx is done.
func (conn *acpConnection) prompt(ctx context.Context, sessionID, text string) (string, error) {
	raw, err := conn.call(ctx, "session/prompt", acpPromptParams{
		SessionID: sessionID,
		Prompt:    []acpPromptBlock{{Type: "text", Text: text}},
	})
	if err != nil {
		return "", fmt.Errorf("eval: acp: session/prompt: %w", err)
	}
	var result acpPromptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("eval: acp: session/prompt: decode result: %w", err)
	}
	return result.StopReason, nil
}

// acpPromptOutcome is one prompt call's resolved (stopReason, err) pair,
// delivered over acpPendingPrompt.done — the ACP analogue of inprocess.go's
// promptResult, used the same way: a prompt action a later cancel action
// targets runs in its own goroutine so the main action loop can reach that
// cancel action without waiting for this call to return first.
type acpPromptOutcome struct {
	stopReason string
	err        error
}

type acpPendingPrompt struct {
	done chan acpPromptOutcome
}

// promptAsync sends one session/prompt request — synchronously, so it has
// completed the actual write to the child before this function returns —
// and hands back a handle to its eventual result, waited for in a separate
// goroutine. Writing synchronously matters: a cancel action immediately
// following the prompt action that started this call (escalateCancel's own
// first rung) must never race this request's own frame onto the wire.
// Never call prompt and promptAsync concurrently against the same
// sessionID; ACP (like the in-process executor) allows only one Turn in
// flight per session.
func (conn *acpConnection) promptAsync(ctx context.Context, sessionID, text string) (*acpPendingPrompt, error) {
	ch, forget, err := conn.callAsync("session/prompt", acpPromptParams{
		SessionID: sessionID,
		Prompt:    []acpPromptBlock{{Type: "text", Text: text}},
	})
	if err != nil {
		return nil, fmt.Errorf("eval: acp: session/prompt: %w", err)
	}
	pending := &acpPendingPrompt{done: make(chan acpPromptOutcome, 1)}
	go func() {
		select {
		case resp := <-ch:
			if resp.Error != nil {
				pending.done <- acpPromptOutcome{err: fmt.Errorf("eval: acp: session/prompt: %w", resp.Error)}
				return
			}
			var result acpPromptResult
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				pending.done <- acpPromptOutcome{err: fmt.Errorf("eval: acp: session/prompt: decode result: %w", err)}
				return
			}
			pending.done <- acpPromptOutcome{stopReason: result.StopReason}
		case <-ctx.Done():
			forget()
			pending.done <- acpPromptOutcome{err: ctx.Err()}
		}
	}()
	return pending, nil
}

// acpPermissionRequestParams is this project's own agent's real
// session/request_permission wire shape
// (internal/harness/adapters/acp/protocol.go's permissionParams, read
// directly), not the full ACP specification's shape in the abstract —
// the exact same convention internal/client/acp/permission.go documents
// for its own independent copy of this shape.
type acpPermissionRequestParams struct {
	SessionID string                `json:"sessionId"`
	ToolCall  acpPermissionToolCall `json:"toolCall"`
	Options   []acpPermissionOption `json:"options"`
}

// acpPermissionToolCall's Title field carries the tool name itself (e.g.
// "write_file"), not a human-readable title — internal/harness/adapters/acp/
// server.go's own fitPermission call sets Title: req.Name directly, verified
// by reading that source rather than assumed.
type acpPermissionToolCall struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
}

type acpPermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type acpPermissionResult struct {
	Outcome acpPermissionOutcome `json:"outcome"`
}

type acpPermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}
