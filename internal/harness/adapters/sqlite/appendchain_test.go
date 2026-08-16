package sqlite

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func auditChainFixtures(t *testing.T, store *Store) (application.AppendRequest, application.AppendRequest) {
	t.Helper()
	first := appendRequest("append-chain-1", "session-chain", 0, "command-chain-1",
		domain.SessionCreated{WorkspaceRoot: "/w"})
	mustAppend(t, store, first)
	second := appendRequest("append-chain-2", "session-chain", 1, "command-chain-2",
		domain.TurnStarted{TurnID: "turn-chain", Input: "x"})
	mustAppend(t, store, second)
	return first, second
}

func TestAppendMaintainsAuditChain(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	first, second := auditChainFixtures(t, store)
	ctx := context.Background()

	if got := tableCount(t, store, "export_outbox"); got != 2 {
		t.Fatalf("export_outbox rows = %d, want 2", got)
	}

	var firstBatch, secondBatch, secondPrevious []byte
	var firstFormat, secondFormat int
	if err := store.db.QueryRowContext(ctx,
		"SELECT audit_format_version, batch_audit_digest FROM event_appends WHERE append_id = 'append-chain-1'").Scan(&firstFormat, &firstBatch); err != nil {
		t.Fatalf("read first audit row: %v", err)
	}
	if err := store.db.QueryRowContext(ctx,
		"SELECT audit_format_version, previous_audit_digest, batch_audit_digest FROM event_appends WHERE append_id = 'append-chain-2'").Scan(&secondFormat, &secondPrevious, &secondBatch); err != nil {
		t.Fatalf("read second audit row: %v", err)
	}
	if firstFormat != 1 || secondFormat != 1 {
		t.Fatalf("audit format versions = %d/%d, want 1/1", firstFormat, secondFormat)
	}
	if string(secondPrevious) != string(firstBatch) {
		t.Fatal("second batch does not chain to the first batch digest")
	}
	if string(firstBatch) == string(auditGenesisDigest[:]) {
		t.Fatal("first batch digest equals the genesis seed")
	}

	var headAudit []byte
	if err := store.db.QueryRowContext(ctx,
		"SELECT head_audit_digest FROM store_metadata WHERE id = 1").Scan(&headAudit); err != nil {
		t.Fatalf("read head audit digest: %v", err)
	}
	if string(headAudit) != string(secondBatch) {
		t.Fatal("head audit digest does not track the latest batch")
	}

	// The first batch chains to genesis; decode both envelopes and verify.
	var firstEnvelope []byte
	if err := store.db.QueryRowContext(ctx,
		"SELECT envelope FROM export_outbox WHERE append_id = 'append-chain-1'").Scan(&firstEnvelope); err != nil {
		t.Fatalf("read first envelope: %v", err)
	}
	codec, _ := auditCodecFor(1)
	decodedFirst, err := codec.Decode(firstEnvelope)
	if err != nil {
		t.Fatalf("decode first envelope: %v", err)
	}
	if decodedFirst.PreviousDigest != auditGenesisDigest {
		t.Fatal("first envelope does not chain to genesis")
	}
	if decodedFirst.CommitPosition != 1 || decodedFirst.AppendID != string(first.AppendID) {
		t.Fatalf("first envelope identity = %d/%s", decodedFirst.CommitPosition, decodedFirst.AppendID)
	}

	var secondEnvelope []byte
	if err := store.db.QueryRowContext(ctx,
		"SELECT envelope FROM export_outbox WHERE append_id = 'append-chain-2'").Scan(&secondEnvelope); err != nil {
		t.Fatalf("read second envelope: %v", err)
	}
	decodedSecond, err := codec.Decode(secondEnvelope)
	if err != nil {
		t.Fatalf("decode second envelope: %v", err)
	}
	if decodedSecond.CommitPosition != 2 || decodedSecond.AppendID != string(second.AppendID) {
		t.Fatalf("second envelope identity = %d/%s", decodedSecond.CommitPosition, decodedSecond.AppendID)
	}
	// Embedded event bytes are the canonical record payloads verbatim.
	var canonicalPayload []byte
	if err := store.db.QueryRowContext(ctx,
		"SELECT payload FROM events WHERE event_id = ?", "event-append-chain-2-0").Scan(&canonicalPayload); err != nil {
		t.Fatalf("read canonical payload: %v", err)
	}
	if len(decodedSecond.Events) != 1 || string(decodedSecond.Events[0]) != string(canonicalPayload) {
		t.Fatal("envelope event bytes differ from the canonical payload")
	}
	if decodedSecond.Events[0] != nil && auditEventPayloadDigest(decodedSecond.Events[0]) != sha256.Sum256(canonicalPayload) {
		t.Fatal("envelope event digest does not match the canonical payload digest")
	}
}

