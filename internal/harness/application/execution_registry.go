package application

import (
	"context"
	"errors"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

var (
	errExecutionIdentityMismatch = errors.New("execution request identity mismatch")
	errSessionUnresolved         = errors.New("session has an unresolved append")
)

type executionPhase string

const (
	executionPhaseAdmissionInFlight  executionPhase = "admission_in_flight"
	executionPhaseAdmissionUnknown   executionPhase = "admission_unknown"
	executionPhaseRunning            executionPhase = "running"
	executionPhaseStepAppendInFlight executionPhase = "step_append_in_flight"
	executionPhaseStepAppendUnknown  executionPhase = "step_append_unknown"
	executionPhaseCancelWon          executionPhase = "cancel_won"
	executionPhaseTerminalInFlight   executionPhase = "terminal_append_in_flight"
	executionPhaseTerminalUnknown    executionPhase = "terminal_unknown"
	executionPhaseTerminalCommitted  executionPhase = "terminal_committed"
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
	if registry.hasRetainedUnknownLocked(sessionID) {
		return nil, false, errSessionUnresolved
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
	if entry.retained && !entry.ownerActive && !entry.terminal && entry.leases == 0 {
		return nil, false
	}
	entry.leases++
	return &executionLease{registry: registry, entry: entry}, true
}

func (registry *executionRegistry) hasRetainedUnknownLocked(sessionID domain.SessionID) bool {
	for _, entry := range registry.entries {
		if entry.sessionID == sessionID && entry.retained && !entry.terminal {
			return true
		}
	}
	return false
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
	if phase == executionPhaseAdmissionUnknown || phase == executionPhaseTerminalUnknown || phase == executionPhaseStepAppendUnknown {
		return errors.New("execution owner capability rejected")
	}
	if !lease.ownsLocked() && !(lease.entry != nil && lease.entry.retained && !lease.entry.terminal && lease.ownerToken != 0 && lease.entry.ownerToken == lease.ownerToken) {
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
	if !lease.ownsLocked() && !lease.canResolveLocked() {
		return errors.New("execution owner capability rejected")
	}
	lease.entry.intent = &cloned
	return nil
}

func (lease *executionLease) canResolveLocked() bool {
	return lease != nil && !lease.released && lease.ownerToken != 0 && lease.entry != nil && lease.entry.ownerToken == lease.ownerToken && lease.entry.retained && !lease.entry.terminal
}

func (lease *executionLease) retainUnknown(phase executionPhase) error {
	if phase != executionPhaseAdmissionUnknown && phase != executionPhaseTerminalUnknown && phase != executionPhaseStepAppendUnknown {
		return errors.New("invalid unknown execution phase")
	}
	if lease == nil || lease.registry == nil {
		return errors.New("invalid execution owner")
	}
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if (!lease.ownsLocked() && !lease.canResolveLocked()) || lease.entry.intent == nil || !validExecutionTransition(lease.entry.phase, phase) {
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
	case executionPhaseAdmissionUnknown:
		return to == executionPhaseRunning || to == executionPhaseCancelWon || to == executionPhaseTerminalInFlight
	case executionPhaseRunning:
		return to == executionPhaseStepAppendInFlight || to == executionPhaseTerminalInFlight || to == executionPhaseCancelWon
	case executionPhaseStepAppendInFlight:
		return to == executionPhaseRunning || to == executionPhaseStepAppendUnknown || to == executionPhaseCancelWon
	case executionPhaseStepAppendUnknown:
		// terminal_append_in_flight is forbidden: mid-loop batches are never Turn terminals.
		return to == executionPhaseRunning || to == executionPhaseStepAppendInFlight || to == executionPhaseCancelWon
	case executionPhaseCancelWon:
		return to == executionPhaseTerminalInFlight || to == executionPhaseTerminalUnknown
	case executionPhaseTerminalInFlight:
		return to == executionPhaseTerminalInFlight || to == executionPhaseTerminalUnknown
	case executionPhaseTerminalUnknown:
		return to == executionPhaseTerminalInFlight
	default:
		return false
	}
}

func (lease *executionLease) resumeAfterResolvedAdmission() error {
	return lease.resumeAfterResolvedUnknown(executionPhaseAdmissionUnknown)
}

func (lease *executionLease) resumeAfterResolvedStepAppend() error {
	return lease.resumeAfterResolvedUnknown(executionPhaseStepAppendUnknown)
}

func (lease *executionLease) resumeAfterResolvedUnknown(required executionPhase) error {
	if lease == nil || lease.registry == nil {
		return errors.New("invalid execution owner")
	}
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if lease.released || lease.ownerToken == 0 || lease.entry == nil || lease.entry.ownerToken != lease.ownerToken || !lease.entry.retained || lease.entry.terminal || lease.entry.phase != required {
		return errors.New("execution owner capability rejected")
	}
	lease.entry.phase = executionPhaseRunning
	lease.entry.retained = false
	lease.entry.ownerActive = true
	return nil
}

func (lease *executionLease) publishRetained(result RunTurnResult, err error) error {
	if lease == nil || lease.registry == nil {
		return errors.New("invalid execution owner")
	}
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if lease.released || lease.ownerToken == 0 || lease.entry == nil || lease.entry.ownerToken != lease.ownerToken || lease.entry.terminal || !lease.entry.retained {
		return errors.New("execution owner capability rejected")
	}
	lease.entry.result = cloneRunTurnResult(result)
	lease.entry.err = err
	lease.entry.terminal = true
	lease.entry.retained = false
	lease.entry.phase = executionPhaseTerminalCommitted
	close(lease.entry.done)
	lease.registry.cleanupLocked(lease.entry)
	return nil
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
