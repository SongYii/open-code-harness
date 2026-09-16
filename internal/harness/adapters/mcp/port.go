package mcp

import (
	"context"
	"io"
)

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

// Command is a prepared, owned byte channel to a confined server. The
// implementation owns pipes, OS startup, quota enrollment, the sole wait and
// process-tree teardown. MCP owns only the protocol spoken on that channel.
type Command interface {
	// Start makes one startup attempt. Cancellation after successful startup
	// does not end the resource lifetime. Close is required even on failure.
	Start(context.Context) error
	// Close must unblock I/O, prove teardown, release temporary resources and
	// cache its result. SDK connection closure may call it concurrently with
	// owner cleanup. A nil result means cleanup was proven complete.
	io.ReadWriteCloser
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
