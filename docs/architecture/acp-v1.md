# ACP v1 Adapter — Implemented Contract

**Status:** Implemented; not GA

**Authority:** [ACP v1 Adapter (Milestone 6) design](../superpowers/specs/2026-08-22-acp-v1-adapter-design.md)

**Evidence:** [ACP v1 adapter completion evidence](acp-v1-evidence.md); Slices A/A′ mapping in [conversation and session transcript evidence](conversation-and-transcript-evidence.md); session lifecycle (list/resume/close/delete) in [ACP session lifecycle (Slice B) evidence](acp-session-lifecycle-evidence.md)

**Package:** `internal/harness/adapters/acp`

## Scope

ACP v1 JSON-RPC 2.0 over newline-delimited UTF-8. The adapter translates
initialize, session/new, session/load, session/prompt, session/cancel,
session/request_permission, session/list, session/resume, session/close, and
session/delete onto the existing Application service. Mapping lives in
adapter-owned pure functions (`ProjectRuntimeEvent`, `ProjectRecordedEvent`).
The adapter owns no domain rules.

Composition exposes `ServeACP`. `cmd/och -acp` serves stdin/stdout and
writes diagnostics only to stderr.

Conversation (user / assistant / tool cards) is this adapter. Trajectory
(usage, step identity, truncation flags, wall-clock) is
[session transcript](session-transcript.md). The two surfaces do not share a
codec and must not import each other.

## Initialize and session RPCs

- `protocolVersion` is `1`. `loadSession` is advertised, alongside
  `sessionCapabilities: {list:{}, resume:{}, close:{}, delete:{}}`.
  `authMethods` is empty. The adapter does not negotiate the client's
  version.
- `session/new` creates a Session at the assembly workspace. A non-empty
  `cwd` that does not equal that workspace is `-32602`.
- `session/load` and `session/prompt` admit the RPC only when the loaded
  Session's `WorkspaceRoot`, canonicalized with
  `application.CanonicalWorkspaceRoot` (absolute, `filepath.Clean`, no
  symlink resolution), equals the assembly workspace canonicalized the same
  way. Mismatch or unknown session is `-32602` `invalid params`, with no
  `session/update` and no `RunTurn`. The wire message does not distinguish
  missing from foreign and does not leak the foreign path. A deleted Session
  is indistinguishable from unknown at this boundary.
- `session/prompt` runs `RunTurn`. A catalog-backed turn prefixes the model
  prompt with prior user/assistant/tool messages from the event log.
  Settlement is the committed turn: `completed` → `end_turn`; any
  `TurnStatusInterrupted`, cancel category, or cancelled context →
  `cancelled`; anything else → `-32603` `session prompt failed`.
- Concurrent prompts on one session are `-32600`
  `a prompt is already in flight for this session`.
- `session/cancel` cancels the in-flight prompt context; unknown IDs are ignored.
- Permission bridging is `tools.Slot`: allow-once grants, every other
  outcome including transport failure denies. `session/request_permission`
  is a reverse RPC, not a `session/update`. Its `toolCallId` is the same
  namespaced id as the tool card (`turnID + "/" + callID`). Title is the
  tool name; kind follows the table below; status is `pending`.

## `toolCallId` and kind

`toolCallId = string(turnID) + "/" + callID`. Domain `CallID` is not
session-unique across turns. Title is the tool name.

| Tool name | ACP `kind` |
| --- | --- |
| `read_file`, `list_dir` | `read` |
| `write_file` | `edit` |
| `exec` | `execute` |
| anything else | `other` |

Live identity for a Code-only `tool.execution.failed` (empty `Text`) comes
from the prompt sink's remembered `LiveTool`, not from Domain. Sequential
`executeOneTool` makes one outstanding call honest.

## Live `session/prompt` mapping

Source: `engine.RuntimeEvent` via `RunTurnRequest.Sink`. The client already
has the user prompt; the in-flight turn does not emit `user_message_chunk`.

| Runtime event | ACP `session/update` | Notes |
| --- | --- | --- |
| `model.text.delta` (non-empty) | `agent_message_chunk` `{type:text, text}` | Clip per bounds below |
| `model.tool_call` | `tool_call` `{toolCallId, title, kind, status: pending}` | Parse `Text` as `name:callID` on the last `:`. No `rawInput` |
| `tool.execution.started` | `tool_call_update` `{toolCallId, status: in_progress}` | Includes validation / approval wait |
| `approval.requested` / `resolved` | none | Permission RPC is the UX |
| `tool.execution.completed` | `tool_call_update` `{status: completed}` | No live `content` / `rawOutput` |
| `tool.execution.failed` | `tool_call_update` `{status: failed}` | Never skip a sendable frame. `Code` stays off the wire. If `toolCallId` itself cannot fit, omit like any other tool card |
| `model.stream.*`, `append.completed` | none | Runner / store internals |

