package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/runtime"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

type drainingPolicy struct{ entered, cancelled, release chan struct{} }

type drainingToolPolicy struct{ entered, cancelled, release chan struct{} }

func (policy *drainingToolPolicy) Decide(ctx context.Context, _ toolpolicy.Input) (toolpolicy.Decision, error) {
	close(policy.entered)
	<-ctx.Done()
	close(policy.cancelled)
	<-policy.release
	return toolpolicy.Decision{}, ctx.Err()
}

func (policy *drainingPolicy) Plan(ctx context.Context, _ contextpolicy.Input) (contextpolicy.Decision, error) {
	close(policy.entered)
	<-ctx.Done()
	close(policy.cancelled)
	<-policy.release // represents cooperative callback cleanup
	return contextpolicy.Decision{}, ctx.Err()
}

// Enumerate the contracts so newly added methods cannot silently escape this
// rejection matrix. Zero requests deliberately require admission to win over
// validation; an unrelated validation/storage error does not satisfy the test.
func assertFacadeRejection(t *testing.T, ctx context.Context, service Service, store application.EventStore, want error) {
	t.Helper()
	for _, facade := range []struct {
		name     string
		contract reflect.Type
		value    reflect.Value
	}{
		{"service", reflect.TypeFor[Service](), reflect.ValueOf(service)},
		{"store", reflect.TypeFor[application.EventStore](), reflect.ValueOf(store)},
	} {
		for index := 0; index < facade.contract.NumMethod(); index++ {
			method := facade.contract.Method(index)
			t.Run(facade.name+"/"+method.Name, func(t *testing.T) {
				signature := method.Type
				if signature.NumIn() == 0 || signature.In(0) != reflect.TypeFor[context.Context]() ||
					signature.NumOut() == 0 || signature.Out(signature.NumOut()-1) != reflect.TypeFor[error]() || signature.IsVariadic() {
					t.Fatal("new facade signature needs an explicit admission test")
				}
				args := make([]reflect.Value, signature.NumIn())
				args[0] = reflect.ValueOf(ctx)
				for index := 1; index < len(args); index++ {
					args[index] = reflect.Zero(signature.In(index))
				}
				results := facade.value.MethodByName(method.Name).Call(args)
				err, _ := results[len(results)-1].Interface().(error)
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want admission rejection %v", err, want)
				}
			})
		}
	}
}

