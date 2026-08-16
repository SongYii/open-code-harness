# JSONL Audit Replica and Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Activate the audit chain inside the append transaction, backfill pre-Slice-3 history under codec v1, and deliver the crash-convergent exporter, replica layout, consistent export, and verified import — with SQLite remaining the sole live commit authority.

**Architecture:** The append transaction gains envelope/outbox/head-digest maintenance without changing the port or error algebra. The exporter is a library component with an inventory-based restart state machine. Import writes only new databases and verifies eight layers before success.

**Tech Stack:** Go 1.26, `database/sql`, `modernc.org/sqlite`, `crypto/sha256`, `encoding/json` with canonical encoding from the frozen domain codec, `testing/synctest` where deterministic time helps, race/benchmark tooling.

## Global Constraints

- Normative specification: `docs/superpowers/specs/2026-08-16-jsonl-audit-replica-design.md`; research evidence: the Slice 3 architecture gate.
- The `application.EventStore` port, `StoreError` algebra, and `eventstoretest` suite must not change.
- SQLite is the sole live commit authority; JSONL is never written by any commit decision other than the exporter, and never read as authority by any runtime path.
- `event_appends.audit_format_version` is the sole codec selection key; a codec for any committed format cannot be removed from a supported upgrade path; a missing codec is `StoreCorrupt`.
- The chain seed is a fixed genesis constant; `previousDigest` chains by commit position.
- Segment bound: 1,000 commit positions or 4 MiB, whichever comes first.
- Publication: write → sync → close → reopen → verify bytes and digest → publish. Idempotent by commit range and digest; disagreement quarantines and rebuilds.
- Export failure never rolls back or falsifies a domain append.
- Import writes only a new or empty database and verifies the eight parent steps; auto-merge is forbidden.
- Every behavior is TDD. Every task ends with `gofmt`, focused tests, `go test ./... -count=1`, race when concurrency changes, review, and one small commit.
- English is normative; the Chinese plan is a synchronized reading copy committed together.

## File map

| Path | Responsibility |
| --- | --- |
| `internal/harness/adapters/sqlite/auditcodec.go` | Codec registry, envelope v1 encode/decode, batch digest, chain constants |
| `internal/harness/adapters/sqlite/auditcodec_test.go` | Round-trip, chain, tamper fixtures per format version |
| `internal/harness/adapters/sqlite/append.go` | Envelope maintenance inside the append transaction |
| `internal/harness/adapters/sqlite/migrations.go` | Code-driven migration step support; migration 3 |
| `internal/harness/adapters/sqlite/migrations_sql.go` | `export_leases` DDL |
| `internal/harness/adapters/sqlite/backfill.go` | Codec-v1 backfill with determinism gates |
| `internal/harness/adapters/sqlite/exporter.go` | `ExportOnce`, staging/seal/manifest/checkpoint publication, inventory restart |
| `internal/harness/adapters/sqlite/exporter_test.go` | Publication and crash-boundary matrix |
| `internal/harness/adapters/sqlite/auditimport.go` | Eight-step verified import into a new database |
| `internal/harness/adapters/sqlite/auditimport_test.go` | Verification-layer refusals |
| `internal/harness/adapters/sqlite/benchmark_test.go` | Export/import/append-overhead benchmarks |
| `docs/architecture/jsonl-audit-replica.md` | Implemented contract |
| `docs/architecture/jsonl-audit-replica-evidence.md` | Commits, verification, benchmarks, exclusions |

---

### Task 1 (PR 1): Audit codec v1

**Files:**
- Create: `auditcodec.go`, `auditcodec_test.go`

