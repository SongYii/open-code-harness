package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// processCrashCode is the stable recovery terminal reason.
const processCrashCode = "process_crash"

const noItemSentinel = "no_item"

// recoveryAppendID derives the deterministic recovery AppendID in a fixed
// namespace from Session, Turn, Item (or the no-item sentinel), and the
// process_crash code. A lost recovery acknowledgement retries the exact
// same append and resolves to the original receipt.
func recoveryAppendID(session domain.SessionID, turn domain.TurnID, item string) domain.AppendID {
	sum := sha256.Sum256([]byte("open-code-harness/recovery/v1|" + string(session) + "|" + string(turn) + "|" + item + "|" + processCrashCode))
	return domain.AppendID("rcv_" + hex.EncodeToString(sum[:16]))
}

func recoveryEventID(appendID domain.AppendID, index int) domain.EventID {
	return domain.EventID(fmt.Sprintf("%s_e%d", appendID, index))
}

// reconciler reads canonical streams through the Store port and appends
// recovery terminal facts. It owns no domain rules.
type reconciler struct {
	store     application.EventStore
	authority application.WriterAuthority
	now       func() time.Time
}

// reconcileSession replays one session stream and appends the recovery
// terminal facts when the replay ends with a running execution. Duplicate
// reconciliation is idempotent through exact-retry semantics.
func (r *reconciler) reconcileSession(ctx context.Context, session domain.SessionID) (bool, error) {
	records, head, err := r.readAll(ctx, session)
	if err != nil {
		return false, err
	}
	if len(records) == 0 {
		return false, nil
	}
	state, err := domain.Replay(records)
	if err != nil {
		return false, fmt.Errorf("session %s fails replay: %w", session, err)
	}
	turn := state.ActiveTurn
	if turn == nil {
		return false, nil
	}
	item := turn.ActiveItem
	if item == nil {
		// Legacy shape: a running turn with no active item closes the turn
		// only, with the explicit no-item sentinel in the ID namespace.
		return r.appendRecovery(ctx, session, records, turn.ID, noItemSentinel, head, nil)
	}
	if item.TurnID != turn.ID {
		return false, fmt.Errorf("session %s active item %s references turn %s", session, item.ID, item.TurnID)
	}
	interrupted := domain.AssistantMessageInterrupted{TurnID: turn.ID, ItemID: item.ID, Code: processCrashCode, Message: ""}
	return r.appendRecovery(ctx, session, records, turn.ID, string(item.ID), head, &interrupted)
}

func (r *reconciler) appendRecovery(ctx context.Context, session domain.SessionID, records []domain.RecordedEvent, turn domain.TurnID, item string, head uint64, itemInterrupted *domain.AssistantMessageInterrupted) (bool, error) {
	// The original CommandID of the turn remains the correlation lineage.
	var lineage domain.CommandID
	for i := len(records) - 1; i >= 0; i-- {
		if started, ok := records[i].Event.(domain.TurnStarted); ok && started.TurnID == turn {
			lineage = records[i].CommandID
			break
		}
	}
	if lineage == "" {
		return false, fmt.Errorf("session %s running turn %s has no start event", session, turn)
	}

	appendID := recoveryAppendID(session, turn, item)
	occurredAt := r.now().UTC()
	var events []application.ProposedEvent
	add := func(event domain.Event) {
		events = append(events, application.ProposedEvent{
			ID:            recoveryEventID(appendID, len(events)),
			SchemaVersion: 1,
			OccurredAt:    occurredAt,
			Event:         event,
		})
	}
	if itemInterrupted != nil {
		add(*itemInterrupted)
	}
	add(domain.TurnInterrupted{TurnID: turn, Reason: processCrashCode})

	receipt, err := r.store.Append(ctx, application.AppendRequest{
		AppendID:        appendID,
		SessionID:       session,
		ExpectedVersion: head,
		CommandID:       lineage,
		Authority:       r.authority,
		Events:          events,
	})
	if err != nil {
		return false, err
	}
	_ = receipt
	return true, nil
}

// readAll pages through one session stream at a pinned head.
func (r *reconciler) readAll(ctx context.Context, session domain.SessionID) ([]domain.RecordedEvent, uint64, error) {
	var records []domain.RecordedEvent
	var head *uint64
	after := uint64(0)
	for {
		page, err := r.store.ReadStream(ctx, application.ReadStreamRequest{SessionID: session, Limit: 256, AfterSequence: after, HeadVersion: head})
		if err != nil {
			return nil, 0, err
		}
		if head == nil {
			pinned := page.HeadVersion
			head = &pinned
		}
		records = append(records, page.Records...)
		after = page.NextAfterSequence
		if page.End {
			return records, *head, nil
		}
	}
}
