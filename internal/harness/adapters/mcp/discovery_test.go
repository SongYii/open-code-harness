//go:build unix

package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func discoverFrom(t *testing.T, mode string) (DiscoveryResult, error) {
	t.Helper()
	factory := fixtureFactory(t, "OCH_FIXTURE_TOOLS="+mode)
	server, err := Connect(t.Context(), ServerConfig{Name: "srv", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server.Discover(t.Context())
}

// TestDiscoverProjectsRealisticToolsIntoTheCatalogsOwnType is the ordinary
// path: a server publishing a schema in full JSON Schema, which this
// project's own compiler cannot read, still contributes a usable tool.
func TestDiscoverProjectsRealisticToolsIntoTheCatalogsOwnType(t *testing.T) {
	result, err := discoverFrom(t, "realistic")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Specs) != 2 {
		t.Fatalf("discovered %d tools, want 2: %+v (dropped=%+v)", len(result.Specs), result.Specs, result.Dropped)
	}
	if len(result.Dropped) != 0 {
		t.Fatalf("dropped %+v; a realistic schema must not cost a tool", result.Dropped)
	}

	// Every discovered spec must register in the real catalog alongside the
	// builtins, which is the whole point of projecting into this type.
	all := append(tools.DefaultWorkspaceSpecs(), result.Specs...)
	if _, err := tools.NewCatalog(all); err != nil {
		t.Fatalf("discovered specs do not coexist with the builtins: %v", err)
	}
}

// TestDiscoveredToolsAreAlwaysExecRiskAndMutating pins the classification the
// specification requires. A server's own readOnlyHint must never soften it.
func TestDiscoveredToolsAreAlwaysExecRiskAndMutating(t *testing.T) {
	result, err := discoverFrom(t, "realistic")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, spec := range result.Specs {
		if spec.Risk != domain.RiskExec {
			t.Errorf("%s has risk %q, want %q — a server's own hints are untrusted", spec.Name, spec.Risk, domain.RiskExec)
		}
		if !spec.Mutates {
			t.Errorf("%s is not marked mutating", spec.Name)
		}
		if spec.Source != tools.SourceMCP {
			t.Errorf("%s has source %q, want %q", spec.Name, spec.Source, tools.SourceMCP)
		}
		if !strings.HasPrefix(spec.Name, "mcp_srv_") {
			t.Errorf("%s is not qualified with its server", spec.Name)
		}
	}
}

// TestDiscoverRecordsWhichToolsWillBeLooselyChecked holds the commitment the
// schema decision made: degradation is auditable, not invisible.
func TestDiscoverRecordsWhichToolsWillBeLooselyChecked(t *testing.T) {
	result, err := discoverFrom(t, "realistic")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Degraded) != 1 {
		t.Fatalf("Degraded = %v, want exactly the one tool whose schema this project cannot read", result.Degraded)
	}
	if !strings.Contains(result.Degraded[0], "search") {
		t.Fatalf("Degraded = %v, want the realistic-schema tool", result.Degraded)
	}
	// And the strict one really is checked strictly, end to end.
	for _, spec := range result.Specs {
		if strings.Contains(spec.Name, "strict") {
			if err := tools.ValidateArgs(spec, `{"path":"ok","invented":1}`); err == nil {
				t.Fatal("a model-invented key passed a tool whose schema does compile")
			}
		}
	}
}

// TestDiscoverDropsOneUnusableToolWithoutLosingTheServer is the denial of
// service this ordering exists to prevent: a schema that is not a JSON object
// would fail NewCatalog, and failing the catalog fails composition.Open.
func TestDiscoverDropsOneUnusableToolWithoutLosingTheServer(t *testing.T) {
	result, err := discoverFrom(t, "unregisterable")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Specs) != 1 {
		t.Fatalf("kept %d tools, want the one usable tool: %+v", len(result.Specs), result.Specs)
	}
	if len(result.Dropped) != 1 {
		t.Fatalf("Dropped = %+v, want exactly the unusable tool", result.Dropped)
	}
	if result.Dropped[0].RawName != "bad" {
		t.Fatalf("dropped %q, want %q", result.Dropped[0].RawName, "bad")
	}
	if result.Dropped[0].Reason == "" {
		t.Fatal("a dropped tool must carry a stated reason")
	}
}

// TestDiscoverRejectsAServerExceedingTheToolBound: a listing over the bound
// fails the whole server, because admitting an arbitrary prefix of a
// misbehaving server's tools would be worse than admitting none.
func TestDiscoverRejectsAServerExceedingTheToolBound(t *testing.T) {
	_, err := discoverFrom(t, "flood")
	if !errors.Is(err, ErrDiscoveryBound) {
		t.Fatalf("err = %v, want ErrDiscoveryBound", err)
	}
	if !strings.Contains(err.Error(), "srv") {
		t.Fatalf("err = %v, want it to name the offending server", err)
	}
}

// TestDiscoverRejectsAnOversizedToolDefinition closes the other exhaustion
// route: one tool with an enormous description.
func TestDiscoverRejectsAnOversizedToolDefinition(t *testing.T) {
	_, err := discoverFrom(t, "oversized")
	if !errors.Is(err, ErrDiscoveryBound) {
		t.Fatalf("err = %v, want ErrDiscoveryBound", err)
	}
	if !strings.Contains(err.Error(), "huge") {
		t.Fatalf("err = %v, want it to name the offending tool", err)
	}
}

