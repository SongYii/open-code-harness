package engine

import (
	"context"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ModelRequest is one provider-neutral request for a single assistant item.
type ModelRequest struct {
	SessionID domain.SessionID
	TurnID    domain.TurnID
	ItemID    domain.ItemID
	Input     string
}

type StreamEventType string

const (
	StreamEventTextDelta StreamEventType = "text_delta"
	StreamEventCompleted StreamEventType = "completed"
)

type TokenUsage struct {
	InputTokens       uint64
	OutputTokens      uint64
	CachedInputTokens uint64
}

type AttemptStats struct {
	Usage             *TokenUsage
	FinishReason      string
	ProviderRequestID string
	LatencyMs         uint64
}

// StreamEvent is a provider-neutral model event. Streams emit zero or more
// non-empty UTF-8 text deltas followed by one completed event.
type StreamEvent struct {
	Type  StreamEventType
	Text  string
	Usage *TokenUsage // nil except optionally on completed
}

// AttemptObserver is optional. HTTP streams report finish, request id, and
// latency here; scripted adapters omit it.
type AttemptObserver interface {
	Snapshot() AttemptStats
}

// Model may receive concurrent Stream calls for independent turns. Each
// returned ModelStream has one consumer, which owns all Next and Close calls.
type Model interface {
	Stream(context.Context, ModelRequest) (ModelStream, error)
}

// ModelStream is synchronously consumed by its single owner. Close must tear
// down adapter-owned work promptly; it is never called concurrently with Next.
type ModelStream interface {
	Next(context.Context) (StreamEvent, error)
	Close() error
}
