package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// AcquireLease takes or renews the singleton runtime lease in one
// BEGIN IMMEDIATE transaction. An absent or expired lease is taken with a new
// monotonically increasing token; a live lease held by another runtime is
// refused. SQLite's unixepoch('subsec') is the only lease clock.
func (store *Store) AcquireLease(ctx context.Context) (application.WriterAuthority, error) {
	if err := contextError(ctx); err != nil {
		return application.WriterAuthority{}, newStoreError(application.StoreCodeUnavailable, "", err)
	}
	if _, err := application.ParseRuntimeID(string(application.RuntimeID(store.config.RuntimeID))); err != nil {
		return application.WriterAuthority{}, fmt.Errorf("sqlite lease: %w", err)
	}

	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	conn := store.writer

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return application.WriterAuthority{}, mapStorageError(err, "")
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var runtimeID string
	var fencingToken uint64
	var expiresAt float64
	err := conn.QueryRowContext(ctx,
		"SELECT runtime_id, fencing_token, lease_expires_at_unix FROM runtime_leases WHERE id = 1").Scan(&runtimeID, &fencingToken, &expiresAt)
	var authority application.WriterAuthority
	switch {
	case isNoRows(err):
		authority = application.WriterAuthority{RuntimeID: application.RuntimeID(store.config.RuntimeID), FencingToken: 1}
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO runtime_leases (id, runtime_id, fencing_token, lease_expires_at_unix, last_heartbeat_at_unix) VALUES (1, ?, ?, unixepoch('subsec') + ?, unixepoch('subsec'))",
			string(application.RuntimeID(store.config.RuntimeID)), authority.FencingToken, store.config.LeaseDuration.Seconds()); err != nil {
			return application.WriterAuthority{}, mapStorageError(err, "")
		}
	case err != nil:
		return application.WriterAuthority{}, mapStorageError(err, "")
	default:
		var now float64
		if err := conn.QueryRowContext(ctx, "SELECT unixepoch('subsec')").Scan(&now); err != nil {
			return application.WriterAuthority{}, mapStorageError(err, "")
		}
		switch {
		case expiresAt < now:
			authority = application.WriterAuthority{RuntimeID: application.RuntimeID(store.config.RuntimeID), FencingToken: fencingToken + 1}
		case runtimeID == string(application.RuntimeID(store.config.RuntimeID)):
			authority = application.WriterAuthority{RuntimeID: application.RuntimeID(store.config.RuntimeID), FencingToken: fencingToken}
		default:
			return application.WriterAuthority{}, &ErrLeaseHeld{Owner: runtimeID}
		}
		if _, err := conn.ExecContext(ctx,
			"UPDATE runtime_leases SET runtime_id = ?, fencing_token = ?, lease_expires_at_unix = unixepoch('subsec') + ?, last_heartbeat_at_unix = unixepoch('subsec') WHERE id = 1",
			string(application.RuntimeID(store.config.RuntimeID)), authority.FencingToken, store.config.LeaseDuration.Seconds()); err != nil {
			return application.WriterAuthority{}, mapStorageError(err, "")
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return application.WriterAuthority{}, mapStorageError(err, "")
	}
	committed = true
	store.authority = authority
	return authority, nil
}

// RenewLease extends the lease and stamps the heartbeat. A lease no longer
// owned by this runtime fences the renewal.
func (store *Store) RenewLease(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return newStoreError(application.StoreCodeUnavailable, "", err)
	}
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	result, err := store.writer.ExecContext(ctx,
		"UPDATE runtime_leases SET lease_expires_at_unix = unixepoch('subsec') + ?, last_heartbeat_at_unix = unixepoch('subsec') WHERE id = 1 AND runtime_id = ? AND fencing_token = ? AND lease_expires_at_unix >= unixepoch('subsec')",
		store.config.LeaseDuration.Seconds(), string(store.authority.RuntimeID), store.authority.FencingToken)
	if err != nil {
		return mapStorageError(err, "")
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return mapStorageError(err, "")
	}
	if updated == 0 {
		return newStoreError(application.StoreCodeWriterFenced, "", nil)
	}
	return nil
}

var _ application.AuthoritySource = (*Store)(nil)

// Authority returns the lease authority acquired at open. The read takes
// the write lock: AcquireLease mutates the field during expired takeover.
func (store *Store) Authority() application.WriterAuthority {
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	return store.authority
}

// CurrentAuthority implements application.AuthoritySource with the live
// lease state, so a fencing-token rotation performed by expired takeover is
// visible to the next append instead of stranding holders of a snapshot.
func (store *Store) CurrentAuthority() application.WriterAuthority {
	return store.Authority()
}

// verifyLeaseForAppend enforces the ownership predicate on every new append:
// an exact runtime and token match on a live lease.
func (store *Store) verifyLeaseForAppend(ctx context.Context, conn *sql.Conn, request application.AppendRequest) error {
	var runtimeID string
	var fencingToken uint64
	var expiresAt float64
	err := conn.QueryRowContext(ctx,
		"SELECT runtime_id, fencing_token, lease_expires_at_unix FROM runtime_leases WHERE id = 1").Scan(&runtimeID, &fencingToken, &expiresAt)
	switch {
	case isNoRows(err):
		return newStoreError(application.StoreCodeWriterFenced, request.SessionID, nil)
	case err != nil:
		return mapStorageError(err, request.SessionID)
	}
	var now float64
	if err := conn.QueryRowContext(ctx, "SELECT unixepoch('subsec')").Scan(&now); err != nil {
		return mapStorageError(err, request.SessionID)
	}
	if runtimeID != string(request.Authority.RuntimeID) || fencingToken != request.Authority.FencingToken || expiresAt < now {
		return newStoreError(application.StoreCodeWriterFenced, request.SessionID, nil)
	}
	return nil
}

