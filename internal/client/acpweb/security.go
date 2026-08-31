package acpweb

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// tokenBytes is the amount of entropy behind a per-invocation token: 32
// random bytes, hex-encoded to a 64-character string safe to carry as a
// URL query parameter.
const tokenBytes = 32

// GenerateToken returns a fresh, cryptographically random per-invocation
// token. It is never derived from a predictable seed (time, PID, or the
// process's own environment) — only crypto/rand.
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("acpweb: generate token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// ValidateToken reports whether got matches want, in constant time. A
// plain "==" comparison is never used here: this compares a value an
// attacker can supply against a secret this process generated, and an
// early-exit comparison would leak timing information about how much of
// the token an attacker has already guessed correctly.
func ValidateToken(want, got string) bool {
	if len(want) != len(got) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// CheckOrigin reports whether a WebSocket upgrade request's Origin header
// is acceptable. Its contract is narrower than "is this request safe": a
// present Origin must equal selfOrigin exactly (the bridge's own serving
// origin, e.g. "http://127.0.0.1:54321"), defending against a hostile page
// in another browser tab whose own Origin can never equal this one. An
// *absent* Origin header (no browser tab initiated this request at all, or
// a non-browser client) is accepted by this function alone — the token
// check in UpgradeAllowed is what still gates a request with no Origin.
func CheckOrigin(selfOrigin, requestOrigin string) bool {
	if requestOrigin == "" {
		return true
	}
	return requestOrigin == selfOrigin
}

// UpgradeAllowed runs both mandatory, independent checks a WebSocket
// upgrade must pass: the Origin check (CheckOrigin) and the per-invocation
// token check (ValidateToken). Neither substitutes for the other; both
// must pass. Callers must not expose which specific check failed on a
// deny — this function intentionally returns only a bool.
func UpgradeAllowed(selfOrigin, requestOrigin, wantToken, gotToken string) bool {
	return CheckOrigin(selfOrigin, requestOrigin) && ValidateToken(wantToken, gotToken)
}
