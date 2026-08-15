package application

import (
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

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
	NewApprovalID() (domain.ApprovalID, error)
}
