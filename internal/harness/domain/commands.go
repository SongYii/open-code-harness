package domain

type Command interface {
	CommandType() string
	TargetSessionID() SessionID
}

const (
	CommandCreateSession = "session.create"
	CommandStartTurn     = "turn.start"
	CommandCompleteTurn  = "turn.complete"
	CommandFailTurn      = "turn.fail"
	CommandInterruptTurn = "turn.interrupt"
	CommandCloseSession  = "session.close"
)

type CreateSession struct {
	SessionID     SessionID
	WorkspaceRoot string
}

func (CreateSession) CommandType() string          { return CommandCreateSession }
func (c CreateSession) TargetSessionID() SessionID { return c.SessionID }

type StartTurn struct {
	SessionID SessionID
	TurnID    TurnID
	Input     string
}

func (StartTurn) CommandType() string          { return CommandStartTurn }
func (c StartTurn) TargetSessionID() SessionID { return c.SessionID }

type CompleteTurn struct {
	SessionID SessionID
	TurnID    TurnID
}

func (CompleteTurn) CommandType() string          { return CommandCompleteTurn }
func (c CompleteTurn) TargetSessionID() SessionID { return c.SessionID }

type FailTurn struct {
	SessionID SessionID
	TurnID    TurnID
	Code      string
	Message   string
}

func (FailTurn) CommandType() string          { return CommandFailTurn }
func (c FailTurn) TargetSessionID() SessionID { return c.SessionID }

type InterruptTurn struct {
	SessionID SessionID
	TurnID    TurnID
	Reason    string
}

func (InterruptTurn) CommandType() string          { return CommandInterruptTurn }
func (c InterruptTurn) TargetSessionID() SessionID { return c.SessionID }

type CloseSession struct {
	SessionID SessionID
}

func (CloseSession) CommandType() string          { return CommandCloseSession }
func (c CloseSession) TargetSessionID() SessionID { return c.SessionID }
