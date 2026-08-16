# SQLite Canonical EventStore (Slice 2)

**Status:** Accepted design

**Date:** 2026-08-16

**Parent:** [Production Runtime Persistence, Recovery, and Client Boundary](2026-08-13-runtime-persistence-recovery-client-design.md)

**Evidence:** [SQLite Canonical EventStore Architecture Gate](../../research/architecture-gates/2026-08-16-sqlite-canonical-eventstore.md)

**Implemented contract this adapter must satisfy:** [EventStore v2](../../architecture/eventstore-v2.md)

## 1. Decision summary

This slice implements the first durable canonical EventStore: a pure-Go SQLite
adapter behind the already-implemented `application.EventStore` port. The
storage model, append transaction, exact-retry rules, and error algebra are
normative in the parent design sections 7 and 8 and are restated here only
where Slice 2 freezes a boundary. Nothing in this spec changes the port, the
conformance suite, or Domain behavior.

The load-bearing decisions of this spec are slice decisions, not redesigns:

1. The schema is created once at its full target shape through versioned
   migrations. Tables belonging to later slices (`export_outbox`,
   `transcript_entries`, `snapshots`, `export_checkpoints`) exist from the
   first migration but are not maintained by Slice 2 code.
2. Fencing is real from the first append. `runtime_leases` acquire and renew
   primitives and the per-append ownership predicate ship in this slice; host
   lifecycle policy (heartbeat scheduling, takeover, reconciliation,
   graceful shutdown) is Slice 4.
3. The append transaction follows the parent algorithm without the
   `export_outbox` envelope insert and audit-chain maintenance. Slice 3 adds
   those steps inside the same transaction shape; the port and error algebra
   do not change.
4. Backup is the SQLite Online Backup API with post-copy verification.

## 2. Goals

- A CGO-free, pure-Go SQLite EventStore that passes the shared
  `eventstoretest` conformance suite unchanged.
- Exact append retry, CAS, admission identity, and domain-identity
  enforcement inside one `BEGIN IMMEDIATE` transaction.
- Pinned-head paginated reads through explicit read transactions.
- A verified fencing predicate on every append.
- Fail-closed corruption and schema-version handling.
- A consistent, verified backup operation.
- SQLite result-code fault evidence at COMMIT, including busy, full, I/O,
  interrupted, and close/rollback behavior.

## 3. Non-goals

- JSONL audit export, outbox encoding, segments, manifests, and import —
  Slice 3.
- Runtime Host lifecycle: heartbeat scheduling, takeover, startup
  reconciliation, graceful shutdown — Slice 4.
- `transcript_entries`, `snapshots`, and Context-facing projections — later
  consumers.
- ACP, TUI, and any client boundary.
- Multi-database distribution, replication, or encryption at rest.

## 4. Package and driver

- Package `internal/harness/adapters/sqlite` implements the adapter. It
  depends only on `application`, `domain`, and the driver.
- Driver: `modernc.org/sqlite`, selected by the parent design so production
  builds remain `CGO_ENABLED=0`. This is the repository's first external
  dependency; the completion evidence must record the dependency and license
  inventory it introduces.
- `database/sql` is the access layer. One dedicated writer connection owns
  all `BEGIN IMMEDIATE` transactions. Reads use a bounded connection pool
  and explicit read transactions whenever multi-statement consistency
  matters. Connection counts are configured and bounded.

## 5. Operating profile

Configured and verified on every open, before any migration or lease write:

1. `PRAGMA journal_mode = WAL` — the actual returned mode must be `wal`;
   failure to establish WAL (for example on a network filesystem) is a
   fail-closed open error with a diagnosis, absorbing the Grok Build NFS
   finding from the architecture gate.
2. `PRAGMA synchronous = FULL` per the parent design; commits fsync before
   the receipt is returned.
3. Foreign-key enforcement on; a bounded busy timeout; an explicit WAL
   checkpoint policy.
4. The database path must resolve to a local filesystem. Known network or
   synchronization locations are rejected at open with a prominent
  diagnosis.
5. Schema-version gate: `PRAGMA user_version` (and the `store_metadata`
   format version) is read before any other check. A database written by a
   newer format is refused with an upgrade-direction error, never reported
   as corrupt — the DeepSeek Harness contract confirmed by the gate.

