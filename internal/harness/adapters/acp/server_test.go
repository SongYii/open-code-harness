package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

type fakeSessions struct {
	mu       sync.Mutex
	created  int
	runs     int
	reads    int
	lists    int
	resumes  int
	deletes  int
	sessions map[domain.SessionID]domain.Session
	run      func(context.Context, application.RunTurnRequest) (application.RunTurnResult, error)
	list     func(context.Context, application.ListSessionsRequest) (application.ListSessionsResult, error)
	resume   func(context.Context, application.ResumeSessionRequest) (domain.Session, error)
	del      func(context.Context, application.DeleteSessionRequest) error
	history  []domain.RecordedEvent
}

var fakeUpdatedAt = time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

func sessionNotFoundError() error {
	return &application.Error{Category: application.CategoryValidation, Code: "session_not_found"}
}

func newFake() *fakeSessions {
	return &fakeSessions{sessions: map[domain.SessionID]domain.Session{}}
}

func (f *fakeSessions) CreateSession(_ context.Context, req application.CreateSessionRequest) (application.CreateSessionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	id := domain.SessionID("session-acp-1")
	f.sessions[id] = domain.Session{ID: id, Status: domain.SessionStatusActive, WorkspaceRoot: req.WorkspaceRoot}
	return application.CreateSessionResult{SessionID: id}, nil
}

func (f *fakeSessions) LoadSession(_ context.Context, id domain.SessionID) (domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok || session.Status == domain.SessionStatusDeleted {
		return domain.Session{}, sessionNotFoundError()
	}
	return session, nil
}

func (f *fakeSessions) RunTurn(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
	f.mu.Lock()
	f.runs++
	run := f.run
	f.mu.Unlock()
	if run != nil {
		return run(ctx, request)
	}
	_ = request.Sink.Emit(ctx, engine.RuntimeEvent{Type: engine.RuntimeModelTextDelta, Text: "hello"})
	return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusCompleted, Text: "hello", TerminalCommitted: true}, nil
}

func (f *fakeSessions) ListSessions(ctx context.Context, req application.ListSessionsRequest) (application.ListSessionsResult, error) {
	f.mu.Lock()
	f.lists++
	list := f.list
	f.mu.Unlock()
	if list != nil {
		return list(ctx, req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var sessions []application.ListedSession
	for id, session := range f.sessions {
		if session.Status == domain.SessionStatusDeleted || session.WorkspaceRoot != req.WorkspaceRoot {
			continue
		}
		sessions = append(sessions, application.ListedSession{SessionID: id, WorkspaceRoot: session.WorkspaceRoot, UpdatedAt: fakeUpdatedAt})
	}
	return application.ListSessionsResult{Sessions: sessions}, nil
}

func (f *fakeSessions) ResumeSession(ctx context.Context, req application.ResumeSessionRequest) (domain.Session, error) {
	f.mu.Lock()
	f.resumes++
	resume := f.resume
	f.mu.Unlock()
	if resume != nil {
		return resume(ctx, req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[req.SessionID]
	if !ok || session.WorkspaceRoot != req.WorkspaceRoot || session.Status != domain.SessionStatusActive {
		return domain.Session{}, sessionNotFoundError()
	}
	return session, nil
}

func (f *fakeSessions) DeleteSession(ctx context.Context, req application.DeleteSessionRequest) error {
	f.mu.Lock()
	f.deletes++
	del := f.del
	f.mu.Unlock()
	if del != nil {
		return del(ctx, req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[req.SessionID]
	if !ok || session.WorkspaceRoot != req.WorkspaceRoot || session.Status == domain.SessionStatusDeleted {
		return sessionNotFoundError()
	}
	session.Status = domain.SessionStatusDeleted
	f.sessions[req.SessionID] = session
	return nil
}

func (f *fakeSessions) ReadStream(context.Context, application.ReadStreamRequest) (application.StreamPage, error) {
	f.mu.Lock()
	f.reads++
	history := append([]domain.RecordedEvent(nil), f.history...)
	f.mu.Unlock()
	return application.StreamPage{Records: history, End: true}, nil
}

func TestServeInitializeNewPromptAndBusyReject(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`)
	init := readJSON(t, clientIn)
	if init["id"] != float64(1) {
		t.Fatalf("initialize id = %v", init["id"])
	}
	result := init["result"].(map[string]any)
	if result["protocolVersion"] != float64(1) {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/workspace"}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)
	if sessionID == "" {
		t.Fatal("session/new returned empty sessionId")
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"type":"text","text":"hi"}]}}`)
	var promptResult map[string]any
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			continue
		}
		promptResult = message
		break
	}
	if promptResult["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
		t.Fatalf("prompt result = %#v", promptResult)
	}

	blocked := make(chan struct{})
	fake.run = func(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
		close(blocked)
		<-ctx.Done()
		return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusInterrupted}, ctx.Err()
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"type":"text","text":"one"}]}}`)
	<-blocked
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":5,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"type":"text","text":"two"}]}}`)
	busy := readJSON(t, clientIn)
	if busy["error"].(map[string]any)["code"] != float64(codeInvalidRequest) {
		t.Fatalf("busy error = %#v", busy)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"`+sessionID+`"}}`)
	cancelled := readJSON(t, clientIn)
	if cancelled["result"].(map[string]any)["stopReason"] != stopReasonCancelled {
		t.Fatalf("cancelled result = %#v", cancelled)
	}
}

func TestServeLoadReplaysHistoryAndUnknownSessionErrors(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	when := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	fake.sessions = map[domain.SessionID]domain.Session{"session-acp-1": {ID: "session-acp-1", Status: domain.SessionStatusActive, WorkspaceRoot: "/workspace"}}
	fake.history = []domain.RecordedEvent{
		{Event: domain.TurnStarted{TurnID: "turn-1", Input: "hello"}, OccurredAt: when},
		{Event: domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "world"}, OccurredAt: when},
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":"missing"}}`)
	missing := readJSON(t, clientIn)
	if missing["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("missing load = %#v", missing)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/load","params":{"sessionId":"session-acp-1"}}`)
	updates := 0
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			updates++
			continue
		}
		if message["id"] != float64(3) {
			t.Fatalf("unexpected load frame %#v", message)
		}
		break
	}
	if updates != 2 {
		t.Fatalf("load updates = %d, want 2", updates)
	}
}

