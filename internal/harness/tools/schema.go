package tools

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// Closed subset. Any other keyword fails at compile (NewCatalog).
var allowedSchemaKeywords = map[string]struct{}{
	"type":                 {},
	"properties":           {},
	"required":             {},
	"additionalProperties": {},
	"enum":                 {},
	"minLength":            {},
	"maxLength":            {},
	"minimum":              {},
	"maximum":              {},
	"minItems":             {},
	"maxItems":             {},
	"items":                {},
}

type compiledSchema struct {
	kind       string
	properties map[string]*compiledSchema
	required   []string
	enum       []any
	minLength  *int
	maxLength  *int
	minimum    *float64
	maximum    *float64
	minItems   *int
	maxItems   *int
	items      *compiledSchema
}

func ValidateArgs(spec domain.ToolSpec, raw string) error {
	if !utf8.ValidString(raw) {
		return argsError()
	}
	compiled, err := compileSchema(spec.InputSchema)
	if err != nil {
		// A builtin's schema is written here and must always compile; a
		// failure is a real defect, not a dialect difference.
		if spec.Source != SourceMCP {
			return argsError()
		}
		// An MCP server writes its own schema in full JSON Schema, which
		// this project's builtin-shaped compiler frequently cannot read.
		// Refusing the call would make the tool permanently uncallable, so
		// the check degrades to the shape every tool call must have anyway.
		// The server owns rejecting arguments its own schema forbids; what
		// protects this harness is unchanged and independent of the schema —
		// every MCP tool is RiskExec, approval-gated, and confined.
		return validateDegradedArgs(raw)
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return argsError()
	}
	if decoder.More() {
		return argsError()
	}
	if err := compiled.validate(value); err != nil {
		return argsError()
	}
	return nil
}

