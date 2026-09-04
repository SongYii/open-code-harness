# Versioned System Prompt and Append-Only Workspace Instructions Design

**Status:** Draft for written review; architecture accepted in conversation on 2026-09-04

**Date:** 2026-09-04

**Stability:** Prompt format, instruction events, and configuration remain `internal` before v1.0.

**Research basis:** [Agent instructions and safe file mutation architecture gate](../../research/architecture-gates/2026-09-04-agent-instructions-and-file-mutation.md)

English is normative. The
[Chinese file](2026-09-04-system-prompt-workspace-instructions-design.zh-CN.md)
is a synchronized reading copy.

## Problem

The runtime has provider-neutral request recording and durable Context Engine
projection, but no explicit coding-agent prompt identity or workspace
instruction lifecycle. Simply re-reading `AGENTS.md` and replacing an early
system message on every turn would detect changes while invalidating provider
prefix-cache reuse from the first changed token. Loading instructions only once
would preserve caching while silently ignoring legitimate repository changes.

## Goals

- Ship one explicit, versioned, model-neutral coding-agent system prompt.
- Load deterministic hierarchical `AGENTS.md` instructions inside the admitted workspace.
- Detect additions, replacements, and removals without rewriting earlier request bytes.
- Persist the exact instruction change before any request that consumes it.
- Preserve exact request reconstruction, Context Engine bounds, restart, and compaction semantics.
- Make prompt-token and KV-cache effects explicit and measurable, including a DeepSeek-compatible live validation lane.

## Non-goals

- User-home/global instruction files, `CLAUDE.md`, `GEMINI.md`, arbitrary filenames, includes, executable directives, or remote instruction sources.
- Semantic merging of prose or parsing instructions into permissions.
- Per-provider prompt forks in the first version.
- Treating repository instructions as trusted authorization. Policy and Approver remain authoritative.
- Implementing long-term memory, skills, MCP, or a TUI.

## Request layout

Every request uses this stable order:

```text
system: och_coding_agent_v1 (fixed bytes and digest)
developer/context: root AGENTS.md baseline, if present
existing canonical conversation and tool history
developer/context: zero or more append-only instruction deltas
current user/tool continuation
```

The exact provider role used for the provider-neutral developer/context item is
resolved by the existing adapter capability contract; it must not be flattened
into user-authored text. The fixed prompt contains tool discipline, workspace
scope, stale-file recovery, policy/approval precedence, concise progress rules,
and a ban on claiming unverified success. It does not contain model names,
credentials, mutable timestamps, Session IDs, or workspace-specific text.

The prompt document has a stable ID, semantic version, exact UTF-8 bytes, and
SHA-256 digest. A byte change requires a version/digest change and tests. There
is no “latest text” assembled from ambient strings.

## Discovery and hierarchy

Only files named exactly `AGENTS.md` are recognized. Discovery never escapes
the admitted workspace.

- At session creation/first preparation, the workspace-root `AGENTS.md` is the baseline. Its confirmed absence is also recorded.
- When a structured filesystem tool resolves a target, Application discovers the directory chain from workspace root through the target's parent.
- At most one `AGENTS.md` per directory participates. Instructions are ordered shallow to deep; a deeper file is later and therefore has higher precedence for its subtree.
- A directory instruction applies only to targets in that directory subtree. Tool guidance names the active scope; the prose is not converted into a capability grant.
- Symlinks are evaluated through the existing workspace jail. A canonical target outside the workspace is rejected.

Discovery and reads use the safe filesystem observation primitives from the
preceding module. An unreadable, non-regular, invalid-UTF-8, or over-budget
instruction file fails request preparation closed with a structured error.

## Change detection and append-only deltas

Application keeps a runtime registry for every discovered instruction path:
scope, present/absent state, opaque file version, content digest, and last
accepted bytes. At each provider-preparation boundary it rechecks the root and
all paths already discovered for the active route. A newly relevant directory
chain is checked before the first provider request following the tool action
that discovered it.

Differences become one ordered delta with operations:

- `add(path, scope, digest, content)`;
- `replace(path, scope, priorDigest, digest, content)`;
- `remove(path, scope, priorDigest)`.