func TestServeLoadReplaysToolBearingHistory(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	when := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	turn := domain.TurnID("turn-1")
	fake.sessions = map[domain.SessionID]domain.Session{
		"session-acp-1": {ID: "session-acp-1", Status: domain.SessionStatusActive, WorkspaceRoot: "/workspace"},
	}
	fake.history = []domain.RecordedEvent{
		{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}, OccurredAt: when},
		{Event: domain.TurnStarted{TurnID: turn, Input: "hello"}, OccurredAt: when},
		{Event: domain.AssistantMessageCompleted{TurnID: turn, ItemID: "item-1", Text: "world", ToolCalls: []domain.ToolCallOffer{{ID: "call-1", Name: "read_file"}}}, OccurredAt: when},
		{Event: domain.ModelRequestRecorded{TurnID: turn, ItemID: "item-1"}, OccurredAt: when},
		{Event: domain.ModelUsageRecorded{TurnID: turn, ItemID: "item-1", InputTokens: 3}, OccurredAt: when},
		{Event: domain.PolicyDecisionRecorded{TurnID: turn, ItemID: "item-2", CallID: "call-1", Effect: "allow"}, OccurredAt: when},
		{Event: domain.ToolCallStarted{TurnID: turn, ItemID: "item-2", CallID: "call-1", Name: "read_file", Arguments: `{"path":"NOTES.md"}`, StepIndex: 1}, OccurredAt: when},
		{Event: domain.ToolCallCompleted{TurnID: turn, ItemID: "item-2", CallID: "call-1", Content: "file body"}, OccurredAt: when},
		{Event: domain.ToolCallStarted{TurnID: turn, ItemID: "item-3", CallID: "call-2", Name: "write_file", Arguments: `{"path":"OUT.md"}`}, OccurredAt: when},
		{Event: domain.ToolCallFailed{TurnID: turn, ItemID: "item-3", CallID: "call-2", Code: "policy_denied", Message: "denied"}, OccurredAt: when},
		{Event: domain.ToolCallStarted{TurnID: turn, ItemID: "item-4", CallID: "call-3", Name: "exec", Arguments: "echo hi"}, OccurredAt: when},
		{Event: domain.ToolCallInterrupted{TurnID: turn, ItemID: "item-4", CallID: "call-3", Code: "canceled", Message: "stopped"}, OccurredAt: when},
		{Event: domain.AssistantMessageFailed{TurnID: turn, ItemID: "item-5", Code: "model_stream", Message: "partial"}, OccurredAt: when},
		{Event: domain.AssistantMessageInterrupted{TurnID: turn, ItemID: "item-6", Code: "canceled", Message: "stop"}, OccurredAt: when},
		{Event: domain.TurnCompleted{TurnID: turn}, OccurredAt: when},
		{Event: domain.ApprovalRequested{TurnID: turn, ItemID: "item-3", CallID: "call-2", Name: "write_file"}, OccurredAt: when},
		{Event: domain.ApprovalResolved{TurnID: turn, ItemID: "item-3", Decision: "denied"}, OccurredAt: when},
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":"session-acp-1"}}`)

	var updates []map[string]any
	var loadResult map[string]any
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			if loadResult != nil {
				t.Fatalf("session/update after load result: %#v", message)
			}
			params := message["params"].(map[string]any)
			if params["sessionId"] != "session-acp-1" {
				t.Fatalf("load update sessionId = %#v", params["sessionId"])
			}
			updates = append(updates, params["update"].(map[string]any))
			continue
		}
		loadResult = message
		break
	}
	if loadResult["id"] != float64(2) || loadResult["error"] != nil {
		t.Fatalf("load result = %#v", loadResult)
	}
	want := []map[string]any{
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "hello"}},
		{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "world"}},
		{"sessionUpdate": "tool_call", "toolCallId": "turn-1/call-1", "title": "read_file", "kind": "read", "status": "in_progress", "rawInput": map[string]any{"path": "NOTES.md"}},
		{"sessionUpdate": "tool_call_update", "toolCallId": "turn-1/call-1", "status": "completed", "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "file body"}}}},
		{"sessionUpdate": "tool_call", "toolCallId": "turn-1/call-2", "title": "write_file", "kind": "edit", "status": "in_progress", "rawInput": map[string]any{"path": "OUT.md"}},
		{"sessionUpdate": "tool_call_update", "toolCallId": "turn-1/call-2", "status": "failed", "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "denied"}}}},
		{"sessionUpdate": "tool_call", "toolCallId": "turn-1/call-3", "title": "exec", "kind": "execute", "status": "in_progress"},
		{"sessionUpdate": "tool_call_update", "toolCallId": "turn-1/call-3", "status": "failed"},
		{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "partial"}},
		{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "stop"}},
	}
	gotJSON, err := json.Marshal(updates)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("load updates = %s, want %s", gotJSON, wantJSON)
	}
	for _, update := range updates {
		payload := mustJSON(update)
		if strings.Contains(payload, "interrupted") {
			t.Fatalf("load emitted interrupted status: %s", payload)
		}
		if update["sessionUpdate"] == "tool_call_update" && update["status"] == "failed" && update["toolCallId"] == "turn-1/call-3" {
			if _, ok := update["content"]; ok {
				t.Fatalf("interrupted tool card must not carry content: %#v", update)
			}
		}
	}
}

