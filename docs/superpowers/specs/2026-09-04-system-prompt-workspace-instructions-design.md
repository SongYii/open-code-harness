# Versioned System Prompt and Append-Only Workspace Instructions Design

**Status:** Accepted on 2026-09-05. Refined and implementation authorized on
2026-09-07. The refinement fixes the model-visible role to durable `user`
messages (the provider-neutral role the current domain can reconstruct), keeps
the last confirmed state on transient source failures, and pins the v1 bounds
and over-budget selection policy. These choices replace the earlier
`developer/context`, fail-closed-source, and all-or-nothing-budget wording
below rather than creating a competing design.

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
user/context: root AGENTS.md baseline, if present
existing canonical conversation and tool history
user/context: zero or more append-only instruction deltas
current user/tool continuation
```

Workspace instruction messages use `domain.PromptRoleUser` because it is a
portable, reconstructable role in the current provider-neutral domain. Harness-
owned framing distinguishes them from direct user input, states their scope,
and says explicitly that they cannot override system, operator, policy, or
direct user authority. The fixed prompt teaches that framing and contains tool
discipline, workspace scope, stale-file recovery, policy/approval precedence,
concise progress rules, and a ban on claiming unverified success. It does not
contain model names, credentials, mutable timestamps, Session IDs, or
workspace-specific text.

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
preceding module. The final candidate must be a regular UTF-8 file whose
canonical resolution stays inside the workspace. A final-component symlink to
an outside target is rejected by the existing workspace jail; v1 does not load
workspace-external instruction content.

## Change detection and append-only deltas

Application keeps a runtime registry for every discovered instruction path:
scope, present/absent state, opaque file version, content digest, and last
accepted bytes. At each provider-preparation boundary it rechecks the root and
all paths already discovered for the active route. A newly relevant directory
chain is checked before the first provider request following the tool action
that discovered it.

Differences become one ordered delta with operations:

- `set(path, scope, digest, content)`;
- `replace(path, scope, priorDigest, digest, content)`;
- `remove(path, scope, priorDigest)`.

The rendered delta is appended after prior model-visible history. It states the
new effective instruction set for affected scopes and explicitly marks removed
or superseded content as no longer authoritative. It never edits a previously
recorded message. Multiple changes observed at one boundary are sorted by
normalized scope depth and path and recorded together. A newly observed source
failure may instead append a diagnostic-only event with no operation and an
unchanged effective-set digest. No-change checks with neither an operation nor
a new failure episode append nothing. The same path/failure class is reported
once per continuous in-process episode; one complete successful probe clears
that suppression, and a restarted process begins a new episode.

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
- ordered bounded source diagnostics, which may exist with no operation;
- the exact bounded rendered user/context message bytes;
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

Append-only deltas cannot grow forever. Instruction events are excluded from
the prose supplied to the summarizer: repository text must not be paraphrased
into policy. When Context Engine covers instruction messages, the completed
checkpoint carries an instruction rebase record: exact sorted effective
files/scopes/bytes/digests, system-prompt identity, source-event coverage and
aggregate digest, instruction epoch, and one deterministic rendered snapshot.
Materialization orders the fixed system prompt, checkpoint summary or reset
marker, rebased instruction snapshot, retained conversation tail, later
instruction deltas, and current input. It does not resend superseded pre-
checkpoint messages. Canonical events are never rewritten.

On restart, the durable event/checkpoint projection reconstructs the last model-
visible instruction state. Before the next provider dispatch, Application
rechecks the root and discovered paths against the live workspace. Any change
becomes a new durable delta. Previously undiscovered subtrees remain
undiscovered until a tool touches them. The checkpoint snapshot digest is
validated during replay; a corrupt or inconsistent snapshot fails recovery
rather than inventing an effective state.

## Bounds and failure semantics

V1 pins two independent limits rather than coupling instruction behavior to an
unrelated tool or provider constant:

- one source read is bounded at 1 MiB;
- the rendered effective instruction context in one request is bounded at 64
  KiB, and remains subject to the Context Engine's ordinary input budget;
- one session tracks at most 256 discovered instruction paths. The root path
  reserves one slot; excess newly discovered paths are skipped with a diagnostic.

When the effective set exceeds 64 KiB, rendering preserves the most specific
sources first, drops whole broader sources before truncating the most-specific
remaining source, and emits a model-visible and audited diagnostic naming every
omitted or truncated path. The resulting bytes, ordering, and diagnostic are
deterministic. There is no silent omission and no second unmetered allowance.

Confirmed absence is the only observation that removes prior instructions.
Unreadable, non-regular, invalid-UTF-8, over-1-MiB, canceled, or unstable reads
are temporarily unavailable: Application retains the last confirmed effective
state, records a bounded path/class diagnostic, and retries at the next
preparation boundary. If there is no prior accepted state, that source
contributes no instruction bytes until a complete valid read succeeds. A
failure for one source does not suppress independently confirmed changes for
other sources in the same batch. Durable-recording failure still aborts before
provider dispatch, and diagnostics never include uncontrolled source content.
The 256-path limit applies before a candidate is read and does not evict an
already accepted source.

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
- deterministic set/replace/remove ordering, diagnostic-only episode
  deduplication, and no event/message on a genuinely unchanged healthy scan;
- unchanged prefix bytes across a delta-producing request;
- opaque `FileVersion` as a read-avoidance hint and digest as the authoritative
  content identity, including version changes with an unchanged digest;
- confirmed absence removes prior state while transient read, invalid UTF-8,
  non-regular, oversize, cancellation, and unstable-version failures retain it;
- deterministic 1 MiB source and 64 KiB aggregate bounds, specific-first
  omission/truncation diagnostics, symlink escape rejection, and fail-closed
  durable-append failures;
- harness framing cannot be escaped by instruction text containing its closing
  marker;
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
