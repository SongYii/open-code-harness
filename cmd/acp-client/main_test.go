package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunRequiresAgentFlag(t *testing.T) {
	err := run([]string{"-cwd", t.TempDir()}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "-agent") {
		t.Fatalf("run() err = %v, want a missing -agent error", err)
	}
}

func TestRunRequiresCwdFlag(t *testing.T) {
	err := run([]string{"-agent", "/bin/true"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "-cwd") {
		t.Fatalf("run() err = %v, want a missing -cwd error", err)
	}
}

// The two SSE fixtures a scripted provider server plays back, in the same
// format this project's own openaicompat adapter tests use
// (internal/harness/adapters/openaicompat/testdata/sse/*.sse) - a
// write_file tool call, then a final assistant message after the tool
// result comes back.
const writeFileToolCallSSE = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_write","function":{"name":"write_file","arguments":"{\"path\":\"greeting.txt\",\"content\":\"hello from acp-client\"}"}}]},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}

data: [DONE]
`

const finalAssistantMessageSSE = `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":"Done"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}

data: [DONE]
`

func newScriptedProviderServer(t *testing.T) *httptest.Server {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = io.WriteString(w, writeFileToolCallSSE)
			return
		}
		_, _ = io.WriteString(w, finalAssistantMessageSSE)
	}))
	t.Cleanup(server.Close)
	return server
}

// buildOchBinary builds this repository's own real och binary from source
// into a temp directory, so the interoperability test below drives an
// actual, independent OS process - not this repository's own scripted
// test fixtures - which is the entire point of this plan.
func buildOchBinary(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd() // .../cmd/acp-client
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Join(wd, "..", "..")
	binPath := filepath.Join(t.TempDir(), "och")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/och")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/och: %v\n%s", err, out)
	}
	return binPath
}

// TestInteropRealAgentCompletesAnApprovedWriteFile is this plan's
// acceptance proof, not merely another unit test: it spawns this
// repository's own real, independently built och -acp binary as a real OS
// process and drives it through this package's own real run() - the exact
// code main() calls - proving the ACP v1 adapter interoperates with an
// actual, independent client for the first time anywhere in this
// repository's history. The provider itself is a local, scripted,
// keyless HTTP fixture (matching this project's own established testing
// philosophy), not a live model; everything else - the agent subprocess,
// the ACP wire protocol, the workspace filesystem tool, and the
// interactive permission answer - is real.
func TestInteropRealAgentCompletesAnApprovedWriteFile(t *testing.T) {
	ochBin := buildOchBinary(t)
	provider := newScriptedProviderServer(t)

	workspace := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "harness.db")
	t.Setenv("OCH_API_KEY", "test-key")

	agentArgs := []string{
		"-workspace", workspace,
		"-database", dbPath,
		"-runtime-id", "interop-test",
		"-provider-url", provider.URL,
		"-provider-allow-insecure-loopback",
		"-model", "test-model",
		"-context-window", "8192",
		"-max-output", "1024",
		"-allow-unsandboxed-exec",
		"-acp",
	}
	clientArgs := append([]string{"-agent", ochBin, "-cwd", workspace, "--"}, agentArgs...)

	// One prompt line, then the operator's "y" answer to the write_file
	// permission request that prompt triggers - read sequentially off the
	// same shared input, in the order the real interaction produces them.
	operatorInput := strings.NewReader("please write a greeting file\ny\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := run(clientArgs, operatorInput, &stdout, &stderr); err != nil {
		t.Fatalf("run() err = %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "write_file") {
		t.Fatalf("stdout = %q, want the write_file tool call rendered", output)
	}
	if !strings.Contains(output, "[end_turn]") {
		t.Fatalf("stdout = %q, want the turn to reach end_turn", output)
	}

	written, err := os.ReadFile(filepath.Join(workspace, "greeting.txt"))
	if err != nil {
		t.Fatalf("greeting.txt was not written: %v\nstdout:\n%s\nstderr:\n%s", err, output, stderr.String())
	}
	if string(written) != "hello from acp-client" {
		t.Fatalf("greeting.txt content = %q, want the tool call's own argument", written)
	}
}