func TestServeLoadDegradesOversizeToolIdentity(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	when := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	hugeName := strings.Repeat("x", maxFrameBytes)
	hugeCallID := strings.Repeat("c", maxFrameBytes)
	fake.sessions = map[domain.SessionID]domain.Session{
		"session-acp-1": {ID: "session-acp-1", Status: domain.SessionStatusActive, WorkspaceRoot: "/workspace"},
	}
	fake.history = []domain.RecordedEvent{
		{Event: domain.TurnStarted{TurnID: "turn-1", Input: "hello"}, OccurredAt: when},
		{Event: domain.ToolCallStarted{TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Name: hugeName, Arguments: `{"path":"NOTES.md"}`, StepIndex: 1}, OccurredAt: when},
		{Event: domain.ToolCallStarted{TurnID: "turn-1", ItemID: "item-2", CallID: hugeCallID, Name: "read_file", Arguments: `{"path":"SKIP.md"}`, StepIndex: 2}, OccurredAt: when},
		{Event: domain.ToolCallCompleted{TurnID: "turn-1", ItemID: "item-2", CallID: hugeCallID, Content: "secret"}, OccurredAt: when},
		{Event: domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-3", Text: "world"}, OccurredAt: when},
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":"session-acp-1"}}`)

	var updates []map[string]any
	var loadResult map[string]any
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			updates = append(updates, message["params"].(map[string]any)["update"].(map[string]any))
			continue
		}
		loadResult = message
		break
	}
	if loadResult["id"] != float64(2) || loadResult["error"] != nil {
		t.Fatalf("load result error = %#v", loadResult["error"])
	}
	if len(updates) != 3 {
		t.Fatalf("load updates = %d, want 3", len(updates))
	}
	if updates[0]["sessionUpdate"] != "user_message_chunk" {
		t.Fatalf("first update = %#v", updates[0]["sessionUpdate"])
	}
	card := updates[1]
	if card["sessionUpdate"] != "tool_call" || card["toolCallId"] != "turn-1/call-1" {
		t.Fatalf("tool card id = %#v", card["toolCallId"])
	}
	title, _ := card["title"].(string)
	if title == "" || len(title) >= len(hugeName) || !strings.HasPrefix(hugeName, title) {
		t.Fatalf("tool title len = %d, want clipped prefix", len(title))
	}
	if _, ok := card["rawInput"]; ok {
		t.Fatal("oversize title must omit rawInput")
	}
	if updates[2]["sessionUpdate"] != "agent_message_chunk" {
		t.Fatalf("last update = %#v", updates[2]["sessionUpdate"])
	}
	for _, update := range updates {
		if id, _ := update["toolCallId"].(string); strings.Contains(id, hugeCallID) {
			t.Fatal("load emitted unsendable toolCallId")
		}
	}
}

func TestServeLoadFailsOnSessionUpdateWriteError(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	fake.sessions = map[domain.SessionID]domain.Session{
		"session-acp-1": {ID: "session-acp-1", Status: domain.SessionStatusActive, WorkspaceRoot: "/workspace"},
	}
	fake.history = []domain.RecordedEvent{
		{Event: domain.TurnStarted{TurnID: "turn-1", Input: "hello"}, OccurredAt: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)},
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, dropSessionUpdates{writer: agentOut})
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":"session-acp-1"}}`)
	result := readJSON(t, clientIn)
	if result["method"] == methodSessionUpdate {
		t.Fatalf("failed load wrote session/update: %#v", result)
	}
	rpcErr, _ := result["error"].(map[string]any)
	if rpcErr["code"] != float64(codeInternalError) {
		t.Fatalf("load write failure = %#v", result)
	}
	if rpcErr["message"] != promptFailedMessage {
		t.Fatalf("load write failure message = %#v, want %q", rpcErr["message"], promptFailedMessage)
	}
}

