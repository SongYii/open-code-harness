package composition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/anthropic"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/mcp"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/runtime"
)

type closeableModel interface {
	engine.Model
	io.Closer
}

func resourceModel(t *testing.T, kind, endpoint string, client *http.Client) closeableModel {
	t.Helper()
	if kind == "messages" {
		m, err := anthropic.New(anthropic.Config{BaseURL: endpoint, ModelID: "test-model", APIKey: "fixture-only", ContextWindow: 8192, MaxOutput: 1024, AllowInsecureLoopback: true, HTTPClient: client})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	m, err := openaicompat.New(openaicompat.Config{BaseURL: endpoint, ModelID: "test-model", APIKey: openaicompat.StaticAPIKey{Value: "fixture-only"}, Profile: openaicompat.ProfileToolsSupported(8192, 1024), AllowInsecureLoopback: true, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func providerResourceWire(kind string) string {
	if kind == "messages" {
		return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"test-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"ok\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	}
	return "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
}

// Observe actual keep-alive sockets, not a counter in a mocked Close. The
// source client's existing pool must survive closing its cloned model pool.
func TestBuiltinProviderCloseOwnsOnlyPrivateConnections(t *testing.T) {
	for _, kind := range []string{"chat", "messages"} {
		for _, sourceKind := range []string{"default", "cloned"} {
			t.Run(kind+"/"+sourceKind, func(t *testing.T) {
				closed := make(chan string, 8)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, providerResourceWire(kind))
				}))
				server.Config.ConnState = func(c net.Conn, state http.ConnState) {
					if state == http.StateClosed {
						closed <- c.RemoteAddr().String()
					}
				}
				server.Start()
				defer server.Close()
				var source *http.Client
				if sourceKind == "cloned" {
					transport := &http.Transport{}
					source = &http.Client{Transport: transport}
					defer transport.CloseIdleConnections()
					response, err := source.Get(server.URL)
					if err != nil {
						t.Fatal(err)
					}
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
				}
				model := resourceModel(t, kind, server.URL, source)
				defer model.Close()
				var localAddress string
				ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { localAddress = info.Conn.LocalAddr().String() }})
				stream, err := model.Stream(ctx, engine.ModelRequest{Input: "hello"})
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				for {
					event, err := stream.Next(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if event.Type == engine.StreamEventCompleted {
						break
					}
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				select {
				case address := <-closed:
					t.Fatalf("positive control: socket %s closed before model teardown", address)
				default:
				}
				if err := model.Close(); err != nil {
					t.Fatal(err)
				}
				select {
				case address := <-closed:
					if address != localAddress {
						t.Fatal("closed somebody else's socket")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("owned idle socket survived model Close")
				}
				var wg sync.WaitGroup
				for range 10 {
					wg.Go(func() {
						if err := model.Close(); err != nil {
							t.Error(err)
						}
					})
				}
				wg.Wait()
				if stream, err := model.Stream(context.Background(), engine.ModelRequest{Input: "after close"}); err == nil || stream != nil {
					t.Fatal("closed model reopened")
				} else {
					var failure *engine.ProviderFailure
					if !errors.As(err, &failure) || failure.Retryable || failure.Class != engine.FailureClassPermanent {
						t.Fatal("closed model rejection must be permanent")
					}
				}
				if source != nil {
					reused := false
					ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }})
					request, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
					response, err := source.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					if !reused {
						t.Fatal("closing model destroyed borrowed source pool")
					}
				}
			})
		}
	}
}

type borrowedProviderTransport struct{ closes atomic.Int32 }

func (*borrowedProviderTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected request")
}
func (b *borrowedProviderTransport) CloseIdleConnections() { b.closes.Add(1) }
func TestBuiltinProviderCloseDoesNotCloseBorrowedTransport(t *testing.T) {
	for _, kind := range []string{"chat", "messages"} {
		t.Run(kind, func(t *testing.T) {
			transport := &borrowedProviderTransport{}
			m := resourceModel(t, kind, "https://provider.invalid", &http.Client{Transport: transport})
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			if transport.closes.Load() != 0 {
				t.Fatal("borrowed custom transport was closed")
			}
		})
	}
}

type providerCloseProbe struct {
	close func() error
	calls atomic.Int32
}

func (p *providerCloseProbe) Close() error { p.calls.Add(1); return p.close() }

