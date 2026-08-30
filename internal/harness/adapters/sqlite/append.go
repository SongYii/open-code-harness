package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// Append executes the canonical append transaction. Receipt resolution
// precedes fencing validation, so a fenced process may learn that its exact
// request already committed but cannot create a new commit. The batch is
// wholly visible or wholly absent.
func (store *Store) Append(ctx context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	if err := contextError(ctx); err != nil {
		return application.CommitReceipt{}, appendRejected(request.SessionID, err)
	}

	// Clone and digest before touching storage: the immutable identity used
	// for receipt publication is independent of caller mutations that race
	// with Append. DigestAppendRequest also enforces IDs, schema versions,
	// UTC timestamps, and event-count and byte limits.
	cloned, err := cloneAppendRequest(request)
	if err != nil {
		return application.CommitReceipt{}, appendRejected(request.SessionID, err)
	}
	digest, err := application.DigestAppendRequest(cloned)
	if err != nil {
		return application.CommitReceipt{}, appendRejected(request.SessionID, err)
	}

	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	conn := store.writer

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	// Receipt resolution: an existing AppendID with the same digest returns
	// the original receipt even after the stream advanced; a different digest
	// is an identity mismatch that never commits.
	receipt, found, err := store.lookupReceipt(ctx, conn, request.AppendID, digest, request.SessionID)
	switch {
	case err != nil:
		return application.CommitReceipt{}, err
	case found:
		return receipt, nil
	}

	if err := store.verifyLeaseForAppend(ctx, conn, request); err != nil {
		return application.CommitReceipt{}, err
	}

	if request.Admission != nil {
		var existingSession string
		var existingDigest []byte
		err := conn.QueryRowContext(ctx,
			"SELECT session_id, request_digest FROM command_requests WHERE run_turn_request_id = ?",
			string(request.Admission.RunTurnRequestID)).Scan(&existingSession, &existingDigest)
		switch {
		case err == nil:
			if existingSession == string(request.SessionID) && !bytes.Equal(existingDigest, request.Admission.RequestDigest[:]) {
				return application.CommitReceipt{}, newStoreError(application.StoreCodeCommandIdentityMismatch, request.SessionID, nil)
			}
			return application.CommitReceipt{}, newStoreError(application.StoreCodeCommandRequestConflict, request.SessionID, nil)
		case !isNoRows(err):
			return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
		}
	}

	var current uint64
	err = conn.QueryRowContext(ctx,
		"SELECT version FROM event_streams WHERE session_id = ?", string(request.SessionID)).Scan(&current)
	switch {
	case isNoRows(err):
		current = 0
	case err != nil:
		return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
	}
	if current != request.ExpectedVersion {
		return application.CommitReceipt{}, store.versionConflict(request.SessionID, request.ExpectedVersion, current)
	}

	prepared, err := prepareEvents(cloned, digest, current)
	if err != nil {
		return application.CommitReceipt{}, appendRejected(request.SessionID, err)
	}
	if request.Admission != nil {
		if !containsTurnID(prepared.turnIDs, request.Admission.TurnID) || !containsItemID(prepared.itemIDs, request.Admission.ItemID) {
			return application.CommitReceipt{}, appendRejected(request.SessionID, fmt.Errorf("admission requires matching turn and item start events"))
		}
	}

	for _, entry := range prepared.events {
		var exists int
		err := conn.QueryRowContext(ctx,
			"SELECT 1 FROM events WHERE event_id = ?", string(entry.record.ID)).Scan(&exists)
		switch {
		case err == nil:
			return application.CommitReceipt{}, appendRejected(request.SessionID, fmt.Errorf("duplicate event ID %q", entry.record.ID))
		case !isNoRows(err):
			return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
		}
		for _, reservation := range entry.reservations {
			err := conn.QueryRowContext(ctx,
				"SELECT 1 FROM domain_identities WHERE session_id = ? AND identity_kind = ? AND identity_id = ?",
				string(request.SessionID), reservation.kind, reservation.id).Scan(&exists)
			switch {
			case err == nil:
				return application.CommitReceipt{}, domainIdentityError(request.SessionID, reservation.kind)
			case !isNoRows(err):
				return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
			}
		}
	}

	var headPosition uint64
	if err := conn.QueryRowContext(ctx,
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&headPosition); err != nil {
		return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
	}
	position := headPosition + 1
	receipt = application.CommitReceipt{
		AppendID:       request.AppendID,
		CommitPosition: position,
		FirstSequence:  current + 1,
		LastSequence:   current + uint64(len(prepared.events)),
	}

	if _, err := conn.ExecContext(ctx,
		"INSERT INTO event_streams (session_id, version, created_at_commit_position, last_append_commit_position) VALUES (?, ?, ?, ?) "+
			"ON CONFLICT(session_id) DO UPDATE SET version = excluded.version, last_append_commit_position = excluded.last_append_commit_position",
		string(request.SessionID), receipt.LastSequence, position, position); err != nil {
		return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
	}

	var committedAtUnix float64
	if err := conn.QueryRowContext(ctx, "SELECT unixepoch('subsec')").Scan(&committedAtUnix); err != nil {
		return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
	}

	if _, err := conn.ExecContext(ctx,
		"INSERT INTO event_appends (append_id, commit_position, session_id, expected_version, first_sequence, last_sequence, event_count, command_id, request_digest, writer_runtime_id, writer_fencing_token, committed_at_unix) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		string(request.AppendID), position, string(request.SessionID), request.ExpectedVersion,
		receipt.FirstSequence, receipt.LastSequence, len(prepared.events), string(request.CommandID),
		digest[:], string(request.Authority.RuntimeID), request.Authority.FencingToken, committedAtUnix); err != nil {
		return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
	}

	if request.Admission != nil {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO command_requests (run_turn_request_id, request_digest, session_id, command_id, turn_id, item_id, admission_append_id) VALUES (?, ?, ?, ?, ?, ?, ?)",
			string(request.Admission.RunTurnRequestID), request.Admission.RequestDigest[:], string(request.SessionID),
			string(request.CommandID), string(request.Admission.TurnID), string(request.Admission.ItemID), string(request.AppendID)); err != nil {
			return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
		}
	}

	for _, entry := range prepared.events {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO events (session_id, sequence, event_id, append_id, order_in_append, command_id, event_type, schema_version, occurred_at, payload, payload_digest) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			string(request.SessionID), entry.record.Sequence, string(entry.record.ID), string(request.AppendID),
			entry.record.Sequence-current, string(request.CommandID), entry.eventType, entry.record.SchemaVersion,
			entry.record.OccurredAt.Format(time.RFC3339Nano), entry.payload, entry.payloadDigest[:]); err != nil {
			return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
		}
		for _, reservation := range entry.reservations {
			if _, err := conn.ExecContext(ctx,
				"INSERT INTO domain_identities (session_id, identity_kind, identity_id, introducing_event_id) VALUES (?, ?, ?, ?)",
				string(request.SessionID), reservation.kind, reservation.id, string(reservation.introducingEvt)); err != nil {
				return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
			}
		}
	}

	if err := store.updateSessionHead(ctx, conn, request.SessionID, prepared, position); err != nil {
		return application.CommitReceipt{}, err
	}

	if err := store.maintainAuditChain(ctx, conn, request, prepared, position, committedAtUnix); err != nil {
		return application.CommitReceipt{}, err
	}

	if _, err := conn.ExecContext(ctx,
		"UPDATE store_metadata SET head_commit_position = ? WHERE id = 1", position); err != nil {
		return application.CommitReceipt{}, mapStorageError(err, request.SessionID)
	}

	if err := contextError(ctx); err != nil {
		return application.CommitReceipt{}, appendRejected(request.SessionID, err)
	}
	if cause := store.popFault(faultBeforeCommit); cause != nil {
		return application.CommitReceipt{}, newStoreError(application.StoreCodeUnavailable, request.SessionID, cause)
	}
	store.runCommitHook(commitHookBeforePublish)
	if err := contextError(ctx); err != nil {
		return application.CommitReceipt{}, appendRejected(request.SessionID, err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		// COMMIT was attempted: the outcome is never converted to a definite
		// non-commit. Release or quarantine the writer, then perform exactly
		// one bounded receipt lookup on a fresh connection.
		if _, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK"); rollbackErr != nil {
			if replaceErr := store.replaceWriterConn(context.Background()); replaceErr != nil {
				return application.CommitReceipt{}, newStoreError(application.StoreCodeCommitOutcomeUnknown, request.SessionID,
					fmt.Errorf("commit failed (%v); writer quarantine failed (%v)", err, replaceErr))
			}
			conn = store.writer
		}
		resolved, found, lookupErr := store.lookupReceipt(ctx, store.db, request.AppendID, digest, request.SessionID)
		if lookupErr == nil && found {
			return resolved, nil
		}
		if lookupErr != nil {
			err = fmt.Errorf("%w; receipt lookup also failed: %v", err, lookupErr)
		}
		return application.CommitReceipt{}, newStoreError(application.StoreCodeCommitOutcomeUnknown, request.SessionID, err)
	}
	committed = true
	store.runCommitHook(commitHookAfterPublish)
	if cause := store.popFault(faultAfterCommitBeforeAck); cause != nil {
		return application.CommitReceipt{}, newStoreError(application.StoreCodeCommitOutcomeUnknown, request.SessionID, cause)
	}
	return receipt, nil
}

