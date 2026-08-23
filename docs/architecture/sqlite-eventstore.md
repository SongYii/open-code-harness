# SQLite Canonical EventStore — Implemented Contract

**Status:** Implemented; not GA

**Authority:** [SQLite Canonical EventStore (Slice 2) design](../superpowers/specs/2026-08-16-sqlite-canonical-eventstore-design.md)

**Port:** the unchanged `application.EventStore` interface and `StoreError` algebra from [EventStore v2](eventstore-v2.md)

**Package:** `internal/harness/adapters/sqlite`

## Scope

Pure-Go (`CGO_ENABLED=0`) SQLite adapter behind the EventStore v2 port.
SQLite is the sole live commit authority. Slice 2 delivers: verified open
profile, full-shape versioned migrations, the append transaction with exact
retry, pinned-head paginated reads, the fencing lease primitive, a verified
backup, and an offline projection rebuild. It passes the shared
`eventstoretest` conformance suite with zero suite change.

## Open profile

Configured and verified on every open, before migrations: `journal_mode`
must actually be `wal` (anything else fails closed with a location
diagnosis), `synchronous=FULL`, foreign keys enforced, bounded busy timeout,
explicit WAL checkpoint policy, and a bundled-SQLite version gate for
`unixepoch('subsec')`. A configurable deny-list diagnoses known network or
synchronized locations. One dedicated writer connection owns every
`BEGIN IMMEDIATE` transaction; reads use a bounded pool with explicit read
transactions.

## Schema

Ordered versioned migrations create the full target shape once (migration
1) plus the receipt-verification index (migration 2): `store_metadata`
(singleton), `event_streams`, `event_appends` (unique `append_id`,
`commit_position`; zero-value audit-chain columns until Slice 3), `events`
(unique `(session_id, sequence)`, globally unique `event_id`, canonical
record payload and digest), `command_requests` (globally unique
`run_turn_request_id`), `domain_identities` (unique `(session_id,
identity_kind, identity_id)`), `runtime_leases` (singleton),
`export_outbox`/`transcript_entries`/`snapshots`/`export_checkpoints`
(existing, unmaintained until later slices), and `schema_migrations`.

A database stamped by a newer format is refused with an upgrade-direction
error; a `user_version`/history disagreement is corruption; tampered
metadata fails closed.

## Append transaction

`BEGIN IMMEDIATE` on the writer connection: receipt resolution by
`AppendID` and request digest (exact retry returns the original receipt
after any stream advance; digest mismatch is `AppendIdentityMismatch`;
resolved receipts are cross-checked against the events committed under
them), the lease ownership predicate (`WriterFenced` otherwise), admission
lookup (`CommandRequestConflict`/`CommandIdentityMismatch`), version CAS
(`VersionConflict`), limits and identity validation
(`InvalidAppend`/`DomainIdentityConflict`), global commit-position
increment, contiguous sequence allocation, receipt/admission/event/identity
writes, the synchronous `session_heads` upsert, and the head-position
update. The batch is wholly visible or wholly absent.

Result-code mapping: busy/locked bounded to `StoreUnavailable` with the code
retained; constraint and integrity failures on the serialized writer path
are `StoreCorrupt`; environment failures are `StoreUnavailable`. Once
COMMIT is attempted, failure is never converted to definite absence: the
adapter releases or quarantines the writer connection, performs exactly one
bounded receipt lookup on a fresh connection, and otherwise returns
`CommitOutcomeUnknown` with `MayHaveCommitted`.

## Read path

`ReadStream` serves exclusive-`AfterSequence` pages pinned to one WAL
snapshot per call; an unservable pinned head is `InvalidRead`, never a
silent empty page. `ResolveAppend` is read-only
(committed/not_found/identity_mismatch). `FindCommandRequest` compares
Session and digest together and never reveals another Session's record.

## Fencing lease primitive

Open acquires the singleton `runtime_leases` row in its own `BEGIN
IMMEDIATE`: absent or expired leases are taken with a monotonically
increasing token; a live foreign lease refuses open; a same-runtime reopen
renews. `RenewLease` extends expiry and stamps heartbeat; renewing an
expired lease fences. SQLite `unixepoch('subsec')` is the only lease clock.
Every append verifies `runtime_id`, `fencing_token`, and expiry inside the
write transaction. `Authority` / `CurrentAuthority` return the live lease
state under the writer lock, so an expired-takeover token rotation is
visible to the next append. Host lifecycle (heartbeat scheduling, takeover
policy, startup reconciliation) is Slice 4 and intentionally absent.

## Projections, backup, rebuild

`session_heads` is the one synchronous projection, derived through the same
event-type transition walk used by `RebuildAndVerifySessionHeads`, which
replays canonical streams and reports any disagreement as corruption. The
projection is never authoritative. `Backup` produces a consistent snapshot
copy (via `VACUUM INTO`, because the pure-Go driver does not export the
Online Backup API) and verifies the copy's schema version, contiguity, and
invariant counts against the live database before reporting success.

## OpenReader

`OpenReader(ctx, ReaderConfig) (*Reader, error)` opens Path for pinned
`ReadStream` only. It is additive: existing `Open` is unchanged. `Reader`
is not a second EventStore — `Append`, `ResolveAppend`, and
`FindCommandRequest` are absent.

`ReaderConfig` is the read profile: `Path`, `BusyTimeout` (default 5s;
allowed range 100ms–60s, same as `Config.BusyTimeout`),
`DeniedPathPrefixes` (same diagnosis as `Open`), and `WALAutoCheckpoint`
(default 1000; applied as a read-side pragma only). It does not include
`RuntimeID` or `LeaseDuration`.

Open profile, verified before return:

- WAL. Does **not** set `immutable=1` (must see the live writer's last commit).
- `synchronous=FULL`, `foreign_keys=1`, bounded `busy_timeout`.
- `query_only=1` and `mode=rw` (a missing file is refused, never created).
- `DeniedPathPrefixes` — a network or synchronized location `Open` would
  refuse is also refused here.
- `user_version` must equal this binary's latest migration. Newer is
  `FormatNewerError`. Older is refused with “writer must migrate first”.
  OpenReader does not run `migrate`.
- Fail-closed on corrupt metadata the same as `Open` reads.

OpenReader does not acquire `runtime_leases` or `export_leases`. A live
writer may keep the fencing lease; the reader waits up to `BusyTimeout`
on `SQLITE_BUSY` rather than failing immediately. `ReadStream` is the
same pinned-head page function the writer uses.

`composition.ExportSession` passes `ReaderConfig{Path: databasePath}` and
takes defaults. Session transcript JSONL is documented in
[session transcript](session-transcript.md); it is not this adapter's
audit replica and does not populate `transcript_entries`.

## Exclusions

- Audit envelope, digest chain, and outbox maintenance — Slice 3 (columns
  exist at zero values).
- Runtime Host lifecycle: heartbeat scheduling, takeover, crash
  reconciliation, graceful shutdown — Slice 4.
- `transcript_entries`, `snapshots`, `export_checkpoints` — schema only.
  Session transcript JSONL is an export projection, not those tables.
- JSONL audit replica, ACP, TUI — out of scope of this adapter's writer
  contract. `OpenReader` is the additive read path used by session export.
- GA blockers: no crash-injection harness at the process level, no
  long-running soak or corruption fuzzing, no concurrent multi-process
  writer evidence beyond the lease predicate tests.
