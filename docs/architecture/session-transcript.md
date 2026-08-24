# Session Transcript — Implemented Contract

**Status:** Implemented; not GA

**Stability:** `experimental`

**Evidence:** [Conversation and session transcript completion evidence](conversation-and-transcript-evidence.md)

**Package:** `internal/harness/transcript`

**Wiring:** `composition.ExportSession`; `cmd/och export-session`

## Scope

Experimental session-facing JSONL export of one EventStore session.
Schema name `och.session.transcript`, `formatVersion` 1. One projected
fact per line. EventStore remains the sole live commit authority;
transcript is a projection, not a replica, not a commit point, and not
writable back into the store.

This is not the Slice 3 audit replica (one line per atomic append with a
digest chain). It is not ACP conversation. `adapters/acp` must not import
this package; this package must not import ACP or any adapter.

## Envelope

One UTF-8 JSON object per line. No embedded raw newlines. Encoded line
limit **2 MiB** (`line_limit`; fail closed, do not skip).

Three wire structs. Fact lines use frozen key order
`formatVersion, schema, sessionId, eventId, commandId, sequence, occurredAt, type, payload`.
Integrity lines (`transcript.snapshot`, `transcript.complete`) omit
`eventId`, `commandId`, and `sequence`.

`sequence` is the EventStore per-session sequence, never omitted on a
fact, never dense. Omitted domain types appear as gaps. `occurredAt` is
RFC3339Nano UTC (`2006-01-02T15:04:05.000000000Z07:00`). Equal timestamps
mean the same atomic batch, not concurrency.

First line of a successful export is `transcript.snapshot`. Last line is
`transcript.complete`. Consumers reject a stream or file that is missing
either trailer, whose `complete.headSequence` disagrees with the snapshot,
whose intervening fact count disagrees with `complete.factLines`, or that
has bytes after the complete newline. Sequence gaps do not excuse a
missing trailer.

| Integrity `type` | Payload |
| --- | --- |
| `transcript.snapshot` | `headSequence`, `open`, `running`, `stability` (`experimental`) |
| `transcript.complete` | `headSequence`, `factLines`, `open`, `running` |

`open` is `session.Status == active`. `running` is `ActiveTurn != nil`.
An idle active session is `open: true`, `running: false`. Snapshot and
complete `occurredAt` share the exporter clock, not a domain event.

## Fact catalog

`ProjectRecord` emits a fact line, or omits, or fails closed.

| `type` | Payload fields | Source |
| --- | --- | --- |
| `session.created` | `workspaceRoot` | `session.created` |
| `session.closed` | `{}` | `session.closed` |
| `turn.started` | `turnID`, `input` | `turn.started` |
| `turn.completed` | `turnID` | `turn.completed` |
| `turn.failed` | `turnID`, `code`, `message` | `turn.failed` |
| `turn.interrupted` | `turnID`, `reason` | `turn.interrupted` |
| `assistant.message.started` | `turnID`, `itemID`, `stepIndex`, `stepRef` | started + projector counter |
| `assistant.message.completed` | `turnID`, `itemID`, `stepIndex`, `stepRef`, `text`, `toolCalls?` | `assistant.message.completed` |
| `assistant.message.failed` | `turnID`, `itemID`, `stepIndex`, `stepRef`, `code`, `message` | `assistant.message.failed` |
| `assistant.message.interrupted` | `turnID`, `itemID`, `stepIndex`, `stepRef`, `code`, `message` | `assistant.message.interrupted` |
| `model.usage.recorded` | `turnID`, `itemID`, `inputTokens`, `outputTokens`, `cachedInputTokens`, `latencyMs`, `finishReason`, `providerRequestID` | `model.usage.recorded` |
| `tool.call.started` | `turnID`, `itemID`, `callID`, `stepIndex`, `stepRef`, `name`, `arguments` | `tool.call.started` |
| `tool.call.completed` | `turnID`, `itemID`, `callID`, `stepIndex`, `stepRef`, `content`, `truncated` | `tool.call.completed` |
| `tool.call.failed` | `turnID`, `itemID`, `callID`, `stepIndex`, `stepRef`, `code`, `message` | `tool.call.failed` |
| `tool.call.interrupted` | `turnID`, `itemID`, `callID`, `stepIndex`, `stepRef`, `code`, `message` | `tool.call.interrupted` |
| `approval.requested` | `turnID`, `itemID`, `approvalID`, `callID`, `name`, `reason` | `approval.requested` |
| `approval.resolved` | `turnID`, `itemID`, `approvalID`, `decision` | `approval.resolved` |

