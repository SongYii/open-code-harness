// Package acpweb carries ACP v1's existing wire bytes to a browser tab. It
// never parses JSON-RPC, never inspects a method name, and never reduces a
// trajectory: every ACP semantic lives in the browser's own, independent
// TypeScript client. Relay pumps a spawned agent subprocess's NDJSON stdin/
// stdout lines to and from whichever Conn (a WebSocket connection, or a
// fake in a test) is currently active, one line per message in each
// direction, with no buffering across a disconnect.
package acpweb

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// MaxRelayFrameBytes bounds one NDJSON line this package will read from a
// spawned agent's stdout, and is the max-message-size a WebSocket server
// (server.go) configures for frames in both directions. It is deliberately
// larger than ACP v1's own 1 MiB outgoing-frame bound (acp-v1.md's Clip
// bounds table) — headroom against that existing, already-enforced limit,
// not a second independent limit this package polices on its own.
const MaxRelayFrameBytes = 2 << 20 // 2 MiB

// Conn is one active connection to a browser tab, abstracted so Relay can
// be tested without a real network connection. server.go adapts a real
// WebSocket connection to this interface.
type Conn interface {
	// ReadMessage blocks until one text frame arrives, or returns an error
	// (including context cancellation or the peer closing) that ends this
	// connection's lifetime as far as Relay is concerned.
	ReadMessage(ctx context.Context) ([]byte, error)
	// WriteMessage sends one text frame. Relay never calls this
	// concurrently with itself on the same Conn.
	WriteMessage(ctx context.Context, data []byte) error
}

// Relay spawns one ACP v1 agent subprocess and pumps its NDJSON stdout
// lines to, and its stdin lines from, whichever Conn is currently active.
// A line is an opaque byte slice to Relay: it strips exactly the trailing
// newline in each direction and nothing more. At most one Conn is active
// at a time; SetConn replaces it. The subprocess is never restarted or
// otherwise informed when the active Conn changes — from its perspective
// its stdin briefly has no writer, which an ACP v1 agent already handles
// correctly since a slow or thinking client is normal.
type Relay struct {
	stdout io.Reader
	stdin  io.Writer

	closeFn func() error

	mu     sync.Mutex
	active Conn
	gen    uint64 // bumped on every SetConn; lets a superseded read-pump goroutine recognize it and exit without being told directly

	stdoutDone chan struct{}
}

// NewRelay spawns agentPath with agentArgs (the agent's own stderr passes
// through to this process's stderr, matching cmd/acp-client's own
// precedent) and starts pumping its stdout immediately. Until a Conn is
// set via SetConn, stdout lines are simply dropped — this is a live view
// only, never a buffered or replayed one, matching this project's existing
// "session/update write errors are swallowed" precedent for a client with
// nothing currently listening. The caller must call Close when done.
func NewRelay(agentPath string, agentArgs []string) (*Relay, *exec.Cmd, error) {
	cmd := exec.Command(agentPath, agentArgs...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("acpweb: agent stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("acpweb: agent stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("acpweb: start agent: %w", err)
	}
	r := newRelayFromPipes(stdout, stdin, func() error {
		_ = stdin.Close()
		return cmd.Wait()
	})
	return r, cmd, nil
}

// newRelayFromPipes is the lower-level constructor a test uses directly
// with an io.Pipe-backed fake subprocess, skipping exec.Command entirely.
func newRelayFromPipes(stdout io.Reader, stdin io.Writer, closeFn func() error) *Relay {
	r := &Relay{
		stdout:     stdout,
		stdin:      stdin,
		closeFn:    closeFn,
		stdoutDone: make(chan struct{}),
	}
	go r.pumpStdout()
	return r
}

// pumpStdout runs for the lifetime of the Relay, independent of how many
// times the active Conn changes: one goroutine, one subprocess stdout
// stream, for as long as the subprocess lives.
func (r *Relay) pumpStdout() {
	defer close(r.stdoutDone)
	scanner := bufio.NewScanner(r.stdout)
	// Matches internal/client/acp's own decodeFrames technique: the
	// default bufio.Scanner token limit (64 KiB) would truncate a
	// legitimate, large ACP frame with bufio.ErrTooLong. This package
	// never parses the line, but it must not corrupt or drop one either.
	scanner.Buffer(make([]byte, 0, 64*1024), MaxRelayFrameBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		msg := append([]byte(nil), line...) // scanner.Bytes() is reused on the next Scan
		r.mu.Lock()
		conn := r.active
		r.mu.Unlock()
		if conn == nil {
			continue // no browser currently connected; live view only, nothing buffered
		}
		_ = conn.WriteMessage(context.Background(), msg)
	}
}

// SetConn makes conn the active connection: a new goroutine starts reading
// its incoming frames and writing each, one subprocess-stdin line per
// frame, until conn.ReadMessage returns an error or this Relay's active
// Conn changes again. The previous active Conn (if any) is returned so the
// caller can close its underlying network connection; Relay itself never
// closes a Conn — it only stops writing subprocess stdout to it.
func (r *Relay) SetConn(conn Conn) (previous Conn) {
	r.mu.Lock()
	previous = r.active
	r.active = conn
	r.gen++
	myGen := r.gen
	r.mu.Unlock()

	if conn != nil {
		go r.pumpConnIn(conn, myGen)
	}
	return previous
}

// pumpConnIn reads conn's incoming frames and writes each as one
// subprocess-stdin line, until conn errors/closes or myGen is superseded
// by a later SetConn call — checked after every read so a stale goroutine
// from a replaced Conn stops writing to stdin even if its own Conn has not
// yet errored on its end.
func (r *Relay) pumpConnIn(conn Conn, myGen uint64) {
	ctx := context.Background()
	for {
		data, err := conn.ReadMessage(ctx)
		if err != nil {
			return
		}
		r.mu.Lock()
		superseded := r.gen != myGen
		r.mu.Unlock()
		if superseded {
			return
		}
		if _, err := r.stdin.Write(append(data, '\n')); err != nil {
			return
		}
	}
}

// Close stops the subprocess and waits for it to exit, in the same fixed
// order cmd/acp-client already established: close stdin first (so the
// agent sees EOF and can shut down on its own), then wait for exit.
func (r *Relay) Close() error {
	if r.closeFn == nil {
		return nil
	}
	return r.closeFn()
}
