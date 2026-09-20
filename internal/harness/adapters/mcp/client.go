package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

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

	// ErrTeardownUnproven preserves cleanup uncertainty across the execution
	// port. Composition must abandon ownership rather than release its lease.
	ErrTeardownUnproven = errors.New("mcp: server teardown could not be proven")
)

// Server is one connected MCP server: its configuration, its confined
// subprocess, and the SDK session speaking to it.
//
// A Server is created by Connect and released by Close. It is not safe for
// concurrent use by multiple goroutines beyond what the SDK's own session
// guarantees; composition holds one per configured server for the harness's
// lifetime.
type Server struct {
	config    ServerConfig
	command   Command
	session   *sdk.ClientSession
	closeOnce sync.Once
	closeErr  error

	// rawNames maps a qualified Catalog name back to the name the server
	// knows. Discovery owns the qualification, so only discovery can supply
	// the inverse, and a call for a name this server never offered is
	// refused rather than guessed at.
	rawNames map[string]string
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
// Every acquired command is closed on failure, including a failed handshake.
// Unproven cleanup is returned explicitly; it is not a promise that hostile
// processes can always be reaped. Factories own partial construction on error.
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
	if command == nil {
		return nil, fmt.Errorf("%w: factory returned no command", ErrConnect)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, closeCommand(command))
		}
	}()
	if err = command.Start(ctx); err != nil {
		return nil, fmt.Errorf("%w: server %q: %w", ErrConnect, config.Name, err)
	}

	client := sdk.NewClient(&sdk.Implementation{Name: clientName, Version: clientVersion}, nil)
	// Only the writer closes the shared channel. That invokes the execution
	// owner's EOF/teardown path; closing stdout first would skip graceful EOF.
	transport := &sdk.IOTransport{Reader: io.NopCloser(command), Writer: command}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: server %q: %w", ErrConnect, config.Name, err)
	}

	return &Server{config: config, command: command, session: session}, nil
}

// Name returns the configured server name.
func (s *Server) Name() string { return s.config.Name }

// Session exposes the connected SDK session for discovery and invocation.
func (s *Server) Session() *sdk.ClientSession { return s.session }

// Close ends the protocol session, then checks the execution owner's cached
// cleanup result. The SDK may already have closed the channel on a read error;
// that must not hide a failed teardown. Repeated Close preserves the result.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		var sessionErr error
		if s.session != nil {
			sessionErr = s.session.Close()
		}
		s.closeErr = errors.Join(sessionErr, closeCommand(s.command))
	})
	return s.closeErr
}

func closeCommand(command Command) error {
	if command == nil {
		return nil
	}
	if err := command.Close(); err != nil {
		return errors.Join(ErrTeardownUnproven, err)
	}
	return nil
}
