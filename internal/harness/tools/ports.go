package tools

import (
	"context"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// FileSystem is the workspace I/O port. Adapters re-check prefix on execute.
// Resolve is a scope probe and must not create, truncate, or write.
type FileSystem interface {
	Resolve(ctx context.Context, workspace, requested string) (abs string, err error)
	Read(ctx context.Context, abs string, limit int) (data []byte, truncated bool, err error)
	Write(ctx context.Context, abs string, data []byte) error
	List(ctx context.Context, abs string, depth, limit int) (names []string, truncated bool, err error)
}

type CommandSpec struct {
	Argv     []string
	Cwd      string
	Timeout  time.Duration
	MaxBytes int
}

type CommandResult struct {
	ExitCode  int
	Output    string
	Truncated bool
	TimedOut  bool
	// ResourceLimited is true when the command was killed for exceeding a
	// resource bound (for example a memory quota) rather than a timeout.
	// A run is killed for exactly one reason: this is mutually exclusive
	// with TimedOut.
	ResourceLimited bool
}

type CommandRunner interface {
	Run(ctx context.Context, spec CommandSpec) (CommandResult, error)
}

type Approver interface {
	Decide(ctx context.Context, req ApprovalRequest) (ApprovalAnswer, error)
}

type ApprovalRequest struct {
	SessionID  domain.SessionID
	TurnID     domain.TurnID
	ApprovalID domain.ApprovalID
	Name       string
	CallID     string
	Arguments  string
	Reason     string
}

type ApprovalAnswer struct {
	Granted bool
}

// DenyApprover always denies. Used when composition leaves Approver unset.
type DenyApprover struct{}

func (DenyApprover) Decide(context.Context, ApprovalRequest) (ApprovalAnswer, error) {
	return ApprovalAnswer{Granted: false}, nil
}

var _ Approver = DenyApprover{}
