package memory

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/application/eventstoretest"
)

func TestEventStoreV2Contract(t *testing.T) {
	eventstoretest.RunV2(t, func(t *testing.T) eventstoretest.V2Harness {
		store := mustV2Store(t, application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1})
		return eventstoretest.V2Harness{
			Store: store, RotateAuthority: store.SetAuthority,
			FailNext: func(point eventstoretest.FaultPoint, err error) { store.FailNext(FaultPoint(point), err) },
		}
	})
}

func mustV2Store(t *testing.T, authority application.WriterAuthority) *EventStoreV2 {
	t.Helper()
	store, err := NewEventStoreV2(authority)
	if err != nil {
		t.Fatalf("NewEventStoreV2() error = %v", err)
	}
	return store
}