func TestAssemblyProviderCloseAfterDrainBeforeLeaseRelease(t *testing.T) {
	config := internalValidConfig(t)
	config.AllowUnsandboxedExec = true
	config.Diagnostics = io.Discard
	t.Setenv(config.Provider.APIKeyEnv, "fixture-only")
	assembly, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer assembly.Close()
	if assembly.provider == nil {
		t.Fatal("Open did not register Provider ownership")
	}
	owned := assembly.provider
	store, err := assembly.host.Store()
	if err != nil {
		t.Fatal(err)
	}
	work, finish, err := assembly.host.Admit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	entered := make(chan struct{})
	probe := &providerCloseProbe{close: func() error {
		close(entered)
		// Must still hold ownership, not merely retain an open DB handle.
		if err := store.RenewLease(context.Background()); err != nil {
			t.Errorf("lease released before Provider: %v", err)
		}
		return owned.Close()
	}}
	assembly.provider = probe
	closed := make(chan error, 1)
	go func() { closed <- assembly.Close() }()
	select {
	case <-work.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("admission not cancelled")
	}
	select {
	case <-entered:
		t.Fatal("Provider closed before active call drained")
	default:
	}
	finish()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if probe.calls.Load() != 1 {
		t.Fatal("Provider not closed exactly once")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := assembly.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if probe.calls.Load() != 1 {
		t.Fatal("concurrent Close repeated Provider teardown")
	}
	successor, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	successor.Close()
}

func TestAssemblyProviderUnprovenTeardownRetainsLease(t *testing.T) {
	for _, mode := range []string{"failure", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			config := internalValidConfig(t)
			config.AllowUnsandboxedExec = true
			config.Diagnostics = io.Discard
			config.ShutdownTimeout = 100 * time.Millisecond
			t.Setenv(config.Provider.APIKeyEnv, "fixture-only")
			assembly, err := Open(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			store, err := assembly.host.Store()
			if err != nil {
				t.Fatal(err)
			}
			owned := assembly.provider
			release, finished := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			probe := &providerCloseProbe{close: func() error {
				defer close(finished)
				if mode == "timeout" {
					<-release
					return owned.Close()
				}
				return errors.New("fixture teardown failure")
			}}
			assembly.provider = probe
			defer func() { unblock(); <-finished; owned.Close(); _ = assembly.host.Shutdown(context.Background()) }()
			err = assembly.Close()
			if err == nil || assembly.Ready() {
				t.Fatal("unproven cleanup announced success")
			}
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if again := assembly.Close(); again != err {
				t.Fatal("Close did not retain first failure")
			}
			if probe.calls.Load() != 1 {
				t.Fatal("Provider close repeated")
			}
			// Match-pair release would make this fail even though the file exists.
			if err := store.RenewLease(context.Background()); err != nil {
				t.Fatalf("unproven cleanup released/closed store: %v", err)
			}
			config.RuntimeID = "successor"
			if next, err := Open(context.Background(), config); err == nil {
				next.Close()
				t.Fatal("unproven cleanup allowed successor")
			} else {
				var held *runtime.ErrLeaseHeld
				if !errors.As(err, &held) {
					t.Fatalf("wrong successor failure: %v", err)
				}
			}
			unblock()
			<-finished
			if again := assembly.Close(); again != err || probe.calls.Load() != 1 {
				t.Fatal("late Provider cleanup changed the terminal Close result")
			}
		})
	}
}

func TestProviderStartupRollbackAllowsHealthySuccessor(t *testing.T) {
	for _, kind := range []string{"openaicompat", "deepseek-messages"} {
		t.Run(kind, func(t *testing.T) {
			config := internalValidConfig(t)
			config.AllowUnsandboxedExec = true
			config.Diagnostics = io.Discard
			config.Provider.AdapterKind = kind
			t.Setenv(config.Provider.APIKeyEnv, "fixture-only")
			config.MCPServers = []mcp.ServerConfig{{Name: "missing", Command: filepath.Join(config.WorkspaceRoot, "no-such-provider-test-command")}}
			if a, err := Open(context.Background(), config); a != nil || err == nil || !strings.Contains(err.Error(), "mcp") {
				t.Fatalf("expected post-Provider construction failure: %v", err)
			}
			config.MCPServers = nil
			a, err := Open(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