func TestServePermissionGrantAndDeny(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	slot := tools.NewSlot(nil)
	fake := newFake()
	fake.run = func(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
		answer, err := slot.Decide(ctx, tools.ApprovalRequest{SessionID: request.SessionID, TurnID: "turn-1", CallID: "call-1", Name: "write_file"})
		if err != nil || !answer.Granted {
			return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusFailed}, &application.Error{Category: application.CategoryInternal, Code: "denied"}
		}
		return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusCompleted}, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace", Approver: slot}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"write"}]}}`)
	perm := readJSON(t, clientIn)
	if perm["method"] != methodRequestPermission {
		t.Fatalf("want permission request, got %#v", perm)
	}
	toolCall := perm["params"].(map[string]any)["toolCall"].(map[string]any)
	if toolCall["toolCallId"] != "turn-1/call-1" {
		t.Fatalf("toolCallId = %#v", toolCall["toolCallId"])
	}
	if toolCall["kind"] != "edit" {
		t.Fatalf("kind = %#v", toolCall["kind"])
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":`+mustJSON(perm["id"])+`,"result":{"outcome":{"outcome":"selected","optionId":"allow-once"}}}`)
	final := readJSON(t, clientIn)
	if final["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
		t.Fatalf("granted prompt = %#v", final)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"write"}]}}`)
	denied := readJSON(t, clientIn)
	if denied["method"] != methodRequestPermission {
		t.Fatalf("want permission request, got %#v", denied)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":`+mustJSON(denied["id"])+`,"result":{"outcome":{"outcome":"selected","optionId":"reject-once"}}}`)
	rejected := readJSON(t, clientIn)
	if rejected["error"].(map[string]any)["code"] != float64(codeInternalError) {
		t.Fatalf("denied prompt = %#v", rejected)
	}
}

func TestServePermissionClipsOversizeTitleAndDeniesUnsendableID(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	slot := tools.NewSlot(nil)
	hugeName := strings.Repeat("x", maxFrameBytes)
	hugeCallID := strings.Repeat("c", maxFrameBytes)
	fake := newFake()
	var round int
	fake.run = func(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
		round++
		req := tools.ApprovalRequest{SessionID: request.SessionID, TurnID: "turn-1", CallID: "call-1", Name: hugeName}
		if round == 2 {
			req.CallID = hugeCallID
			req.Name = "write_file"
		}
		answer, err := slot.Decide(ctx, req)
		if err != nil || !answer.Granted {
			return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusFailed}, &application.Error{Category: application.CategoryInternal, Code: "denied"}
		}
		return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusCompleted}, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace", Approver: slot}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"write"}]}}`)
	perm := readJSON(t, clientIn)
	if perm["method"] != methodRequestPermission {
		t.Fatalf("want permission request, got method %#v", perm["method"])
	}
	toolCall := perm["params"].(map[string]any)["toolCall"].(map[string]any)
	if toolCall["toolCallId"] != "turn-1/call-1" {
		t.Fatalf("toolCallId = %#v", toolCall["toolCallId"])
	}
	title, _ := toolCall["title"].(string)
	if title == "" || len(title) >= len(hugeName) || !strings.HasPrefix(hugeName, title) {
		t.Fatalf("permission title len = %d, want clipped prefix", len(title))
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":`+mustJSON(perm["id"])+`,"result":{"outcome":{"outcome":"selected","optionId":"allow-once"}}}`)
	final := readJSON(t, clientIn)
	if final["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
		t.Fatalf("granted prompt = %#v", final["result"])
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"write"}]}}`)
	denied := readJSON(t, clientIn)
	if denied["method"] == methodRequestPermission {
		t.Fatal("unsendable permission toolCallId must not be written")
	}
	if denied["error"].(map[string]any)["code"] != float64(codeInternalError) {
		t.Fatalf("unsendable permission prompt = %#v", denied["error"])
	}
}

func TestCodecRejectsNonACPAndRequiresInitialize(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: newFake(), Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{not-json`)
	parse := readJSON(t, clientIn)
	if parse["error"].(map[string]any)["code"] != float64(codeParseError) {
		t.Fatalf("parse = %#v", parse)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"session/new","params":{}}`)
	before := readJSON(t, clientIn)
	if before["error"].(map[string]any)["code"] != float64(codeInvalidRequest) {
		t.Fatalf("before initialize = %#v", before)
	}
}

func writeLine(t *testing.T, w io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
	if !scanner.Scan() {
		t.Fatalf("read: %v", scanner.Err())
	}
	var message map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
		t.Fatalf("unmarshal %q: %v", scanner.Text(), err)
	}
	return message
}

func mustJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func TestConcatenatePromptIgnoresNonText(t *testing.T) {
	got := concatenatePrompt([]promptBlock{{Type: "text", Text: "a"}, {Type: "image", Text: "no"}, {Text: "b"}})
	if got != "ab" {
		t.Fatalf("concatenate = %q", got)
	}
}

func TestServePromptProjectsLiveToolCallsAndCodeOnlyFailed(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	fake.run = func(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
		correlation := engine.Correlation{TurnID: "turn-1"}
		events := []engine.RuntimeEvent{
			{Type: engine.RuntimeModelTextDelta, Text: "looking", Correlation: correlation},
			{Type: engine.RuntimeModelToolCall, Text: "read_file:call-1", Correlation: correlation},
			{Type: engine.RuntimeToolExecutionStarted, Text: "read_file:call-1", Correlation: correlation},
			{Type: engine.RuntimeToolExecutionFailed, Code: "policy_denied", Correlation: correlation},
		}
		for _, event := range events {
			if err := request.Sink.Emit(ctx, event); err != nil {
				return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusFailed}, err
			}
		}
		return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusCompleted}, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"read"}]}}`)

	var updates []map[string]any
	var promptResult map[string]any
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			updates = append(updates, message["params"].(map[string]any)["update"].(map[string]any))
			continue
		}
		promptResult = message
		break
	}
	if promptResult["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
		t.Fatalf("prompt result = %#v", promptResult)
	}
	want := []struct {
		sessionUpdate string
		status        string
	}{
		{"agent_message_chunk", ""},
		{"tool_call", "pending"},
		{"tool_call_update", "in_progress"},
		{"tool_call_update", "failed"},
	}
	if len(updates) != len(want) {
		t.Fatalf("updates = %#v", updates)
	}
	for index, step := range want {
		if updates[index]["sessionUpdate"] != step.sessionUpdate {
			t.Fatalf("update %d sessionUpdate = %#v", index, updates[index])
		}
		if step.status != "" && updates[index]["status"] != step.status {
			t.Fatalf("update %d status = %#v", index, updates[index])
		}
		if step.sessionUpdate != "agent_message_chunk" && updates[index]["toolCallId"] != "turn-1/call-1" {
			t.Fatalf("update %d toolCallId = %#v", index, updates[index]["toolCallId"])
		}
	}
}

