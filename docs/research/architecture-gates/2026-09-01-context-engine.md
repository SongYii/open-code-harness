# Context Engine Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-09-01

**Scope:** Milestone 8 in `docs/README.md` — the Context Engine (selection,
budgeting, and compression of model-visible context) — remains undesigned.
The [2026-09-01 roadmap gate](2026-09-01-context-engine-evaluation-observability-tui.md)
surveyed Context Engine at the same shallow depth as three other
under-designed milestones and explicitly said it "still needs its own
subsystem-specific architecture gate re-verifying then-current primary
sources before a normative design." This is that gate: a single-subsystem
deep dive into how the six reference projects' compaction mechanisms
actually work — trigger math, boundary-finding, summarization mechanics,
persistence shape, failure semantics, and configuration — matched against
what this project's own code already has and does not have. It does not
design or implement anything. The next step after this gate is a normative
design (`docs/superpowers/specs/`) per Documentation rule 1, not an
implementation plan directly.

English is normative. The Chinese file is a synchronized reading copy.

## Comparison set and pinned commits

Per Documentation rule 8, these are the same gitignored `.reference/`
checkouts obtained via `./scripts/fetch-reference.sh --list`. Per
Documentation rule 7: a re-fetch was considered and judged unnecessary —
these are the identical commits the 2026-09-01 roadmap gate read hours
earlier the same day, which itself re-verified they matched the
2026-08-31 web-trajectory-ui gate's fetch. Re-fetching again within the
same day for the same six-project set would not surface new state; this
gate instead spent its effort reading further into files the roadmap gate
only opened at the survey level.

