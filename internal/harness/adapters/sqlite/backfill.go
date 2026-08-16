package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"

	"github.com/SongYii/open-code-harness/internal/harness/application"
)

// maintainAuditChain computes the batch envelope inside the append
// transaction, records the audit columns, retains the exact canonical
// envelope in the outbox, and advances the head audit digest. The chain is
// created by commit-position order, never by the asynchronous exporter.
func (store *Store) maintainAuditChain(ctx context.Context, conn *sql.Conn, request application.AppendRequest, prepared *preparedAppend, position uint64, committedAtUnix float64) error {
	var headAudit []byte
	if err := conn.QueryRowContext(ctx,
		"SELECT head_audit_digest FROM store_metadata WHERE id = 1").Scan(&headAudit); err != nil {
		return mapStorageError(err, request.SessionID)
	}

	previous := auditGenesisDigest
	if headAudit != nil {
		if len(headAudit) != sha256.Size {
			return newStoreError(application.StoreCodeCorrupt, request.SessionID,
				wrapDetail("head audit digest has unexpected length", nil))
		}
		copy(previous[:], headAudit)
	}

	batch := auditBatch{
		FormatVersion:   auditFormatVersionV1,
		CommitPosition:  position,
		AppendID:        string(request.AppendID),
		CommandID:       string(request.CommandID),
		SessionID:       string(request.SessionID),
		ExpectedVersion: request.ExpectedVersion,
		FirstSequence:   prepared.events[0].record.Sequence,
		LastSequence:    prepared.events[len(prepared.events)-1].record.Sequence,
		CommittedAtUnix: committedAtUnix,
		PreviousDigest:  previous,
		Events:          make([][]byte, 0, len(prepared.events)),
	}
	for _, entry := range prepared.events {
		batch.Events = append(batch.Events, entry.payload)
	}
	codec, err := auditCodecFor(auditFormatVersionV1)
	if err != nil {
		return newStoreError(application.StoreCodeCorrupt, request.SessionID, err)
	}
	envelope, batchDigest, err := codec.Encode(batch)
	if err != nil {
		return appendRejected(request.SessionID, err)
	}
	envelopeDigest := sha256.Sum256(envelope)

	if _, err := conn.ExecContext(ctx,
		"UPDATE event_appends SET audit_format_version = ?, previous_audit_digest = ?, batch_audit_digest = ? WHERE append_id = ?",
		auditFormatVersionV1, previous[:], batchDigest[:], string(request.AppendID)); err != nil {
		return mapStorageError(err, request.SessionID)
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO export_outbox (commit_position, append_id, audit_format_version, envelope, envelope_digest, export_state) VALUES (?, ?, ?, ?, ?, 'pending')",
		position, string(request.AppendID), auditFormatVersionV1, envelope, envelopeDigest[:]); err != nil {
		return mapStorageError(err, request.SessionID)
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE store_metadata SET head_audit_digest = ? WHERE id = 1", batchDigest[:]); err != nil {
		return mapStorageError(err, request.SessionID)
	}
	return nil
}

