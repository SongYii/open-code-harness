package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// clientName and clientVersion identify this harness to an MCP server during
// the initialize handshake. They are this project's own identity, not the
// SDK's default, so a server operator can see who connected.
const (
	clientName    = "open-code-harness"
	clientVersion = "v0"
)

var (
	// ErrInvalidServerConfig marks a configuration this package refuses
	// before any process is spawned.
	ErrInvalidServerConfig = errors.New("mcp: invalid server configuration")

	// ErrConnect marks a server that could not be reached or would not
	// complete the initialize handshake.
	ErrConnect = errors.New("mcp: server connect failed")
)

// Server is one connected MCP server: its configuration, its confined
// subprocess, and the SDK session speaking to it.
//
// A Server is created by Connect and released by Close. It is not safe for
// concurrent use by multiple goroutines beyond what the SDK's own session
// guarantees; composition holds one per configured server for the harness's
// lifetime.
type Server struct {
	config  ServerConfig
	command Command
	session *sdk.ClientSession
}

// Validate rejects a configuration this package will not act on. It runs
// before any subprocess exists, so a malformed entry fails at composition
// time rather than at first tool call.
func (config ServerConfig) Validate() error {
	if strings.TrimSpace(config.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidServerConfig)
	}
	if config.Name != strings.TrimSpace(config.Name) {
		return fmt.Errorf("%w: name %q has surrounding whitespace", ErrInvalidServerConfig, config.Name)
	}
	if strings.TrimSpace(config.Command) == "" {
		return fmt.Errorf("%w: command is required for server %q", ErrInvalidServerConfig, config.Name)
	}
	return nil
}

// Connect prepares a confined subprocess for config through factory, then
// runs the SDK's own initialize handshake over its stdio.
//
// The handshake, protocol-version negotiation, and framing all belong to the
// SDK; this project deliberately implements none of them. The 2026-07-28
// specification revision is backward-incompatible with earlier ones and the
// SDK carries four live protocol versions with negotiation between them,
// which is the whole reason the design adopts it instead of hand-rolling a
// wire this project would have to re-verify on every specification move.
//
// Every failure path closes what it opened. A factory error, a failed
// handshake, or a server that exits during initialize all leave no process
// and no temporary directory behind.
func Connect(ctx context.Context, config ServerConfig, factory CommandFactory) (server *Server, err error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if factory == nil {
		return nil, fmt.Errorf("%w: a command factory is required", ErrInvalidServerConfig)
	}

	command, err := factory.NewCommand(config)
	if err != nil {
		return nil, fmt.Errorf("%w: server %q: %w", ErrConnect, config.Name, err)
	}
	defer func() {
		if err != nil {
			_ = command.Close()
		}
	}()

	client := sdk.NewClient(&sdk.Implementation{Name: clientName, Version: clientVersion}, nil)
	transport := &sdk.CommandTransport{Command: command.Cmd()}

	// The SDK's Connect is what actually calls Start, so the platform's
	// pre-Start resource bracket has to wrap this call rather than a Start
	// this package makes itself. See localexec.ConfinedCommand.StartBracket.
	release := command.StartBracket()
	session, err := client.Connect(ctx, transport, nil)
	release()
	if err != nil {
		return nil, fmt.Errorf("%w: server %q: %w", ErrConnect, config.Name, err)
	}

	if process := command.Cmd().Process; process != nil {
		// Best-effort quota enrollment, matching localexec.Run's own
		// treatment: a platform without the controller must not fail a
		// server that is otherwise correctly confined.
		_ = command.Register(process.Pid)
	}

	return &Server{config: config, command: command, session: session}, nil
}

// Name returns the configured server name.
func (s *Server) Name() string { return s.config.Name }

// Session exposes the connected SDK session for discovery and invocation.
func (s *Server) Session() *sdk.ClientSession { return s.session }

// Close ends the session and releases the command's resources.
//
// Closing the session runs the SDK's own stdio shutdown — close stdin, wait,
// SIGTERM, then Kill. That ladder signals the process rather than its group
// and proves no reap, both weaker than this repository's established
// practice; a later task owns escalating past it. Close reports the session's
// own error and still releases the command either way, so a stubborn server
// cannot leak a temporary directory.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	var sessionErr error
	if s.session != nil {
		sessionErr = s.session.Close()
		s.session = nil
	}
	var commandErr error
	if s.command != nil {
		commandErr = s.command.Close()
		s.command = nil
	}
	return errors.Join(sessionErr, commandErr)
}
