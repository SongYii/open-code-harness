# Secret Redaction — Implemented Contract

**Status:** Implemented; not GA

**Stability:** `internal` — a Go package and application-layer behavior, not a public wire contract

**Maturity:** pre-v0, not a general availability release

**Authority:** [Secret redaction design](../superpowers/specs/2026-08-31-secret-redaction-design.md)

**Evidence:** [Secret redaction completion evidence](secret-redaction-evidence.md)

**Packages:** `internal/harness/redact` (the pattern set); `internal/harness/application` (the two call sites); `internal/harness/adapters/openaicompat` (the consolidated caller)

This document records behavior enforced by the current code and tests. It
is an internal Go contract, not a stable public protocol.

## Scope

This contract closes the gap [`SECURITY.md`](../../SECURITY.md) named:
tool call results, tool call failure messages, and the model's final
assistant message text are scanned for a small set of secret-shaped
substrings and redacted before they are persisted, replicated to the
JSONL audit trail, or projected onto ACP `session/update` — live or
replayed. It is a hardcoded, shape-specific pattern match, not an
entropy-based heuristic and not an exhaustive secret scanner; the
[architecture gate](../research/architecture-gates/2026-08-30-secret-redaction.md)
that preceded this contract found no reference project among six studied
solves this exact problem either.

## `internal/harness/redact`

`Text(s string) string` (`redact.go`) applies a fixed, ordered list of
patterns, replacing each matched secret's *value* with the literal marker
`[redacted]` — never an empty string, so a reader can tell "a secret was
here and removed" apart from "this field was legitimately empty." A
pattern that captures a key name or header prefix preserves it; only the
value becomes the marker (`TestTextRedactsAuthorizationHeader`,
`TestTextRedactsGenericKeyValueAssignment`).

| Shape | Pattern behavior | Test |
| --- | --- | --- |
| `Authorization` header | The rest of the line becomes `[redacted]`; the label is preserved | `TestTextRedactsAuthorizationHeader` |
| `Bearer` token | A single token becomes `[redacted]` | `TestTextRedactsBearerToken` |
| Provider-style secret key | `sk-`, `sk-ant-`, `sk-proj-` prefixes | `TestTextRedactsProviderStyleSecretKeys` |
| Generic key/value assignment | Case-insensitive `key`/`token`/`secret`/`password`/`credential`, standalone or as the tail of an underscore-joined identifier (`API_KEY`), followed by `:`/`=` | `TestTextRedactsGenericKeyValueAssignment` |
| AWS access key ID | `AKIA`/`ASIA` + 16 alphanumerics | `TestTextRedactsAWSAccessKeyID` |
| GitHub token | `gh[pousr]_...`, `github_pat_...` | `TestTextRedactsGitHubTokens` |
| PEM private key block | `-----BEGIN ... PRIVATE KEY-----` through the matching `END` line, one match spanning the whole block | `TestTextRedactsPEMPrivateKeyBlockAsOneMatch` |

A dedicated `?key=`/`&key=` query-string pattern was planned but dropped
during implementation: once the generic key/value pattern's value matcher
was fixed to also stop at `&`, a standalone query-string rule never fired
for either of its own dedicated tests — mutation testing caught it as
dead weight in a security-critical package, not a coverage gain
(`TestTextRedactsQueryStringKeyPreservingParameterName` and
`TestTextRedactsQueryStringKeyAfterAmpersand` are both satisfied by the
generic pattern alone). Every remaining pattern is independently
mutation-verified: disabling it makes its own dedicated test fail for the
right reason.

`redact.Text` is idempotent on already-redacted output: re-running it on
a string it has already processed never changes that string further,
since a `[redacted]` marker (or a preserved label followed by one) never
itself matches any pattern in the set. This property is what
`openaicompat`'s own test helpers use to assert "no unredacted secret
shape remains" without hardcoding which labels are expected to survive
(§"Consolidation" below).

## Where redaction runs

Redaction runs once, upstream, at the point a model-visible string is
about to become a domain command — never downstream at a projection or
export boundary, and never after this project's own existing
size-bounding/truncation logic, so a secret can never straddle a
truncation boundary from redaction's perspective (truncation always sees
already-redacted content).

