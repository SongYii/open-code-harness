// Package eventstoretest provides adapter-neutral EventStore v2 conformance tests.
package eventstoretest

import (
	"errors"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// FaultPoint names deterministic test-only storage failure boundaries.
type FaultPoint string

const (
	FaultBeforeCommit         FaultPoint = "before_commit"
	FaultAfterCommitBeforeAck FaultPoint = "after_commit_before_ack"
	FaultResolve              FaultPoint = "resolve"
)

// CommitHookPoint names the two sides of the adapter's single publication
// boundary. It is conformance-only, not a Store-port concern.
type CommitHookPoint string

const (
	CommitHookBeforePublish CommitHookPoint = "before_publish"
	CommitHookAfterPublish  CommitHookPoint = "after_publish"
)

// V2Harness supplies a fresh adapter instance for one independent test case.
type V2Harness struct {
	Store           application.EventStoreV2
	RotateAuthority func(application.WriterAuthority)
	FailNext        func(FaultPoint, error)
	CorruptReceipt  func(domain.AppendID)
	SetCommitHook   func(CommitHookPoint, func())
}

// V2Factory avoids colliding with the v1 conformance factory retained during
// the migration.
type V2Factory func(*testing.T) V2Harness

func RunV2(t *testing.T, factory V2Factory) {
	t.Helper()
	t.Run("atomic append and CAS", func(t *testing.T) { testAtomicAppendAndCAS(t, factory) })
	t.Run("proposed metadata preservation", func(t *testing.T) { testProposedMetadataPreservation(t, factory) })
	t.Run("exact receipt retry", func(t *testing.T) { testExactReceiptRetry(t, factory) })
	t.Run("pinned pagination", func(t *testing.T) { testPinnedPagination(t, factory) })
	t.Run("admission identity", func(t *testing.T) { testAdmissionIdentity(t, factory) })
	t.Run("writer fencing", func(t *testing.T) { testWriterFencing(t, factory) })
	t.Run("unknown outcome", func(t *testing.T) { testUnknownOutcome(t, factory) })
	t.Run("limits copies cancellation and corruption", func(t *testing.T) { testLimitsCopiesCancellationAndCorruption(t, factory) })
	t.Run("publication cancellation and corrupt receipts", func(t *testing.T) { testPublicationCancellationAndCorruptReceipts(t, factory) })
	t.Run("concurrent commit positions", func(t *testing.T) { testConcurrentCommitPositions(t, factory) })
}

func requireV2(t *testing.T, harness V2Harness) {
	t.Helper()
	if harness.Store == nil || harness.RotateAuthority == nil || harness.FailNext == nil || harness.CorruptReceipt == nil || harness.SetCommitHook == nil {
		t.Fatal("v2 harness is incomplete")
	}
}

func requireCode(t *testing.T, err error, code application.StoreErrorCode) {
	t.Helper()
	if !application.IsStoreCode(err, code) {
		t.Fatalf("error = %v, want store code %q", err, code)
	}
	var storeErr *application.StoreError
	if !errors.As(err, &storeErr) {
		t.Fatalf("error = %v, want *application.StoreError", err)
	}
	if storeErr.MayHaveCommitted != (code == application.StoreCodeCommitOutcomeUnknown) {
		t.Fatalf("MayHaveCommitted = %t for %q", storeErr.MayHaveCommitted, code)
	}
}