func TestServePromptProjectsOversizeToolName(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	hugeName := strings.Repeat("x", maxFrameBytes)
	fake := newFake()
	fake.run = func(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
		correlation := engine.Correlation{TurnID: "turn-1"}
		events := []engine.RuntimeEvent{
			{Type: engine.RuntimeModelToolCall, Text: hugeName + ":call-1", Correlation: correlation},
			{Type: engine.RuntimeToolExecutionFailed, Code: "unknown_tool", Correlation: correlation},
		}
		for _, event := range events {
			if err := request.Sink.Emit(ctx, event); err != nil {
				return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusFailed}, err
			}
		}
		return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusCompleted}, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"read"}]}}`)

	var updates []map[string]any
	var promptResult map[string]any
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			updates = append(updates, message["params"].(map[string]any)["update"].(map[string]any))
			continue
		}
		promptResult = message
		break
	}
	if promptResult["error"] != nil {
		t.Fatalf("prompt error = %#v", promptResult["error"])
	}
	if promptResult["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
		t.Fatalf("prompt result = %#v", promptResult["result"])
	}
	if len(updates) != 2 {
		t.Fatalf("updates = %d, want 2", len(updates))
	}
	if updates[0]["sessionUpdate"] != "tool_call" || updates[0]["toolCallId"] != "turn-1/call-1" {
		t.Fatalf("tool_call id = %#v", updates[0]["toolCallId"])
	}
	title, _ := updates[0]["title"].(string)
	if title == "" || len(title) >= len(hugeName) || !strings.HasPrefix(hugeName, title) {
		t.Fatalf("live title len = %d, want clipped prefix", len(title))
	}
	if updates[1]["sessionUpdate"] != "tool_call_update" || updates[1]["status"] != "failed" || updates[1]["toolCallId"] != "turn-1/call-1" {
		t.Fatalf("failed update = %#v", updates[1]["status"])
	}
}

func TestServePromptSwallowsSessionUpdateWriteErrors(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	fake.run = func(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
		if err := request.Sink.Emit(ctx, engine.RuntimeEvent{Type: engine.RuntimeModelTextDelta, Text: "hello"}); err != nil {
			return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusFailed}, err
		}
		return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusCompleted, Text: "hello", TerminalCommitted: true}, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, dropSessionUpdates{writer: agentOut})
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"hi"}]}}`)
	result := readJSON(t, clientIn)
	if result["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
		t.Fatalf("prompt result = %#v", result)
	}
}

func TestServeLoadAndPromptRejectForeignWorkspace(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	fake.sessions["session-foreign"] = domain.Session{
		ID: "session-foreign", Status: domain.SessionStatusActive, WorkspaceRoot: "/other-workspace",
	}
	fake.history = []domain.RecordedEvent{
		{Event: domain.TurnStarted{TurnID: "turn-1", Input: "hello"}, OccurredAt: time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)},
		{Event: domain.ToolCallStarted{TurnID: "turn-1", ItemID: "item-2", CallID: "call-1", Name: "read_file", Arguments: `{"path":"secret.md"}`}, OccurredAt: time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)},
		{Event: domain.ToolCallCompleted{TurnID: "turn-1", ItemID: "item-2", CallID: "call-1", Content: "classified"}, OccurredAt: time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)},
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/load","params":{"sessionId":"session-foreign"}}`)
	foreignLoad := readJSON(t, clientIn)
	if foreignLoad["method"] == methodSessionUpdate {
		t.Fatalf("foreign load emitted session/update: %#v", foreignLoad)
	}
	if foreignLoad["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("foreign load = %#v", foreignLoad)
	}
	if strings.Contains(mustJSON(foreignLoad), "/other-workspace") {
		t.Fatalf("leaked foreign workspace: %#v", foreignLoad)
	}
	if strings.Contains(mustJSON(foreignLoad), "tool_call") {
		t.Fatalf("foreign load leaked tool card: %#v", foreignLoad)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"session-foreign","prompt":[{"text":"hi"}]}}`)
	foreignPrompt := readJSON(t, clientIn)
	if foreignPrompt["method"] == methodSessionUpdate {
		t.Fatalf("foreign prompt emitted session/update: %#v", foreignPrompt)
	}
	if foreignPrompt["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("foreign prompt = %#v", foreignPrompt)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":5,"method":"session/prompt","params":{"sessionId":"missing","prompt":[{"text":"hi"}]}}`)
	unknown := readJSON(t, clientIn)
	if unknown["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("unknown prompt = %#v", unknown)
	}

	fake.mu.Lock()
	runs, reads := fake.runs, fake.reads
	fake.mu.Unlock()
	if runs != 0 {
		t.Fatalf("RunTurn calls = %d, want 0", runs)
	}
	if reads != 0 {
		t.Fatalf("ReadStream calls = %d, want 0", reads)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":6,"method":"session/load","params":{"sessionId":"`+sessionID+`"}}`)
	sawToolCard := false
	loadUpdates := 0
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			loadUpdates++
			update := message["params"].(map[string]any)["update"].(map[string]any)
			if update["sessionUpdate"] == "tool_call" || update["sessionUpdate"] == "tool_call_update" {
				sawToolCard = true
			}
			continue
		}
		if message["id"] != float64(6) || message["error"] != nil {
			t.Fatalf("same-workspace load = %#v", message)
		}
		break
	}
	if loadUpdates != 3 {
		t.Fatalf("same-workspace load updates = %d, want 3", loadUpdates)
	}
	if !sawToolCard {
		t.Fatal("same-workspace load of tool-bearing history produced no tool cards")
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":7,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"hi"}]}}`)
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			continue
		}
		if message["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
			t.Fatalf("same-workspace prompt = %#v", message)
		}
		break
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.runs != 1 {
		t.Fatalf("RunTurn calls = %d, want 1", fake.runs)
	}
}

