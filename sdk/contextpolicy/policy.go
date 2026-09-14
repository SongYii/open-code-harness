// Package contextpolicy is the experimental, standard-library-only context
// planning contract. Source compatibility is not yet promised; pin a tested
// revision. Policies are trusted, startup-composed Go code, not a sandbox.
// Experimental API status does not relax durable-event integrity or replay
// compatibility requirements.
package contextpolicy

import (
	"context"
	"encoding/json"
)

const DefaultID = "builtin"

// Identity attributes a decision, not checkpoint compatibility. ConfigDigest
// is a host-computed SHA-256 of canonical JSON; configurations must not contain
// credentials or other secrets.
type Identity struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	ConfigDigest string `json:"configDigest"`
}

type Budget struct {
	HardInput, Trigger, Target, ProtectedTail, SummaryOutputCap uint64
}

// Candidate is a core-approved contiguous prefix ending before a whole turn.
// It never splits tool pairs, covers the active turn, or crosses the core's
// protected retention floor. Candidate IDs are opaque and valid for one call.
type Candidate struct {
	ID                                         uint64
	CoveredUnits, RetainedUnits, RetainedTurns uint64
	EstimatedRetainedTokens                    uint64
}

// Input is detached metadata: no message text, event pointers, store, provider,
// or mutation capability crosses this boundary. Trigger is pre_turn, mid_turn,
// manual or overflow_retry. Force also covers the provider-usage anchor.
type Input struct {
	Trigger                 string
	Force                   bool
	Budget                  Budget
	EstimatedTokens         uint64
	PreviousThroughSequence uint64
	Candidates              []Candidate
}

// Decision either preserves all input or selects one offered candidate.
// Forced requests cannot be vetoed when at least one safe candidate exists.
type Decision struct {
	Compact     bool
	CandidateID uint64
}

// Policy must be deterministic, concurrency-safe, cooperative with context
// cancellation, and perform no I/O or background work. The host cannot kill
// arbitrary in-process Go code. Return an error rather than guessing a cut.
type Policy interface {
	Plan(context.Context, Input) (Decision, error)
}

// Registration is local to one launcher invocation. Factory validates its
// nonsecret JSON configuration before the host opens durable resources.
type Registration struct {
	ID      string
	Version string
	Factory func(json.RawMessage) (Policy, error)
}
