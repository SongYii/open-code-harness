package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// classifyCode maps a primary SQLite result code to the stable Store error
// code. Busy and locked are bounded unavailability with the result code
// retained as cause; constraint and integrity failures on pre-validated
// single-writer paths are corruption; environment failures are
// unavailability.
func classifyCode(code int) application.StoreErrorCode {
	switch code & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return application.StoreCodeUnavailable
	case sqlite3.SQLITE_FULL, sqlite3.SQLITE_IOERR, sqlite3.SQLITE_READONLY,
		sqlite3.SQLITE_CANTOPEN, sqlite3.SQLITE_NOMEM, sqlite3.SQLITE_PERM,
		sqlite3.SQLITE_NOLFS, sqlite3.SQLITE_TOOBIG, sqlite3.SQLITE_INTERRUPT,
		sqlite3.SQLITE_ABORT, sqlite3.SQLITE_SCHEMA:
		return application.StoreCodeUnavailable
	case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
		return application.StoreCodeCorrupt
	case sqlite3.SQLITE_CONSTRAINT, sqlite3.SQLITE_MISMATCH,
		sqlite3.SQLITE_INTERNAL, sqlite3.SQLITE_MISUSE, sqlite3.SQLITE_RANGE,
		sqlite3.SQLITE_PROTOCOL:
		return application.StoreCodeCorrupt
	default:
		return application.StoreCodeUnavailable
	}
}

// mapStorageError converts a driver or database/sql error into the Store
// error algebra. Non-driver errors become unavailability with the original
// cause retained.
func mapStorageError(err error, session domain.SessionID) error {
	if err == nil {
		return nil
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return newStoreError(classifyCode(sqliteErr.Code()), session, err)
	}
	return newStoreError(application.StoreCodeUnavailable, session, err)
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func newStoreError(code application.StoreErrorCode, session domain.SessionID, cause error) error {
	storeErr, err := application.NewStoreError(application.StoreError{
		Code:      code,
		SessionID: session,
		Cause:     cause,
	})
	if err != nil {
		return fmt.Errorf("sqlite adapter: construct store error: %w", err)
	}
	return storeErr
}

func appendRejected(session domain.SessionID, cause error) error {
	return newStoreError(application.StoreCodeInvalidAppend, session, cause)
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