func compileSchema(raw json.RawMessage) (*compiledSchema, error) {
	if len(bytes.TrimSpace(raw)) == 0 || !isJSONObject(raw) {
		return nil, specError()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var obj map[string]any
	if err := decoder.Decode(&obj); err != nil {
		return nil, specError()
	}
	if decoder.More() {
		return nil, specError()
	}
	return compileSchemaObject(obj)
}

func compileSchemaObject(obj map[string]any) (*compiledSchema, error) {
	if obj == nil {
		return nil, specError()
	}
	for key := range obj {
		if _, ok := allowedSchemaKeywords[key]; !ok {
			return nil, specError()
		}
	}
	kind, ok := obj["type"].(string)
	if !ok {
		return nil, specError()
	}
	compiled := &compiledSchema{kind: kind}
	switch kind {
	case "object":
		if err := compileObjectKeywords(compiled, obj); err != nil {
			return nil, err
		}
	case "string":
		if err := compileStringKeywords(compiled, obj); err != nil {
			return nil, err
		}
	case "integer":
		if err := compileIntegerKeywords(compiled, obj); err != nil {
			return nil, err
		}
	case "array":
		if err := compileArrayKeywords(compiled, obj); err != nil {
			return nil, err
		}
	default:
		return nil, specError()
	}
	if err := compileEnum(compiled, obj); err != nil {
		return nil, err
	}
	return compiled, nil
}

func compileObjectKeywords(compiled *compiledSchema, obj map[string]any) error {
	// Object schemas must close extra fields so model-invented keys cannot pass.
	if _, err := expectBoolFalse(obj, "additionalProperties"); err != nil {
		return err
	}
	if _, exists := obj["minLength"]; exists {
		return specError()
	}
	if _, exists := obj["maxLength"]; exists {
		return specError()
	}
	if _, exists := obj["minimum"]; exists {
		return specError()
	}
	if _, exists := obj["maximum"]; exists {
		return specError()
	}
	if _, exists := obj["minItems"]; exists {
		return specError()
	}
	if _, exists := obj["maxItems"]; exists {
		return specError()
	}
	if _, exists := obj["items"]; exists {
		return specError()
	}
	if rawProps, exists := obj["properties"]; exists {
		props, ok := rawProps.(map[string]any)
		if !ok {
			return specError()
		}
		compiled.properties = make(map[string]*compiledSchema, len(props))
		for name, raw := range props {
			child, ok := raw.(map[string]any)
			if !ok {
				return specError()
			}
			nested, err := compileSchemaObject(child)
			if err != nil {
				return err
			}
			compiled.properties[name] = nested
		}
	} else {
		compiled.properties = map[string]*compiledSchema{}
	}
	if rawRequired, exists := obj["required"]; exists {
		items, ok := rawRequired.([]any)
		if !ok {
			return specError()
		}
		compiled.required = make([]string, 0, len(items))
		seen := make(map[string]struct{}, len(items))
		for _, item := range items {
			name, ok := item.(string)
			if !ok || name == "" {
				return specError()
			}
			if _, dup := seen[name]; dup {
				return specError()
			}
			if _, known := compiled.properties[name]; !known {
				return specError()
			}
			seen[name] = struct{}{}
			compiled.required = append(compiled.required, name)
		}
	}
	return nil
}

func compileStringKeywords(compiled *compiledSchema, obj map[string]any) error {
	if err := rejectKeywords(obj, "properties", "required", "additionalProperties", "minimum", "maximum", "minItems", "maxItems", "items"); err != nil {
		return err
	}
	if v, exists, err := optionalNonNegativeInt(obj, "minLength"); err != nil {
		return err
	} else if exists {
		compiled.minLength = &v
	}
	if v, exists, err := optionalNonNegativeInt(obj, "maxLength"); err != nil {
		return err
	} else if exists {
		compiled.maxLength = &v
	}
	if compiled.minLength != nil && compiled.maxLength != nil && *compiled.minLength > *compiled.maxLength {
		return specError()
	}
	return nil
}

func compileIntegerKeywords(compiled *compiledSchema, obj map[string]any) error {
	if err := rejectKeywords(obj, "properties", "required", "additionalProperties", "minLength", "maxLength", "minItems", "maxItems", "items"); err != nil {
		return err
	}
	if v, exists, err := optionalNumber(obj, "minimum"); err != nil {
		return err
	} else if exists {
		compiled.minimum = &v
	}
	if v, exists, err := optionalNumber(obj, "maximum"); err != nil {
		return err
	} else if exists {
		compiled.maximum = &v
	}
	if compiled.minimum != nil && compiled.maximum != nil && *compiled.minimum > *compiled.maximum {
		return specError()
	}
	return nil
}

func compileArrayKeywords(compiled *compiledSchema, obj map[string]any) error {
	if err := rejectKeywords(obj, "properties", "required", "additionalProperties", "minLength", "maxLength", "minimum", "maximum"); err != nil {
		return err
	}
	if v, exists, err := optionalNonNegativeInt(obj, "minItems"); err != nil {
		return err
	} else if exists {
		compiled.minItems = &v
	}
	if v, exists, err := optionalNonNegativeInt(obj, "maxItems"); err != nil {
		return err
	} else if exists {
		compiled.maxItems = &v
	}
	if compiled.minItems != nil && compiled.maxItems != nil && *compiled.minItems > *compiled.maxItems {
		return specError()
	}
	rawItems, exists := obj["items"]
	if !exists {
		return specError()
	}
	child, ok := rawItems.(map[string]any)
	if !ok {
		return specError()
	}
	nested, err := compileSchemaObject(child)
	if err != nil {
		return err
	}
	compiled.items = nested
	return nil
}

func compileEnum(compiled *compiledSchema, obj map[string]any) error {
	raw, exists := obj["enum"]
	if !exists {
		return nil
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return specError()
	}
	compiled.enum = items
	return nil
}

func (s *compiledSchema) validate(value any) error {
	if s == nil {
		return argsError()
	}
	if s.enum != nil {
		matched := false
		for _, candidate := range s.enum {
			if jsonValuesEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return argsError()
		}
	}
	switch s.kind {
	case "object":
		return s.validateObject(value)
	case "string":
		return s.validateString(value)
	case "integer":
		return s.validateInteger(value)
	case "array":
		return s.validateArray(value)
	default:
		return argsError()
	}
}

func (s *compiledSchema) validateObject(value any) error {
	obj, ok := value.(map[string]any)
	if !ok {
		return argsError()
	}
	for _, name := range s.required {
		if _, exists := obj[name]; !exists {
			return argsError()
		}
	}
	for name, child := range obj {
		schema, ok := s.properties[name]
		if !ok {
			return argsError()
		}
		if err := schema.validate(child); err != nil {
			return err
		}
	}
	return nil
}

func (s *compiledSchema) validateString(value any) error {
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) {
		return argsError()
	}
	// maxLength/minLength are UTF-8 bytes to match the 32 KiB argument bound.
	n := len(text)
	if s.minLength != nil && n < *s.minLength {
		return argsError()
	}
	if s.maxLength != nil && n > *s.maxLength {
		return argsError()
	}
	return nil
}

func (s *compiledSchema) validateInteger(value any) error {
	number, ok := asInteger(value)
	if !ok {
		return argsError()
	}
	if s.minimum != nil && number < *s.minimum {
		return argsError()
	}
	if s.maximum != nil && number > *s.maximum {
		return argsError()
	}
	return nil
}

