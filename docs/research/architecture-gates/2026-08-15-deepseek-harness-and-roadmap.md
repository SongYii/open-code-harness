# DeepSeek Harness Comparison and Delivery Sequencing

**Status:** Complete research evidence

**Date:** 2026-08-15

**Scope:** Record the official DeepSeek Harness primary source, the adopt/reject
boundary for later subsystem gates, and the delivery sequencing decision after
the Engine vertical slice.

This document is research evidence. It does not change EventStore v2 behavior
and does not authorize copying DeepSeek Harness types, plugins, or runtime.

English is the normative research record. The Chinese file is a synchronized
reading copy.

## Questions

1. Is the Open Code Harness product goal still correct after the Engine slice
   and the production-runtime design?
2. Should EventStore v2 remain the current implementation slice?
3. After official DeepSeek Harness opened, which comparison sources are
   first-party evidence and which are non-authoritative context?
4. Which DeepSeek Harness ideas may later gates adopt, and which conflict with
   the charter?
5. After EventStore v2 Slice 1, should delivery continue through the remaining
   runtime slices or return to product capabilities?

## Product-goal finding

The charter goal remains correct: a model-neutral, UI-neutral, event-driven
code-agent engine with recoverable Session/Turn semantics, independent
verification surfaces, ACP as the public client boundary, and industrial
quality on every completed slice.

The Engine vertical slice is correctly named a Minimal Executable Turn Runner,
not a tool-using agent loop. EventStore v2 is a justified contract correction
of that slice: v1 treated any non-nil append error as definitely uncommitted
and loaded the entire stream. Those assumptions are too strong or unbounded
for a production database.

The correction does not replace the product roadmap. The charter's next product
capabilities remain Provider and Tool/Policy. Persistence Slice 1 exists so
later adapters do not inherit an ambiguous store contract.

## Required comparison set

Later subsystem architecture gates must re-verify the then-public official
sources that are directly relevant to that slice:

| Source | Role | Primary entry |
| --- | --- | --- |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | Official DeepSeek harness; first-party evidence after 2026-08-13 | [architecture](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/architecture.md), [session](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/session.md), [persistence](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/persistence.md), [tool pipeline](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/tool-execution-pipeline.md) |
| [Pi agent core](https://github.com/badlogic/pi-mono/tree/main/packages/agent) | Small injectable loop and cancellation | existing Engine and runtime gates |
| [Kimi Code](https://github.com/MoonshotAI/kimi-code) | Package split, transcript order, client/server separation | existing Engine and runtime gates |
| [Grok Build](https://github.com/xai-org/grok-build) | Composition-root split, ACP stdio, headless parity | existing runtime gate |
| [OpenAI Codex](https://github.com/openai/codex) | Explicit item lifecycle, bounded queues, thread-store authority | existing Engine and runtime gates |
| [Maka](https://github.com/maka-agent/maka-agent) | Single execution authority; facts vs projections | existing Engine and runtime gates |

[DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) remains
community, non-authoritative context for provider-specific cache and routing
heuristics. It is not a substitute for official DeepSeek Harness.

Unavailable implementation details stay unknown. Marketing pages, unofficial
mirrors, and plugin-ecosystem repositories are not primary evidence.

## DeepSeek Harness observations

Observed on 2026-08-15 from the official repository
[deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness)
(`developer preview`, MIT, TypeScript/Cordis, "everything is a plugin").

| Observed contract | Adopt later | Boundary |
| --- | --- | --- |
| Session log is the source of model-visible context. `deriveMessages()` projects history from the log. A runtime invariant requires that anything reaching a model request is reconstructable from the log. | Context Engine and Provider gates: model-visible means logged. Context is a projection, not a second mutable transcript. | Do not treat their in-memory `Session` object as our Domain aggregate. |
| Events split into surface types (`user/message`, `assistant/message`, `tool/result`) and log-only facts. Unrecognized required types fail closed unless marked `ignorable`. | Keep unknown schema fail-closed. Distinguish model-visible facts from replayable audit/runtime facts. | Do not copy their event type names or `SessionEventMap` plugin merge. |
| Cold reload of an open `turn/start` appends `turn/end { reason: interrupted }` and does not truncate already durable steps. Live sessions are not synthetically interrupted. | Matches `process_crash` / no silent replay of model or tool effects. | Their async batched flush after a synchronous in-memory append is not our commit authority. Terminal facts commit before delivery. |
| A step is one model request plus the tools it called. A turn is zero or more steps. | Use this layering when Tool Runtime exists. The current Engine slice remains one model attempt. | Do not overload the current Item/Turn machine to fake a tool loop. |
| `tool/call` is logged before execution. The pipeline is `pre-execute → approval → guards → execute → post-execute → tool/result`. | Tool/Policy gate: one audited pipeline for built-in and MCP tools. | Do not implement policy as a Cordis waterfall of `next()` listeners. |
| Capability seams have a service definition, a provider, and a consumer. Swapping filesystem or subprocess moves Bash, PTY, and LSP together. | Keep replaceable adapters behind consumer-owned ports. | "Everything is a plugin" is not the Go core architecture. |
| Each model request logs `request/header` (config, system prompt, tool schemas) so a request is a pure function of the log. | Provider/Context gates: persist the exact request envelope used for an attempt. | Do not log raw secrets. Redaction remains a separate export. |
| Persistence backends implement one `SessionPersistence` seam. Format-unsupported is distinct from corruption. Compaction replaces surface nodes and does not rewrite earlier facts. | Keep rebuildable projections, explicit format refusal, and compaction as a new fact. | JSONL is not a second online authority. SQLite remains the sole commit authority in the accepted runtime design. |
| Generated persistence/event catalogs, bilingual docs, and package-owned runtime invariants. | Continue mechanical documentation and evidence discipline. | Do not adopt their 100% per-file coverage rule as a substitute for contract, race, and fault evidence. |

## Rejected DeepSeek Harness shapes

These conflict with the charter or with already accepted designs:

1. Cordis and "everything is a plugin" as the engine kernel. Adapters are
   replaceable; Domain, Application authority, and store contracts are not
   unloadable plugins.
2. TypeScript/Node as the engine core. The core remains pure Go, CGO-free.
3. Web UI as the primary product surface, with ACP reduced to automation-only.
   ACP v1 remains the public client boundary; the TUI is an ACP client.
4. Memory append first, durable flush later. Commit is the online fact.
5. JSONL and SQLite as peer live authorities.
6. Self-modifying runtime, Claude/Codex hook bridges, and generic event-bus
   interception of the loop.
7. Copying DeepSeek type names, plugin IDs, or ecosystem packaging.

## Delivery sequencing

Accepted runtime design still names six persistence/client slices. Slice 1
(EventStore v2) must finish before a SQLite adapter. Completing Slice 1 does
not obligate immediately implementing slices 2–6.

Recommended order after this research:

1. Finish EventStore v2 Slice 1, including unknown-outcome resolution and v1
   surface removal. Do not leave the migration half-cut over.
2. Do not start SQLite, JSONL audit, Runtime Host, ACP, or TUI solely because
   they appear next in the runtime split.
3. The next product designs are Provider and the minimal Tool/Policy loop.
   Those gates must include official DeepSeek Harness among their primary
   sources.
4. SQLite, recovery, ACP, and TUI resume after the tool-using loop contract
   exists, or when a later gate proves they are blocking that loop.

Without this pause, the repository can become a strong event store with no
real agent loop.

## Findings

### F1. Product goal is unchanged

Industrial, model-neutral, protocol-aligned harness. Not a demo, not a vendor
wrapper, not a plugin host.

### F2. EventStore v2 remains the open implementation slice

It is a breaking contract migration required by the accepted runtime design.
It is not a new product milestone and not a reason to rewrite the Engine
slice.

### F3. Official DeepSeek Harness replaces Reasonix as DeepSeek evidence

Reasonix stays community context. Later gates cite
`deepseek-ai/deepseek-harness` documents and source, not plugin galleries.

### F4. Adopt log reconstructability, fail-closed unknown events, crash
completion, step/turn layering, and an explicit tool pipeline

These strengthen Provider, Tool/Policy, Context, and later persistence slices
without changing Slice 1.

### F5. Reject plugin-kernel, web-first, async-flush authority, and dual live
stores

Those would undo the charter and the accepted runtime design.

### F6. After Slice 1, return to Provider and Tool/Policy

Runtime slices 2–6 stay designed, not automatically next.

## Evidence limits

- DeepSeek Harness is a developer preview and states that compatibility will
  break. Later gates must re-read the then-current documents.
- Public docs and source show observable contracts. Unpublished invariants
  remain unknown.
- This gate does not implement Provider, Tool, Context, SQLite, ACP, or TUI.
- Reference projects are not dependencies and do not donate type names.
