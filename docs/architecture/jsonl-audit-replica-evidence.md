# JSONL Audit Replica Completion Evidence

**Status:** Complete evidence ledger for Slice 3

**Contract:** [JSONL Audit Replica — Implemented Contract](jsonl-audit-replica.md)

**Branch:** `agent/jsonl-audit-replica`

## Commits

| Commit | Task | Content |
| --- | --- | --- |
| `056d0ff` | Docs | Bilingual Slice 3 and Slice 4 specs and plans (with the host-recovery gate) |
| `3feadff` | Docs | Bilingual Slice 3 and Slice 4 architecture gates |
| `721bdf4` | Task 1 / PR 1 | Audit codec v1: frozen envelope, digest chain, tamper fixtures, fail-closed registry |
| `b41e884` (sqlite audit chain) | Task 2 / PR 2 | Chain maintenance inside the append transaction, migration 3 with codec-v1 backfill and determinism gates |
| `b41e884` (exporter) | Task 3 / PR 3 | Crash-convergent exporter: staging/seal/manifest/checkpoint publication, restart inventory, canonical regeneration, outbox pruning |
| (consistent export and import) | Task 4 / PR 4 | `ExportConsistent`, eight-step verified import, layer-refusal matrix |
| (this ledger) | Task 5 / PR 5 | Benchmarks, implemented contract, evidence |

## Verification evidence

Commands and observed results (Apple M1, go 1.26.4):

- `go test ./... -count=1` — all packages pass; `eventstoretest` conformance
  still green with zero suite change after the append transaction gained
  envelope maintenance.
- `go test ./internal/harness/adapters/sqlite/ -count=1 -race` — pass.
- Codec: canonical field order, lossless round-trip, digest coverage of
  every field (formatVersion guarded by construction and decode-time
  digest verification), genesis constant, registry fail-closed.
- Chain integration: audit columns/outbox/head digest atomic with the
  batch (fault-point rollback proof); exact retry keeps a single envelope;
  backfill determinism reproduces the maintained chain exactly; a wrong
  pre-seeded digest aborts.
- Publication matrix: idempotent republication; incremental export writes
  immutable new generations; staging leftovers discarded; checkpoint
  behind the manifest converges; a lost replica regenerates
  byte-identical segments from canonical data; tampered segments and
  conflicting generations quarantine; foreign live export leases refuse.
- Import: full verification happy path; tampered, missing, torn-final-line,
  and wrong-head-digest replicas all refuse; a crafted envelope with valid
  digests but a sequence gap is caught by the deep layers; non-empty
  destinations refuse.

## Benchmark sample

`go test ./internal/harness/adapters/sqlite/ -run XXX -bench . -benchtime 20x`:

```text
BenchmarkAppend1Event-8       20    335821 ns/op    14388 B/op     336 allocs/op
BenchmarkAppend8Events-8      20    740879 ns/op    48133 B/op     896 allocs/op
BenchmarkReadStreamPaged-8    20   4170244 ns/op  2893469 B/op   66921 allocs/op
BenchmarkBackup-8             20   2357612 ns/op    13517 B/op     184 allocs/op
BenchmarkExportOnce-8         10   24056229 ns/op  2461054 B/op   50337 allocs/op
BenchmarkImportAudit-8        10   11096696 ns/op  2881996 B/op   66861 allocs/op
```

Append overhead added by envelope maintenance: 336µs vs 288µs
pre-Slice-3 for one event (~17%); 741µs vs 602µs for eight events (~23%).
`ExportOnce` drains a 100-append (201-event) database including the full
restart inventory; `ImportAudit` verifies and lands a 50-append replica.

## Deviations from the accepted design

None on mechanism. Segment digest prefixes in filenames use the first six
digest bytes for readability while the manifest records full digests.

## Deferred GA blockers

- Power-loss device testing of the directory-synchronization caveat
  (derived files may be lost; domain facts cannot).
- Multi-process exporter contention beyond the lease refusal test.
- Long-running replica soak and adversarial fuzzing of import.
