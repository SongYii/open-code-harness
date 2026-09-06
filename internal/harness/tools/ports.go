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
	// Throttled is true when the command's CPU usage was measurably
	// bandwidth-limited during its run (Linux cgroup v2 cpu.max). It is
	// additive, not a kill reason: it can be true alongside TimedOut,
	// alongside ResourceLimited, or alongside neither, since a throttled
	// command may still finish well within its timeout.
	Throttled bool
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

// ExternalToolResult is one call's outcome from a tool this project does not
// implement itself.
type ExternalToolResult struct {
	// Text is the result the model sees.
	Text string
	// IsError reports that the tool itself failed, as distinct from the call
	// failing to reach it. The difference matters: a tool failure is an
	// ordinary event within a Turn that the model can read and react to,
	// while a transport failure ends the Turn. Conflating them would let a
	// routine "file not found" tear down a whole session.
	IsError bool
	// Truncated reports that Text was clipped to this project's own result
	// bound rather than being the tool's complete output.
	Truncated bool
}

// ExternalTools invokes a tool whose ToolSpec came from outside this project
// — today, one discovered from an MCP server.
//
// Arguments are the raw JSON the model produced, already validated against
// the tool's own declared schema by ValidateArgs. They are forwarded verbatim
// because their shape belongs to the external tool, not to this project: no
// fixed-field decode applies, and the workspace-containment question that
// governs the builtin tools does not either, since an external server is not
// a location inside this workspace.
//
// The port lives here, beside FileSystem and CommandRunner, so Application
// can dispatch to it without importing any adapter.
type ExternalTools interface {
	Call(ctx context.Context, name string, arguments string) (ExternalToolResult, error)
}
