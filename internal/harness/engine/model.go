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

// StreamEvent is a provider-neutral model event. Streams emit zero or more
// non-empty UTF-8 text deltas followed by one completed event.
type StreamEvent struct {
	Type StreamEventType
	Text string
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
