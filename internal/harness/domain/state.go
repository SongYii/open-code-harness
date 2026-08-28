package domain

import "time"

type SessionStatus string

const (
	SessionStatusActive  SessionStatus = "active"
	SessionStatusClosed  SessionStatus = "closed"
	SessionStatusDeleted SessionStatus = "deleted"
)

type TurnStatus string

const (
	TurnStatusRunning     TurnStatus = "running"
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusInterrupted TurnStatus = "interrupted"
)

type ItemKind string

const (
	ItemKindAssistantMessage ItemKind = "assistant_message"
	ItemKindToolCall         ItemKind = "tool_call"
)

func validItemKind(kind ItemKind) bool {
	return kind == ItemKindAssistantMessage || kind == ItemKindToolCall
}

type ItemStatus string

const (
	ItemStatusRunning     ItemStatus = "running"
	ItemStatusCompleted   ItemStatus = "completed"
	ItemStatusFailed      ItemStatus = "failed"
	ItemStatusInterrupted ItemStatus = "interrupted"
)

type ItemPayload interface {
	ItemKind() ItemKind
	cloneItemPayload() ItemPayload
}

type AssistantMessagePayload struct {
	Text      string
	ToolCalls []ToolCallOffer
}

func (AssistantMessagePayload) ItemKind() ItemKind { return ItemKindAssistantMessage }

func (payload AssistantMessagePayload) cloneItemPayload() ItemPayload {
	cloned := payload
	cloned.ToolCalls = cloneToolCallOffers(payload.ToolCalls)
	return cloned
}

type ToolCallPayload struct {
	CallID    string
	Name      string
	Arguments string
	Content   string
	Truncated bool
}

func (ToolCallPayload) ItemKind() ItemKind { return ItemKindToolCall }

func (payload ToolCallPayload) cloneItemPayload() ItemPayload { return payload }

type ItemTerminal struct {
	Code    string
	Message string
}

// Item retains only the information needed while an item is active.
type Item struct {
	ID        ItemID
	TurnID    TurnID
	Kind      ItemKind
	StartedAt time.Time
}

// Turn retains only the information needed while a turn is active.
type Turn struct {
	ID               TurnID
	Input            string
	StartedAt        time.Time
	LastTransitionAt time.Time
	ActiveItem       *Item
}

// Session discards completed turns and items. Persistent identity
// uniqueness for discarded records is enforced by the Store identity index.
type Session struct {
	ID            SessionID
	Status        SessionStatus
	Version       uint64
	WorkspaceRoot string
	ActiveTurn    *Turn
}

func (state Session) Exists() bool { return state.ID != "" }

func (state Session) Clone() Session {
	clone := state
	if state.ActiveTurn != nil {
		turn := *state.ActiveTurn
		clone.ActiveTurn = &turn
		if state.ActiveTurn.ActiveItem != nil {
			item := *state.ActiveTurn.ActiveItem
			clone.ActiveTurn.ActiveItem = &item
		}
	}
	return clone
}

func (state Session) isPristine() bool {
	return state.ID == "" && state.Status == "" && state.Version == 0 && state.WorkspaceRoot == "" && state.ActiveTurn == nil
}