| Project | Repository | Commit | Observed | Why fetched |
| --- | --- | --- | --- | --- |
| Pi (agent core source) | `badlogic/pi-mono` | `853a80d` | 2026-08-28 | `packages/agent/src/harness/compaction/` and `session/context.ts` — read line-by-line for trigger, cut-point, and persistence mechanics |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb5` | 2026-08-30 | `packages/compaction/compaction-basic/src/{region,summarizer}.ts` — read for its full transaction/lock/stability-check machinery, not just its existence |
| Kimi Code | `MoonshotAI/kimi-code` | `8f2c60b` | 2026-08-31 | `packages/agent-core-v2/src/agent/fullCompaction/` — read for trigger ratios, tool-pairing cut logic, and turn-mutual-exclusion handling |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-28 | `crates/common/xai-grok-compaction/src/{intra,inter,code}_compaction/` — read for exact trigger formula, error philosophy, and the `xai-grok-shell` two-pass prefire cache |
| Codex | `openai/codex` | `a9519cb` | 2026-08-31 | `codex-rs/core/src/compact*.rs` — read for pre/post-compact hooks, the token-budget strategy's actual mechanism, and remote-v2's retained-token budget |
| Maka | `maka-agent/maka-agent` | `ef94235` | 2026-08-31 | `packages/runtime/src/{history-compaction,compaction-boundary,context-budget-policy}.ts` — read for its three-mechanism budget policy and explicit fail-open replay logic |

## What this project already has

This project has no compaction mechanism at all today — no domain command,
no domain event, no budget concept — but it is not starting from nothing.
The following facts are the concrete baseline any future design has to
build against.

- **The context sent to the model is already unbounded, and the exact
  place that would have to change is `projectPriorTurns`.**
  `internal/harness/application/loop.go:66`
  (`func projectPriorTurns(records []domain.RecordedEvent, current
  domain.TurnID) []domain.ModelPromptMessage`) folds **every** committed
  event from every earlier turn into the model's prompt messages, with no
  budget, truncation, or summarization — its own doc comment
  (`loop.go:62-65`) states plainly: "the compact Session aggregate
  discards completed turns; the event log is the authority." It is called
  from `runAfterAdmission` at `loop.go:191`
  (`owned.projection = newTurnProjectionWithPrefix(projectPriorTurns(records,
  result.TurnID), request.Input)`), itself reached from `RunTurn`
  (`internal/harness/application/turn.go:28`) via `runTurnOwned` →
  `runAfterAdmission`. A compaction feature's natural insertion point is
  here: either `projectPriorTurns` gains a compaction watermark and starts
  folding from a later point in the stream, or a new function wraps its
  output before `newTurnProjectionWithPrefix` consumes it.
- **This project's own code already uses "compact" for something else,
  and a design must not collide with it.** `loop.go:63`'s own comment
  language — "the compact Session aggregate" — names a real, distinct
  concept: `internal/harness/domain/compact_test.go` asserts the in-memory
  `Session` aggregate stays small and non-growing as events apply to it
  (`compact_test.go:17,20`: "compact state = ...", "compact state
  unexpectedly grew"). This is aggregate-snapshot compactness (the
  `Session` struct never accumulates a full history itself — the event log
  is authoritative), not context/token compaction for the model. A
  Context Engine design needs its own distinct vocabulary — `Context`
  compaction, or a name avoiding "compact" outright — to keep these two
  unrelated meanings from colliding in code, tests, or documentation.
- **A token-window denominator already flows end-to-end; a token
  numerator does not.** `ContextWindowTokens`/`MaxOutputTokens` travel from
  the `-context-window`/`-max-output` CLI flags
  (`internal/harness/composition/config.go:23-24`, validated non-zero at
  `config.go:126-127`) through `openaicompat.ProfileToolsSupported(...)`
  (`internal/harness/composition/assembly.go:122`) into
  `engine.Profile.ContextWindowTokens`/`MaxOutputTokens`
  (`internal/harness/engine/profile.go:25-26`), and separately into the
  domain's own `ModelRequestSpec.ContextWindowTokens`/`MaxOutputTokens`
  (`internal/harness/domain/commands.go:105-106`, assigned at
  `internal/harness/domain/decide.go:74-75`, and durably recorded in the
  `ModelAttemptStarted`-shaped event schema at
  `internal/harness/domain/events.go:178-179`). What's genuinely missing
  is the numerator every reference project's trigger math needs: an actual
  or estimated count of tokens the about-to-be-built prompt will consume,
  and a decision point that consults both before `RunTurn` builds its
  projection. `ModelUsageRecorded`
  (`internal/harness/application/turn.go:529-543`,
  `modelUsageFromStats`) already captures real post-hoc
  `InputTokens`/`OutputTokens`/`CachedInputTokens` from provider
  `stats.Usage` — this is exactly the kind of "last real usage" signal Pi's
  own `estimateContextTokens` (below) prefers over a character estimate,
  and this project already has it, just not yet consulted by any
  pre-turn budget check.
- **The event store is exact-append-only with no delete or rewrite
  method, which fixes the shape any compaction design must take.**
  `docs/architecture/eventstore-v2.md:31-36`'s `EventStore` interface is
  exactly `ReadStream`, `Append`, `ResolveAppend`, `FindCommandRequest` —
  no update, no delete, no rewrite. Compaction cannot remove or alter a
  prior event; it can only **append a new event** and change what a later
  *projection* — `projectPriorTurns` itself — chooses to fold in. This is,
  independently, almost exactly Pi's own architecture: Pi's session log is
  append-only JSONL, its `compaction` entry is a normal appended log entry
  (`CompactionEntry extends EntryBase`,
  `pi-mono/packages/agent/src/harness/session/types.ts:44-51`, sharing
  the same `id`/`parentId`/`seq`/`timestamp` shape every other entry has),
  and `defaultContextEntryTransform`
  (`pi-mono/packages/agent/src/harness/session/context.ts:44-54`) is a
  pure **projection** that finds the latest `compaction` entry and returns
  `[compaction, ...pathEntries.slice(compactionIndex + 1)]` — the
  underlying append-only entry array is never mutated or truncated; only
  what a read-time projection folds into model messages changes. This is
  the single most transferable precedent this gate found: a design can
  add one new `domain.Event` (a compaction fact) and change
  `projectPriorTurns` to start folding from the newest such event forward,
  without touching this project's append-only Store contract at all.
- **Secret redaction already runs on model-visible and persisted text at
  two call sites, and a compaction summary is new text derived from
  already-redacted material.** `redact.Text` is called at
  `internal/harness/application/pipeline.go:290` (tool failure message),
  `pipeline.go:307` (tool result content), and
  `internal/harness/application/loop.go:246,295` (the final assistant
  message, both the success and terminal-unknown-resolution paths) — see
  `docs/architecture/secret-redaction.md:13`. A compaction-generated
  summary is built from history that already passed through these call
  sites once, so unless the summarization model is prompted in a way that
  reproduces a secret shape it was never shown, redaction-before-persistence
  already covers the summary transitively. This gate does not resolve
  whether that transitive coverage is airtight enough that a design can
  skip a third redaction call site on the summary itself, or whether
  belt-and-suspenders redaction is warranted precisely because a
  summarization model is explicitly instructed to preserve "exact file
  paths, function names, and error messages" (DeepSeek Harness's own
  instruction wording, below) — preservation instructions like that are in
  tension with redaction's own goal.
- **The charter names a domain entity this project has not built.**
  `docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md`
  §6.1 lists `ContextSnapshot`（"发送给模型的上下文构成及裁剪依据"）beside `Session`,
  `Turn`, `Item`, `ModelAttempt`, `Approval`, `Checkpoint`, and
  `PolicyDecision` as target domain entities. A direct grep of
  `internal/harness/domain/` confirms no `ContextSnapshot` type exists —
  every other entity that section names already does. This gate does not
  decide whether a compaction feature requires materializing
  `ContextSnapshot` as its own type or can express the same idea as a new
  variant on the existing `domain.Event` union; it only confirms the gap
  the charter itself already named is still open.

## Per-project findings

### Pi — append-only compaction entries, provider-usage-aware trigger, tool-call-safe cut points

- **Trigger math**: `shouldCompact(contextTokens, contextWindow, settings)`
  (`compaction.ts:247-250`) is a single reserved-margin check:
  `contextTokens > contextWindow - settings.reserveTokens`.
  `DEFAULT_COMPACTION_SETTINGS` (`compaction.ts:158-162`) fixes
  `reserveTokens: 16384` and `keepRecentTokens: 20000`.
  `estimateContextTokens` (`compaction.ts:215-241`) prefers the last real
  assistant message's provider `usage` block over a character-based
  estimate when one exists (`getLastAssistantUsageInfo`,
  `compaction.ts:207-212`), and only estimates trailing (post-usage)
  messages by character count — real usage, not estimation, is the
  primary signal whenever it's available.
- **Boundary-finding**: `findValidCutPoints` (`compaction.ts:312-341`)
  enumerates entries whose type is a message with role
  `user`/`assistant`/`bashExecution`/`custom`/`branchSummary`/`compactionSummary`,
  or a `branch_summary` entry — explicitly **excluding** `toolResult`
  entries as valid cut points (`compaction.ts:329-331`: `case
  "toolResult": break;` — no `cutPoints.push`). `findCutPoint`
  (`compaction.ts:373-421`) walks backward accumulating token counts until
  `keepRecentTokens` is reached, snaps forward to the nearest valid cut
  point, then additionally walks back over any non-message entry
  (`thinking_level_change`, `model_change`, etc.) so the cut always lands
  exactly on a message or compaction boundary
  (`compaction.ts:406-412`). It reports `isSplitTurn`
  (`compaction.ts:419`) when the cut lands mid-turn rather than on a
  turn-starting user message.
- **Summarization mechanics**: `SUMMARIZATION_SYSTEM_PROMPT` and
  `SUMMARIZATION_PROMPT` (`compaction.ts:424-459`) are real, fixed prompt
  text with an exact Markdown checkpoint format (`## Goal`, `##
  Constraints & Preferences`, `## Progress` with `### Done`/`In
  Progress`/`Blocked`, `## Key Decisions`, `## Next Steps`, `## Critical
  Context`); `UPDATE_SUMMARIZATION_PROMPT` (`compaction.ts:461-497`) is a
  distinct prompt for **incrementally updating** an existing summary
  rather than re-summarizing from scratch, used whenever a prior
  compaction entry exists (`prepareCompaction`, `compaction.ts:614-621`).
  `generateSummaryWithUsage`'s `maxTokens`
  (`compaction.ts:539-542`) is `Math.min(0.8 * reserveTokens,
  model.maxTokens)` — the summary itself is capped at 80% of the reserve
  budget it's meant to free up. There is no separate non-summarizing reset
  strategy in Pi; every compaction is model-generated.
- **Persistence/transaction shape**: `CompactionEntry` is a first-class
  member of the `Entry` union
  (`pi-mono/packages/agent/src/harness/session/types.ts:44-51`), sharing
  `EntryBase`'s `id`/`parentId`/`seq`/`timestamp` — an ordinary append-only
  log entry, not an ephemeral in-memory value. Raw history is never
  deleted: `defaultContextEntryTransform`
  (`session/context.ts:44-54`) finds the latest `compaction` entry and
  returns only `[compaction, ...pathEntries.slice(compactionIndex + 1)]`
  as the basis for `buildContextEntries`/`buildSessionContext`
  (`session/context.ts:57-100`) — a pure read-time projection over an
  untouched append-only entry array.
- **Failure/retry semantics**: `generateSummaryWithUsage`
  (`compaction.ts:642-655`) returns a typed `CompactionError` on an
  aborted (`"aborted"`) or errored (`"summarization_failed"`) response
  stop reason — a `Result` type, not a thrown exception, that the caller
  must explicitly handle. This gate did not trace the caller-side
  fail-open/fail-closed choice beyond `prepareCompaction` itself, which
  simply returns `undefined` (no-op, `compaction.ts:614-615`) when there
  is nothing to compact.
- **Configuration**: `CompactionSettings` exposes `enabled`,
  `reserveTokens`, `keepRecentTokens` (`compaction.ts:158-162`) as the
  only tunables — the smallest configuration surface of the six.
- **Tool-call/binary handling**: `estimateTokens`
  (`compaction.ts:269-299`) charges a flat `ESTIMATED_IMAGE_CHARS = 4800`
  (`compaction.ts:249`) per image content block rather than reading actual
  image size — images inflate the token estimate by a fixed amount but are
  not specially excluded from or specially handled by compaction itself.

### DeepSeek Harness — a real logged transaction with a lock, a stability check, and an explicit shrink requirement

- **Trigger math**: not read in this gate (lives outside the
  `compaction-basic` package this gate scoped to; the roadmap gate did not
  locate it either). What this gate did confirm in depth is everything
  downstream of a trigger decision: boundary selection through commit.
- **Boundary-finding**: `selectCompactableRange`
  (`region.ts:100-133`) walks backward from the end accumulating a
  `retainTokens` tail, then walks the cutoff index backward again,
  specifically until `toolPairingBalancedBefore(session, ...)`
  returns true (`region.ts:122-125`) — i.e., boundary safety is checked
  as a **second, independent pass** after the token-budget pass picks a
  provisional cut. `validateSurfaceRegion`
  (`region.ts:322-341`) re-checks both ends of the selected span
  (`toolPairingBalancedBefore`/`After`) a **second time**, immediately
  before the transaction runs — defense in depth against the selected
  range having gone stale between selection and commit.
- **Summarization mechanics**: `COMPACTION_INSTRUCTION`
  (`summarizer.ts:26-64`) is real, fixed prompt text — a
  `## Primary Request and Intent` / `## Key Technical Concepts` / `##
  Files and Code` / `## Errors and Fixes` / `## Pending Jobs` / `##
  Current Work` / `## Next Step` / `## Critical Context` checkpoint
  format, delivered as the **final user message** appended after the
  replayed conversation rather than as a separate system prompt — the
  module's own doc comment (`summarizer.ts:20-25`) states this
  explicitly: keeping the conversation's own system prompt, tools, and
  message prefix in front of the instruction makes the summarization call
  "a genuine prefix of the last routed request," reusing the provider's
  KV/prompt cache instead of invalidating it. The instruction also
  explicitly handles **iterative** re-summarization: "If the conversation
  already contains a `<compacted-summary>` block, it is a PRIOR
  checkpoint... merge newer information into a single consolidated summary"
  (`summarizer.ts:63`). `summaryText`
  (`summarizer.ts:216-223`) rejects any image content in the model's
  summary output outright (`throw new LlmError('compaction summary cannot
  contain image output', ...)`) — the summary itself must be pure text,
  though this gate did not confirm whether images **within** the
  region being summarized are included in, or stripped from, what's sent
  to the summarization call.
