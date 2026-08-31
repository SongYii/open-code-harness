package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRunRequiresAgentFlag(t *testing.T) {
	err := run([]string{"-cwd", t.TempDir()}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "-agent") {
		t.Fatalf("run() err = %v, want a missing -agent error", err)
	}
}

func TestRunRequiresCwdFlag(t *testing.T) {
	err := run([]string{"-agent", "/bin/true"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "-cwd") {
		t.Fatalf("run() err = %v, want a missing -cwd error", err)
	}
}

func TestRunRejectsNonLoopbackListenHost(t *testing.T) {
	err := run([]string{"-agent", "/bin/true", "-cwd", t.TempDir(), "-listen", "0.0.0.0:0"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("run() err = %v, want a 127.0.0.1-only error", err)
	}
}

var readyLineRE = regexp.MustCompile(`^acp-web-bridge: ready at (http://[^\s]+)\n$`)

// TestRunSpawnsRealSubprocessAndRelaysOverRealWebSocket drives the real
// run() entrypoint (main() calls this directly) against a real OS
// subprocess (a trivial line-echoing shell script standing in for an ACP
// agent) and a real WebSocket client — proving the whole wiring (flags,
// net.Listen, the embedded frontend asset, Server, Relay, and a real
// exec.Command subprocess) end to end. This is not the full Task 8
// browser-driven interoperability proof against the real och binary; it
// is the Go-level wiring proof this task's own scope covers.
func TestRunSpawnsRealSubprocessAndRelaysOverRealWebSocket(t *testing.T) {
	stderrR, stderrW := writePipe(t)
	go func() {
		_ = run([]string{
			"-agent", "/bin/sh",
			"-cwd", t.TempDir(),
			"--", "-c", `while IFS= read -r line; do echo "$line"; done`,
		}, &bytes.Buffer{}, stderrW)
	}()
	// run() blocks serving until this process exits or is signaled; this
	// test does not exercise shutdown (that is exec.Command-subprocess
	// signal-handling territory, not this task's own scope), so it
	// deliberately does not wait for it to return.
	t.Cleanup(func() { _ = stderrW.Close() })

	readyLine := readLineWithTimeout(t, stderrR, testTimeout)
	m := readyLineRE.FindStringSubmatch(readyLine)
	if m == nil {
		t.Fatalf("got stderr line %q, want a match for %s", readyLine, readyLineRE)
	}
	readyURL := m[1]

	u, err := url.Parse(readyURL)
	if err != nil {
		t.Fatalf("parse ready URL: %v", err)
	}
	token := u.Query().Get("token")
	if token == "" {
		t.Fatal("ready URL has no token")
	}

	wsURL := *u
	wsURL.Scheme = "ws"
	wsURL.Path = "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"ping":1}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != `{"ping":1}` {
		t.Fatalf("got %q, want the echoed line back unchanged", data)
	}
}

// TestRunServesTheRealBuiltFrontendNotThePlaceholder guards against the
// go:embed directive silently falling back to whatever happens to be on
// disk under web/dist at build time: after Task 7's real "npm run build"
// step, "/" must serve the Vite build's actual output, not Task 3's
// original placeholder page.
func TestRunServesTheRealBuiltFrontendNotThePlaceholder(t *testing.T) {
	stderrR, stderrW := writePipe(t)
	go func() {
		_ = run([]string{
			"-agent", "/bin/sh",
			"-cwd", t.TempDir(),
			"--", "-c", `while IFS= read -r line; do echo "$line"; done`,
		}, &bytes.Buffer{}, stderrW)
	}()
	t.Cleanup(func() { _ = stderrW.Close() })

	readyLine := readLineWithTimeout(t, stderrR, testTimeout)
	m := readyLineRE.FindStringSubmatch(readyLine)
	if m == nil {
		t.Fatalf("got stderr line %q, want a match for %s", readyLine, readyLineRE)
	}

	resp, err := http.Get(m[1])
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	text := string(body)
	if strings.Contains(text, "Placeholder asset") {
		t.Fatal("served the Task 3 placeholder page, not the real build — run \"npm run build\" under web/ before building this binary")
	}
	if !strings.Contains(text, `<div id="app">`) {
		t.Fatalf("served page did not contain the expected app root element; got:\n%s", text)
	}
}

const testTimeout = 5 * time.Second

func writePipe(t *testing.T) (*bufio.Reader, *io.PipeWriter) {
	t.Helper()
	r, w := io.Pipe()
	return bufio.NewReader(r), w
}

func readLineWithTimeout(t *testing.T, r *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read line: %v", res.err)
		}
		return res.line
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a line")
		return ""
	}
}
