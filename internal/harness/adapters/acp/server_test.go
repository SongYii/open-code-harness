package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	sessions map[domain.SessionID]domain.Session
	run      func(context.Context, application.RunTurnRequest) (application.RunTurnResult, error)
	history  []domain.RecordedEvent
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
	if !ok {
		return domain.Session{}, &application.Error{Category: application.CategoryValidation, Code: "session_not_found"}
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
	loadUpdates := 0
	for {
		message := readJSON(t, clientIn)
		if message["method"] == methodSessionUpdate {
			loadUpdates++
			continue
		}
		if message["id"] != float64(6) || message["error"] != nil {
			t.Fatalf("same-workspace load = %#v", message)
		}
		break
	}
	if loadUpdates != 1 {
		t.Fatalf("same-workspace load updates = %d, want 1", loadUpdates)
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

type dropSessionUpdates struct {
	writer io.Writer
}

func (w dropSessionUpdates) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"method":"session/update"`)) {
		return 0, errors.New("session update write failed")
	}
	return w.writer.Write(p)
}