- **Persistence/transaction shape**: `compactSurfaceRegion`
  (`region.ts:152-249`) is a genuine two-phase transaction over the
  session's own append-only event log: `session.append('compaction/start',
  lifecycle)` commits **before** summarization begins
  (`region.ts:186`), and exactly one `session.append('compaction/end',
  ...)` follows no matter which stage fails — success
  (`region.ts:214`) or a caught failure
  (`region.ts:225`, with the error attached:
  `{ ...lifecycle, error: errorChain(error) }`). `assertCompactionInactive`
  (`region.ts:284-296`) is a durable lock: it inspects the event log for
  an unmatched `compaction/start` and refuses to begin a new compaction
  transaction while one is open, checked once before starting
  (`region.ts:168-170`) and again after an async policy decision via the
  separately exported `assertNoActiveCompaction`
  (`region.ts:299-306`). Automatic compaction requires an **open turn**
  and manual compaction requires **no** open turn
  (`region.ts:174-183`) — mutual exclusion with the turn lifecycle
  enforced at the type level (`owner: 'current-turn' | null`).
- **Failure/retry semantics**: `summarizeCompaction`
  (`region.ts:367-386`) explicitly rejects a summary that isn't smaller
  than what it replaced: `if (framedSummaryTokenCount >=
  prepared.shadowedRouteTokenCount) throw ...` (`region.ts:384-386`,
  message: "summary is not smaller than the shadowed content"). A
  `SurfaceChangedError` (`region.ts:78`) is thrown separately when the
  session's surface changed between selection and commit — an optimistic-
  concurrency check distinguished from a summarizer failure so a manual
  caller can report the two causes differently
  (`throwManualFailure`, `region.ts:257-278`, mapping to `'busy'` /
  `'commit'` / `'changed'` / `'summary'` / `'persistence'`
  `ManualCompactionError` kinds). This is fail-**closed**: any failure
  after `compaction/start` still closes the bracket with an error payload,
  but the compaction itself does not take effect and the caller receives a
  typed error, not a silent fallback to full history.
- **Configuration**: not read in depth in this gate — the package this
  gate scoped to (`compaction-basic`) implements mechanism, not the
  per-deployment tunables (retain-token budget, trigger threshold) that
  presumably live in a calling package.

### Kimi Code — dual trigger/block ratios, an adaptive context-size estimate, and turn-mutual-exclusion

- **Trigger math**: `DEFAULT_COMPACTION_CONFIG`
  (`strategy.ts:18-27`) sets `triggerRatio: 0.85`, and `blockRatio` is
  computed, not independently configured — `config()`
  (`strategy.ts:90-99`) derives `blockRatio: Math.max(triggerRatio,
  DEFAULT_COMPACTION_CONFIG.blockRatio)`, so block never falls below
  trigger even if a caller sets a lower trigger. `shouldCompact`
  (`strategy.ts:112-118`) fires at `usedSize >= maxSize *
  config.triggerRatio` **or** `shouldUseReservedContext`
  (`strategy.ts:126-129`: `reservedSize > 0 && reservedSize < maxSize &&
  usedSize + reservedSize >= maxSize`) — a second, independent trigger
  path via `reservedContextSize: 50_000`
  (`strategy.ts:20`), mirroring Pi's own reserve-margin concept as an
  *additional* trigger alongside the ratio-based one, not a replacement
  for it. `shouldBlock` uses the same two-path structure against
  `blockRatio` — a request is refused outright once usage crosses the
  block line, distinct from merely triggering a compaction attempt.
- **Boundary-finding**: `canSplitAfter`
  (`strategy.ts:242-250`) refuses a split when the message at the
  candidate index is a `user` message, an `assistant` message with
  pending `toolCalls`, or when the **next** message is a `tool` result —
  and separately calls `prefixEndsWithOpenToolExchange`
  (`strategy.ts:252-261`) to detect and refuse a boundary that would land
  inside a still-open multi-part tool exchange. For `source === 'manual'`
  compaction specifically (`strategy.ts:132-138`), it walks backward from
  the end looking for the first valid split point via `canSplitAfter`,
  rather than using the ratio-driven automatic-trigger walk.
- **Summarization mechanics**: `compaction-instruction.md` is a dedicated
  prompt file (not inline in TypeScript) framed as a first-person
  continuation note rather than a third-party report — its own text: "Write
  the note as your own continuing train of thought — first person, present
  tense... Do not write a third-party report about someone else's work."
  It explicitly instructs preserving exact commands run, exact file paths,
  and actual returned values ("the concrete values returned, the key
  lines or error text... since re-running to recover them may be slow or
  impossible") and instructs writing in whatever language the conversation
  itself used, not defaulting to English.
- **Persistence/transaction shape**: not confirmed as an append-only log
  entry in this gate the way Pi's and DeepSeek Harness's are — this gate
  did not locate an equivalent `CompactionEntry`-shaped durable record in
  `agent-core-v2`; `fullCompactionCompactionCountInTurnKey` and
  sibling `defineState<...>` calls (`fullCompactionService.ts:107-121`)
  look like in-memory/session-state counters, not event-log facts. This
  is a real gap in this gate's own coverage, not a confirmed negative.
- **Failure/retry semantics**: `CompactionTruncatedError`
  (`fullCompactionService.ts:101-105`) is thrown when a compaction
  response is cut off before producing a complete summary.
  `shouldRecoverFromContextOverflow`
  (`fullCompactionService.ts:298-309`) checks both a coded
  `CONTEXT_OVERFLOW` error and a raw HTTP 413 against
  `OVERFLOW_STATUS_RECOVERY_RATIO` of the currently *effective* max
  context. Distinctively, `observeContextOverflow`
  (`fullCompactionService.ts:311-321`) **adaptively lowers** its own
  per-model context-size estimate the first time it observes a real
  overflow — `this.observedMaxContextTokensByModel.set(modelAlias,
  Math.floor(estimatedRequestTokens * OVERFLOW_CONTEXT_SAFETY_RATIO))`
  — rather than trusting a statically declared context window forever.
  `begin()` (`fullCompactionService.ts:335-345`) throws
  `COMPACTION_UNABLE` ("Cannot compact while a turn is active or another
  context change is running") when a manual compaction is requested but
  the loop cannot acquire quiescence — mutual exclusion with an active
  turn, enforced by refusal rather than queuing.
- **Configuration**: `maxCompactionPerTurn` (default `Infinity`),
  `maxOverflowCompactionAttempts: 3`,
  `minOverflowReductionRatio: 0.05` (a compaction pass that shrinks the
  transcript by less than 5% counts as a failed attempt),
  `maxRecentMessages: 4`, `maxRecentUserMessages: Infinity`,
  `maxRecentSizeRatio: 0.2` (`strategy.ts:18-27`) — the widest tunable
  surface read directly in this gate.

### Grok Build — three named strategies, an opt-in default, an explicitly fail-open error philosophy, and a speculative pre-compute cache

- **Trigger math**: `should_compact`
  (`trigger.rs:117-149`) computes `threshold = context_window *
  trigger_threshold_percent / 100` (`trigger.rs:137`) and fires when
  `last_prompt_tokens > threshold`; a test fixture
  (`trigger.rs:159-160`) pins the working defaults at
  `trigger_threshold_percent: 85`, `target_threshold_percent: 50` — the
  pass compacts **down to** roughly half the window, not merely below the
  85% trigger line, the widest trigger/target gap of any project read in
  this gate. Partial modes additionally require `current_step >=
  policy.min_steps_before_compact` (default 3,
  `trigger.rs:131-133`) before firing at all, skipping compaction on
  very short conversations; `FullReplace` mode ignores this floor
  (`trigger.rs:129-130`). `IntraCompactionConfig::enabled`
  (`config.rs:90-91`) defaults to **`false`** — unlike every other
  project read in this gate, compaction is opt-in, not on by default.
- **Boundary-finding**: this gate located the concept
  ("no safe split point," `trigger.rs:57`) but not the implementing
  function — `select_turns_to_compact` and
  `get_accumulated_turns_for_compaction` are named in
  `IntraCompactionError::NothingToCompact`'s own doc comment
  (`trigger.rs:53-58`) but were not found inside
  `xai-grok-compaction`; the crate's own module doc for inter-compaction
  states plainly that per-harness turn gathering and sanitization "stay in
  the product host" (`inter_compaction/compact.rs:11-14`), so the actual
  boundary-finding likely lives in `xai-grok-shell`, outside this gate's
  read scope.
- **Summarization mechanics**: three genuinely distinct, named strategies
  rather than one algorithm with modes. Inter-compaction
  (`inter_compaction/compact.rs:1-14`) shares one chunked pipeline between
  `Basic` (unbounded chunk budget, exactly one chunk) and
  `DivideAndConquer` (`dnc_chunk_token_limit`-bounded, N chunks), differing
  only in per-chunk budget. Code-compaction
  (`code_compaction/compact.rs:1-16`) is explicitly the opposite of
  tail-preserving compaction: "grok-build does not select a tail to keep;
  it summarizes the whole conversation and rebuilds a fresh history from
  scratch," pipelined as `build prompt → sample (retry + classify) → clean
  → assemble`.
- **Persistence/transaction shape**: not confirmed in this gate — this
  gate's read scope (`xai-grok-compaction`) is explicitly described in its
  own module docs as transport-agnostic orchestration that "never commits
  or persists" (`code_compaction/compact.rs:16`); persistence is a
  per-harness concern in the product host, not read here.
- **Failure/retry semantics**: `IntraCompactionError`
  (`trigger.rs:47-80`) is explicit that **every** variant is non-fatal by
  design — the enum's own doc comment: "All errors are non-fatal — the
  caller should log and continue without compaction. Worst case the next
  sampling call may fail with 400, which is the same as today (no
  compaction support at all)" (`trigger.rs:47-50`). This is the most
  explicitly stated fail-open philosophy of the six projects read in this
  gate. `InsufficientReduction { tokens_before, tokens_after }`
  (`trigger.rs:70-74`) is a named variant for exactly DeepSeek Harness's
  own "not smaller" check, configured via `max_reduction_ratio`.
- **Configuration**: `IntraCompactionConfig`
  (`config.rs:82-101+`) is `#[serde(default)]` — every field individually
  optional with a documented default, loadable from YAML or a remote
  agent-config proto that "may only surface a *subset* of these fields"
  (`config.rs:76-81`) — a real distinction between locally-configurable
  and remotely-configurable knobs this gate did not see named explicitly
  in any other project.
