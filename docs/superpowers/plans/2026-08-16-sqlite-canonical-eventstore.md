# SQLite Canonical EventStore Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the first durable canonical EventStore as a pure-Go SQLite adapter behind the unchanged `application.EventStore` port: full-shape versioned migrations, one `BEGIN IMMEDIATE` append transaction with exact retry and fencing, pinned-head paginated reads, a verified backup, and result-code fault evidence at COMMIT.

**Architecture:** The port, error algebra, and conformance suite are frozen by the EventStore v2 contract; this plan adds an adapter only. One dedicated writer connection serializes all mutations; reads use a bounded pool with explicit read transactions; SQLite is the sole commit authority. Later-slice tables exist in schema but no Slice 2 code path maintains them.

**Tech Stack:** Go 1.26, `database/sql`, `modernc.org/sqlite` (first external dependency), `testing`, `testing/synctest` where deterministic time helps, race/benchmark tooling, GitHub Actions.

## Global Constraints

- Normative specification: `docs/superpowers/specs/2026-08-16-sqlite-canonical-eventstore-design.md`; sections 4–13 are mandatory. Research evidence: `docs/research/architecture-gates/2026-08-16-sqlite-canonical-eventstore.md`.
- This is Slice 2 only: no audit envelope or digest chain, no outbox maintenance, no heartbeat scheduler, takeover policy, or crash reconciliation, no transcript/snapshot/context projections, no JSONL, no ACP, no TUI.
- The `application.EventStore` port, `StoreError` algebra, and `eventstoretest` suite must not change. The adapter passes `eventstoretest.Run` with a harness factory; harness helpers may manipulate lease rows only through the adapter's own connections.
- `internal/harness/adapters/sqlite` imports only `application`, `domain`, the driver, and the standard library; the architecture dependency test extends to the new package.
- Store assigns only per-Session sequence and global commit position. Application-owned request identity is untouched.
- Limits identical to the memory adapter: 8 MiB per canonical Event payload, 64 Events per append, 16 MiB per encoded append request, 256 Records per read page. Canonical facts are rejected, never truncated.
- No unbounded hidden retry. Busy waits are bounded by configuration and caller context; the unknown-outcome protocol performs exactly one bounded receipt lookup on a fresh connection.
- Schema is created once at full target shape. Uniqueness encoding the contract lives in DDL: `(session_id, sequence)` unique, `event_id` globally unique, `append_id` unique, `commit_position` unique and contiguous, `run_turn_request_id` globally unique, `(session_id, identity_kind, identity_id)` unique.
- Audit-chain columns exist from migration 1 and hold zero values; nothing may create a shape that blocks Slice 3's single-writer backfill.
- `synchronous=FULL`; the receipt is returned only after COMMIT durability. `journal_mode` must actually be `wal` after open, or open fails closed with a diagnosis.
- Lease time authority is SQLite `unixepoch('subsec')`; a startup test asserts the bundled SQLite version supports it.
- Every behavior is TDD: observe the intended failure before implementation, then run focused and full tests.
- Every task ends with `gofmt`, focused tests, `go test ./... -count=1`, `go test -race ./... -count=1` when the task changes concurrency, an independent review gate, and one small commit.
- English is normative. The Chinese plan is a complete synchronized reading copy committed together.

## File map

| Path | Responsibility |
| --- | --- |
| `internal/harness/adapters/sqlite/doc.go` | Package scope and operating-profile summary |
| `internal/harness/adapters/sqlite/config.go` | Bounded configuration: pool sizes, busy timeout, checkpoint policy, deny-list prefixes |
| `internal/harness/adapters/sqlite/open.go` | Open: driver registration, pragmas, profile verification, lease acquisition, corruption gate |
| `internal/harness/adapters/sqlite/migrations.go` | Ordered versioned migrations, migration history, format-version gate |
| `internal/harness/adapters/sqlite/migrations_sql.go` | Migration 1 DDL creating the full target shape |
| `internal/harness/adapters/sqlite/append.go` | Append transaction per spec section 7 |
| `internal/harness/adapters/sqlite/validate.go` | Request limits, ID, schema-version, and canonicality checks shared by append |
| `internal/harness/adapters/sqlite/read.go` | ReadStream pinned pagination through read transactions |
| `internal/harness/adapters/sqlite/lookup.go` | ResolveAppend and FindCommandRequest |
| `internal/harness/adapters/sqlite/lease.go` | runtime_leases acquire, renew, and the per-append ownership predicate |
| `internal/harness/adapters/sqlite/errors.go` | SQLite result-code classification and StoreError mapping |
| `internal/harness/adapters/sqlite/fault.go` | Test-visible fault injection at commit boundaries (nil in production) |
| `internal/harness/adapters/sqlite/backup.go` | Verified consistent backup copy |
| `internal/harness/adapters/sqlite/rebuild.go` | Offline session_heads rebuild-and-verify |
| `internal/harness/adapters/sqlite/*_test.go` | Adapter tests plus `eventstoretest.Run` wiring |
| `internal/harness/architecture/dependencies_test.go` | Import rules extended to the new package |
| `docs/architecture/sqlite-eventstore.md` | Implemented contract |
| `docs/architecture/sqlite-eventstore-evidence.md` | Commits, verification output, dependency inventory, benchmark baseline, exclusions |

