package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
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
	if f.run != nil {
		return f.run(ctx, request)
	}
	_ = request.Sink.Emit(ctx, engine.RuntimeEvent{Type: engine.RuntimeModelTextDelta, Text: "hello"})
	return application.RunTurnResult{SessionID: request.SessionID, Status: domain.TurnStatusCompleted, Text: "hello", TerminalCommitted: true}, nil
}

func (f *fakeSessions) ReadStream(context.Context, application.ReadStreamRequest) (application.StreamPage, error) {
	return application.StreamPage{Records: append([]domain.RecordedEvent(nil), f.history...), End: true}, nil
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
	fake.sessions = map[domain.SessionID]domain.Session{"session-acp-1": {ID: "session-acp-1", Status: domain.SessionStatusActive}}
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
		answer, err := slot.Decide(ctx, tools.ApprovalRequest{SessionID: request.SessionID, CallID: "call-1", Name: "write_file"})
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
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":`+mustJSON(perm["id"])+`,"result":{"outcome":{"outcome":"selected","optionId":"allow-once"}}}`)
	final := readJSON(t, clientIn)
	if final["result"].(map[string]any)["stopReason"] != stopReasonEndTurn {
		t.Fatalf("granted prompt = %#v", final)
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
