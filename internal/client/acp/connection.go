package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// errConnectionClosed is returned by Call, and used to fail every pending
// waiter, once the read loop has stopped for any reason.
var errConnectionClosed = errors.New("acp: connection closed")

// Connection is one NDJSON JSON-RPC 2.0 duplex over an agent's stdio: it
// owns exactly one background read-loop goroutine dispatching inbound
// traffic to a Handler, and offers Call/Notify for outbound traffic. It
// takes an io.ReadCloser rather than a plain io.Reader specifically so
// Close can unblock a read the loop is currently blocked in — an io.Pipe
// pair (both ends are io.ReadCloser/io.WriteCloser) is enough for every
// test of this type; a real subprocess's stdio pipes work the same way.
type Connection struct {
	writer  *frameWriter
	reader  io.Closer
	handler Handler

	mu       sync.Mutex
	waiters  map[string]chan message
	closing  bool
	nextID   uint64
	readDone chan struct{}
}

// NewConnection starts the read loop immediately; the caller must not read
// from r itself afterward.
func NewConnection(r io.ReadCloser, w io.Writer, handler Handler) *Connection {
	c := &Connection{
		writer:   newFrameWriter(w),
		reader:   r,
		handler:  handler,
		waiters:  make(map[string]chan message),
		readDone: make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

func (c *Connection) readLoop(r io.Reader) {
	defer close(c.readDone)
	decodeErr := decodeFrames(r, c.dispatch)

	c.mu.Lock()
	waiters := c.waiters
	c.waiters = nil
	c.mu.Unlock()

	failure := errConnectionClosed
	if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		failure = fmt.Errorf("acp: %w", decodeErr)
	}
	for _, ch := range waiters {
		ch <- message{Error: &rpcError{Message: failure.Error()}}
		close(ch)
	}
}

func (c *Connection) dispatch(m message) error {
	switch {
	case m.isResponse():
		key := idKey(m.ID)
		c.mu.Lock()
		ch := c.waiters[key]
		if ch != nil {
			delete(c.waiters, key)
		}
		c.mu.Unlock()
		if ch != nil {
			ch <- m
			close(ch)
		}
		return nil
	case m.isRequest():
		c.dispatchRequest(m)
		return nil
	case m.isNotification():
		if m.Method == methodSessionUpdate {
			c.handler.HandleSessionUpdate(m.Params)
		}
		return nil
	}
	return nil
}

// dispatchRequest answers the one inbound call this client accepts,
// session/request_permission, on its own goroutine so a slow or blocked
// answer never stalls the read loop's delivery of unrelated
// session/update notifications. Any other inbound method is answered
// immediately with "method not found" rather than left to hang the agent
// waiting for a response this client will never send.
func (c *Connection) dispatchRequest(m message) {
	if m.Method != methodRequestPermission {
		_ = c.writer.writeMessage(message{
			JSONRPC: jsonRPCVersion,
			ID:      m.ID,
			Error:   &rpcError{Code: codeMethodNotFound, Message: "method not found"},
		})
		return
	}
	go func() {
		result, err := c.handler.HandleRequestPermission(context.Background(), m.Params)
		if err != nil {
			_ = c.writer.writeMessage(message{
				JSONRPC: jsonRPCVersion,
				ID:      m.ID,
				Error:   &rpcError{Code: codeInternalError, Message: err.Error()},
			})
			return
		}
		_ = c.writer.writeMessage(message{JSONRPC: jsonRPCVersion, ID: m.ID, Result: result})
	}()
}

// Call sends an outbound JSON-RPC request and blocks for its matching
// response, or until ctx is done.
func (c *Connection) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("acp: encode %s params: %w", method, err)
	}
	id := c.newID()
	key := idKey(id)
	ch := make(chan message, 1)

	c.mu.Lock()
	if c.waiters == nil {
		c.mu.Unlock()
		return nil, errConnectionClosed
	}
	c.waiters[key] = ch
	c.mu.Unlock()

	if err := c.writer.writeMessage(message{JSONRPC: jsonRPCVersion, ID: id, Method: method, Params: payload}); err != nil {
		c.forgetWaiter(key)
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.forgetWaiter(key)
		return nil, ctx.Err()
	}
}

func (c *Connection) forgetWaiter(key string) {
	c.mu.Lock()
	if c.waiters != nil {
		delete(c.waiters, key)
	}
	c.mu.Unlock()
}

// Notify sends an outbound JSON-RPC notification; there is no response to
// wait for.
func (c *Connection) Notify(method string, params any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("acp: encode %s params: %w", method, err)
	}
	return c.writer.writeMessage(message{JSONRPC: jsonRPCVersion, Method: method, Params: payload})
}

func (c *Connection) newID() json.RawMessage {
	n := atomic.AddUint64(&c.nextID, 1)
	return json.RawMessage(strconv.FormatUint(n, 10))
}

func idKey(id json.RawMessage) string { return string(id) }

// Close stops the read loop (by closing the reader out from under it, so
// its blocked Read returns) and waits for it to finish failing every
// outstanding waiter before returning. Close is idempotent.
func (c *Connection) Close() error {
	c.mu.Lock()
	alreadyClosing := c.closing
	c.closing = true
	c.mu.Unlock()

	var err error
	if !alreadyClosing {
		err = c.reader.Close()
	}
	<-c.readDone
	return err
}