- **Tool-call/binary handling**: not confirmed — no `tool_call`/`ToolCall`
  reference found inside `xai-grok-compaction` itself; likely handled in
  the per-harness turn-gathering step in `xai-grok-shell`, outside this
  gate's read scope.
- **Notable adjacent mechanism**: `xai-grok-shell`'s two-pass "prefire"
  flow speculatively pre-computes a compaction summary ahead of the
  trigger and caches it keyed by a content fingerprint of the
  conversation prefix — `fingerprint_prefix`
  (`compaction_two_pass_prefire_helper_tests.rs:5-27`) is tested to
  change whenever the prefix's content or length changes, invalidating the
  cached summary on any edit or rewind. This is a caching optimization
  none of the other five projects were observed to implement, and a real
  answer to "how do you avoid paying compaction latency synchronously at
  the moment the trigger fires."

### Codex — one shared lifecycle across three strategies, hook-vetoable compaction, and compaction wired directly into telemetry

- **Trigger math**: not read in this gate — `compact.rs`'s own imports
  (`compact.rs:1-48`) show `AutoCompactWindowIds` state and
  `CodexCompactionEvent`/`CompactionTrigger` typed telemetry, but the
  threshold-computation function itself was not located in the files this
  gate opened.
- **Boundary-finding**: not read in this gate — out of scope given the
  gate's time budget once the shared-lifecycle and hook findings below
  were established as Codex's most distinctive contribution.
