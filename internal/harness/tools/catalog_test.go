package tools

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestDefaultWorkspaceSpecsLockedContracts(t *testing.T) {
	specs := DefaultWorkspaceSpecs()
	if len(specs) != 4 {
		t.Fatalf("len(DefaultWorkspaceSpecs()) = %d, want 4", len(specs))
	}

	byName := map[string]domain.ToolSpec{}
	for _, spec := range specs {
		if _, exists := byName[spec.Name]; exists {
			t.Fatalf("duplicate default name %q", spec.Name)
		}
		byName[spec.Name] = spec
	}

	read := mustSpec(t, byName, NameReadFile)
	if read.Risk != domain.RiskRead || read.Mutates || read.Source != SourceBuiltin {
		t.Fatalf("read_file identity = %#v", read)
	}
	assertSchema(t, read.InputSchema, map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"path"},
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "minLength": float64(1), "maxLength": float64(4096)},
		},
	})

	write := mustSpec(t, byName, NameWriteFile)
	if write.Risk != domain.RiskWrite || !write.Mutates {
		t.Fatalf("write_file identity = %#v", write)
	}
	assertSchema(t, write.InputSchema, map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"path", "content"},
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "minLength": float64(1), "maxLength": float64(4096)},
			"content": map[string]any{"type": "string", "maxLength": float64(32768)},
		},
	})

	list := mustSpec(t, byName, NameListDir)
	if list.Risk != domain.RiskRead || list.Mutates {
		t.Fatalf("list_dir identity = %#v", list)
	}
	assertSchema(t, list.InputSchema, map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"path"},
		"properties": map[string]any{
			"path":  map[string]any{"type": "string", "minLength": float64(1), "maxLength": float64(4096)},
			"depth": map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(2)},
		},
	})
	var listObj map[string]any
	if err := json.Unmarshal(list.InputSchema, &listObj); err != nil {
		t.Fatal(err)
	}
	props := listObj["properties"].(map[string]any)
	if _, requiredHasDepth := asStringSet(listObj["required"])["depth"]; requiredHasDepth {
		t.Fatal("list_dir.depth must be optional; omitted ≡ 1")
	}
	if _, ok := props["timeout"]; ok {
		t.Fatal("list_dir must not expose timeout")
	}

	execSpec := mustSpec(t, byName, NameExec)
	if execSpec.Risk != domain.RiskExec || !execSpec.Mutates {
		t.Fatalf("exec identity = %#v", execSpec)
	}
	assertSchema(t, execSpec.InputSchema, map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"argv"},
		"properties": map[string]any{
			"argv": map[string]any{
				"type":     "array",
				"minItems": float64(1),
				"maxItems": float64(64),
				"items":    map[string]any{"type": "string", "maxLength": float64(4096)},
			},
			"cwd": map[string]any{"type": "string", "minLength": float64(1), "maxLength": float64(4096)},
		},
	})
	var execObj map[string]any
	if err := json.Unmarshal(execSpec.InputSchema, &execObj); err != nil {
		t.Fatal(err)
	}
	if _, ok := execObj["properties"].(map[string]any)["timeout"]; ok {
		t.Fatal("exec timeout is config, not a model field")
	}

	catalog, err := NewCatalog(specs)
	if err != nil {
		t.Fatalf("NewCatalog(DefaultWorkspaceSpecs()) error = %v", err)
	}
	if got := catalog.Specs(); len(got) != 4 {
		t.Fatalf("Specs() len = %d", len(got))
	}
	if _, ok := catalog.Spec(NameListDir); !ok {
		t.Fatal("Spec(list_dir) missing")
	}
}

func TestNewCatalogRejectsDuplicateNames(t *testing.T) {
	specs := DefaultWorkspaceSpecs()
	specs = append(specs, specs[0])
	_, err := NewCatalog(specs)
	if !IsCode(err, CodeDuplicateName) {
		t.Fatalf("NewCatalog(duplicate) error = %v, want %s", err, CodeDuplicateName)
	}
}

