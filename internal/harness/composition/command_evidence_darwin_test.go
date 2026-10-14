//go:build darwin

package composition_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

type commandEvidenceSink func(context.Context, engine.RuntimeEvent) error

func (sink commandEvidenceSink) Emit(ctx context.Context, event engine.RuntimeEvent) error {
	return sink(ctx, event)
}

// Real HTTP/SSE provider fixture -> approval -> command -> SQLite -> live
// notification -> close/reopen -> idempotent request replay. No model service.
func TestAssemblyPipeDrainFailurePersistsAcrossReopen(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "fixture-key")
	config.Approver = testkit.NewScriptedApprover(testkit.ScriptedApproval{Answer: tools.ApprovalAnswer{Granted: true}})
	realWorkspace, err := filepath.EvalSymlinks(config.WorkspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	holder := filepath.Join(realWorkspace, "unique-pipe-holder")
	pidFile := filepath.Join(config.WorkspaceRoot, "holder.pid")
	t.Cleanup(func() {
		data, _ := os.ReadFile(pidFile)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 {
			args, _ := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
			if strings.Contains(string(args), holder) {
				data, _ := os.ReadFile(filepath.Join(config.WorkspaceRoot, "leader.pid"))
				leader, _ := strconv.Atoi(strings.TrimSpace(string(data)))
				if group, err := syscall.Getpgid(pid); err == nil && group == leader && leader > 0 {
					_ = syscall.Kill(-group, syscall.SIGKILL)
				}
			}
		}
	})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = readAll(r)
		requestNumber := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		if requestNumber > 1 {
			write(w, flusher, `{"choices":[{"delta":{"content":"unexpected continuation"},"finish_reason":null}]}`)
			write(w, flusher, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
			write(w, flusher, "[DONE]")
			return
		}
		// Keep the leader present through monitor registration; its real side effect
		// is deliberately complete before output collection fails.
		args := `{"argv":["sh","-c","echo $$ > leader.pid; sh -c 'sleep 15; :' \"$PWD/unique-pipe-holder\" & echo $! > holder.pid; echo done > side-effect.txt; printf partial; sleep 0.2; exit 7"]}`
		write(w, flusher, fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-exec","function":{"name":"exec","arguments":%s}}]},"finish_reason":null}]}`, jsonString(args)))
		write(w, flusher, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		write(w, flusher, "[DONE]")
	}))
	defer server.Close()
	config.Provider.BaseURL, config.Provider.AllowInsecureLoopback = server.URL, true
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	assembly, err := composition.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = assembly.Close() }()
	created, err := assembly.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	notified := false
	sink := commandEvidenceSink(func(ctx context.Context, event engine.RuntimeEvent) error {
		if event.Type == engine.RuntimeToolExecutionCompleted {
			t.Logf("unexpected completed command content: %s", event.Content)
		}
		if event.Type != engine.RuntimeToolExecutionFailed {
			return nil
		}
		records, err := application.ReadWholeStreamPinned(ctx, assembly.Store(), created.SessionID, 256)
		if err != nil {
			return err
		}
		for _, record := range records {
			if failed, ok := record.Event.(domain.TurnFailed); ok && failed.Code == event.Code {
				notified = true
			}
		}
		if !notified {
			t.Error("live command failure preceded its durable Turn failure")
		}
		if event.Code != application.CodeExecOutputIncomplete || event.Content != application.ToolTextExecOutputIncomplete {
			t.Errorf("live failure=%+v", event)
		}
		return nil
	})
	request := application.RunTurnRequest{SessionID: created.SessionID, RequestID: "pipe-drain-request", Input: "run the fixture once", Sink: sink}
	result, runErr := assembly.Service().RunTurn(ctx, request)
	var typed *application.Error
	if !errors.As(runErr, &typed) || typed.Category != application.CategoryInternal || typed.Code != application.CodeExecOutputIncomplete || result.Status != domain.TurnStatusFailed || !result.TerminalCommitted {
		t.Fatalf("status=%s terminal=%v error=%v", result.Status, result.TerminalCommitted, runErr)
	}
	data, err := os.ReadFile(filepath.Join(config.WorkspaceRoot, "side-effect.txt"))
	if err != nil || string(data) != "done\n" {
		t.Fatalf("real side effect missing: %q %v", data, err)
	}
	if !notified || requests.Load() != 1 {
		t.Fatalf("notified=%v provider requests=%d", notified, requests.Load())
	}
	if err := assembly.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := composition.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	request.Sink = discardSink{}
	replayed, replayErr := fresh.Service().RunTurn(ctx, request)
	if !errors.As(replayErr, &typed) || typed.Category != application.CategoryInternal || typed.Code != application.CodeExecOutputIncomplete || replayed.Status != domain.TurnStatusFailed || !replayed.TerminalCommitted {
		t.Fatalf("replayed status=%s terminal=%v error=%v", replayed.Status, replayed.TerminalCommitted, replayErr)
	}
	if requests.Load() != 1 {
		t.Fatal("durable failure re-executed the Provider/command")
	}
	records, err := application.ReadWholeStreamPinned(ctx, fresh.Store(), created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, record := range records {
		counts[record.Event.EventType()]++
	}
	if counts[domain.EventToolCallFailed] != 1 || counts[domain.EventTurnFailed] != 1 || counts[domain.EventToolCallCompleted] != 0 || counts[domain.EventApprovalRequested] != 1 || counts[domain.EventApprovalResolved] != 1 {
		t.Fatalf("durable event counts=%v", counts)
	}
	t.Logf("real side effect retained; durable=%s live=%s; Provider requests=%d after reopen; approval resolved once", typed.Code, application.CodeExecOutputIncomplete, requests.Load())
}
