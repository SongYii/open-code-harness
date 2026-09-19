package och_test

import (
	"bufio"
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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

func TestExternalToolPolicyDeniesExecAndPreservesEvidenceBoundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs external launcher")
	}
	_, file, _, _ := runtime.Caller(0)
	example := filepath.Join(filepath.Dir(file), "..", "..", "examples", "deny-tools")
	root := t.TempDir()
	binary := filepath.Join(root, "deny-tools-och")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, ".")
	build.Dir = example
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("external tool-policy module build: %v\n%s", err, output)
	}

	marker := filepath.Join(root, "exec-ran")
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if providerCalls.Add(1) == 1 {
			arguments, _ := json.Marshal(fmt.Sprintf(`{"argv":["sh","-c","printf ran > %s"]}`, marker))
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_exec\",\"function\":{\"name\":\"exec\",\"arguments\":%s}}]},\"finish_reason\":null}]}\n\n", arguments)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"continued-after-denial\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()

	database := filepath.Join(root, "runtime.db")
	command := exec.CommandContext(ctx, binary,
		"-acp", "-workspace", root, "-database", database, "-runtime-id", "external-tool-policy-test",
		"-provider-url", provider.URL, "-provider-allow-insecure-loopback", "-model", "fixture",
		"-api-key-env", "OCH_EXTERNAL_TOOL_POLICY_KEY", "-context-window", "4096", "-max-output", "512",
		"-tool-policy", "deny_tools", "-tool-policy-version", "1.0.0", "-tool-policy-config", `{"names":["exec"]}`,
		"-allow-unsandboxed-exec",
	)
	command.Env = append(os.Environ(), "OCH_EXTERNAL_TOOL_POLICY_KEY=fixture-key")
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
	client := externalACPClient{t: t, input: input, scanner: bufio.NewScanner(output), command: command, diagnostics: &diagnostics, waited: &waited}
	client.scanner.Buffer(make([]byte, 4096), 1<<20)
	client.call(1, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}, nil)
	created := client.call(2, "session/new", map[string]any{"cwd": root}, nil)
	var session struct {
		ID string `json:"sessionId"`
	}
	if err := json.Unmarshal(created, &session); err != nil || session.ID == "" {
		t.Fatalf("new session: %s", created)
	}
	var promptUpdates []json.RawMessage
	client.call(3, "session/prompt", map[string]any{"sessionId": session.ID, "prompt": []any{map[string]any{"type": "text", "text": "run the command"}}}, &promptUpdates)
	if providerCalls.Load() != 2 {
		t.Fatalf("provider calls = %d, want tool request plus continued turn", providerCalls.Load())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("denied exec created marker: %v", err)
	}
	if !bytes.Contains(joinRaw(promptUpdates), []byte("continued-after-denial")) {
		t.Fatalf("turn did not continue after denial: %s", joinRaw(promptUpdates))
	}
	assertNoPolicyDecisionProjection(t, "live ACP updates", joinRaw(promptUpdates))

	var loadUpdates []json.RawMessage
	client.call(4, "session/load", map[string]any{"sessionId": session.ID, "cwd": root, "mcpServers": []any{}, "additionalDirectories": []any{}}, &loadUpdates)
	assertNoPolicyDecisionProjection(t, "ACP load updates", joinRaw(loadUpdates))

	_ = input.Close()
	if err := command.Wait(); err != nil {
		t.Fatalf("launcher shutdown: %v\n%s", err, diagnostics.String())
	}
	waited = true

	inspection, err := composition.InspectEvaluationStore(ctx, database, domain.SessionID(session.ID))
	if err != nil {
		t.Fatal(err)
	}
	evidenceRoot := t.TempDir()
	transcriptPath := filepath.Join(evidenceRoot, "transcript.jsonl")
	auditDirectory := filepath.Join(evidenceRoot, "audit")
	if _, err := composition.ExportEvaluationEvidence(ctx, inspection, composition.EvaluationExportDestinations{TranscriptPath: transcriptPath, AuditDirectory: auditDirectory}); err != nil {
		t.Fatal(err)
	}
	transcript, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoPolicyDecisionProjection(t, "transcript", transcript)
	snapshot, err := composition.VerifyAuditSnapshot(auditDirectory)
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := toolpolicy.CanonicalConfig(json.RawMessage(`{"names":["exec"]}`))
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := domain.ToolPolicyIdentity{ID: "deny_tools", Version: "1.0.0", ConfigDigest: digest}
	found := false
	for _, auditSession := range snapshot.Sessions {
		if auditSession.SessionID != session.ID {
			continue
		}
		for _, record := range auditSession.Events {
			decision, ok := record.Event.(domain.PolicyDecisionRecorded)
			if !ok || decision.Name != "exec" {
				continue
			}
			if decision.Policy == nil || *decision.Policy != wantIdentity || decision.Effect != domain.PolicyEffectDeny || decision.RuleID != "deny_tools.configured_name" || decision.Reason != "configured_deny" {
				t.Fatalf("durable exec policy decision = %#v", decision)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("canonical audit contains no attributed exec denial")
	}
}

type externalACPClient struct {
	t           *testing.T
	input       io.WriteCloser
	scanner     *bufio.Scanner
	command     *exec.Cmd
	diagnostics *bytes.Buffer
	waited      *bool
}

func (client externalACPClient) call(id int, method string, params any, notifications *[]json.RawMessage) json.RawMessage {
	client.t.Helper()
	frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := fmt.Fprintln(client.input, string(frame)); err != nil {
		client.t.Fatal(err)
	}
	for client.scanner.Scan() {
		line := append(json.RawMessage(nil), client.scanner.Bytes()...)
		var response struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &response); err != nil {
			client.t.Fatalf("non-ACP stdout: %s", line)
		}
		if response.Method != "" {
			if notifications != nil {
				*notifications = append(*notifications, line)
			}
			continue
		}
		if response.ID != id {
			continue
		}
		if len(response.Error) > 0 {
			client.t.Fatalf("ACP %s id=%d: %s\n%s", method, id, response.Error, client.diagnostics.String())
		}
		return response.Result
	}
	_ = client.input.Close()
	waitErr := client.command.Wait()
	*client.waited = true
	client.t.Fatalf("ACP ended before %s: %v wait=%v\n%s", method, client.scanner.Err(), waitErr, client.diagnostics.String())
	return nil
}

func joinRaw(values []json.RawMessage) []byte {
	return []byte(strings.Join(func() []string {
		joined := make([]string, len(values))
		for index := range values {
			joined[index] = string(values[index])
		}
		return joined
	}(), "\n"))
}

func assertNoPolicyDecisionProjection(t *testing.T, surface string, data []byte) {
	t.Helper()
	if bytes.Contains(data, []byte(domain.EventPolicyDecisionRecorded)) {
		t.Fatalf("%s exposed internal policy decision: %s", surface, data)
	}
}