// TestDiscoverQualifiesHostileToolNames: the names come from the server, so
// whatever it sends must land in the catalog's own alphabet.
func TestDiscoverQualifiesHostileToolNames(t *testing.T) {
	result, err := discoverFrom(t, "hostile_names")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Specs) != 2 {
		t.Fatalf("discovered %d tools, want 2 (dropped=%+v)", len(result.Specs), result.Dropped)
	}
	for _, spec := range result.Specs {
		if !legalQualifiedName.MatchString(spec.Name) {
			t.Errorf("%q is not a legal qualified name", spec.Name)
		}
		if len(spec.Name) > MaxQualifiedNameBytes {
			t.Errorf("%q is %d bytes, over the %d-byte bound", spec.Name, len(spec.Name), MaxQualifiedNameBytes)
		}
	}
	if _, err := tools.NewCatalog(result.Specs); err != nil {
		t.Fatalf("hostile names did not survive catalog registration: %v", err)
	}
}

// TestDiscoverReturnsNothingWhenTheServerOffersNoTools keeps a server without
// tool support from being an error.
func TestDiscoverReturnsNothingWhenTheServerOffersNoTools(t *testing.T) {
	result, err := discoverFrom(t, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// The default fixture registers exactly one tool.
	if len(result.Specs) != 1 {
		t.Fatalf("discovered %d tools, want the fixture's single greet tool", len(result.Specs))
	}
}

func TestDiscoverRefusesADisconnectedServer(t *testing.T) {
	var server *Server
	if _, err := server.Discover(t.Context()); !errors.Is(err, ErrInvalidServerConfig) {
		t.Fatalf("err = %v, want ErrInvalidServerConfig", err)
	}
}

// TestProjectToolDropsASchemaThatIsNotAJSONObject covers the sharpest drop
// case at the projection level rather than end to end, because this SDK's
// own server side will not publish it: AddTool panics with "can't marshal
// input schema to a JSON object". Only a server built on some other MCP
// implementation could send one, which is exactly why the defense has to
// exist and cannot be exercised through the fixture.
func TestProjectToolDropsASchemaThatIsNotAJSONObject(t *testing.T) {
	server := &Server{config: ServerConfig{Name: "srv"}}
	for name, schema := range map[string]any{
		"array":  []string{"not", "an", "object"},
		"string": "not an object",
		"number": 42,
		"null":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			spec, diagnostic, _, err := server.projectTool(&sdk.Tool{
				Name: "bad", Description: "d", InputSchema: schema,
			})
			if err != nil {
				t.Fatalf("projectTool returned a hard error for one bad tool: %v", err)
			}
			if diagnostic == nil {
				t.Fatalf("the tool was registered with schema %v: %+v", schema, spec)
			}
			if diagnostic.RawName != "bad" || diagnostic.Reason == "" {
				t.Fatalf("diagnostic = %+v, want the raw name and a stated reason", *diagnostic)
			}
		})
	}
}

// TestProjectToolDropsAToolWithNoName: a name is what a call is addressed to,
// so a tool without one cannot be reached even if registered.
func TestProjectToolDropsAToolWithNoName(t *testing.T) {
	server := &Server{config: ServerConfig{Name: "srv"}}
	_, diagnostic, _, err := server.projectTool(&sdk.Tool{Name: "", Description: "d"})
	if err != nil {
		t.Fatalf("projectTool: %v", err)
	}
	if diagnostic == nil {
		t.Fatal("a tool with no name was registered")
	}
}

// TestDefinitionBoundIsWiderThanTheCatalogsSchemaBound records a real
// difference between two bounds this slice did not invent and does not
// reconcile: discovery admits a definition up to MaxToolDefinitionBytes
// (description plus schema), while registration admits a schema up to
// tools.MaxMCPSchemaBytes, which is half that. A schema between them clears
// discovery and is dropped at registration.
//
// That is a safe outcome — the tool is dropped with a reason and the server
// survives — but the asymmetry is deliberate to expose rather than paper
// over, because a reader comparing the two constants would otherwise assume
// they agree.
func TestDefinitionBoundIsWiderThanTheCatalogsSchemaBound(t *testing.T) {
	if MaxToolDefinitionBytes <= tools.MaxMCPSchemaBytes {
		t.Skip("the bounds no longer disagree; this guard has served its purpose")
	}
	server := &Server{config: ServerConfig{Name: "srv"}}
	between := tools.MaxMCPSchemaBytes + 1000
	if between >= MaxToolDefinitionBytes {
		t.Fatalf("no size lies between the two bounds")
	}
	schema := json.RawMessage(`{"type":"object","description":"` + strings.Repeat("p", between) + `"}`)
	_, diagnostic, _, err := server.projectTool(&sdk.Tool{Name: "mid", InputSchema: schema})
	if err != nil {
		t.Fatalf("a schema between the bounds failed the whole server: %v", err)
	}
	if diagnostic == nil {
		t.Fatal("a schema over the catalog's own bound was registered")
	}
}
