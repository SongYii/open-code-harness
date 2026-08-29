package acp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// Sessions is the Application command surface the adapter translates onto.
type Sessions interface {
	CreateSession(context.Context, application.CreateSessionRequest) (application.CreateSessionResult, error)
	LoadSession(context.Context, domain.SessionID) (domain.Session, error)
	RunTurn(context.Context, application.RunTurnRequest) (application.RunTurnResult, error)
	ListSessions(context.Context, application.ListSessionsRequest) (application.ListSessionsResult, error)
	ResumeSession(context.Context, application.ResumeSessionRequest) (domain.Session, error)
	DeleteSession(context.Context, application.DeleteSessionRequest) error
}

// History is the EventStore read surface used by session/load replay.
type History interface {
	ReadStream(context.Context, application.ReadStreamRequest) (application.StreamPage, error)
}

// Config is one ACP agent serving one duplex.
type Config struct {
	Sessions  Sessions
	History   History
	Workspace string
	Approver  *tools.Slot
}

// Serve reads newline-delimited JSON-RPC from in and writes only ACP frames
// to out until in closes or ctx is cancelled.
func Serve(ctx context.Context, config Config, in io.Reader, out io.Writer) error {
	workspace, workspaceErr := application.CanonicalWorkspaceRoot(config.Workspace)
	if ctx == nil || config.Sessions == nil || out == nil || workspaceErr != nil {
		return fmt.Errorf("acp: invalid configuration")
	}
	server := &server{
		ctx:       ctx,
		config:    config,
		out:       &frameWriter{writer: out},
		pending:   make(map[string]chan rpcRequest),
		sessions:  make(map[string]*sessionState),
		workspace: workspace,
	}
	if config.Approver != nil {
		config.Approver.Set(server)
		defer config.Approver.Set(tools.DenyApprover{})
	}
	return decodeFrames(in, func(message rpcRequest) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return server.dispatch(message)
	})
}

type server struct {
	ctx         context.Context
	config      Config
	out         *frameWriter
	mu          sync.Mutex
	initialized bool
	outgoing    uint64
	pending     map[string]chan rpcRequest
	sessions    map[string]*sessionState
	workspace   string
}

type wireSessionState uint8

const (
	wireIdle wireSessionState = iota
	wireRunning
	wireClosing
	wireDetached
	wireDeleting
)

type sessionState struct {
	state      wireSessionState
	cancel     context.CancelFunc
	promptDone chan struct{}
}

// admitBusy reports whether entry blocks a new prompt/resume/load admission:
// running, closing, and deleting are all in-flight wire operations.
func admitBusy(entry *sessionState) bool {
	return entry != nil && (entry.state == wireRunning || entry.state == wireClosing || entry.state == wireDeleting)
}

func (s *server) requestWorkspaceMatches(cwd string) bool {
	if cwd == "" {
		return true
	}
	workspace, err := application.CanonicalWorkspaceRoot(cwd)
	return err == nil && workspace == s.workspace
}

func (s *server) sessionWorkspaceMatches(root string) bool {
	workspace, err := application.CanonicalWorkspaceRoot(root)
	return err == nil && workspace == s.workspace
}

func (s *server) dispatch(message rpcRequest) error {
	if message.Error != nil && message.Method == "" {
		if len(message.ID) > 0 {
			s.deliver(message)
			return nil
		}
		return s.out.writeError(nil, message.Error.Code, message.Error.Message)
	}
	if message.Method == "" && len(message.ID) > 0 {
		s.deliver(message)
		return nil
	}
	if message.Method == "" {
		return s.out.writeError(message.ID, codeInvalidRequest, "invalid request")
	}
	if len(message.ID) == 0 {
		s.handleNotification(message)
		return nil
	}
	return s.handleRequest(message)
}