The rendered delta is appended after prior model-visible history. It states the
new effective instruction set for affected scopes and explicitly marks removed
or superseded content as no longer authoritative. It never edits a previously
recorded message. Multiple changes observed at one boundary are sorted by
normalized scope depth and path and recorded together. No-change checks append
nothing.

This is the cache decision: scanning each preparation does not itself change
the request. When an instruction changes, only a new suffix is added, so the
unchanged earlier prefix remains cacheable. Rewriting the original system or
baseline message is forbidden.

## Durability and reconstruction

Before provider dispatch, Application appends one
`workspace.instructions.recorded` fact containing:

- format version and prompt ID/digest;
- normalized workspace-relative paths and scopes;
- ordered structured operations and old/new content digests;
- the exact bounded rendered developer/context message bytes;
- the resulting effective-instruction-set digest.

Only after that append resolves may `model.request.recorded` be constructed and
persisted. The latter remains the exact dispatched envelope and therefore the
final reconstruction authority. If either append has unknown outcome, existing
resolve-before-effect rules apply; no provider request may be repeated or sent
without resolving the durable fact.

Instruction content is repository-controlled but untrusted. It is bounded,
delimited, and labelled separately from harness policy. It cannot change tool
risk, approval mode, workspace admission, sandboxing, credentials, or event
authority.

## Compaction and restart

Append-only deltas cannot grow forever. When Context Engine covers instruction
messages, the checkpoint carries an instruction rebase record: exact sorted
effective files/scopes/bytes/digests, system-prompt identity, source-event
coverage and aggregate digest, and one deterministic rendered snapshot.
Materialization uses that snapshot plus later deltas and does not resend
superseded pre-checkpoint messages. Canonical events are never rewritten.

On restart, the durable event/checkpoint projection reconstructs the last model-
visible instruction state. Before the next provider dispatch, Application
rechecks the root and discovered paths against the live workspace. Any change
becomes a new durable delta. Previously undiscovered subtrees remain
undiscovered until a tool touches them.

## Bounds and failure semantics

Configuration fixes maximum participating files, bytes per file, aggregate
instruction bytes, discovery depth, and rendered-delta bytes. Defaults are part
of the implementation plan and must fit inside the Context Engine's input
budget, not create a second unmetered allowance.

Exceeding any bound, encountering an unstable read/version during preparation,
or failing durable recording aborts before provider dispatch. Errors identify
the path and class but do not include uncontrolled file content. There is no
best-effort subset and no silent truncation because either makes precedence
ambiguous.

## Cache and token evidence

Acceptance evidence distinguishes the fixed prompt/baseline cost, zero request-
byte change for a no-change rescan, append-only suffix cost for changes,
compaction rebase cost/reclaimed tokens, and provider-reported cached versus
uncached input tokens where available.

A live lane may use a user-supplied DeepSeek OpenAI-compatible credential, but
credentials are read only after explicit consent and deterministic checks. The
live result is evidence, never an ordinary PR requirement. Offline tests use a
recording fixture adapter and exact request bytes.

## Verification and acceptance

Acceptance requires:

- golden bytes and digest for the versioned system prompt;
- root presence/absence and shallow-to-deep precedence;
- nested discovery only after a target in that subtree is touched;
- deterministic add/replace/remove ordering and no event/message on no change;
- unchanged prefix bytes across a delta-producing request;
- fail-closed invalid UTF-8, unstable read, symlink escape, bounds, and durable-append failures;
- instruction text cannot grant a denied tool or bypass approval;
- exact `workspace.instructions.recorded` then `model.request.recorded` ordering;
- restart reconstruction followed by live recheck;
- compaction rebase equivalence and bounded long-session growth;
- race tests for concurrent tool discovery and provider preparation;
- paired offline Application and ACP scenarios;
- an optional consent-gated DeepSeek live sample reporting request tokens,
  cached-token evidence when exposed, and exact prompt/delta digests.

Completion also requires an implemented contract, Chinese reading copy,
evidence ledger, and explicit prompt-change procedure. The implementation plan
follows only after written review of this design.