`session/update` write errors on the prompt path are swallowed
(`Emit` returns nil) so a dropped card cannot become `CodeDelivery` or
change `stopReason`. Prompt JSON-RPC **result** writes stay best-effort.
A failed `session/update` write during `session/load` fails the load RPC
with `-32603` `session prompt failed`.

## `session/load` replay mapping

Source: pinned `History.ReadStream` (page size 256, head pinned on the
first page). Empty text is skipped. Open and in-flight sessions may be
replayed; the last tool card may remain `in_progress`. Load does not emit
`session/request_permission`.

| Domain event | ACP `session/update` | Not projected |
| --- | --- | --- |
| `turn.started` with non-empty `Input` | `user_message_chunk` | empty Input |
| `assistant.message.completed` with non-empty `Text` | `agent_message_chunk` | `ToolCalls` offers (cards come from `tool.call.*`) |
| `assistant.message.failed` / `interrupted` with non-empty `Message` | `agent_message_chunk` | codes |
| `tool.call.started` | `tool_call` `{toolCallId, title, kind, status: in_progress, rawInput?}` | — |
| `tool.call.completed` | `tool_call_update` `{status: completed, content: [text block]}` | domain `Truncated` (transcript keeps it) |
| `tool.call.failed` | `tool_call_update` `{status: failed, content: [text block of Message]}` | `Code` |
| `tool.call.interrupted` | `tool_call_update` `{status: failed}` | ACP v1 has no `interrupted` status |
| `approval.*`, `turn.*` terminals, `session.*`, `assistant.message.started`, `model.request.recorded`, `model.usage.recorded`, `policy.decision.recorded` | none | — |

`rawInput`: if `Arguments` is a JSON object or array, pass the compact JSON
value; otherwise omit. Tool `Content` is a string — use
`content: [{type:"content", content:{type:"text", text: ...}}]`. Do not
invent `rawOutput`.

## Session lifecycle (list / resume / close / delete)

| Method | Request | Success | Rejection |
| --- | --- | --- | --- |
| `session/list` | optional `cwd`, optional opaque `cursor` | `{sessions:[{sessionId,cwd,updatedAt}], nextCursor?}` | non-empty foreign `cwd` or bad cursor → `-32602` |
| `session/resume` | required `sessionId`, required `cwd`; a non-empty `mcpServers` or `additionalDirectories` is rejected, empty lists are tolerated | `{}`, no `session/update` | absent, foreign, domain-closed, running, or deleted → `-32602` |
| `session/close` | required `sessionId` | `{}` after the wire entry cancels/settles and detaches; no domain append | unattached (no idle/running wire entry for this id), or a session already closing/detached/deleting → `-32602` |
| `session/delete` | required `sessionId` | `{}` after a durable `session.deleted` append, or an idempotent no-op | a same-workspace entry that is running, closing, or deleting → `-32602`; absent, foreign, or already-deleted → `{}` with no mutation |

Every internal (non-validation) failure in these four methods is `-32603`
`session operation failed`. `updatedAt` is RFC3339Nano UTC. `session/list`
always lists the assembly workspace, even when `cwd` is omitted; it never
returns a deleted session, and it carries no title, `additionalDirectories`,
or `_meta`.

**ACP close is not the durable `session.closed` fact.** Close only cancels
work owned by this duplex and detaches the wire entry; the persistent
Session remains resumable and `application.CloseSession` is never called.
Delete is the sole new durable lifecycle fact this slice adds: it appends
`session.deleted` through the same CAS-guarded append path as every other
command, is logical (no row is physically erased — see
[session transcript](session-transcript.md)), and treats absent,
foreign-workspace, and already-deleted sessions as one indistinguishable
successful no-op so it can never become an existence oracle.

### Wire-session state machine

Each attached session on a duplex is one of five states, tracked only in
adapter memory (`internal/harness/adapters/acp/server.go`):

```text
new / load(active) / resume ─────────────────────────────────> idle
idle ── prompt ──> running ── terminal response settles ─────> idle
idle / running ── close ─> closing ── cancel + settle + release ─> detached
detached ── load / resume ───────────────────────────────────> idle
idle / detached / absent ── delete ─> deleting ── append/no-op ─> absent
```

