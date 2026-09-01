# Context Engine: Budgeted History and Durable Compaction — Implemented Contract

**Status:** Implemented; not GA

**Authority:** [Context Engine: Budgeted History, Durable Compaction, and Recovery design](../superpowers/specs/2026-09-01-context-engine-design.md)

**Implemented plan:** [Context Engine implementation plan](../superpowers/plans/2026-09-01-context-engine.md)

**Completion evidence:** [Context Engine evidence ledger](context-engine-evidence.md)

**Chinese reading copy:** [已实现 Context Engine 合同](context-engine.zh-CN.md)

**Packages:** `internal/harness/contextengine` (pure), `internal/harness/application` (orchestration), `internal/harness/adapters/{sqlite,memory}` (checkpoint persistence and recovery), `internal/harness/composition` (assembly wiring)

This document records behavior enforced by the current code and tests. It is
an internal Go contract, not a stable public protocol.

## Scope

One Context Engine constructs the model-visible request from canonical
Session events for both model-only and tool-enabled Turns: it meters the
complete provider-neutral envelope against the selected route's declared
capacity, selects a tool-pair-safe history prefix, and replaces that prefix
with a durable checkpoint before a Provider consumes it. Canonical events and
the JSONL audit replica are never compacted or rewritten — a checkpoint is a
lossy, disposable projection with exact source coverage, a SHA-256 digest
over that coverage, format version, token evidence, and predecessor lineage.

