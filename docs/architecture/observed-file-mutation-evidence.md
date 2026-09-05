# Observed File Mutation Completion Evidence

- Scope: [Implemented observed file mutation contract](observed-file-mutation.md)
- Design: [Observed-state safe file mutation](../superpowers/specs/2026-09-04-observed-file-mutation-design.md)
- Plan: [six-task implementation plan](../superpowers/plans/2026-09-05-observed-file-mutation.md)
- Status: Tasks 1–6 implemented and locally verified; internal pre-v0, not GA

This ledger separates original task evidence from the fresh final ordinary-PR
gate. It makes no live-model, network, provider, or API-key claim.

## Commit ledger

| Task | Commit | Evidence delivered |
| --- | --- | --- |
| design / plan | `73f3098` | accepted observed-state design and six-task plan |
| 1 values and errors | `c5dea0f` | opaque values, guards, 1 MiB bound, seven error families |
| 1 contract correction | `5998f73` | required wire values `fs_is_directory`, `edit_no_match`, `edit_ambiguous`; Task 1 re-review approved |
| 2 guarded adapter | `111f530` | final FileSystem port, staged atomic workspacefs implementation, and approved MemFS migration deviation |
| 3 observations | `895789e` | process-local per-session observations, lifecycle clears, fixed recovery mapping |
| 4 edit tool | `6620472` | fifth closed-schema `edit_file`, parse/dispatch and Policy/Approver ordering |
| 5 proof | `d87b206` | concurrent/stale/exec/lifecycle scenarios and mutation evidence |
| 6 documentation | `0713b8e` | synchronized contract, reading copy, indexes, security limit, and this ledger |

Task 2's brief omitted `internal/harness/testkit/memfs.go` and
`memfs_test.go`; the migration was approved because every compile-time
`tools.FileSystem` caller had to adopt the final port and retaining an old test
adapter would make the repository uncompilable or less faithful. It adds no
production capability beyond equivalent guarded semantics.

## TDD and focused evidence recorded by the implementing tasks

| Task | RED then GREEN / focused result |
| --- | --- |
| 1 | `go test ./internal/harness/tools -run 'TestMutationGuard|TestFilesystemErrorCodes' -count=1` first failed for missing values/codes, then passed; the re-review found the initial three wrong wire values and `5998f73` corrected them. |
| 2 | `go test ./internal/harness/adapters/workspacefs ./internal/harness/tools/porttest -count=1` first failed against old signatures, then passed. The incomplete-UTF-8-at-EOF regression first failed in `TestGuardedMutationPort` and `TestMemFSGuardedMutation`, then passed after the bounded-read correction. |
| 3 | `go test ./internal/harness/application -run TestFileObservations -count=1` first failed for absent table, then passed. Wiring tests first returned generic invalid arguments/create guards, then passed after observation/error/lifecycle wiring. |
| 4 | `go test ./internal/harness/tools -run 'TestDefaultWorkspaceSpecs|TestValidateArgs|TestNewCatalog' -count=1` first failed on `undefined: NameEditFile`, then passed (`ok .../tools 0.010s`). `go test ./internal/harness/application -run 'Test.*EditFile|Test.*Edit.*Approval' -count=1` first returned `unknown_tool`, then passed (`ok .../application 0.023s`). |
| 5 | Initial scenario fixture exposed only an append-identity collision from a fresh deterministic ID generator; it was corrected without production change. `go test ./internal/harness/adapters/workspacefs ./internal/harness/application -run 'Test.*Mutation' -count=1` then exited 0 (`workspacefs 0.135s`; `application 0.033s`). |

Prior tasks recorded successful focused packages, repository tests, vet, and
race/repetition where applicable. This ledger does not silently upgrade those
historical runs into Windows runtime coverage.

## Task 5 mutation and concurrency proof

Task 5 temporarily inverted both stale-version equality checks (`!=` to `==`).
Task 5 records that its targeted matrix exited 1. The exhaustive failing test/subtest list for that mutant run was:

```text
TestMutationPublicationFailurePreservesDestinationAndCleansStage/replace
TestGuardedMutationPort
TestMutationConcurrentWritersFromOneVersionHaveOneWinner
TestMutationGuardedWrite
TestMutationEditLiteralAndNewlines/unique
TestMutationEditLiteralAndNewlines/missing
TestMutationEditLiteralAndNewlines/ambiguous
TestMutationEditLiteralAndNewlines/all
TestMutationEditLiteralAndNewlines/lf
TestMutationEditLiteralAndNewlines/crlf
TestMutationEditLiteralAndNewlines/dominant_crlf
TestMutationEditLiteralAndNewlines/delete
TestMutationGuardBeforeMatchAndExternalChange
TestMutationRejectsInvalidTargetsAndText
TestMutationRejailsAndPreservesMode
TestFileMutationObservationIsSessionScopedAndSurvivesOrdinaryTurns
TestFileMutationExecBypassIsDetectedByFollowingStructuredEdit
```