- **Summarization mechanics**: `compact_remote_v2.rs`'s
  `RETAINED_MESSAGE_TOKEN_BUDGET: usize = 64_000`
  (`compact_remote_v2.rs:77`) is the fixed token budget for messages
  retained verbatim after remote/server-assisted summarization;
  `truncate_retained_messages_for_remote_compaction`
  (`compact_remote_v2.rs:582`) and `message_text_token_count`
  (`compact_remote_v2.rs:693`) implement truncation against that budget.
  `compact_model_fallback::should_retry_with_current_model`
  (`compact_model_fallback.rs:8-19`) treats `InvalidRequest`,
  `UnexpectedStatus`, `ContextWindowExceeded`, `UsageLimitReached`,
  `ServerOverloaded`, `InternalServerError`, and `RetryLimit` as
  retryable-with-current-model failures — errors that might be transient
  rather than requiring a model downgrade.
- **Persistence/transaction shape**: every strategy — including
  **token-budget compaction, which performs no summarization at all** —
  is modeled as the same `TurnItem::ContextCompaction` lifecycle.
  `run_compact_task_inner`
  (`compact_token_budget.rs:66-91`) is explicit in its own doc comment
  (`compact_token_budget.rs:23-25,49-51`, verbatim on both the manual and
  auto entry points): "Token-budget compaction skips model/server
  summarization and installs a fresh context window instead. It is still
  modeled as compaction so compact hooks and `ContextCompaction` turn
  items observe the same lifecycle as local or remote compaction." The
  function itself: `run_pre_compact_hooks` →
  `sess.emit_turn_item_started(ContextCompaction)` →
  `sess.start_new_context_window(...)` →
  `sess.emit_turn_item_completed(...)` → `run_post_compact_hooks`
  (`compact_token_budget.rs:73-89`) — a hard reset dressed in the exact
  same observable lifecycle as a real summarization pass.
