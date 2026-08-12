package domain

import "time"

type SessionStatus string

const (
	SessionStatusActive SessionStatus = "active"
	SessionStatusClosed SessionStatus = "closed"
)

type TurnStatus string

const (
	TurnStatusRunning     TurnStatus = "running"
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusInterrupted TurnStatus = "interrupted"
)

type ItemKind string

const ItemKindAssistantMessage ItemKind = "assistant_message"

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
	Text string
}

func (AssistantMessagePayload) ItemKind() ItemKind { return ItemKindAssistantMessage }

func (payload AssistantMessagePayload) cloneItemPayload() ItemPayload { return payload }

type ItemTerminal struct {
	Code    string
	Message string
}

type Item struct {
	ID        ItemID
	TurnID    TurnID
	Kind      ItemKind
	Status    ItemStatus
	Payload   ItemPayload
	StartedAt time.Time
	EndedAt   time.Time
	Terminal  *ItemTerminal
}

func (item Item) Clone() Item {
	clone := item
	if item.Payload != nil {
		clone.Payload = item.Payload.cloneItemPayload()
	}
	if item.Terminal != nil {
		terminal := *item.Terminal
		clone.Terminal = &terminal
	}
	return clone
}

type Turn struct {
	ID           TurnID
	Status       TurnStatus
	Input        string
	StartedAt    time.Time
	EndedAt      time.Time
	FailureCode  string
	FailureText  string
	InterruptWhy string
	ActiveItemID ItemID
	ItemOrder    []ItemID
	Items        map[ItemID]Item
}

func (turn Turn) Clone() Turn {
	clone := turn
	if turn.ItemOrder != nil {
		clone.ItemOrder = make([]ItemID, len(turn.ItemOrder))
		copy(clone.ItemOrder, turn.ItemOrder)
	}
	if turn.Items != nil {
		clone.Items = make(map[ItemID]Item, len(turn.Items))
		for id, item := range turn.Items {
			clone.Items[id] = item.Clone()
		}
	}
	return clone
}

type Session struct {
	ID            SessionID
	Status        SessionStatus
	Version       uint64
	WorkspaceRoot string
	ActiveTurnID  TurnID
	TurnOrder     []TurnID
	Turns         map[TurnID]Turn
}

func (s Session) Exists() bool { return s.ID != "" }

func (s Session) Clone() Session {
	clone := s
	if s.TurnOrder != nil {
		clone.TurnOrder = make([]TurnID, len(s.TurnOrder))
		copy(clone.TurnOrder, s.TurnOrder)
	}
	if s.Turns != nil {
		clone.Turns = make(map[TurnID]Turn, len(s.Turns))
		for id, turn := range s.Turns {
			clone.Turns[id] = turn.Clone()
		}
	}
	return clone
}

func (s Session) isPristine() bool {
	return s.ID == "" &&
		s.Status == "" &&
		s.Version == 0 &&
		s.WorkspaceRoot == "" &&
		s.ActiveTurnID == "" &&
		s.TurnOrder == nil &&
		s.Turns == nil
}