A busy database never causes an unbounded hidden retry: every wait is bound
by the caller context and configuration.

## 6. Schema and migrations

- Migrations are ordered, versioned SQL steps recorded in a migration
  history table; `store_metadata` carries storage format version and
  creation/last-migration metadata.
- Migration 1 creates the full parent-design target shape: `store_metadata`,
  `event_streams`, `event_appends`, `events`, `command_requests`,
  `domain_identities`, `runtime_leases`, `export_outbox`,
  `session_heads`, `transcript_entries`, `snapshots`, `export_checkpoints`.
- Audit-chain columns on `event_appends` and `head_audit_digest` on
  `store_metadata` exist from migration 1 and hold zero values until Slice 3
  activates the chain. Slice 3 backfills under its frozen codec in one
  single-writer migration; Slice 2 must not create a shape that makes that
  backfill impossible.
- Uniqueness that encodes the contract lives in the schema:
  `(session_id, sequence)` unique, `event_id` globally unique, `append_id`
  unique, `commit_position` unique and contiguous, `run_turn_request_id`
  globally unique, `(session_id, identity_kind, identity_id)` unique.
- All mutation SQL is parameterized; dynamic SQL is limited to fixed
  statement shapes.

## 7. Append transaction

The parent design section 8.3 algorithm is normative. Slice 2 executes it
as:

```text
BEGIN IMMEDIATE
  resolve append_id receipt (digest match -> original receipt;
                             digest mismatch -> AppendIdentityMismatch)
  verify runtime lease predicate (runtime_id, fencing_token,
                                  lease_expires_at >= sqlite_now)
  admission lookup when Admission present
  read event_streams.version (mismatch -> VersionConflict)
  validate request limits, IDs, schema versions, payload canonicality,
    and batch event uniqueness
  reserve creation-event identities in domain_identities
  increment store_metadata.head_commit_position
  allocate contiguous stream sequences
  insert command_requests row when admitted
  insert event_appends receipt row
  insert complete events batch
  upsert event_streams
  upsert session_heads projection
COMMIT
```

Deferred to Slice 3 inside the same shape: export-outbox envelope insert and
audit-digest maintenance. Adding them must not change the port, the error
algebra, or visibility atomicity; that constraint is part of this spec.

The batch is wholly visible or wholly absent. Receipt resolution precedes
fencing validation, so a fenced process may learn its exact request already
committed but cannot create a new commit.

## 8. Read path

- `ReadStream` serves `(AfterSequence exclusive, Limit, HeadVersion)` pages
  from an explicit read transaction so a page sequence pinned to one
  `HeadVersion` observes one WAL snapshot.
- `End`, `NextAfterSequence`, and `HeadVersion` follow the implemented
  EventStore v2 contract; a pinned head that no longer matches any
  retrievable snapshot state is an invalid read, not a silent empty page.
- Reads never take write locks and never block on the writer connection
  beyond the bounded busy timeout.

## 9. Fencing and lease primitive

- `runtime_leases` holds the singleton database scope, `runtime_id`,
  monotonically increasing `fencing_token`, `lease_expires_at`, and
  `last_heartbeat_at`.
- Open acquires the lease inside `BEGIN IMMEDIATE`: an absent or expired
  lease is taken with a new token; a live lease held by another runtime is
  refused. Renewal extends `lease_expires_at` and stamps
  `last_heartbeat_at`. SQLite's `unixepoch('subsec')` is the only lease
  clock; callers never supply wall time.
- Every append verifies the parent predicate in the write transaction:
  `runtime_id = request.runtime_id AND fencing_token =
  request.fencing_token AND lease_expires_at >= sqlite_now`; failure maps
  to `WriterFenced`.
- Who schedules renewal, when takeover is attempted, and how startup
  reconciles crashed owners is Slice 4 policy. This slice ships the store
  mechanism, not the host.

## 10. Error mapping

- Pre-COMMIT definite failures map by the parent algebra: `InvalidAppend`,
  `VersionConflict`, `AppendIdentityMismatch`, `CommandRequestConflict`,
  `CommandIdentityMismatch`, `DomainIdentityConflict`, `WriterFenced`,
  `StoreUnavailable`.
- SQLite busy/locked within bounds maps to `StoreUnavailable` with the
  result code retained as cause; it never hides as a retry loop.
