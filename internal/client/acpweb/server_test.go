package acpweb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
)

func newTestServer(t *testing.T) (baseURL, token string, stdoutW io.WriteCloser, stdinR io.Reader) {
	t.Helper()
	stdoutR, stdoutWriter := io.Pipe()
	stdinReader, stdinW := io.Pipe()
	relay := newRelayFromPipes(stdoutR, stdinW, func() error { return nil })
	t.Cleanup(func() { _ = stdoutWriter.Close() })

	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>ok</html>")},
	}
	token = "test-token"
	s := NewServer(relay, assets, Config{Cwd: "/workspace"}, token)

	ts := httptest.NewUnstartedServer(s.Handler())
	s.SetSelfOrigin("http://" + ts.Listener.Addr().String())
	ts.Start()
	t.Cleanup(ts.Close)

	return ts.URL, token, stdoutWriter, stdinReader
}

func dialWS(t *testing.T, baseURL, token, origin string) *websocket.Conn {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Scheme = "ws"
	u.Path = "/ws"
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func TestServerRelaysOverRealWebSocketConnection(t *testing.T) {
	baseURL, token, stdoutW, stdinR := newTestServer(t)
	conn := dialWS(t, baseURL, token, baseURL)
	defer conn.CloseNow()

	// Dial returns as soon as the WebSocket handshake completes, which can be
	// just before the server handler installs the connection in the Relay. A
	// client-to-agent round trip makes that activation observable before this
	// test writes agent stdout, which is intentionally dropped without an
	// active connection.
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte("relay-ready")); err != nil {
		t.Fatalf("write readiness frame: %v", err)
	}
	line, err := readLine(stdinR, testTimeout)
	if err != nil {
		t.Fatalf("read readiness frame: %v", err)
	}
	if line != "relay-ready\n" {
		t.Fatalf("got %q, want %q", line, "relay-ready\n")
	}

	if _, err := stdoutW.Write([]byte("hello-browser\n")); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read ws: %v", err)
	}
	if string(data) != "hello-browser" {
		t.Fatalf("got %q, want %q", data, "hello-browser")
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte("hello-agent")); err != nil {
		t.Fatalf("write ws: %v", err)
	}
	line, err = readLine(stdinR, testTimeout)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if line != "hello-agent\n" {
		t.Fatalf("got %q, want %q", line, "hello-agent\n")
	}
}

func TestServerSecondConnectionTakesOverFirst(t *testing.T) {
	baseURL, token, stdoutW, _ := newTestServer(t)
	first := dialWS(t, baseURL, token, baseURL)
	second := dialWS(t, baseURL, token, baseURL)
	defer second.CloseNow()

	// The first connection must be closed by the server once superseded.
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if _, _, err := first.Read(ctx); err == nil {
		t.Fatal("expected the first connection to be closed once superseded")
	}

	if _, err := stdoutW.Write([]byte("to-second\n")); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), testTimeout)
	defer cancel2()
	_, data, err := second.Read(ctx2)
	if err != nil {
		t.Fatalf("read ws: %v", err)
	}
	if string(data) != "to-second" {
		t.Fatalf("got %q, want %q", data, "to-second")
	}
}

func TestServerOlderConnectionCannotActivateAfterNewerConnection(t *testing.T) {
	relay, _, _ := newTestRelay(t)
	s := NewServer(relay, nil, Config{}, "test-token")

	olderID := s.beginConnection()
	newerID := s.beginConnection()
	newer := newFakeConn()
	if previous, activated := s.activateConnection(newerID, newer); !activated || previous != nil {
		t.Fatalf("activate newer connection: activated=%v previous=%T", activated, previous)
	}

	older := newFakeConn()
	if previous, activated := s.activateConnection(olderID, older); activated || previous != nil {
		t.Fatalf("activate older connection: activated=%v previous=%T", activated, previous)
	}

	relay.mu.Lock()
	active := relay.active
	relay.mu.Unlock()
	if active != Conn(newer) {
		t.Fatalf("active connection = %T, want newer connection", active)
	}
}

func TestServerRejectsUpgradeWithBadTokenOrMismatchedOrigin(t *testing.T) {
	baseURL, token, _, _ := newTestServer(t)

	u, _ := url.Parse(baseURL)
	u.Scheme = "ws"
	u.Path = "/ws"

	cases := []struct {
		name   string
		token  string
		origin string
	}{
		{"wrong token", "wrong-token", baseURL},
		{"missing token", "", baseURL},
		{"mismatched origin", token, "http://evil.example"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := u.Query()
			q.Set("token", c.token)
			u.RawQuery = q.Encode()

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			header := http.Header{}
			if c.origin != "" {
				header.Set("Origin", c.origin)
			}
			_, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: header})
			if err == nil {
				t.Fatal("expected the upgrade to be rejected")
			}
			if resp != nil && resp.StatusCode != http.StatusForbidden {
				t.Fatalf("got status %d, want %d", resp.StatusCode, http.StatusForbidden)
			}
		})
	}
}

func readLine(r io.Reader, timeout time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 0, 256)
		one := make([]byte, 1)
		for {
			n, err := r.Read(one)
			if n > 0 {
				buf = append(buf, one[0])
				if one[0] == '\n' {
					ch <- result{string(buf), nil}
					return
				}
			}
			if err != nil {
				ch <- result{"", err}
				return
			}
		}
	}()
	select {
	case res := <-ch:
		return res.line, res.err
	case <-time.After(timeout):
		return "", io.ErrUnexpectedEOF
	}
}
