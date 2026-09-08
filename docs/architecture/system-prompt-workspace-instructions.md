# Versioned System Prompt and Workspace Instructions

**Status:** Implemented contract. This document describes behavior present in
the repository and covered by tests. The accepted design is
[Versioned system prompt and append-only workspace instructions](../superpowers/specs/2026-09-04-system-prompt-workspace-instructions-design.md),
the implementation plan is
[here](../superpowers/plans/2026-09-08-system-prompt-workspace-instructions.md),
and completion evidence is recorded in
[system-prompt-workspace-instructions-evidence.md](system-prompt-workspace-instructions-evidence.md).

The synchronized Chinese reading copy is
[system-prompt-workspace-instructions.zh-CN.md](system-prompt-workspace-instructions.zh-CN.md).
If the copies diverge, this English document wins.

## Contract in one paragraph

Every Context-enabled conversation request starts with one harness-owned,
versioned system prompt. Repository-owned `AGENTS.md` files are discovered
from the workspace root and from directory ancestors reached by successful
structured file tools. Their effective state is recorded as append-only
`set/replace/remove` deltas before the next provider request. An unchanged
file adds no event or message, so prior request bytes remain a stable prefix.
Summary and reset checkpoints replace covered deltas with one validated
effective-set snapshot. Repository instructions are guidance, never
authorization, and are never sent to the Context summarizer.

## Fixed system prompt

The prompt asset is
`internal/harness/agentinstructions/prompts/och_coding_agent_v1.md`.
`internal/harness/agentinstructions/prompt.go` binds it to `PromptID`,
`PromptVersion`, and `PromptDigest`; `SystemPromptMessage` returns it as the
first system-role message. The prompt has no session, workspace, provider,
date, credential, or other request-specific interpolation.

Legacy callers that do not enable the Context Engine keep their historical
request contract. Context-enabled conversation calls get the fixed prompt;
Context summarization calls retain their separate summary prompt.

### prompt-change procedure

A prompt change is one reviewed unit. Change all of the following together:

1. the prompt asset;
2. `PromptVersion` when semantics change;
3. `PromptDigest` to the SHA-256 of the exact embedded bytes;
4. the golden assertions in
   `internal/harness/agentinstructions/prompt_test.go`;
5. this implemented contract and its evidence ledger.

Changing only prose, only a version, or only a digest is an incomplete
change. The golden test also rejects provider-specific and dynamic markers.

## Discovery and precedence

`AGENTS.md` is the only recognized filename. Paths are normalized,
workspace-relative slash paths. The root candidate is always considered.
After a successful `read_file`, `write_file`, `edit_file`, or `list_dir`, the
candidate chain for the touched directory is considered before another model
request. `exec` and MCP calls do not drive discovery.

Effective sources are ordered broad-to-specific by scope depth, then by path.
More specific files therefore appear later. At most 256 paths may be
discovered, each source is at most 1 MiB, and one rendered instruction message
is at most 64 KiB. When rendering must shed content, broader sources are
omitted before the most specific source; diagnostics disclose omission or
truncation.

## Observation and reconciliation

For each session, Application keeps a process-local probe table. A
`tools.FileVersion` is a fast equality hint only. When the version changes—or
after restart, when the probe table is empty—the source is read and a content
SHA-256 decides identity. A same-digest rewrite is suppressed. A confirmed
absence is distinct from a transient read failure: absence may create a
remove delta; failure retains the last good effective source and emits one
deduplicated diagnostic episode.

The durable instruction state, rather than the probe table, is replayed after
restart. A fresh process always rechecks disk before trusting it.

## Durable event and request order

`workspace.instructions.recorded` carries prompt identity, epoch, newly
discovered scopes, typed changes, diagnostics, the deterministic rendered
message, and the effective-set digest. Every non-empty batch increments the
epoch exactly once. The event is committed before any model request that
uses it.

Requests are assembled as:

1. fixed system prompt;
2. checkpoint summary/reset marker, when present;
3. checkpoint instruction snapshot, when present;
4. retained conversation tail and later instruction deltas in canonical
   sequence order;
5. current user input.

With no instruction change, earlier messages remain byte-identical. A change
adds one suffix delta; it never rewrites the historical prefix. This is the
cache-stability guarantee. It does not promise that a changed instruction
will hit a provider cache—the changed suffix is intentionally new.

## Checkpoint rebase and recovery

Rolling-summary and deterministic-reset checkpoints snapshot the effective
instruction set at their covered-through sequence. The snapshot contains the
prompt identity, epoch, covered sequence, sorted sources, exact rendered
message, and aggregate source digest. The summary model sees conversational
units only, never instruction framing or repository prose.

Materialization places the snapshot after the summary/reset marker and before
the retained tail. Snapshot and fixed-prefix tokens participate in fit,
non-shrinking, and hard-input checks. Domain decode verifies path ordering,
per-source content digests, aggregate digest, and sequence agreement;
Application additionally regenerates the rendered snapshot and checks the
active prompt identity. Memory returns owned copies. SQLite reload and index
rebuild use the canonical completion event, so no schema migration is needed.

## Security and failure semantics

Repository prose is JSON-escaped inside a harness-owned marker and explicitly
labeled as non-authoritative. It cannot grant approval, widen the workspace,
change sandbox or credential policy, or change tool risk. The exact prose is
present in canonical events and the content-bearing transcript by design; it
is not copied into summarizer prompts.

Invalid UTF-8, invalid paths, oversized sources, excess discovered paths, bad
digest chains, inconsistent snapshots, and prompt-identity drift fail closed.
Transient filesystem observation failures retain the last good state instead
of converting uncertainty into removal.

## Exclusions

- No Windows-specific runtime claim is made by this module.
- `exec` and MCP do not discover nested instruction files.
- There is no provider-managed prompt-cache API contract or guaranteed cache
  hit rate; the module guarantees stable request bytes where inputs are
  unchanged.
- A live-model quality or prompt-injection-resistance claim requires separate
  evaluation. Keyless deterministic tests do not make that claim.

