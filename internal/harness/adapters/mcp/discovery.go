package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

const (
	// MaxToolsPerServer bounds how many tools one server may contribute.
	//
	// A tool listing is externally supplied and a server may paginate
	// forever, so the count needs a stated bound rather than whatever the
	// server decides to send. 256 matches tools.MaxListDirEntries, this
	// project's existing round bound for an externally supplied list.
	MaxToolsPerServer = 256

	// MaxToolDefinitionBytes bounds one tool's combined description and
	// input schema. A server can otherwise attach an arbitrarily large
	// description to a single tool and reach the same exhaustion the count
	// bound closes.
	MaxToolDefinitionBytes = 65536
)

// DiscoveryDiagnostic records one tool this discovery declined to register,
// so a dropped tool is a reported fact rather than a silent absence.
type DiscoveryDiagnostic struct {
	// RawName is the tool name the server offered, before qualification.
	RawName string
	// Reason states why it was not registered.
	Reason string
}

// DiscoveryResult is one server's contribution to the tool catalog.
type DiscoveryResult struct {
	// Specs are the tools that will be registered.
	Specs []domain.ToolSpec
	// Dropped names the tools that were not, and why.
	Dropped []DiscoveryDiagnostic
	// Degraded names the tools whose declared schema this project's own
	// compiler cannot read, so their arguments are checked loosely at call
	// time. Loose checking is an auditable fact, not an invisible one.
	Degraded []string
}

// Discover lists the connected server's tools and projects them into this
// project's own domain.ToolSpec type.
//
// Two failure modes are deliberately different. A breach of a resource bound
// fails the **whole server**: a listing over MaxToolsPerServer, or a single
// definition over MaxToolDefinitionBytes, means the server is not behaving
// within the limits this harness is willing to accept, and admitting a prefix
// of its tools would be arbitrary. A single unusable tool — one whose schema
// is not even a JSON object — drops **that tool only**, because letting it
// through would fail tools.NewCatalog and prevent the harness from starting
// at all, which hands one malformed tool a denial of service over everything.
func (s *Server) Discover(ctx context.Context) (DiscoveryResult, error) {
	if s == nil || s.session == nil {
		return DiscoveryResult{}, fmt.Errorf("%w: server is not connected", ErrInvalidServerConfig)
	}

	// A server that never advertised tool support has none to list; asking
	// anyway would be a protocol error rather than an empty answer.
	if capabilities := s.session.InitializeResult().Capabilities; capabilities.Tools == nil {
		return DiscoveryResult{}, nil
	}

	var result DiscoveryResult
	seen := 0
	// The SDK owns cursor pagination; the bound below is what stops a server
	// that paginates without end.
	for tool, err := range s.session.Tools(ctx, nil) {
		if err != nil {
			return DiscoveryResult{}, fmt.Errorf("%w: server %q: listing tools: %w", ErrConnect, s.config.Name, err)
		}
		seen++
		if seen > MaxToolsPerServer {
			return DiscoveryResult{}, fmt.Errorf(
				"%w: server %q offered more than %d tools", ErrDiscoveryBound, s.config.Name, MaxToolsPerServer)
		}
		spec, diagnostic, degraded, err := s.projectTool(tool)
		if err != nil {
			return DiscoveryResult{}, err
		}
		if diagnostic != nil {
			result.Dropped = append(result.Dropped, *diagnostic)
			continue
		}
		if degraded {
			result.Degraded = append(result.Degraded, spec.Name)
		}
		result.Specs = append(result.Specs, spec)
	}
	return result, nil
}

// ErrDiscoveryBound marks a server that exceeded a declared resource bound.
// It is separate from ErrConnect because the server is reachable and correct
// on the wire; it is simply outside the limits this harness accepts.
var ErrDiscoveryBound = fmt.Errorf("mcp: discovery bound exceeded")

// projectTool converts one server-declared tool into a domain.ToolSpec.
//
// Every classification here is fixed rather than derived from what the server
// says about itself. The MCP specification states that a tool's own
// readOnlyHint/destructiveHint annotations "MUST be treated as untrusted
// unless it comes from a trusted server", and this project cannot establish
// server trust in the general case, so every MCP tool is classified as
// conservatively as the builtin exec tool: RiskExec and mutating, which the
// existing Policy table already denies under the restrictive modes and gates
// behind approval under the permissive ones.
func (s *Server) projectTool(tool *sdk.Tool) (domain.ToolSpec, *DiscoveryDiagnostic, bool, error) {
	if tool == nil || tool.Name == "" {
		return domain.ToolSpec{}, &DiscoveryDiagnostic{
			RawName: "", Reason: "the server offered a tool with no name",
		}, false, nil
	}

	schema, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return domain.ToolSpec{}, &DiscoveryDiagnostic{
			RawName: tool.Name, Reason: "the tool's input schema is not encodable as JSON",
		}, false, nil
	}

	if size := len(tool.Description) + len(schema); size > MaxToolDefinitionBytes {
		return domain.ToolSpec{}, nil, false, fmt.Errorf(
			"%w: server %q tool %q definition is %d bytes, over the %d-byte bound",
			ErrDiscoveryBound, s.config.Name, tool.Name, size, MaxToolDefinitionBytes)
	}

	spec := domain.ToolSpec{
		Name:        QualifyToolName(s.config.Name, tool.Name),
		Description: tool.Description,
		InputSchema: schema,
		Source:      tools.SourceMCP,
		Risk:        domain.RiskExec,
		Mutates:     true,
	}

	// Registration validation is exactly the one tools.NewCatalog applies,
	// asked here through the same public constructor rather than a
	// reimplementation of it. Checking now means one unusable tool is
	// dropped with a stated reason, instead of failing the catalog and with
	// it the whole harness — which would hand a single malformed tool a
	// denial of service over everything.
	if _, err := tools.NewCatalog([]domain.ToolSpec{spec}); err != nil {
		return domain.ToolSpec{}, &DiscoveryDiagnostic{
			RawName: tool.Name,
			Reason:  fmt.Sprintf("the tool cannot be registered: %v", err),
		}, false, nil
	}

	// Whether this project's own compiler can read the schema decides only
	// how strictly arguments are checked at call time (see the Tool runtime
	// contract's source-aware validation); it never decides whether the tool
	// exists.
	return spec, nil, !tools.SchemaIsStrictlyValidatable(schema), nil
}
