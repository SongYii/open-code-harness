package composition

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// Exercise the real signal-owning main, SDK launcher, ACP, Composition and
// SQLite. Socket death after process exit is NOT evidence of Model.Close;
// provider_isolation_test.go checks pool ownership while its process is alive.
func TestStockLauncherProviderInFlightShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs stock launcher")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX SIGTERM/pipe lifecycle runtime test; no Windows runtime claim")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "och")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-mod=readonly", "-o", binary, "./cmd/och")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build stock launcher: %v\n%s", err, out)
	}
	for _, kind := range []string{"chat", "messages"} {
		for _, purpose := range []string{"conversation", "compaction"} {
			for _, stop := range []string{"complete", "eof", "sigterm"} {
				t.Run(kind+"/"+purpose+"/"+stop, func(t *testing.T) { testStockProviderShutdown(t, binary, kind, purpose, stop) })
			}
		}
	}
}

func testStockProviderShutdown(t *testing.T, binary, kind, purpose, stop string) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	entered, cancelled := make(chan string, 1), make(chan struct{}, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var armed atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		requests.Add(1)
		text := "ok"
		if armed.CompareAndSwap(true, false) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			phase := r.Header.Get("X-Och-Request-Purpose")
			if phase == "" {
				phase = "conversation"
			} // Legacy/default attribution is omitted.
			entered <- phase
			select {
			case <-release:
			case <-r.Context().Done():
				cancelled <- struct{}{}
				return
			case <-ctx.Done():
				return
			}
			if purpose == "compaction" {
				var summary strings.Builder
				for _, heading := range []string{"Objective", "User Constraints", "Established Facts", "Work Completed", "Files and Commands", "Open Work", "Risks and Unknowns", "Continuation"} {
					fmt.Fprintf(&summary, "## %s\nSynthetic fixture fact.\n", heading)
				}
				text = summary.String()
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		encoded, _ := json.Marshal(text)
		wire := providerResourceWire(kind)
		field := "content"
		if kind == "messages" {
			field = "text"
		}
		wire = strings.Replace(wire, `"`+field+`":"ok"`, `"`+field+`":`+string(encoded), 1)
		fmt.Fprint(w, wire)
	}))
	defer server.Close()
	defer cancel()
	defer unblock()
	config := internalValidConfig(t)
	config.AllowUnsandboxedExec = true
	config.Diagnostics = io.Discard
	config.Provider.BaseURL = server.URL
	config.Provider.AllowInsecureLoopback = true
	if kind == "messages" {
		config.Provider.AdapterKind = "deepseek-messages"
	}
	t.Setenv(config.Provider.APIKeyEnv, "local-fixture-only")
	seed, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	created, err := seed.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	if purpose == "compaction" {
		for i := 0; i < 2; i++ {
			result, err := seed.Service().RunTurn(ctx, application.RunTurnRequest{SessionID: created.SessionID, RequestID: domain.RunTurnRequestID(fmt.Sprintf("seed-%d", i)), Input: strings.Repeat("synthetic history material ", 160), Sink: &testkit.RecordingSink{}})
			if err != nil || result.Status != domain.TurnStatusCompleted {
				t.Fatalf("seed turn: %v", err)
			}
		}
	}
	baseline := messagesLifecycleRecords(t, seed.Store(), created.SessionID)
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	beforeCalls := requests.Load()
	args := []string{"-acp", "-workspace", config.WorkspaceRoot, "-database", config.DatabasePath, "-runtime-id", "shutdown-child", "-provider-url", server.URL, "-provider-allow-insecure-loopback", "-model", config.Provider.ModelID, "-api-key-env", config.Provider.APIKeyEnv, "-context-window", "8192", "-max-output", "1024", "-allow-unsandboxed-exec"}
	if kind == "messages" {
		args = append(args, "-provider-adapter", "deepseek-messages")
	}
	if purpose == "compaction" {
		args = append(args, "-context-trigger-percent", "60", "-context-target-percent", "30", "-context-tail-percent", "10")
	}
	child := startProviderLauncher(t, ctx, binary, args)
	child.rpc(t, 1, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}})
	child.rpc(t, 2, "session/load", map[string]any{"sessionId": created.SessionID, "cwd": config.WorkspaceRoot, "mcpServers": []any{}})
	armed.Store(true)
	prompt := "Continue this synthetic task."
	if purpose == "compaction" {
		prompt = strings.Repeat("Continue synthetic task. ", 128)
	}
	child.send(t, 3, "session/prompt", map[string]any{"sessionId": created.SessionID, "prompt": []any{map[string]string{"type": "text", "text": prompt}}})
	if got := awaitIsolation(t, ctx, entered); got != purpose {
		t.Fatalf("target phase = %q, want %q", got, purpose)
	}
	switch stop {
	case "complete":
		unblock()
		child.response(t, 3)
		child.input.Close()
	case "eof":
		child.input.Close()
	case "sigterm":
		if err := child.command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
	}
	// This shorter deadline is the actual shutdown assertion, independent of
	// CommandContext's emergency kill. SIGTERM propagates context.Canceled and
	// stock main exits 1; clean EOF is 0. A signal-killed process is not success.
	var exitErr error
	select {
	case exitErr = <-child.exited:
	case <-time.After(8 * time.Second):
		t.Fatal("launcher did not exit within shutdown bound")
	}
	if stop == "sigterm" {
		if exitErr == nil || child.command.ProcessState.ExitCode() != 1 || !strings.Contains(child.diagnostics.String(), "context canceled") {
			t.Fatalf("wrong signal exit: %v\n%s", exitErr, child.diagnostics.String())
		}
	} else if exitErr != nil {
		t.Fatalf("launcher exit: %v\n%s", exitErr, child.diagnostics.String())
	}
	if stop != "complete" {
		awaitIsolation(t, ctx, cancelled)
	}
	beforeRestart := readMessagesCrashDB(t, config.DatabasePath, created.SessionID)
	if len(beforeRestart) < len(baseline) || !reflect.DeepEqual(beforeRestart[:len(baseline)], baseline) {
		t.Fatal("shutdown rewrote committed history")
	}
	// A healthy close must release ownership immediately; no lease edits,
	// expiry sleeps or recovery retries are allowed to make this test pass.
	nextArgs := append([]string(nil), args...)
	for i, arg := range nextArgs {
		if arg == "shutdown-child" {
			nextArgs[i] = "shutdown-successor"
		}
	}
	next := startProviderLauncher(t, ctx, binary, nextArgs)
	next.rpc(t, 1, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}})
	next.rpc(t, 2, "session/load", map[string]any{"sessionId": created.SessionID, "cwd": config.WorkspaceRoot, "mcpServers": []any{}})
	var fresh struct {
		SessionID domain.SessionID `json:"sessionId"`
	}
	if err := json.Unmarshal(next.rpc(t, 3, "session/new", map[string]any{"cwd": config.WorkspaceRoot}), &fresh); err != nil || fresh.SessionID == "" {
		t.Fatal("successor could not create a durable session")
	}
	next.input.Close()
	select {
	case err := <-next.exited:
		if err != nil {
			t.Fatalf("successor exit: %v\n%s", err, next.diagnostics.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("successor shutdown exceeded bound")
	}
	if records := readMessagesCrashDB(t, config.DatabasePath, fresh.SessionID); crashEventCount(records, domain.EventSessionCreated) != 1 {
		t.Fatal("successor creation was not durable")
	}
	recovered := readMessagesCrashDB(t, config.DatabasePath, created.SessionID)
	if len(recovered) < len(beforeRestart) || !reflect.DeepEqual(recovered[:len(beforeRestart)], beforeRestart) {
		t.Fatal("successor rewrote prior facts")
	}
	tail := recovered[len(baseline):]
	state, err := domain.Replay(recovered)
	if err != nil || state.ActiveTurn != nil || state.ContextCompaction != nil {
		t.Fatalf("unfinished shutdown/recovery: %v", err)
	}
	if stop == "complete" {
		if crashEventCount(tail, domain.EventTurnCompleted) != 1 {
			t.Fatal("positive control did not complete turn")
		}
		if purpose == "compaction" && crashEventCount(tail, domain.EventContextCompactionCompleted) != 1 {
			t.Fatal("positive control did not complete summary")
		}
	} else {
		for _, kind := range []string{domain.EventAssistantMessageCompleted, domain.EventToolCallStarted, domain.EventContextCompactionCompleted, domain.EventTurnCompleted} {
			if crashEventCount(tail, kind) != 0 {
				t.Fatalf("cancelled request published %s", kind)
			}
		}
		if purpose == "conversation" && crashEventCount(tail, domain.EventTurnInterrupted) != 1 {
			t.Fatal("missing interrupted turn")
		}
		if purpose == "compaction" && crashEventCount(tail, domain.EventContextCompactionFailed) != 1 {
			t.Fatal("missing failed summary")
		}
	}
	wantCalls := int32(1)
	if stop == "complete" && purpose == "compaction" {
		wantCalls = 2
	}
	if requests.Load()-beforeCalls != wantCalls {
		t.Fatalf("unexpected retry/call count: got %d want %d", requests.Load()-beforeCalls, wantCalls)
	}
}

type providerLauncherFrame struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}
type providerLauncher struct {
	ctx         context.Context
	command     *exec.Cmd
	input       io.WriteCloser
	responses   chan providerLauncherFrame
	exited      chan error
	diagnostics bytes.Buffer
}

