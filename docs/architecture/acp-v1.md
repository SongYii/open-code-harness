# ACP v1 Adapter — Implemented Contract

**Status:** Implemented; not GA

**Authority:** [ACP v1 Adapter (Milestone 6) design](../superpowers/specs/2026-08-22-acp-v1-adapter-design.md)

**Evidence:** [ACP v1 adapter completion evidence](acp-v1-evidence.md); Slices A/A′ mapping in [conversation and session transcript evidence](conversation-and-transcript-evidence.md)

**Package:** `internal/harness/adapters/acp`

## Scope

ACP v1 JSON-RPC 2.0 over newline-delimited UTF-8. The adapter translates
initialize, session/new, session/load, session/prompt, session/cancel, and
session/request_permission onto the existing Application service. Mapping
lives in adapter-owned pure functions (`ProjectRuntimeEvent`,
`ProjectRecordedEvent`). The adapter owns no domain rules.

Composition exposes `ServeACP`. `cmd/och -acp` serves stdin/stdout and
writes diagnostics only to stderr.

Conversation (user / assistant / tool cards) is this adapter. Trajectory
(usage, step identity, truncation flags, wall-clock) is
[session transcript](session-transcript.md). The two surfaces do not share a
codec and must not import each other.

## Initialize and session RPCs

- `protocolVersion` is `1`. `loadSession` is advertised. `authMethods` is empty.
  The adapter does not negotiate the client's version.
- `session/new` creates a Session at the assembly workspace. A non-empty
  `cwd` that does not equal that workspace is `-32602`.
- `session/load` and `session/prompt` admit the RPC only when
  `filepath.Clean(loaded.WorkspaceRoot)` equals the assembly workspace.
  Mismatch or unknown session is `-32602` `invalid params`, with no
  `session/update` and no `RunTurn`. The wire message does not distinguish
  missing from foreign and does not leak the foreign path.
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
| `tool.execution.failed` | `tool_call_update` `{status: failed}` | Never skip. `Code` stays off the wire |
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

## Clip bounds

Incoming RPC frames remain `maxFrameBytes = 1 MiB`. An oversize line fails
the codec (`token too long`) and tears down `Serve`; it is not a `-32700`
frame. `-32700` is only for invalid JSON or a wrong `jsonrpc` version.
Clip outgoing text in the projector, at a UTF-8 code-point boundary, never
in Domain.

| Bound | Limit | On exceed |
| --- | --- | --- |
| Outgoing `agent_message_chunk` / `user_message_chunk` text | 768 KiB | clip; conversation continues |
| Outgoing tool `content` text | 16 KiB | clip; if the clipped prefix does not already end with `\n[truncated]`, append that marker |
| Outgoing `rawInput` | 16 KiB compact JSON | clip encoded bytes at a UTF-8 boundary; if the result is no longer valid JSON, **omit** `rawInput`. Never append `\n[truncated]` to `rawInput` |

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

ACP v2, resume / list / delete, terminals, slash commands, authenticate,
token-aware compaction, protocolVersion negotiation, permission waiter
cleanup on cancel, `RuntimeEvent` enrichment, and subprocess stdio as the
default test gate.
