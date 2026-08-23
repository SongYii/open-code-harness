# Conversation and session transcript completion evidence / 对话面与会话转录完成证据

**Status:** Complete evidence ledger

**Date:** 2026-08-23

**Contracts:** [ACP v1 Adapter](acp-v1.md), [Session transcript](session-transcript.md), [SQLite OpenReader](sqlite-eventstore.md#openreader)

This ledger records Slices A / A′ as implemented on this branch. Completion
is claimed from the evidence below, not from checkbox state.

英文为规范记录。

The Slices A/A′ design spec lands in PR 1 (stack assembly). This branch
documents the implemented mapping, clip bounds, transcript schema,
OpenReader, and `export-session` without re-landing that Draft.

## PRs

| PR | Commit | Subject |
| --- | --- | --- |
| 1 | spec branch (`2d3174f`) | Design spec and Chinese reading copy (not on this branch) |
| 2 | `3862e81` | ACP live `tool_call` updates, namespaced permission id, workspace admission |
| 3 | `a7687f1` | Transcript schema, codec, golden fixtures, `ownerTranscript` |
| 4 | `528b55e` (`f95037e` review fix) | SQLite `OpenReader` without taking the runtime lease |
| 5 | `a6b6b78` | ACP `session/load` replay through `ProjectRecordedEvent` |
| 6 | `f43beab` | `WriteSession` with snapshot and complete trailer |
| 7 | `17d3190` | `composition.ExportSession` and `och export-session` |
| 8 | this commit | Implemented contracts, Chinese reading copies, this ledger, authority rows |

## Mapping-table tests

| Surface | Tests |
| --- | --- |
| ACP live | `TestProjectRuntimeEvent`, `TestServePromptProjectsLiveToolCallsAndCodeOnlyFailed`, `TestServePromptSwallowsSessionUpdateWriteErrors` |
| ACP load | `TestProjectRecordedEvent`, `TestServeLoadReplaysToolBearingHistory`, `TestServeLoadFailsOnSessionUpdateWriteError` |
| ACP clip / id / kind | `TestClipBounds`, `TestToolCallIDAndToolKind` |
| ACP workspace | `TestServeLoadAndPromptRejectForeignWorkspace` |
| Transcript catalog | `TestProjectRecordFrozenPayloads`, `TestProjectRecordOmitsRequestAndPolicy`, `TestProjectRecordRejectsUnknownDomainType`, `TestProjectRecordStepRefAlignment` |
| Transcript goldens | `TestGoldenFixturesRoundTrip`, `TestSnapshotAndCompleteGoldensMatchSpec`, `TestDecodeSkipsUnknownFactTypesOnly` |
| WriteSession | `TestWriteSessionSnapshotBits`, `TestWriteSessionCompleteTrailerAndFactLines`, `TestWriteSessionDoublePinnedIgnoresLaterAppend`, `TestWriteSessionCancelAfterSnapshotOmitsComplete`, `TestWriteSessionLineLimitOmitsComplete` |
| OpenReader vs live lease | `TestOpenReaderDoesNotAcquireLease`, `TestOpenReaderReadsWhileLeasedWriterCommits`, `TestOpenReaderWaitsOnWriterImmediateLock`, `TestReaderTypeHasNoAppend` |
| CLI | `TestExportSessionStdoutStartsWithSnapshotEndsWithComplete`, `TestExportSessionCancelledOutputLeavesDestAbsent`, `TestExportSessionOutputPublishesCompleteFile`, `TestProductionFilesDoNotImportTranscriptOrSQLite` |

## Golden JSONL hashes

SHA-256 of `internal/harness/transcript/testdata/` as of this ledger:

| File | SHA-256 |
| --- | --- |
| `snapshot.jsonl` | `50fc9326ba416fc87a216ae6e9d0e359cf381f4dc2d2295718704cdffa47f19f` |
| `facts.jsonl` | `70718d2b740b72b5fdb92e35e6a543fcd6dda2044bd725dcbd1fd368815f2b94` |
| `complete.jsonl` | `debe4e7ae626946ff1edc9cd85413f0a8fef632e8db8738f5d2b18f5afdc844f` |

Fixtures are byte-stable `encoding/json` round-trips with RFC3339Nano
timestamps. Snapshot and complete goldens have no `eventId`, `commandId`,
or `sequence` keys.

## Verification commands

All keyless and network-free.

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./internal/harness/adapters/acp/ ./internal/harness/transcript/ \
  ./internal/harness/adapters/sqlite/ ./internal/harness/composition/ \
  ./internal/harness/architecture/ ./internal/docsguard/ ./cmd/och/ -count=1
go test -race ./internal/harness/adapters/acp/ ./internal/harness/transcript/ -count=1
```

## Exclusions

Recorded as out of this slice, not as deferred bugs inside it:

- Maze / trajectory UI and verdict heuristics (community visualizers).
- ACP v2; `session/list`, `session/resume`, `session/delete`.
- TypeScript TUI; Context Engine / token-aware compaction implementation
  (constraint only: compaction must be future domain events).
- MCP client; parallel tool execution.
- Changing the Slice 3 audit JSONL codec.
- Writing `transcript_entries` / `snapshots`.
- Copying foreign on-disk layouts or event names.
- New Application projection port; `RuntimeEvent` payload enrichment.
- Subagent `origin` field; redacted export.
- ACP `initialize` `protocolVersion` negotiation.
- Permission reverse-RPC waiter cleanup on cancel.
- Import of transcript JSONL into EventStore.

## Remaining

- Live ACP tool cards still omit `rawInput` and result content (thin
  `RuntimeEvent`; documented fidelity gap).
- `stopReason` still maps every `TurnStatusInterrupted` to `cancelled`.
- Default gate does not spawn `cmd/och`.
- Surfaces remain `experimental`; not GA.
