package sqlite

import (
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// faultPoint names deterministic test-only failure boundaries inside the
// adapter's commit path.
type faultPoint string

const (
	faultBeforeCommit         faultPoint = "before_commit"
	faultAfterCommitBeforeAck faultPoint = "after_commit_before_ack"
	faultResolve              faultPoint = "resolve"
)

// commitHookPoint names the two sides of the adapter's single commit
// boundary. It is conformance-only.
type commitHookPoint string

const (
	commitHookBeforePublish commitHookPoint = "before_publish"
	commitHookAfterPublish  commitHookPoint = "after_publish"
)

type faultState struct {
	mu     sync.Mutex
	faults map[faultPoint]error
	hooks  map[commitHookPoint]func()
}

func newFaultState() faultState {
	return faultState{faults: make(map[faultPoint]error), hooks: make(map[commitHookPoint]func())}
}

// FailNext arms a one-shot fault at the named boundary.
func (store *Store) FailNext(point faultPoint, cause error) {
	store.faults.mu.Lock()
	defer store.faults.mu.Unlock()
	store.faults.faults[point] = cause
}

func (store *Store) popFault(point faultPoint) error {
	store.faults.mu.Lock()
	defer store.faults.mu.Unlock()
	cause, ok := store.faults.faults[point]
	if ok {
		delete(store.faults.faults, point)
	}
	return cause
}

// SetCommitHook installs one bounded conformance-only hook; nil clears it.
func (store *Store) SetCommitHook(point commitHookPoint, hook func()) {
	store.faults.mu.Lock()
	defer store.faults.mu.Unlock()
	if hook == nil {
		delete(store.faults.hooks, point)
		return
	}
	store.faults.hooks[point] = hook
}

func (store *Store) runCommitHook(point commitHookPoint) {
	store.faults.mu.Lock()
	hook := store.faults.hooks[point]
	store.faults.mu.Unlock()
	if hook != nil {
		hook()
	}
}

// CorruptReceiptForTesting exposes the corruption seam to the conformance
// harness.
func (store *Store) CorruptReceiptForTesting(appendID domain.AppendID) {
	_ = store.corruptReceiptForTesting(appendID)
}
