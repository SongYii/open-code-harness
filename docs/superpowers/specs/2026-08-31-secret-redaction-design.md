# Secret Redaction — Design

- **Date:** 2026-08-31
- **Status:** Accepted 2026-08-31.
- **Stability:** new surface; changes the *content* (not the shape) of
  three existing implemented contracts' string fields — see "Implemented
  contracts this slice must not change" below for the ones whose wire/DB
  shape stays untouched.
- **Repository:** `open-code-harness` (`github.com/SongYii/open-code-harness`)
- **Normative language:** English
- **Chinese summary:** [Secret 脱敏设计（中文摘要）](2026-08-31-secret-redaction-design.zh-CN.md)
- **Authority:** [Secret redaction architecture gate](../../research/architecture-gates/2026-08-30-secret-redaction.md)
- **Implemented contracts this slice must not change:** [Domain events and state machine](../../architecture/domain-events.md) (no new event fields, no new event types), [Tool runtime](../../architecture/tool-runtime.md) (no new tool, no new risk class, no new Policy rule), [ACP v1 adapter](../../architecture/acp-v1.md) (no new wire field; `content`/`rawInput` keep their existing shape — only the *bytes inside* an already-existing `Content`/`Text` string change)

English is normative. The Chinese file is a synchronized summary, not a
field-for-field translation.

---

## 1. Decision summary

The gate found this project's own `internal/harness/adapters/openaicompat/classify.go`'s
`redactSecrets` — four hardcoded regexes, applied to exactly one path
(`engine.ProviderFailure.SafeMessage`) — is the closest existing
precedent, and that no reference project among six studied solves the
actual gap `SECURITY.md` names: tool call results and event payloads
flow through this project's own domain events, JSONL audit replica, and
ACP `session/update` projections completely unscanned.

Six decisions, each resolving an open question the gate left open
(cross-referenced in §10):

1. **A new, dependency-free package, `internal/harness/redact`, replaces
   the private copy in `openaicompat`.** One implementation, one test
   suite, importable by any layer that needs it (`application`,
   `openaicompat`) without creating a new dependency edge, since it
   imports nothing beyond `regexp` from the standard library.
2. **Redact once, upstream, at the point a model-visible string is
   about to become a domain command — not at each of N downstream
   projection sites.** `application/pipeline.go`'s
   `completeToolAndContinue`/`failToolAndContinue` and
   `application/loop.go`'s `domain.CompleteAssistantMessage`/
   `CompleteAssistantTurn` construction sites redact `content`/`message`/
   `runResult.Text` once, before calling `domain.Decide`. The resulting
   redacted string is what gets persisted, audited, and projected live
   *and* replayed — one choke point protects every downstream consumer
   at once, directly addressing the gate's own finding that a fresh
   projection call site (this session's live-tool-card-fidelity fix) can
   appear without every future change remembering to re-apply redaction
   at yet another site.
3. **Scope is tool call results/failure messages and the final assistant
   message text — not tool call arguments, and not live `model.text.delta`
   streaming chunks.** Tool arguments double as the actual input driving
   a real workspace write or `exec` invocation; redacting that value
   before use would corrupt the tool's real effect, and redacting only a
   *display* copy while leaving the executed copy raw is a materially
   different, harder problem this slice does not solve (§2). Live text
   deltas arrive as arbitrary byte chunks before the model's full message
   is known; a secret could straddle a chunk boundary with no
   accumulation buffer to redact against, so this slice redacts the
   final assembled text once it is known and accepts that the live
   delta stream itself is not scanned (§6, an explicit, disclosed
   residual risk, not a silent gap).
4. **Detection is a small, hardcoded, shape-specific pattern set —
   extended from what this project already has, not an entropy-based
   heuristic.** The gate found no reference project solving this
   problem at all; adopting an unbounded heuristic now would trade a
   known, bounded false-negative rate (misses unknown-shape secrets) for
   an unbounded false-positive rate against exactly the kind of content
   a coding agent legitimately reads and writes (hex/base64 blobs, git
   SHAs, UUIDs) — a worse trade for a first slice (§4).
5. **Redaction runs before this project's own existing size-bounding /
   truncation logic, never after.** Because redaction now happens
   upstream at the full, untruncated string (decision 2), the gate's own
   worried-about failure mode — a secret straddling a *truncation*
   boundary and matching only a fragment of a pattern on either side —
   cannot occur: truncation (`toolTextContent`'s 16 KiB clip,
   `MaxToolResultBytes`) already runs downstream of this new redaction
   step, unchanged.
6. **No `RedactedString`-equivalent Go type for the Provider API key in
   this slice.** Codex and Grok Build's converged compile-time
   redact-by-construction pattern is real and worth adopting eventually,
   but the current convention-based protection (`APIKeySource.APIKey()`
   is called at request time only, never stored, never logged — verified
   directly against every call site) has held with zero reported
   incidents; introducing a wrapper type now is an independently
   scoped, smaller improvement this slice does not need in order to close
   the actual gap `SECURITY.md` names (tool results and event payloads,
   not the API key itself, which was never the unprotected surface).

## 2. Goals and non-goals

### Goals

- Close `SECURITY.md`'s "no secret redaction" gap for the two surfaces
  its own wording names as unscanned: tool call results (and failure
  messages) and event payloads — concretely, the final assistant message
  text, which is itself a persisted event payload.
- Consolidate this project's own existing, narrow `redactSecrets` into a
  single, shared, more broadly applicable implementation, rather than
  leaving a second, duplicated copy to drift.
- Protect every downstream consumer of a redacted value — the domain
  event itself, the JSONL audit replica, and both the live and replayed
  ACP `session/update` projections — from one upstream choke point,
  rather than requiring each to apply redaction independently.

### Non-goals (excluded from this slice, not deferred without a reason)

- **Tool call arguments (`rawInput`) are not redacted.** A tool's
  arguments are the actual input driving a real workspace write or
  `exec` invocation; this project cannot redact that value before using
  it without corrupting the tool's own effect (imagine redacting
  `write_file`'s `content` argument before writing — the file itself
  would be corrupted). Redacting only the *display* copy (what appears
  in `rawInput` on the wire and in the audit trail) while leaving the
  executed copy untouched is a legitimate future extension, but it is a
  second, independent scrubbing path this slice does not need to solve
  to close the gap `SECURITY.md` actually names (results, not arguments).