- **`internal/harness/application/pipeline.go`**: `completeToolAndContinue`
  redacts `content` once, before both `domain.CompleteToolCall{Content:
  ...}` and the `engine.RuntimePayload{..., Content: ...}` emit that
  follows in the same function. `failToolAndContinue` redacts `message`
  symmetrically, before both `domain.FailToolCall{Message: ...}` and its
  own `RuntimePayload` emit. One redacted value protects the persisted
  domain event, the JSONL audit replica, and both the live and replayed
  ACP projections of that same tool call's result or failure text.
- **`internal/harness/application/loop.go`**: `runResult.Text` is
  redacted at both places it becomes a domain command —
  `runStepLoop`'s intermediate `domain.CompleteAssistantMessage` (a step
  with tool call offers) and `completeAssistantTurn`'s
  `domain.CompleteAssistantTurn` (the turn's final text).
  `completeAssistantTurn`'s redacted copy also flows into
  `commitTerminalAppend`'s `text` parameter, which becomes the
  caller-visible `RunTurnResult.Text` (`owned.result.Text = text`) — a
  second leak surface distinct from the domain event, found and closed
  during implementation, not assumed away.

`FailAssistantTurn`/`InterruptAssistantTurn`'s `Message`/`Code` fields are
never redaction targets: verified directly that `durableFailure`
(`turn.go`) always returns `displayFailureSentence`'s fixed, code-keyed
sentence, discarding even a real `engine.ProviderFailure`'s own
(already-redacted, see below) `SafeMessage` entirely and keeping only its
`Code`. No tool-call argument and no live `model.text.delta` streaming
chunk is ever redacted (see Exclusions).

## Consolidation with the Provider adapter's existing precedent

`internal/harness/adapters/openaicompat` had its own narrow,
pre-existing redaction — four hardcoded regexes applied to exactly one
path, `engine.ProviderFailure.SafeMessage` (a provider HTTP failure
body). That private implementation is deleted; `safeMessage` and
`startupFailure` call `redact.Text` directly. This is a disclosed
behavior change: the old implementation replaced a matched secret with an
empty string, so a test that hardcoded "`Authorization`/`Bearer
`/`sk-` must never appear anywhere in classified or persisted text" broke
against the new marker-preserving behavior (`"Authorization:
[redacted]"` legitimately contains the word `Authorization`). Both of
this directory's shared `assertNoSecrets` test helpers (one per test
package, `openaicompat` and `openaicompat_test`) were rewritten to assert
`redact.Text` is idempotent on the text instead — a stronger,
pattern-set-agnostic invariant that does not hardcode which labels
survive.

## Exclusions

This implemented contract does not provide:

- **Tool call argument redaction.** A tool's arguments are the actual
  input driving a real workspace write or `exec` invocation; redacting
  that value before use would corrupt the tool's own effect. Redacting
  only a *display* copy (`rawInput` on the ACP wire, the audit trail)
  while leaving the executed copy untouched is a legitimate future
  extension this contract does not implement.
- **Live `model.text.delta` streaming redaction.** Deltas arrive as
  arbitrary byte fragments before the model's full message is assembled;
  a secret could span two chunks with no accumulation buffer to redact
  against. Only the complete, assembled text (`engine.RunResult.Text`) is
  redacted, once, before it becomes a domain event.
- **Entropy-based or ML-based secret detection.** A bounded, known
  false-negative rate against unknown-shape secrets (no recognizable
  prefix) is the accepted trade against an unbounded false-positive rate
  a heuristic would produce against this project's own legitimate
  high-entropy content (git SHAs, base64 blobs, UUIDs).
- **A compile-time redact-by-construction type for the Provider API
  key** (a `RedactedString`-equivalent Go type). The key is protected
  structurally today — `APIKeySource.APIKey()` is called at request time
  only and never stored past that call — but by code-review convention,
  not a type the compiler enforces.
- **Durable storage encryption.** A separate, independent defense
  (`SECURITY.md`'s neighboring "not encrypted" bullet); redacting a
  secret before it is written and encrypting the store it is written to
  are not substitutes for each other.
