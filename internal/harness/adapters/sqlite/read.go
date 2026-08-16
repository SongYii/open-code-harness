package sqlite

import (
	"context"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ReadStream serves one pinned-head page from a single read transaction, so
// the stream version and the returned rows always observe one WAL snapshot.
// A pinned head that cannot be served consistently is an invalid read, never
// a silent empty page.
func (store *Store) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	if err := contextError(ctx); err != nil {
		return application.StreamPage{}, readRejected(request.SessionID, err)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil || request.Limit == 0 || request.Limit > 256 {
		return application.StreamPage{}, readRejected(request.SessionID, err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.StreamPage{}, mapStorageError(err, request.SessionID)
	}
	defer func() { _ = tx.Rollback() }()

	var currentHead uint64
	err = tx.QueryRowContext(ctx,
		"SELECT version FROM event_streams WHERE session_id = ?", string(request.SessionID)).Scan(&currentHead)
	if err != nil {
		if !isNoRows(err) {
			return application.StreamPage{}, mapStorageError(err, request.SessionID)
		}
		currentHead = 0
	}

	head := currentHead
	if request.HeadVersion != nil {
		head = *request.HeadVersion
	}
	if request.AfterSequence > head || head > currentHead || (request.HeadVersion != nil && head < request.AfterSequence) {
		return application.StreamPage{}, readRejected(request.SessionID, errInvalidPinnedCursor)
	}

	end := request.AfterSequence + uint64(request.Limit)
	if end > head {
		end = head
	}
	page := application.StreamPage{HeadVersion: head, NextAfterSequence: request.AfterSequence, End: request.AfterSequence == head}
	if request.AfterSequence >= end {
		if err := tx.Commit(); err != nil {
			return application.StreamPage{}, mapStorageError(err, request.SessionID)
		}
		return page, nil
	}

	rows, err := tx.QueryContext(ctx,
		"SELECT payload FROM events WHERE session_id = ? AND sequence > ? AND sequence <= ? ORDER BY sequence",
		string(request.SessionID), request.AfterSequence, end)
	if err != nil {
		return application.StreamPage{}, mapStorageError(err, request.SessionID)
	}
	defer rows.Close()
	records := make([]domain.RecordedEvent, 0, end-request.AfterSequence)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return application.StreamPage{}, mapStorageError(err, request.SessionID)
		}
		record, err := domain.UnmarshalRecordedEvent(payload)
		if err != nil {
			return application.StreamPage{}, newStoreError(application.StoreCodeCorrupt, request.SessionID,
				wrapDetail("unreadable canonical event payload", err))
		}
		if record.SessionID != request.SessionID || record.Sequence == 0 || record.Sequence > head {
			return application.StreamPage{}, newStoreError(application.StoreCodeCorrupt, request.SessionID,
				wrapDetail("event row disagrees with stream boundary", nil))
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return application.StreamPage{}, mapStorageError(err, request.SessionID)
	}
	if err := tx.Commit(); err != nil {
		return application.StreamPage{}, mapStorageError(err, request.SessionID)
	}

	if len(records) > 0 {
		page.Records = records
		page.NextAfterSequence = records[len(records)-1].Sequence
		page.End = page.NextAfterSequence == head
	}
	return page, nil
}