// backfillAuditChain encodes every pre-Slice-3 append under codec v1,
// chaining from genesis in commit-position order. It is a no-op when the
// head audit digest is already maintained; a pre-existing digest that
// disagrees with recomputation aborts fail-closed. Runs inside the
// migration's single write transaction.
func backfillAuditChain(ctx context.Context, conn *sql.Conn) error {
	var headAudit []byte
	err := conn.QueryRowContext(ctx,
		"SELECT head_audit_digest FROM store_metadata WHERE id = 1").Scan(&headAudit)
	switch {
	case isNoRows(err):
		// Fresh database: the metadata singleton is created after migrations,
		// so create it here with an unmaintained audit digest.
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO store_metadata (id, storage_format_version, head_commit_position, head_audit_digest, created_at_unix, last_migration_at_unix) VALUES (1, ?, 0, NULL, unixepoch('subsec'), unixepoch('subsec'))",
			latestMigrationVersion); err != nil {
			return err
		}
	case err != nil:
		return err
	case headAudit != nil:
		return nil
	}

	previous := auditGenesisDigest

	appends, err := loadAuditAppendRows(ctx, conn, 0)
	if err != nil {
		return err
	}
	for _, row := range appends {
		var existingBatch []byte
		if err := conn.QueryRowContext(ctx,
			"SELECT batch_audit_digest FROM event_appends WHERE append_id = ?",
			row.appendID).Scan(&existingBatch); err != nil {
			return err
		}
		envelope, batchDigest, err := encodeAuditAppend(ctx, conn, row, previous)
		if err != nil {
			return err
		}
		// Determinism gate: any pre-existing digest must match recomputation.
		if existingBatch != nil && !bytesEqual(existingBatch, batchDigest[:]) {
			return &CorruptError{Detail: "audit backfill digest mismatch for append " + row.appendID}
		}
		envelopeDigest := sha256.Sum256(envelope)
		if _, err := conn.ExecContext(ctx,
			"UPDATE event_appends SET audit_format_version = ?, previous_audit_digest = ?, batch_audit_digest = ? WHERE append_id = ?",
			auditFormatVersionV1, previous[:], batchDigest[:], row.appendID); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO export_outbox (commit_position, append_id, audit_format_version, envelope, envelope_digest, export_state) VALUES (?, ?, ?, ?, ?, 'pending')",
			row.position, row.appendID, auditFormatVersionV1, envelope, envelopeDigest[:]); err != nil {
			return err
		}
		previous = batchDigest
	}

	if len(appends) > 0 {
		if _, err := conn.ExecContext(ctx,
			"UPDATE store_metadata SET head_audit_digest = ? WHERE id = 1", previous[:]); err != nil {
			return err
		}
	} else {
		if _, err := conn.ExecContext(ctx,
			"UPDATE store_metadata SET head_audit_digest = ? WHERE id = 1", auditGenesisDigest[:]); err != nil {
			return err
		}
	}
	return nil
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// auditAppendRow is one event_appends row in codec-neutral form.
type auditAppendRow struct {
	appendID        string
	position        uint64
	sessionID       string
	expectedVersion uint64
	first, last     uint64
	commandID       string
	committedAt     float64
}

// loadAuditAppendRows returns append rows at or after fromPosition in
// commit-position order.
func loadAuditAppendRows(ctx context.Context, conn *sql.Conn, fromPosition uint64) ([]auditAppendRow, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT append_id, commit_position, session_id, expected_version, first_sequence, last_sequence, command_id, committed_at_unix FROM event_appends WHERE commit_position >= ? ORDER BY commit_position",
		fromPosition)
	if err != nil {
		return nil, err
	}
	var appends []auditAppendRow
	for rows.Next() {
		var row auditAppendRow
		if err := rows.Scan(&row.appendID, &row.position, &row.sessionID, &row.expectedVersion,
			&row.first, &row.last, &row.commandID, &row.committedAt); err != nil {
			rows.Close()
			return nil, err
		}
		appends = append(appends, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return appends, nil
}

// rowsQueryer is the query surface shared by *sql.Conn and *sql.DB.
type rowsQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryRows(ctx context.Context, queryer rowsQueryer, query string, args ...any) (*sql.Rows, error) {
	return queryer.QueryContext(ctx, query, args...)
}

// encodeAuditAppend re-encodes one append under codec v1 from canonical
// bytes, chaining onto previous. The recomputed digest is authoritative.
func encodeAuditAppend(ctx context.Context, queryer rowsQueryer, row auditAppendRow, previous [sha256.Size]byte) ([]byte, [sha256.Size]byte, error) {
	codec, err := auditCodecFor(auditFormatVersionV1)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	eventRows, err := queryRows(ctx, queryer, "SELECT payload FROM events WHERE append_id = ? ORDER BY order_in_append", row.appendID)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	var payloads [][]byte
	for eventRows.Next() {
		var payload []byte
		if err := eventRows.Scan(&payload); err != nil {
			eventRows.Close()
			return nil, [sha256.Size]byte{}, err
		}
		payloads = append(payloads, payload)
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return nil, [sha256.Size]byte{}, err
	}
	eventRows.Close()
	batch := auditBatch{
		FormatVersion:   auditFormatVersionV1,
		CommitPosition:  row.position,
		AppendID:        row.appendID,
		CommandID:       row.commandID,
		SessionID:       row.sessionID,
		ExpectedVersion: row.expectedVersion,
		FirstSequence:   row.first,
		LastSequence:    row.last,
		CommittedAtUnix: row.committedAt,
		PreviousDigest:  previous,
		Events:          payloads,
	}
	return codec.Encode(batch)
}