func TestAssemblyCloseDrainsTurnsAndManualCompaction(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		manual    bool
		leaseLoss bool
	}{
		{name: "close/turn"},
		{name: "close/manual", manual: true},
		{name: "fenced/turn", leaseLoss: true},
		{name: "fenced/manual", manual: true, leaseLoss: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := internalValidConfig(t)
			config.AllowUnsandboxedExec = true
			t.Setenv(config.Provider.APIKeyEnv, "fixture-key")
			policy := &drainingPolicy{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
			config.Context.PolicyID = "draining"
			config.ContextPolicies = []contextpolicy.Registration{{ID: "draining", Version: "1", Factory: func(json.RawMessage) (contextpolicy.Policy, error) { return policy, nil }}}
			assembly, err := Open(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(policy.release) }) }
			defer func() { release(); _ = assembly.Close() }()
			service, store, done := assembly.Service(), assembly.Store(), assembly.Done()
			if !assembly.Ready() || done == nil || done != assembly.Done() {
				t.Fatal("ready assembly must expose a stable lifecycle signal")
			}
			select {
			case <-done:
				t.Fatal("ready assembly already signalled cancellation")
			default:
			}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			t.Run("caller_cancelled", func(t *testing.T) {
				assertFacadeRejection(t, cancelled, service, store, context.Canceled)
			})
			if !assembly.Ready() {
				t.Fatal("caller cancellation stopped the assembly")
			}
			created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
			if err != nil {
				t.Fatal(err)
			}
			operation := make(chan error, 1)
			go func() {
				if scenario.manual {
					_, err := service.CompactSession(context.Background(), application.CompactSessionRequest{SessionID: created.SessionID})
					operation <- err
				} else {
					_, err := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "drain-request", Input: "continue", Sink: &testkit.RecordingSink{}})
					operation <- err
				}
			}()
			select {
			case <-policy.entered:
			case err := <-operation:
				t.Fatalf("never reached policy: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("policy not entered")
			}
			closed := make(chan error, 1)
			if scenario.leaseLoss {
				// Exercise the terminal fencing seam before Close: cancellation
				// must not depend on the launcher initiating shutdown. Heartbeat
				// tests separately verify how renewal failure enters this state.
				assembly.host.Abandon()
			} else {
				go func() { closed <- assembly.Close() }()
			}
			select {
			case <-policy.cancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("host failed to cancel operation")
			}
			select {
			case <-closed:
				t.Fatal("closed resources while callback cleanup was active")
			default:
			}
			if assembly.Ready() {
				t.Fatal("stopped assembly still reports ready")
			}
			select {
			case <-done:
			default:
				t.Fatal("cached Done did not signal before callback cleanup finished")
			}
			if _, err := service.LoadSession(context.Background(), created.SessionID); !errors.Is(err, runtime.ErrNotReady) {
				t.Fatalf("cached service bypassed stopped admission: %v", err)
			}
			if _, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: created.SessionID, Limit: 1}); !errors.Is(err, runtime.ErrNotReady) {
				t.Fatalf("cached store bypassed stopped admission: %v", err)
			}
			t.Run("admission_stopped", func(t *testing.T) {
				assertFacadeRejection(t, context.Background(), service, store, runtime.ErrNotReady)
			})
			if scenario.leaseLoss {
				go func() { closed <- assembly.Close() }()
			}
			release()
			if err := <-operation; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation not preserved: %v", err)
			}
			if err := <-closed; err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for index := 0; index < 20; index++ {
				wg.Go(func() {
					if err := assembly.Close(); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			t.Run("teardown_complete", func(t *testing.T) {
				assertFacadeRejection(t, context.Background(), service, store, runtime.ErrNotReady)
			})
			if _, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: created.SessionID, Limit: 1}); !errors.Is(err, runtime.ErrNotReady) {
				t.Fatalf("cached store bypassed admission after teardown: %v", err)
			}
			// A clean close really released the database and its lease.
			next, err := Open(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			if err := next.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAssemblyCloseAndLeaseLossDrainToolPolicy(t *testing.T) {
	for _, leaseLoss := range []bool{false, true} {
		name := "close"
		if leaseLoss {
			name = "lease_loss"
		}
		t.Run(name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-read\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer provider.Close()

			config := internalValidConfig(t)
			config.AllowUnsandboxedExec = true
			config.Provider.BaseURL = provider.URL
			config.Provider.AllowInsecureLoopback = true
			t.Setenv(config.Provider.APIKeyEnv, "fixture-key")
			if err := os.WriteFile(filepath.Join(config.WorkspaceRoot, "README.md"), []byte("hello"), 0o600); err != nil {
				t.Fatal(err)
			}
			selected := &drainingToolPolicy{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
			config.ToolPolicy = ToolPolicy{ID: "draining", Version: "1"}
			config.ToolPolicies = []toolpolicy.Registration{{ID: "draining", Version: "1", Factory: func(json.RawMessage) (toolpolicy.Policy, error) { return selected, nil }}}
			assembly, err := Open(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(selected.release) }) }
			defer func() { release(); _ = assembly.Close() }()
			service, store, done := assembly.Service(), assembly.Store(), assembly.Done()
			created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
			if err != nil {
				t.Fatal(err)
			}
			operation := make(chan error, 1)
			go func() {
				_, runErr := service.RunTurn(context.Background(), application.RunTurnRequest{SessionID: created.SessionID, RequestID: "tool-policy-drain", Input: "read", Sink: &testkit.RecordingSink{}})
				operation <- runErr
			}()
			select {
			case <-selected.entered:
			case err := <-operation:
				t.Fatalf("never reached tool policy: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("tool policy not entered")
			}

			closed := make(chan error, 1)
			if leaseLoss {
				assembly.host.Abandon()
			} else {
				go func() { closed <- assembly.Close() }()
			}
			select {
			case <-selected.cancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("host failed to cancel tool policy")
			}
			select {
			case err := <-closed:
				t.Fatalf("closed resources while tool policy cleanup was active: %v", err)
			default:
			}
			select {
			case <-done:
			default:
				t.Fatal("Done did not signal while tool policy cleanup was active")
			}
			if _, err := service.LoadSession(context.Background(), created.SessionID); !errors.Is(err, runtime.ErrNotReady) {
				t.Fatalf("new service work admitted while draining: %v", err)
			}
			if _, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: created.SessionID, Limit: 1}); !errors.Is(err, runtime.ErrNotReady) {
				t.Fatalf("new store work admitted while draining: %v", err)
			}
			if leaseLoss {
				go func() { closed <- assembly.Close() }()
			}
			release()
			if err := <-operation; !errors.Is(err, context.Canceled) {
				t.Fatalf("tool policy cancellation not preserved: %v", err)
			}
			if err := <-closed; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInvalidPluginStartupCreatesNoDatabase(t *testing.T) {
	for _, kind := range []string{"context", "tool"} {
		t.Run(kind, func(t *testing.T) {
			var providerRequests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { providerRequests.Add(1) }))
			defer provider.Close()
			config := internalValidConfig(t)
			config.Provider.BaseURL = provider.URL
			config.Provider.AllowInsecureLoopback = true
			t.Setenv(config.Provider.APIKeyEnv, "fixture-key")
			marker := filepath.Join(config.WorkspaceRoot, "mcp-started")
			if goruntime.GOOS != "windows" {
				script := filepath.Join(config.WorkspaceRoot, "mcp-marker.sh")
				if err := os.WriteFile(script, []byte("#!/bin/sh\n: > \"$1\"\nexit 1\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				config.MCPServers = []MCPServerConfig{{Name: "must-not-start", Command: script, Args: []string{marker}}}
			}
			if kind == "context" {
				config.Context.PolicyID = "missing"
			} else {
				config.ToolPolicy.ID = "missing"
			}
			assembly, err := Open(context.Background(), config)
			if err == nil || assembly != nil {
				t.Fatal("unknown plugin accepted")
			}
			if _, err := os.Stat(config.DatabasePath); !os.IsNotExist(err) {
				t.Fatal("plugin validation created durable resources")
			}
			if providerRequests.Load() != 0 {
				t.Fatalf("plugin validation made %d provider requests", providerRequests.Load())
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("plugin validation started MCP process: %v", err)
			}
		})
	}
}