---

### Task 1 (PR 1): Driver, open profile, and full-shape migrations

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/harness/adapters/sqlite/doc.go`, `config.go`, `open.go`, `migrations.go`, `migrations_sql.go`
- Create: `internal/harness/adapters/sqlite/open_test.go`, `migrations_test.go`
- Modify: `internal/harness/architecture/dependencies_test.go`

**Steps:**
- [ ] Add `modernc.org/sqlite` with a pinned version; record `go version -m` and the transitive license set for the evidence ledger. Confirm `CGO_ENABLED=0 go build ./...` still passes.
- [ ] Failing test: opening a database applies and then reports `journal_mode=wal`, `synchronous=FULL`, `foreign_keys=ON`, and the configured busy timeout, read back through the pool rather than asserted blindly.
- [ ] Implement `Open(config)`: resolve symlinks, apply the configurable deny-list prefix check as diagnosis, open the database, apply pragmas, and verify `journal_mode` is actually `wal`; any mismatch fails closed with a store_unavailable error naming the location.
- [ ] Failing test (unit): journal-mode verification rejects `delete`, `truncate`, `memory`, and `off`; accepts only `wal`.
- [ ] Failing test: opening a database whose `user_version`/`store_metadata.format_version` is newer refuses with an upgrade-direction message and never reports corruption; older migrates forward; equal is a no-op.
- [ ] Implement the migration runner: `schema_migrations` history, ordered steps, single-writer `BEGIN IMMEDIATE`, `user_version` and `store_metadata` updated in the same transaction as the last step.
- [ ] Failing test: migration 1 creates every table from spec section 6 with every uniqueness constraint, and `store_metadata` exists as a singleton (a second row insert fails).
- [ ] Failing test: tampered metadata (impossible format version, missing singleton) makes open fail with store_corrupt and refuses all mutation paths.
- [ ] Failing test: `SELECT sqlite_version()` meets the minimum for `unixepoch('subsec')`; below it, open fails closed.
- [ ] Extend the architecture dependency test to the new package.
- [ ] gofmt, focused tests, `go test ./... -count=1`, race not required here, review, commit `sqlite: driver, open profile, and full-shape migrations`.

### Task 2 (PR 2): Append transaction

**Files:**
- Create: `internal/harness/adapters/sqlite/append.go`, `validate.go`, `errors.go`
- Create: `internal/harness/adapters/sqlite/append_test.go`

**Steps:**
- [ ] Failing test: one Append commits an atomic batch — receipt carries `commit_position=1` and contiguous sequences; read-back preserves proposed `EventID`, schema version, UTC `occurred_at`, and canonical payload bytes exactly.
- [ ] Implement the spec section 7 transaction on the dedicated writer connection: receipt resolution first, then lease predicate (stubbed to pass until Task 4 via a temporary permissive predicate that still reads `runtime_leases`), version CAS, validation, identity reservation, position increment, sequence allocation, admission insert, receipt insert, event batch insert, `event_streams` upsert, `session_heads` upsert, COMMIT.
- [ ] Failing test: exact retry — same `AppendID` and digest returns the original receipt after the stream advanced; different digest returns append_identity_mismatch; no duplicate rows in any table.
- [ ] Failing test: `ExpectedVersion` mismatch returns version_conflict with the actual version and leaves every table unchanged for that append (all-index rollback assertion).
- [ ] Failing test: admission — `command_requests` insert, command_request_conflict on same identity, command_identity_mismatch on Session/digest mismatch without leaking another Session's record.
- [ ] Failing test: duplicate creation Turn/Item identity returns domain_identity_conflict with the identity kind and rolls back the whole batch.
- [ ] Failing test: limits — payload over 8 MiB, over 64 events, request over 16 MiB each return invalid_append; nothing is committed.
- [ ] Implement result-code classification: busy/locked within bounds to store_unavailable with the code retained as cause; constraint and integrity failures to their algebra codes; invariant violations to store_corrupt. Unit-test the classification table across documented result codes.
- [ ] gofmt, focused tests, `go test ./... -count=1`, review, commit `sqlite: append transaction with exact retry, CAS, admission, and identities`.

### Task 3 (PR 3): Pinned reads, resolve, and command lookup

**Files:**
- Create: `internal/harness/adapters/sqlite/read.go`, `lookup.go`
- Create: `internal/harness/adapters/sqlite/read_test.go`

**Steps:**
- [ ] Failing test: `ReadStream` pages with exclusive `AfterSequence`, `Limit`, and `End`/`NextAfterSequence` semantics identical to the memory adapter, pinned to one `HeadVersion` snapshot.
- [ ] Implement reads on the bounded pool inside explicit read transactions so a pinned page sequence observes one WAL snapshot; a pinned head that cannot be served consistently returns invalid_read, never a silent empty page.
- [ ] Failing test: `ResolveAppend` is read-only and returns committed/not_found/identity_mismatch per digest; `FindCommandRequest` compares Session and digest and returns identity_mismatch without revealing another Session's record.
- [ ] Failing test: concurrent readers during a writer's transaction observe only complete batches (no half-visible append) and never block beyond the busy timeout.
- [ ] gofmt, focused tests, `go test ./... -count=1`, `go test -race ./... -count=1`, review, commit `sqlite: pinned pagination, append resolution, and command request lookup`.

### Task 4 (PR 4): Fencing, unknown outcome, and conformance

**Files:**
- Create: `internal/harness/adapters/sqlite/lease.go`, `fault.go`
- Create: `internal/harness/adapters/sqlite/lease_test.go`, `fault_test.go`, `conformance_test.go`
- Modify: `internal/harness/adapters/sqlite/append.go` (replace the permissive predicate)

**Steps:**
- [ ] Failing test: open acquires the singleton lease (`BEGIN IMMEDIATE`, absent/expired taken with a new token, live foreign lease refused); renew extends expiry and stamps heartbeat using `unixepoch('subsec')` only.
- [ ] Replace the permissive append predicate with the real one; failing test: stale or wrong token returns writer_fenced; expired lease refuses appends until reacquisition.
- [ ] Implement the test harness: `RotateAuthority` expires and retakes the lease through the adapter's own connections; `FailNext` at before_commit, after_commit_before_ack, and resolve; `CorruptReceipt` breaks the stored digest through the writer connection.
- [ ] Failing test: fault at after_commit_before_ack returns commit_outcome_unknown; the bounded fresh-connection lookup resolves a digest match to the original receipt; absence or unavailable lookup returns commit_outcome_unknown — never a definite non-commit.
- [ ] Failing test: result codes at COMMIT — live busy contention between two writer connections maps to store_unavailable bounded; context interruption mid-commit runs the same one-lookup protocol; injected full/IO errors classify through the same path as the classification unit tests.
- [ ] Failing test: reopen after abrupt termination — close all handles without clean shutdown mid-WAL activity, reopen, verify invariants, and observe no half-visible batches.
- [ ] Failing test: parallel appenders on separate sessions serialize through the one writer connection with contiguous, gap-free global `commit_position`.
- [ ] Wire `eventstoretest.Run(t, factory)`; the full conformance suite passes with zero suite change.
- [ ] gofmt, focused tests, `go test ./... -count=1`, `go test -race ./... -count=1`, review, commit `sqlite: fencing lease, unknown-outcome protocol, and conformance`.

### Task 5 (PR 5): Backup, rebuild, benchmarks, and evidence

**Files:**
- Create: `internal/harness/adapters/sqlite/backup.go`, `rebuild.go`, `backup_test.go`, `rebuild_test.go`, `benchmark_test.go`
- Create: `docs/architecture/sqlite-eventstore.md`, `docs/architecture/sqlite-eventstore-evidence.md`, and both zh-CN reading copies
- Modify: `docs/README.md`

**Steps:**
- [ ] Verify the pure-Go driver's backup facility; implement the consistent copy with it, or with `VACUUM INTO` if the facility is unavailable — the evidence ledger records which mechanism shipped and why.
- [ ] Failing test: backup to a destination, then open the copy and verify schema version and core invariants (row counts, contiguity, uniqueness) before success is reported; a damaged source fails the backup.
- [ ] Failing test: `session_heads` rebuild from canonical streams reproduces the maintained projection; a seeded mismatch is reported as corruption.
- [ ] Benchmarks: append throughput and latency distribution, paged read throughput, backup duration; record a sample in the evidence ledger.
- [ ] Record the dependency and license inventory (`go mod graph`, `go version -m`).
- [ ] Publish the implemented contract and evidence ledger with explicit exclusions (audit chain inactive, host lifecycle absent, extra projections unmaintained); update the README index and milestone status.
- [ ] Final gates: `gofmt`, `go vet ./...`, `go test ./... -count=1`, `go test -race ./... -count=1`, `CGO_ENABLED=0` builds for linux/darwin/windows, review, commit `sqlite: backup, projection rebuild, benchmarks, and evidence`.

## Final completion gate

- Spec section 15: conformance with zero suite change, every section-13 test class evidenced, CGO-free gates on three platforms, dependency inventory recorded, contract and ledger published with exclusions visible.
- No v1 or temporary names remain; no port, suite, or Domain change was needed to pass conformance.
