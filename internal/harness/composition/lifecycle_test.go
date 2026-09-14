package composition

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/runtime"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
)

type drainingPolicy struct{ entered, cancelled, release chan struct{} }

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

func TestInvalidPluginStartupCreatesNoDatabase(t *testing.T) {
	config := internalValidConfig(t)
	config.Context.PolicyID = "missing"
	assembly, err := Open(context.Background(), config)
	if err == nil || assembly != nil {
		t.Fatal("unknown plugin accepted")
	}
	if _, err := os.Stat(config.DatabasePath); !os.IsNotExist(err) {
		t.Fatal("plugin validation created durable resources")
	}
}