- **Failure/retry semantics**: **pre-compact hooks can veto compaction
  outright.** `PreCompactHookOutcome::Stopped` aborts the entire turn
  (`compact_token_budget.rs:74-76`: `return Err(CodexErr::TurnAborted)`)
  before any context change happens; `PostCompactHookOutcome::Stopped`
  does the same after (`compact_token_budget.rs:86-88`). No other project
  read in this gate exposes an extension point that can refuse a
  compaction the trigger already decided to run.
- **Configuration**: `CompactionReason`
  (`compact_model_fallback.rs:29-34`) enumerates `UserRequested` /
  `ContextLimit` / `ModelDownshift` / `CompHashChanged` — the last
  meaning a hash of the compaction configuration/prompt itself changed,
  forcing recompaction even without new context pressure; this is the
  same class of cache-invalidation-by-content-fingerprint idea as Grok
  Build's `fingerprint_prefix`, arrived at independently for a different
  purpose (config-change invalidation, not prefix-edit invalidation).
- **Compaction is wired directly into this project's own Observability
  gap.** `record_model_fallback`
  (`compact_model_fallback.rs:21-27`) takes a `&SessionTelemetry`
  parameter directly from `codex_otel` — the same dedicated OTel crate the
  2026-09-01 roadmap gate already found (`otel/`,
  `otel_init.rs`) — and records structured `reason_tag`/`implementation_tag`
  fields per compaction fallback event. Codex is the clearest evidence in
  this comparison set that compaction decisions are exactly the kind of
  event a future Observability integration would want to trace, matching
  the roadmap gate's own synthesis point.

### Maka — three composable budget mechanisms and explicit fail-open replay

- **Trigger math**: `buildDefaultContextBudgetPolicy`
  (`context-budget-policy.ts:30-63`) derives `reserveTokens` from
  `defaultCompactReserveTokens(contextWindow)`
  (`context-budget-policy.ts:70-75`): a quarter of the context window,
  capped at the classic `16_384`, falling back to `16_384` when the
  window is unknown. Its own comment explains the derivation was a real
  bug fix, not an arbitrary choice: "The classic 16384 reserve assumed
  large-window models; on an 8K window it derived a 1-token history
  budget... every multi-step turn ran the summarizer for a checkpoint the
  replay gate could never admit" (`context-budget-policy.ts:66-69`) — a
  concrete cautionary precedent for this project's own future default,
  since this project already supports arbitrarily small
  `-context-window` values with no floor on what a reserve constant would
  leave for actual conversation.
- **Boundary-finding**: not read in depth in this gate — out of scope
  given the greater novelty of Maka's three-mechanism budget split, below.
- **Summarization mechanics**: not read in depth in this gate;
  `history-compact-summarizer.ts` (319 lines) and
  `history-compact-summary-validation.ts` (262 lines) exist as dedicated
  files but their prompt text and validation rules were not opened.
- **Persistence/transaction shape**: `CompactionBoundary`
  (`compaction-boundary.ts:52+`) is a typed, durable record with
  `kind: CompactionBoundaryKind`, `stage: CompactionStage`
  (`'priorReplay' | 'activeStep'`), a `boundaryId`, and a
  `predecessorBoundaryId` chaining one compaction to the last — a
  linked-checkpoint shape, not a single mutable pointer.
  `applyRuntimeEventHistoryCompact`
  (`history-compaction.ts:495-547`) replays the latest durable checkpoint
  against the current event ledger by prefix-matching
  (`matchHistoryCompactCheckpointPrefix`) rather than assuming the
  checkpoint still applies — checkpoint validity is actively re-verified
  against the live log on every read, not trusted once written.
- **Failure/retry semantics**: **explicit fail-open, distinct from every
  other project read in this gate.** When `matchHistoryCompactCheckpointPrefix`
  fails to match (`history-compaction.ts:505-514`) or the replayed
  checkpoint doesn't fit the token budget
  (`evaluateHistoryCompactCheckpointReplay`,
  `history-compaction.ts:527-539`), `applyRuntimeEventHistoryCompact`
  returns the **original, uncompacted `events` array unchanged**, tagged
  with a `decision: 'failedOpen'` diagnostic
  (`history-compaction.ts:513,539`) rather than throwing or blocking the
  turn — on any doubt about whether a checkpoint is still valid, Maka
  sends the full history rather than risk an incorrect or stale summary.
  `CompactionDecisionKind` (`compaction-boundary.ts:31`) types this as a
  first-class outcome alongside `'unchanged'`/`'replaced'`, not an
  exceptional case.
