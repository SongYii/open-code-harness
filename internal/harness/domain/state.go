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

type Turn struct {
	ID           TurnID
	Status       TurnStatus
	Input        string
	StartedAt    time.Time
	EndedAt      time.Time
	FailureCode  string
	FailureText  string
	InterruptWhy string
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
			clone.Turns[id] = turn
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
