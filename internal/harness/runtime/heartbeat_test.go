package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
)

type scriptedLease struct {
	mu        sync.Mutex
	renewals  int
	renewErrs []error // consumed in order; repeats the last
	acquireOK bool
	acquired  int
}

func (script *scriptedLease) RenewLease(ctx context.Context) error {
	script.mu.Lock()
	script.renewals++
	index := script.renewals - 1
	if index >= len(script.renewErrs) {
		index = len(script.renewErrs) - 1
	}
	errs := script.renewErrs
	script.mu.Unlock()
	if errs == nil {
		return nil
	}
	return errs[index]
}

func (script *scriptedLease) AcquireLease(ctx context.Context) (application.WriterAuthority, error) {
	script.mu.Lock()
	defer script.mu.Unlock()
	if !script.acquireOK {
		return application.WriterAuthority{}, errors.New("lease still held")
	}
	script.acquired++
	return application.WriterAuthority{RuntimeID: "runtime-host-test", FencingToken: uint64(script.acquired + 1)}, nil
}

func (script *scriptedLease) acquireCount() int {
	script.mu.Lock()
	defer script.mu.Unlock()
	return script.acquired
}

func newHeartbeatHost() *Host {
	workCtx, workCancel := context.WithCancel(context.Background())
	return &Host{
		config: Config{
			HeartbeatInterval: 100 * time.Millisecond,
			HeartbeatDeadline: 350 * time.Millisecond,
		},
		ready:      true,
		workCtx:    workCtx,
		workCancel: workCancel,
	}
}

func TestHeartbeatFencingReactionStopsAdmissionAndCancelsWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newHeartbeatHost()
		script := &scriptedLease{
			renewErrs: []error{newFencedError()},
			acquireOK: false,
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { host.heartbeatLoop(ctx, script, 100*time.Millisecond); close(done) }()
		defer func() { cancel(); <-done }()

		// The first fenced renewal reacts immediately.
		time.Sleep(150 * time.Millisecond)
		if host.Ready() {
			t.Fatal("admission still open after fencing")
		}
		if err := host.WorkContext().Err(); err == nil {
			t.Fatal("work context was not cancelled by the fencing reaction")
		}
		select {
		case <-done:
			t.Fatal("heartbeat loop exited on fencing; it must keep polling for takeover")
		default:
		}
	})
}

func newFencedError() error {
	storeErr, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeWriterFenced})
	if err != nil {
		panic(err)
	}
	return storeErr
}

func TestHeartbeatTransientUnavailabilityWithinDeadlineContinues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newHeartbeatHost()
		script := &scriptedLease{
			renewErrs: []error{
				newUnavailableError(),
				newUnavailableError(),
				nil, // confirmed again before the deadline
			},
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { host.heartbeatLoop(ctx, script, 100*time.Millisecond); close(done) }()
		defer func() { cancel(); <-done }()
		time.Sleep(260 * time.Millisecond)
		if !host.Ready() {
			t.Fatal("transient unavailability within the deadline revoked ownership")
		}
		if err := host.WorkContext().Err(); err != nil {
			t.Fatal("work context cancelled by transient unavailability")
		}
	})
}

func newUnavailableError() error {
	storeErr, err := application.NewStoreError(application.StoreError{Code: application.StoreCodeUnavailable})
	if err != nil {
		panic(err)
	}
	return storeErr
}

func TestHeartbeatRegainsLeaseAfterQuiescence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newHeartbeatHost()
		script := &scriptedLease{
			renewErrs: []error{newFencedError(), newFencedError()},
			acquireOK: true,
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { host.heartbeatLoop(ctx, script, 100*time.Millisecond); close(done) }()
		defer func() { cancel(); <-done }()
		time.Sleep(400 * time.Millisecond)
		if !host.Ready() {
			t.Fatal("lease was not regained through the takeover path")
		}
		if script.acquireCount() == 0 {
			t.Fatal("takeover was never attempted")
		}
	})
}

func TestHeartbeatConfigBounds(t *testing.T) {
	if err := (Config{HeartbeatInterval: time.Second, HeartbeatDeadline: time.Second}).validate(); err == nil {
		t.Fatal("deadline equal to interval accepted")
	}
	if err := (Config{HeartbeatInterval: 2 * time.Second, HeartbeatDeadline: time.Second}).validate(); err == nil {
		t.Fatal("deadline below interval accepted")
	}
	if err := (Config{HeartbeatInterval: time.Second, HeartbeatDeadline: 2 * time.Second, SQLite: sqliteLeaseConfig(1500 * time.Millisecond)}).validate(); err == nil {
		t.Fatal("deadline not strictly shorter than the lease duration accepted")
	}
	if err := (Config{HeartbeatInterval: time.Second, HeartbeatDeadline: 2 * time.Second, SQLite: sqliteLeaseConfig(30 * time.Second)}).validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func sqliteLeaseConfig(duration time.Duration) sqlite.Config {
	return sqlite.Config{LeaseDuration: duration}
}
