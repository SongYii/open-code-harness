# SQLite Canonical EventStore Completion Evidence

**Status:** Complete evidence ledger for Slice 2

**Contract:** [SQLite Canonical EventStore — Implemented Contract](sqlite-eventstore.md)

**Branch:** `agent/sqlite-canonical-eventstore`

## Commits

| Commit | Task | Content |
| --- | --- | --- |
| `103204e` | Docs | Architecture gate, bilingual spec, and bilingual plan |
| `9d7f187` | Task 1 / PR 1 | Driver introduction, open profile verification, version gate, full-shape migrations, corruption fail-closed path |
| `35523bd` | Task 2 / PR 2 | Append transaction: exact retry, CAS, admission, domain identities, result-code classification |
| `c310f59` | Task 3 / PR 3 | Pinned pagination, append resolution, command request lookup, reader/writer atomicity |
| `74c65f1` | Task 4 / PR 4 | Fencing lease acquire/renew, per-append predicate, unknown-outcome protocol, conformance suite green |
| `6ef089d` | Task 5 / PR 5 | Backup, projection rebuild, benchmarks, contract and evidence publication |

## Dependency and license inventory

First external dependency of the repository. From `go.mod`:

| Module | Version | Role | License |
| --- | --- | --- | --- |
| `modernc.org/sqlite` | v1.56.0 | Pure-Go SQLite driver (direct) | BSD-3-Clause |
| `modernc.org/libc` | v1.74.4 | Driver runtime (indirect) | BSD-3-Clause |
| `modernc.org/mathutil` | v1.7.1 | Driver utility (indirect) | BSD-3-Clause |
| `modernc.org/memory` | v1.11.0 | Driver utility (indirect) | BSD-3-Clause |
| `golang.org/x/sys` | v0.47.0 | Driver platform layer (indirect) | BSD-3-Clause |
| `github.com/dustin/go-humanize`, `github.com/google/uuid`, `github.com/mattn/go-isatty`, `github.com/ncruces/go-strftime`, `github.com/remyoudompheng/bigfft` | as pinned | Driver indirects | MIT |

Bundled SQLite at observation: 3.5x line, verified ≥ 3.42 by the open-time
`sqlite_version()` gate. `CGO_ENABLED=0` builds pass for linux, darwin, and
windows.

## Verification evidence

Commands and observed results (Apple M1, go 1.26.4):

- `go build ./...`, `go vet ./...` — clean.
- `go test ./... -count=1` — all 12 packages with tests pass.
- `go test ./internal/harness/adapters/sqlite/ -count=1 -race` — pass,
  including reader/writer concurrency and parallel append serialization.
- `CGO_ENABLED=0 GOOS=linux|windows|darwin go build ./...` — all pass.
- `eventstoretest.Run` passes against the SQLite adapter with zero suite
  change (`TestConformance`, all ten cases).
- Spec section 13 test classes: result-code classification table (busy,
  busy-extended, locked, full, IOERR, IOERR-extended, interrupt, readonly,
  cantopen, corrupt, notadb, constraint, mismatch, internal); unknown
  outcome with one bounded fresh-connection lookup; concurrent contiguous
  commit positions (conformance case); reopen-after-termination
  consistency; journal-mode fail-closed verification (unit table);
  busy-contention bounded-unavailable live test; disk-full and injected
  IO covered through the same classification path as the unit table (no
  live ENOSPC device in CI; recorded as a GA blocker).

## Benchmark sample

`go test ./internal/harness/adapters/sqlite/ -run XXX -bench . -benchtime 50x`:

```text
BenchmarkAppend1Event-8       50   288240 ns/op   10102 B/op    259 allocs/op
BenchmarkAppend8Events-8      50   602344 ns/op   40037 B/op    828 allocs/op
BenchmarkReadStreamPaged-8    50  4624678 ns/op 2893281 B/op  66922 allocs/op
BenchmarkBackup-8             50  2150897 ns/op   13506 B/op    182 allocs/op
```

`ReadStreamPaged` reads a full 400-event stream (two 256-record pages) per
operation, including canonical JSON decode of every record. `Backup` copies
a 100-append database with verification.

## Deviations from the accepted design

1. The backup uses `VACUUM INTO`, not the Online Backup API: the pure-Go
   driver does not export the backup facility (`NewBackup` exists only on
   an internal type). SQLite documents `VACUUM INTO` as producing the same
   consistent snapshot; the copy is verified before success is reported.

## Deferred GA blockers

- Process-level crash-injection harness (unclean shutdown mid-WAL).
- Long-running soak and corruption fuzzing against the database file.
- Live `SQLITE_FULL` device-level evidence (classification is unit-proven).
- Multi-process writer evidence beyond the lease predicate and busy tests.
