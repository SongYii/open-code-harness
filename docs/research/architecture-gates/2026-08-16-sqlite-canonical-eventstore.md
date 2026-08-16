# SQLite Canonical EventStore Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-16

**Scope:** Slice 2 (SQLite canonical EventStore) primary-source
re-verification. Records the then-public persistence contracts of the required
comparison set, the adopt/reject boundary for the SQLite adapter gate, and the
sequencing confirmation that Slice 2 is the next implementation slice after
the Tool Runtime contract landed.

This document is research evidence. It does not change EventStore v2 behavior
and does not authorize copying reference-project types, schemas, or runtime.

English is the normative research record. The Chinese file is a synchronized
reading copy.

## Questions

1. After the Provider adapter and Tool Runtime contracts landed, is Slice 2
   (SQLite canonical EventStore) the correct next implementation slice?
2. Does the accepted runtime design's Slice 2 scope — migrations,
   transaction/CAS, exact retry, fencing, projections, backup — remain correct
   against the re-verified public implementations?
3. Which observed persistence contracts should the SQLite adapter adopt, and
   which conflict with the charter or accepted designs?
4. On re-verification one day after the 2026-08-15 comparison, what does the
   official DeepSeek Harness persistence seam contribute, and where is it
   insufficient for a canonical store?
5. Which subsystem-specific authoritative sources must join the comparison set
   for SQLite semantics?

## Re-verified primary sources

All observed from official repositories on the listed dates. Commits are the
observed state, not endorsements.

