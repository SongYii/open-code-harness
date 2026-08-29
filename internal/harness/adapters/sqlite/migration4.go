package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func migrateSessionHeadsV4(ctx context.Context, conn *sql.Conn) error {
	var orphanCount int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM session_heads AS h LEFT JOIN event_streams AS s ON s.session_id = h.session_id WHERE s.session_id IS NULL").Scan(&orphanCount); err != nil {
		return err
	}
	if orphanCount != 0 {
		return &CorruptError{Detail: "orphan legacy session_heads row"}
	}

	type streamRow struct {
		sessionID string
		position  uint64
	}
	rows, err := conn.QueryContext(ctx,
		"SELECT session_id, last_append_commit_position FROM event_streams ORDER BY session_id")
	if err != nil {
		return err
	}
	var streams []streamRow
	for rows.Next() {
		var stream streamRow
		if err := rows.Scan(&stream.sessionID, &stream.position); err != nil {
			rows.Close()
			return err
		}
		streams = append(streams, stream)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, stream := range streams {
		records, err := loadMigrationStream(ctx, conn, stream.sessionID)
		if err != nil {
			return err
		}
		expected, err := projectSessionHead(records, stream.position)
		if err != nil {
			return &CorruptError{Detail: fmt.Sprintf("session %q cannot rebuild head: %v", stream.sessionID, err)}
		}

		var legacy sessionHeadState
		err = conn.QueryRowContext(ctx,
			"SELECT status, active_turn_id, active_item_id, updated_at_commit_position FROM session_heads WHERE session_id = ?",
			stream.sessionID).Scan(&legacy.status, &legacy.turn, &legacy.item, &legacy.position)
		switch {
		case err == nil:
			statusMatches := false
			switch expected.status {
			case "running":
				statusMatches = legacy.status == "active"
			case "idle", "closed":
				statusMatches = legacy.status == expected.status
			}
			if !statusMatches || legacy.turn != expected.turn || legacy.item != expected.item || legacy.position != expected.position {
				return &CorruptError{Detail: fmt.Sprintf("legacy session_heads row for %q disagrees with canonical replay", stream.sessionID)}
			}
		case isNoRows(err):
			// session_heads is derived state. A missing legacy row is rebuilt.
		default:
			return err
		}

		if _, err := conn.ExecContext(ctx,
			"INSERT INTO session_heads_v4 (session_id, workspace_root, status, active_turn_id, active_item_id, updated_at_commit_position) VALUES (?, ?, ?, ?, ?, ?)",
			stream.sessionID, expected.workspaceRoot, expected.status, expected.turn, expected.item, expected.position); err != nil {
			return err
		}
	}

	if _, err := conn.ExecContext(ctx, "DROP TABLE session_heads"); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "ALTER TABLE session_heads_v4 RENAME TO session_heads"); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx,
		"CREATE INDEX session_heads_visible_by_workspace ON session_heads (workspace_root, updated_at_commit_position DESC, session_id DESC) WHERE status <> 'deleted'"); err != nil {
		return err
	}
	return nil
}

func loadMigrationStream(ctx context.Context, conn *sql.Conn, sessionID string) ([]domain.RecordedEvent, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT payload FROM events WHERE session_id = ? ORDER BY sequence", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []domain.RecordedEvent
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		record, err := domain.UnmarshalRecordedEvent(payload)
		if err != nil {
			return nil, &CorruptError{Detail: fmt.Sprintf("unreadable canonical event in session %q: %v", sessionID, err)}
		}
		if string(record.SessionID) != sessionID {
			return nil, &CorruptError{Detail: fmt.Sprintf("payload session %q does not match stream %q", record.SessionID, sessionID)}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
