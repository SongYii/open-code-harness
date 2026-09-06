package application

import (
	"fmt"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// TestFileObservationsTransitions pins the three states a target can be in for
// one session, and the guard each produces.
//
// Unseen and observed-absent look the same to a write — both become
// create-if-absent — but they are different to an edit: there is nothing to
// edit in a file known to be absent, and an unseen file has to be read first.
// Collapsing them would make "read the file before changing it" the answer to
// a situation where the file was read and simply is not there.
func TestFileObservationsTransitions(t *testing.T) {
	observations := newFileObservations()
	session, target := domain.SessionID("session-1"), "/workspace/a.txt"

	if got := observations.guardForWrite(session, target); got.Kind != tools.GuardCreateIfAbsent {
		t.Fatalf("unseen write guard = %#v, want create-if-absent", got)
	}
	if _, err := observations.guardForEdit(session, target); !tools.IsCode(err, tools.CodeFSNotObserved) {
		t.Fatalf("unseen edit guard = %v, want %q", err, tools.CodeFSNotObserved)
	}

	observations.recordPresent(session, target, "v1")
	guard, err := observations.guardForEdit(session, target)
	if err != nil {
		t.Fatalf("observed edit guard: %v", err)
	}
	if guard.Kind != tools.GuardReplaceIfVersion || guard.Version != "v1" {
		t.Fatalf("observed edit guard = %#v, want replace-if-version v1", guard)
	}
	if got := observations.guardForWrite(session, target); got.Version != "v1" {
		t.Fatalf("observed write guard = %#v, want the observed version", got)
	}

	observations.recordAbsent(session, target)
	if got := observations.guardForWrite(session, target); got.Kind != tools.GuardCreateIfAbsent || got.Version != "" {
		t.Fatalf("absent write guard = %#v, want a bare create-if-absent", got)
	}
	if _, err := observations.guardForEdit(session, target); !tools.IsCode(err, tools.CodeFSEditNotFound) {
		t.Fatalf("absent edit guard = %v, want %q", err, tools.CodeFSEditNotFound)
	}

	observations.forget(session)
	if _, err := observations.guardForEdit(session, target); !tools.IsCode(err, tools.CodeFSNotObserved) {
		t.Fatalf("forgotten edit guard = %v, want %q", err, tools.CodeFSNotObserved)
	}
}

// TestFileObservationsAreScopedToOneSession. One session's read must not
// license another session's write: the two may be looking at the same
// workspace with entirely different histories.
func TestFileObservationsAreScopedToOneSession(t *testing.T) {
	observations := newFileObservations()
	target := "/workspace/shared.txt"
	observations.recordPresent("session-a", target, "v1")

	if _, err := observations.guardForEdit("session-b", target); !tools.IsCode(err, tools.CodeFSNotObserved) {
		t.Fatalf("session-b edit guard = %v; one session's read licensed another's edit", err)
	}
	if got := observations.guardForWrite("session-b", target); got.Version != "" {
		t.Fatalf("session-b write guard = %#v; it inherited session-a's version", got)
	}

	observations.forget("session-b")
	if guard, err := observations.guardForEdit("session-a", target); err != nil || guard.Version != "v1" {
		t.Fatalf("forgetting session-b disturbed session-a: %#v %v", guard, err)
	}
}

// TestFileObservationsUnderConcurrency exists for the race detector. Readers,
// recorders, and forgetters all run against one table because a Service is
// shared across sessions and turns.
func TestFileObservationsUnderConcurrency(t *testing.T) {
	observations := newFileObservations()
	const workers = 16

	var wg sync.WaitGroup
	for i := range workers {
		session := domain.SessionID(fmt.Sprintf("session-%d", i%4))
		target := fmt.Sprintf("/workspace/file-%d.txt", i%3)

		wg.Add(3)
		go func() {
			defer wg.Done()
			for range 50 {
				observations.recordPresent(session, target, tools.FileVersion(fmt.Sprintf("v%d", i)))
			}
		}()
		go func() {
			defer wg.Done()
			for range 50 {
				_ = observations.guardForWrite(session, target)
				_, _ = observations.guardForEdit(session, target)
			}
		}()
		go func() {
			defer wg.Done()
			for range 50 {
				observations.forget(session)
			}
		}()
	}
	wg.Wait()
}

// TestEachServiceGetsItsOwnObservationTable is the process-restart property,
// asserted here rather than through a second Service over a shared store.
//
// The claim is that observations do not survive a process: they are not
// persisted, so a restart -- or any second process holding the same durable
// Session -- begins having seen nothing and must read before it may change
// anything. That is the honest consequence of keeping versions out of Domain
// events, where they would look like durable history while describing a
// filesystem the reader may not even be running on.
//
// Driving it end to end would need two Services over one store, and this
// package's sequence ID generator restarts from the same values, so the second
// Service collides on append identity before it can reach a tool. The property
// itself is exactly this: no two Services share a table.
func TestEachServiceGetsItsOwnObservationTable(t *testing.T) {
	first := newFileObservations()
	second := newFileObservations()
	session, target := domain.SessionID("session-1"), "/workspace/a.txt"

	first.recordPresent(session, target, "v1")
	if second.seen(session, target) {
		t.Fatal("a second table inherited the first one's observations")
	}
	if _, err := second.guardForEdit(session, target); !tools.IsCode(err, tools.CodeFSNotObserved) {
		t.Fatalf("second table edit guard = %v, want %q", err, tools.CodeFSNotObserved)
	}
	if got := second.guardForWrite(session, target); got.Kind != tools.GuardCreateIfAbsent {
		t.Fatalf("second table write guard = %#v, want a fail-closed create", got)
	}
}
