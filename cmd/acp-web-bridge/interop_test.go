package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// findChromeExecutable locates a real Chrome/Chromium binary this test can
// drive via CDP. CHROME_EXECUTABLE overrides discovery for a host (like
// this project's own CI/dev sandbox) whose browser was not installed at
// one of the conventional binary names below.
func findChromeExecutable() (string, bool) {
	if p := os.Getenv("CHROME_EXECUTABLE"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	return "", false
}

// buildOchBinaryForInterop builds this repository's own real och binary
// from source, mirroring cmd/acp-client's own buildOchBinary — this
// package cannot import that unexported helper across a main-package
// boundary, so it owns a small copy rather than refactoring a shared test
// helper package into existence for one call site.
func buildOchBinaryForInterop(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd() // .../cmd/acp-web-bridge
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

// The same two SSE fixtures cmd/acp-client's own interop test plays back:
// a write_file tool call, then a final assistant message once the tool
// result comes back.
const interopWriteFileToolCallSSE = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_write","function":{"name":"write_file","arguments":"{\"path\":\"greeting.txt\",\"content\":\"hello from acp-web-bridge\"}"}}]},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}

data: [DONE]
`

const interopFinalAssistantMessageSSE = `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":"Done"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}

data: [DONE]
`

func newInteropScriptedProviderServer(t *testing.T) *httptest.Server {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = io.WriteString(w, interopWriteFileToolCallSSE)
			return
		}
		_, _ = io.WriteString(w, interopFinalAssistantMessageSSE)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestInteropRealBrowserCompletesAnApprovedWriteFile is this plan's
// acceptance proof for the whole slice, matching
// cmd/acp-client's TestInteropRealAgentCompletesAnApprovedWriteFile's own
// standard: it builds this repository's own real och binary, spawns it
// through this package's own real run() (the exact code main() calls),
// and drives the served page with a real, independently controlled
// headless Chrome instance over the Chrome DevTools Protocol — not a
// mock, not this repository's own scripted fixtures beyond the model
// provider itself. Everything else is real: the agent subprocess, the
// WebSocket relay, the independent TypeScript ACP v1 client running
// inside that real browser, the interactive permission approval rendered
// by the real UI, and the write_file tool call actually executing
// against a real workspace directory.
func TestInteropRealBrowserCompletesAnApprovedWriteFile(t *testing.T) {
	chromePath, ok := findChromeExecutable()
	if !ok {
		t.Skip("no Chrome/Chromium executable found (set CHROME_EXECUTABLE, or install google-chrome/chromium); skipping the real browser interoperability proof")
	}

	ochBin := buildOchBinaryForInterop(t)
	provider := newInteropScriptedProviderServer(t)

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
	bridgeArgs := append([]string{"-agent", ochBin, "-cwd", workspace, "--"}, agentArgs...)

	stderrR, stderrW := writePipe(t)
	go func() {
		_ = run(bridgeArgs, &bytes.Buffer{}, stderrW)
	}()
	t.Cleanup(func() { _ = stderrW.Close() })

	readyLine := readLineWithTimeout(t, stderrR, testTimeout)
	m := readyLineRE.FindStringSubmatch(readyLine)
	if m == nil {
		t.Fatalf("got stderr line %q, want a match for %s", readyLine, readyLineRE)
	}
	readyURL := m[1]

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(chromePath),
			chromedp.Flag("headless", "new"),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-gpu", true),
		)...,
	)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()
	ctx, cancel := context.WithTimeout(browserCtx, 30*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx,
		chromedp.Navigate(readyURL),
		chromedp.WaitVisible(".composer input", chromedp.ByQuery),
		chromedp.SendKeys(".composer input", "please write a greeting file", chromedp.ByQuery),
		chromedp.Click(".composer button", chromedp.ByQuery),
		chromedp.WaitVisible(".permission-allow", chromedp.ByQuery),
		chromedp.Click(".permission-allow", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("chromedp.Run: %v", err)
	}

	var statusText string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := chromedp.Run(ctx, chromedp.Text("#status", &statusText, chromedp.ByQuery)); err != nil {
			t.Fatalf("read #status: %v", err)
		}
		if strings.Contains(statusText, "end_turn") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(statusText, "end_turn") {
		t.Fatalf("#status = %q after waiting, want it to contain \"end_turn\"", statusText)
	}

	var ledgerText string
	if err := chromedp.Run(ctx, chromedp.Text(".ledger", &ledgerText, chromedp.ByQuery)); err != nil {
		t.Fatalf("read .ledger: %v", err)
	}
	if !strings.Contains(ledgerText, "write_file") {
		t.Fatalf(".ledger text = %q, want the write_file tool call rendered", ledgerText)
	}

	written, err := os.ReadFile(filepath.Join(workspace, "greeting.txt"))
	if err != nil {
		t.Fatalf("greeting.txt was not written: %v\nledger:\n%s", err, ledgerText)
	}
	if string(written) != "hello from acp-web-bridge" {
		t.Fatalf("greeting.txt content = %q, want the tool call's own argument", written)
	}
}
