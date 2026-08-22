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
	AcquireLease(ctx context.Context) (application.WriterAuthority, error)
}

// runHeartbeat renews ownership on a bounded interval. Failure to confirm
// stops admission, cancels local work, and stops the exporter; nothing is
// deleted and no takeover is attempted. After in-flight work quiesces, the
// normal expired-takeover path may restore ownership with the next token.
func (host *Host) runHeartbeat(ctx context.Context) {
	defer host.loopWG.Done()
	controller := leaseController(host.store)
	host.heartbeatLoop(ctx, controller, host.config.HeartbeatInterval)
}

func (host *Host) heartbeatLoop(ctx context.Context, controller leaseController, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sinceConfirmed := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := controller.RenewLease(ctx); err == nil {
			sinceConfirmed = 0
			continue
		} else if !application.IsStoreCode(err, application.StoreCodeWriterFenced) {
			// Transient unavailability does not revoke ownership: the store
			// predicate is the authority, not the renewal round-trip.
			sinceConfirmed += interval
			if sinceConfirmed < host.config.HeartbeatDeadline {
				continue
			}
		}
		host.fencingReaction()
		// After quiescence, attempt the normal expired-takeover path.
		if _, err := controller.AcquireLease(ctx); err != nil {
			continue
		}
		host.leaseRegained()
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

// leaseRegained resumes admission with a fresh work context. The previous
// work cancel is invoked after the lock is released: a cancel callback that
// reads Ready/Store/WorkContext would deadlock if it ran while holding mu.
func (host *Host) leaseRegained() {
	host.mu.Lock()
	if host.shutdown {
		host.mu.Unlock()
		return
	}
	host.lostLease = false
	previous := host.workCancel
	host.workCtx, host.workCancel = context.WithCancel(context.Background())
	host.mu.Unlock()
	if previous != nil {
		previous()
	}
}

// runExporter drains the audit replica on a bounded cadence after
// readiness; lag never blocks Runtime readiness.
func (host *Host) runExporter(ctx context.Context) {
	defer host.loopWG.Done()
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
		_, _ = host.store.ExportOnce(ctx, sqlite.ExportConfig{Directory: host.config.AuditDirectory})
	}
}
