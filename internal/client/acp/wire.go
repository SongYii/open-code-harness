// Package acp implements the client role of the Agent Client Protocol
// (ACP) v1: it spawns and speaks to an agent, never the other way round.
// Everything in this package takes its I/O as an io.Reader/io.Writer or an
// interface — nothing here touches os.Stdin, os.Stdout, or os.Exec
// directly, so every type is testable against an in-process fake agent
// instead of a real subprocess or a real terminal.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const (
	jsonRPCVersion = "2.0"
	maxFrameBytes  = 1 << 20

	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

// methodSessionUpdate and methodRequestPermission are the only two ACP
// methods this package's wire layer dispatches by name: the one
// notification a Handler receives, and the one inbound call it answers.
// Every other ACP method this package speaks (initialize, session/new,
// session/load, session/prompt, session/cancel) is a plain outbound Call
// or Notify with no dispatch logic of its own.
const (
	methodSessionUpdate     = "session/update"
	methodRequestPermission = "session/request_permission"
)

// Handler receives what an agent sends this client unprompted: a stream of
// session/update notifications, and the one call an agent makes back into
// the client, session/request_permission. HandleSessionUpdate must not
// block for long — the read loop dispatches it inline, in order, on the
// same goroutine that reads every other frame. HandleRequestPermission may
// block indefinitely (a real implementation waits on an operator's
// answer); the read loop dispatches it on its own goroutine so a slow or
// stalled answer never stalls unrelated session/update delivery.
type Handler interface {
	HandleSessionUpdate(params json.RawMessage)
	HandleRequestPermission(ctx context.Context, params json.RawMessage) (result json.RawMessage, err error)
}

// message is the single JSON-RPC 2.0 envelope covering a request, a
// response, and a notification — which fields are set distinguishes the
// three. Mirrors the shape and quality bar of this project's own agent-side
// codec (internal/harness/adapters/acp/codec.go), read directly for
// reference; this package owns a separate copy rather than importing that
// one, per this project's own decision that the framing contract is small
// enough to own on both sides of the wire independently.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (err *rpcError) Error() string { return err.Message }

func (m message) isResponse() bool     { return len(m.ID) > 0 && m.Method == "" }
func (m message) isRequest() bool      { return len(m.ID) > 0 && m.Method != "" }
func (m message) isNotification() bool { return len(m.ID) == 0 && m.Method != "" }

// frameWriter serializes concurrent writers onto one NDJSON stream: one
// JSON value per line, never a literal newline inside a frame, never a
// frame over maxFrameBytes.
type frameWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func newFrameWriter(w io.Writer) *frameWriter {
	return &frameWriter{writer: w}
}

// writeMessage marshals and writes one NDJSON line. There is no separate
// guard against an embedded literal newline: encoding/json never produces
// one for a well-formed message — a json.RawMessage field containing a raw,
// unescaped newline inside a string is invalid JSON and Marshal itself
// fails on it (verified directly, not assumed), and any newline that is
// merely insignificant formatting whitespace (for example a pretty-printed
// nested object) is stripped when Marshal compacts a nested RawMessage.
func (w *frameWriter) writeMessage(m message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("acp: encode frame: %w", err)
	}
	if len(payload)+1 > maxFrameBytes {
		return fmt.Errorf("acp: encoded frame exceeds %d bytes", maxFrameBytes)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.writer.Write(append(payload, '\n'))
	if err != nil {
		return fmt.Errorf("acp: write frame: %w", err)
	}
	return nil
}

// decodeFrames reads r as NDJSON, calling emit once per decoded message
// until r is exhausted, emit returns an error, or a line fails to parse as
// a JSON-RPC message. A malformed line is a hard failure: this package
// talks to a subprocess it spawned itself, not an untrusted network peer,
// so there is no reason to keep parsing past a corrupt frame.
func decodeFrames(r io.Reader, emit func(message) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			return fmt.Errorf("acp: parse error: %w", err)
		}
		if err := emit(m); err != nil {
			return err
		}
	}
	return scanner.Err()
}
