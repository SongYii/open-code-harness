# ACP session lifecycle (Slice B) completion evidence / ACP 会话生命周期（切片 B）完成证据

**Status:** Complete evidence ledger

**Date:** 2026-08-29

**Design:** [ACP Session Lifecycle (Slice B)](../superpowers/specs/2026-08-27-acp-session-lifecycle-slice-b-design.md)

**Contracts:** [ACP v1 adapter](acp-v1.md) (session/list, session/resume,
session/close, session/delete, workspace canonicalization), [Session
transcript](session-transcript.md) (`session.deleted` fact), [SQLite
canonical EventStore](sqlite-eventstore.md) (migration 4 session head
catalog, `ListSessionHeads`)

This ledger records Slice B — capability-gated ACP `session/list`,
`session/resume`, `session/close`, and `session/delete` over the existing
persistent Session model, plus the logical `session.deleted` domain fact
that makes deletion durable, replayable, and auditable without physically
erasing anything — as implemented on this branch. Completion is claimed from
the evidence below, not from checkbox state.

英文为规范记录。

## Commits

| Task | Commit | Subject |
| --- | --- | --- |
| 1 | `af91e9c` | Domain: logical session deletion fact (`SessionStatusDeleted`, `DeleteSession`, `SessionDeleted`, codec/apply/decide/clone/oracle coverage) |
| 2 | `19526c2` | Application: `ListSessions`, `ResumeSession`, `DeleteSession` use cases; canonical workspace-root helper; deleted-session ordinary-load boundary |
| 3 | `666759b` | Application: memory-backed session catalog and shared `EventStore.ListSessionHeads` conformance fixture |
| 4 | `7f1223d` | SQLite: migration 4 shadow-table swap to the v4 `session_heads` shape, synchronous per-append maintenance, rebuild/audit-import/recovery consumers |
| 5 | `1501175` | SQLite: one-transaction keyset `ListSessionHeads` query, base64url cursor, `sqlite.Reader` unaffected |
| 5 fix | `0d44866` | Strict base64url cursor decoding (reject noncanonical padding); bounded race-test waits |
| 6 | `484088d` | ACP: `session/list`/`resume`/`close`/`delete`, explicit five-state wire machine, workspace canonicalization at `Serve` construction |
| 6 fix | `6da3e2a` | `session/load` gains the same `CanonicalWorkspaceRoot`/empty-MCP-list validation as `session/resume`; lock-scoped reads of shared wire-entry fields |
| 7 | `6bb3c05` | `session.deleted` transcript fact, implemented-contract and evidence publication |
| 7 review | `1455d9c` | Domain-event catalog completion, evidence/test-table corrections, and stale exclusion pointers |
| Post-review 1 | `46e0a59` | Durable close revalidation and canonical-append rollback proof |
| Post-review 2 | `af35fe8` | Domain-closed close rejection, migration-4 deleted-session compatibility, and cursor integer-bound parity |

## Mapping-table tests