func (s *server) deliver(message rpcRequest) {
	s.mu.Lock()
	ch := s.pending[idKey(message.ID)]
	delete(s.pending, idKey(message.ID))
	s.mu.Unlock()
	if ch != nil {
		ch <- message
	}
}

func (s *server) handleNotification(message rpcRequest) {
	if message.Method != methodSessionCancel {
		return
	}
	var params sessionIDParams
	if json.Unmarshal(message.Params, &params) != nil {
		return
	}
	var cancel context.CancelFunc
	s.mu.Lock()
	if state := s.sessions[params.SessionID]; state != nil {
		cancel = state.cancel
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *server) handleRequest(message rpcRequest) error {
	switch message.Method {
	case methodInitialize:
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
		return s.out.writeResult(message.ID, initializeResult{
			ProtocolVersion: protocolVersion,
			AgentCapabilities: agentCapabilities{
				LoadSession:         true,
				SessionCapabilities: sessionCapabilities{},
			},
			AgentInfo:   agentInfo{Name: agentName, Version: agentVersion},
			AuthMethods: []struct{}{},
		})
	}
	s.mu.Lock()
	ready := s.initialized
	s.mu.Unlock()
	if !ready {
		return s.out.writeError(message.ID, codeInvalidRequest, "initialize required")
	}
	switch message.Method {
	case methodSessionNew:
		return s.sessionNew(message)
	case methodSessionLoad:
		return s.sessionLoad(message)
	case methodSessionPrompt:
		return s.sessionPrompt(message)
	case methodSessionList:
		return s.sessionList(message)
	case methodSessionResume:
		return s.sessionResume(message)
	case methodSessionClose:
		return s.sessionClose(message)
	case methodSessionDelete:
		return s.sessionDelete(message)
	default:
		return s.out.writeError(message.ID, codeMethodNotFound, "method not found")
	}
}

func (s *server) sessionNew(message rpcRequest) error {
	var params sessionNewParams
	if len(message.Params) > 0 && json.Unmarshal(message.Params, &params) != nil {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	if !s.requestWorkspaceMatches(params.Cwd) {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	created, err := s.config.Sessions.CreateSession(s.ctx, application.CreateSessionRequest{WorkspaceRoot: s.workspace})
	if err != nil {
		return s.out.writeError(message.ID, codeInternalError, promptFailedMessage)
	}
	s.mu.Lock()
	s.sessions[string(created.SessionID)] = &sessionState{state: wireIdle}
	s.mu.Unlock()
	return s.out.writeResult(message.ID, sessionIDParams{SessionID: string(created.SessionID)})
}

func (s *server) sessionLoad(message rpcRequest) error {
	var params sessionLoadParams
	if json.Unmarshal(message.Params, &params) != nil || params.SessionID == "" {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	if len(params.MCPServers) > 0 || len(params.AdditionalDirectories) > 0 {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	if !s.requestWorkspaceMatches(params.Cwd) {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	sessionID, err := domain.ParseSessionID(params.SessionID)
	if err != nil {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	s.mu.Lock()
	if admitBusy(s.sessions[params.SessionID]) {
		s.mu.Unlock()
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	s.mu.Unlock()
	session, err := s.config.Sessions.LoadSession(s.ctx, sessionID)
	if err != nil || !s.sessionWorkspaceMatches(session.WorkspaceRoot) {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	if s.config.History != nil {
		if err := s.replay(sessionID); err != nil {
			return s.out.writeError(message.ID, codeInternalError, promptFailedMessage)
		}
	}
	s.mu.Lock()
	if session.Status == domain.SessionStatusActive {
		s.sessions[params.SessionID] = &sessionState{state: wireIdle}
	} else {
		delete(s.sessions, params.SessionID)
	}
	s.mu.Unlock()
	return s.out.writeResult(message.ID, struct{}{})
}

func (s *server) replay(sessionID domain.SessionID) error {
	after := uint64(0)
	var head *uint64
	for {
		page, err := s.config.History.ReadStream(s.ctx, application.ReadStreamRequest{
			SessionID: sessionID, Limit: 256, AfterSequence: after, HeadVersion: head,
		})
		if err != nil {
			return err
		}
		if head == nil {
			pinned := page.HeadVersion
			head = &pinned
		}
		for _, record := range page.Records {
			if err := s.project(string(sessionID), record); err != nil {
				return err
			}
		}
		after = page.NextAfterSequence
		if page.End {
			return nil
		}
	}
}

func (s *server) project(sessionID string, record domain.RecordedEvent) error {
	for _, update := range ProjectRecordedEvent(sessionID, record) {
		if err := s.out.writeNotification(methodSessionUpdate, sessionUpdateParams{
			SessionID: sessionID,
			Update:    update,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) sessionPrompt(message rpcRequest) error {
	var params promptParams
	if json.Unmarshal(message.Params, &params) != nil || params.SessionID == "" {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	sessionID, err := domain.ParseSessionID(params.SessionID)
	if err != nil {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	input := concatenatePrompt(params.Prompt)
	if strings.TrimSpace(input) == "" {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	session, err := s.config.Sessions.LoadSession(s.ctx, sessionID)
	if err != nil || !s.sessionWorkspaceMatches(session.WorkspaceRoot) {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}

	s.mu.Lock()
	entry := s.sessions[params.SessionID]
	var state wireSessionState
	if entry != nil {
		state = entry.state
	}
	if entry == nil || state != wireIdle {
		s.mu.Unlock()
		if state == wireRunning {
			return s.out.writeError(message.ID, codeInvalidRequest, promptInFlightMessage)
		}
		return s.out.writeError(message.ID, codeInvalidRequest, sessionNotAttachedMessage)
	}
	promptCtx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	entry.state = wireRunning
	entry.cancel = cancel
	entry.promptDone = done
	s.mu.Unlock()

	go s.runPrompt(promptCtx, message.ID, sessionID, params.SessionID, input, done)
	return nil
}

func (s *server) runPrompt(ctx context.Context, id json.RawMessage, sessionID domain.SessionID, wireID, input string, done chan struct{}) {
	requestID, err := newRequestID()
	if err != nil {
		s.settlePrompt(wireID)
		_ = s.out.writeError(id, codeInternalError, promptFailedMessage)
		close(done)
		return
	}
	result, runErr := s.config.Sessions.RunTurn(ctx, application.RunTurnRequest{
		SessionID: sessionID,
		RequestID: requestID,
		Input:     input,
		Sink:      &updateSink{sessionID: wireID, out: s.out},
	})
	// The entry returns to idle before the terminal response is published, so
	// a client that reacts to that response by sending another prompt for the
	// same session never races an entry that still reads running. done closes
	// only after the response write, so a waiter blocked in sessionClose sees
	// the terminal frame settle first.
	s.settlePrompt(wireID)
	if reason, ok := stopReason(result, runErr, ctx); ok {
		_ = s.out.writeResult(id, promptResult{StopReason: reason})
	} else {
		_ = s.out.writeError(id, codeInternalError, promptFailedMessage)
	}
	close(done)
}

// settlePrompt returns a running entry to idle. A closing entry is left
// untouched: sessionClose owns the closing -> detached transition once it
// wakes from done.
func (s *server) settlePrompt(wireID string) {
	s.mu.Lock()
	if entry := s.sessions[wireID]; entry != nil && entry.state == wireRunning {
		entry.state = wireIdle
		entry.cancel = nil
		entry.promptDone = nil
	}
	s.mu.Unlock()
}

func (s *server) sessionList(message rpcRequest) error {
	var params sessionListParams
	if len(message.Params) > 0 && json.Unmarshal(message.Params, &params) != nil {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	if !s.requestWorkspaceMatches(params.Cwd) {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	result, err := s.config.Sessions.ListSessions(s.ctx, application.ListSessionsRequest{
		WorkspaceRoot: s.workspace,
		Cursor:        params.Cursor,
	})
	if err != nil {
		if application.IsCategory(err, application.CategoryValidation) {
			return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
		}
		return s.out.writeError(message.ID, codeInternalError, sessionOperationFailedMessage)
	}
	entries := make([]sessionListEntry, len(result.Sessions))
	for index, session := range result.Sessions {
		entries[index] = sessionListEntry{
			SessionID: string(session.SessionID),
			Cwd:       session.WorkspaceRoot,
			UpdatedAt: session.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	return s.out.writeResult(message.ID, sessionListResult{Sessions: entries, NextCursor: result.NextCursor})
}

func (s *server) sessionResume(message rpcRequest) error {
	var params sessionResumeParams
	if json.Unmarshal(message.Params, &params) != nil || params.SessionID == "" || params.Cwd == "" {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	if len(params.MCPServers) > 0 || len(params.AdditionalDirectories) > 0 {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	if !s.requestWorkspaceMatches(params.Cwd) {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	sessionID, err := domain.ParseSessionID(params.SessionID)
	if err != nil {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	s.mu.Lock()
	if admitBusy(s.sessions[params.SessionID]) {
		s.mu.Unlock()
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	s.mu.Unlock()
	if _, err := s.config.Sessions.ResumeSession(s.ctx, application.ResumeSessionRequest{
		SessionID:     sessionID,
		WorkspaceRoot: s.workspace,
	}); err != nil {
		if application.IsCategory(err, application.CategoryValidation) {
			return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
		}
		return s.out.writeError(message.ID, codeInternalError, sessionOperationFailedMessage)
	}
	s.mu.Lock()
	s.sessions[params.SessionID] = &sessionState{state: wireIdle}
	s.mu.Unlock()
	return s.out.writeResult(message.ID, struct{}{})
}

// sessionClose fast-fails obviously ineligible entries under the mutex, then
// hands off to a goroutine for everything that can block: the durable
// LoadSession check (confirming the session still exists in this workspace,
// so close cannot outlive an externally deleted or foreign session) and
// cancelling/waiting for running work. Neither step holds the mutex or
// blocks frames for other sessions.
//
// The durable check is unlocked and can race a concurrently settling or
// restarting prompt on the same entry, so admission is decided exactly once,
// fresh under the mutex, immediately before the wireClosing transition — the
// cancel/promptDone pair used below is read at that same instant, never
// captured before the durable call. Reusing an earlier snapshot would let
// close cancel a prompt that already finished while a different prompt that
// started in the interim ran on uncancelled.
//
// It never calls the durable Application close use case and never appends
// session.closed: a resumable persistent Session survives.
func (s *server) sessionClose(message rpcRequest) error {
	var params sessionIDParams
	if json.Unmarshal(message.Params, &params) != nil || params.SessionID == "" {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	sessionID, err := domain.ParseSessionID(params.SessionID)
	if err != nil {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	s.mu.Lock()
	entry := s.sessions[params.SessionID]
	fastEligible := entry != nil && (entry.state == wireIdle || entry.state == wireRunning)
	s.mu.Unlock()
	if !fastEligible {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}

	go s.durableCloseAdmission(message.ID, sessionID, params.SessionID)
	return nil
}

// durableCloseAdmission runs off the dispatch goroutine so its LoadSession
// call cannot block other sessions on the same duplex. It takes the one
// authoritative admission decision fresh, under the mutex, right after that
// call returns.
func (s *server) durableCloseAdmission(id json.RawMessage, sessionID domain.SessionID, wireID string) {
	session, err := s.config.Sessions.LoadSession(s.ctx, sessionID)
	if err != nil || !s.sessionWorkspaceMatches(session.WorkspaceRoot) {
		_ = s.out.writeError(id, codeInvalidParams, "invalid params")
		return
	}

	s.mu.Lock()
	entry := s.sessions[wireID]
	if entry == nil || (entry.state != wireIdle && entry.state != wireRunning) {
		s.mu.Unlock()
		_ = s.out.writeError(id, codeInvalidParams, "invalid params")
		return
	}
	entry.state = wireClosing
	cancel := entry.cancel
	done := entry.promptDone
	s.mu.Unlock()

	s.finishClose(id, wireID, cancel, done)
}

func (s *server) finishClose(id json.RawMessage, wireID string, cancel context.CancelFunc, done chan struct{}) {
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	s.mu.Lock()
	s.sessions[wireID] = &sessionState{state: wireDetached}
	s.mu.Unlock()
	_ = s.out.writeResult(id, struct{}{})
}

// sessionDelete admits idle, detached, or absent entries only and installs
// deleting under the mutex before returning control to the dispatch loop, so
// a prompt arriving for the same session sees deleting immediately rather
// than racing the Application load/append that runs in the background.
// Restoration after any failure other than the idempotent
// absent/foreign/deleted case brings the entry back to its exact prior state.
func (s *server) sessionDelete(message rpcRequest) error {
	var params sessionIDParams
	if json.Unmarshal(message.Params, &params) != nil || params.SessionID == "" {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	s.mu.Lock()
	prior := s.sessions[params.SessionID]
	if admitBusy(prior) {
		s.mu.Unlock()
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	s.sessions[params.SessionID] = &sessionState{state: wireDeleting}
	s.mu.Unlock()

	go s.finishDelete(message.ID, params.SessionID, prior)
	return nil
}

func (s *server) finishDelete(id json.RawMessage, wireID string, prior *sessionState) {
	err := s.config.Sessions.DeleteSession(s.ctx, application.DeleteSessionRequest{
		SessionID:     domain.SessionID(wireID),
		WorkspaceRoot: s.workspace,
	})
	if err == nil || isSessionNotFound(err) {
		s.mu.Lock()
		delete(s.sessions, wireID)
		s.mu.Unlock()
		_ = s.out.writeResult(id, struct{}{})
		return
	}
	s.mu.Lock()
	if prior == nil {
		delete(s.sessions, wireID)
	} else {
		s.sessions[wireID] = prior
	}
	s.mu.Unlock()
	if application.IsCategory(err, application.CategoryValidation) {
		_ = s.out.writeError(id, codeInvalidParams, "invalid params")
		return
	}
	_ = s.out.writeError(id, codeInternalError, sessionOperationFailedMessage)
}

func isSessionNotFound(err error) bool {
	var applicationErr *application.Error
	return errors.As(err, &applicationErr) && applicationErr != nil && applicationErr.Code == "session_not_found"
}

func stopReason(result application.RunTurnResult, err error, ctx context.Context) (string, bool) {
	if result.Status == domain.TurnStatusCompleted {
		return stopReasonEndTurn, true
	}
	if result.Status == domain.TurnStatusInterrupted || application.IsCategory(err, application.CategoryCanceled) || ctx.Err() != nil {
		return stopReasonCancelled, true
	}
	return "", false
}

func concatenatePrompt(blocks []promptBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == "" || block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

func newRequestID() (domain.RunTurnRequestID, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return domain.ParseRunTurnRequestID("request-" + hex.EncodeToString(raw))
}

// Decide implements tools.Approver as a reverse RPC.
func (s *server) Decide(ctx context.Context, req tools.ApprovalRequest) (tools.ApprovalAnswer, error) {
	id, waiter := s.nextID()
	params, ok := fitPermission(id, permissionParams{
		SessionID: string(req.SessionID),
		ToolCall:  permissionToolCall{ToolCallID: ToolCallID(req.TurnID, req.CallID), Title: req.Name, Kind: ToolKind(req.Name), Status: "pending"},
		Options: []permissionOption{
			{OptionID: optionAllowOnce, Name: "Allow once", Kind: "allow_once"},
			{OptionID: optionRejectOnce, Name: "Reject once", Kind: "reject_once"},
		},
	})
	if !ok {
		s.forgetWaiter(id)
		return tools.ApprovalAnswer{}, nil
	}
	if err := s.out.write(permissionRequest{JSONRPC: jsonRPCVersion, ID: id, Method: methodRequestPermission, Params: params}); err != nil {
		s.forgetWaiter(id)
		return tools.ApprovalAnswer{}, nil
	}
	select {
	case <-ctx.Done():
		return tools.ApprovalAnswer{}, nil
	case response := <-waiter:
		if response.Error != nil {
			return tools.ApprovalAnswer{}, nil
		}
		var result permissionResult
		if json.Unmarshal(response.Result, &result) != nil {
			return tools.ApprovalAnswer{}, nil
		}
		if result.Outcome.Outcome == "selected" && result.Outcome.OptionID == optionAllowOnce {
			return tools.ApprovalAnswer{Granted: true}, nil
		}
		return tools.ApprovalAnswer{}, nil
	}
}

func (s *server) nextID() (json.RawMessage, chan rpcRequest) {
	s.mu.Lock()
	s.outgoing++
	id := json.RawMessage([]byte(strings.TrimSpace(string(mustMarshal(s.outgoing)))))
	waiter := make(chan rpcRequest, 1)
	s.pending[idKey(id)] = waiter
	s.mu.Unlock()
	return id, waiter
}

func (s *server) forgetWaiter(id json.RawMessage) {
	s.mu.Lock()
	delete(s.pending, idKey(id))
	s.mu.Unlock()
}

type permissionRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id"`
	Method  string           `json:"method"`
	Params  permissionParams `json:"params"`
}

func fitPermission(id json.RawMessage, params permissionParams) (permissionParams, bool) {
	if permissionFrameFits(id, params) {
		return params, true
	}
	title := params.ToolCall.Title
	params.ToolCall.Title = shrinkUntil(title, func(s string) bool {
		next := params
		next.ToolCall.Title = s
		return permissionFrameFits(id, next)
	})
	return params, permissionFrameFits(id, params)
}

func permissionFrameFits(id json.RawMessage, params permissionParams) bool {
	payload, err := json.Marshal(permissionRequest{
		JSONRPC: jsonRPCVersion, ID: id, Method: methodRequestPermission, Params: params,
	})
	return err == nil && !bytes.Contains(payload, []byte{'\n'}) && len(payload)+1 <= maxFrameBytes
}

func mustMarshal(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		return []byte("0")
	}
	return payload
}

type updateSink struct {
	sessionID string
	out       *frameWriter
	live      LiveTool
}

func (sink *updateSink) Emit(_ context.Context, event engine.RuntimeEvent) error {
	live := sink.remember(event)
	for _, update := range ProjectRuntimeEvent(sink.sessionID, event, live) {
		_ = sink.out.writeNotification(methodSessionUpdate, sessionUpdateParams{
			SessionID: sink.sessionID,
			Update:    update,
		})
	}
	return nil
}

func (sink *updateSink) remember(event engine.RuntimeEvent) LiveTool {
	switch event.Type {
	case engine.RuntimeModelToolCall, engine.RuntimeToolExecutionStarted:
		name, callID := splitToolText(event.Text)
		sink.live = LiveTool{TurnID: event.TurnID, CallID: callID, Name: name}
		return sink.live
	case engine.RuntimeToolExecutionCompleted, engine.RuntimeToolExecutionFailed:
		live := sink.live
		if event.Text != "" {
			name, callID := splitToolText(event.Text)
			live = LiveTool{TurnID: event.TurnID, CallID: callID, Name: name}
		}
		sink.live = LiveTool{}
		return live
	default:
		return sink.live
	}
}

var _ tools.Approver = (*server)(nil)
var _ engine.RuntimeSink = (*updateSink)(nil)