- **Configuration and — distinctively — tool-result handling separated
  from history summarization entirely.** The default policy
  (`context-budget-policy.ts:47-62`) composes **three independent**
  mechanisms, not one: `historyCompact` (summarization, `midTurn: {
  enabled: true, reserveTokens }`), `staleToolResultPrune`
  (`enabled: true, maxResultEstimatedTokens: 2_048, minRecentTurnsFull:
  2` — truncates old tool results over 2KB-estimated-tokens outside the
  most recent 2 turns), and `activeToolResultPrune`
  (`enabled: true, maxCurrentResultEstimatedTokens: 2_048,
  minSupersededResultEstimatedTokens: 256, minStepNumber: 1` — truncates
  **superseded** tool results within the *current, still-open* turn).
  This directly answers a question no other project in this comparison
  set answered explicitly: large/binary tool output is not left to a
  general-purpose text-summarization pass at all — it is pruned by a
  separate, narrower, size-threshold mechanism that runs independently of
  (and, per `activeToolResultPrune`, even *before*) full-history
  compaction ever triggers.

## Cross-cutting synthesis

- **The append-only-log-plus-projection-collapse shape is real precedent,
  not this gate's own invention.** Pi's `CompactionEntry` and
  `defaultContextEntryTransform` and Maka's chained `CompactionBoundary`
  with a re-verified-on-read checkpoint are both durable, event-log-native
  compaction records read at projection time, not history-mutating
  operations — directly compatible with this project's own append-only
  `EventStore` (no delete/rewrite method exists) and its existing
  `projectPriorTurns` projection point.
- **A trigger is never a single number in the deep implementations.** Pi
  is the simplest (`reserveTokens` alone); every other project layers a
  second signal on top — Kimi Code's independent `reservedContextSize`
  path alongside its ratio, Grok Build's `trigger`/`target` pair plus a
  `min_steps_before_compact` floor, Maka's window-fraction-derived reserve
  with an explicit small-window bug-fix history. A design should treat
  "one threshold" as an oversimplification even for a first slice.
- **Boundary safety against splitting a tool-call/result pair is checked
  more than once wherever it's checked carefully.** DeepSeek Harness
  checks it twice (selection, then re-validation immediately before the
  transaction commits); Kimi Code's `canSplitAfter` combines three
  independent conditions (user message, pending tool calls, next-message-
  is-tool) plus a separate open-tool-exchange scan. This project's own
  Step loop already treats a tool call and its result as tightly coupled
  (`ToolCallStarted`/`ToolCallCompleted`/`ToolCallFailed` in
  `projectPriorTurns` itself), so an equivalent safety check is a natural
  fit, not a new concept to invent.
- **"Compaction" is not always summarization, and the two named
  exceptions (Codex's token-budget reset, Grok Build's `FullReplace`)
  still route through the same lifecycle as summarizing strategies** —
  Codex's hook/turn-item pair fires identically whether or not any model
  call happens. If this project ever wants more than one compaction
  strategy, Codex's "one lifecycle, pluggable mechanism" shape is the
  cleanest precedent read in this gate.
- **Tool-result pruning and history summarization are separable
  concerns, per Maka's explicit three-policy split** — a large tool
  output does not have to wait for (or be handled by) full-history
  compaction at all; it can be bounded independently and earlier. This is
  the most direct, concrete answer this gate found to the question of how
  a naive summarization pass would mishandle large/binary tool content:
  the answer, in the one project that addressed it explicitly, is that it
  doesn't try to — a separate mechanism handles it first.
- **Fail-open and fail-closed are both real, deliberate choices, not an
  oversight in whichever project picked the "wrong" one.** DeepSeek
  Harness's manual path is fail-closed (a typed error, no silent
  fallback); Maka's checkpoint replay is fail-open (silently falls back to
  full history on any doubt); Grok Build states its philosophy outright as
  fail-open ("log and continue... worst case a 400"). A design must pick
  one deliberately per failure mode rather than assume a single project's
  choice is the obvious default.
- **Compaction already sits next to this project's two other
  under-designed-milestone concerns.** Codex's `record_model_fallback`
  feeding `codex_otel::SessionTelemetry` directly ties Context Engine to
  Observability; Grok Build's prefire cache and Codex's
  `CompHashChanged` reason both independently reach for
  content-fingerprint-based invalidation, a pattern any design here could
  reuse regardless of which specific trigger math is chosen.

## Open questions a design must resolve