`session/new`, an active `session/load`, and `session/resume` attach an idle
entry; a `session/load` of a durably closed Session may still replay its
history but leaves no entry, so it stays unpromptable — and it cannot be
reattached by `session/resume` either, since `ResumeSession` itself rejects
a non-active status. `session/prompt`
requires an attached idle entry and moves it to `running`; the entry returns
to `idle` **before** the terminal JSON-RPC response for the prompt is
published (so a client that immediately re-prompts on the same response
never races a stale `running` read), while the completion signal a blocked
`session/close` waits on only fires **after** that response write — so
close never reports `{}` before the cancelled prompt's own terminal frame
is on the wire. `session/prompt`, `session/resume`, and `session/load` are
all rejected while an entry is `running`, `closing`, or `deleting`, with the
one exception that `session/close` itself admits `running` (that is how a
close cancels an in-flight prompt). `session/delete` installs `deleting`
under the same mutex used for every other transition before calling
`DeleteSession`, so a prompt cannot be admitted between deletion admission
and the durable append; on any failure other than the idempotent
absent/foreign/deleted case, the entry is restored to its exact prior state.
Close and delete both hand off their slow work (waiting for a cancelled
prompt, calling the Application layer) to a goroutine after their
mutex-guarded admission check, so neither blocks frames for other sessions
on the same duplex.

### Fixed, non-leaking errors

Every lifecycle validation failure uses one of two fixed strings
(`invalid params` at `-32602`, or `session operation failed` at `-32603`);
none of them include a session ID, a workspace root, or a lifecycle state
name.

## Clip bounds

Incoming RPC frames remain `maxFrameBytes = 1 MiB`. An oversize line fails
the codec (`token too long`) and tears down `Serve`; it is not a `-32700`
frame. `-32700` is only for invalid JSON or a wrong `jsonrpc` version.
Clip outgoing text in the projector, at a UTF-8 code-point boundary, never
in Domain. After JSON encoding (including escaping of newlines and
control characters), the `session/update` NDJSON frame including its
trailing newline must be at most `maxFrameBytes`. The projector shrinks
text and tool `title` until that encoded frame fits. It never clips
`toolCallId`: identity must match across live updates, load replay, and
`session/request_permission`. If the identity fields themselves cannot
fit, the projector omits that update (load continues) rather than failing
the RPC or writing an oversize frame.

| Bound | Limit | On exceed |
| --- | --- | --- |
| Outgoing `session/update` frame | 1 MiB encoded | shrink text/title until the marshaled frame fits; omit the update if identity still cannot fit |
| Outgoing `agent_message_chunk` / `user_message_chunk` text | 768 KiB raw, then encoded-frame fit | clip; conversation continues |
| Outgoing tool `content` text | 16 KiB raw, then encoded-frame fit | clip; if the clipped prefix does not already end with `\n[truncated]`, append that marker |
| Outgoing tool `title` (and permission `title`) | shrink until encoded-frame fit | clip at a UTF-8 boundary; never append `\n[truncated]`. `kind` still uses the unclipped name |
| Outgoing `toolCallId` (and permission `toolCallId`) | must fit in the encoded frame | never clip. Omit the `session/update`. Skip the permission RPC (fail-closed deny) |
| Outgoing `rawInput` | 16 KiB compact JSON | clip encoded bytes at a UTF-8 boundary; if the result is no longer valid JSON, **omit** `rawInput`. Never append `\n[truncated]` to `rawInput`. Omit entirely if the tool-call frame still exceeds 1 MiB |

## Live fidelity gap

`engine.RuntimeEvent` is unchanged. Live tool cards carry id, name, kind,
and status only. Arguments and result text appear on `session/load` and on
the transcript. A client that never loads will not see `rawInput` or output
content for the in-flight turn.

## Never projected on ACP

Usage tokens, latency, `finishReason`, `providerRequestID`; policy rule IDs;
`model.request.recorded`; audit digests / commit positions; raw provider
SSE; domain error codes (fixed JSON-RPC messages remain); subagent origin,
plans, thoughts, terminals, diffs, ACP v2 fields; verdicts.

## Exclusions

ACP v2, terminals, slash commands, authenticate, token-aware compaction,
protocolVersion negotiation, permission waiter cleanup on cancel,
`RuntimeEvent` enrichment, and subprocess stdio as the default test gate.
`session/set_mode`, `session/set_config_option`, session fork, batch
deletion, undelete, and physical retention/garbage collection of a deleted
Session. `additionalDirectories` and session-scoped MCP configuration are
accepted only as empty on `session/load`/`session/resume` and never acted
on; no MCP client is constructed. No titles generated from prompts, search,
tags, or status metadata beyond ACP's required `SessionInfo` fields. No
multi-page historical snapshot guarantee for `session/list`: each page is
one read transaction, and a concurrent write can move a session between
pages.