- **Live `model.text.delta` streaming chunks are not redacted.** They
  arrive as arbitrary byte fragments before the model's full message is
  assembled; a secret pattern could span two chunks with no buffer to
  redact against. This slice redacts the complete, assembled text once
  streaming finishes (`engine.RunResult.Text`) and before it becomes a
  domain event — the durable, audited, replayable record is fully
  redacted; only the transient live character-by-character stream is
  not. Accepted explicitly (§6), not silently dropped.
- **No entropy-based or ML-based secret detection.** Per decision 4 —
  a bounded, known false-negative rate against unknown-shape secrets is
  the accepted trade for this slice, not an oversight.
- **No `RedactedString`-equivalent Go type for the Provider API key**
  (decision 6) — a real, smaller, independently-scoped improvement,
  deferred rather than bundled into this slice.
- **No Resources/Prompts-style new domain concept, no new Policy rule,
  no new Risk class, no new ACP wire field.** This slice changes what
  bytes end up inside three existing string fields; it does not touch
  any contract's shape.
- **No durable-storage encryption.** `SECURITY.md`'s neighboring "not
  encrypted" bullet is a separate, independent defense this slice does
  not implement — redacting a secret before it is written and
  encrypting the store it is written to are not substitutes for each
  other.

## 3. Package and integration points

```
internal/harness/redact/   # new: Text(string) string, and its tests
```

A small, dependency-free leaf package (imports only `regexp` and
`strings` from the standard library), sitting alongside `domain`,
`policy`, and `tools` rather than inside any of them — nothing in this
project's existing `dependencies_test.go` forbids a new leaf package like
this from being imported by `application` (which already imports
`domain`/`engine`/`policy`/`tools`) or by an adapter such as
`openaicompat` (which already imports `engine`); it introduces exactly
one new node in the dependency graph, with no project-internal imports of
its own, so it cannot create a cycle.

**Call sites in `application`** (redact before constructing the domain
command, per decision 2):

- `pipeline.go`'s `completeToolAndContinue(ctx, owned, call, content, truncated)`:
  redact `content` once, at the top of the function, before both the
  `domain.CompleteToolCall{Content: ...}` construction and the
  `engine.RuntimePayload{..., Content: ...}` emit that follows it in the
  same function — one redaction, two protected consumers.
- `pipeline.go`'s `failToolAndContinue(ctx, owned, call, code, message)`:
  redact `message` once, symmetrically, before both
  `domain.FailToolCall{Message: ...}` and the
  `engine.RuntimePayload{..., Content: ...}` emit.
- `loop.go`'s `domain.CompleteAssistantMessage{..., Text: runResult.Text, ...}`
  (line 249) and `completeAssistantTurn`'s
  `domain.CompleteAssistantTurn{..., Text: runResult.Text, ...}` (line
  297): redact `runResult.Text` once at each site. `FailAssistantTurn`/
  `InterruptAssistantTurn`'s `Message` fields are excluded — verified
  directly that they are always built from `displayFailureSentence`'s
  fixed, code-keyed sentence table (`turn.go:466`), never from raw model
  or provider text, so there is nothing to redact there.

