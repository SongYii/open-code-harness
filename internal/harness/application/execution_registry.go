package application

import (
	"context"
	"errors"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

var errExecutionIdentityMismatch = errors.New("execution request identity mismatch")

type executionPhase string

const (
	executionPhaseAdmissionInFlight executionPhase = "admission_in_flight"
	executionPhaseRunning           executionPhase = "running"
	executionPhaseTerminalInFlight  executionPhase = "terminal_append_in_flight"
	executionPhaseTerminalCommitted executionPhase = "terminal_committed"
)

type executionRegistry struct {
	mu         sync.Mutex
	entries    map[domain.RunTurnRequestID]*executionEntry
	unresolved map[domain.SessionID]uint32
}

type executionEntry struct {
	requestID domain.RunTurnRequestID
	sessionID domain.SessionID
	digest    Digest
	done      chan struct{}
	phase     executionPhase
	intent    *AppendIntent
	result    RunTurnResult
	err       error
	terminal  bool
	leases    uint32
}

type executionLease struct {
	registry *executionRegistry
	entry    *executionEntry
	released bool
}

func newExecutionRegistry() *executionRegistry {
	return &executionRegistry{entries: make(map[domain.RunTurnRequestID]*executionEntry), unresolved: make(map[domain.SessionID]uint32)}
}

func (registry *executionRegistry) acquire(requestID domain.RunTurnRequestID, sessionID domain.SessionID, digest Digest) (*executionLease, bool, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if entry := registry.entries[requestID]; entry != nil {
		if entry.sessionID != sessionID || entry.digest != digest {
			return nil, false, errExecutionIdentityMismatch
		}
		entry.leases++
		return &executionLease{registry: registry, entry: entry}, false, nil
	}
	entry := &executionEntry{requestID: requestID, sessionID: sessionID, digest: digest, done: make(chan struct{}), phase: executionPhaseAdmissionInFlight, leases: 1}
	registry.entries[requestID] = entry
	registry.unresolved[sessionID]++
	return &executionLease{registry: registry, entry: entry}, true, nil
}

func (registry *executionRegistry) attachExisting(requestID domain.RunTurnRequestID, sessionID domain.SessionID, digest Digest) (*executionLease, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := registry.entries[requestID]
	if entry == nil || entry.sessionID != sessionID || entry.digest != digest {
		return nil, false
	}
	entry.leases++
	return &executionLease{registry: registry, entry: entry}, true
}

func (registry *executionRegistry) unresolvedForSession(sessionID domain.SessionID) uint32 {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.unresolved[sessionID]
}

func (lease *executionLease) setPhase(phase executionPhase) {
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if !lease.released && !lease.entry.terminal {
		lease.entry.phase = phase
	}
}

func (lease *executionLease) retainIntent(intent AppendIntent) {
	cloned, err := cloneAppendIntent(intent)
	if err != nil {
		return
	}
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if !lease.released && !lease.entry.terminal {
		lease.entry.intent = &cloned
	}
}

func (lease *executionLease) publish(result RunTurnResult, err error) {
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if lease.entry.terminal {
		return
	}
	lease.entry.result = cloneRunTurnResult(result)
	lease.entry.err = err
	lease.entry.terminal = true
	lease.entry.phase = executionPhaseTerminalCommitted
	close(lease.entry.done)
	lease.registry.cleanupLocked(lease.entry)
}

func (lease *executionLease) wait(ctx context.Context) (RunTurnResult, error) {
	if err := contextError(ctx); err != nil {
		return RunTurnResult{}, err
	}
	select {
	case <-lease.entry.done:
		lease.registry.mu.Lock()
		defer lease.registry.mu.Unlock()
		return cloneRunTurnResult(lease.entry.result), lease.entry.err
	case <-ctx.Done():
		return RunTurnResult{}, applicationError(CategoryCanceled, "canceled", false, ctx.Err())
	}
}

func (lease *executionLease) release() {
	if lease == nil || lease.registry == nil {
		return
	}
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if lease.released {
		return
	}
	lease.released = true
	if lease.entry.leases > 0 {
		lease.entry.leases--
	}
	lease.registry.cleanupLocked(lease.entry)
}

func (registry *executionRegistry) cleanupLocked(entry *executionEntry) {
	if !entry.terminal || entry.leases != 0 || registry.entries[entry.requestID] != entry {
		return
	}
	delete(registry.entries, entry.requestID)
	if registry.unresolved[entry.sessionID] <= 1 {
		delete(registry.unresolved, entry.sessionID)
	} else {
		registry.unresolved[entry.sessionID]--
	}
}
