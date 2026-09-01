package engine

import (
	"context"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ModelRequestPurpose distinguishes a normal conversation attempt from a
// Context Engine summarization attempt (design §6.3). It is attribution
// only: an adapter may use it for a non-secret header, never to change
// model-visible request semantics (messages, tools, or any JSON body
// field).
type ModelRequestPurpose string

const (
	ModelRequestPurposeConversation ModelRequestPurpose = "conversation"
	ModelRequestPurposeCompaction   ModelRequestPurpose = "compaction"
)

// ModelRequest is one provider-neutral request for a single assistant item.
// Empty Messages or Tools means Input-only; the runner does not consult a profile.
type ModelRequest struct {
	SessionID domain.SessionID
	TurnID    domain.TurnID
	ItemID    domain.ItemID
	Input     string
	Messages  []domain.ModelPromptMessage
	Tools     []domain.ToolSchema
	// Purpose is attribution only (see ModelRequestPurpose); the zero
	// value behaves exactly like ModelRequestPurposeConversation, so
	// every caller that predates this field is unaffected.
	Purpose ModelRequestPurpose
	// MaxOutputTokens, when positive, overrides the route's statically
	// configured maximum output for this one request; it must not exceed
	// that route maximum. Zero means "use the route's own configured
	// value," preserving every existing caller's current behavior.
	MaxOutputTokens uint32
}

type StreamEventType string

const (
	StreamEventTextDelta StreamEventType = "text_delta"
	StreamEventToolCall  StreamEventType = "tool_call"
	StreamEventCompleted StreamEventType = "completed"
)

// ToolCall is one assembled model tool invocation. Uniqueness is on ID, not Name.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

const (
	maxToolCallIDBytes        = 128
	maxToolCallArgumentsBytes = 32 * 1024
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

// StreamEvent is a provider-neutral model event. Grammar is
// text_delta* tool_call* completed. The completed event has empty Text
// and a nil ToolCall; RunResult may still carry both concatenated text
// and ToolCalls.
type StreamEvent struct {
	Type     StreamEventType
	Text     string
	Usage    *TokenUsage // nil except optionally on completed
	ToolCall *ToolCall   // non-nil iff Type == tool_call
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