| Surface | Tests |
| --- | --- |
| Domain deletion | `TestDecideDeleteSessionAcceptsOnlyIdleActiveOrClosedSession`, `TestApplySessionDeletedIsTerminalFromIdleActiveOrClosed`, `TestApplyCompactMatchesHistoricalSessionDeletion`, `TestRecordedEventJSONSessionDeletedIsStrictAndCanonical` |
| Application lifecycle | `TestListSessionsUsesCanonicalFixedPageAndMapsCatalogErrors`, `TestResumeSessionRequiresSameWorkspaceAndActiveIdleState`, `TestDeleteSessionUsesCanonicalAppendAndNonEnumeratingBoundary`, `TestRunTurnAndDeleteSessionHaveOneCASWinner` |
| SQLite migration 4 | `TestMigration4MigratesEmptyV3Database`, `TestMigration4ReplaysPopulatedV3Head`, `TestMigration4RejectsNonLegacyRunningStatus`, `TestMigration4RebuildsMissingLegacyHead`, `TestMigration4MismatchRollsBackToV3`, `TestMigration4MalformedEventRollsBackToV3`, `TestMigration4OrphanLegacyHeadRollsBackToV3`, `TestImportMaintainsSessionHead` |
| SQLite append head integrity | `TestAppendSessionHeadCorruptionRollsBackWholeCanonicalAppend`, `TestAppendRejectsMissingSessionHeadForExistingStream`, `TestAppendRejectsMissingHeadForExistingVersionZeroStream`, `TestAppendRejectsStaleSessionHeadPosition` |
| SQLite keyset catalog | `TestListSessionHeadsFiltersDeletedBeforeLimitAndPaginates`, `TestListSessionHeadsBreaksCommitPositionTiesBySessionID`, `TestListSessionHeadsStrictlyDecodesCursorsAndBindsCursorValues`, `TestListSessionHeadsConvertsUTCAndRejectsCorruptVisibleRows`, `TestListSessionHeadsUsesOneSnapshotDuringConcurrentAppend`, shared `session_head_catalog` conformance cases (including duplicate JSON keys) |
| ACP capabilities golden | `TestServeInitializeAdvertisesSessionLifecycleCapabilities` |
| ACP list | `TestServeSessionListReturnsWorkspaceSessions` |
| ACP resume | `TestServeSessionResumeReattachesAndRejectsIneligible` |
| ACP close | `TestServeSessionCloseCancelsSettlesAndDetaches`, `TestServeSessionLifecycleRejectsDuringClosingAndDeleting`, `TestServeSessionCloseReservesClosingBeforeDurableCheck`, `TestServeSessionCloseRejectsDomainClosedSession` |
| ACP delete | `TestServeSessionDeleteBlocksPromptEntryAndIsIdempotent`, `TestServeSessionDeleteRestoresStateAfterInternalFailure` |
| ACP fixed error strings | `TestServeSessionLifecycleErrorsDoNotLeakDetails`, `TestServeSessionCloseRestoresSettledPromptAfterDurableInternalFailure` |
| Composition end-to-end | `TestAssemblyServesACPTurnEndToEnd` (extended through list/close/resume/delete and a durable-stream deletion-evidence read) |
| Transcript deletion fact | `TestProjectRecordFrozenPayloads/session_deleted`, `TestGoldenFixturesRoundTrip/testdata/facts.jsonl`, `TestWriteSessionSnapshotBits/deleted_session`, `TestWriteSessionDeletedSessionExportsDeletionFact` |

## Golden JSONL hash

SHA-256 of `internal/harness/transcript/testdata/facts.jsonl` as of this
ledger (the only fixture Slice B changes — a trailing `session.deleted` line
was appended; `snapshot.jsonl` and `complete.jsonl` are unchanged from the
[conversation and session transcript ledger](conversation-and-transcript-evidence.md)):

| File | SHA-256 |
| --- | --- |
| `facts.jsonl` | `82bbaeeb54b6452f493c8d4fc722045d7d69ff554377636e9e9e479134c5b452` |

## Original Slice B verification commands and output (historical at `6bb3c05`)

All keyless and network-free.

```text
$ test -z "$(gofmt -l .)"
(clean)

$ go vet ./...
(clean)

$ go test -count=1 ./...
ok   github.com/SongYii/open-code-harness/cmd/och
ok   github.com/SongYii/open-code-harness/internal/docsguard
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/acp
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/localexec
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/memory
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/system
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/workspacefs
ok   github.com/SongYii/open-code-harness/internal/harness/application
ok   github.com/SongYii/open-code-harness/internal/harness/architecture
ok   github.com/SongYii/open-code-harness/internal/harness/composition
ok   github.com/SongYii/open-code-harness/internal/harness/domain
ok   github.com/SongYii/open-code-harness/internal/harness/engine
ok   github.com/SongYii/open-code-harness/internal/harness/policy
ok   github.com/SongYii/open-code-harness/internal/harness/runtime
ok   github.com/SongYii/open-code-harness/internal/harness/testkit
ok   github.com/SongYii/open-code-harness/internal/harness/tools
ok   github.com/SongYii/open-code-harness/internal/harness/transcript

$ go test -race -count=1 ./internal/harness/domain/ ./internal/harness/application/ \
  ./internal/harness/adapters/acp/ ./internal/harness/adapters/sqlite/ \
  ./internal/harness/transcript/ ./internal/harness/composition/
ok   github.com/SongYii/open-code-harness/internal/harness/domain
ok   github.com/SongYii/open-code-harness/internal/harness/application
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/acp
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite
ok   github.com/SongYii/open-code-harness/internal/harness/transcript
ok   github.com/SongYii/open-code-harness/internal/harness/composition

$ go test -race -count=1 ./...
ok   (every listed package)

$ go test -race -count=5 ./internal/harness/adapters/sqlite -timeout 300s
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite

$ git diff --check
(clean)

$ git status --short
(clean at `6bb3c05`)

$ GOOS=windows GOARCH=amd64 go build ./...
(clean)

$ GOOS=darwin GOARCH=arm64 go build ./...
(clean)
```

