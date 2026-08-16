# JSONL Audit Replica — Implemented Contract

**Status:** Implemented; not GA

**Authority:** [JSONL Audit Replica and Import (Slice 3) design](../superpowers/specs/2026-08-16-jsonl-audit-replica-design.md)

**Base:** [SQLite canonical EventStore](sqlite-eventstore.md)

**Package:** `internal/harness/adapters/sqlite`

## Scope

Slice 3 activates the audit chain inside the append transaction, backfills
pre-Slice-3 history under codec v1, and delivers the crash-convergent
exporter, replica layout, consistent export, and eight-step verified
import. SQLite remains the sole live commit authority; JSONL is a complete,
lossless audit replica — never an online commit point, never a peer
authority, never a silent overwrite of a live database.

## Audit codec v1

One JSONL line per atomic append with frozen field order: `formatVersion`
(1), `commitPosition`, `appendId`, `commandId`, `sessionId`,
`expectedVersion`, `firstSequence`, `lastSequence`, `committedAt`
(RFC3339Nano), `previousDigest`, `events` (canonical record payloads
verbatim), `batchDigest`. `batchDigest` is SHA-256 over the canonical
envelope bytes excluding itself; `previousDigest` chains batches in
commit-position order from a fixed genesis seed. The codec registry
resolves by `event_appends.audit_format_version`; a missing codec is
corruption and export/import fail closed.

## Append-transaction integration

Inside the same `BEGIN IMMEDIATE`, after the events batch and before
COMMIT: the envelope is computed, `event_appends` audit columns are set,
the exact canonical envelope is retained in `export_outbox`, and
`head_audit_digest` advances — all atomic with the batch. Exact retry
returns the original receipt without re-encoding. The port and error
algebra are unchanged; the conformance suite still passes with zero change.

## Backfill (migration 3)

Migration 3 creates `export_leases` and backfills every pre-Slice-3 append
in commit-position order under codec v1 in one single-writer transaction,
populating audit columns, outbox rows, and the head digest. A pre-existing
digest that disagrees with recomputation aborts fail-closed. A fresh
database opens at the genesis digest.

## Exporter and restart state machine

`ExportOnce` runs the inventory first: staging is discarded; immutable
manifest generations and their sealed segments are verified against SQLite
digests; the unique highest continuous valid generation not past the SQLite
head is chosen (two conflicting valid generations at one head quarantine
the replica); the checkpoint is recomputed from verified evidence — a
checkpoint ahead or behind the manifest is repair evidence, never
authority. Pending positions export from retained outbox envelopes when
present and re-encode from canonical bytes otherwise; re-encoding must
reproduce the stored digest exactly. Publication per segment: staging →
sync → close → reopen → verify → rename to an immutable
`segments/<first>-<last>-<digest>.jsonl`; segment bound 1,000 positions or
4 MiB. Publication is idempotent by commit range and digest; disagreement
quarantines. After a verified generation and transactional checkpoint, the
covered outbox rows are pruned (digests remain on `event_appends`).

`ExportConsistent(target)` fixes a target position, emits every batch
through it into a fresh directory, and writes a self-contained manifest
generation naming the target batch's digest. It never touches the exporter
checkpoint. Plain file copy is not a supported export procedure.

## Import

`ImportAuditReplica` verifies, in order: manifest and segment digests;
continuous commit positions and batch hash chain; event payload
canonicality and digests; continuous per-session sequences;
expected-version transitions; known schema versions; complete domain
replay; and after landing, the rebuilt heads projection. A torn final line
refuses the whole import — nothing is silently discarded. Import writes
only a new or empty database; automatic merge into an active database is
forbidden.

## Exclusions

- Redacted export (separate command, later slice).
- Host-owned exporter scheduling and lifecycle (Slice 4 wiring).
- `command_requests` are not part of the audit envelope and are not
  reconstructed by import.
- GA blockers: no power-loss device testing of the directory-sync caveat;
  no multi-process exporter contention beyond the lease refusal test; no
  long-running replica soak.