The reported concurrent result was `0 success, 2 stale; want exactly one of
each`; the exec scenario lost its bounded stale result. The mutant was restored
and `git diff -- internal/harness/adapters/workspacefs/mutation.go` was empty
before GREEN reruns. The Task 5 report calls this failing invocation “the targeted matrix”; it does not retain a separate mutant command line. Its exact retained stale/concurrent matrix command is the successful post-restoration command shown below. This ledger does not reconstruct a missing invocation.

A second mutant temporarily disabled `count > 1 && !replaceAll`. Its edit matrix exited 1 with exactly `TestGuardedMutationPort` (`fs_test.go:32: <nil>`) and `TestMutationEditLiteralAndNewlines/ambiguous` (`mutation_test.go:106: <nil>`). The report similarly does not retain a separate edit-matrix command; it records the mutation, two exact failures, restoration, and an empty `git diff -- internal/harness/adapters/workspacefs/mutation.go` before the post-restoration GREEN matrix. No command is invented here.

The recorded repetition command exited zero without a race report:

```bash
go test -race ./internal/harness/adapters/workspacefs ./internal/harness/application \
  -run 'Test.*(Mutation|Observation|Stale|Concurrent|Resume)' -count=10
```

It proved one winner / one stale structured replacement, one winner / one
`fs.ErrExist` creator race, complete winner contents, session-local and
runtime-local observation, lifecycle clearing, and exec-bypass detection by a
later structured edit. The Close/Delete clearing assertions are deliberately
narrow reflection checks of private state because no subsequent public mutation
path exists after those terminal calls. This is an approved Minor caveat, not a
claim of a public lifecycle observation seam.

## Fresh Task 6 ordinary-PR gate

The following commands were run from the repository root after the Task 6 documentation edits; this ledger records their results. The implementer report contains the fuller execution narrative:

```text
go test ./internal/docsguard ./internal/harness/architecture -count=1
git diff --check
cd cmd/acp-web-bridge/web && npm ci && npm run build
go vet ./...
go test -race ./... -count=1
env CGO_ENABLED=0 go build ./...
env GOOS=windows go build ./...
env GOOS=darwin go build ./...
```

The ordinary-PR scheduled matrix is not fabricated: this local run executes
the exact requested gate, not an external scheduler.

## Limits retained by the evidence

- Structured writes sharing one `workspacefs` instance serialize; uncooperative
  external/kernel writers have no atomic CAS, including the external final
  check-to-rename race.
- `exec` is not mediated by observation guards. The test proves later stale
  detection, not exec serialization.
- Observations are process-local and non-persistent. Successful Resume, Close,
  and Delete clear them; `LoadSession` does not.
- Windows runtime is not tested or claimed. Windows and Darwin builds are
  compile-only from a Linux host.
- The slice uses no live DeepSeek/model/provider request and no API key.

## Fresh gate results

`go test ./internal/docsguard ./internal/harness/architecture -count=1` exited 0
in 4.5s (`docsguard` 0.173s; `architecture` 0.342s). `git diff --check` exited
0; elapsed time was not captured. `npm ci && npm run build` exited 0 in 8.0s
(83 packages, 0 vulnerabilities; Vite build 59ms). `go vet ./...` exited 0 in
4.2s. The chained CGO-disabled, Windows, and Darwin `go build ./...` commands
exited 0 in 23.7s. Windows and Darwin individual elapsed times were not
captured; both are compile-only evidence, never runtime coverage.

The first exact `go test -race ./... -count=1` run exited 1 after about 307s.
Only `internal/harness/adapters/sqlite` failed:
`TestConformance/limits_copies_cancellation_and_corruption` reported `rejected
over-limit request leaked identities: store/writer_fenced
(session=session-request-plus-one expected=0 actual=0 identity_kind=
may_have_committed=false)`. The Task 6 diff was docs-only and Tasks 4–5 contain
no SQLite paths. The focused investigation ran:

```text
go test -race ./internal/harness/adapters/sqlite -run 'TestConformance/limits_copies_cancellation_and_corruption' -count=1
# exit 0 in 17.925s
go test -race ./internal/harness/adapters/sqlite -run 'TestConformance/limits_copies_cancellation_and_corruption' -count=3
# exit 0 in 38.379s (4/4 focused passes)
```

The exact full race command was rerun once without a SQLite change and exited 0
in about 250s, including SQLite in 59.0s. This ledger retains the initial
intermittent failure; it does not relabel that first execution as a pass.
