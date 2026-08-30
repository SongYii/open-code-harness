# Secret Redaction Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-30

**Scope:** [`SECURITY.md`](../../../SECURITY.md)'s "Not enforced" list states
plainly: "No secret redaction. Provider credentials live in adapter
configuration; event payloads and tool results are not scanned for
secrets before being persisted or emitted." This gate establishes what
this project's own code already does (§"What this project already has"),
and re-verifies six reference agent projects' actual secret-handling
mechanisms at their current, pinned state, ahead of designing a fix. It
does not design or implement anything.

English is normative. The Chinese file is a synchronized reading copy.

## Comparison set and pinned commits

Per Documentation rule 8, each was fetched with
`scripts/fetch-reference.sh <owner/repo> <sha>` into the gitignored
`.reference/` directory and read directly. Per Documentation rule 7, all
six were re-verified today: `kimi-code`, `grok-build`, `pi`, and
`deepseek-harness` had not moved since this repository's own MCP client
adapter gate re-verified them earlier today; `codex` and `maka-agent` had
each advanced one commit since then, re-fetched and re-checked out below
— diffed against the prior commit for the specific files this gate cites,
confirming no drift in their content.

| Project | Repository | Commit | Observed | Why chosen |
| --- | --- | --- | --- | --- |
| Codex | `openai/codex` | `94cbbdd` | 2026-08-30 | Rust; ships a purpose-built `RedactedString` type and a shell environment policy with default secret-name excludes |
| Kimi Code | `MoonshotAI/kimi-code` | `cbe0a77` | 2026-08-30 | TypeScript; the only project in this set with a real, general-purpose free-text secret-pattern redactor |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-30 | Rust; a second, independent instance of redact-by-construction on a credential-holding type |
| Maka | `maka-agent/maka-agent` | `5d519d6` | 2026-08-30 | TypeScript; a managed-secret injection system — checked as a candidate, ruled in as a *different* problem (§"Maka") |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb5` | 2026-08-30 | TypeScript; a file literally named `sanitize.ts` inside its terminal tool — checked directly, ruled out (§"DeepSeek Harness") |
| Pi | `earendil-works/pi` | `853a80d` | 2026-08-30 | TypeScript; checked directly per rule 7 rather than assumed absent from a prior gate's characterization |

## What this project already has

- **A narrow, real, tested precedent already exists, but it covers exactly
  one path.** `internal/harness/adapters/openaicompat/classify.go`'s
  `redactSecrets` runs four hardcoded regexes —
  `reAuthorization` (`Authorization: ...`), `reBearer` (`Bearer ...`),
  `reSecretKey` (`sk-[A-Za-z0-9_-]+`), and `reQueryKey` (`?key=...` /
  `&key=...`, preserving the parameter name) — and is called from
  `safeMessage`/`startupFailure` to build every
  `engine.ProviderFailure.SafeMessage`. This exists specifically because a
  provider's own HTTP error body can echo back request headers or an
  API-key-shaped substring, verified by `TestProviderFailureErrorNeverRendersSecrets`.
  It is never called anywhere else in this codebase.
- **The Provider API key itself is protected structurally, not by
  scanning — but only by convention, not by a compiler-enforced type.**
  `APIKeySource.APIKey()` (`openaicompat/model.go`) is called at request
  time and the returned value is injected directly into the outbound
  `Authorization` header; the `Model` struct never stores it past that
  call. The comment "Implementations must not log it" is a discipline
  rule enforced by code review, not a type system guarantee — nothing
  stops a future line of code from formatting an `APIKeySource` value
  into a log line the way it could with a plain `string`.
- **Tool call results flow verbatim through every hop, with no scanning
  anywhere.** `domain.CompleteToolCall.Content` / `ToolCallFailed.Message`
  (the exact bytes `read_file`, `list_dir`, and `exec` produced) are
  persisted as domain events unchanged, replicated into the JSONL audit
  trail unchanged, and projected onto ACP `session/update`
  `tool_call_update.content` unchanged — both on `session/load` replay
  (already true before this session) and, as of this session's own
  `internal/harness/adapters/acp/project.go` change — see the
  [conversation and session transcript evidence ledger](../../architecture/conversation-and-transcript-evidence.md)'s
  2026-08-30 update — on the **live** path too. That change closed a
  documented fidelity gap between live and replayed tool cards; it is
  also, honestly, a second projection call
  site that would need to apply whatever redaction this gate's design
  eventually adds — a fact this gate surfaces for that design, not a
  regression this gate is reporting.

## Per-project findings

### openai/codex — redact-by-construction for credential-holding types, denylist filtering before subprocess spawn

Three independent, narrower mechanisms, none of which scan arbitrary tool
output for secret-shaped substrings:

- **`RedactedString`** (`codex-rs/utils/redacted-string/src/lib.rs`): a
  newtype wrapper whose `Debug` implementation always prints
  `<redacted>`, regardless of the wrapped value. A value can only leak by
  an explicit `.into_inner()` call, not by an accidental derived
  `Debug`/log line — the compiler, not a reviewer, is what prevents the
  common mistake.
- **`ShellEnvironmentPolicy`**'s default excludes
  (`codex-rs/protocol/src/shell_environment.rs:125-130`): before a shell
  command spawns, any inherited environment variable whose name matches
  `*KEY*`, `*SECRET*`, or `*TOKEN*` (case-insensitive glob) is stripped,
  on top of whatever explicit inherit/exclude policy is configured — a
  denylist over a broad default inheritance.
- **`SanitizedGitUrl`** (`codex-rs/protocol/src/sanitized_git_url.rs`): a
  smart-constructor type that strips `user:token@`-shaped embedded
  credentials from a git remote URL at construction time, and explicitly
  rejects (does not merely strip) a remote-helper URL whose nested syntax
  could otherwise smuggle an arbitrary secret into an executable command
  line.

Each targets a specific, known data shape (a type Codex itself
constructs, an environment variable's name, a URL) — none scans free-form
text a tool actually produced.

### xai-org/grok-build — the same redact-by-construction pattern, independently arrived at

`AuthManager`'s hand-written `Debug` implementation
(`crates/codegen/xai-grok-shell/src/auth/manager.rs:188-196`) prints only
`AuthManager` with no fields, with a doc comment stating this exists "so
`AuthManager`... never leaks credentials into logs or panics." This is a
second, independent instance of exactly the type-level pattern Codex's
`RedactedString` represents — two unrelated Rust codebases converging on
"make the mistake structurally impossible for a type the code itself
owns," not on scanning content the code did not construct as a credential.

### MoonshotAI/kimi-code — the only free-text secret scanner found, scoped to its own logs only

`redactCtx` (`packages/agent-core-v2/src/_base/log/formatter.ts`) is a
genuinely general-purpose, two-layer redactor:

1. **Structured, key-name-based:** `REDACTED_KEYS` is a fixed,
   normalized set (`authorization`, `apikey`, `token`, `refreshtoken`,
   `accesstoken`, `idtoken`, `password`, `secret`, `clientsecret`,
   `apisecret`, `cookie`, `setcookie`, `bearer`). Walking an arbitrary
   structured log context object recursively, any key matching this set
   has its value replaced with `[REDACTED]`, with cycle detection
   (`WeakSet`) and a depth cap (`REDACT_MAX_DEPTH = 10`) so a malformed or
   cyclic context cannot hang or crash the logger.
2. **Free-text, regex-based:** `RAW_SECRET_PATTERNS` is a small regex set
   matching `key: value` / `key=value`-shaped substrings for
   `authorization: bearer ...`, `api_key=...` / `access_token=...` /
   `refresh_token=...` / `id_token=...` / `token=...` / `password=...` /
   `secret=...`, and `cookie=...`, replacing only the value while
   preserving the matched key/prefix.

Verified directly (`packages/agent-core/src/logging/logger.ts`,
`index.ts`) that `redactCtx` is called exclusively from Kimi Code's own
internal diagnostic-logging pipeline. It is never called on a tool call
result, an assistant message, or anything written to a persisted session
record or exposed to the model — the exact surfaces this project's own
`SECURITY.md` gap names.

### maka-agent/maka-agent — a different, adjacent problem: secure injection, not redaction

`ActivationSecretSink` / `ManagedSecretStore`
(`packages/storage/src/activation-secret-injector.ts`) solve "get a
credential safely **into** an isolated tool-execution environment": a
secret lives in a managed store, is referenced indirectly
(`ManagedSecretReference`), and is only materialized into a fresh,
isolated environment overlay immediately before a sandboxed activation —
explicitly never the host-wide `process.env`, so concurrent activations
cannot observe each other's values. This is the opposite direction from
this project's own gap (secrets flowing **out** through tool results and
event payloads, not credentials flowing safely in). Noted because a
design phase should not confuse the two problems: this project has no
"managed secrets for tools" concept today — the only credential in scope
is the Provider API key, already handled structurally (§"What this
project already has").

### deepseek-ai/deepseek-harness — a false lead, checked directly and ruled out

`packages/terminal/terminal-bash/src/sanitize.ts`'s `TerminalSanitizer`
strips ANSI/OSC/CSI terminal control sequences from PTY output for
line-oriented rendering (`normalizeTerminalText`, prompt-marker
detection). "Sanitize" here means terminal-escape-sequence stripping, an
entirely unrelated meaning from secret redaction. Read in full rather
than assumed relevant from the filename, matching this gate's own
standard of checking a lead directly before citing it.

### earendil-works/pi — no secret-redaction mechanism found, checked directly

The only "redact" hits in this codebase are a `redacted` field on
Anthropic-style "thinking" content blocks
(`packages/server/src/protocol.ts:42,266`) — a provider-specific meaning
(hidden/redacted extended-thinking content), unrelated to secret
scrubbing. No `REDACTED_KEYS`-equivalent structure and no secret-pattern
regex exist anywhere in `packages/agent`, `packages/coding-agent`, or
`packages/server`. A genuine, directly-checked negative finding, not an
absence assumed from a prior gate's unrelated characterization of this
project.

## Cross-cutting synthesis

- **Two independent projects (Codex, Grok Build) converge on
  redact-by-construction for credential-*holding* types**: suppress
  `Debug`/serialization at the type level so the common mistake — an
  accidental log line, a derived trait, a panic message — is prevented by
  the compiler, not by a reviewer remembering a rule. This project's own
  Provider API key gets an equivalent protection today only by
  convention (a code comment), not by a Go type that makes the mistake
  structurally impossible.
- **The actual gap this project's `SECURITY.md` names — scanning content
  a tool or the model *produces* (not a credential type the code itself
  holds) for secret-shaped substrings before it is persisted or emitted —
  has exactly one precedent in this six-project comparison set** (Kimi
  Code's `redactCtx` / `RAW_SECRET_PATTERNS`), and even that precedent is
  scoped only to internal diagnostic logs, never to tool output, session
  content, or anything model-visible or durably persisted. **No project
  in this comparison set solves the actual problem this gate was opened
  to research.** A design that follows this gate is not adopting an
  existing, converged industry pattern — it would be closing a gap that
  is open across this entire comparison set, not only in this project.
  Stated plainly here rather than implied to be a solved-elsewhere
  problem this project simply hasn't caught up on yet.
- **This project's own existing `redactSecrets` is the closest available
  precedent to extend, not a project outside this codebase.** Its
  mechanism (a small, hardcoded regex set, applied at the point a
  human/model-visible string is constructed) is structurally identical to
  half of Kimi Code's two-layer design (the free-text regex half); the
  other half (key-name-based structured redaction) may not transfer,
  since this project's tool results are plain strings, not structured log
  contexts (an open question below, not a finding).

## Open questions for the design that follows

- **Which surfaces get scanned**: tool call results
  (`domain.CompleteToolCall.Content` / `ToolCallFailed.Message`) only, or
  also model-generated assistant text, or also the raw arguments a model
  supplies to a tool call (a user could paste a secret directly into a
  prompt, which becomes a tool argument verbatim)?
- **Detection method**: extend this project's own existing hardcoded
  regex set (cheap, deterministic, zero new dependency, but only catches
  shapes it already knows — `sk-...`, bearer tokens, `?key=...`) versus a
  broader entropy-based heuristic (catches unknown-shape high-entropy
  strings — a raw hex/base64 credential with no recognizable prefix — at
  the cost of a real false-positive rate against legitimate high-entropy
  content a coding agent routinely reads and writes: git commit SHAs,
  base64-encoded binary content, UUIDs).
- **Where in the pipeline**: redact before the domain event is
  constructed and persisted (one place, upstream — but irreversible even
  for an operator who legitimately needs the original during an
  investigation) versus only at the ACP/export projection boundary
  (durable storage and audit stay intact, but every projection call site
  must remember to apply it — and the live-path change cited above is a
  fresh, concrete instance of "another emission surface appearing later"
  risk, since it added a second live-path call site alongside the
  pre-existing replay-path one).
- **Whether Kimi Code's key-name-based structured layer is relevant at
  all**: this project's tool results are plain strings today, not
  structured log contexts, so `REDACTED_KEYS`-style key matching may have
  no natural target here; the free-text regex half is the directly
  applicable precedent.
- **Whether the Provider API key path deserves a `RedactedString`-equivalent
  Go type** (compile-time redact-by-construction), independent of
  whatever is chosen for tool-result scanning — a smaller, more clearly
  scoped decision than the main one, following Codex's and Grok Build's
  converged pattern rather than this project's current
  code-review-discipline-only convention.
- **Interaction with this project's own existing size-bounding /
  truncation logic** (`toolTextContent`'s 16 KiB clip,
  `MaxToolResultBytes`): does redaction run before or after truncation,
  given a secret could straddle a truncation boundary and be only
  partially matched by a pattern on either side of it?
- **Durable storage encryption** (`SECURITY.md`'s neighboring "Durable
  storage is not encrypted" bullet) is a related but separate gap this
  gate does not research — redacting a secret before it is written and
  encrypting the store it is written to are independent defenses, and a
  design for one should not be assumed to substitute for the other.

## Evidence limits

- Every citation above traces to a specific pinned commit read in this
  session (table above); no claim is from memory or from a project's
  marketing page or README screenshots.
- This gate does not authorize copying any regex, type name, or key list
  verbatim from any of these five external projects — only the mechanisms
  and architectural choices they represent, exactly as every prior gate
  in this project's history already states for its own comparison set.
- The `RAW_SECRET_PATTERNS`/`REDACTED_KEYS` lists were read and
  summarized, not exhaustively reproduced here; a design phase that wants
  the complete list should re-read `formatter.ts` directly rather than
  treat this gate's summary as the full set.
- This gate searched each reference project with targeted, case-insensitive
  greps for "redact", "scrub", "secret", "mask", "sanitize", and
  variants, then read every match's surrounding context directly. A
  mechanism using vocabulary this search did not anticipate could exist
  unfound; this is a real, stated limit of a keyword-driven search, not a
  claim of exhaustive code review of six large codebases.
- "Current state" here means 2026-08-30. A future gate that revisits any
  of these projects must re-fetch and re-read per Documentation rule 7,
  rather than reuse this document's characterization.
- This gate does not choose a design. The next step is a normative design
  for secret redaction, informed by — not dictated by — the findings
  above.