// rowQueryer is the shared shape of *sql.Conn, *sql.DB, and *sql.Tx for
// receipt lookups.
type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// lookupReceipt resolves one AppendID against a request digest and validates
// the stored receipt by cross-checking the events actually committed under
// it. found=false means no row exists.
func (store *Store) lookupReceipt(ctx context.Context, queryer rowQueryer, appendID domain.AppendID, digest application.Digest, session domain.SessionID) (application.CommitReceipt, bool, error) {
	var storedDigest []byte
	var storedPosition, storedFirst, storedLast uint64
	var storedSession string
	var eventCount int
	err := queryer.QueryRowContext(ctx,
		"SELECT request_digest, commit_position, first_sequence, last_sequence, session_id, event_count FROM event_appends WHERE append_id = ?",
		string(appendID)).Scan(&storedDigest, &storedPosition, &storedFirst, &storedLast, &storedSession, &eventCount)
	switch {
	case isNoRows(err):
		return application.CommitReceipt{}, false, nil
	case err != nil:
		return application.CommitReceipt{}, false, mapStorageError(err, session)
	}
	if !bytes.Equal(storedDigest, digest[:]) {
		return application.CommitReceipt{}, false, newStoreError(application.StoreCodeAppendIdentityMismatch, session, nil)
	}
	if (session != "" && storedSession != string(session)) || storedPosition == 0 || storedFirst == 0 ||
		storedLast < storedFirst || storedLast != storedFirst+uint64(eventCount)-1 {
		return application.CommitReceipt{}, false, newStoreError(application.StoreCodeCorrupt, session,
			wrapDetail(fmt.Sprintf("stored receipt for append %q is inconsistent", appendID), nil))
	}
	var counted int
	var minSequence, maxSequence sql.NullInt64
	if err := queryer.QueryRowContext(ctx,
		"SELECT COUNT(*), MIN(sequence), MAX(sequence) FROM events WHERE append_id = ?",
		string(appendID)).Scan(&counted, &minSequence, &maxSequence); err != nil {
		return application.CommitReceipt{}, false, mapStorageError(err, session)
	}
	if counted != eventCount || !minSequence.Valid || !maxSequence.Valid ||
		uint64(minSequence.Int64) != storedFirst || uint64(maxSequence.Int64) != storedLast {
		return application.CommitReceipt{}, false, newStoreError(application.StoreCodeCorrupt, session,
			wrapDetail(fmt.Sprintf("receipt range for append %q disagrees with committed events", appendID), nil))
	}
	return application.CommitReceipt{
		AppendID:       appendID,
		CommitPosition: storedPosition,
		FirstSequence:  storedFirst,
		LastSequence:   storedLast,
	}, true, nil
}

