package sqlite

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type sessionHeadCursor struct {
	Version  int    `json:"v"`
	Position uint64 `json:"p"`
	Session  string `json:"s"`
}

type listedSessionHead struct {
	head     application.SessionHead
	position uint64
}

func (store *Store) ListSessionHeads(ctx context.Context, request application.ListSessionHeadsRequest) (application.SessionHeadPage, error) {
	if err := contextError(ctx); err != nil {
		return application.SessionHeadPage{}, readRejected("", err)
	}
	root, err := application.CanonicalWorkspaceRoot(request.WorkspaceRoot)
	if err != nil || root != request.WorkspaceRoot || request.Limit == 0 || request.Limit > 256 {
		return application.SessionHeadPage{}, readRejected("", fmt.Errorf("invalid session head request"))
	}
	cursor, hasCursor, err := decodeSessionHeadCursor(request.Cursor)
	if err != nil {
		return application.SessionHeadPage{}, readRejected("", err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.SessionHeadPage{}, mapStorageError(err, "")
	}
	defer func() { _ = tx.Rollback() }()

	query := "SELECT h.session_id, h.workspace_root, h.status, h.updated_at_commit_position, a.committed_at_unix " +
		"FROM session_heads AS h JOIN event_appends AS a ON a.commit_position = h.updated_at_commit_position " +
		"WHERE h.workspace_root = ? AND h.status <> 'deleted' "
	args := []any{root}
	if hasCursor {
		query += "AND (h.updated_at_commit_position < ? OR (h.updated_at_commit_position = ? AND h.session_id < ?)) "
		args = append(args, cursor.Position, cursor.Position, cursor.Session)
	}
	query += "ORDER BY h.updated_at_commit_position DESC, h.session_id DESC LIMIT ?"
	args = append(args, request.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return application.SessionHeadPage{}, mapStorageError(err, "")
	}

	heads := make([]listedSessionHead, 0, request.Limit+1)
	for rows.Next() {
		var sessionID, workspaceRoot, status string
		var position uint64
		var committedAtUnix float64
		if err := rows.Scan(&sessionID, &workspaceRoot, &status, &position, &committedAtUnix); err != nil {
			rows.Close()
			return application.SessionHeadPage{}, mapStorageError(err, "")
		}
		parsedID, err := domain.ParseSessionID(sessionID)
		if err != nil {
			rows.Close()
			return application.SessionHeadPage{}, newStoreError(application.StoreCodeCorrupt, "", err)
		}
		canonicalRoot, err := application.CanonicalWorkspaceRoot(workspaceRoot)
		if err != nil || canonicalRoot != root || workspaceRoot != canonicalRoot || position == 0 || committedAtUnix <= 0 {
			rows.Close()
			return application.SessionHeadPage{}, newStoreError(application.StoreCodeCorrupt, parsedID,
				wrapDetail("invalid session head catalog row", err))
		}
		headStatus := application.SessionHeadStatus(status)
		switch headStatus {
		case application.SessionHeadStatusIdle, application.SessionHeadStatusRunning, application.SessionHeadStatusClosed:
		default:
			rows.Close()
			return application.SessionHeadPage{}, newStoreError(application.StoreCodeCorrupt, parsedID,
				wrapDetail("invalid visible session head status", nil))
		}
		heads = append(heads, listedSessionHead{
			head: application.SessionHead{
				SessionID:     parsedID,
				WorkspaceRoot: canonicalRoot,
				Status:        headStatus,
				UpdatedAt:     unixMillisecondsTime(committedAtUnix),
			},
			position: position,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return application.SessionHeadPage{}, mapStorageError(err, "")
	}
	if err := rows.Close(); err != nil {
		return application.SessionHeadPage{}, mapStorageError(err, "")
	}
	if err := tx.Commit(); err != nil {
		return application.SessionHeadPage{}, mapStorageError(err, "")
	}

	count := len(heads)
	if count > int(request.Limit) {
		count = int(request.Limit)
	}
	page := application.SessionHeadPage{Sessions: make([]application.SessionHead, count)}
	for index := 0; index < count; index++ {
		page.Sessions[index] = heads[index].head
	}
	if len(heads) > count {
		last := heads[count-1]
		page.NextCursor, err = encodeSessionHeadCursor(sessionHeadCursor{
			Version: 1, Position: last.position, Session: string(last.head.SessionID),
		})
		if err != nil {
			return application.SessionHeadPage{}, newStoreError(application.StoreCodeCorrupt, "", err)
		}
	}
	return page, nil
}

// unixMillisecondsTime converts SQLite's unixepoch('subsec') REAL, whose
// documented precision is milliseconds, without magnifying its binary
// floating-point representation into nanoseconds.
func unixMillisecondsTime(value float64) time.Time {
	seconds := int64(value)
	nanoseconds := int64(math.Round((value-float64(seconds))*1e3)) * int64(time.Millisecond)
	return time.Unix(seconds, nanoseconds).UTC()
}

func decodeSessionHeadCursor(encoded string) (sessionHeadCursor, bool, error) {
	if encoded == "" {
		return sessionHeadCursor{}, false, nil
	}
	if len(encoded) > 512 {
		return sessionHeadCursor{}, false, fmt.Errorf("session head cursor exceeds limit")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > 512 {
		return sessionHeadCursor{}, false, fmt.Errorf("invalid session head cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor sessionHeadCursor
	if err := decoder.Decode(&cursor); err != nil {
		return sessionHeadCursor{}, false, fmt.Errorf("invalid session head cursor")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return sessionHeadCursor{}, false, fmt.Errorf("invalid session head cursor")
	}
	if cursor.Version != 1 || cursor.Position == 0 || cursor.Position > math.MaxInt64 {
		return sessionHeadCursor{}, false, fmt.Errorf("invalid session head cursor")
	}
	if _, err := domain.ParseSessionID(cursor.Session); err != nil {
		return sessionHeadCursor{}, false, fmt.Errorf("invalid session head cursor")
	}
	return cursor, true, nil
}

func encodeSessionHeadCursor(cursor sessionHeadCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil || len(raw) > 512 {
		return "", fmt.Errorf("invalid session head cursor")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