func TestServeInitializeAdvertisesSessionLifecycleCapabilities(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: newFake(), Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`)
	init := readJSON(t, clientIn)
	want := map[string]any{
		"protocolVersion": float64(1),
		"agentCapabilities": map[string]any{
			"loadSession": true,
			"promptCapabilities": map[string]any{
				"image": false, "audio": false, "embeddedContext": false,
			},
			"sessionCapabilities": map[string]any{
				"list": map[string]any{}, "resume": map[string]any{}, "close": map[string]any{}, "delete": map[string]any{},
			},
		},
		"agentInfo":   map[string]any{"name": agentName, "version": agentVersion},
		"authMethods": []any{},
	}
	if !reflect.DeepEqual(init["result"], want) {
		t.Fatalf("initialize result = %#v, want %#v", init["result"], want)
	}
}

func TestServeSessionListReturnsWorkspaceSessions(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	fake.sessions["session-a"] = domain.Session{ID: "session-a", Status: domain.SessionStatusActive, WorkspaceRoot: "/workspace"}
	fake.sessions["session-foreign"] = domain.Session{ID: "session-foreign", Status: domain.SessionStatusActive, WorkspaceRoot: "/other-workspace"}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/list","params":{"cwd":"/other-workspace"}}`)
	foreign := readJSON(t, clientIn)
	if foreign["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("foreign cwd list = %#v", foreign)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/list","params":{"cwd":"/workspace"}}`)
	listed := readJSON(t, clientIn)
	result, _ := listed["result"].(map[string]any)
	sessions, _ := result["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v, want 1 workspace-scoped session", sessions)
	}
	entry, _ := sessions[0].(map[string]any)
	if entry["sessionId"] != "session-a" || entry["cwd"] != "/workspace" {
		t.Fatalf("entry = %#v", entry)
	}
	updatedAt, _ := entry["updatedAt"].(string)
	if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		t.Fatalf("updatedAt = %q, want RFC3339Nano: %v", updatedAt, err)
	}
	if _, ok := result["nextCursor"]; ok {
		t.Fatalf("nextCursor present with no more pages: %#v", result)
	}
	if strings.Contains(mustJSON(listed), "/other-workspace") {
		t.Fatalf("list leaked a foreign-workspace session: %#v", listed)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/list","params":{}}`)
	assemblyDefault := readJSON(t, clientIn)
	defaultResult, _ := assemblyDefault["result"].(map[string]any)
	if len(defaultResult["sessions"].([]any)) != 1 {
		t.Fatalf("default-workspace list = %#v, want 1", defaultResult)
	}
}

