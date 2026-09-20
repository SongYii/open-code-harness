// Package httpresource owns connections of private Provider HTTP transports.
// It has no protocol, model, routing or application policy.
package httpresource

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"runtime"
	"sync"
)

// Connections must be attached to a private, unused transport, never a caller's
// source transport. Close runs after application requests drain. Unlike an idle
// sweep, it also closes sockets whose canceled H2 streams are still retiring.
type Connections struct {
	transport *http.Transport
	mu        sync.Mutex
	closed    bool
	conns     map[*connection]struct{}
	once      sync.Once
	err       error
	lifetime  context.Context
	cancel    context.CancelFunc
	dials     sync.WaitGroup
	lateErr   error
}

// Own leaves nonstandard RoundTrippers borrowed. For standard transports the
// caller must already have created/cloned the transport before calling Own.
func Own(rt http.RoundTripper) *Connections {
	t, ok := rt.(*http.Transport)
	if !ok || t == nil {
		return nil
	}
	o := &Connections{transport: t, conns: make(map[*connection]struct{})}
	o.lifetime, o.cancel = context.WithCancel(context.Background())
	dial := t.DialContext
	if dial == nil {
		if legacy := t.Dial; legacy != nil {
			dial = func(_ context.Context, network, address string) (net.Conn, error) { return legacy(network, address) }
		} else {
			dial = (&net.Dialer{}).DialContext
		}
	}
	t.DialContext = o.wrap(dial, false)
	if t.DialTLSContext != nil {
		t.DialTLSContext = o.wrap(t.DialTLSContext, true)
	} else if legacy := t.DialTLS; legacy != nil {
		t.DialTLSContext = o.wrap(func(_ context.Context, network, address string) (net.Conn, error) { return legacy(network, address) }, true)
	}
	return o
}

func (o *Connections) wrap(dial func(context.Context, string, string) (net.Conn, error), tlsDial bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		o.mu.Lock()
		closed := o.closed
		if !closed {
			o.dials.Add(1)
		}
		o.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		defer o.dials.Done()
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(o.lifetime, cancel)
		defer func() { stop(); cancel() }()
		conn, err := dial(ctx, network, address)
		if conn == nil {
			if err != nil {
				return nil, err
			}
			return nil, errors.New("httpresource: dial returned no connection")
		}
		tracked := &connection{Conn: conn, owner: o}
		tlsConn, isTLS := conn.(*tls.Conn)
		if tlsDial && isTLS {
			// Keep the concrete TLS value visible to net/http's H2 negotiation,
			// but own its underlying socket without retaining the TLS object.
			tracked.Conn = tlsConn.NetConn()
		}
		o.mu.Lock()
		closed = o.closed
		if !closed {
			o.conns[tracked] = struct{}{}
		}
		o.mu.Unlock()
		if closed {
			closeErr := tracked.Close()
			o.mu.Lock()
			o.lateErr = errors.Join(o.lateErr, closeErr)
			o.mu.Unlock()
			return nil, errors.Join(net.ErrClosed, closeErr)
		}
		if err != nil {
			return nil, errors.Join(err, tracked.Close())
		}
		if tlsDial && isTLS {
			// Reclaim bookkeeping when a custom TLS connection is discarded.
			// Shutdown does NOT depend on GC: the owner still holds and closes
			// the underlying socket synchronously, even if cleanup never runs.
			runtime.AddCleanup(tlsConn, func(c *connection) { _ = c.Close() }, tracked)
			return conn, nil
		}
		return tracked, nil
	}
}

func (o *Connections) Close() error {
	if o == nil {
		return nil
	}
	o.once.Do(func() {
		o.mu.Lock()
		o.closed = true
		pending := make([]*connection, 0, len(o.conns))
		for conn := range o.conns {
			pending = append(pending, conn)
		}
		o.conns = nil
		o.mu.Unlock()
		o.cancel()
		// Also cancel transport-owned pending dials. A dial already returning
		// is guarded above and cannot repopulate a closed model's pool.
		o.transport.CloseIdleConnections()
		for _, conn := range pending {
			o.err = errors.Join(o.err, conn.Close())
		}
		// A custom dial may return after cancellation. Its socket must be
		// closed (or its failure reported) BEFORE shutdown claims success.
		// Composition's shared leaf deadline bounds an uncooperative dial.
		o.dials.Wait()
		o.err = errors.Join(o.err, o.lateErr)
	})
	return o.err
}

type connection struct {
	net.Conn
	owner *Connections
	once  sync.Once
	err   error
}

func (c *connection) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		if errors.Is(c.err, net.ErrClosed) {
			c.err = nil
		}
		if c.err == nil {
			c.owner.mu.Lock()
			delete(c.owner.conns, c)
			c.owner.mu.Unlock()
		}
	})
	return c.err
}
