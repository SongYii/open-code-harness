package och_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A separate module is essential: same-module tests would not catch leaked
// internal types/imports. Drive its real ACP subprocess through compaction,
// then reopen read-only and verify durable attribution, not console output.
func TestExternalLauncherACPAndPolicyEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("builds external launcher")
	}
	_, file, _, _ := runtime.Caller(0)
	example := filepath.Join(filepath.Dir(file), "..", "..", "examples", "keep-last-n")
	root := t.TempDir()
	binary := filepath.Join(root, "custom-och")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, ".")
	build.Dir = example
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("external module build: %v\n%s", err, output)
	}
	summary := strings.Join([]string{"## Objective", "Continue.", "## User Constraints", "None.", "## Established Facts", "History exists.", "## Work Completed", "Prior work.", "## Files and Commands", "None.", "## Open Work", "Continue.", "## Risks and Unknowns", "None.", "## Continuation", "Proceed."}, "\n")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": summary}}}})
		fmt.Fprintf(w, "data: %s\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", delta)
	}))
	defer provider.Close()
	database := filepath.Join(root, "runtime.db")
	command := exec.CommandContext(ctx, binary, "-acp", "-workspace", root, "-database", database, "-runtime-id", "external-test", "-provider-url", provider.URL, "-provider-allow-insecure-loopback", "-model", "fixture", "-api-key-env", "OCH_EXTERNAL_TEST_KEY", "-context-window", "4096", "-max-output", "512", "-context-tail-percent", "10", "-context-policy", "keep_last_n_turns", "-context-policy-version", "1.0.0", "-context-policy-config", `{"turns":1}`, "-allow-unsandboxed-exec")
	command.Env = append(os.Environ(), "OCH_EXTERNAL_TEST_KEY=fixture-key")
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	rpc := func(id int, method string, params any) json.RawMessage {
		t.Helper()
		frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := fmt.Fprintln(input, string(frame)); err != nil {
			t.Fatal(err)
		}
		for scanner.Scan() {
			var response struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
				t.Fatalf("non-ACP stdout: %s", scanner.Bytes())
			}
			if response.ID != id {
				continue
			}
			if len(response.Error) > 0 {
				_ = input.Close()
				waitErr := command.Wait()
				waited = true
				export := exec.CommandContext(ctx, binary, "export-session", "-database", database, "-session", sessionIDForDiagnostics(params))
				evidence, exportErr := export.CombinedOutput()
				t.Fatalf("ACP %s id=%d: %s wait=%v\n%s\nexport=%v\n%s", method, id, response.Error, waitErr, diagnostics.String(), exportErr, evidence)
			}
			return response.Result
		}
		_ = input.Close()
		waitErr := command.Wait()
		waited = true
		t.Fatalf("ACP ended before %s: %v wait=%v\n%s", method, scanner.Err(), waitErr, diagnostics.String())
		return nil
	}
	rpc(1, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}})
	created := rpc(2, "session/new", map[string]any{"cwd": root})
	var session struct {
		ID string `json:"sessionId"`
	}
	if err := json.Unmarshal(created, &session); err != nil || session.ID == "" {
		t.Fatalf("new session: %s", created)
	}
	for index := 0; index < 10; index++ {
		rpc(3+index, "session/prompt", map[string]any{"sessionId": session.ID, "prompt": []any{map[string]any{"type": "text", "text": fmt.Sprintf("continue task %d", index)}}})
	}
	_ = input.Close()
	err = command.Wait()
	waited = true
	if err != nil {
		t.Fatalf("launcher shutdown: %v\n%s", err, diagnostics.String())
	}
	export := exec.CommandContext(ctx, binary, "export-session", "-database", database, "-session", session.ID)
	evidence, err := export.Output()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var prepared, compacted bool
	for _, line := range bytes.Split(evidence, []byte("\n")) {
		if !bytes.Contains(line, []byte(`"keep_last_n_turns"`)) {
			continue
		}
		if bytes.Contains(line, []byte(`"context.prepared"`)) {
			prepared = true
		}
		if bytes.Contains(line, []byte(`"context.compaction.started"`)) {
			compacted = true
		}
	}
	if !prepared || !compacted {
		t.Fatalf("missing durable policy attribution prepared=%t compacted=%t\n%s", prepared, compacted, evidence)
	}
}

func sessionIDForDiagnostics(params any) string {
	if object, ok := params.(map[string]any); ok {
		if id, ok := object["sessionId"].(string); ok {
			return id
		}
	}
	return "unknown"
}
