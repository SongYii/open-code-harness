package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// sessionHeadState is the derived projection state for one session.
type sessionHeadState struct {
	workspaceRoot string
	status        string
	turn          sql.NullString
	item          sql.NullString
	position      uint64
}

// applyHeadTransition advances the projection state over one event. It is
// the single derivation shared by the synchronous append projection and the
// offline rebuild, so both always agree.
func applyHeadTransition(head sessionHeadState, event domain.Event) (sessionHeadState, error) {
	switch typed := event.(type) {
	case domain.SessionCreated:
		root, err := application.CanonicalWorkspaceRoot(typed.WorkspaceRoot)
		if err != nil || head.workspaceRoot != "" {
			return sessionHeadState{}, fmt.Errorf("invalid session.created workspace root")
		}
		head.workspaceRoot = root
		head.status = "idle"
	case domain.TurnStarted:
		head.status = "running"
		head.turn = nullString(string(typed.TurnID))
		head.item = sql.NullString{}
	case domain.TurnCompleted, domain.TurnFailed, domain.TurnInterrupted:
		head.status = "idle"
		head.turn = sql.NullString{}
		head.item = sql.NullString{}
	case domain.SessionClosed:
		head.status = "closed"
		head.turn = sql.NullString{}
		head.item = sql.NullString{}
	case domain.SessionDeleted:
		head.status = "deleted"
		head.turn = sql.NullString{}
		head.item = sql.NullString{}
	case domain.AssistantMessageStarted:
		head.item = nullString(string(typed.ItemID))
	case domain.ToolCallStarted:
		head.item = nullString(string(typed.ItemID))
	case domain.AssistantMessageCompleted, domain.AssistantMessageFailed, domain.AssistantMessageInterrupted,
		domain.ToolCallCompleted, domain.ToolCallFailed, domain.ToolCallInterrupted:
		head.item = sql.NullString{}
	}
	return head, nil
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func projectSessionHead(records []domain.RecordedEvent, position uint64) (sessionHeadState, error) {
	state, err := domain.Replay(records)
	if err != nil {
		return sessionHeadState{}, err
	}
	root, err := application.CanonicalWorkspaceRoot(state.WorkspaceRoot)
	if err != nil {
		return sessionHeadState{}, err
	}
	head := sessionHeadState{workspaceRoot: root, position: position}
	switch state.Status {
	case domain.SessionStatusActive:
		head.status = "idle"
		if state.ActiveTurn != nil {
			head.status = "running"
			head.turn = nullString(string(state.ActiveTurn.ID))
			if state.ActiveTurn.ActiveItem != nil {
				head.item = nullString(string(state.ActiveTurn.ActiveItem.ID))
			}
		}
	case domain.SessionStatusClosed:
		head.status = "closed"
	case domain.SessionStatusDeleted:
		head.status = "deleted"
	default:
		return sessionHeadState{}, fmt.Errorf("invalid session status %q", state.Status)
	}
	return head, nil
}

// RebuildAndVerifySessionHeads reconstructs the session_heads projection from
// the canonical streams and reports any disagreement with the maintained
// projection as corruption. It never writes.
func (store *Store) RebuildAndVerifySessionHeads(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return newStoreError(application.StoreCodeUnavailable, "", err)
	}

	rows, err := store.db.QueryContext(ctx, "SELECT session_id, last_append_commit_position FROM event_streams ORDER BY session_id")
	if err != nil {
		return mapStorageError(err, "")
	}
	type streamHead struct {
		sessionID string
		position  uint64
	}
	var sessions []streamHead
	for rows.Next() {
		var stream streamHead
		if err := rows.Scan(&stream.sessionID, &stream.position); err != nil {
			rows.Close()
			return mapStorageError(err, "")
		}
		sessions = append(sessions, stream)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return mapStorageError(err, "")
	}
	rows.Close()

	for _, stream := range sessions {
		sessionID := stream.sessionID
		var records []domain.RecordedEvent
		eventRows, err := store.db.QueryContext(ctx,
			"SELECT payload FROM events WHERE session_id = ? ORDER BY sequence", sessionID)
		if err != nil {
			return mapStorageError(err, "")
		}
		for eventRows.Next() {
			var payload []byte
			if err := eventRows.Scan(&payload); err != nil {
				eventRows.Close()
				return mapStorageError(err, "")
			}
			record, err := domain.UnmarshalRecordedEvent(payload)
			if err != nil {
				eventRows.Close()
				return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
					wrapDetail("unreadable canonical event payload during rebuild", err))
			}
			if string(record.SessionID) != sessionID {
				eventRows.Close()
				return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
					wrapDetail("canonical event belongs to another session during rebuild", nil))
			}
			records = append(records, record)
		}
		if err := eventRows.Err(); err != nil {
			eventRows.Close()
			return mapStorageError(err, "")
		}
		eventRows.Close()
		expected, err := projectSessionHead(records, stream.position)
		if err != nil {
			return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
				wrapDetail("invalid canonical stream during rebuild", err))
		}

		var stored sessionHeadState
		err = store.db.QueryRowContext(ctx,
			"SELECT workspace_root, status, active_turn_id, active_item_id, updated_at_commit_position FROM session_heads WHERE session_id = ?",
			sessionID).Scan(&stored.workspaceRoot, &stored.status, &stored.turn, &stored.item, &stored.position)
		if err != nil {
			if isNoRows(err) {
				return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
					wrapDetail("session_heads row missing for existing stream", nil))
			}
			return mapStorageError(err, domain.SessionID(sessionID))
		}
		if stored != expected {
			return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
				wrapDetail("session_heads disagrees with canonical stream replay", nil))
		}
	}
	var orphanCount int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM session_heads AS h LEFT JOIN event_streams AS s ON s.session_id = h.session_id WHERE s.session_id IS NULL").Scan(&orphanCount); err != nil {
		return mapStorageError(err, "")
	}
	if orphanCount != 0 {
		return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("orphan session_heads row", nil))
	}
	return nil
}