func TestAppendFaultLeavesNoAuditRows(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	request := appendRequest("append-chain-fault", "session-chain-fault", 0, "command-chain-fault",
		domain.SessionCreated{WorkspaceRoot: "/w"})
	store.FailNext(faultBeforeCommit, errTestFault)
	if _, err := store.Append(context.Background(), request); err == nil {
		t.Fatal("faulted append succeeded")
	}
	if got := tableCount(t, store, "export_outbox"); got != 0 {
		t.Fatalf("export_outbox rows = %d after rolled-back append, want 0", got)
	}
	var headAudit []byte
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT head_audit_digest FROM store_metadata WHERE id = 1").Scan(&headAudit); err != nil {
		t.Fatalf("read head digest: %v", err)
	}
	// The chain stays at the genesis seed: a rolled-back append must not
	// advance it.
	if string(headAudit) != string(auditGenesisDigest[:]) {
		t.Fatal("head audit digest advanced for a rolled-back append")
	}
}

func TestExactRetryKeepsSingleEnvelope(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	first, _ := auditChainFixtures(t, store)
	// First fixture appended two batches; retrying the first must not add
	// envelopes or alter the chain head.
	retry, err := store.Append(context.Background(), first)
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if retry.CommitPosition != 1 {
		t.Fatalf("retry receipt position = %d, want 1", retry.CommitPosition)
	}
	if got := tableCount(t, store, "export_outbox"); got != 2 {
		t.Fatalf("export_outbox rows = %d after retry, want 2", got)
	}
}

func deSliceAuditState(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.writer.ExecContext(ctx,
		"UPDATE event_appends SET audit_format_version = 0, previous_audit_digest = NULL, batch_audit_digest = NULL"); err != nil {
		t.Fatalf("clear audit columns: %v", err)
	}
	if _, err := store.writer.ExecContext(ctx, "DELETE FROM export_outbox"); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}
	if _, err := store.writer.ExecContext(ctx,
		"UPDATE store_metadata SET head_audit_digest = NULL WHERE id = 1"); err != nil {
		t.Fatalf("clear head digest: %v", err)
	}
}

func TestBackfillRebuildsChainFromPreSlice3State(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	auditChainFixtures(t, store)
	ctx := context.Background()

	// Capture the maintained chain for comparison.
	var maintainedHead []byte
	if err := store.db.QueryRowContext(ctx,
		"SELECT head_audit_digest FROM store_metadata WHERE id = 1").Scan(&maintainedHead); err != nil {
		t.Fatal(err)
	}

	deSliceAuditState(t, store)
	store.writeMu.Lock()
	err := func() error {
		if _, err := store.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		if err := backfillAuditChain(ctx, store.writer); err != nil {
			_, _ = store.writer.ExecContext(context.Background(), "ROLLBACK")
			return err
		}
		if _, err := store.writer.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		return nil
	}()
	store.writeMu.Unlock()
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var backfilledHead []byte
	if err := store.db.QueryRowContext(ctx,
		"SELECT head_audit_digest FROM store_metadata WHERE id = 1").Scan(&backfilledHead); err != nil {
		t.Fatal(err)
	}
	if string(backfilledHead) != string(maintainedHead) {
		t.Fatal("backfilled chain head differs from the maintained chain")
	}
	if got := tableCount(t, store, "export_outbox"); got != 2 {
		t.Fatalf("export_outbox rows = %d after backfill, want 2", got)
	}
}

func TestBackfillIsNoOpWhenChainMaintained(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	auditChainFixtures(t, store)
	ctx := context.Background()
	store.writeMu.Lock()
	err := backfillAuditChain(ctx, store.writer)
	store.writeMu.Unlock()
	if err != nil {
		t.Fatalf("backfill no-op: %v", err)
	}
	if got := tableCount(t, store, "export_outbox"); got != 2 {
		t.Fatalf("export_outbox rows = %d, want 2", got)
	}
}

func TestBackfillDigestMismatchAborts(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	auditChainFixtures(t, store)
	ctx := context.Background()
	deSliceAuditState(t, store)
	// Pre-seed a wrong digest on the first append; recomputation must abort.
	if _, err := store.writer.ExecContext(ctx,
		"UPDATE event_appends SET batch_audit_digest = ? WHERE append_id = 'append-chain-1'",
		make([]byte, 32)); err != nil {
		t.Fatalf("seed wrong digest: %v", err)
	}
	store.writeMu.Lock()
	err := backfillAuditChain(ctx, store.writer)
	store.writeMu.Unlock()
	if err == nil {
		t.Fatal("backfill with wrong pre-seeded digest = nil, want abort")
	}
}

var errTestFault = &staticError{"injected"}

type staticError struct{ msg string }

func (err *staticError) Error() string { return err.msg }
