# Local Subagent Delegation Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-09-12

**Scope:** Re-verify the six standing comparison projects and decide whether a
bounded local-subagent slice is justified for Open Code Harness. This document
designs and implements nothing.

## Short answer

Local task delegation is now a real comparison-set gap: Codex, Grok Build,
DeepSeek Harness, Kimi Code, and Maka all ship first-class child-agent
lifecycle machinery. Pi still leaves it to extensions. The projects do not
converge on one large feature set, but they do converge on five invariants:

1. a child has an identity and context separate from its parent;
2. lineage and recursion depth are explicit;
3. concurrency, output, and waiting are bounded;
4. parent cancellation reaches the child; and
5. the parent consumes a bounded terminal result rather than the child's raw
   internal stream.

The right first slice here is therefore **one synchronous, in-process,
read-only child Turn in a new durable Session**. It is deliberately smaller
than the references: no background roster, messaging, model override, ACP
child process, recursive delegation, or child writes. Those are separate
contracts, not fields to pre-build.

## Re-verified sources

The gitignored reference checkouts were fetched immediately before reading.

| Project | Ref read | Finding |
| --- | --- | --- |
| Codex | `c4017a87aa` | First-class `spawn_agent`, `wait_agent`, messaging, interruption, child threads, roles, model overrides, shared concurrency guard, and fork modes. |
| Grok Build | `37949780c1` | Durable subagent attempts, child runtime, resume windows, start publication, prompt receipts, recovery, accounting, ACP projection, and a separate resolution crate. |
| DeepSeek Harness | `c291e7961a` | A provider seam supporting one-shot and continuable children across in-process, ACP, SDK, Codex, and Claude backends; explicit depth and capability checks; bounded safe results. |
| Kimi Code | `ee2cac102b` | A built-in agent tool, separate subagent sessions, profile/catalog selection, foreground/background tasks, fork behavior, timeout handling, and swarm/tower layers. |
| Pi | `71dca871bc` | No built-in child-agent contract; the package manager and tests recognize third-party subagent extensions. This is an extension precedent, not a core implementation. |
| Maka | `3cfcb09fd9` | Durable parent/child session relations, idempotent spawn claims, runtime profiles, result status, navigation, and persistence-backed child discovery. |

## What the references teach

### Identity is not optional

Current Codex uses child threads and task paths; DeepSeek records descriptors
and a parent-owned child catalog; Maka persists parent session, spawn identity,
and child runtime metadata. A child answer without a child identity is easy to
display but impossible to audit, resume, or distinguish from an ordinary model
call.

For this repository the natural identity is a real `SessionID`. The canonical
EventStore already provides recovery, transcripts, context checkpoints, and
model/tool evidence per Session. Inventing a second task log would throw those
properties away.

### Publication must be atomic enough to tell the truth

DeepSeek treats publication as the ownership boundary: before publication the
provider must roll back; after publication the caller owns cleanup. Maka uses
an idempotent spawn claim. Grok stores start intent and completion separately.

The first slice here need not solve distributed spawn, but it must durably link
the child before claiming success. A parent tool result that names a child
whose Session was never committed is false evidence.

### Context inheritance is a product choice

Codex now supports multiple fork modes and has extensive tests around compacted
or paginated parent history. Kimi also supports forking. DeepSeek supports both
fresh one-shot children and continuable children with explicit descriptors.

Copying parent history in v1 would couple delegation to every compaction and
instruction rule. A fresh child Session with only the delegated task is the
smaller honest contract. It still receives the same fixed system prompt and
workspace `AGENTS.md` instructions through the ordinary production path.

### Permissions must narrow, never silently widen

DeepSeek's child scope is fixed when started and approval-requiring operations
are rejected automatically. Its ACP backend defaults permission prompts to
reject. This is the safest precedent for a first slice whose parent is already
waiting inside one tool call.

Open Code Harness should expose only `read_file` and `list_dir` to the child.
It should not expose `write_file`, `edit_file`, `exec`, MCP tools, or delegation
itself. That makes the first child useful for codebase inspection while
avoiding nested approval routing and concurrent workspace mutation before
either has its own design.

### Bounds belong in the contract

Codex shares a concurrency guard across clones and caps waits. DeepSeek checks
depth monotonically across resume and bounds result diagnostics. Every mature
implementation separates normal completion, cancellation, and failure.

The first slice here needs fixed task-input and result limits, one child per
delegation call, depth exactly one, caller-context cancellation, the existing
Turn/Step bounds inside the child, and a composition-level timeout. Synchronous
execution means the existing parent tool loop supplies the concurrency bound:
one running child per parent Turn and no background task survives it.

## Fit with this repository

- The charter excludes A2A, remote daemons, and distributed multi-agent work,
  but explicitly does not exclude local in-process delegation.
- `application.Service` already owns Session creation and Turn execution, so a
  fresh child can reuse the real production path without a second engine.
- Session-scoped observed-file state naturally isolates what parent and child
  have read.
- The Tool Catalog, Policy, durable Tool Call events, redaction, Context Engine,
  and traces already provide the containing pipeline.
- The model request cache is affected only when the opt-in static tool schema
  is enabled. No per-Turn roster or timestamp is injected into the prefix.

## Rejected first-slice shapes

| Shape | Why rejected now |
| --- | --- |
| Native OTel metrics instead | Existing spans can produce RED metrics in the Collector; the accepted trace design explicitly avoids duplicating them. |
| Spawn another `och` over ACP | Strong isolation, but it duplicates client transport inside the Harness or violates the enforced client-tree boundary, requires credential/environment forwarding, and pays process startup per task. |
| Background `spawn` plus `wait` tools | Requires a durable task registry, ownership transfer, notification delivery, shutdown policy, and crash recovery before it can tell the truth. |
| Parent-history fork | Couples v1 to checkpoint, compaction, usage-anchor, and instruction-prefix reconstruction rules. |
| Child write/exec/MCP access | Requires a nested human-approval route and a policy for concurrent workspace side effects. |
| A generic provider/plugin seam | There is one concrete in-process consumer today; the charter forbids extension points without consumers. |

## Questions the normative design must freeze

1. Exact durable parent/child linkage and whether it is one event or two.
2. The opt-in configuration and stable tool schema.
3. Task/result byte limits and timeout bounds.
4. How child-only tool filtering is enforced even if a model invents a hidden
   call by name.
5. Cancellation and partial-child failure mapping into the parent Tool Result.
6. Whether a child failure is an ordinary tool failure or terminates the parent
   Turn; the reference precedent favors an ordinary, safe tool failure.
7. What ACP and transcript consumers see without adding a new client protocol.

## Evidence limits

This gate reads placement, lifecycle, limits, and failure semantics. It does
not claim that any reference implementation is correct or safe, and it does
not copy their schemas or prompts. The proposed slice has not yet been
implemented or validated with a real model.
