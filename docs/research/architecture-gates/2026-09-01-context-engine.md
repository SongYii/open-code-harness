# Context Engine Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-09-01

**Scope:** Milestone 8's missing Context Engine only: model-visible history
projection, token budgeting, safe compaction boundaries, summarization,
checkpoint durability, manual compaction, and provider-overflow recovery. The
already implemented canonical EventStore, JSONL audit replica, Runtime Host,
and crash reconciliation remain the persistence foundation rather than being
redesigned here. Evaluation, OpenTelemetry, TUI, long-term cross-session
memory, and MCP are separate milestones.

English is normative. The Chinese file is a synchronized reading copy.

## Why a focused gate is still required

The broader [Context Engine, Evaluation, Observability, and TUI gate](2026-09-01-context-engine-evaluation-observability-tui.md)
established sequence and comparison-set coverage, but explicitly did not
choose a Context Engine design. Documentation rules 7 and 8 require the
subsystem gate to re-read directly relevant source before a normative design.

The six disposable `.reference/` checkouts were re-verified on 2026-09-01 at
the same pinned commits fetched for the broader gate. No source was copied
into this repository.

| Project | Repository | Commit | Focused source re-read |
| --- | --- | --- | --- |
| Codex | [`openai/codex`](https://github.com/openai/codex) | `a9519cbcdd` | `codex-rs/core/src/compact.rs`, `compact_token_budget.rs`, `compact_remote_v2.rs`, `compact_model_fallback.rs`, and compaction suites |
| Pi | [`badlogic/pi-mono`](https://github.com/badlogic/pi-mono) | `853a80d26` | `packages/agent/src/harness/compaction/compaction.ts`, session context projection, and compaction regressions |
| DeepSeek Harness | [`deepseek-ai/deepseek-harness`](https://github.com/deepseek-ai/deepseek-harness) | `0a53fb55` | `compaction-basic/{region,summarizer}.ts`, the compaction service definition, token meter, tool-result pruner, and tests |
| Kimi Code | [`MoonshotAI/kimi-code`](https://github.com/MoonshotAI/kimi-code) | `8f2c60b32` | v1/v2 full compaction, strategies, handoff, token counting, and compaction scenarios |
| Grok Build | [`xai-org/grok-build`](https://github.com/xai-org/grok-build) | `bc7f02e` | intra/inter/code compaction, trigger, fit, selection, two-pass host tests, and token estimation |
| Maka | [`maka-agent/maka-agent`](https://github.com/maka-agent/maka-agent) | `ef94235ba` | context-budget policy, history compaction, checkpoint, ledger, coordinator, boundary, summary validation, Codex compactor, and tests |

## Current repository reality

The repository is not literally message-amnesiac anymore, but it still has no
independent Context Engine.

- `internal/harness/application/loop.go` now has `projectPriorTurns()`, which
  reconstructs prior user, assistant, and tool messages from canonical events.
  The path runs only when a non-empty tool catalog enables the Step loop; the
  model-only path still sends only the current input.
- The projection is guarded only by `MaxProjectionBytes` (4 MiB). It does not
  use `CapabilityProfile.ContextWindowTokens` or `MaxOutputTokens`.
- There is no compaction command, lifecycle event, checkpoint, summary prompt,
  safe cut planner, or manual operation.
- The first `ModelRequestRecorded` is admitted before historical projection is
  built, and later Step records contain only a suffix. The durable request
  facts therefore do not independently equal the message envelope actually
  sent to the provider.
- The EventStore already offers pinned, paged reads; the domain aggregate is
  intentionally bounded; SQLite already maintains derived projections in the
  append transaction; the Runtime Host already reconciles incomplete durable
  work. Those are the seams the Context Engine must use.
- Composition already rejects zero `Provider.ContextWindow` and zero
  `Provider.MaxOutput`. A production budget never needs to guess an unknown
  model capacity.
- The OpenAI-compatible adapter already classifies recognized 400/413/422
  context-limit responses as durable `context_overflow`. Recovery can depend
  on a closed error code rather than vendor-message substring checks in the
  application loop.

## Cross-project invariants worth adopting

### Token pressure is evaluated against the routed model

All six make model capacity part of the decision. Pi combines the latest
provider usage with a deterministic trailing estimate; DeepSeek Harness has a
dedicated token-meter service; Kimi Code separates start and block pressure;
Grok Build separates trigger and target thresholds; Codex has a distinct token
budget policy; Maka resolves capacity from the selected route and rechecks a
checkpoint under the current route.

The transferable requirement is not any one constant. It is that the budget
must name its capacity source, output reserve, safety reserve, trigger, target,
and estimated request shape. Open Code Harness already has a composition-time
route profile, so that profile is the capacity authority. A deterministic,
replaceable meter estimates the provider-neutral wire envelope; provider usage
is evidence, not a mutable replacement for the current estimate.

### A cut is a protocol boundary, not a message index

Pi's valid cut points, DeepSeek Harness's before/after tool-pair balance, Grok
Build's turn and Step selection, and Maka's explicit boundary taxonomy all
reject arbitrary slicing. The shared invariant is stronger than “keep N recent
messages”: no provider request may begin with an orphan tool result or end a
retained assistant tool-call block before all results are represented.

Open Code Harness can derive stronger units from its events:

1. a complete Turn is the preferred boundary;
2. a closed assistant-tool Step is an allowed boundary inside an oversized
   historical Turn;
3. the current open assistant item and an incomplete tool pair are protected;
4. model-request, usage, policy, approval, and compaction operational events
   are evidence, not conversational source units.

### Compaction is a logged transaction

Pi stores a compaction entry; DeepSeek Harness brackets start/end and closes a
failure; Codex emits a first-class compaction item; Grok Build observes the
same lifecycle across strategies; Maka durably records a checkpoint before it
may replace source history. This is the closest comparison-set consensus and
matches this repository's command/event architecture.

A successful summary is not allowed to exist only in memory. The lifecycle
must be `started -> completed|failed`; completed embeds an immutable checkpoint
covering an ordered source prefix. An incomplete start is recoverable work,
not a usable checkpoint.

### The log remains truth; the checkpoint is a disposable projection

Maka supplies the most directly compatible precedent: coverage count and
boundary, source digest, predecessor lineage, current-policy replay checks, a
bounded latest-checkpoint projection, and recovery from canonical ledgers.
Pi's session projection reaches the same authority outcome with a simpler log
entry. Neither requires destroying the source history.

Open Code Harness must retain every canonical event and JSONL audit fact. A
checkpoint can hide a covered prefix from a model request, but cannot delete,
rewrite, or become CAS authority for that prefix.

### Recent raw context and rolling summaries control quality and cost

Pi, DeepSeek Harness, Kimi Code, and Maka preserve a recent raw tail. Pi and
Maka carry a previous summary forward; DeepSeek Harness builds from the latest
routed surface; Kimi Code creates a structured handoff. Re-summarizing the
entire lifetime on every pressure event makes cost grow and lets old facts
drift repeatedly.

The checkpoint successor must therefore consume the previous valid summary
plus only newly covered canonical units. A later full rebuild remains possible
from the event log when a summary format, route, or quality rule changes.

### A result must shrink and must be structurally usable

DeepSeek Harness rejects a framed summary whose token cost is not smaller than
the replaced region. Kimi Code checks minimum reduction and prevents repeated
overflow loops. Pi and Maka have fixed summary shapes; Maka additionally
validates new-format summaries at write and load boundaries.

A non-empty string is insufficient. The design must validate required
sections, UTF-8, byte/token caps, truncation signals, redaction, and net shrink.
Invalid output closes the compaction as failed and never becomes a checkpoint.

## Divergent choices and decisions for this project

### Synchronous preparation, not speculative background compaction

Some Kimi paths start compaction before the hard block threshold and later
verify that history stayed safe to replace. That can save foreground latency,
but introduces a race between a mutating Step loop and an expensive summary.
This repository values one append authority and deterministic recovery over
speculative latency reduction.

Automatic compaction will run synchronously at a clean pre-provider boundary.
Pre-turn compaction runs before admission; mid-turn compaction runs while the
existing Turn owner is between Steps. Manual compaction refuses an active Turn.
There is no background summary goroutine and no late candidate that can race a
new source head.

### Two-pass event scanning, not an unbounded in-memory transcript

Grok Build's host has an explicit two-pass prefire path. The exact code is not
portable, but the mechanism answers a local problem: this EventStore reads
forward in pages while safe-tail selection reasons backward from the head.

The first pinned pass computes source digest, units, token totals, safe
boundaries, and a bounded recent deque. If compaction is necessary, the second
pass rereads only the selected ranges and streams bounded chunks to the
summarizer. A below-trigger request holds at most one request budget of raw
messages. No path loads an unbounded session transcript just to find a cut.

### One summarization mechanism plus one deterministic reset fallback

Grok Build and Codex expose several peer strategies, including provider-native
and no-summary reset. Open Code Harness currently has one generic Chat
Completions adapter and no provider-native compact protocol. Inventing an
opaque native abstraction with no working adapter would violate YAGNI.

The first complete engine has two checkpoint variants:

- `rolling_summary_v1`, the normal LLM-assisted, tail-preserving projection;
- `source_tail_reset_v1`, a deterministic, fact-free marker used only when a
  hard limit or confirmed overflow must shed an old prefix and a trustworthy
  summary cannot be produced.

The reset says only that older canonical history was omitted; it makes no
claims about that history. Manual compaction never silently selects reset: the
caller must request it explicitly. Provider-native opaque checkpoints require
a later provider-specific design and real adapter.

### Deterministic tool-result pruning is adjacent, not a second authority

DeepSeek Harness and Maka both prune large tool results without treating the
rewrite as a summary. Open Code Harness adopts this as request projection:
preserve the assistant call and tool result identity, replace an oversized body
with bounded head/tail excerpts plus byte count and digest, and keep the full
event canonical. The exact projected request is recorded before dispatch.

Pruning does not create a competing checkpoint chain and does not promise an
archive-read tool. A future retrieval tool is a separate Tool Runtime feature.

### A dedicated summary route is explicit

Pi and Kimi usually reuse the active model; DeepSeek Harness and Codex can
route or fall back. Sending history to another provider changes the data
boundary. Open Code Harness defaults to the active model. A separately
configured summary route is optional and explicit, with its own credential
environment variable and persisted non-secret route identity. Only when that
route was configured may the engine use it; cancellation never falls back.

## Required failure semantics

- Below the hard budget, summary failure records a failed bracket and keeps the
  prior valid projection or complete source-derived request.
- At the hard budget or after a recognized startup overflow, failure may use a
  durable deterministic reset plus the newest complete raw tail. It must never
  invent a summary.
- A candidate checkpoint is unusable until its exact append is committed or an
  unknown outcome is resolved as committed.
- Version conflict invalidates the candidate and replans from a new pinned
  head; it is never force-written.
- Cancellation wins before every provider call and append. Late model output
  is discarded.
- Startup overflow can be retried only after measurable context reduction and
  under a per-Turn recovery cap. Mid-stream failure after any delivered delta
  is not retried.
- A single protected unit that cannot fit after bounded tool-result projection
  fails with a stable context-unit-too-large error.
- A stale, malformed, source-mismatched, or currently over-budget checkpoint
  is rejected and rebuilt or bypassed from canonical events.
- Runtime startup closes an unmatched durable compaction start as failed; it
  does not synthesize a completion.

## Consequence for the normative design

The Context Engine design following this gate must specify:

1. a pure `contextengine` package for metering, projection, planning,
   checkpoint validation, and materialization;
2. application-owned orchestration and durable commands/events;
3. exact budget math and resource ceilings;
4. pre-turn, mid-turn, manual, and overflow flows;
5. rolling and reset checkpoint variants with coverage/digest/lineage;
6. a derived latest-checkpoint index in memory and SQLite, rebuildable from the
   event stream;
7. complete request-envelope recording before every provider dispatch;
8. conformance, fuzz, race, crash, mutation, and long-session evidence.

## Explicit exclusions

- vector retrieval, embeddings, semantic memory, and cross-session memory;
- deletion or compaction of canonical EventStore/JSONL data;
- provider-native opaque compaction without a real supporting provider;
- background/speculative compaction;
- a general retry subsystem for non-context provider failures;
- MCP, Context ArchiveRead, TUI, OpenTelemetry, and scenario evaluation;
- copying reference-project type names, prompt text, schemas, or constants.

## Evidence limits

- The six repositories were read at the pinned commits above. Their behavior
  can change after 2026-09-01.
- This gate evaluates mechanisms and placement, not summary quality claims or
  production correctness of the reference implementations.
- Character/token estimation differs by model. A replaceable meter and
  provider-overflow recovery bound the uncertainty; the generic engine cannot
  promise tokenizer-exact counts for every compatible endpoint.
- Provider-native Codex/Maka compact state is evidence that the checkpoint
  union may evolve, not authorization to implement a non-existent generic
  endpoint today.