func (s *compiledSchema) validateArray(value any) error {
	items, ok := value.([]any)
	if !ok {
		return argsError()
	}
	if s.minItems != nil && len(items) < *s.minItems {
		return argsError()
	}
	if s.maxItems != nil && len(items) > *s.maxItems {
		return argsError()
	}
	if s.items == nil {
		return argsError()
	}
	for _, item := range items {
		if err := s.items.validate(item); err != nil {
			return err
		}
	}
	return nil
}

func asInteger(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	if i, err := number.Int64(); err == nil {
		return float64(i), true
	}
	f, err := number.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, false
	}
	return f, true
}

func jsonValuesEqual(left, right any) bool {
	if ln, ok := left.(json.Number); ok {
		left = jsonNumberAsValue(ln)
	}
	if rn, ok := right.(json.Number); ok {
		right = jsonNumberAsValue(rn)
	}
	return jsonEqual(left, right)
}

func jsonNumberAsValue(n json.Number) any {
	if i, err := n.Int64(); err == nil {
		return float64(i)
	}
	if f, err := n.Float64(); err == nil {
		return f
	}
	return string(n)
}

func jsonEqual(left, right any) bool {
	switch l := left.(type) {
	case nil:
		return right == nil
	case bool:
		r, ok := right.(bool)
		return ok && l == r
	case string:
		r, ok := right.(string)
		return ok && l == r
	case float64:
		r, ok := right.(float64)
		return ok && l == r
	default:
		lb, err := json.Marshal(left)
		if err != nil {
			return false
		}
		rb, err := json.Marshal(right)
		if err != nil {
			return false
		}
		return bytes.Equal(lb, rb)
	}
}

func isJSONObject(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func rejectKeywords(obj map[string]any, keys ...string) error {
	for _, key := range keys {
		if _, exists := obj[key]; exists {
			return specError()
		}
	}
	return nil
}

func expectBoolFalse(obj map[string]any, key string) (bool, error) {
	raw, exists := obj[key]
	if !exists {
		return false, specError()
	}
	value, ok := raw.(bool)
	if !ok || value {
		return false, specError()
	}
	return false, nil
}

func optionalNonNegativeInt(obj map[string]any, key string) (int, bool, error) {
	raw, exists := obj[key]
	if !exists {
		return 0, false, nil
	}
	number, ok := asInteger(raw)
	if !ok || number < 0 || number > float64(math.MaxInt32) {
		return 0, false, specError()
	}
	return int(number), true, nil
}

func optionalNumber(obj map[string]any, key string) (float64, bool, error) {
	raw, exists := obj[key]
	if !exists {
		return 0, false, nil
	}
	number, ok := raw.(json.Number)
	if !ok {
		return 0, false, specError()
	}
	value, err := number.Float64()
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false, specError()
	}
	return value, true, nil
}

// validateDegradedArgs is the fallback check for an MCP tool whose declared
// schema this project's compiler cannot read. It requires exactly what every
// tool call must be regardless of schema: one well-formed JSON object, with
// nothing trailing it.
//
// Degraded is not absent. A tool call that is an array, a bare string, a
// number, null, truncated, or followed by trailing content is still refused
// here, so the argument shape the rest of the pipeline assumes still holds.
func validateDegradedArgs(raw string) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return argsError()
	}
	if decoder.More() {
		return argsError()
	}
	if _, isObject := value.(map[string]any); !isObject {
		return argsError()
	}
	return nil
}

// MaxMCPSchemaBytes bounds one MCP tool's declared input schema. The schema
// is written by an external server, so it gets a stated bound like every
// other externally supplied value here; the number matches
// MaxWriteContentBytes, this project's existing round bound for a
// caller-supplied blob.
const MaxMCPSchemaBytes = 32768

// validateMCPSchema is registration-time validation for a tool discovered
// from an MCP server.
//
// It deliberately does not require compileSchema to succeed. That compiler
// was written for this project's own four builtin tools: twelve keywords
// applied recursively, four permitted type values, and a mandatory
// additionalProperties:false. Published MCP tools use full JSON Schema, so
// requiring it here refused a per-property description, "type":"number",
// "type":"boolean", $schema, title, anyOf, and default — in practice every
// tool of every healthy server, while the harness started and reported
// success. See the design's 2026-09-05 amendment, and the reference survey
// that found no comparable project validates external schemas this way.
//
// What remains required is that the schema is a bounded JSON object. The
// per-call check in ValidateArgs is where strictness is recovered whenever
// the server's schema does happen to compile.
func validateMCPSchema(raw json.RawMessage) error {
	if len(raw) > MaxMCPSchemaBytes {
		return specError()
	}
	if !isJSONObject(raw) {
		return specError()
	}
	// isJSONObject only inspects the opening token; a trailing-garbage or
	// truncated document must not reach the model as a tool definition.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return specError()
	}
	if decoder.More() {
		return specError()
	}
	return nil
}