func TestServeSessionResumeReattachesAndRejectsIneligible(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	fake.sessions["session-active"] = domain.Session{ID: "session-active", Status: domain.SessionStatusActive, WorkspaceRoot: "/workspace"}
	fake.sessions["session-closed"] = domain.Session{ID: "session-closed", Status: domain.SessionStatusClosed, WorkspaceRoot: "/workspace"}
	fake.sessions["session-foreign"] = domain.Session{ID: "session-foreign", Status: domain.SessionStatusActive, WorkspaceRoot: "/other-workspace"}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/resume","params":{"sessionId":"missing","cwd":"/workspace"}}`)
	if missing := readJSON(t, clientIn); missing["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("resume missing = %#v", missing)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/resume","params":{"sessionId":"session-closed","cwd":"/workspace"}}`)
	if closedResp := readJSON(t, clientIn); closedResp["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("resume closed = %#v", closedResp)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/resume","params":{"sessionId":"session-foreign","cwd":"/workspace"}}`)
	foreignResp := readJSON(t, clientIn)
	if foreignResp["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("resume foreign = %#v", foreignResp)
	}
	if strings.Contains(mustJSON(foreignResp), "/other-workspace") {
		t.Fatalf("resume leaked foreign workspace: %#v", foreignResp)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":5,"method":"session/resume","params":{"sessionId":"session-active","cwd":"/workspace","mcpServers":[{"name":"x"}]}}`)
	if mcpRejected := readJSON(t, clientIn); mcpRejected["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("resume with non-empty mcpServers = %#v", mcpRejected)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":6,"method":"session/resume","params":{"sessionId":"session-active","cwd":"/workspace","mcpServers":[],"additionalDirectories":[]}}`)
	resumed := readJSON(t, clientIn)
	if resumed["method"] == methodSessionUpdate {
		t.Fatalf("resume emitted a replay notification: %#v", resumed)
	}
	if resumed["error"] != nil || len(resumed["result"].(map[string]any)) != 0 {
		t.Fatalf("resume active = %#v", resumed)
	}
	fake.mu.Lock()
	reads := fake.reads
	fake.mu.Unlock()
	if reads != 0 {
		t.Fatalf("resume read history = %d calls, want 0", reads)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":7,"method":"session/prompt","params":{"sessionId":"session-active","prompt":[{"text":"hi"}]}}`)
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			continue
		}
		if message["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
			t.Fatalf("prompt after resume = %#v", message)
		}
		break
	}
}

// TestServeSessionCloseCancelsSettlesAndDetaches proves close-cancel-terminal
// ordering (the prompt's terminal frame settles before close's own result),
// that ACP close never mutates durable Session status, that a detached
// session rejects a prompt until reattached, that duplicate close is
// rejected, and that load reattaches a detached session to idle.
func TestServeSessionCloseCancelsSettlesAndDetaches(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	blocked := make(chan struct{})
	fake.run = func(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
		close(blocked)
		<-ctx.Done()
		return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusInterrupted}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"hi"}]}}`)
	<-blocked
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/close","params":{"sessionId":"`+sessionID+`"}}`)

	promptTerminal := readJSON(t, clientIn)
	if promptTerminal["id"] != float64(3) || promptTerminal["result"].(map[string]any)["stopReason"] != stopReasonCancelled {
		t.Fatalf("cancelled prompt terminal frame = %#v", promptTerminal)
	}
	closeResult := readJSON(t, clientIn)
	if closeResult["id"] != float64(4) || closeResult["error"] != nil {
		t.Fatalf("close result = %#v", closeResult)
	}
	if len(closeResult["result"].(map[string]any)) != 0 {
		t.Fatalf("close result body = %#v, want an empty object", closeResult["result"])
	}

	fake.mu.Lock()
	status := fake.sessions[domain.SessionID(sessionID)].Status
	fake.mu.Unlock()
	if status != domain.SessionStatusActive {
		t.Fatalf("session status after ACP close = %q, want active: close must never append session.closed", status)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":5,"method":"session/close","params":{"sessionId":"`+sessionID+`"}}`)
	if dup := readJSON(t, clientIn); dup["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("duplicate close = %#v", dup)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":6,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"hi"}]}}`)
	if detachedPrompt := readJSON(t, clientIn); detachedPrompt["error"].(map[string]any)["code"] != float64(codeInvalidRequest) {
		t.Fatalf("detached prompt = %#v", detachedPrompt)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":7,"method":"session/load","params":{"sessionId":"`+sessionID+`"}}`)
	if reloaded := readJSON(t, clientIn); reloaded["error"] != nil {
		t.Fatalf("reload after close = %#v", reloaded)
	}

	fake.mu.Lock()
	fake.run = nil
	fake.mu.Unlock()
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":8,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"hi"}]}}`)
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			continue
		}
		if message["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
			t.Fatalf("prompt after reattach = %#v", message)
		}
		break
	}
}