`internal/harness/contextengine` is pure: it imports only `internal/harness/domain`
and the standard library (CE-01). It owns projection, metering, planning, and
checkpoint validation, but never a Provider call, a Store append, or
cancellation handling — those are Application's (CE-02). Composition
constructs the Context Engine unconditionally as part of every assembly
(`composition.Config.Context` tunes it; there is no separate enable switch —
see [Composition wiring](#composition-wiring) below).

## Budget and meter contract

For a route's declared context window `W` and maximum output `O`:

```text
safety = max(512, ceil(W * 0.02))
hardInput = W - O - safety
trigger = floor(hardInput * TriggerPercent / 100)
target = floor(hardInput * TargetPercent / 100)
protectedTail = floor(hardInput * TailPercent / 100)
summaryOutputCap = min(O, max(128, floor(hardInput * 0.10)))
```

`contextengine.ComputeBudget` computes this; it returns `ErrBudgetInvalid`
when `O + safety >= W`, since no positive `hardInput` can exist. Composition
validates this before constructing any resource (see below), and Application
independently re-validates `Budget.HardInput > 0` at `NewService` when
`Context.Enabled`.

Defaults (design §8's own table, all configurable via
`composition.Config.Context`, all range-checked before assembly):

| Setting | Default | Valid range/invariant |
| --- | --- | --- |
| `TriggerPercent` | 80 | 60–95 |
| `TargetPercent` | 55 | 30–80 and `< TriggerPercent` |
| `TailPercent` | 25 | 10–50 and `< TargetPercent` |
| `MaxSummaryChunks` | 8 | 1–16 (accepted and validated; **not yet consumed** — see [Known limitations](#known-limitations)) |
| `MaxOverflowCompactionsPerTurn` | 2 | 1–3 |
| `CompactionTimeout` | 2 minutes | 5 seconds–10 minutes |
| `MaxPrunedToolResultsPerRequest` | 64 | 1–64 (accepted and validated; **not yet consumed** — see [Known limitations](#known-limitations)) |

The default deterministic meter, `och_wire_estimate_v1`
(`contextengine.WireEstimateMeter`): text/JSON payload `ceil(UTF-8 bytes / 3)`;
8 tokens fixed framing per message; 16 additional tokens per Tool Call or
Tool Result; 16 tokens plus `ceil(canonical JSON bytes / 3)` per Tool Schema.
This intentionally overprices typical ASCII prose rather than risk
underestimating.

`contextengine.EvaluateUsageAnchor` implements design §8's non-lowering
provider-usage anchor (`budgetEstimate = max(wireEstimate, anchoredEstimate?)`)
as pure, independently tested logic — but it is **not yet called from
Application**; every real dispatch decision today uses only the deterministic
wire estimate. See [Known limitations](#known-limitations).

## Projection, planning, and checkpoint pipeline

Canonical events project into conversational units (`contextengine.ProjectSourceEvents`,
design §9.1) in sequence order: `TurnStarted` → user message,
`AssistantMessageCompleted` → assistant message (+ Tool Calls),
`ToolCallStarted`/`Completed`/`Failed`/`Interrupted` → tool results. Session,
model request/usage, policy, approval, and context events never directly add
messages. A boundary is balanced only after every offered Tool Call has
exactly one terminal result; duplicate, unknown, or missing IDs fail closed.

`contextengine.Scan` performs the pinned-head, page-by-page read (Pass 1): the
first page fixes `HeadVersion`; every following page is requested against
that same value, failing closed (`ErrHeadMismatch`) on disagreement.
`contextengine.SelectCutPoint` (Pass 2) walks backward from the head until
`protectedTail` is met, then snaps older coverage to the nearest safe Turn
boundary — coverage is always a full Turn, never a Turn split between
covered and retained. `contextengine.Materialize` combines an optional
checkpoint's own message, the retained tail, and the current input into one
`PreparedContext`, whose `EstimatedTotalTokens` is what Application compares
against `Budget.HardInput` before dispatch.

A checkpoint (`contextengine.ContextCheckpoint` / `domain.ContextCheckpointRecord`)
is one of two kinds:

- `rolling_summary_v1` — an LLM-generated structured summary, validated for
  shape, redaction, truncation, and measured shrink
  (`contextengine.ValidateSummary`) before it is ever accepted;
- `source_tail_reset_v1` — a deterministic marker containing no historical
  claims, used only when a hard capacity limit cannot wait for a trustworthy
  summary.

Every checkpoint carries a SHA-256 digest chain (`contextengine.ExtendSourceDigest`,
seeded at `D0 = SHA256("och-context-source-v1\n")`) over exactly the source
events it covers, filtered by `contextengine.IsSourceEvent` (the same six
event types the projection grammar folds into a message). A successor
checkpoint starts its digest from the predecessor's own digest and extends
only over the newly covered range; a same-coverage rewrite requires an
identical digest. This chain is independently re-verified — never trusted
from a claimed value — at three points: the SQLite write-time hook
(`updateContextCheckpointHead`, inside the same transaction that commits the
completion event), the SQLite/memory read paths (`LoadLatestContextCheckpoint`),
and the SQLite cold-rebuild path (`RebuildAndVerifyContextCheckpointHeads`).
Any disagreement is `store_corrupt`, never silently repaired over an existing
row — only a genuinely *missing* row for a session with an independently
valid checkpoint is repaired (written) by rebuild.

## The four triggers

| Trigger | When | Turn state required |
| --- | --- | --- |
| `pre_turn` | Before a Turn's admission batch commits, when the uncompacted estimate exceeds `Budget.Trigger` | No active Turn |
| `mid_turn` | Between tool Steps, same condition | An active Turn |
| `manual` | Operator-invoked (`Service.CompactSession`), bypasses the Trigger comparison via `PlanInput.Force` | No active Turn |
| `overflow_retry` | A Provider startup rejection classified as context overflow, bounded by `MaxOverflowCompactionsPerTurn` per Turn | An active Turn |

Domain enforces this Turn-state pairing at both `Decide` and `Apply`
(`decideStartContextCompaction`/`applyContextCompactionStarted`): `pre_turn`/
`manual` require no active Turn; `mid_turn`/`overflow_retry` require one.
At most one compaction is ever active per Session (`Session.ContextCompaction`).

Manual compaction (`Service.CompactSession`) is deliberately narrower than the
automatic paths: a single strategy attempt only (`summary`, the default, or
explicit `reset`) with **no ladder fallback** — a failed manual summary
returns its own failure directly, per design §16's "manual summary instead
returns its failure" rule, rather than falling through to a deterministic
reset the operator did not ask for. The automatic paths do fall through: a
failed summary attempt below `hardInput` proceeds uncompacted (logged); at or
above `hardInput`, a deterministic reset is attempted if eligible — except a
caller's own cancellation is checked immediately after a failed summary and
short-circuits this ladder entirely (cancellation never becomes a reset,
Global Constraint, verified under real goroutine contention in
`context_concurrency_test.go`).

Overflow recovery reduces by at least 10% between attempts and is bounded by
`MaxOverflowCompactionsPerTurn`; because `SelectCutPoint`'s own cut is
already maximal in one shot, at most one real recovery can ever succeed per
Turn in practice — a second attempt structurally finds nothing further to
cover before ever reaching the configured cap (disclosed, not a gap).

## Recovery

Design §14 in three parts:

1. **SQLite projection** (migration 5, `context_checkpoint_heads`): a single
   indexed row per session (`checkpoint_event_sequence`, `checkpoint_event_id`,
   `checkpoint_id`, `covered_through_sequence`, `source_digest`,
   `updated_at_commit_position`), updated only after independent hash-chain
   verification inside the same append transaction. `LoadLatestContextCheckpoint`
   is O(1): one indexed row read plus one canonical-event join, not a
   full-stream scan (benchmarked — see the evidence ledger).
2. **Projection recovery**: `RebuildAndVerifyContextCheckpointHeads` re-derives
   the furthest independently-valid checkpoint per session from canonical
   events alone, in bounded pages (never materializing a whole session's
   history at once), and reconciles the stored row against it. Wired into
   JSONL import as a dedicated layer, since import never writes this
   projection incrementally.
3. **Runtime Host reconciliation**: an unmatched `ContextCompactionStarted`
   found at startup becomes `ContextCompactionFailed{Code: "runtime_recovered"}`.
   When a `mid_turn`/`overflow_retry` compaction crashed inside a running
   Turn, its failure is ordered *before* the Turn's own terminal events. A
   `manual`/`pre_turn` compaction has no enclosing Turn, so `Store.SessionsWithActiveCompaction`
   (a session whose own stream head is an unmatched `context.compaction.started`
   — reliable without a dedicated derived-state table, since no other command
   can extend the stream while a compaction is active) supplies the candidate
   `session_heads.status` alone cannot surface.

The memory adapter's `LoadLatestContextCheckpoint` makes a different,
disclosed trade: no separate write-time hook exists, so every read
independently recomputes the full digest chain from `D0` — O(history) per
read rather than SQLite's O(1), but delivering the identical verification
guarantee, consistent with this adapter's existing precedent of favoring
simplicity over performance.

## Failure algebra

| Code | Category | Retryable | Meaning |
| --- | --- | --- | --- |
| `context_budget_invalid` | validation/config | no | Route capacity cannot produce a positive hard input budget. |
| `context_projection_invalid` | internal | no | Canonical events cannot form valid message/tool units. |
| `context_unit_too_large` | context/model | no | One protected projected unit exceeds hard input. |
| `context_compaction_busy` | conflict | yes, after owner finishes | Another durable compaction owns the Session. |
| `context_nothing_to_compact` | validation for manual; no-op internally | no | No safe source prefix exists. |
| `context_summary_failed` | model | yes, by a later command | Active-route summary call or stream failed. |
| `context_summary_invalid` | model | yes, after route/prompt-version change | Output failed structure, truncation, redaction, or shrink validation. |
| `context_checkpoint_invalid` | internal/store | no | Candidate or loaded checkpoint violates coverage/lineage/schema. |
| `context_compaction_limit` | model | no, in current Turn | Overflow recovery cap was exhausted. |

Every compaction append (Start/Complete/Fail) that returns an unknown commit
outcome is resolved via the same `ResolveAppendIntent` pattern every other
append in this project uses — never left permanently uncertain.

## Concurrency and cancellation

Verified under `go test -race` (`context_concurrency_test.go`): concurrent
manual/manual compaction on one Session (chain-integrity invariant — no
duplicate or overlapping coverage in a strictly-advancing, correctly-linked
chain, not a naive "exactly one succeeds" assertion, since legitimate
sequential success is possible); manual compaction concurrent with `RunTurn`
(mutually exclusive, verified by walking the durable log for overlapping
compaction/Turn brackets); `RunTurn` concurrent with Session close (same
pattern); overflow recovery concurrent with caller cancellation (two
independent guards: `runCompactionBracket` checks `contextError(ctx)`
immediately after a failed summary and skips the reset ladder outright, and
`ResetEligibility.CallerCanceled` is threaded through as a second,
independent check); a compaction append landing in the unknown-outcome state
concurrent with cancellation (resolver rules win).

## Composition wiring

`composition.Config.Context` is a tuning struct, not an enable switch — a
working Context Engine is this milestone's baseline assembly behavior.
`Open`'s construction order gains "Context meter/engine + summarizer" between
the conversation Provider/runner and the workspace tools/catalog: the
summarizer (`application.EngineContextSummarizer`) wraps the *same* runner/
model the conversation path uses (design §18's "no second Provider"), and the
already-constructed SQLite store (a `ContextCheckpointStore` since migration
5) is passed straight through. Every range/relationship in `Context` is
validated, and whether the route can even produce a positive budget is
checked via `contextengine.ComputeBudget` itself, before `Open` constructs
any resource.

## Adapter and protocol projection

- **ACP** (`adapters/acp/project.go`): a checkpoint or context preparation
  decision never replaces or supplements visible conversation history — all
  four new event types project to nothing (`ProjectRecordedEvent`'s
  `default` case), exactly as `ModelRequestRecorded`/`PolicyDecisionRecorded`
  already did. An explicit non-projection test proves this, not merely its
  absence from a positive-projection table.
- **Transcript** (`transcript/codec.go`): the strict fact codec accepts all
  four new event types, including the checkpoint summary text itself — the
  transcript is an explicit content-bearing export, unlike ACP's
  canonical-only projection. Before this task, an unrecognized event type
  failed the whole export closed (`CodeUnsupportedEventType`); any session
  that had ever run a compaction would have made `och export-session` error
  out the moment it reached that event. Fixed as part of this milestone, not
  left as a latent regression.

## Resource bounds

- No request Application ever dispatches exceeds `Budget.HardInput` tokens
  or `MaxProjectionBytes` (4 MiB) — confirmed by `BenchmarkMaterialize`
  (100/1,000/10,000-Turn streams): the materialized envelope and its
  estimated token count stay flat regardless of Turn count once
  `SelectCutPoint` has decided what to retain.
- `LoadLatestContextCheckpoint` (SQLite) stays flat regardless of history
  length — `BenchmarkLoadLatestContextCheckpoint` measured ~190µs/23KB at
  100, 1,000, and 10,000 Turns alike.
- `RebuildAndVerifyContextCheckpointHeads` is `O(history)` in time (an
  accepted, disclosed cost for a cold/rare recovery path) but paged and
  heap-bounded — it never materializes a whole session's canonical history
  in one slice; `TestRebuildContextCheckpointHeadsSpansMultiplePages` proves
  correctness across a page boundary specifically.

## Known limitations

Disclosed here, in the evidence ledger, and in code comments at their exact
location — not left for a benchmark number or an absent test to speak for
itself. None of these compromise correctness or safety; all of them trade
away performance, completeness, or convenience this milestone chose not to
spend more time on.

1. **`Scan` and `SelectCutPoint`'s own upfront estimate are `O(history)` in
   both time and transient memory, on every single call — not merely `O(1)`
   relative to the configured budget window.** `Scan` re-reads and holds
   every canonical source record from the beginning of the stream every
   time `PrepareContext` runs; there is no "resume scanning from the last
   checkpoint" mode, even once a checkpoint already covers everything but
   the protected tail. `BenchmarkScan`/`BenchmarkSelectCutPoint`
   (`internal/harness/contextengine/bench_test.go`) measure this directly:
   allocations scale linearly with Turn count (100 → 1,000 Turns is roughly
   a 10x jump in bytes/op for both). This means a below-trigger Turn's
   admission on a long-lived session pays a cost proportional to the whole
   session's history, not to the configured budget — the single most
   significant gap this milestone's own benchmark work found. It does not
   violate correctness (`BenchmarkMaterialize` confirms the envelope
   actually dispatched stays bounded), but it does mean the "live heap
   bounded by budget, not Turn count" property design §22.4 asks the
   benchmark suite to check is *not* delivered on the planning path today.
   Fixing it needs `Scan` to support an incremental, resume-from-checkpoint
   scanning mode — a real architectural change to a pure, heavily
   mutation-tested package, planned as a follow-up rather than rushed at
   the tail of this milestone.
2. **The non-lowering provider-usage anchor (`EvaluateUsageAnchor`) is
   implemented and independently tested as pure logic, but is not called
   from Application.** Every real dispatch decision uses only the
   deterministic wire estimate; the anchor's potential to tighten that
   estimate using observed usage is inert. Safe (the wire estimate is
   always a valid, if conservative, upper bound) but incomplete relative to
   design §8.
3. **`MaxSummaryChunks` and `MaxPrunedToolResultsPerRequest` are accepted
   and range-validated by `composition.Config.Context`, matching the
   design's literal contract, but do not yet change behavior.** The
   summarizer is single-shot only (`buildSummaryCheckpointWithFocus`
   rejects, rather than chunks, source material too large for one call);
   Tool Result pruning (`contextengine.ProjectToolResult`) is never called
   from `Materialize`'s pipeline.
4. A **duplicate `RunTurnRequestID` join specifically while a `pre_turn`
   automatic compaction is mid-flight** (design §22.2's own named scenario)
   has no dedicated test; the adjacent manual-compaction-vs-RunTurn
   exclusivity case (`TestConcurrentManualCompactionAndRunTurnAreMutuallyExclusive`)
   is covered, but this narrower automatic-trigger variant is not.

`och compact-session` (design CE-14's own CLI command) is now built —
`cmd/och`'s own `compact-session` subcommand opens the normal composition
root, runs `Service.CompactSession`, and prints one stable JSON object to
stdout (see [Getting Started](../getting-started.md#manual-compaction)).
Building it against a real, composition-wired `ContextCheckpointStore` for
the first time (every prior reset-strategy test used either
`fakeCheckpointStore`, which never verifies anything, or
`ValidateSuccessor`'s own structural-only checks, neither of which
recomputes a digest from canonical content) surfaced and fixed a real bug:
`buildResetCheckpoint`'s `Coverage.SourceDigest` was left at its seed value,
never actually extended over the newly covered canonical records, even
though `ThroughSequence` correctly advanced — every deterministic-reset
checkpoint ever built (both the manual `-strategy reset` path and the
automatic overflow-recovery reset path, which share this one function)
would have been rejected by a genuinely verifying store the moment it was
read back. Fixed by extending the digest exactly as the rolling-summary
path already did; regression-tested against a real, independently
verifying store (`TestCompactSessionResetCheckpointDigestSurvivesIndependentVerification`,
plus the CLI's own end-to-end tests), with its own mutation check.

## Exclusions

Restating design §4's own non-goals as delivered exclusions, not silent
absences:

- No vector search, embeddings, RAG, semantic retrieval, or cross-session
  memory.
- No deletion, rewriting, squashing, or storage compaction of canonical
  events — every checkpoint is lossy and disposable; the canonical log and
  JSONL audit replica are untouched.
- No provider-native opaque compaction without a concrete supporting
  Adapter.
- No background compaction worker or speculative summary generation —
  compaction is always synchronous and caller-driven.
- No general retry for authentication, quota, rate-limit, transient, or
  arbitrary permanent Provider failures — only confirmed startup context
  overflow retries, and only after a measured reduction.
- No model-facing archive read tool for pruned Tool Results.
- No context editing, rewind, branching, or user-authored checkpoint
  mutation.
- No MCP, TUI, OpenTelemetry, or milestone 10 evaluation-runner surface.
- No ACP method or non-standard `session/update` variant for manual or live
  compaction.
- No exact tokenizer parity for every endpoint speaking an OpenAI-compatible
  wire protocol — `och_wire_estimate_v1` is a deliberately conservative,
  model-neutral estimate.

## GA blockers

This milestone stays **not GA** until, at minimum: real-model quality
evaluation of rolling summaries exists (this implementation's own tests use
scripted/fixture summarizers throughout, never a live model's actual
summarization quality); wider provider coverage than the OpenAI-compatible
adapter this milestone exercises; the `Scan`/`SelectCutPoint` `O(history)`
planning-path cost above is resolved or explicitly accepted for production
scale; and a wall-clock, multi-process soak of the recovery paths beyond
this milestone's deterministic-time and scripted-outcome test evidence.
