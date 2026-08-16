# JSONL Audit Replica and Import (Slice 3)

**Status:** Draft pending architecture-gate evidence

**Date:** 2026-08-16

**Parent:** [Production Runtime Persistence, Recovery, and Client Boundary](2026-08-13-runtime-persistence-recovery-client-design.md)

**Gate:** `docs/research/architecture-gates/2026-08-16-jsonl-audit-replica.md` (evidence in flight; this spec freezes slice decisions against the parent design, sections 10 and 7.5)

**Implemented base:** [SQLite canonical EventStore](../../architecture/sqlite-eventstore.md)

## 1. Decision summary

Slice 3 activates the audit chain that Slice 2 created but did not maintain,
and builds the exporter, replica layout, restart state machine, consistent
export, and import. SQLite remains the sole live commit authority; JSONL is
a complete, lossless audit replica — never an online commit point, never a
peer authority, never a silent overwrite of a live database.

Load-bearing slice decisions:

1. The audit codec registry is versioned with the binary;
   `event_appends.audit_format_version` is its sole selection key. A codec
   for any committed format cannot be removed from a supported upgrade path.
   A missing codec is `StoreCorrupt`; export and import fail closed.
2. The append transaction gains, inside the same `BEGIN IMMEDIATE` and
   without changing the port or error algebra: batch-envelope computation,
   `event_appends` audit columns, one `export_outbox` row, and
   `head_audit_digest` maintenance. The chain is created by commit-position
   order, not by the asynchronous exporter.
3. A single-writer backfill migration encodes every pre-Slice-3 append
   under codec v1, building the chain from genesis in commit-position
   order. Any digest mismatch during backfill fails closed.
4. The exporter is a library component (`ExportOnce`, bounded run loop);
   host-driven scheduling belongs to Slice 4.
5. Import writes only a new or empty database. Automatic merge into an
   active database is forbidden.

## 2. Goals

- One JSONL line per atomic append (batch envelope, format version 1) with
  a `previousDigest`/`batchDigest` hash chain ordered by commit position.
- Segmented, manifest-tracked, immutable replica layout with verified
  publication.
- Crash-convergent exporter restart that never trusts one mutable
  checkpoint.
- `export --consistent` producing a self-contained verified replica through
  a fixed commit position.
- Import with the parent's eight-step verification into a new database
  only.
- Divergence handling exactly per the parent policy table.

## 3. Non-goals

- Redacted export (separate command, later slice).
- Host heartbeat scheduling and exporter lifecycle ownership — Slice 4.
- Any writer role for JSONL, any auto-merge import, any peer-authority
  comparison API.

## 4. Audit codec v1

The envelope is the parent section 10.2 shape with canonical JSON encoding:
`formatVersion` 1, `commitPosition`, `appendId`, `commandId`, `sessionId`,
`expectedVersion`, `firstSequence`, `lastSequence`, `committedAt`,
`previousDigest`, `events` (canonical event payloads), `batchDigest`.
`batchDigest` is SHA-256 over the canonical envelope bytes excluding
itself; `previousDigest` chains to the prior batch by commit position. The
chain seed is a fixed genesis constant. Codec v1 round-trip fixtures are
frozen in-tree for every committed format version.

The canonical event payload is the same frozen `domain` record encoding the
SQLite adapter stores; envelope event bytes must reproduce the stored
`events.payload_digest` exactly or the export fails closed.

## 5. Append transaction integration

After the `events` batch insert and before COMMIT, the transaction now
also:

