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

// runtimeRecoveredCode is the stable ContextCompactionFailed code design
// §14.4 requires for an unmatched ContextCompactionStarted found at
// startup: no summary or reset is ever synthesized during recovery, only a
// closed failure.
const runtimeRecoveredCode = "runtime_recovered"
const runtimeRecoveredMessage = "context compaction did not complete before the runtime restarted"

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

// recoveryCompactionAppendID derives the deterministic recovery AppendID
// for a compaction-only recovery (no active Turn to key off of, e.g. a
// crashed manual or pre-turn compaction): keyed by Session and Compaction
// ID rather than Turn/Item, since at most one compaction is ever active per
// Session at a time.
func recoveryCompactionAppendID(session domain.SessionID, compaction domain.ContextCompactionID) domain.AppendID {
	sum := sha256.Sum256([]byte("open-code-harness/recovery/v1/compaction|" + string(session) + "|" + string(compaction) + "|" + runtimeRecoveredCode))
	return domain.AppendID("rcv_" + hex.EncodeToString(sum[:16]))
}

// reconciler reads canonical streams through the Store port and appends
// recovery terminal facts. It owns no domain rules.
type reconciler struct {
	store application.EventStore
	// authority is read at append time so an expired-takeover token
	// rotation between Launch attempts cannot strand the recovery append
	// behind a stale fencing token.
	authority application.AuthoritySource
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
	compaction := state.ContextCompaction
	if turn == nil && compaction == nil {
		return false, nil
	}
	if turn == nil {
		// A manual or pre-turn compaction crashed with no active Turn to
		// key recovery off of (design §14.4).
		return r.appendCompactionOnlyRecovery(ctx, session, records, compaction, head)
	}
	item := turn.ActiveItem
	if item == nil {
		// Legacy shape: a running turn with no active item closes the turn
		// only, with the explicit no-item sentinel in the ID namespace.
		return r.appendRecovery(ctx, session, records, turn.ID, noItemSentinel, head, nil, compaction)
	}
	if item.TurnID != turn.ID {
		return false, fmt.Errorf("session %s active item %s references turn %s", session, item.ID, item.TurnID)
	}
	interrupted := domain.AssistantMessageInterrupted{TurnID: turn.ID, ItemID: item.ID, Code: processCrashCode, Message: ""}
	return r.appendRecovery(ctx, session, records, turn.ID, string(item.ID), head, &interrupted, compaction)
}

// appendCompactionOnlyRecovery closes a dangling compaction that has no
// enclosing active Turn (a crashed manual or pre-turn compaction).
func (r *reconciler) appendCompactionOnlyRecovery(ctx context.Context, session domain.SessionID, records []domain.RecordedEvent, compaction *domain.ContextCompaction, head uint64) (bool, error) {
	var lineage domain.CommandID
	for i := len(records) - 1; i >= 0; i-- {
		if started, ok := records[i].Event.(domain.ContextCompactionStarted); ok && started.ID == compaction.ID {
			lineage = records[i].CommandID
			break
		}
	}
	if lineage == "" {
		return false, fmt.Errorf("session %s running compaction %s has no start event", session, compaction.ID)
	}

	appendID := recoveryCompactionAppendID(session, compaction.ID)
	occurredAt := latestOccurredAt(records)
	events := []application.ProposedEvent{{
		ID:            recoveryEventID(appendID, 0),
		SchemaVersion: 1,
		OccurredAt:    occurredAt,
		Event:         domain.ContextCompactionFailed{ID: compaction.ID, Code: runtimeRecoveredCode, Message: runtimeRecoveredMessage},
	}}

	receipt, err := r.store.Append(ctx, application.AppendRequest{
		AppendID:        appendID,
		SessionID:       session,
		ExpectedVersion: head,
		CommandID:       lineage,
		Authority:       r.authority.CurrentAuthority(),
		Events:          events,
	})
	if err != nil {
		return false, err
	}
	_ = receipt
	return true, nil
}

// latestOccurredAt derives a byte-stable timestamp from the replayed stream
// alone (the maximum recorded time), so retried recovery appends stay
// identical across restarts instead of racing a lost acknowledgement into
// AppendIdentityMismatch (see appendRecovery's own note on this).
func latestOccurredAt(records []domain.RecordedEvent) time.Time {
	occurredAt := records[0].OccurredAt.UTC()
	for _, record := range records[1:] {
		if record.OccurredAt.After(occurredAt) {
			occurredAt = record.OccurredAt.UTC()
		}
	}
	return occurredAt
}

func (r *reconciler) appendRecovery(ctx context.Context, session domain.SessionID, records []domain.RecordedEvent, turn domain.TurnID, item string, head uint64, itemInterrupted *domain.AssistantMessageInterrupted, compaction *domain.ContextCompaction) (bool, error) {
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
	// The recovery facts must be byte-stable across retries: the digest of
	// an append covers every event's OccurredAt, so a wall-clock timestamp
	// here would turn a lost acknowledgement into AppendIdentityMismatch
	// instead of the exact-retry resolution the deterministic AppendID
	// promises. Derive the stamp from the replayed stream itself — the
	// maximum recorded time is a function of the log alone. It also keeps
	// the replay monotonicity invariant intact (no event may precede the
	// last transition it follows).
	occurredAt := latestOccurredAt(records)
	var events []application.ProposedEvent
	add := func(event domain.Event) {
		events = append(events, application.ProposedEvent{
			ID:            recoveryEventID(appendID, len(events)),
			SchemaVersion: 1,
			OccurredAt:    occurredAt,
			Event:         event,
		})
	}
	if compaction != nil {
		// A mid-turn or overflow-retry compaction, active inside this same
		// Turn, must close before the Turn's own terminal events land
		// (design §14.4's stated ordering, preserving Domain eligibility).
		add(domain.ContextCompactionFailed{ID: compaction.ID, Code: runtimeRecoveredCode, Message: runtimeRecoveredMessage})
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
		Authority:       r.authority.CurrentAuthority(),
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
