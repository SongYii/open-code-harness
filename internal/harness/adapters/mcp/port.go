package mcp

import "os/exec"

// ServerConfig names one MCP server this harness may talk to.
//
// Configuration is static and read once at composition time, the same
// lifecycle `localexec.New(workspaceRoot)` and `workspacefs.New(...)` already
// have. The configured list *is* this project's admission control for which
// MCP servers may exist at all; there is no per-session configuration, and
// the ACP adapter's existing fail-closed rejection of `mcpServers` is
// unchanged.
type ServerConfig struct {
	// Name is the operator-chosen server name. It becomes part of every
	// tool's qualified Catalog name (see QualifyToolName).
	Name string

	// Command is argv[0] for the stdio subprocess, and Args the rest.
	Command string
	Args    []string
}

// Command is one confined, prepared, not-yet-started server subprocess.
//
// The MCP stdio transport needs the process handed over unstarted, because
// its stdin and stdout are the protocol channel: the SDK's own
// CommandTransport attaches the pipes and calls Start itself. That is why
// this port yields an *exec.Cmd rather than running anything.
type Command interface {
	// Cmd returns the prepared, unstarted command. The caller attaches stdio
	// and starts it.
	Cmd() *exec.Cmd

	// StartBracket applies the platform's pre-Start resource bracket and
	// returns its release, to be invoked exactly once after the process has
	// started. On platforms with no such bracket it is a no-op.
	StartBracket() func()

	// Register enrolls a started process in the provider's resource quota.
	Register(pid int) error

	// Close releases the command's temporary resources. It does not stop the
	// process; this package owns that.
	Close() error
}

// CommandFactory builds confined server subprocesses.
//
// This interface is the reason this package never imports
// internal/harness/adapters/localexec. The design requires MCP servers to
// reuse the OS-level confinement localexec already implements, and separately
// forbids an adapter from importing a sibling adapter; those two rules
// contradicted each other until the 2026-09-04 amendment to design §3
// resolved them here. internal/harness/composition — the one package
// permitted to import both — supplies the localexec-backed implementation,
// exactly as it supplies every other adapter.
//
// Keeping the interface here rather than in localexec is deliberate: the
// consumer declares what it needs, so localexec owes nothing to MCP and a
// second, differently-confined provider could be substituted without either
// package learning about the other.
type CommandFactory interface {
	NewCommand(config ServerConfig) (Command, error)
}