// TestServeSessionLifecycleRejectsDuringClosingAndDeleting proves that while
// an entry is closing, resume/load/delete are all rejected, and that close
// itself still settles once the cancelled prompt's terminal frame is
// published.
func TestServeSessionLifecycleRejectsDuringClosingAndDeleting(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	blocked := make(chan struct{})
	releaseRun := make(chan struct{})
	fake.run = func(ctx context.Context, request application.RunTurnRequest) (application.RunTurnResult, error) {
		close(blocked)
		<-ctx.Done()
		// Held open until the test has driven resume/load/delete through the
		// closing window, so the entry is deterministically still closing
		// rather than racing finishClose's own completion.
		<-releaseRun
		return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusInterrupted}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"hi"}]}}`)
	<-blocked
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/close","params":{"sessionId":"`+sessionID+`"}}`)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":5,"method":"session/resume","params":{"sessionId":"`+sessionID+`","cwd":"/workspace"}}`)
	if resumeRejected := readJSON(t, clientIn); resumeRejected["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("resume during closing = %#v", resumeRejected)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":6,"method":"session/load","params":{"sessionId":"`+sessionID+`"}}`)
	if loadRejected := readJSON(t, clientIn); loadRejected["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("load during closing = %#v", loadRejected)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":7,"method":"session/delete","params":{"sessionId":"`+sessionID+`"}}`)
	if deleteRejected := readJSON(t, clientIn); deleteRejected["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("delete during closing = %#v", deleteRejected)
	}

	close(releaseRun)
	promptTerminal := readJSON(t, clientIn)
	if promptTerminal["id"] != float64(3) || promptTerminal["result"].(map[string]any)["stopReason"] != stopReasonCancelled {
		t.Fatalf("cancelled prompt = %#v", promptTerminal)
	}
	closeResult := readJSON(t, clientIn)
	if closeResult["id"] != float64(4) || closeResult["error"] != nil {
		t.Fatalf("close result = %#v", closeResult)
	}
}

// TestServeSessionDeleteBlocksPromptEntryAndIsIdempotent proves that once
// deleting is installed under the mutex, a prompt cannot enter before the
// Application call returns, and that absent, foreign, and already-deleted
// sessions are all indistinguishable idempotent successes.
func TestServeSessionDeleteBlocksPromptEntryAndIsIdempotent(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	fake.sessions["session-foreign"] = domain.Session{ID: "session-foreign", Status: domain.SessionStatusActive, WorkspaceRoot: "/other-workspace"}
	releaseDelete := make(chan struct{})
	deleteEntered := make(chan struct{})
	var enterOnce sync.Once
	fake.del = func(_ context.Context, req application.DeleteSessionRequest) error {
		enterOnce.Do(func() { close(deleteEntered) })
		<-releaseDelete
		fake.mu.Lock()
		defer fake.mu.Unlock()
		session, ok := fake.sessions[req.SessionID]
		if !ok || session.WorkspaceRoot != req.WorkspaceRoot || session.Status == domain.SessionStatusDeleted {
			return sessionNotFoundError()
		}
		session.Status = domain.SessionStatusDeleted
		fake.sessions[req.SessionID] = session
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/delete","params":{"sessionId":"`+sessionID+`"}}`)
	<-deleteEntered

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"hi"}]}}`)
	blockedPrompt := readJSON(t, clientIn)
	if blockedPrompt["id"] != float64(4) || blockedPrompt["error"].(map[string]any)["code"] != float64(codeInvalidRequest) {
		t.Fatalf("prompt admitted while deleting = %#v", blockedPrompt)
	}

	close(releaseDelete)
	deleteResult := readJSON(t, clientIn)
	if deleteResult["id"] != float64(3) || deleteResult["error"] != nil {
		t.Fatalf("delete result = %#v", deleteResult)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":5,"method":"session/delete","params":{"sessionId":"`+sessionID+`"}}`)
	if dup := readJSON(t, clientIn); dup["error"] != nil {
		t.Fatalf("duplicate delete of an already-deleted session = %#v", dup)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":6,"method":"session/delete","params":{"sessionId":"session-missing"}}`)
	if missingResult := readJSON(t, clientIn); missingResult["error"] != nil {
		t.Fatalf("delete of an absent session = %#v", missingResult)
	}
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":7,"method":"session/delete","params":{"sessionId":"session-foreign"}}`)
	if foreignResult := readJSON(t, clientIn); foreignResult["error"] != nil {
		t.Fatalf("delete of a foreign-workspace session = %#v", foreignResult)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":8,"method":"session/load","params":{"sessionId":"`+sessionID+`"}}`)
	if loadDeleted := readJSON(t, clientIn); loadDeleted["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Fatalf("load of a deleted session = %#v", loadDeleted)
	}
}

// TestServeSessionDeleteRestoresStateAfterInternalFailure proves that an
// internal (non-validation) DeleteSession failure restores the entry to its
// exact prior idle state rather than leaving it stuck deleting.
func TestServeSessionDeleteRestoresStateAfterInternalFailure(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	fake.del = func(context.Context, application.DeleteSessionRequest) error {
		return &application.Error{Category: application.CategoryPersistence, Code: "list_failed"}
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	created := readJSON(t, clientIn)
	sessionID := created["result"].(map[string]any)["sessionId"].(string)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/delete","params":{"sessionId":"`+sessionID+`"}}`)
	failed := readJSON(t, clientIn)
	failedErr, _ := failed["error"].(map[string]any)
	if failedErr["code"] != float64(codeInternalError) || failedErr["message"] != sessionOperationFailedMessage {
		t.Fatalf("delete internal failure = %#v", failed)
	}
	if strings.Contains(mustJSON(failed), sessionID) {
		t.Fatalf("delete internal failure leaked the session id: %#v", failed)
	}

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"`+sessionID+`","prompt":[{"text":"hi"}]}}`)
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			continue
		}
		if message["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
			t.Fatalf("prompt after a failed delete = %#v, want the entry restored to idle", message)
		}
		break
	}
}

// TestServeSessionLifecycleErrorsDoNotLeakDetails checks the fixed,
// non-leaking error strings the lifecycle methods use for validation and
// internal failures: no session ID, workspace root, or lifecycle state name
// ever appears in a rejection.
func TestServeSessionLifecycleErrorsDoNotLeakDetails(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	fake.sessions["session-foreign"] = domain.Session{ID: "session-foreign", Status: domain.SessionStatusActive, WorkspaceRoot: "/other-workspace"}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		_ = agentIn.Close()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
		<-done
	})
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_ = readJSON(t, clientIn)

	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":2,"method":"session/resume","params":{"sessionId":"session-foreign","cwd":"/workspace"}}`)
	resumeRejected := readJSON(t, clientIn)
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":3,"method":"session/close","params":{"sessionId":"session-foreign"}}`)
	closeRejected := readJSON(t, clientIn)

	for _, rejected := range []map[string]any{resumeRejected, closeRejected} {
		payload := mustJSON(rejected)
		for _, leak := range []string{"session-foreign", "/other-workspace", "/workspace"} {
			if strings.Contains(payload, leak) {
				t.Fatalf("rejection leaked %q: %s", leak, payload)
			}
		}
	}
}

type dropSessionUpdates struct {
	writer io.Writer
}

func (w dropSessionUpdates) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"method":"session/update"`)) {
		return 0, errors.New("session update write failed")
	}
	return w.writer.Write(p)
}
