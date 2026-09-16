package composition

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"
)

func TestProviderCustomTLSDialRetainsHTTP2AndSourcePool(t *testing.T) {
	for _, kind := range []string{"chat", "messages"} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy=%t", kind, legacy), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				closed := make(chan string, 8)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.ProtoMajor != 2 {
						t.Errorf("custom TLS dial downgraded to HTTP%d", r.ProtoMajor)
					}
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, providerResourceWire(kind))
				}))
				server.EnableHTTP2 = true
				server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
					if state == http.StateClosed {
						closed <- conn.RemoteAddr().String()
					}
				}
				server.StartTLS()
				defer server.Close()
				client := server.Client()
				client.Timeout = 10 * time.Second
				transport := client.Transport.(*http.Transport)
				transport.ForceAttemptHTTP2 = true
				tlsConfig := transport.TLSClientConfig.Clone()
				tlsConfig.NextProtos = []string{"h2"}
				dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: tlsConfig}
				if legacy {
					transport.DialTLS = func(network, address string) (net.Conn, error) { return dialer.DialContext(ctx, network, address) }
				} else {
					transport.DialTLSContext = dialer.DialContext
				}
				defer client.CloseIdleConnections()
				get := func(checkReuse bool) {
					reused := false
					request, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}), "GET", server.URL, nil)
					response, err := client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
					if response.ProtoMajor != 2 || checkReuse && !reused {
						t.Fatal("source H2 pool was changed or closed")
					}
				}
				get(false)
				model := resourceModel(t, kind, server.URL, client)
				defer model.Close()
				address := ""
				work := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { address = info.Conn.LocalAddr().String() }})
				stream, err := model.Stream(work, isolationRequest("A"))
				if err != nil {
					t.Fatal(err)
				}
				result := consumeIsolation(work, stream)
				if result.err != nil || result.completed == nil {
					t.Fatalf("custom TLS stream failed: %v", result.err)
				}
				if err := model.Close(); err != nil {
					t.Fatal(err)
				}
				if got := awaitIsolation(t, ctx, closed); got != address {
					t.Fatalf("closed wrong custom TLS socket: %q != %q", got, address)
				}
				get(true)
			})
		}
	}
}
