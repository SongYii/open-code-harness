package sqlite

import (
	"context"
	"database/sql"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// sessionHeadState is the derived projection state for one session.
type sessionHeadState struct {
	status string
	turn   sql.NullString
	item   sql.NullString
}

// applyHeadTransition advances the projection state over one event. It is
// the single derivation shared by the synchronous append projection and the
// offline rebuild, so both always agree.
func applyHeadTransition(head sessionHeadState, event domain.Event) sessionHeadState {
	switch typed := event.(type) {
	case domain.TurnStarted:
		head.status = "active"
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
	case domain.AssistantMessageStarted:
		head.item = nullString(string(typed.ItemID))
	case domain.ToolCallStarted:
		head.item = nullString(string(typed.ItemID))
	case domain.AssistantMessageCompleted, domain.AssistantMessageFailed, domain.AssistantMessageInterrupted,
		domain.ToolCallCompleted, domain.ToolCallFailed, domain.ToolCallInterrupted:
		head.item = sql.NullString{}
	}
	return head
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

// RebuildAndVerifySessionHeads reconstructs the session_heads projection from
// the canonical streams and reports any disagreement with the maintained
// projection as corruption. It never writes.
func (store *Store) RebuildAndVerifySessionHeads(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return newStoreError(application.StoreCodeUnavailable, "", err)
	}

	rows, err := store.db.QueryContext(ctx, "SELECT session_id FROM event_streams ORDER BY session_id")
	if err != nil {
		return mapStorageError(err, "")
	}
	var sessions []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return mapStorageError(err, "")
		}
		sessions = append(sessions, sessionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return mapStorageError(err, "")
	}
	rows.Close()

	for _, sessionID := range sessions {
		expected := sessionHeadState{status: "idle"}
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
			expected = applyHeadTransition(expected, record.Event)
		}
		if err := eventRows.Err(); err != nil {
			eventRows.Close()
			return mapStorageError(err, "")
		}
		eventRows.Close()

		var stored sessionHeadState
		err = store.db.QueryRowContext(ctx,
			"SELECT status, active_turn_id, active_item_id FROM session_heads WHERE session_id = ?",
			sessionID).Scan(&stored.status, &stored.turn, &stored.item)
		if err != nil {
			if isNoRows(err) {
				return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
					wrapDetail("session_heads row missing for existing stream", nil))
			}
			return mapStorageError(err, domain.SessionID(sessionID))
		}
		if stored.status != expected.status || stored.turn != expected.turn || stored.item != expected.item {
			return newStoreError(application.StoreCodeCorrupt, domain.SessionID(sessionID),
				wrapDetail("session_heads disagrees with canonical stream replay", nil))
		}
	}
	return nil
}
