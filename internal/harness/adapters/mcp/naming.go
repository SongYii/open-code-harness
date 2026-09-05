// Package mcp is this project's MCP client adapter: it discovers tools from
// configured MCP servers and projects them into the same domain.ToolSpec type,
// tools.Catalog, Policy table, Approver slot, and audit trail the four builtin
// workspace tools already flow through.
//
// It never imports a sibling adapter. OS-level confinement for a server
// subprocess arrives through a port this package declares and
// internal/harness/composition fills — see the design's 2026-09-04 amendment
// to §3, which resolved a direct contradiction between that import rule and
// the requirement to reuse localexec's confinement.
package mcp

import (
	"strings"
	"unicode"
)

const (
	// namePrefix marks every tool this adapter contributes to the Catalog.
	namePrefix = "mcp"

	// nameSeparator joins the prefix, server, and tool parts.
	//
	// Underscore is deliberately *excluded* from the sanitized part alphabet
	// (see sanitizeNamePart), which is the whole mechanism that makes the
	// join injective. The design's §5 amendment proposed sanitizing to
	// `[a-zA-Z0-9_-]`, and that does not work: it leaves the separator
	// inside the alphabet, so `a` + `b__c` and `a__b` + `c` still land on
	// the same qualified name. Reserving one character for the separator is
	// what actually closes it.
	nameSeparator = "_"

	// MaxQualifiedNameBytes bounds a qualified tool name. The name is chosen
	// by an operator's server configuration and by the server itself, so it
	// is externally supplied input and gets a stated bound like every other
	// externally supplied value in this project.
	MaxQualifiedNameBytes = 64

	// suffixHexDigits is the width of the disambiguating hash appended when a
	// qualified name would exceed the bound. Eight hex digits of FNV-1a,
	// following Kimi Code's tool-naming convention rather than inventing one.
	suffixHexDigits = 8
)

// QualifyToolName converts a configured server name and a server-supplied raw
// tool name into one Catalog-legal, collision-resistant tool name.
//
// Both parts are untrusted to different degrees: the server name comes from
// operator configuration, the raw tool name comes from the MCP server itself,
// which the design's threat model does not trust. Neither may reach the
// Catalog, a log line, or a rendered prompt unsanitized — tools.validateSpec
// rejects leading and trailing whitespace but not an interior newline, so it
// is not a sufficient defense on its own.
//
// The result is deterministic: the same pair always qualifies to the same
// name, which is what lets a Catalog built at composition.Open be compared
// against one built later.
func QualifyToolName(server, rawTool string) string {
	qualified := strings.Join(
		[]string{namePrefix, sanitizeNamePart(server), sanitizeNamePart(rawTool)},
		nameSeparator,
	)
	if len(qualified) <= MaxQualifiedNameBytes {
		return qualified
	}
	// Truncating alone would map two distinct long names onto one, so the
	// hash is taken over the untruncated name and carried in the suffix.
	suffix := nameSeparator + fnv1aHex(qualified)
	keep := MaxQualifiedNameBytes - len(suffix)
	return qualified[:keep] + suffix
}

// sanitizeNamePart reduces one part to `[a-zA-Z0-9-]` — ASCII letters,
// digits, and hyphen, and deliberately not underscore, which is reserved as
// the separator.
//
// Sanitization is lossy, so on its own it would create collisions of its own
// making: `a/b` and `a.b` both reduce to `a-b`. A part that sanitization
// actually altered therefore carries a hash of its original, which keeps
// distinct originals distinct without making ordinary names unreadable — an
// already-legal name passes through untouched, and a tool name reaching the
// model is worth keeping legible.
//
// A part with nothing left after sanitization becomes the hash alone, so a
// qualified name never degenerates toward the bare prefix and two different
// unsanitizable parts still differ.
//
// One residual edge, stated rather than hidden: a raw name that is already
// legal and happens to look like `<something>-<8 hex digits>` occupies the
// same shape a lossy encoding produces. Reaching an actual collision needs
// that coincidence *and* an FNV-1a collision on the other name. This is
// disambiguation against a hostile startup denial of service, not
// authentication, so the residual case is accepted and recorded.
func sanitizeNamePart(part string) string {
	var builder strings.Builder
	builder.Grow(len(part))
	lastWasHyphen := false
	for _, r := range part {
		switch {
		case r == '-' || unicode.IsDigit(r) || (r < unicode.MaxASCII && unicode.IsLetter(r)):
			builder.WriteRune(r)
			lastWasHyphen = r == '-'
		case lastWasHyphen:
			// Collapse the run; nothing to write.
		default:
			builder.WriteByte('-')
			lastWasHyphen = true
		}
	}
	cleaned := strings.Trim(builder.String(), "-")
	switch {
	case cleaned == "":
		return fnv1aHex(part)
	case cleaned == part:
		return cleaned
	default:
		return cleaned + "-" + fnv1aHex(part)
	}
}

// fnv1aHex is the 32-bit FNV-1a of s as fixed-width lowercase hex.
//
// Not a cryptographic choice and not one: this disambiguates truncated names,
// it does not authenticate them. FNV-1a is used because Kimi Code's own
// convention uses it and because a stable, dependency-free, fixed-width digest
// is exactly what the suffix needs.
func fnv1aHex(s string) string {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	hash := uint32(offset32)
	for i := 0; i < len(s); i++ {
		hash ^= uint32(s[i])
		hash *= prime32
	}
	const hexDigits = "0123456789abcdef"
	out := make([]byte, suffixHexDigits)
	for i := suffixHexDigits - 1; i >= 0; i-- {
		out[i] = hexDigits[hash&0xf]
		hash >>= 4
	}
	return string(out)
}