1. computes the envelope bytes with codec v1 (or the codec keyed by the
   row's `audit_format_version` after future formats exist);
2. sets `event_appends.audit_format_version`, `previous_audit_digest`, and
   `batch_audit_digest`;
3. inserts the exact canonical envelope into `export_outbox` (envelope
   retained verbatim while publication is pending — the exporter never
   re-encodes a live append differently);
4. advances `store_metadata.head_audit_digest` to the new batch digest.

Exact-retry resolution still returns the original receipt without
recomputing envelopes. The port, error algebra, and visibility atomicity
are unchanged; this is the constraint Slice 2 declared.

## 6. Backfill migration 3

Migration 3 adds the `export_leases` table (parent 7.8) and backfills, in
one single-writer transaction: every existing `event_appends` row in
commit-position order is encoded under codec v1, its audit columns are
populated, its `export_outbox` row is inserted, and
`head_commit_position`/`head_audit_digest` are reconciled. Backfill
determinism is asserted: re-encoding any row must reproduce the stored
digest exactly, else the migration aborts and open fails closed. Migration
3 does not touch `events`, `event_streams`, or domain tables.

## 7. Replica layout and publication

Exactly the parent section 10.3 layout: disposable `manifest.json` hint,
immutable `manifests/<head-position>-<head-digest>.json` generations,
sealed immutable `segments/<first>-<last>-<digest>.jsonl`, disposable
`staging/`. Sealing requires write → sync → close → reopen → verify bytes
and digest → publish. Segment bound: 1,000 commit positions or 4 MiB,
whichever first (filename ranges stay position-based). The exporter holds
the `export_leases` row while running; the lease never authorizes domain
appends.

Publication is idempotent by commit range and digest: the same range and
digest is already complete; the same range with a different digest
quarantines the replica and triggers rebuild. Export failure never rolls
back or falsifies a domain append.

## 8. Exporter restart state machine

Startup inventory per parent 10.5: discard staging; verify immutable
manifest generations and their sealed segments against SQLite digests;
choose the unique highest continuous valid generation not past the SQLite
head (two conflicting valid generations at the same head quarantine the
replica); adopt an unnamed sealed segment only when it is the exact next
range; regenerate missing or invalid derived segments from canonical bytes
and the frozen codec; recompute `export_checkpoints` transactionally from
the verified generation (a checkpoint ahead or behind the manifest is
repair evidence, never authority); resume at the next commit position.

The conformance test matrix exercises every publication boundary: crash
after segment publish, after manifest publish, before checkpoint update,
and mid-staging — each converges through the same inventory.

## 9. Consistent export and import

`ExportConsistent(target)` fixes the target commit position in a SQLite
read snapshot, emits all batches through it, and writes a self-contained
manifest generation. Plain file copy is not a supported export procedure.

`Import(path)` writes only a new or empty database and verifies, in order:
manifest and segment digests; continuous commit positions and batch hash
chain; event payload digests; continuous per-Session sequences;
expected-version to last-sequence transitions; known schema and
deterministic upcasters; complete domain replay invariants; rebuilt heads
projection. Any failure refuses the import with a classified error; a
partially imported staging database is discarded. Automatic merge into an
active database is forbidden.

## 10. Outbox pruning

After a sealed segment and manifest generation are verified and their
SQLite checkpoint is committed, the covered `export_outbox` envelope rows
may be pruned. The permanent `event_appends` row, event bytes, format
version, and digests remain; regeneration from the frozen codec must
reproduce the stored digest exactly or fail closed.

## 11. Testing evidence

1. Codec v1 round-trip and chain fixtures; tamper detection at every
   envelope field.
2. Append integration: audit columns, outbox row, and head digest
   transactional with the batch (all-or-nothing under fault points).
3. Backfill determinism on a pre-Slice-3 database; corruption aborts.
4. Publication matrix: every crash boundary converges; idempotent
   republication; quarantine on digest disagreement.
5. Import: full verification happy path; each of the eight steps' failure
   refuses; refusal to write a non-empty database.
6. Divergence policy table as executable tests.
7. Benchmarks: export throughput, import throughput, append overhead added
   by envelope computation.

## 12. Delivery plan

1. **Audit codec v1** — envelope, digests, chain, frozen fixtures.
2. **Transaction integration and backfill** — migration 3, `export_leases`,
   envelope maintenance inside the append transaction, determinism gates.
3. **Exporter and restart state machine** — staging/seal/manifest/
   checkpoint publication, inventory convergence, pruning.
4. **Consistent export and import** — snapshot export, eight-step
   verified import, divergence matrix.
5. **Docs and evidence** — implemented contract, evidence ledger,
   bilingual, index updates.

## 13. Exclusions

No redacted export; no host-owned exporter scheduling; no JSONL writer
authority; no auto-merge; no cross-replica diff API; no compaction of the
audit trail.
