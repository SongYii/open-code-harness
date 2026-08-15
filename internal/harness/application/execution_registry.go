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
	executionPhaseAdmissionUnknown  executionPhase = "admission_unknown"
	executionPhaseRunning           executionPhase = "running"
	executionPhaseTerminalInFlight  executionPhase = "terminal_append_in_flight"
	executionPhaseTerminalUnknown   executionPhase = "terminal_unknown"
	executionPhaseTerminalCommitted executionPhase = "terminal_committed"
)

type executionRegistry struct {
	mu         sync.Mutex
	entries    map[domain.RunTurnRequestID]*executionEntry
	unresolved map[domain.SessionID]uint32
}

type executionEntry struct {
	requestID   domain.RunTurnRequestID
	sessionID   domain.SessionID
	digest      Digest
	done        chan struct{}
	phase       executionPhase
	intent      *AppendIntent
	result      RunTurnResult
	err         error
	terminal    bool
	leases      uint32
	ownerToken  uint64
	ownerActive bool
	retained    bool
}

type executionLease struct {
	registry   *executionRegistry
	entry      *executionEntry
	released   bool
	ownerToken uint64
}

type executionSnapshot struct {
	Phase    executionPhase
	Terminal bool
	Retained bool
	Leases   uint32
	Intent   *AppendIntent
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
	entry := &executionEntry{requestID: requestID, sessionID: sessionID, digest: digest, done: make(chan struct{}), phase: executionPhaseAdmissionInFlight, leases: 1, ownerToken: 1, ownerActive: true}
	registry.entries[requestID] = entry
	registry.unresolved[sessionID]++
	return &executionLease{registry: registry, entry: entry, ownerToken: entry.ownerToken}, true, nil
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

func (registry *executionRegistry) snapshot(requestID domain.RunTurnRequestID) (executionSnapshot, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := registry.entries[requestID]
	if entry == nil {
		return executionSnapshot{}, false
	}
	snapshot := executionSnapshot{Phase: entry.phase, Terminal: entry.terminal, Retained: entry.retained, Leases: entry.leases}
	if entry.intent != nil {
		copy, err := cloneAppendIntent(*entry.intent)
		if err == nil {
			snapshot.Intent = &copy
		}
	}
	return snapshot, true
}

func (lease *executionLease) ownsLocked() bool {
	return lease != nil && lease.registry != nil && lease.entry != nil && !lease.released && lease.ownerToken != 0 && lease.registry.entries[lease.entry.requestID] == lease.entry && lease.entry.ownerActive && lease.entry.ownerToken == lease.ownerToken && !lease.entry.terminal
}

func (lease *executionLease) setPhase(phase executionPhase) error {
	if lease == nil || lease.registry == nil {
		return errors.New("invalid execution owner")
	}
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if phase == executionPhaseAdmissionUnknown || phase == executionPhaseTerminalUnknown || !lease.ownsLocked() {
		return errors.New("execution owner capability rejected")
	}
	if !validExecutionTransition(lease.entry.phase, phase) {
		return errors.New("invalid execution phase transition")
	}
	lease.entry.phase = phase
	return nil
}

func (lease *executionLease) retainIntent(intent AppendIntent) error {
	cloned, err := cloneAppendIntent(intent)
	if err != nil {
		return err
	}
	if lease == nil || lease.registry == nil {
		return errors.New("invalid execution owner")
	}
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if !lease.ownsLocked() {
		return errors.New("execution owner capability rejected")
	}
	lease.entry.intent = &cloned
	return nil
}

func (lease *executionLease) retainUnknown(phase executionPhase) error {
	if phase != executionPhaseAdmissionUnknown && phase != executionPhaseTerminalUnknown {
		return errors.New("invalid unknown execution phase")
	}
	if lease == nil || lease.registry == nil {
		return errors.New("invalid execution owner")
	}
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if !lease.ownsLocked() || lease.entry.intent == nil || !validExecutionTransition(lease.entry.phase, phase) {
		return errors.New("execution owner capability rejected")
	}
	lease.entry.phase = phase
	lease.entry.retained = true
	lease.entry.ownerActive = false
	return nil
}

func validExecutionTransition(from, to executionPhase) bool {
	switch from {
	case executionPhaseAdmissionInFlight:
		return to == executionPhaseRunning || to == executionPhaseAdmissionUnknown
	case executionPhaseRunning:
		return to == executionPhaseTerminalInFlight
	case executionPhaseTerminalInFlight:
		return to == executionPhaseTerminalInFlight || to == executionPhaseTerminalUnknown
	default:
		return false
	}
}

func (lease *executionLease) publish(result RunTurnResult, err error) error {
	if lease == nil || lease.registry == nil {
		return errors.New("invalid execution owner")
	}
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if !lease.ownsLocked() {
		return errors.New("execution owner capability rejected")
	}
	lease.entry.result = cloneRunTurnResult(result)
	lease.entry.err = err
	lease.entry.terminal = true
	lease.entry.phase = executionPhaseTerminalCommitted
	close(lease.entry.done)
	lease.registry.cleanupLocked(lease.entry)
	return nil
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
	if !entry.terminal || entry.retained || entry.leases != 0 || registry.entries[entry.requestID] != entry {
		return
	}
	delete(registry.entries, entry.requestID)
	if registry.unresolved[entry.sessionID] <= 1 {
		delete(registry.unresolved, entry.sessionID)
	} else {
		registry.unresolved[entry.sessionID]--
	}
}
