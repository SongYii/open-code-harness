package mcp

import (
	"regexp"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// legalQualifiedName is what every qualified name must look like once both
// parts have been sanitized: the fixed prefix, then only characters the
// Catalog and any downstream consumer can carry without escaping.
var legalQualifiedName = regexp.MustCompile(`^mcp_[a-zA-Z0-9_-]+$`)

// TestQualifyToolNameIsInjectiveAcrossTheSeparator is the counterexample the
// design's 2026-09-04 amendment records.
//
// The original design claimed a collision "can only happen via a server.Name
// collision, since raw tool names are always prefixed". A naive
// `mcp__<server>__<tool>` join is not injective: the separator can appear
// inside either part. Raw tool names come from the MCP server, which the
// design's own threat model treats as untrusted, and NewCatalog fails closed
// at composition.Open — so a hostile server picking a colliding name stops
// the harness from starting at all.
func TestQualifyToolNameIsInjectiveAcrossTheSeparator(t *testing.T) {
	first := QualifyToolName("a", "b__c")
	second := QualifyToolName("a__b", "c")
	if first == second {
		t.Fatalf("both qualified to %q; the separator must not survive inside a part", first)
	}
}

// TestQualifyToolNameSeparatesDistinctPairs covers the general property the
// case above is one instance of: distinct (server, tool) pairs must not
// collide, including pairs that differ only in where a separator-shaped run
// of underscores falls.
func TestQualifyToolNameSeparatesDistinctPairs(t *testing.T) {
	pairs := [][2]string{
		{"a", "b__c"}, {"a__b", "c"}, {"a_b", "c"}, {"a", "b_c"},
		{"a", "b-c"}, {"a-b", "c"}, {"srv", "tool"}, {"srv_", "tool"},
	}
	seen := map[string][2]string{}
	for _, pair := range pairs {
		name := QualifyToolName(pair[0], pair[1])
		if previous, clash := seen[name]; clash {
			t.Errorf("%v and %v both qualified to %q", previous, pair, name)
			continue
		}
		seen[name] = pair
	}
}

// TestQualifyToolNameSanitizesHostileCharacters proves the name a server
// supplies cannot carry anything but the sanitized alphabet into the
// Catalog, a log line, or a rendered prompt. tools.validateSpec rejects
// leading and trailing whitespace but not an interior newline, so
// sanitization here is the only thing standing between a server-chosen
// string and everything downstream.
func TestQualifyToolNameSanitizesHostileCharacters(t *testing.T) {
	for _, raw := range []string{
		"drop table", "a/b", "a\nb", "a\tb", "weißbier", "a..b",
		"../../etc/passwd", "a\x00b", "‮evil", "  padded  ",
	} {
		got := QualifyToolName("srv", raw)
		if !legalQualifiedName.MatchString(got) {
			t.Errorf("QualifyToolName(%q, %q) = %q, which is not sanitized", "srv", raw, got)
		}
	}
}

// TestQualifyToolNameCapsLengthWithAStableSuffix keeps an externally-supplied
// name from growing without bound, and keeps truncation from turning two
// distinct names into one.
func TestQualifyToolNameCapsLengthWithAStableSuffix(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := QualifyToolName("srv", long)
	if len(got) > MaxQualifiedNameBytes {
		t.Fatalf("len(%q) = %d, want <= %d", got, len(got), MaxQualifiedNameBytes)
	}
	if again := QualifyToolName("srv", long); got != again {
		t.Fatalf("qualification is not deterministic: %q then %q", got, again)
	}
	if other := QualifyToolName("srv", long+"y"); got == other {
		t.Fatalf("two distinct long names both truncated to %q", got)
	}
	if !legalQualifiedName.MatchString(got) {
		t.Fatalf("truncated name %q is not legal", got)
	}
}

// TestQualifyToolNameNeverReturnsAnEmptyOrDegenerateName covers a server that
// offers a name consisting only of characters sanitization removes. The
// result must still be a usable, distinct Catalog name rather than the bare
// prefix.
func TestQualifyToolNameNeverReturnsAnEmptyOrDegenerateName(t *testing.T) {
	first := QualifyToolName("srv", "///")
	second := QualifyToolName("srv", "\n\n")
	for _, name := range []string{first, second} {
		if !legalQualifiedName.MatchString(name) {
			t.Fatalf("%q is not a legal qualified name", name)
		}
	}
	if first == second {
		t.Fatalf("two distinct unsanitizable names both became %q", first)
	}
}

// TestQualifiedNamesAreCatalogLegal closes the loop: whatever a server sends,
// the qualified result must pass the real Catalog's own validation rather
// than merely look plausible.
func TestQualifiedNamesAreCatalogLegal(t *testing.T) {
	raws := []string{"  spaced  ", "a\nb", strings.Repeat("z", 300), "///", "ok_tool"}
	specs := make([]domain.ToolSpec, 0, len(raws))
	for _, raw := range raws {
		specs = append(specs, domain.ToolSpec{
			Name:        QualifyToolName("srv", raw),
			Description: "fixture",
			// This project's compiler requires an object schema to close
			// extra fields; a bare {"type":"object"} is rejected. See
			// tools.compileObjectKeywords.
			InputSchema: []byte(`{"type":"object","additionalProperties":false,"properties":{}}`),
			Source:      tools.SourceMCP,
			Risk:        domain.RiskExec,
			Mutates:     true,
		})
	}
	if _, err := tools.NewCatalog(specs); err != nil {
		t.Fatalf("NewCatalog rejected qualified MCP names: %v", err)
	}
}