// rotateAuthorityForTesting replaces the lease row through the adapter's own
// writer connection. It exists for the conformance harness only.
func (store *Store) rotateAuthorityForTesting(authority application.WriterAuthority) error {
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	ctx := context.Background()
	if _, err := store.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return mapStorageError(err, "")
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = store.writer.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	result, err := store.writer.ExecContext(ctx,
		"UPDATE runtime_leases SET runtime_id = ?, fencing_token = ?, lease_expires_at_unix = unixepoch('subsec') + 3600, last_heartbeat_at_unix = unixepoch('subsec') WHERE id = 1",
		string(authority.RuntimeID), authority.FencingToken)
	if err != nil {
		return mapStorageError(err, "")
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return mapStorageError(err, "")
	}
	if updated == 0 {
		if _, err := store.writer.ExecContext(ctx,
			"INSERT INTO runtime_leases (id, runtime_id, fencing_token, lease_expires_at_unix, last_heartbeat_at_unix) VALUES (1, ?, ?, unixepoch('subsec') + 3600, unixepoch('subsec'))",
			string(authority.RuntimeID), authority.FencingToken); err != nil {
			return mapStorageError(err, "")
		}
	}
	if _, err := store.writer.ExecContext(ctx, "COMMIT"); err != nil {
		return mapStorageError(err, "")
	}
	committed = true
	store.authority = authority
	return nil
}

// corruptReceiptForTesting moves one stored receipt to a different but
// otherwise plausible range, mirroring the reference adapter's
// corruption seam. Receipt validation must detect it through cross-checks.
func (store *Store) corruptReceiptForTesting(appendID domain.AppendID) error {
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	ctx := context.Background()
	if _, err := store.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return mapStorageError(err, "")
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = store.writer.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	// The derived outbox row references the receipt's commit position; drop
	// it first (outbox rows are prunable derived data) so the receipt row
	// itself can move.
	if _, err := store.writer.ExecContext(ctx,
		"DELETE FROM export_outbox WHERE append_id = ?", string(appendID)); err != nil {
		return mapStorageError(err, "")
	}
	if _, err := store.writer.ExecContext(ctx,
		"UPDATE event_appends SET first_sequence = (SELECT version + 1 FROM event_streams WHERE session_id = event_appends.session_id), "+
			"last_sequence = (SELECT version + 1 FROM event_streams WHERE session_id = event_appends.session_id) "+
			"WHERE append_id = ?", string(appendID)); err != nil {
		return mapStorageError(err, "")
	}
	if _, err := store.writer.ExecContext(ctx, "COMMIT"); err != nil {
		return mapStorageError(err, "")
	}
	committed = true
	return nil
}

// ErrLeaseHeld reports that a live foreign runtime owns the lease.
type ErrLeaseHeld struct {
	Owner string
}

func (err *ErrLeaseHeld) Error() string {
	return "sqlite lease: held by live runtime " + err.Owner
}

// ActiveSessions enumerates sessions whose heads projection says running.
// The projection is never proof; callers confirm by stream replay.
func (store *Store) ActiveSessions(ctx context.Context) ([]domain.SessionID, error) {
	rows, err := store.db.QueryContext(ctx,
		"SELECT session_id FROM session_heads WHERE status = 'running' ORDER BY session_id")
	if err != nil {
		return nil, mapStorageError(err, "")
	}
	var sessions []domain.SessionID
	for rows.Next() {
		var session string
		if err := rows.Scan(&session); err != nil {
			rows.Close()
			return nil, mapStorageError(err, "")
		}
		sessions = append(sessions, domain.SessionID(session))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, mapStorageError(err, "")
	}
	rows.Close()
	return sessions, nil
}

// ReleaseLease expires the lease when — and only when — it is still owned
// by this runtime and fencing token, so a stale host can never release a
// successor's lease. Releasing an already-lost lease reports fencing.
func (store *Store) ReleaseLease(ctx context.Context) error {
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	result, err := store.writer.ExecContext(ctx,
		"UPDATE runtime_leases SET lease_expires_at_unix = unixepoch('subsec') WHERE id = 1 AND runtime_id = ? AND fencing_token = ?",
		string(store.authority.RuntimeID), store.authority.FencingToken)
	if err != nil {
		return mapStorageError(err, "")
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return mapStorageError(err, "")
	}
	if updated == 0 {
		return newStoreError(application.StoreCodeWriterFenced, "", nil)
	}
	return nil
}

// ExpireLeaseForTesting forces the current lease to expiry; it simulates
// an abandoned crashed owner for recovery tests.
func (store *Store) ExpireLeaseForTesting(ctx context.Context) error {
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	_, err := store.writer.ExecContext(ctx,
		"UPDATE runtime_leases SET lease_expires_at_unix = 0 WHERE id = 1")
	if err != nil {
		return mapStorageError(err, "")
	}
	return nil
}