**Call site in `openaicompat`**: `classify.go`'s `redactSecrets` function
is deleted; `safeMessage`/`startupFailure` call `redact.Text` directly.
The existing `TestProviderFailureErrorNeverRendersSecrets` test moves
with it, updated for the new placeholder-based output (§4) rather than
the old empty-string replacement.

## 4. Detection method and pattern set

`redact.Text(s string) string` replaces every matched secret-shaped
substring's *value* with the literal marker `[redacted]`, preserving a
matched key name or header prefix where the pattern captures one (so
`Authorization: [redacted]` reads better than an empty string, and a
reader can tell redaction happened rather than wondering whether the
field was actually empty). A concrete, fixed, hardcoded pattern set —
extending, not replacing, what `redactSecrets` already covers:

| Pattern | Shape | Why included |
| --- | --- | --- |
| `Authorization` header | `Authorization: <anything>` | Already in `redactSecrets`; unchanged |
| `Bearer` token | `Bearer <token>` | Already in `redactSecrets`; unchanged |
| Provider-style secret key | `sk-`, `sk-ant-`, `sk-proj-` prefixes | Broadens `redactSecrets`'s existing `sk-` pattern to the prefix families in actual current use by OpenAI- and Anthropic-shaped keys |
| Query-string key | `?key=...` / `&key=...` | Already in `redactSecrets`; unchanged |
| Generic key/value assignment | case-insensitive `(key\|token\|secret\|password\|credential)\s*[:=]\s*<value>` | The single most common real shape a coding agent actually reads: a `.env` file, a shell profile export, or a config file's `API_KEY=...`/`password: ...` line. None of the fixed-prefix patterns above catch this, since a `.env` value rarely carries a recognizable prefix itself |
| AWS access key ID | `(AKIA\|ASIA)[0-9A-Z]{16}` | A fixed, fifteen-year-stable prefix with a near-zero false-positive rate; a coding agent working in a repository with AWS credentials (`.aws/credentials`, Terraform state, CI env dumps) is a realistic, common scenario |
| GitHub token | `gh[pousr]_[A-Za-z0-9]{36,}` / `github_pat_[A-Za-z0-9_]{22,}` | Same reasoning as AWS: fixed, well-known prefixes, near-zero false-positive rate, realistic in a coding agent's working set |
| PEM private key block | `-----BEGIN [A-Z ]*PRIVATE KEY-----` through the matching `END` line | SSH keys and TLS certificates are exactly the kind of file a workspace-scoped `read_file`/`exec` can expose; a multi-line block match, not a single-line pattern like the rest of this table |

The generic key/value pattern is the one genuine false-positive risk in
this table (§9): it requires an assignment operator (`:` or `=`)
immediately after a sensitive-looking key name, which ordinary prose or
a Go struct field named `Token` in running code does not usually produce
(`Token string` has no `:`/`=` in that position) — but a config file,
comment, or string literal that happens to look like an assignment could
still trigger it. Accepted as the correct trade for catching the most
common real-world secret shape (§3's table), not an oversight.

This list is deliberately small and shape-specific — it is not a general
secret-scanning product, and the gate already established that no
reference project's own tool-output path does better than this. A future
slice may extend the pattern set; this design does not freeze it as
exhaustive.

## 5. Behavior change to the existing narrow precedent

`redactSecrets`'s current behavior for `Authorization`/`Bearer`/`sk-`
matches is to replace the whole match with an empty string; `reQueryKey`
keeps the parameter name via a capture group. `redact.Text` standardizes
on the `[redacted]` marker for every pattern in §4's table instead,
including the ones inherited from `redactSecrets` — a deliberate,
disclosed behavior change (not a silent one) made *because* this is now
a shared, more heavily-used function: a reader of a redacted tool result
or provider error should be able to tell "a secret was here and removed"
apart from "this field was legitimately empty," which an empty-string
replacement cannot distinguish. `TestProviderFailureErrorNeverRendersSecrets`
is updated at implementation time to assert the new marker, not the old
empty-string behavior.

## 6. Live streaming residual risk

Stated plainly, not buried: a secret that appears only in the live
`model.text.delta` stream — and never in the model's final assembled
text, a tool result, or a failure message — is not caught by this
design. This is a narrower, transient exposure than the gap
`SECURITY.md` names (durable persistence and replay), and this slice
accepts it explicitly rather than attempting a streaming-chunk
accumulation-and-redaction buffer that no part of this project's engine
layer has today and that this slice's actual goal (closing the durable
persistence/replay gap) does not require. A future design that wants to
close this narrower remaining gap would need to design that buffer
first; this slice does not sketch one.

## 7. Verification and acceptance

- Unit tests for `redact.Text` covering every pattern in §4's table
  independently, plus: a string with no secret-shaped content passes
  through byte-for-byte unchanged; two distinct secrets in one string
  both get redacted; a secret embedded in otherwise-legitimate content
  (e.g. a `.env`-shaped line inside a larger file read) is redacted
  without corrupting the surrounding text; the known false-positive case
  named in §4 (a generic key/value-shaped match against non-secret
  content) is demonstrated and accepted, not hidden.
