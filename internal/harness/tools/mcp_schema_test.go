package tools

import (
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// realisticMCPSchema is the shape a published MCP tool actually ships: a
// per-property description, a number type, and no additionalProperties. Every
// one of those three is rejected by compileSchema, which was written for this
// project's own four builtin tools and admits twelve keywords and four types.
const realisticMCPSchema = `{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "what to search for"},
    "limit": {"type": "number", "default": 10}
  },
  "required": ["query"]
}`

func mcpSpec(schema string) domain.ToolSpec {
	return domain.ToolSpec{
		Name:        "mcp_srv_search",
		Description: "search",
		InputSchema: []byte(schema),
		Source:      SourceMCP,
		Risk:        domain.RiskExec,
		Mutates:     true,
	}
}

func builtinSpec(schema string) domain.ToolSpec {
	return domain.ToolSpec{
		Name:        "read_file",
		Description: "read",
		InputSchema: []byte(schema),
		Source:      SourceBuiltin,
		Risk:        domain.RiskRead,
		Mutates:     false,
	}
}

// TestBuiltinSchemaValidationIsUnchanged is the guard the design's amendment
// requires: the MCP relaxation must not leak into the builtin path. Every
// schema compileSchema refused before must still be refused for a builtin.
func TestBuiltinSchemaValidationIsUnchanged(t *testing.T) {
	for _, schema := range []string{
		realisticMCPSchema,
		`{"type":"object","properties":{"a":{"type":"string","description":"d"}},"additionalProperties":false}`,
		`{"type":"object","additionalProperties":false,"properties":{"n":{"type":"number"}}}`,
		`{"type":"object","properties":{"a":{"type":"string"}}}`,
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"properties":{}}`,
	} {
		if _, err := NewCatalog([]domain.ToolSpec{builtinSpec(schema)}); err == nil {
			t.Errorf("a builtin spec with schema %.60s... was accepted; builtin strictness must be unchanged", schema)
		}
	}

	// And the four real builtin specs still build a catalog unchanged.
	if _, err := NewCatalog(DefaultWorkspaceSpecs()); err != nil {
		t.Fatalf("NewCatalog(DefaultWorkspaceSpecs()): %v", err)
	}
}

// TestCatalogAcceptsAnMCPSpecWhoseSchemaThisProjectCannotCompile is the
// decision itself. Before it, a healthy server's tools were all refused at
// registration and the harness started with nothing.
func TestCatalogAcceptsAnMCPSpecWhoseSchemaThisProjectCannotCompile(t *testing.T) {
	catalog, err := NewCatalog([]domain.ToolSpec{mcpSpec(realisticMCPSchema)})
	if err != nil {
		t.Fatalf("NewCatalog refused a realistic MCP schema: %v", err)
	}
	spec, ok := catalog.Spec("mcp_srv_search")
	if !ok {
		t.Fatal("the tool did not reach the catalog")
	}
	// The model is shown this field, so it must still be the server's own
	// schema rather than a permissive stand-in.
	if !strings.Contains(string(spec.InputSchema), "what to search for") {
		t.Fatalf("InputSchema was not preserved verbatim: %s", spec.InputSchema)
	}
}

// TestCatalogStillRejectsAnMCPSchemaThatIsNotAJSONObject keeps degrading from
// turning into accepting anything.
func TestCatalogStillRejectsAnMCPSchemaThatIsNotAJSONObject(t *testing.T) {
	for _, schema := range []string{``, `   `, `[]`, `"a string"`, `42`, `null`, `{`, `{} trailing`} {
		if _, err := NewCatalog([]domain.ToolSpec{mcpSpec(schema)}); err == nil {
			t.Errorf("NewCatalog accepted a non-object MCP schema %q", schema)
		}
	}
}

// TestCatalogRejectsAnUnboundedMCPSchema keeps an externally supplied
// definition from being unbounded, which this project's documentation rule
// requires of any external input.
func TestCatalogRejectsAnUnboundedMCPSchema(t *testing.T) {
	huge := `{"type":"object","x":"` + strings.Repeat("y", MaxMCPSchemaBytes) + `"}`
	if _, err := NewCatalog([]domain.ToolSpec{mcpSpec(huge)}); err == nil {
		t.Fatalf("NewCatalog accepted a %d-byte MCP schema; the bound is %d", len(huge), MaxMCPSchemaBytes)
	}
}

// TestValidateArgsStaysStrictWhenAnMCPSchemaDoesCompile: a server that
// happens to publish a schema in this project's own dialect gets the full
// check, not the degraded one.
func TestValidateArgsStaysStrictWhenAnMCPSchemaDoesCompile(t *testing.T) {
	strict := `{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1}}}`
	spec := mcpSpec(strict)
	if err := ValidateArgs(spec, `{"path":"ok"}`); err != nil {
		t.Fatalf("valid arguments were refused: %v", err)
	}
	if err := ValidateArgs(spec, `{"path":"ok","invented":1}`); err == nil {
		t.Fatal("a model-invented key passed a compilable MCP schema; strictness must survive when the schema compiles")
	}
	if err := ValidateArgs(spec, `{"path":""}`); err == nil {
		t.Fatal("minLength was not enforced for a compilable MCP schema")
	}
}

// TestValidateArgsDegradesToObjectCheckWhenAnMCPSchemaCannotCompile is the
// other half: the tool stays usable, and the degraded check is still a check.
func TestValidateArgsDegradesToObjectCheckWhenAnMCPSchemaCannotCompile(t *testing.T) {
	spec := mcpSpec(realisticMCPSchema)
	if err := ValidateArgs(spec, `{"query":"hello","limit":5}`); err != nil {
		t.Fatalf("arguments matching the server's own schema were refused: %v", err)
	}
	// Degrading is not accepting anything.
	for _, raw := range []string{`[]`, `"text"`, `42`, `null`, `{`, `{} trailing`, "\xff\xfe"} {
		if err := ValidateArgs(spec, raw); err == nil {
			t.Errorf("degraded validation accepted %q, which is not a JSON object", raw)
		}
	}
}

// TestValidateArgsNeverDegradesForABuiltin is the mutation target: removing
// the source guard must turn this red.
func TestValidateArgsNeverDegradesForABuiltin(t *testing.T) {
	spec := builtinSpec(realisticMCPSchema)
	if err := ValidateArgs(spec, `{"query":"hello"}`); err == nil {
		t.Fatal("a builtin with an uncompilable schema validated arguments; the builtin path must never degrade")
	}
}
