package application

import (
	"fmt"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestFileObservationsTransitions(t *testing.T) {
	observations := newFileObservations()
	sessionID := domain.SessionID("session-1")
	target := "/workspace/a.txt"

	if got := observations.guardForWrite(sessionID, target); got.Kind != tools.GuardCreateIfAbsent || got.Version != "" {
		t.Fatalf("unseen guardForWrite() = %#v, want create-if-absent", got)
	}
	if _, err := observations.guardForEdit(sessionID, target); !tools.IsCode(err, tools.CodeFilesystemNotObserved) {
		t.Fatalf("unseen guardForEdit() error = %v, want fs_not_observed", err)
	}

	observations.recordAbsent(sessionID, target)
	if got := observations.guardForWrite(sessionID, target); got.Kind != tools.GuardCreateIfAbsent || got.Version != "" {
		t.Fatalf("observed-absent guardForWrite() = %#v, want create-if-absent", got)
	}
	if _, err := observations.guardForEdit(sessionID, target); !tools.IsCode(err, tools.CodeFilesystemNotFound) {
		t.Fatalf("observed-absent guardForEdit() error = %v, want fs_not_found", err)
	}

	observations.recordPresent(sessionID, target, "v1")
	wantReplace := tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: "v1"}
	if got := observations.guardForWrite(sessionID, target); got != wantReplace {
		t.Fatalf("observed-present guardForWrite() = %#v, want %#v", got, wantReplace)
	}
	if got, err := observations.guardForEdit(sessionID, target); err != nil || got != wantReplace {
		t.Fatalf("observed-present guardForEdit() = (%#v, %v), want (%#v, nil)", got, err, wantReplace)
	}

	observations.forget(sessionID)
	if got := observations.guardForWrite(sessionID, target); got.Kind != tools.GuardCreateIfAbsent || got.Version != "" {
		t.Fatalf("forgotten guardForWrite() = %#v, want create-if-absent", got)
	}
	if _, err := observations.guardForEdit(sessionID, target); !tools.IsCode(err, tools.CodeFilesystemNotObserved) {
		t.Fatalf("forgotten guardForEdit() error = %v, want fs_not_observed", err)
	}
}

func TestFileObservationEditGuardTransitions(t *testing.T) {
	sessionID := domain.SessionID("session-1")
	target := "/workspace/a.txt"
	wantReplace := tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: "v1"}
	tests := []struct {
		name      string
		record    func(*fileObservations)
		wantGuard tools.MutationGuard
		wantError tools.ErrorCode
	}{
		{
			name:      "unseen",
			wantError: tools.CodeFilesystemNotObserved,
		},
		{
			name: "observed absent",
			record: func(observations *fileObservations) {
				observations.recordAbsent(sessionID, target)
			},
			wantError: tools.CodeFilesystemNotFound,
		},
		{
			name: "observed present",
			record: func(observations *fileObservations) {
				observations.recordPresent(sessionID, target, "v1")
			},
			wantGuard: wantReplace,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observations := newFileObservations()
			if test.record != nil {
				test.record(observations)
			}
			guard, err := observations.guardForEdit(sessionID, target)
			if test.wantError != "" {
				if !tools.IsCode(err, test.wantError) {
					t.Fatalf("guardForEdit() error = %v, want %s", err, test.wantError)
				}
				return
			}
			if err != nil || guard != test.wantGuard {
				t.Fatalf("guardForEdit() = (%#v, %v), want (%#v, nil)", guard, err, test.wantGuard)
			}
		})
	}
}

func TestFileObservationsConcurrentAccess(t *testing.T) {
	observations := newFileObservations()
	const workers = 24
	const operations = 200

	var ready sync.WaitGroup
	ready.Add(workers)
	start := make(chan struct{})
	var workersDone sync.WaitGroup
	workersDone.Add(workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		go func() {
			defer workersDone.Done()
			ready.Done()
			<-start
			sessionID := domain.SessionID(fmt.Sprintf("session-%d", worker%4))
			target := fmt.Sprintf("/workspace/%d.txt", worker%6)
			for operation := 0; operation < operations; operation++ {
				switch (worker + operation) % 5 {
				case 0:
					observations.recordPresent(sessionID, target, tools.FileVersion(fmt.Sprintf("v-%d-%d", worker, operation)))
				case 1:
					observations.recordAbsent(sessionID, target)
				case 2:
					observations.forget(sessionID)
				case 3:
					_ = observations.guardForWrite(sessionID, target)
				case 4:
					_, _ = observations.guardForEdit(sessionID, target)
				}
			}
		}()
	}
	ready.Wait()
	close(start)
	workersDone.Wait()
}
