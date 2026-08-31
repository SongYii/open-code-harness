// Package redact removes secret-shaped substrings from text before it is
// persisted or displayed. It is a small, hardcoded, shape-specific pattern
// match — not a general secret scanner and not an entropy-based heuristic —
// per docs/superpowers/specs/2026-08-31-secret-redaction-design.md.
package redact

import "regexp"

// marker replaces every matched secret's value. It is a visible token
// rather than an empty string so a reader can tell "a secret was here and
// removed" apart from "this field was legitimately empty".
const marker = "[redacted]"

// pattern pairs a regular expression with the replacement template applied
// to each of its matches.
type pattern struct {
	re          *regexp.Regexp
	replacement string
}

// patterns is applied in order. Each entry targets one known secret shape;
// see the design doc for why each was chosen and what it deliberately does
// not catch.
var patterns = []pattern{
	// Authorization header: the value is the rest of the line, since a
	// real header value never carries trailing, unrelated prose.
	{regexp.MustCompile(`(?i)(Authorization\s*[:=]\s*)\S.*`), "${1}" + marker},
	// Bearer token: a single token, since "Bearer" can appear inside a
	// sentence with unrelated trailing words.
	{regexp.MustCompile(`(?i)Bearer\s+\S+`), marker},
	// Provider-style secret keys (OpenAI/Anthropic-shaped prefixes).
	{regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]+`), marker},
	// A generic key/token/secret/password/credential assignment, the
	// shape a .env file or shell export most commonly uses. Requires the
	// sensitive word to be either a standalone identifier or the tail of
	// an underscore-separated one (API_KEY, DB_PASSWORD), so an ordinary
	// word that merely contains the substring (monkey) does not match.
	// Runs before the narrower query-string pattern below since both can
	// match a bare "key=" shape; the capture group includes the
	// separator itself so nothing is silently dropped from the
	// replacement, and running this one first makes the narrower pattern
	// an idempotent no-op on its output rather than a second, corrupting
	// pass over it.
	// A dedicated `?key=`/`&key=` query-string pattern is not needed
	// separately: "key" is already one of this pattern's trigger words,
	// "?"/"&" satisfy its leading word boundary, and the value matcher
	// already stops at "&" — confirmed by mutation testing that a
	// standalone query-string pattern never actually fired for either of
	// its own dedicated tests once this one existed.
	{regexp.MustCompile(`(?i)\b((?:[A-Za-z0-9]+_)?(?:key|token|secret|password|credential)\s*[:=]\s*)[^\s&]+`), "${1}" + marker},
	// AWS access key IDs: a fixed, long-stable prefix with a near-zero
	// false-positive rate.
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), marker},
	// GitHub tokens: fixed, well-known prefixes.
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`), marker},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}\b`), marker},
	// PEM private key blocks, matched as one block rather than line by
	// line since the encoded key material spans many lines.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), marker},
}

// Text replaces every recognized secret-shaped substring in s with a
// [redacted] marker, preserving a matched key name or header prefix where
// the pattern captures one. It is deterministic and side-effect-free.
func Text(s string) string {
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.replacement)
	}
	return s
}