func TestNewCatalogRejectsUnsupportedSchemaKeywords(t *testing.T) {
	base := DefaultWorkspaceSpecs()[0]
	keywords := []string{"$ref", "oneOf", "anyOf", "allOf", "pattern", "format", "description", "$schema"}
	for _, keyword := range keywords {
		t.Run(keyword, func(t *testing.T) {
			spec := base
			spec.InputSchema = []byte(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string"}},"` + keyword + `":true}`)
			_, err := NewCatalog([]domain.ToolSpec{spec})
			if !IsCode(err, CodeInvalidSpec) {
				t.Fatalf("keyword %q error = %v, want %s", keyword, err, CodeInvalidSpec)
			}
		})
	}
}

func TestNewCatalogRejectsInvalidSpecs(t *testing.T) {
	valid := DefaultWorkspaceSpecs()[0]
	tests := []struct {
		name string
		spec domain.ToolSpec
	}{
		{name: "empty name", spec: mutateSpec(valid, func(s *domain.ToolSpec) { s.Name = "" })},
		{name: "padded name", spec: mutateSpec(valid, func(s *domain.ToolSpec) { s.Name = " read_file" })},
		{name: "unknown risk", spec: mutateSpec(valid, func(s *domain.ToolSpec) { s.Risk = "invented" })},
		{name: "unknown source", spec: mutateSpec(valid, func(s *domain.ToolSpec) { s.Source = "plugin" })},
		{name: "read mutates", spec: mutateSpec(valid, func(s *domain.ToolSpec) { s.Mutates = true })},
		{name: "write not mutates", spec: mutateSpec(DefaultWorkspaceSpecs()[1], func(s *domain.ToolSpec) { s.Mutates = false })},
		{name: "empty schema", spec: mutateSpec(valid, func(s *domain.ToolSpec) { s.InputSchema = nil })},
		{name: "schema array", spec: mutateSpec(valid, func(s *domain.ToolSpec) { s.InputSchema = []byte(`[]`) })},
		{name: "additionalProperties true", spec: mutateSpec(valid, func(s *domain.ToolSpec) {
			s.InputSchema = []byte(`{"type":"object","additionalProperties":true,"properties":{}}`)
		})},
		{name: "type boolean", spec: mutateSpec(valid, func(s *domain.ToolSpec) {
			s.InputSchema = []byte(`{"type":"boolean"}`)
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCatalog([]domain.ToolSpec{test.spec}); !IsCode(err, CodeInvalidSpec) {
				t.Fatalf("error = %v, want %s", err, CodeInvalidSpec)
			}
		})
	}
}

func TestCatalogCopiesAreDefensive(t *testing.T) {
	catalog, err := NewCatalog(DefaultWorkspaceSpecs())
	if err != nil {
		t.Fatal(err)
	}
	specs := catalog.Specs()
	specs[0].Name = "mutated"
	specs[0].InputSchema[0] = 'X'
	got, ok := catalog.Spec(NameReadFile)
	if !ok || got.Name != NameReadFile || got.InputSchema[0] == 'X' {
		t.Fatalf("catalog mutated through Specs(): %#v", got)
	}
	got.InputSchema[1] = 'Y'
	again, _ := catalog.Spec(NameReadFile)
	if bytes.Contains(again.InputSchema, []byte("Y")) {
		t.Fatal("catalog mutated through Spec() schema bytes")
	}
	schemas := catalog.Schemas()
	if len(schemas) != 4 || schemas[0].Name != NameReadFile {
		t.Fatalf("Schemas() = %#v", schemas)
	}
	schemas[0].InputSchema[0] = 'Z'
	if spec, _ := catalog.Spec(NameReadFile); spec.InputSchema[0] == 'Z' {
		t.Fatal("catalog mutated through Schemas()")
	}
}

func TestNewCatalogEmptyIsValid(t *testing.T) {
	catalog, err := NewCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	if specs := catalog.Specs(); len(specs) != 0 {
		t.Fatalf("empty catalog Specs() = %#v", specs)
	}
	if _, ok := catalog.Spec(NameReadFile); ok {
		t.Fatal("empty catalog returned a spec")
	}
}

func TestErrorStableAndSecretFree(t *testing.T) {
	err := argsError()
	if got := err.Error(); got != "tools/invalid_args" {
		t.Fatalf("Error() = %q", got)
	}
	if strings.Contains(err.Error(), "{") || strings.Contains(err.Error(), "path") {
		t.Fatalf("Error() leaked payload: %q", err.Error())
	}
	if !IsCode(err, CodeInvalidArgs) || IsCode(err, CodeInvalidSpec) {
		t.Fatal("IsCode mismatch")
	}
	if IsCode(&Error{Code: "invented"}, "invented") {
		t.Fatal("IsCode accepted undeclared code")
	}
}

func mustSpec(t *testing.T, byName map[string]domain.ToolSpec, name string) domain.ToolSpec {
	t.Helper()
	spec, ok := byName[name]
	if !ok {
		t.Fatalf("missing spec %q", name)
	}
	return spec
}

func assertSchema(t *testing.T, raw json.RawMessage, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("schema JSON: %v", err)
	}
	gotBytes, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var gotNorm, wantNorm any
	if err := json.Unmarshal(gotBytes, &gotNorm); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantBytes, &wantNorm); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(gotNorm, wantNorm) {
		t.Fatalf("schema = %s\nwant %s", gotBytes, wantBytes)
	}
}

func asStringSet(raw any) map[string]struct{} {
	items, _ := raw.([]any)
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out[s] = struct{}{}
		}
	}
	return out
}

func mutateSpec(spec domain.ToolSpec, fn func(*domain.ToolSpec)) domain.ToolSpec {
	spec.InputSchema = append([]byte(nil), spec.InputSchema...)
	fn(&spec)
	return spec
}