func startProviderLauncher(t *testing.T, ctx context.Context, binary string, args []string) *providerLauncher {
	t.Helper()
	p := &providerLauncher{ctx: ctx, command: exec.CommandContext(ctx, binary, args...), responses: make(chan providerLauncherFrame, 32), exited: make(chan error, 1)}
	p.command.Env = os.Environ()
	p.command.Stderr = &p.diagnostics
	var err error
	p.input, err = p.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.command.Start(); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan struct{})
	processDone := make(chan struct{})
	go func() {
		defer close(readDone)
		decoder := json.NewDecoder(out)
		for {
			var f providerLauncherFrame
			if decoder.Decode(&f) != nil {
				return
			}
			if f.ID != 0 {
				select {
				case p.responses <- f:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	go func() { p.exited <- p.command.Wait(); close(processDone) }()
	t.Cleanup(func() {
		p.input.Close()
		select {
		case <-processDone:
		default:
			_ = p.command.Process.Kill()
			<-processDone
		}
		<-readDone
	})
	return p
}
func (p *providerLauncher) send(t *testing.T, id int, method string, params any) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(p.input, string(b)); err != nil {
		t.Fatal(err)
	}
}
func (p *providerLauncher) response(t *testing.T, id int) json.RawMessage {
	t.Helper()
	select {
	case f := <-p.responses:
		if f.ID != id || len(f.Error) > 0 {
			t.Fatalf("ACP response: %+v", f)
		}
		return f.Result
	case err := <-p.exited:
		t.Fatalf("launcher exited before response %d: %v\n%s", id, err, p.diagnostics.String())
	case <-p.ctx.Done():
		t.Fatal(p.ctx.Err())
	}
	return nil
}
func (p *providerLauncher) rpc(t *testing.T, id int, method string, params any) json.RawMessage {
	t.Helper()
	p.send(t, id, method, params)
	return p.response(t, id)
}