**Omitted (gap in `sequence`, not an error):** `model.request.recorded`,
`policy.decision.recorded`.

**Honest usage omission:** if `model.usage.recorded` was never appended,
there is no usage line. Do not emit zero tokens.

**No `origin` field.** v0 has no subagent.

Any other canonical domain type is `unsupported_event_type` (fail closed).
Unknown domain `schemaVersion` is `unsupported_schema_version`. Skip-unknown
applies only to *external* transcript fact `type` values
(`DecodeSkipsUnknown`); snapshot/complete are never skipped, and EventStore
records are never skipped.

`toolCalls` on assistant complete, when present, is domain
`[]ToolCallOffer` (`id`, `name`, `arguments`).

## `stepRef`

`stepRef` is `turnID + "/" + decimal(stepIndex)` with no spaces.

- Assistant events: the projector counts `assistant.message.started` per
  `turnID`, 1-based, and writes that count as `stepIndex`.
- `tool.call.started`: copy `ToolCallStarted.StepIndex`. Do not invent a
  second map.
- Tool terminals: use the current `steps[turnID]` (count of
  `assistant.message.started` so far). Do not rewrite a disagreeing stream.

## `WriteSession`

`WriteSession(ctx, StreamReader, sessionID, now, writer) (Result, error)`
pages `ReadStream` at 256 records with the head pinned on the first page.

1. Invalid session id → `invalid_session_id`; write nothing.
2. First page empty and `HeadVersion == 0` → `session_not_found`; write nothing.
3. Double pinned read, same `HeadVersion`, memory O(page):
   first pass `domain.Apply` for `open` / `running` and the fact count;
   second pass writes snapshot, facts, complete.
4. After snapshot is on the wire, later failure (cancel, `line_limit`,
   store corrupt, unreadable canonical payload) **does not** write
   `transcript.complete`. `Result` is returned only after the trailer.
5. A later append after the pin is not visible.

`StreamReader` has no `Append`. Tests using the memory EventStore stay in
`_test.go`.

## Export path

`composition.ExportSession(ctx, databasePath, sessionID, out) (transcript.Result, error)`
opens [`sqlite.OpenReader`](sqlite-eventstore.md) (no runtime lease, no
migrations, no provider credential), calls `WriteSession` with
`time.Now().UTC()`, and closes the reader. Composition does not print.

```text
och export-session -database PATH -session SESSION_ID [-output FILE]
```

This is a subcommand, not a serve-mode flag. It does not call
`composition.Open`. `cmd/och` does not import `transcript` or
`adapters/sqlite`. On success, stderr is one line from `Result`:

```text
och: exported session SESSION facts=N head=M open=bool running=bool
```

Stdout mode writes JSONL directly. `-output PATH` creates a same-directory
temp file, writes, `Sync`s, closes, and `Rename`s onto `PATH`. Error or
cancel removes the temp and leaves `PATH` untouched.

## Ownership

`internal/harness/architecture` owns `ownerTranscript`:

- transcript may import `domain`, `application` (`ReadStream` types), and
  stdlib except `os` / `os/exec` / `net` / `net/http`
- transcript must not import `engine`, `policy`, `tools`, `runtime`,
  `testkit`, or any adapter
- composition may import transcript; domain, engine, application, policy,
  tools, acp, sqlite, and runtime must not

## Compaction and parallel tools

Until compaction exists as domain events, transcript must not emit a
synthetic `context.compacted`, and Application must not mutate prior
`RecordedEvent` payloads. The Step loop is sequential; wall-clock
fidelity is `OccurredAt` of start and terminal events.

## Exclusions

Maze UI and verdicts; ACP v2; session resume / list / delete; writing
`transcript_entries` / `snapshots`; changing the audit JSONL codec;
redacted export; subagent `origin`; `RuntimeEvent` enrichment; copying
foreign on-disk layouts or event names; import of transcript JSONL into
EventStore.
