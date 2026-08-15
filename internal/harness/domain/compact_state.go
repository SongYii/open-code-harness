package domain

import "time"

// CompactItem retains only the information needed while an item is active.
type CompactItem struct {
	ID        ItemID
	TurnID    TurnID
	Kind      ItemKind
	StartedAt time.Time
}

// CompactTurn retains only the information needed while a turn is active.
type CompactTurn struct {
	ID               TurnID
	Input            string
	StartedAt        time.Time
	LastTransitionAt time.Time
	ActiveItem       *CompactItem
}

// CompactSession discards completed turns and items. Persistent identity
// uniqueness for discarded records is enforced by the Store identity index.
type CompactSession struct {
	ID            SessionID
	Status        SessionStatus
	Version       uint64
	WorkspaceRoot string
	ActiveTurn    *CompactTurn
}

func (state CompactSession) Exists() bool { return state.ID != "" }

func (state CompactSession) Clone() CompactSession {
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

func (state CompactSession) isPristine() bool {
	return state.ID == "" && state.Status == "" && state.Version == 0 && state.WorkspaceRoot == "" && state.ActiveTurn == nil
}
