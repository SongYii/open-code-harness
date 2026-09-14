package runtime

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

type blockedLease struct{ release chan struct{} }

func (lease blockedLease) RenewLease(context.Context) error { <-lease.release; return nil }

func TestHeartbeatWatchdogFencesBlockedRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newHeartbeatHost()
		lease := blockedLease{release: make(chan struct{})}
		done := make(chan struct{})
		go func() { host.heartbeatLoop(context.Background(), lease, 100*time.Millisecond); close(done) }()
		time.Sleep(400 * time.Millisecond)
		if host.Ready() || host.WorkContext().Err() == nil {
			t.Fatal("blocked store delayed fencing")
		}
		select {
		case <-done:
			t.Fatal("unreturned worker was forgotten")
		default:
		}
		close(lease.release)
		<-done
		if host.Ready() {
			t.Fatal("late success resurrected fenced host")
		}
	})
}

func TestDrainCancelsAndWaitsForCleanupWhileRejectingNewWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newHeartbeatHost()
		work, finish, err := host.Admit(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- host.Drain(context.Background()) }()
		synctest.Wait()
		if work.Err() == nil {
			t.Fatal("drain did not cancel work")
		}
		if _, _, err := host.Admit(context.Background()); err == nil {
			t.Fatal("admitted during drain")
		}
		select {
		case <-done:
			t.Fatal("drained before cleanup completed")
		default:
		}
		finish()
		finish() // an operation cannot decrement twice
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestDrainTimeoutIsTruthfulAndAdmissionStaysClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newHeartbeatHost()
		_, finish, err := host.Admit(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer finish()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := host.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("false drain success: %v", err)
		}
		if host.Ready() {
			t.Fatal("timed out host reopened")
		}
	})
}

func TestAdmissionFollowsCallerCancellation(t *testing.T) {
	host := newHeartbeatHost()
	ctx, cancel := context.WithCancel(context.Background())
	work, finish, err := host.Admit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	cancel()
	if work.Err() == nil || !host.Ready() {
		t.Fatal("caller cancellation incorrectly affected host")
	}
}
