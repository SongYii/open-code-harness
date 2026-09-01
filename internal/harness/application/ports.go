package application

import (
	"context"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
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
	NewContextCompactionID() (domain.ContextCompactionID, error)
	NewContextDecisionID() (domain.ContextDecisionID, error)
}

// ContextSummarizeRequest is what the Context Engine's compaction bracket
// sends the active conversation Provider to produce one rolling summary
// (design §6.3). Content is the fully rendered prompt — Task 6's
// och_context_summary_v1 asset plus the bounded source units and any
// previous summary, already delimited as untrusted data — so this port
// owns no prompt assembly of its own.
type ContextSummarizeRequest struct {
	SessionID       domain.SessionID
	TurnID          domain.TurnID
	ItemID          domain.ItemID
	Content         string
	MaxOutputTokens uint32
	MaxOutputBytes  int
}

// ContextSummarizeResult is one completed summarization attempt.
type ContextSummarizeResult struct {
	Text  string
	Usage engine.TokenUsage
	// Route is a non-secret active route identity, optional.
	Route string
}

// ContextSummarizer is an Application port implemented by an Engine
// adapter over engine.Model (design §6.3). It sends no Tools, collects
// only text under a byte/token cap, rejects Tool Calls, and uses the same
// closed engine.ProviderFailure taxonomy conversation attempts already
// use. It never enters RunTurn, emits assistant deltas, or recursively
// invokes the Context Engine.
type ContextSummarizer interface {
	Summarize(context.Context, ContextSummarizeRequest) (ContextSummarizeResult, error)
}

// ContextCheckpointLookupStatus is LoadLatestContextCheckpoint's result
// shape: it never fabricates a checkpoint, so "none" and "found" are the
// only non-error outcomes.
type ContextCheckpointLookupStatus string

const (
	ContextCheckpointLookupNone  ContextCheckpointLookupStatus = "none"
	ContextCheckpointLookupFound ContextCheckpointLookupStatus = "found"
)

// ContextCheckpointLookup is LoadLatestContextCheckpoint's result: either
// no checkpoint exists yet for the Session, or the verified latest one.
type ContextCheckpointLookup struct {
	Status     ContextCheckpointLookupStatus
	Checkpoint domain.ContextCheckpointRecord
}

// ContextCheckpointStore is an Application read port over the derived
// latest-checkpoint projection (design §6.4). Writes always go through
// EventStore.Append; this port only ever returns ContextCheckpointLookupNone,
// ContextCheckpointLookupFound, or a classified *StoreError (most commonly
// StoreCodeCorrupt when a stored projection disagrees with its canonical
// event) — it never fabricates a checkpoint.
type ContextCheckpointStore interface {
	LoadLatestContextCheckpoint(context.Context, domain.SessionID) (ContextCheckpointLookup, error)
}
