package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

var _ application.ContextCheckpointStore = (*Store)(nil)

// LoadLatestContextCheckpoint implements application.ContextCheckpointStore
// (design §6.4/§14.1). It joins the canonical context.compaction.completed
// event this Session's context_checkpoint_heads row points at for the
// checkpoint payload itself, verifying row/event agreement -- the row is
// only ever accepted into the table (updateContextCheckpointHead, below)
// after an independent hash-chain re-verification inside the SAME append
// transaction that committed the event, so a normal read here trusts that
// already-proven agreement rather than re-verifying on every read.
func (store *Store) LoadLatestContextCheckpoint(ctx context.Context, sessionID domain.SessionID) (application.ContextCheckpointLookup, error) {
	if err := contextError(ctx); err != nil {
		return application.ContextCheckpointLookup{}, readRejected(sessionID, err)
	}
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return application.ContextCheckpointLookup{}, readRejected(sessionID, err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.ContextCheckpointLookup{}, mapStorageError(err, sessionID)
	}
	defer func() { _ = tx.Rollback() }()

	var checkpointEventID string
	var checkpointEventSequence, coveredThroughSequence uint64
	var checkpointID string
	var sourceDigest []byte
	err = tx.QueryRowContext(ctx,
		"SELECT checkpoint_event_id, checkpoint_event_sequence, checkpoint_id, covered_through_sequence, source_digest FROM context_checkpoint_heads WHERE session_id = ?",
		string(sessionID)).Scan(&checkpointEventID, &checkpointEventSequence, &checkpointID, &coveredThroughSequence, &sourceDigest)
	switch {
	case isNoRows(err):
		if err := tx.Commit(); err != nil {
			return application.ContextCheckpointLookup{}, mapStorageError(err, sessionID)
		}
		return application.ContextCheckpointLookup{Status: application.ContextCheckpointLookupNone}, nil
	case err != nil:
		return application.ContextCheckpointLookup{}, mapStorageError(err, sessionID)
	}

	var payload []byte
	var rowSequence uint64
	err = tx.QueryRowContext(ctx,
		"SELECT sequence, payload FROM events WHERE session_id = ? AND event_id = ?",
		string(sessionID), checkpointEventID).Scan(&rowSequence, &payload)
	switch {
	case isNoRows(err):
		return application.ContextCheckpointLookup{}, newStoreError(application.StoreCodeCorrupt, sessionID,
			wrapDetail("context_checkpoint_heads points at a missing canonical event", nil))
	case err != nil:
		return application.ContextCheckpointLookup{}, mapStorageError(err, sessionID)
	}
	if rowSequence != checkpointEventSequence {
		return application.ContextCheckpointLookup{}, newStoreError(application.StoreCodeCorrupt, sessionID,
			wrapDetail("context_checkpoint_heads sequence disagrees with the canonical event", nil))
	}
	record, err := domain.UnmarshalRecordedEvent(payload)
	if err != nil {
		return application.ContextCheckpointLookup{}, newStoreError(application.StoreCodeCorrupt, sessionID,
			wrapDetail("unreadable canonical context.compaction.completed payload", err))
	}
	completed, ok := record.Event.(domain.ContextCompactionCompleted)
	if !ok {
		return application.ContextCheckpointLookup{}, newStoreError(application.StoreCodeCorrupt, sessionID,
			wrapDetail("context_checkpoint_heads points at a non-completion event", nil))
	}
	if err := tx.Commit(); err != nil {
		return application.ContextCheckpointLookup{}, mapStorageError(err, sessionID)
	}

	checkpoint := completed.Checkpoint
	if checkpoint.ID != checkpointID || checkpoint.ThroughSequence != coveredThroughSequence || checkpoint.SourceDigestHex != hex.EncodeToString(sourceDigest) {
		return application.ContextCheckpointLookup{}, newStoreError(application.StoreCodeCorrupt, sessionID,
			wrapDetail("context_checkpoint_heads row disagrees with its own canonical event", nil))
	}
	return application.ContextCheckpointLookup{Status: application.ContextCheckpointLookupFound, Checkpoint: checkpoint}, nil
}