**Steps:**
- [ ] Failing test: encoding a batch produces the exact envelope field order and canonical JSON of the spec; decoding round-trips losslessly.
- [ ] Failing test: `batchDigest` covers every envelope field except itself; flipping any single field breaks it.
- [ ] Failing test: chain constants — genesis seed is stable, `previousDigest` of the first batch equals the seed.
- [ ] Failing test: envelope event bytes reproduce the stored `payload_digest` exactly; a mismatch fails closed.
- [ ] Failing test: the registry resolves codec v1 by `audit_format_version` and reports a missing codec as corruption.
- [ ] Implement codec v1 with frozen fixtures; commit `sqlite: audit codec v1 with chain and tamper fixtures`.

### Task 2 (PR 2): Transaction integration and backfill

**Files:**
- Modify: `append.go`, `migrations.go`, `migrations_sql.go`
- Create: `backfill.go` + tests

**Steps:**
- [ ] Failing test: a new append populates `event_appends` audit columns, inserts the exact `export_outbox` envelope, and advances `head_audit_digest` — atomically with the batch (fault points prove all-or-nothing).
- [ ] Failing test: exact retry does not recompute or duplicate envelope rows.
- [ ] Extend the migration runner to support code-driven steps; migration 3 creates `export_leases` and backfills every pre-existing append in commit-position order under codec v1, populating audit columns, outbox rows, and `head_audit_digest` in one transaction.
- [ ] Failing test: backfill determinism — a pre-seeded database backfills once; re-running the migration is a no-op; a corrupted pre-existing digest aborts open fail-closed.
- [ ] Failing test: append error algebra and receipt resolution are unchanged; the conformance suite still passes with zero suite change.
- [ ] Commit `sqlite: audit chain in append transaction with codec v1 backfill`.

### Task 3 (PR 3): Exporter and restart state machine

**Files:**
- Create: `exporter.go`, `exporter_test.go`

**Steps:**
- [ ] Failing test: `ExportOnce` drains outbox rows in commit-position order through staging → sync → close → reopen → verify → sealed segment → immutable manifest generation → transactional checkpoint update.
- [ ] Failing test: segment bound at 1,000 positions or 4 MiB; filenames record position ranges and digests.
- [ ] Failing test: publication idempotence — same range and digest is complete; same range different digest quarantines and rebuilds from canonical bytes.
- [ ] Failing test: restart inventory converges from every boundary: crash after segment publish, after manifest publish, before checkpoint, mid-staging; two conflicting valid generations at one head quarantine; an unnamed segment is adopted only as the exact next range.
- [ ] Failing test: pruned outbox rows regenerate from the frozen codec and reproduce stored digests exactly.
- [ ] Failing test: the exporter lease (`export_leases`) coordinates runs and never authorizes domain appends.
- [ ] Commit `sqlite: crash-convergent audit exporter with restart inventory`.

### Task 4 (PR 4): Consistent export and import

**Files:**
- Create: `auditimport.go`, `auditimport_test.go`

**Steps:**
- [ ] Failing test: `ExportConsistent(target)` fixes the position in a read snapshot, emits all batches through it, and writes a self-contained manifest.
- [ ] Failing test: import into a new database verifies all eight layers and lands a working store (reads serve the imported streams; heads projection rebuilt).
- [ ] Failing test: each verification layer, when violated, refuses with a classified error and discards staging.
- [ ] Failing test: import refuses a non-empty database.
- [ ] Failing test: the divergence policy table as executable tests (seven rows).
- [ ] Commit `sqlite: consistent export and eight-step verified import`.

### Task 5 (PR 5): Benchmarks, docs, and evidence

**Steps:**
- [ ] Benchmarks: export throughput, import throughput, append overhead with envelope computation; record samples.
- [ ] Publish the implemented contract and evidence ledger (bilingual); update the README index and milestone status.
- [ ] Final gates: `gofmt`, `go vet ./...`, `go test ./... -count=1`, `-race`, CGO-free three-OS builds; commit `sqlite: audit benchmarks, contract, and evidence`.

## Final completion gate

- Conformance suite still green with zero change; audit chain transactional with every batch; every publication boundary converges; import refuses every violated layer; divergence table executable; benchmarks recorded; contract and evidence published with exclusions visible.