- `application`-level tests (extending this session's own
  `TestRuntimeToolExecutionCompletedCarriesResultContent`-style pattern,
  which already exercises a real `RunTurn` rather than a synthetic
  `RuntimeEvent`): a tool result containing a recognizable secret shape
  is redacted in the domain event `Content`, in the emitted
  `RuntimeEvent.Content`, and (via a second assertion against the ACP
  projection layer) in both the live and replayed `session/update`
  `content` field — proving decision 2's "one choke point protects every
  downstream consumer" claim is actually true, not merely designed.
- A mutation check at implementation time: reverting the redaction call
  at each of the four sites named in §3 independently, confirming each
  reversion fails the corresponding test for the right reason before
  being restored — matching this project's own established rigor for
  every prior security-relevant slice (exec sandboxing, the ACP-native
  client's permission handling).
- `openaicompat`'s existing `TestProviderFailureErrorNeverRendersSecrets`
  continues to pass against the new shared `redact.Text`, updated for
  the `[redacted]` marker (§5).

## 8. Risks

| Risk | Mitigation |
| --- | --- |
| The generic key/value pattern (§4) produces a false positive against legitimate content (a config file's comment, a string literal that looks like an assignment). | Accepted explicitly (§4, §9) as the correct trade for catching the most common real-world secret shape; a reader sees `[redacted]` rather than silently losing content to a `""` replacement, making a false positive visible and reportable. |
| An unknown-shape secret (no recognizable prefix, not a key/value assignment) is never caught. | Decision 4's accepted trade — a bounded, known false-negative rate rather than an unbounded false-positive rate from entropy-based detection; stated as a real limit, not implied to be solved. |
| The live `model.text.delta` stream still exposes a secret transiently even after this slice ships. | §6, disclosed explicitly as a residual risk narrower than the gap this slice closes, not silently left as an unstated gap. |
| A future new projection or persistence call site (like this session's own live-tool-card-fidelity change) is added without going through the same upstream-redacted value. | Decision 2's placement — redact once, upstream, before the domain command is constructed — means any future consumer of that same domain event or `RuntimeEvent` field automatically receives the already-redacted value; the risk only reappears if a future change introduces a *new*, separate raw-string source bypassing `application`'s existing construction sites entirely, which would itself be a new decision this design's own §3 integration points do not anticipate. |
| Migrating `openaicompat.redactSecrets`'s callers to the shared package's new `[redacted]`-marker behavior (§5) changes an already-shipped, tested contract's observable output. | `TestProviderFailureErrorNeverRendersSecrets`'s assertion changes with it, at implementation time, in the same commit — a disclosed, intentional behavior change, not an accidental regression. |

## 9. Known false-positive example (illustrative, not exhaustive)

A workspace file containing the line `# TODO: rotate the API secret =
soon` would match the generic key/value pattern (§4) and redact "soon"
even though nothing sensitive is present. This is the accepted cost of
catching real `.env`-shaped secrets, named here explicitly rather than
discovered later as a surprise.

## 10. How this design answers the gate's open questions

Cross-referencing `2026-08-30-secret-redaction.md`'s "Open questions for
the design that follows" directly:

1. *Which surfaces get scanned* → §1.3/§2: tool call results/failure
   messages and the final assistant message text; not tool arguments,
   not live text deltas (both named non-goals with reasons).
2. *Detection method: hardcoded regex vs. entropy heuristic* → §1.4/§4:
   hardcoded, shape-specific pattern set, extended from this project's
   own existing precedent.
3. *Where in the pipeline* → §1.2/§1.5: upstream, once, before the
   domain command is constructed — which also resolves the
   truncation-ordering question as a direct consequence, not a separate
   decision.
4. *Whether Kimi Code's key-name structured layer is relevant* → §4:
   not as a separate structured layer (this project's tool results are
   plain strings), but its free-text regex half is directly reflected in
   the generic key/value pattern.
5. *Whether the Provider API key deserves a `RedactedString`-equivalent
   type* → §1.6: not in this slice, with a stated reason.
6. *Interaction with existing truncation/bounding logic* → §1.5: redact
   before truncation, always, by construction of where redaction now
   runs.
7. *Durable storage encryption* → §2: explicitly out of scope, an
   independent defense this slice does not implement or substitute for.

Unlike the MCP client adapter design, this slice is not deferred: it
closes a real, currently-live gap (tool results and event payloads flow
through this project's own already-shipped Provider adapter and ACP
surfaces unscanned today, not a capability nothing yet depends on), and
its own §2 goals are narrow enough to plan directly. The next artifact is
an implementation plan against this design, not a deferred "accepted, not
planned" record.
