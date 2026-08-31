package acpweb

import (
	"bufio"
	"context"
	"io"
	"testing"
	"time"
)

// fakeConn is a minimal in-memory Conn for testing Relay without a real
// network connection or a real subprocess.
type fakeConn struct {
	incoming chan []byte
	outgoing chan []byte
	closed   chan struct{}
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		incoming: make(chan []byte, 16),
		outgoing: make(chan []byte, 16),
		closed:   make(chan struct{}),
	}
}

func (f *fakeConn) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case msg := <-f.incoming:
		return msg, nil
	case <-f.closed:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeConn) WriteMessage(ctx context.Context, data []byte) error {
	msg := append([]byte(nil), data...)
	select {
	case f.outgoing <- msg:
		return nil
	case <-f.closed:
		return io.ErrClosedPipe
	}
}

func (f *fakeConn) send(msg []byte) { f.incoming <- msg }

func (f *fakeConn) recv(t *testing.T, timeout time.Duration) []byte {
	t.Helper()
	select {
	case msg := <-f.outgoing:
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for outgoing message")
		return nil
	}
}

func (f *fakeConn) expectSilence(t *testing.T, window time.Duration) {
	t.Helper()
	select {
	case msg := <-f.outgoing:
		t.Fatalf("expected no message, got %q", msg)
	case <-time.After(window):
	}
}

func (f *fakeConn) Close() { close(f.closed) }

const testTimeout = 2 * time.Second

// testRelay wires a Relay to an io.Pipe pair standing in for a subprocess:
// stdoutW is written to as if it were the agent's stdout; stdinR is read
// from as if it were the agent reading its own stdin.
func newTestRelay(t *testing.T) (relay *Relay, stdoutW io.WriteCloser, stdinR io.Reader) {
	t.Helper()
	stdoutR, stdoutWriter := io.Pipe()
	stdinReader, stdinW := io.Pipe()
	r := newRelayFromPipes(stdoutR, stdinW, func() error { return nil })
	t.Cleanup(func() { _ = stdoutWriter.Close() })
	return r, stdoutWriter, stdinReader
}

func TestRelayPumpsSubprocessStdoutToActiveConn(t *testing.T) {
	relay, stdoutW, _ := newTestRelay(t)
	conn := newFakeConn()
	relay.SetConn(conn)

	if _, err := stdoutW.Write([]byte("first\nsecond\nthird\n")); err != nil {
		t.Fatalf("write stdout: %v", err)
	}

	for _, want := range []string{"first", "second", "third"} {
		if got := conn.recv(t, testTimeout); string(got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestRelayDropsStdoutWhenNoConnActive(t *testing.T) {
	relay, stdoutW, _ := newTestRelay(t)

	if _, err := stdoutW.Write([]byte("dropped\n")); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	// Give the pump goroutine a chance to observe and drop the line
	// before a Conn is ever attached.
	time.Sleep(50 * time.Millisecond)

	conn := newFakeConn()
	relay.SetConn(conn)
	if _, err := stdoutW.Write([]byte("kept\n")); err != nil {
		t.Fatalf("write stdout: %v", err)
	}

	if got := conn.recv(t, testTimeout); string(got) != "kept" {
		t.Fatalf("got %q, want %q (the pre-connection line must not have been buffered)", got, "kept")
	}
	conn.expectSilence(t, 200*time.Millisecond)
}

func TestRelayWritesIncomingConnFramesToSubprocessStdinWithNewline(t *testing.T) {
	relay, _, stdinR := newTestRelay(t)
	conn := newFakeConn()
	relay.SetConn(conn)

	conn.send([]byte(`{"jsonrpc":"2.0","method":"session/prompt"}`))

	reader := bufio.NewReader(stdinR)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	want := `{"jsonrpc":"2.0","method":"session/prompt"}` + "\n"
	if line != want {
		t.Fatalf("got %q, want %q", line, want)
	}
}

func TestRelayHandlesLargeLineNearMaxFrameBytes(t *testing.T) {
	relay, stdoutW, _ := newTestRelay(t)
	conn := newFakeConn()
	relay.SetConn(conn)

	payload := make([]byte, MaxRelayFrameBytes-64)
	for i := range payload {
		payload[i] = 'x'
	}
	line := append(append([]byte(nil), payload...), '\n')

	go func() {
		if _, err := stdoutW.Write(line); err != nil {
			t.Errorf("write stdout: %v", err)
		}
	}()

	got := conn.recv(t, testTimeout)
	if len(got) != len(payload) {
		t.Fatalf("got length %d, want %d", len(got), len(payload))
	}
	for i, b := range got {
		if b != 'x' {
			t.Fatalf("byte %d corrupted: got %q", i, b)
		}
	}
}

func TestRelayReconnectRewiresActiveConnWithoutTouchingSubprocess(t *testing.T) {
	relay, stdoutW, stdinR := newTestRelay(t)
	connA := newFakeConn()
	relay.SetConn(connA)

	if _, err := stdoutW.Write([]byte("to-a\n")); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	if got := connA.recv(t, testTimeout); string(got) != "to-a" {
		t.Fatalf("got %q, want %q", got, "to-a")
	}

	connB := newFakeConn()
	previous := relay.SetConn(connB)
	if previous != Conn(connA) {
		t.Fatalf("SetConn did not return the previous connection")
	}
	previous.(*fakeConn).Close()

	if _, err := stdoutW.Write([]byte("to-b\n")); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	if got := connB.recv(t, testTimeout); string(got) != "to-b" {
		t.Fatalf("got %q, want %q (reconnect must rewire, not drop, subsequent stdout)", got, "to-b")
	}

	connB.send([]byte("from-b"))
	reader := bufio.NewReader(stdinR)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if line != "from-b\n" {
		t.Fatalf("got %q, want %q (new connection's inbound frames must reach subprocess stdin)", line, "from-b\n")
	}
}