The plan's literal cross-target `go test ./...` commands were also attempted.
Both compiled the target test binaries and then failed when this linux/amd64
host tried to execute them (`fork/exec ...: exec format error`). The two
cross-target `go build ./...` commands above are the executable portability
checks used in their place; the cross-target test attempts are not recorded as
passing.

## Post-review repair verification (2026-08-30)

This working-tree repair is based on `7ac09d6`. It is intentionally recorded
separately from the frozen original Slice B output above; the review fixes were
uncommitted when these commands ran.

```text
$ gofmt -l .
(clean)

$ go vet ./...
(clean)

$ go test -count=1 ./...
ok (all packages)

$ go test -race -count=1 ./...
ok (all packages)

$ GOOS=windows GOARCH=amd64 go build ./...
(clean)

$ GOOS=darwin GOARCH=arm64 go build ./...
(clean)

$ git diff --check
(clean)
```

The repair's RED checks reproduced all reviewed failures before implementation:
a later prompt entered while close was still validating, close mapped an
internal validation failure to `-32602`, missing and stale `session_heads`
rows (including an existing version-zero stream) were accepted, and cursors
with duplicate top-level keys decoded successfully. The focused tests passed
after the fixes. A mutation that suppressed close's prompt-settlement marker
made the restored session remain spuriously busy, and the corresponding ACP
regression failed as expected before the mutation was reverted.

## Mutation check

Disabling `sessionPrompt`'s `entry.state != wireIdle` admission check (Task
6) crashed `TestServeInitializeNewPromptAndBusyReject` immediately (`panic:
close of closed channel`) rather than degrading silently, confirming the
wire-state guard is load-bearing. The mutation was reverted before
committing; see the Task 6 report in the (gitignored) SDD ledger for detail.

## Design deviation from the literal state chart

The design doc's wire diagram reads `running -> terminal response ->
idle`. On ordinary completion, the implementation keeps `running -> idle ->
terminal response -> promptDone closes`; if close has already moved the entry
to `closing`, prompt settlement preserves `closing`, writes the terminal
response, and only then closes `promptDone` so close can transition to
`detached`. This preserves the original synchronous guarantee that a client
which reacts to an ordinarily completed prompt's terminal response by
immediately sending another prompt never races a stale `running` read (the
literal ordering would reopen that race), while still guaranteeing
`session/close` never reports `{}` before the cancelled prompt's own terminal
frame is on the wire — the property the design's ordering exists to protect.
`frameWriter` serializes each complete frame write across the prompt, close,
and delete goroutines, so this settlement order cannot interleave JSON response
bytes.
`session/close` and `session/delete`
also had to become goroutine-dispatched off the shared frame-reading
goroutine (mirroring the pre-existing `session/prompt`/`runPrompt` split)
rather than fully synchronous: a synchronous implementation would block the
single duplex reader on the Application call or the close wait, making the
documented "deleting blocks a same-session prompt" race unobservable and
starving unrelated sessions on the same duplex.

## Exclusions

Recorded as out of this slice, not as deferred bugs inside it:

- ACP v2, protocol-version negotiation, auth, `session/set_mode`,
  `session/set_config_option`, session fork, batch deletion, undelete, and
  physical retention/garbage collection of a deleted Session.
- `additionalDirectories`, session-scoped MCP configuration, and MCP client
  construction — accepted only as empty and never acted on.
- A new `och` session-management subcommand; this slice is an ACP surface.
- Titles generated from user prompts, search, tags, status metadata, or any
  data beyond ACP's required `SessionInfo` fields.
- A multi-page historical snapshot guarantee for `session/list`; each page
  is one read transaction, and concurrent writes can move a session between
  pages.
- Changing the conversation projector, the audit JSONL codec, or the
  token-aware-context/compaction boundary.

## Remaining

- `SessionID` is parsed once in `application.DeleteSession` and, for some
  ACP handlers, again for wire-level validation; not consolidated into a
  single parse (deferred from Task 6 review).
- Surfaces remain `experimental`; not GA.