| Source | Observed state | Persistence-relevant entry points |
| --- | --- | --- |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | developer preview, MIT, TypeScript/Cordis, 2026-08-16 | `docs/subsystems/persistence.md`, `docs/subsystems/session.md` |
| [OpenAI Codex](https://github.com/openai/codex) | `73abda8`, 2026-08-16 | `codex-rs/rollout`, `codex-rs/state` (SQLite), `codex-rs/thread-store` |
| [Kimi Code](https://github.com/MoonshotAI/kimi-code) | `6b72345`, 2026-08-15 | `packages/agent-core` session store, records persistence, `docs/en/guides/sessions.md` |
| [Grok Build](https://github.com/xai-org/grok-build) | `5163763`, 2026-08-15 | `xai-grok-shell/src/session/persistence.rs`, `xai-sqlite-journal` |
| [Pi agent core](https://github.com/badlogic/pi-mono) | `086c32e`, 2026-08-15 | `packages/agent` session log, JSONL storage, conformance suite |
| [Maka](https://github.com/maka-agent/maka-agent) | `2666a57`, 2026-08-16 | `ARCHITECTURE.md`, runtime-resume architecture |
| [SQLite WAL documentation](https://www.sqlite.org/walformat.html) | subsystem authority | write-ahead log format and concurrency semantics |

[DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) remains
community, non-authoritative context. Marketing pages, unofficial mirrors, and
plugin galleries are not primary evidence.

## Ecosystem convergence

Every re-verified project, regardless of language or architecture, converges
on the same persistence shape. This is the strongest evidence in this gate:

1. An append-only fact/event log is the sole authority; state is derived by
   replay, never stored as a second mutable transcript.
2. Compaction is a new fact or record that shadows earlier facts from a
   projection; canonical rows are never rewritten or deleted.
3. A torn final line from a crashed write is repaired or discarded; corruption
   before the tail is a hard error, never silently skipped.
4. A crash with an open boundary is closed synthetically on recovery; durable
   work is not truncated and model/tool effects are not silently replayed.
5. Unsupported format versions are refused as a distinct condition from
   corruption.
6. Indexes, summaries, and query structures over the log are rebuildable
   projections, never a second authority.

The charter and EventStore v2 already assert all six. Slice 2 does not
re-litigate them; it implements them over SQLite.

## Observed contracts and boundary

| Source | Observed contract | Slice 2 decision | Boundary |
| --- | --- | --- | --- |
| DeepSeek Harness | One `SessionPersistence` seam; SQLite stores one row per `SessionEvent` verbatim with fields mapping 1:1, "no parallel persisted event type"; `SCHEMA_VERSION` pragma gates structure before other checks. | Adopt verbatim row-per-event storage and a pragma-gated schema version check that refuses newer versions with an upgrade-direction error. | Do not copy their column names or seam shape; our Store port, AppendID, and receipt stay store-owned per EventStore v2. |
| DeepSeek Harness | Persistence is async batched flush after in-memory append; `append` resolves only after durability; failed background writes pause auto-retry. | Reject as commit authority. Our durable append is the online fact; terminal facts commit before delivery. | Batched I/O inside one SQLite transaction is an optimization, never a deferral of the commit boundary. |
| DeepSeek Harness | JSONL backend stores checksummed concatenated Zstandard frames by default, raw lines by configuration. | Record as the leading candidate for the Slice 3 audit export envelope. | JSONL is not a live authority in any slice. |
| DeepSeek Harness | Abandoned sessions "leave nothing behind" (lazy materialization); revision tokens change transactionally and are equality-compare only. | Adopt both. | — |
| DeepSeek Harness | Crash repair appends synthetic closure only to cold sessions; live sessions reject repair; only "a torn final record is discarded". | Confirms our reconciliation posture; full detail belongs to Slice 4. | — |
| Codex | SQLite state database with `journal_mode(WAL)`, `synchronous(Normal)`, `busy_timeout(5s)`, 48+ ordered SQL migrations, transactions via connection pool. | Adopt WAL discipline, explicit synchronous mode, busy timeout, and ordered versioned migrations as the Slice 2 baseline. | Do not adopt their dual JSONL+SQLite live surfaces; see rejection R3. |
| Codex | Cross-process writer exclusion is advisory file locks; no fencing tokens; open issue [#36869](https://github.com/openai/codex/issues/36869) documents a writer-lock bypass. | Direct primary evidence that advisory locks alone are insufficient. Slice 2 store design and Slice 4 leases require fencing tokens. | Their bug is evidence, not a pattern to copy. |
| Codex | `codex migrate-rollouts` migrates legacy JSONL into SQLite-backed paginated history; `LocalThreadStore` "persists history through `codex-rollout` JSONL files and persists queryable metadata through the SQLite state database". | Validates the SQLite-canonical sequencing: they are migrating toward what we are building first. | — |
| Codex | On read, unparseable rollout lines are skipped with a counter: "failed to parse line as JSON ... continue". | Reject. Fail-open skipping violates our fail-closed unknown-schema boundary and DeepSeek Harness's own refusal stance. | Any Slice 3 import must refuse or quarantine, never silently skip. |
| Grok Build | `DurableAppendError::{NotCommitted, Committed, AcknowledgementLost}` — commit-outcome triad where acknowledgement loss is an explicit unknown state. | Independent confirmation of the EventStore v2 append error algebra. Keep ours; implement the triad over SQLite transactions. | Do not copy error names. |
| Grok Build | Single actor serializes all session writes; `FlushAndAck` resolves "only after `flush_pending()` finishes writing to disk"; rewrites back up first and a failed backup gates off the rewrite. | Adopt the ack-after-durability barrier and backup-gated destructive operations for migration and backup tooling. | — |
| Grok Build | SQLite journal mode is WAL locally but TRUNCATE over NFS because "WAL does not work over a network filesystem"; those SQLite databases "are all rebuildable indexes/caches". | Adopt explicit journal-mode selection with documented NFS degradation, and the rebuildable-index role for projections. | — |
| Grok Build | Disk-full latch on `ENOSPC`/`EDQUOT` with a health probe. | Adopt as a required resource-limit test class for Slice 2. | — |
| Kimi Code | Batched appends with per-batch `fsync` and one parent-directory fsync on creation; truncated trailing line tolerated, everything else hard-fails. | Adopt fsync-before-receipt and directory fsync on database creation. | Reject their sticky poisoned-persistence behavior after one error; our error algebra classifies and permits exact retry. |
| Pi | One conformance suite pins the in-memory and JSONL backends to a single contract; torn tail is repaired by atomically republishing the valid prefix. | Confirms our `eventstoretest` shape; the SQLite adapter must pass the same suite plus adapter-specific fault injection. | — |
| Pi | Bounded open-operation repair (`limit: 2` dangling opens) matches single in-flight operation plus predecessor. | Record as the reconciliation bound posture for Slice 4 startup reconciliation. | — |
| Maka | "We store only the facts... Projections, such as UI threads, are derived views that can always be rebuilt"; "Resume Is Not Retry"; compaction "does not modify or delete canonical RuntimeEvents". | Adopt the vocabulary and invariants: projections rebuildable, resume is fact-driven. | Maka has no database machinery; it contributes invariants, not implementation. |

## Rejected shapes

1. **Fail-open corruption skipping** (Codex rollout reads). Unknown or
   unparseable entries must refuse or quarantine; a counter is not a contract.
2. **Async flush as commit authority** (DeepSeek Harness). Commit is the
   online fact.
3. **JSONL and SQLite as peer live authorities** (DeepSeek Harness peer
   backends; Codex dual surfaces, with the rename inconsistency recorded in
   issue [#16405](https://github.com/openai/codex/issues/16405)). SQLite is
   the sole commit authority; JSONL is Slice 3 audit and import.
4. **Advisory-lock-only writer exclusion** (Codex, with documented bypass).
   Fencing tokens are required wherever a lock or lease guards writes.
5. **Sticky poisoned persistence** (Kimi Code error handling). A classified
   error algebra with exact-retry resolution replaces it.
6. **Projection visibility without store replay** (Pi header snapshots entries
   at open; later entries "validated, but not replayed"). Our projections
   derive from the store at read time.
7. **Copying reference schemas, type names, plugin seams, or migration
   numbering.** Reference projects are not dependencies and donate nothing.

## Findings

### F1. Slice 2 is the correct next slice

The 2026-08-15 sequencing said SQLite, recovery, ACP, and TUI resume "after
the tool-using loop contract exists". The Tool Runtime contract is implemented
and verified. Nothing observed in this gate changes that order.

### F2. The accepted Slice 2 scope is confirmed and strengthened

Ecosystem convergence (six shared contracts) plus Codex's own JSONL→SQLite
migration validate the design. The gate adds two explicit test classes:
disk-full latch behavior and journal-mode selection under NFS.

### F3. Fencing is necessary, not gold-plating

No re-verified reference implements fencing tokens. Codex's open writer-lock
bypass issue is direct evidence that single-machine advisory locking is the
failure mode our accepted design already guards against.

### F4. Adopt summary

Verbatim row-per-event storage; pragma-gated versioned migrations with
upgrade-direction refusal; WAL plus explicit synchronous mode, busy timeout,
and journal-mode selection; the commit-outcome triad over SQLite transactions;
fsync-before-receipt; backup-gated destructive operations; rebuildable
SQLite-resident projections; disk-full latch as a tested resource limit.

### F5. Reject summary

Fail-open skipping; async-flush authority; dual live stores;
lock-without-fencing; sticky poisoned persistence; header-frozen projection
visibility; copying reference shapes.

### F6. DeepSeek Harness boundary holds on re-verification

The 2026-08-15 adopt/reject table remains correct one day later. Its
persistence seam contributes format refusal, crash closure, torn-tail, and
lazy-materialization contracts. Its SQLite backend documents no WAL,
transaction, CAS, or fencing discipline — the industrial core of Slice 2 must
come from our accepted design plus SQLite's own semantics, not from DeepSeek
Harness shapes.

### F7. Sequencing detail absorbed into later slices

Checksummed compressed audit frames and torn-tail import rules are recorded
for the Slice 3 gate; synthetic crash closure and bounded reconciliation are
recorded for the Slice 4 gate. This gate neither designs nor implements them.

## Evidence limits

- DeepSeek Harness is a developer preview that states compatibility will
  break; the absence of WAL/transaction/CAS documentation is an absence of
  documentation, not a verified absence in code.
- Observations are point-in-time at the listed commits and dates. No runtime
  testing, fuzzing, or benchmarking of reference projects was performed.
- Unpublished invariants remain unknown. Issue links document reported
  defects at observation time, not fixed states.
- This gate implements nothing. The Slice 2 specification, plan, and evidence
  ledger follow separately and cite this document.
