package httpresource

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countedConn struct {
	net.Conn
	closes atomic.Int32
	err    error
}

func (c *countedConn) Close() error { c.closes.Add(1); _ = c.Conn.Close(); return c.err }

func newPair(t *testing.T) (*countedConn, net.Conn) {
	t.Helper()
	client, peer := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	return &countedConn{Conn: client}, peer
}

func assertPeerClosed(t *testing.T, peer net.Conn) {
	t.Helper()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var data [1]byte
	if _, err := peer.Read(data[:]); err != io.EOF {
		t.Fatalf("owned socket survived Close: %v", err)
	}
}

func TestCloseOwnsConnectionsNotYetIdle(t *testing.T) {
	conn, peer := newPair(t)
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil }}
	owner := Own(transport)
	got, err := transport.DialContext(t.Context(), "tcp", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	// The socket has never entered net/http's idle pool. This deterministically
	// checks the ownership obligation that a canceled H2 stream exposes.
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if err := owner.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	assertPeerClosed(t, peer)
	if conn.closes.Load() != 1 {
		t.Fatal("socket closed more than once")
	}
	if _, err := transport.DialContext(t.Context(), "tcp", "fixture"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed owner dialed again: %v", err)
	}
}

func TestLateDialCannotRepopulateClosedOwner(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var err error
			if fail {
				err = errors.New("fixture late close failure")
			}
			testLateDial(t, err)
		})
	}
}

func testLateDial(t *testing.T, closeFailure error) {
	conn, peer := newPair(t)
	conn.err = closeFailure
	entered, release := make(chan struct{}), make(chan struct{})
	cancelled := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		return conn, nil
	}}
	owner := Own(transport)
	done := make(chan error, 1)
	go func() { _, err := transport.DialContext(t.Context(), "tcp", "fixture"); done <- err }()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- owner.Close() }()
	<-cancelled
	select {
	case err := <-closed:
		t.Fatalf("owner returned before its pending dial: %v", err)
	case <-time.After(20 * time.Millisecond): // Assert blocking, not a production delay.
	}
	unblock()
	if err := <-done; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("late dial escaped owner: %v", err)
	}
	assertPeerClosed(t, peer)
	if err := <-closed; !errors.Is(err, closeFailure) {
		t.Fatalf("late cleanup error lost: %v", err)
	}
}

func TestTransportClosedConnectionsAreForgotten(t *testing.T) {
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { c, _ := newPair(t); return c, nil }}
	owner := Own(transport)
	defer owner.Close()
	for range 100 {
		conn, err := transport.DialContext(t.Context(), "tcp", "fixture")
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(owner.conns) != 0 {
		t.Fatal("closed ordinary connections accumulated")
	}
}

func TestCloseFailureIsCachedEvenAfterTransportClose(t *testing.T) {
	conn, _ := newPair(t)
	sentinel := errors.New("fixture socket close failure")
	conn.err = sentinel
	transport := &http.Transport{Dial: func(string, string) (net.Conn, error) { return conn, nil }}
	owner := Own(transport)
	tracked, err := transport.DialContext(t.Context(), "tcp", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := tracked.Close(); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	first := owner.Close()
	if !errors.Is(first, sentinel) || owner.Close() != first {
		t.Fatalf("cleanup failure was lost: %v", first)
	}
	if conn.closes.Load() != 1 {
		t.Fatal("failed close retried implicitly")
	}
}

type borrowedTransport struct{ calls int }

func (*borrowedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unused")
}
func (b *borrowedTransport) CloseIdleConnections() { b.calls++ }
func TestNonstandardTransportRemainsBorrowed(t *testing.T) {
	b := &borrowedTransport{}
	owner := Own(b)
	if owner != nil {
		t.Fatal("borrowed transport acquired an owner")
	}
	if err := owner.Close(); err != nil || b.calls != 0 {
		t.Fatalf("borrowed transport closed: %v", err)
	}
}

func TestCustomTLSBookkeepingDoesNotRetainDiscardedTLSObjects(t *testing.T) {
	conn, _ := newPair(t)
	closed := make(chan struct{})
	transport := &http.Transport{DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
		return tls.Client(conn, &tls.Config{ServerName: "fixture.invalid"}), nil
	}}
	owner := Own(transport)
	defer owner.Close()
	func() {
		tlsConn, err := transport.DialTLSContext(t.Context(), "tcp", "fixture")
		if err != nil {
			t.Fatal(err)
		}
		typed, ok := tlsConn.(*tls.Conn)
		if !ok {
			t.Fatal("custom TLS type hidden from net/http")
		}
		runtime.AddCleanup(typed, func(chanDone chan struct{}) { close(chanDone) }, closed)
		_ = tlsConn.Close()
	}()
	runtime.GC()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("owner retained discarded TLS object")
	}
	// Independent cleanups have no ordering guarantee. Explicit shutdown is
	// still the deterministic release boundary, regardless of GC scheduling.
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}
