package composition

import (
	"context"
	"errors"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/localexec"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/mcp"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// confinedCommandFactory adapts localexec's long-lived confined command entry
// point to the port the MCP adapter declares.
//
// This type is why the MCP adapter never imports localexec. The design forbids
// an adapter from importing a sibling adapter while requiring MCP servers to
// reuse localexec's OS-level confinement; composition is the one package
// permitted to import both, so the join happens here and nowhere else.
type confinedCommandFactory struct {
	runner    *localexec.Runner
	workspace string
}

func (factory confinedCommandFactory) NewCommand(config mcp.ServerConfig) (mcp.Command, error) {
	argv := append([]string{config.Command}, config.Args...)
	confined, err := factory.runner.NewConfinedCommand(tools.CommandSpec{
		Argv: argv,
		Cwd:  factory.workspace,
	})
	if err != nil {
		return nil, err
	}
	return confined, nil
}

// mcpServers holds every connected server for the assembly's lifetime.
type mcpServers []*mcp.Server

// close tears down every server, collecting rather than short-circuiting so
// one stubborn server cannot strand the others.
func (servers mcpServers) close() error {
	var errs []error
	for _, server := range servers {
		if err := server.Close(); err != nil {
			errs = append(errs, fmt.Errorf("composition: mcp server %q: %w", server.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// connectMCPServers connects and discovers every configured server, returning
// the specs they contribute and the handles that must be closed.
//
// It fails closed. A configured server that cannot be reached, that breaches a
// resource bound, or that duplicates another's name fails Open outright, and
// every server already connected is torn down before returning. There is no
// escape hatch equivalent to AllowUnsandboxedExec here: an operator who
// configured a server asked for its tools, and starting without them while
// reporting success is the more dangerous outcome — the tools would appear
// absent rather than broken, and nothing would say why.
func connectMCPServers(ctx context.Context, configs []mcp.ServerConfig, factory mcp.CommandFactory) ([]domain.ToolSpec, mcpServers, error) {
	if len(configs) == 0 {
		return nil, nil, nil
	}

	seenNames := make(map[string]bool, len(configs))
	var connected mcpServers
	var specs []domain.ToolSpec

	fail := func(cause error) ([]domain.ToolSpec, mcpServers, error) {
		if closeErr := connected.close(); closeErr != nil {
			return nil, nil, errors.Join(cause, closeErr)
		}
		return nil, nil, cause
	}

	for _, config := range configs {
		// A duplicate name is an operator configuration error, and it is
		// caught here rather than at NewCatalog so the message names the
		// configuration rather than a derived tool name.
		if seenNames[config.Name] {
			return fail(fmt.Errorf("composition: mcp: two servers are configured with the name %q", config.Name))
		}
		seenNames[config.Name] = true

		server, err := mcp.Connect(ctx, config, factory)
		if err != nil {
			return fail(fmt.Errorf("composition: mcp: %w", err))
		}
		connected = append(connected, server)

		discovered, err := server.Discover(ctx)
		if err != nil {
			return fail(fmt.Errorf("composition: mcp: %w", err))
		}
		specs = append(specs, discovered.Specs...)
	}
	return specs, connected, nil
}
