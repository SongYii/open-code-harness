package composition

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/mcp"
	"github.com/SongYii/open-code-harness/internal/harness/runtime"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// This local byte channel has no OS process handle. It exercises the actual
// SDK handshake through the new port, and injects only teardown uncertainty.
// It is a contract fixture, not a remote backend or external adoption claim.
type mcpOwnedChannel struct {
	net.Conn
	once     sync.Once
	closeErr error
	release  <-chan struct{}
	finished chan struct{}
}

func (*mcpOwnedChannel) Start(context.Context) error { return nil }
func (c *mcpOwnedChannel) Close() error {
	c.once.Do(func() {
		_ = c.Conn.Close()
		if c.release != nil {
			<-c.release
		}
		close(c.finished)
	})
	return c.closeErr
}

type mcpChannelFactory struct{ command mcp.Command }

func (f mcpChannelFactory) NewCommand(mcp.ServerConfig) (mcp.Command, error) { return f.command, nil }

func ownedMCPFixture(t *testing.T, closeErr error, release <-chan struct{}) (*mcp.Server, *mcpOwnedChannel) {
	t.Helper()
	client, peer := net.Pipe()
	channel := &mcpOwnedChannel{Conn: client, closeErr: closeErr, release: release, finished: make(chan struct{})}
	server := sdk.NewServer(&sdk.Implementation{Name: "owned-channel", Version: "v1"}, nil)
	session, err := server.Connect(t.Context(), &sdk.IOTransport{Reader: peer, Writer: peer}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close(); _ = session.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connected, err := mcp.Connect(ctx, mcp.ServerConfig{Name: "owned", Command: "byte-channel-fixture"}, mcpChannelFactory{channel})
	if err != nil {
		t.Fatal(err)
	}
	return connected, channel
}

func TestAssemblyMCPCleanupUncertaintyRetainsLease(t *testing.T) {
	for _, mode := range []string{"failure", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			config := internalValidConfig(t)
			config.AllowUnsandboxedExec = true
			config.Diagnostics = io.Discard
			config.ShutdownTimeout = 100 * time.Millisecond
			t.Setenv(config.Provider.APIKeyEnv, "fixture-only")
			a, err := Open(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			store, err := a.host.Store()
			if err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			failure := errors.New("fixture process cleanup unproven")
			var wait <-chan struct{}
			if mode == "timeout" {
				wait = release
				failure = nil
			}
			server, channel := ownedMCPFixture(t, failure, wait)
			a.mcp = mcpServers{server}
			defer func() {
				unblock()
				_ = server.Close()
				_ = a.commands.Close()
				_ = a.provider.Close()
				_ = a.host.Shutdown(context.Background())
			}()
			err = a.Close()
			if err == nil || a.Ready() {
				t.Fatal("MCP cleanup uncertainty announced success")
			}
			if mode == "failure" && !errors.Is(err, mcp.ErrTeardownUnproven) {
				t.Fatalf("cleanup classification lost: %v", err)
			}
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if err := store.RenewLease(t.Context()); err != nil {
				t.Fatalf("MCP cleanup released lease: %v", err)
			}
			config.RuntimeID = "mcp-successor"
			if next, err := Open(t.Context(), config); err == nil {
				_ = next.Close()
				t.Fatal("successor entered before proven MCP cleanup")
			} else {
				var held *runtime.ErrLeaseHeld
				if !errors.As(err, &held) {
					t.Fatalf("wrong successor rejection: %v", err)
				}
			}
			unblock()
			<-channel.finished
			if again := a.Close(); again != err {
				t.Fatal("late MCP cleanup erased terminal failure")
			}
		})
	}
}
