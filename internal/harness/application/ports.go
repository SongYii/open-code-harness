package application

import (
	"context"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// EventStore is the authoritative per-Session event stream boundary.
//
// Load returns the complete stream in contiguous sequence order. An absent
// stream is returned as an empty slice and no error; Application use cases,
// rather than the Store, decide whether absence means session_not_found. The
// returned records and their events must be defensive copies.
//
// Append applies an exact compare-and-swap: ExpectedVersion must equal the
// current number of records in the Session's authoritative stream. A request
// is committed atomically and advances that version by len(Events). On success,
// Append returns exactly the newly committed batch as a defensive copy. The
// Store assigns contiguous record sequences and all record metadata. If the
// context is canceled before commit, no record or version change is visible.
// A non-nil error means the requested batch did not commit. Once committed,
// Append returns the exact committed records with a nil error even if caller
// cancellation races after the commit point. An adapter that can lose a
// post-commit acknowledgement cannot honestly implement this interface without
// first adding explicit unknown-commit or exact idempotent-retry semantics.
//
// Store atomicity does not make Clock or IDGenerator transactional. A late
// pre-commit failure can leave generated opaque event IDs unused; committed
// stream sequences must nevertheless remain contiguous. Implementations do not
// retry conflicts, re-decide commands, or expose subscription semantics.
type EventStore interface {
	Load(context.Context, domain.SessionID) ([]domain.RecordedEvent, error)
	Append(context.Context, AppendRequest) ([]domain.RecordedEvent, error)
}

// AppendRequest describes one ordered, atomic append to a Session stream.
type AppendRequest struct {
	SessionID       domain.SessionID
	ExpectedVersion uint64
	CommandID       domain.CommandID
	Events          []domain.Event
}

// Clock supplies time to application-owned adapters.
type Clock interface {
	Now() time.Time
}

// IDGenerator supplies opaque unique identifiers. Allocated identifiers may be
// unused when a later pre-commit operation fails; callers must not assume
// transactional allocation or gap-free values.
type IDGenerator interface {
	NewSessionID() (domain.SessionID, error)
	NewTurnID() (domain.TurnID, error)
	NewItemID() (domain.ItemID, error)
	NewCommandID() (domain.CommandID, error)
	NewAppendID() (domain.AppendID, error)
	NewEventID() (domain.EventID, error)
}
