package memory

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/application/eventstoretest"
)

func TestEventStoreContract(t *testing.T) {
	eventstoretest.Run(t, func(t *testing.T) eventstoretest.Harness {
		store := mustV2Store(t, application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1})
		return eventstoretest.Harness{
			Store: store, RotateAuthority: store.SetAuthority,
			FailNext:       func(point eventstoretest.FaultPoint, err error) { store.FailNext(FaultPoint(point), err) },
			CorruptReceipt: store.CorruptReceipt,
			SetCommitHook: func(point eventstoretest.CommitHookPoint, hook func()) {
				store.SetCommitHook(CommitHookPoint(point), hook)
			},
		}
	})
}

func mustV2Store(t *testing.T, authority application.WriterAuthority) *EventStore {
	t.Helper()
	store, err := NewEventStore(authority)
	if err != nil {
		t.Fatalf("NewEventStore() error = %v", err)
	}
	return store
}
