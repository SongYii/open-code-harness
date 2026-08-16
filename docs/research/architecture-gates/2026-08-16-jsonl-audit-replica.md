# JSONL Audit Replica Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-16

**Scope:** Slice 3 (JSONL audit replica and import) primary-source
re-verification: export formats and framing, publication consistency,
import validation, indexes and manifests, and the transactional outbox
pattern.

This document is research evidence. It does not change the accepted design
and does not authorize copying reference-project formats.

English is the normative research record. The Chinese file is a synchronized
reading copy.

## Questions

1. Does the reference set confirm the accepted replica design —
   transactional outbox, batch envelopes with a hash chain, immutable
   sealed segments, manifest generations, verified import into new
   databases only?
2. Which observed publication and repair mechanics should Slice 3 adopt?
3. Which observed import and tolerance behaviors conflict with the charter?

## Re-verified primary sources

| Source | Observed state |
| --- | --- |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | `47f9438`, 2026-08-13 |
| [OpenAI Codex](https://github.com/openai/codex) | `9ded177`, 2026-08-16 |
| [Kimi Code](https://github.com/MoonshotAI/kimi-code) | `84da662`, 2026-08-16 |
| [Pi agent core](https://github.com/badlogic/pi-mono) | `d3ab2af`, 2026-08-16 |
| [Grok Build](https://github.com/xai-org/grok-build) | `5163763`, 2026-08-15 |
| [Transactional outbox](https://microservices.io/patterns/data/transactional-outbox.html) | pattern authority |

## Observed contracts and boundary

| Source | Observed contract | Slice 3 decision | Boundary |
| --- | --- | --- | --- |
| microservices.io | Write outbox messages "as part of the transaction that updates the business entities"; a relay publishes asynchronously preserving order; delivery is at-least-once so "a message consumer must be idempotent, perhaps by tracking the IDs". | Confirms the outbox-in-append-transaction design and idempotent-by-range-and-digest publication already implemented. | — |
| Codex | Rollout migration state machine: "canonicalize into a staged JSONL file, project that staged file into SQLite, verify the projection, then atomically publish it... we always leave behind either the original legacy rollout or a recoverable paginated rollout". | Confirms verify-then-publish ordering: our staging → sync → reopen → verify → seal sequence. | — |
| Codex | Import dedup by content hash with an import ledger; "Name updates are append-only; the most recent entry wins" for `session_index.jsonl`. | The ledger idea is recorded for future import-resume behavior; not Slice 3 scope (our import is one transaction into a new database). | Codex import "trusts source files (no signature/verify step observed)" — rejected; our import verifies eight layers before landing anything. |
| DeepSeek Harness | JSONL "stored as checksummed concatenated Zstandard frames by default or raw lines by configuration"; "append resolves only after durability"; a caught write failure "rolls the file back to its prior byte length". | Confirms checksummed framing as the default posture; our per-line envelope digests plus segment/manifest digests cover the same ground losslessly. | Their raw-lines configuration has "no checksums" — rejected as a mode; we always digest. |
| DeepSeek Harness | Torn-tail handling: "only a torn final record is discarded"; format refusal is "distinct from SessionPersistenceCorruptionError because nothing is damaged". | Adopted for import: a torn final line of a staged import is refused (we never silently discard), and format-version refusal stays distinct from corruption. | — |
| Pi | Atomic publish: "Build a complete sibling temporary file, then atomically rename it over the destination... the destination is untouched until the rename commits"; torn tail repaired by "atomically publishing the valid prefix". | Confirms staging+rename publication for manifests. Segment sealing already uses write-sync-rename. | Prefix-republish repair belongs to a live-writer model; our sealed segments are immutable and regeneration replaces them. |
| Kimi Code | Index appends rely on POSIX single-write atomicity ("well under PIPE_BUF"); malformed index lines are skipped on read. | Single-write atomicity is insufficient for us (segments are multi-line); rejected. | Fail-open line skipping on read is rejected everywhere, including import. |
| Grok Build | `FlushAndAck` resolves "only after `flush_pending()` finishes writing to disk"; destructive rewrites are backup-gated: "back up the on-disk history first, and only rewrite if the backup landed: recoverability gates the destruction". | The ack-after-durability barrier matches our exporter checkpoint semantics. | Full-history replacement rewrites (their compaction strip) contradict immutable audit segments — rejected. |

## Rejected shapes

1. **Unverified import** (Codex external migration trusts its sources):
   every imported line passes the eight verification layers before the
   import lands.
2. **Fail-open line skipping on read** (Kimi wire-scan): malformed lines
   refuse or quarantine; they are never silently dropped.
3. **Optional checksums** (DeepSeek Harness raw-lines mode): digesting is
   always on.
4. **Full-history replacement rewrites of the replica** (Grok Build strip):
   sealed segments and manifest generations are immutable; damage triggers
   quarantine and regeneration from canonical bytes.
5. **Single-write atomicity as the durability story** (Kimi index appends):
   multi-line artifacts are staged, synced, verified, then published.

## Findings

### F1. The accepted replica design is confirmed

The transactional outbox authority plus the verify-then-publish and
backup-gated-destruction patterns across the reference set match the
implemented append-transaction integration, staged publication, and
immutable generations.

### F2. Adopt list

Digests always on; staging → sync → reopen → verify → rename publication;
idempotent at-least-once delivery by range and digest; format refusal
distinct from corruption; torn-final-line refusal on import.

### F3. Reject list

Unverified import; fail-open skipping; optional checksums; replica
rewrites; single-write atomicity claims over multi-line files.

### F4. Recorded for later

Codex's import ledger and content-hash dedup for resumable imports; Grok
Build's disk-full latch is already covered by the Slice 2 classification
tests.

## Evidence limits

- Observations are point-in-time at the listed commits; no runtime testing
  of reference projects was performed.
- Pi's repository moved (badlogic/pi-mono → earendil-works/pi); links may
  need updating at the next gate.
- This gate implements nothing; the Slice 3 specification, plan, and
  evidence ledger cite it.
