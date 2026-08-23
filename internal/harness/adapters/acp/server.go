package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

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
	if ctx == nil || config.Sessions == nil || strings.TrimSpace(config.Workspace) == "" || out == nil {
		return fmt.Errorf("acp: invalid configuration")
	}
	server := &server{
		ctx:       ctx,
		config:    config,
		out:       &frameWriter{writer: out},
		pending:   make(map[string]chan rpcRequest),
		sessions:  make(map[string]*sessionState),
		workspace: filepath.Clean(config.Workspace),
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

type sessionState struct {
	cancel context.CancelFunc
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
	s.mu.Lock()
	state := s.sessions[params.SessionID]
	s.mu.Unlock()
	if state != nil && state.cancel != nil {
		state.cancel()
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
				LoadSession: true,
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
	default:
		return s.out.writeError(message.ID, codeMethodNotFound, "method not found")
	}
}

func (s *server) sessionNew(message rpcRequest) error {
	var params sessionNewParams
	if len(message.Params) > 0 && json.Unmarshal(message.Params, &params) != nil {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	if params.Cwd != "" && filepath.Clean(params.Cwd) != s.workspace {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	created, err := s.config.Sessions.CreateSession(s.ctx, application.CreateSessionRequest{WorkspaceRoot: s.workspace})
	if err != nil {
		return s.out.writeError(message.ID, codeInternalError, promptFailedMessage)
	}
	return s.out.writeResult(message.ID, sessionIDParams{SessionID: string(created.SessionID)})
}

func (s *server) sessionLoad(message rpcRequest) error {
	var params sessionIDParams
	if json.Unmarshal(message.Params, &params) != nil || params.SessionID == "" {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	sessionID, err := domain.ParseSessionID(params.SessionID)
	if err != nil {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	session, err := s.config.Sessions.LoadSession(s.ctx, sessionID)
	if err != nil || filepath.Clean(session.WorkspaceRoot) != s.workspace {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}
	if s.config.History != nil {
		if err := s.replay(sessionID); err != nil {
			return s.out.writeError(message.ID, codeInternalError, promptFailedMessage)
		}
	}
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
	switch event := record.Event.(type) {
	case domain.TurnStarted:
		if event.Input == "" {
			return nil
		}
		return s.out.writeNotification(methodSessionUpdate, sessionUpdateParams{
			SessionID: sessionID,
			Update: agentMessageChunk{ // user history uses the same text-chunk shape with a distinct tag
				SessionUpdate: "user_message_chunk",
				Content:       textContent{Type: "text", Text: event.Input},
			},
		})
	case domain.AssistantMessageCompleted:
		if event.Text == "" {
			return nil
		}
		return s.out.writeNotification(methodSessionUpdate, sessionUpdateParams{
			SessionID: sessionID,
			Update: agentMessageChunk{
				SessionUpdate: "agent_message_chunk",
				Content:       textContent{Type: "text", Text: event.Text},
			},
		})
	default:
		return nil
	}
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
	if err != nil || filepath.Clean(session.WorkspaceRoot) != s.workspace {
		return s.out.writeError(message.ID, codeInvalidParams, "invalid params")
	}

	s.mu.Lock()
	if existing := s.sessions[params.SessionID]; existing != nil && existing.cancel != nil {
		s.mu.Unlock()
		return s.out.writeError(message.ID, codeInvalidRequest, promptInFlightMessage)
	}
	promptCtx, cancel := context.WithCancel(s.ctx)
	s.sessions[params.SessionID] = &sessionState{cancel: cancel}
	s.mu.Unlock()

	go s.runPrompt(promptCtx, message.ID, sessionID, params.SessionID, input)
	return nil
}

func (s *server) runPrompt(ctx context.Context, id json.RawMessage, sessionID domain.SessionID, wireID, input string) {
	requestID, err := newRequestID()
	if err != nil {
		s.finishPrompt(wireID)
		_ = s.out.writeError(id, codeInternalError, promptFailedMessage)
		return
	}
	result, runErr := s.config.Sessions.RunTurn(ctx, application.RunTurnRequest{
		SessionID: sessionID,
		RequestID: requestID,
		Input:     input,
		Sink:      &updateSink{sessionID: wireID, out: s.out},
	})
	s.finishPrompt(wireID)
	if reason, ok := stopReason(result, runErr, ctx); ok {
		_ = s.out.writeResult(id, promptResult{StopReason: reason})
		return
	}
	_ = s.out.writeError(id, codeInternalError, promptFailedMessage)
}

func (s *server) finishPrompt(wireID string) {
	s.mu.Lock()
	delete(s.sessions, wireID)
	s.mu.Unlock()
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
	params := permissionParams{
		SessionID: string(req.SessionID),
		ToolCall:  permissionToolCall{ToolCallID: ToolCallID(req.TurnID, req.CallID), Title: req.Name, Kind: ToolKind(req.Name), Status: "pending"},
		Options: []permissionOption{
			{OptionID: optionAllowOnce, Name: "Allow once", Kind: "allow_once"},
			{OptionID: optionRejectOnce, Name: "Reject once", Kind: "reject_once"},
		},
	}
	if err := s.out.write(struct {
		JSONRPC string           `json:"jsonrpc"`
		ID      json.RawMessage  `json:"id"`
		Method  string           `json:"method"`
		Params  permissionParams `json:"params"`
	}{JSONRPC: jsonRPCVersion, ID: id, Method: methodRequestPermission, Params: params}); err != nil {
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