// updateSessionHead maintains the one synchronous projection inside the
// append transaction. It is derived state, never authoritative.
func (store *Store) updateSessionHead(ctx context.Context, conn *sql.Conn, sessionID domain.SessionID, prepared *preparedAppend, position uint64) error {
	head := sessionHeadState{}
	err := conn.QueryRowContext(ctx,
		"SELECT workspace_root, status, active_turn_id, active_item_id, updated_at_commit_position FROM session_heads WHERE session_id = ?",
		string(sessionID)).Scan(&head.workspaceRoot, &head.status, &head.turn, &head.item, &head.position)
	switch {
	case isNoRows(err):
		head = sessionHeadState{}
	case err != nil:
		return mapStorageError(err, sessionID)
	}
	for _, entry := range prepared.events {
		head, err = applyHeadTransition(head, entry.record.Event)
		if err != nil {
			return newStoreError(application.StoreCodeCorrupt, sessionID, err)
		}
	}
	head.position = position
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO session_heads (session_id, workspace_root, status, active_turn_id, active_item_id, updated_at_commit_position) VALUES (?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT(session_id) DO UPDATE SET workspace_root = excluded.workspace_root, status = excluded.status, active_turn_id = excluded.active_turn_id, active_item_id = excluded.active_item_id, updated_at_commit_position = excluded.updated_at_commit_position",
		string(sessionID), head.workspaceRoot, head.status, head.turn, head.item, position); err != nil {
		return mapStorageError(err, sessionID)
	}
	return nil
}

func (store *Store) versionConflict(session domain.SessionID, expected, actual uint64) error {
	storeErr, err := application.NewStoreError(application.StoreError{
		Code:            application.StoreCodeVersionConflict,
		SessionID:       session,
		ExpectedVersion: expected,
		ActualVersion:   actual,
	})
	if err != nil {
		return err
	}
	return storeErr
}
