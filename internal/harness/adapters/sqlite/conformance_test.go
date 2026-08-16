package sqlite

import (
	"context"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/application/eventstoretest"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// TestConformance runs the adapter-neutral EventStore v2 suite against the
// SQLite adapter with zero suite change.
func TestConformance(t *testing.T) {
	eventstoretest.Run(t, func(t *testing.T) eventstoretest.Harness {
		config := tempStoreConfig(t)
		config.RuntimeID = "runtime-1"
		store, err := Open(context.Background(), config)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return eventstoretest.Harness{
			Store: store,
			RotateAuthority: func(authority application.WriterAuthority) {
				if err := store.rotateAuthorityForTesting(authority); err != nil {
					t.Fatalf("rotate authority: %v", err)
				}
			},
			FailNext: func(point eventstoretest.FaultPoint, cause error) {
				store.FailNext(faultPoint(point), cause)
			},
			CorruptReceipt: func(appendID domain.AppendID) {
				store.CorruptReceiptForTesting(appendID)
			},
			SetCommitHook: func(point eventstoretest.CommitHookPoint, hook func()) {
				store.SetCommitHook(commitHookPoint(point), hook)
			},
		}
	})
}
