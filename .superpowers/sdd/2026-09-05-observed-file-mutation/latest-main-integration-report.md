# Latest-main integration report: observed file mutation safety

Date: 2026-09-06
Branch: `fix/observed-file-mutation-safety`
Base: `origin/main` (`b2771440b3d7d502024abc069954004e415e9282`)

## Status

Integration is complete. The pending evidence-ledger correction was committed
as `de2f8a5` (`docs: map observed mutation fixes onto latest main`). The branch
is clean after verification.

## Conflict-resolution and SHA mapping

The prior integration sequence was retained without rewriting history:

| Reviewed source | Integrated commit | Role |
| --- | --- | --- |
| `4a44822` | `a74309e8314035fc4d14d11428ce6ab25542ace0` | Fix A regression tests |
| `0d98071` | `4b6dbd24dc1829c0171946067fb759bfc0d5e425` | Fix A implementation |
| `86d9dc2` | `3367a5b34a7313198fc6082f0ee9f5e69da9dd28` | Fix B regression tests |
| `bbaf36f` | `0714c68aea0a16f0b45344f1896402971b2285bd` | Fix B implementation |
| `102d706` | `ef27018efda1731aff085d59dd7a8fcfabe9e81d` | synchronized docs |
| — | `de2f8a5` | latest-main evidence SHA mapping |

The original five-task commits (`148004e`, `bae2919`, `f059612`, `0619cf8`,
`8ea5d63`) are already ancestors of `origin/main`; they were not replaced or
removed. The five reviewed source SHAs above are retained only as provenance,
with implementation/audit claims mapped to the integrated SHAs.

## Scope audit

`git diff --name-status origin/main...HEAD` contains only the observed-file-
mutation contract/docs and its implementation/tests:

- architecture and plan/evidence documentation (English and Chinese);
- `workspacefs` bounded edits, private `filePublisher`/`osPublisher`, atomic
  publication verification, fault tests, and FIFO/non-regular tests;
- application observation/recovery mapping and tests;
- tool error vocabulary/tests and the `MemFS`/filesystem test ports.

No MCP, evaluation variance, scheduled-lane, CI, or unrelated files changed.
Production `mutationHooks`, `beforePublish`, and `afterPublish` are absent.
The private publisher seam remains, with pre-publication failure tests and
post-publication interference tests for both rename and in-place mutation.
All eight codes remain covered: `fs_not_observed`, `fs_not_found`,
`fs_stale_version`, `fs_edit_not_found`, `fs_ambiguous_edit`,
`fs_not_regular_file`, `fs_not_text`, and `fs_too_large`.

## Verification

All commands below were run on this branch and exited successfully:

- `go test -race ./internal/harness/adapters/workspacefs ./internal/harness/application ./internal/harness/tools -run 'Test(EditRefusesAFileOverTheBound|MutationPostPublicationChangeReturnsStale|FilesystemErrorCodes|EditFileAfterMissingReadReturnsNotFoundWithoutAdvancingObservation|WriteFileRefusesToOverwriteAnUnobservedFile|MutationRejectsFIFOAsNotRegularFileBeforeOpening|ReadThenGuardedWriteThenStaleWrite|ARefusedMutationLeavesNoStagingBehind)' -count=10`
- `go test -race ./internal/harness/adapters/workspacefs ./internal/harness/application -run 'Test.*(Mutation|Observation|Stale|Concurrent|Resume|Edit)' -count=10`
- `go test ./internal/docsguard ./internal/harness/architecture -count=1`
- `go test ./... -count=1`
- `go vet ./...`
- `GOOS=windows go build ./...`
- `GOOS=darwin go build ./...`
- `npm run build` in `cmd/acp-web-bridge/web` (no frontend files are in the branch diff; this is a baseline PR-gate check)
- `git diff --check`
- `git diff --check origin/main...HEAD`

The scheduled Context matrix was not run because it is intentionally gated by
`OCH_EVAL_SCHEDULED_CONTEXT_MATRIX` and only a schedule-triggered CI job sets
that variable; this branch does not alter that lane.

## Final state

`git status --short` is clean. No push or history rewrite was performed.