// updateContextCheckpointHead is Append's own compaction-projection step
// (design §14.1), called from the same append transaction that commits a
// context.compaction.completed event. Before accepting the row update, it
// independently re-verifies the claimed checkpoint's coverage boundary and
// hash chain against canonical events already committed in THIS
// transaction: an initial checkpoint (no existing row) scans from D0; a
// successor starts from the row's own indexed predecessor digest and scans
// only the newly covered range; a same-coverage rewrite (identical
// ThroughSequence) scans nothing new and so must already match exactly.
// Any verification failure returns a store_corrupt error, which rolls back
// the whole append transaction (event included) via Append's own deferred
// ROLLBACK -- the row is never updated from an unverified claim.
func (store *Store) updateContextCheckpointHead(ctx context.Context, conn *sql.Conn, sessionID domain.SessionID, prepared *preparedAppend, position uint64) error {
	for _, entry := range prepared.events {
		completed, ok := entry.record.Event.(domain.ContextCompactionCompleted)
		if !ok {
			continue
		}
		checkpoint := completed.Checkpoint

		var previousDigest []byte
		var previousThrough uint64
		hasPrevious := true
		err := conn.QueryRowContext(ctx,
			"SELECT source_digest, covered_through_sequence FROM context_checkpoint_heads WHERE session_id = ?",
			string(sessionID)).Scan(&previousDigest, &previousThrough)
		switch {
		case isNoRows(err):
			hasPrevious = false
		case err != nil:
			return mapStorageError(err, sessionID)
		}

		var seed [32]byte
		var afterSequence uint64
		if hasPrevious {
			if len(previousDigest) != 32 {
				return newStoreError(application.StoreCodeCorrupt, sessionID,
					wrapDetail("stored context checkpoint digest has unexpected length", nil))
			}
			copy(seed[:], previousDigest)
			afterSequence = previousThrough
		} else {
			seed = contextengine.InitialSourceDigest()
			afterSequence = 0
		}
		if checkpoint.ThroughSequence < afterSequence {
			return newStoreError(application.StoreCodeCorrupt, sessionID,
				wrapDetail("checkpoint coverage moved backward", nil))
		}

		records, err := readCanonicalRange(ctx, conn, sessionID, afterSequence, checkpoint.ThroughSequence)
		if err != nil {
			return err
		}
		computedDigest, _, err := contextengine.ExtendSourceDigestOverRecords(seed, records)
		if err != nil {
			return newStoreError(application.StoreCodeCorrupt, sessionID,
				wrapDetail("context checkpoint source digest could not be recomputed", err))
		}
		claimedDigest, err := hex.DecodeString(checkpoint.SourceDigestHex)
		if err != nil || len(claimedDigest) != 32 || hex.EncodeToString(computedDigest[:]) != checkpoint.SourceDigestHex {
			return newStoreError(application.StoreCodeCorrupt, sessionID,
				wrapDetail("context checkpoint source digest does not match canonical events", nil))
		}

		if _, err := conn.ExecContext(ctx,
			"INSERT INTO context_checkpoint_heads (session_id, checkpoint_event_sequence, checkpoint_event_id, checkpoint_id, covered_through_sequence, source_digest, updated_at_commit_position) VALUES (?, ?, ?, ?, ?, ?, ?) "+
				"ON CONFLICT(session_id) DO UPDATE SET checkpoint_event_sequence = excluded.checkpoint_event_sequence, checkpoint_event_id = excluded.checkpoint_event_id, checkpoint_id = excluded.checkpoint_id, covered_through_sequence = excluded.covered_through_sequence, source_digest = excluded.source_digest, updated_at_commit_position = excluded.updated_at_commit_position",
			string(sessionID), entry.record.Sequence, string(entry.record.ID), checkpoint.ID, checkpoint.ThroughSequence, computedDigest[:], position); err != nil {
			return mapStorageError(err, sessionID)
		}
	}
	return nil
}

// readCanonicalRange reads exactly the canonical records with
// afterSequence < sequence <= throughSequence for one Session, within the
// given transaction/connection -- the same shape read.go's own ReadStream
// query uses, reused here so the digest recomputation observes the exact
// rows this same append transaction has (or is about to) commit.
func readCanonicalRange(ctx context.Context, conn *sql.Conn, sessionID domain.SessionID, afterSequence, throughSequence uint64) ([]domain.RecordedEvent, error) {
	if throughSequence <= afterSequence {
		return nil, nil
	}
	rows, err := conn.QueryContext(ctx,
		"SELECT payload FROM events WHERE session_id = ? AND sequence > ? AND sequence <= ? ORDER BY sequence",
		string(sessionID), afterSequence, throughSequence)
	if err != nil {
		return nil, mapStorageError(err, sessionID)
	}
	defer rows.Close()
	var records []domain.RecordedEvent
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, mapStorageError(err, sessionID)
		}
		record, err := domain.UnmarshalRecordedEvent(payload)
		if err != nil {
			return nil, newStoreError(application.StoreCodeCorrupt, sessionID,
				wrapDetail("unreadable canonical event payload during checkpoint verification", err))
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, mapStorageError(err, sessionID)
	}
	return records, nil
}