- **Domain shape**: does compaction become a new `domain.Command`/`domain.Event`
  pair (a `CompactContext` command producing a `ContextCompacted`-shaped
  event, naming informed by the fact this project's own `Session` aggregate
  already uses "compact" for a different meaning — see "What this project
  already has" above), and does `projectPriorTurns` change to consult the
  latest such event as a watermark, mirroring Pi's
  `defaultContextEntryTransform`? Or does compaction stay entirely outside
  the domain layer as an application-level transform over
  `projectPriorTurns`'s own output, never durably recorded at all — a
  materially different durability and replay story (a design must decide
  whether a compacted turn's context is reconstructable from the event
  log alone, or requires re-running compaction at read time)?
- **Token-numerator source of truth**: does a pre-turn budget check use
  `ModelUsageRecorded`'s already-captured `InputTokens` from the most
  recent turn (Pi's preferred signal when available), a character/byte
  estimate for the first turn of a session before any real usage exists
  (every project's fallback), or — following Kimi Code's precedent — an
  **adaptively corrected** context-window estimate that lowers itself the
  first time a real 413/context-overflow response is observed, rather
  than trusting the CLI-flag-declared `ContextWindowTokens` as exact
  forever?
- **One strategy or several, from the start**: tail-preserving
  summarization alone (Pi/DeepSeek Harness/Kimi Code/Maka's shared shape)
  or, following Codex's and Grok Build's precedent, a from-day-one
  distinction between a no-summarization reset and a summarizing pass —
  and if the latter, whether both share one lifecycle event the way
  Codex's `TurnItem::ContextCompaction` does regardless of mechanism.
- **Tool-result handling**: does a first slice bundle large/binary tool
  output into the same summarization pass as everything else (Pi's
  approach — a flat per-image token charge, no special handling), or
  follow Maka's precedent of a separate, independent, smaller-scoped
  pruning mechanism that runs before or alongside full-history compaction?
- **Failure semantics per failure mode, not one blanket policy**: does
  a failed or timed-out summarization fail the turn (DeepSeek Harness's
  manual path), silently fall back to uncompacted history
  (Maka's `failedOpen`), or log and proceed with a next-call risk
  (Grok Build's stated philosophy) — and does this project's own
  `application.Error` category system (`CategoryModel`, `CategoryCanceled`,
  etc., already used throughout `loop.go`) already have a natural home for
  a compaction-specific failure code, or does it need a new category?
- **Concurrency with the turn lifecycle**: does compaction require an
  active turn (DeepSeek Harness's automatic path), forbid one (its manual
  path, and Kimi Code's `COMPACTION_UNABLE` refusal), or is it always
  scoped strictly inside the existing `runStepLoop`/`RunTurn` boundary
  this project already serializes per session via its execution-lease
  mechanism (`internal/harness/application/turn.go`), making an
  independent lock unnecessary because the existing admission/lease
  machinery already prevents concurrent turns on one session?
- **Redaction**: does a compaction summary need a third `redact.Text`
  call site of its own, given a summarization prompt that explicitly
  instructs preserving "exact file paths, function names, and error
  messages" (DeepSeek Harness's own wording) is in direct tension with
  redaction's goal of removing secret-shaped text — or is the existing
  two-call-site coverage (tool results/failures, final assistant message)
  transitively sufficient because the summary is built only from material
  that already passed through it once?
- **Client/export visibility**: does compaction need its own ACP
  `session/update` projection (so a live client sees "context was
  compacted" the way it sees a tool call today) or its own representation
  in `och export-session`'s JSONL — or is a compaction event, once it
  exists as a domain event at all, already covered by whatever generic
  event-to-projection mapping those two surfaces already use for every
  other domain event?
- **Resource bounds** (required by Documentation rule 4, and not yet
  named anywhere in this project's own code for this feature): a maximum
  summary size, a maximum number of compaction attempts per turn (Kimi
  Code's `maxOverflowCompactionAttempts: 3` and
  `minOverflowReductionRatio: 0.05` are concrete precedents), and a
  timeout on the summarization call itself, consistent with how this
  project already bounds `MaxAssistantBytes`, `MaxToolResultBytes`, and
  `MaxSteps` elsewhere in `application`.

## Evidence limits

- Every citation above traces to the pinned commits in the comparison
  table, opened and read directly in this session; no claim is
  transcribed from the 2026-09-01 roadmap gate's own (shallower)
  characterization without independent re-verification.
- This gate does not authorize copying any type name, schema shape,
  prompt string, or configuration constant verbatim from any reference
  project — only the mechanisms and architectural choices they represent,
  per the same rule every prior gate in this project states for its own
  comparison set. In particular, none of the reproduced prompt text above
  (Pi's checkpoint format, DeepSeek Harness's `COMPACTION_INSTRUCTION`,
  Kimi Code's continuation-note framing) is cleared for reuse verbatim in
  a future design or implementation; it is quoted here only as evidence of
  mechanism.
- Depth is uneven by design and by explicit disclosure per project, not
  silently: Pi and DeepSeek Harness were read essentially end-to-end for
  the full trigger→boundary→summarize→commit→fail pipeline; Kimi Code and
  Maka were read deeply for their most distinctive contributions
  (adaptive context-size correction; the three-mechanism budget split)
  with some sections (Kimi Code's persistence shape, Maka's boundary-
  finding and summarization prompt) explicitly marked as not confirmed
  rather than silently assumed; Grok Build and Codex were read for their
  most architecturally distinctive findings (opt-in-by-default and an
  explicit fail-open philosophy; hook-vetoable compaction and one shared
  lifecycle across three strategies) with their own trigger/boundary
  mechanics explicitly marked as out of this gate's read scope.
- Several concrete leads were named but not chased to their source in
  this gate, flagged here rather than dropped silently: Grok Build's
  `select_turns_to_compact`/`get_accumulated_turns_for_compaction`
  (likely in `xai-grok-shell`, the product host, not the shared crate);
  whether images *within* a compacted region are included in or stripped
  from what DeepSeek Harness's summarizer actually sends (only the
  summary's own **output** was confirmed to reject images); Maka's
  boundary-finding and summarization-prompt files
  (`history-compact-summarizer.ts`, `history-compact-summary-validation.ts`)
  were confirmed to exist and sized but not opened; Codex's own trigger
  and boundary-finding functions were not located within this gate's time
  budget.
- This gate does not audit any of the six projects' compaction
  implementations for correctness, performance, or security — only for
  placement, mechanism, and precedent value.
- "Current state" here means 2026-09-01. A future normative design for
  this project's own Context Engine must weigh, not copy, the findings
  above, and a gate that revisits any of these six projects later must
  re-fetch and re-read per Documentation rule 7 rather than reuse this
  document's characterization.
- This gate does not choose a design. The next step is a normative design
  for the Context Engine, informed by — not dictated by — the findings
  above.