- Once COMMIT is attempted, an error is never converted to definite
  absence. The adapter finalizes or quarantines the original connection
  according to verified driver behavior, then performs exactly one bounded
  receipt lookup on a fresh connection: a digest match returns the original
  receipt; absence or an unavailable lookup returns
  `CommitOutcomeUnknown`. The caller may only resolve or retry the exact
  request with the same `AppendID`.
- Storage-invariant failures (contiguity, uniqueness assumptions, digest
  mismatch on read-back, unexpected schema shape) map to `StoreCorrupt`;
  mutation is fail-closed from that point.
- Disk-full (`SQLITE_FULL`) at any write is tested as its own resource-limit
  class, per the architecture gate adopt list.

## 11. Projections

- `session_heads` is the one synchronous projection updated in the append
  transaction: minimal status and active Turn/Item candidate index derived
  from canonical events.
- It is never accepted as independent proof of domain state; recovery
  candidates it surfaces are confirmed by authoritative stream replay.
- An offline rebuild-and-verify operation reconstructs `session_heads` from
  the canonical streams and reports any mismatch as corruption.
- `transcript_entries`, `snapshots`, and `export_checkpoints` exist in
  schema only; no Slice 2 code path reads or writes them.

## 12. Backup

- The backup operation produces a consistent copy through the SQLite Online
  Backup API into a caller-supplied destination, then opens the copy and
  verifies schema version and core invariants before reporting success.
- The parent design's naming rule holds: a verified backup copy is the
  primary backup; pairing with JSONL export is Slice 3.

## 13. Fault injection and tests

The adapter passes `eventstoretest` unchanged and adds adapter-specific
evidence:

1. Result-code tests at COMMIT: busy, full, I/O error, interrupted, and
   close/rollback behavior, each asserting the error-algebra outcome.
2. Unknown-outcome protocol: a COMMIT whose acknowledgement is lost resolves
   through the bounded fresh-connection lookup; absence yields
   `CommitOutcomeUnknown`, never a false non-commit.
3. Concurrency: parallel appenders on separate sessions serialize through
   one writer connection with contiguous, gap-free `commit_position`.
4. Reopen: a database reopened after abrupt process termination (simulated
   crash during a WAL write) opens, verifies, and serves consistent state
   with no half-visible batches.
5. Journal-mode verification: a location that cannot establish WAL fails
   closed at open.
6. Disk-full latch behavior under `SQLITE_FULL`.
7. Fencing: append with a stale token fails `WriterFenced`; lease expiry
   refuses appends until reacquisition.
8. Benchmarks: append throughput and latency, paged read throughput, and
   backup duration recorded in the evidence ledger.

## 14. Delivery plan

Five reviewed PRs:

1. **Driver, open, and migrations** — dependency introduction, open-time
   profile verification, schema-version gate, migration 1 with the full
   target shape, corruption fail-closed path.
2. **Append transaction** — receipt resolution, CAS, admission,
   domain identities, receipt and event writes, `session_heads` upsert,
   pre-COMMIT error mapping.
3. **Read path** — pinned pagination through read transactions,
   `ResolveAppend`, `FindCommandRequest`, read pool bounds.
4. **Fencing and unknown outcome** — lease acquire/renew, per-append
   predicate, COMMIT result-code tests, bounded receipt-lookup protocol.
5. **Backup, rebuild, benchmarks, and evidence** — Online Backup copy,
   `session_heads` rebuild-and-verify, benchmark record, implemented
   contract and evidence ledger updates.

## 15. Completion criteria

- `eventstoretest` passes against the SQLite adapter with no suite change.
- Every test class in section 13 has recorded evidence.
- Linux/macOS/Windows CGO-free gates pass.
- The dependency and license inventory is recorded.
- The implemented-contract document and evidence ledger land with explicit
  exclusions: audit chain inactive, host lifecycle absent, extra
  projections unmaintained.

## 16. Exclusions

- No audit envelope, digest chain, or outbox maintenance.
- No heartbeat scheduler, takeover, or crash reconciliation.
- No transcript, snapshot, or context projections.
- No import, export, or JSONL of any kind.
- No ACP or TUI surface.
- No vendor driver alternatives; the driver decision is revisited only by a
  new gate with benchmark evidence.
