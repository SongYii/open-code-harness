package tools

import (
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const (
	NameReadFile  = "read_file"
	NameWriteFile = "write_file"
	NameListDir   = "list_dir"
	NameExec      = "exec"

	SourceBuiltin = "builtin"
	SourceMCP     = "mcp"

	MaxPathBytes         = 4096
	MaxWriteContentBytes = 32768
	MaxArgvItems         = 64
	MaxArgvItemBytes     = 4096
	MaxListDirEntries    = 256
	MaxListDirDepth      = 2
	DefaultListDirDepth  = 1
)

const (
	schemaReadFile  = `{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1,"maxLength":4096}}}`
	schemaWriteFile = `{"type":"object","additionalProperties":false,"required":["path","content"],"properties":{"path":{"type":"string","minLength":1,"maxLength":4096},"content":{"type":"string","maxLength":32768}}}`
	schemaListDir   = `{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1,"maxLength":4096},"depth":{"type":"integer","minimum":1,"maximum":2}}}`
	schemaExec      = `{"type":"object","additionalProperties":false,"required":["argv"],"properties":{"argv":{"type":"array","minItems":1,"maxItems":64,"items":{"type":"string","maxLength":4096}},"cwd":{"type":"string","minLength":1,"maxLength":4096}}}`
)

// Catalog is an immutable name-unique ToolSpec set.
type Catalog struct {
	specs  []domain.ToolSpec
	byName map[string]int
}

func NewCatalog(specs []domain.ToolSpec) (*Catalog, error) {
	catalog := &Catalog{
		specs:  make([]domain.ToolSpec, 0, len(specs)),
		byName: make(map[string]int, len(specs)),
	}
	for _, spec := range specs {
		if err := validateSpec(spec); err != nil {
			return nil, err
		}
		if _, exists := catalog.byName[spec.Name]; exists {
			return nil, duplicateNameError()
		}
		catalog.byName[spec.Name] = len(catalog.specs)
		catalog.specs = append(catalog.specs, cloneSpec(spec))
	}
	return catalog, nil
}

func (c *Catalog) Spec(name string) (domain.ToolSpec, bool) {
	if c == nil {
		return domain.ToolSpec{}, false
	}
	index, ok := c.byName[name]
	if !ok {
		return domain.ToolSpec{}, false
	}
	return cloneSpec(c.specs[index]), true
}

func (c *Catalog) Specs() []domain.ToolSpec {
	if c == nil {
		return nil
	}
	out := make([]domain.ToolSpec, len(c.specs))
	for i, spec := range c.specs {
		out[i] = cloneSpec(spec)
	}
	return out
}

func (c *Catalog) Schemas() []domain.ToolSchema {
	if c == nil {
		return nil
	}
	out := make([]domain.ToolSchema, len(c.specs))
	for i, spec := range c.specs {
		out[i] = domain.ToolSchema{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: append([]byte(nil), spec.InputSchema...),
		}
	}
	return out
}

func DefaultWorkspaceSpecs() []domain.ToolSpec {
	return []domain.ToolSpec{
		{
			Name:        NameReadFile,
			Description: "Read a UTF-8 file inside the workspace.",
			InputSchema: []byte(schemaReadFile),
			Source:      SourceBuiltin,
			Risk:        domain.RiskRead,
			Mutates:     false,
		},
		{
			Name:        NameWriteFile,
			Description: "Write a UTF-8 file inside the workspace.",
			InputSchema: []byte(schemaWriteFile),
			Source:      SourceBuiltin,
			Risk:        domain.RiskWrite,
			Mutates:     true,
		},
		{
			Name:        NameListDir,
			Description: "List workspace directory entries.",
			InputSchema: []byte(schemaListDir),
			Source:      SourceBuiltin,
			Risk:        domain.RiskRead,
			Mutates:     false,
		},
		{
			Name:        NameExec,
			Description: "Run an argv command inside the workspace.",
			InputSchema: []byte(schemaExec),
			Source:      SourceBuiltin,
			Risk:        domain.RiskExec,
			Mutates:     true,
		},
	}
}

func validateSpec(spec domain.ToolSpec) error {
	if spec.Name == "" || !utf8.ValidString(spec.Name) || strings.TrimSpace(spec.Name) != spec.Name {
		return specError()
	}
	if !utf8.ValidString(spec.Description) {
		return specError()
	}
	switch spec.Source {
	case SourceBuiltin, SourceMCP:
	default:
		return specError()
	}
	switch spec.Risk {
	case domain.RiskRead, domain.RiskWrite, domain.RiskExec, domain.RiskNetwork:
	default:
		return specError()
	}
	// Mutates must match risk so a catalog cannot self-declare a write as read.
	wantMutates := spec.Risk == domain.RiskWrite || spec.Risk == domain.RiskExec
	if spec.Mutates != wantMutates {
		return specError()
	}
	if spec.Source == SourceMCP {
		return validateMCPSchema(spec.InputSchema)
	}
	if _, err := compileSchema(spec.InputSchema); err != nil {
		return err
	}
	return nil
}

func cloneSpec(spec domain.ToolSpec) domain.ToolSpec {
	spec.InputSchema = append([]byte(nil), spec.InputSchema...)
	return spec
}
