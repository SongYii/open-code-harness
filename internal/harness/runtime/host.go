package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ErrNotReady reports that reconciliation has not completed; commands are
// unavailable until then.
var ErrNotReady = errors.New("runtime host: reconciliation has not completed")

// ErrLeaseHeld reports that another live runtime owns the database lease.
type ErrLeaseHeld struct {
	Owner string
}

func (err *ErrLeaseHeld) Error() string {
	return fmt.Sprintf("runtime host: database lease is held by live runtime %q", err.Owner)
}

// Config bounds one host. Zero values take documented defaults.
type Config struct {
	SQLite sqlite.Config

	// AuditDirectory enables the background exporter after readiness; empty
	// disables it (export lag never blocks readiness either way).
	AuditDirectory string

	// HeartbeatInterval bounds renewal cadence. Default 5s.
	HeartbeatInterval time.Duration

	// HeartbeatDeadline bounds how long ownership may go unconfirmed before
	// the fencing reaction. Must be strictly shorter than the SQLite lease
	// duration. Default 10s.
	HeartbeatDeadline time.Duration

	// ExportInterval bounds the background export cadence. Default 5s.
	ExportInterval time.Duration
}

const (
	defaultHeartbeatInterval = 5 * time.Second
	defaultHeartbeatDeadline = 10 * time.Second
	defaultExportInterval    = 5 * time.Second
)

func (config Config) withDefaults() Config {
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = defaultHeartbeatInterval
	}
	if config.HeartbeatDeadline == 0 {
		config.HeartbeatDeadline = defaultHeartbeatDeadline
	}
	if config.ExportInterval == 0 {
		config.ExportInterval = defaultExportInterval
	}
	return config
}

func (config Config) validate() error {
	if config.HeartbeatInterval <= 0 || config.HeartbeatDeadline <= 0 {
		return fmt.Errorf("runtime host: heartbeat interval and deadline must be positive")
	}
	if config.HeartbeatDeadline <= config.HeartbeatInterval {
		return fmt.Errorf("runtime host: heartbeat deadline must exceed the interval")
	}
	if config.HeartbeatDeadline >= config.SQLite.LeaseDuration && config.SQLite.LeaseDuration > 0 {
		return fmt.Errorf("runtime host: heartbeat deadline must be strictly shorter than the lease duration")
	}
	return nil
}

// Host is the single Runtime Host. It composes the SQLite store, the
// reconciliation pass, the heartbeat loop, and the background exporter.
type Host struct {
	config Config
	store  *sqlite.Store

	mu         sync.RWMutex
	ready      bool
	shutdown   bool
	workCancel context.CancelFunc
	workCtx    context.Context
	lostLease  bool

	loopCancel context.CancelFunc
	loopWG     sync.WaitGroup
}

// Launch executes the parent startup order: open the database (format
// verification and migrations run inside), acquire the Runtime lease and
// fencing token, enumerate running candidates, replay and append recovery
// terminal facts, then become ready and start the background exporter.
// A second process that cannot acquire the lease fails with a stable
// diagnostic.
func Launch(ctx context.Context, config Config) (*Host, error) {
	config = config.withDefaults()
	config.SQLite = config.SQLite.WithDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}

	store, err := sqlite.Open(ctx, config.SQLite)
	if err != nil {
		return nil, classifyOpenError(err)
	}
	rec := &reconciler{store: store, authority: store}
	if err := reconcileAll(ctx, rec, store); err != nil {
		_ = store.Close()
		return nil, err
	}

	workCtx, workCancel := context.WithCancel(context.WithoutCancel(ctx))
	loopCtx, loopCancel := context.WithCancel(context.Background())
	host := &Host{
		config:     config,
		store:      store,
		workCtx:    workCtx,
		workCancel: workCancel,
		loopCancel: loopCancel,
	}
	host.mu.Lock()
	host.ready = true
	host.mu.Unlock()

	host.loopWG.Add(2)
	go host.runHeartbeat(loopCtx)
	go host.runExporter(loopCtx)
	return host, nil
}

// reconcileAll enumerates candidates from the session_heads projection
// (Turn activity) and, separately, sessions whose own stream head is an
// unmatched context.compaction.started (design §14.4) -- a manual or
// pre-turn compaction crash session_heads alone cannot surface, since
// compaction activity never updates it -- and confirms each by
// authoritative stream replay.
func reconcileAll(ctx context.Context, rec *reconciler, store *sqlite.Store) error {
	running, err := store.ActiveSessions(ctx)
	if err != nil {
		return err
	}
	compacting, err := store.SessionsWithActiveCompaction(ctx)
	if err != nil {
		return err
	}
	seen := make(map[domain.SessionID]bool, len(running)+len(compacting))
	candidates := make([]domain.SessionID, 0, len(running)+len(compacting))
	for _, group := range [][]domain.SessionID{running, compacting} {
		for _, session := range group {
			if seen[session] {
				continue
			}
			seen[session] = true
			candidates = append(candidates, session)
		}
	}
	for _, session := range candidates {
		if _, err := rec.reconcileSession(ctx, session); err != nil {
			return fmt.Errorf("reconcile %s: %w", session, err)
		}
	}
	return nil
}

// Ready reports whether reconciliation completed and commands are accepted.
func (host *Host) Ready() bool {
	host.mu.RLock()
	defer host.mu.RUnlock()
	return host.ready && !host.shutdown && !host.lostLease
}

// Store returns the canonical store for command execution. Before
// readiness, or after shutdown or lease loss, it refuses with a stable
// classified error.
func (host *Host) Store() (store *sqlite.Store, err error) {
	host.mu.RLock()
	defer host.mu.RUnlock()
	switch {
	case host.shutdown:
		return nil, errors.New("runtime host: shut down")
	case host.lostLease:
		return nil, errors.New("runtime host: lease lost; admission stopped")
	case !host.ready:
		return nil, ErrNotReady
	}
	return host.store, nil
}

// WorkContext is cancelled when the host stops admitting executions. The
// read takes the lock: leaseRegained swaps the field concurrently.
func (host *Host) WorkContext() context.Context {
	host.mu.RLock()
	defer host.mu.RUnlock()
	return host.workCtx
}

// Shutdown stops admission, cancels in-flight work, waits for the bounded
// loops, and releases the lease by expiring it — matching the owning
// runtime ID and fencing token exactly, so a stale host can never release
// a successor's lease.
func (host *Host) Shutdown(ctx context.Context) error {
	host.mu.Lock()
	if host.shutdown {
		host.mu.Unlock()
		return nil
	}
	host.shutdown = true
	host.mu.Unlock()

	host.mu.Lock()
	cancel := host.workCancel
	host.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	host.loopCancel()
	done := make(chan struct{})
	go func() { host.loopWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("runtime host: shutdown wait exceeded its bound: %w", ctx.Err())
	}
	if err := host.store.ReleaseLease(context.Background()); err != nil {
		_ = host.store.Close()
		return err
	}
	return host.store.Close()
}

func classifyOpenError(err error) error {
	var held *sqlite.ErrLeaseHeld
	if errors.As(err, &held) {
		return &ErrLeaseHeld{Owner: held.Owner}
	}
	return err
}
