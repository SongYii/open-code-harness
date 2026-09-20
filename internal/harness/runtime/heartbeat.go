package runtime

import (
	"context"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
)

// leaseController is the store surface the heartbeat loop needs; *sqlite.Store
// satisfies it and tests script it.
type leaseController interface {
	RenewLease(ctx context.Context) error
}

// runHeartbeat renews ownership on a bounded interval. Failure to confirm
// stops admission, cancels local work, and stops the exporter; nothing is
// deleted and no takeover is attempted. Only a NEW host can recover.
func (host *Host) runHeartbeat(ctx context.Context) {
	defer host.loopWG.Done()
	controller := leaseController(host.store)
	host.heartbeatLoop(ctx, controller, host.config.HeartbeatInterval)
}

func (host *Host) heartbeatLoop(ctx context.Context, controller leaseController, interval time.Duration) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	confirmed := make(chan time.Time, 1)
	fenced := make(chan struct{}, 1)
	done := make(chan struct{})
	// Exactly one renewal worker. The watchdog never waits on SQLite's
	// mutex to revoke admission. A stuck worker remains accounted for by
	// loopWG and causes Shutdown to report a timeout, not false success.
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			renewCtx, stop := context.WithTimeout(ctx, host.config.HeartbeatDeadline)
			err := controller.RenewLease(renewCtx)
			stop()
			if err == nil {
				select {
				case confirmed <- time.Now():
				case <-ctx.Done():
					return
				}
			} else if application.IsStoreCode(err, application.StoreCodeWriterFenced) {
				fenced <- struct{}{}
				return
			}
		}
	}()
	defer func() { cancel(); <-done }()
	last := time.Now()
	watchdog := time.NewTimer(host.config.HeartbeatDeadline)
	defer watchdog.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-confirmed:
			if at.Sub(last) >= host.config.HeartbeatDeadline {
				host.fencingReaction()
				return
			}
			last = at
			watchdog.Reset(time.Until(last.Add(host.config.HeartbeatDeadline)))
		case <-fenced:
			host.fencingReaction()
			return
		case <-watchdog.C:
			host.fencingReaction()
			return
		}
	}
}

// fencingReaction stops admission and cancels local work exactly once.
func (host *Host) fencingReaction() {
	host.mu.Lock()
	alreadyLost := host.lostLease
	host.lostLease = true
	cancel := host.workCancel
	host.mu.Unlock()
	if !alreadyLost && cancel != nil {
		cancel()
	}
}

// runExporter drains the audit replica on a bounded cadence after
// readiness; lag never blocks Runtime readiness.
func (host *Host) runExporter(ctx context.Context) {
	defer host.loopWG.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(host.WorkContext(), cancel)
	defer func() { stop(); cancel() }()
	if host.config.AuditDirectory == "" {
		return
	}
	ticker := time.NewTicker(host.config.ExportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !host.Ready() {
			return
		}
		_, _ = host.store.ExportOnce(ctx, sqlite.ExportConfig{Directory: host.config.AuditDirectory})
	}
}
