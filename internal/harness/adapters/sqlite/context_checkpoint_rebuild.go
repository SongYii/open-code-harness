package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// derivedCheckpointHead is the furthest independently-valid checkpoint found
// by walking one session's canonical stream from D0, matching the same
// successor-chain rules updateContextCheckpointHead enforces at write time
// (initial checkpoint scans from D0; a successor starts from the last valid
// digest and scans only the newly covered range; a same-coverage rewrite
// requires an identical digest). A ContextCompactionCompleted event that
// fails this chain is simply not the furthest valid checkpoint -- it never
// should have landed under the write-time hook, so a rebuild only ever
// observes this for pre-hook or externally-imported history.
type derivedCheckpointHead struct {
	found           bool
	checkpointID    string
	eventID         string
	eventSequence   uint64
	throughSequence uint64
	digest          [32]byte
}

// readAllCanonicalRecords reads every canonical record for one session
// within the given transaction/connection, unbounded above -- unlike
// readCanonicalRange, whose upper bound must be a real sequence value: the
// sqlite driver used here rejects a uint64 query argument with the high bit
// set, so an unbounded scan cannot reuse it with a sentinel maximum.
func readAllCanonicalRecords(ctx context.Context, conn *sql.Conn, sessionID domain.SessionID) ([]domain.RecordedEvent, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT payload FROM events WHERE session_id = ? ORDER BY sequence", string(sessionID))
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
				wrapDetail("unreadable canonical event payload during checkpoint rebuild", err))
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, mapStorageError(err, sessionID)
	}
	return records, nil
}

func deriveContextCheckpointHead(records []domain.RecordedEvent) derivedCheckpointHead {
	seed := contextengine.InitialSourceDigest()
	var through uint64
	var head derivedCheckpointHead
	for _, record := range records {
		completed, ok := record.Event.(domain.ContextCompactionCompleted)
		if !ok {
			continue
		}
		checkpoint := completed.Checkpoint
		if checkpoint.ThroughSequence < through {
			continue
		}
		var rangeRecords []domain.RecordedEvent
		for _, candidate := range records {
			if candidate.Sequence <= through {
				continue
			}
			if candidate.Sequence > checkpoint.ThroughSequence {
				break
			}
			rangeRecords = append(rangeRecords, candidate)
		}
		digest, _, err := contextengine.ExtendSourceDigestOverRecords(seed, rangeRecords)
		if err != nil || hex.EncodeToString(digest[:]) != checkpoint.SourceDigestHex {
			continue
		}
		seed = digest
		through = checkpoint.ThroughSequence
		head = derivedCheckpointHead{
			found: true, checkpointID: checkpoint.ID, eventID: string(record.ID),
			eventSequence: record.Sequence, throughSequence: through, digest: digest,
		}
	}
	return head
}

// RebuildAndVerifyContextCheckpointHeads independently re-derives the
// furthest verified checkpoint for every session from canonical events
// alone (design §14.2), and reconciles context_checkpoint_heads against it:
// a missing row for a session with a genuinely valid checkpoint is repaired
// (written); an existing row that disagrees with the independently-derived
// truth is reported as store_corrupt, never silently overwritten. Unlike
// RebuildAndVerifySessionHeads (verify-only, since every session
// unconditionally has a head), a session with zero compactions correctly
// has no row at all -- "missing" is only ever corruption when canonical
// events prove a row should exist and disagree with what's stored.
func (store *Store) RebuildAndVerifyContextCheckpointHeads(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return newStoreError(application.StoreCodeUnavailable, "", err)
	}

	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	conn := store.writer
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return mapStorageError(err, "")
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	rows, err := conn.QueryContext(ctx, "SELECT session_id FROM event_streams ORDER BY session_id")
	if err != nil {
		return mapStorageError(err, "")
	}
	var sessions []string
	for rows.Next() {
		var session string
		if err := rows.Scan(&session); err != nil {
			rows.Close()
			return mapStorageError(err, "")
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return mapStorageError(err, "")
	}
	rows.Close()

	for _, sessionID := range sessions {
		records, err := readAllCanonicalRecords(ctx, conn, domain.SessionID(sessionID))
		if err != nil {
			return err
		}
		derived := deriveContextCheckpointHead(records)

		var storedID, storedEventID string
		var storedEventSequence, storedThrough uint64
		var storedDigest []byte
		err = conn.QueryRowContext(ctx,
			"SELECT checkpoint_id, checkpoint_event_id, checkpoint_event_sequence, covered_through_sequence, source_digest FROM context_checkpoint_heads WHERE session_id = ?",
			sessionID).Scan(&storedID, &storedEventID, &storedEventSequence, &storedThrough, &storedDigest)
		hasStored := true
		switch {
		case isNoRows(err):
			hasStored = false
		case err != nil:
			return mapStorageError(err, domain.SessionID(sessionID))
		}

		if !derived.found {
			if hasStored {
				return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
					wrapDetail("context_checkpoint_heads row has no independently-verifiable canonical checkpoint", nil))
			}
			continue
		}

		if hasStored {
			if storedID != derived.checkpointID || storedEventID != derived.eventID ||
				storedEventSequence != derived.eventSequence || storedThrough != derived.throughSequence ||
				hex.EncodeToString(storedDigest) != hex.EncodeToString(derived.digest[:]) {
				return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
					wrapDetail("context_checkpoint_heads disagrees with independently-derived canonical checkpoint", nil))
			}
			continue
		}

		var position uint64
		if err := conn.QueryRowContext(ctx,
			"SELECT ea.commit_position FROM events e JOIN event_appends ea ON ea.append_id = e.append_id WHERE e.session_id = ? AND e.event_id = ?",
			sessionID, derived.eventID).Scan(&position); err != nil {
			if isNoRows(err) {
				return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
					wrapDetail("derived checkpoint event has no matching append", nil))
			}
			return mapStorageError(err, domain.SessionID(sessionID))
		}
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO context_checkpoint_heads (session_id, checkpoint_event_sequence, checkpoint_event_id, checkpoint_id, covered_through_sequence, source_digest, updated_at_commit_position) VALUES (?, ?, ?, ?, ?, ?, ?)",
			sessionID, derived.eventSequence, derived.eventID, derived.checkpointID, derived.throughSequence, derived.digest[:], position); err != nil {
			return mapStorageError(err, domain.SessionID(sessionID))
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return mapStorageError(err, "")
	}
	committed = true
	return nil
}
