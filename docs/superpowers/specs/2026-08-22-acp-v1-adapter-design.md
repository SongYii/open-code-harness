# ACP v1 Adapter (Milestone 6)

**Status:** Accepted design

**Date:** 2026-08-22

**Parent:** [Foundational architecture](2026-08-11-open-code-harness-architecture-design.md)

**Evidence:** [ACP v1 adapter architecture gate](../../research/architecture-gates/2026-08-22-acp-v1-adapter.md)

**Implemented contracts this slice must not change:** [Domain events](../../architecture/domain-events.md),
[Engine vertical slice](../../architecture/engine-vertical-slice.md),
[EventStore v2](../../architecture/eventstore-v2.md),
[Tool runtime](../../architecture/tool-runtime.md),
[Composition root](../../architecture/composition-root.md),
[Runtime Host](../../architecture/runtime-host.md)

English is the normative specification. The Chinese file is a synchronized reading copy.

## 1. Decision summary

1. **Target ACP v1 only.** Serve `protocolVersion: 1`. v2 stays additive-by-design and is not implemented.
2. **`adapters/acp` is a transport package.** It translates JSON-RPC into existing Application commands. It does not decide policy, retry, or conversation memory. Composition is the only production importer.
3. **Approver injection is a `tools.Slot`, not a new Application port.** `Service` still takes `tools.Approver` at construction. Composition always installs a mutex-protected slot that starts as `DenyApprover`. The ACP server `Set`s itself for the life of `Serve` and `Set`s deny on teardown. Fail-closed is the default when no client is attached.
4. **Stop reasons are a total function over the implemented turn algebra.** `completed → end_turn`. `interrupted` with `caller_canceled → cancelled`. Every other terminal or error is JSON-RPC `-32603` with the fixed message `session prompt failed`. The adapter never invents `refusal`.
5. **Own a minimal NDJSON JSON-RPC codec.** No community Go SDK. Conformance is against this spec and the architecture gate, not a moving library.
6. **`session/load` projects the event log; `session/prompt` remains amnesiac.** Prior turns are visible to the client as `session/update` notifications. `RunTurn` still starts from the current user input. Conversation projection is milestone 8.
7. **Default verification is in-memory NDJSON over the real assembly.** No network, no credential, no subprocess. A `-acp` stdio path on `cmd/och` exists but is not the gating test.

## 2. Goals

1. An ACP v1 agent that speaks initialize, session/new, session/load, session/prompt, session/cancel, and session/request_permission.
2. Composition exposes `ServeACP(ctx, io.Reader, io.Writer) error` and never writes non-ACP bytes to that writer.
3. Concurrent `session/prompt` on one session is rejected locally with JSON-RPC `-32600`.
4. Permission asks are fail-closed: transport failure, client cancel, and teardown deny.
5. Keyless tests covering the methods above.

## 3. Non-goals

1. ACP v2, session/resume, terminals, elicitations, slash commands, session modes, MCP servers from `session/new`.
2. Authenticate. This process has no agent-side client credential; `authMethods` is empty and `authenticate` is not required.
3. Context Engine, retry, compaction, steering, always-allow policy rules.
4. Changing Domain turn terminals to add refusal/blocked.
5. Making `cmd/och` print banners on stdout when `-acp` is set (stdout is the protocol).

## 4. Package and ownership

`internal/harness/adapters/acp` consumes `application.Service` (as a session/turn port), `application.EventStore` (pinned reads for load), `engine.RuntimeSink`, and `tools.Approver`. It imports no other adapter.

The architecture guard gains `ownerACP`. Composition may import it; every other owner may not. ACP may not import another adapter.

## 5. Lifecycle and mapping

| ACP | Application |
| --- | --- |
| `initialize` | Negotiate version 1. Advertise `loadSession: true`. No auth methods. |
| `session/new` | `CreateSession` with the assembly workspace root. `cwd` if present must equal that root after cleaning; otherwise `-32602`. Ignore `mcpServers`. |
| `session/load` | `LoadSession`; on missing session `-32602`. Then `ReadStream` the committed log and emit `session/update` for `turn.started` (user text) and `assistant.message.completed` (agent text) before the RPC result. |
| `session/prompt` | Concatenate `prompt[]` text blocks. `RunTurn` with a generated `RunTurnRequestID` and a sink that forwards `model.text.delta` as `agent_message_chunk`. Respond when the turn commits. |
| `session/cancel` | Cancel the in-flight prompt context. Unknown session IDs are ignored. |
| `session/request_permission` | Reverse RPC from `tools.Approver.Decide`. Options: `allow-once` / `reject-once`. Selected allow grants; anything else, including cancel and RPC failure, denies. |

Methods other than `initialize` require a completed initialize. Prompt handling is asynchronous so `session/cancel` can be read while `RunTurn` is in flight.

## 6. Errors

| Condition | Wire |
| --- | --- |
| Parse / non-object line | `-32700` |
| Unknown method | `-32601` |
| Bad params, unknown session on requests, cwd mismatch | `-32602` |
| Prompt already in flight | `-32600` (`a prompt is already in flight for this session`) |
| Turn failed / interrupted for a reason other than caller cancel / internal leak | `-32603` (`session prompt failed`) |
| Notification send failure | log nowhere on the protocol writer; do not fail the in-flight prompt unless it is the prompt response itself |

Raw engine and store messages never appear on the wire.

## 7. Verification

1. Codec tests: NDJSON framing, no embedded newlines, initialize handshake.
2. Adapter tests with a scripted Application port: new/load/prompt/cancel, busy reject, permission grant/deny/cancel, stop-reason table.
3. Composition test: `ServeACP` over `io.Pipe` against the real assembly and a loopback provider fixture, one completed turn, durable stream replayed.

## 8. Key decisions

| Decision | Rationale |
| --- | --- |
| `tools.Slot` instead of a new port | Existing `Approver` already models the ask. A slot is an adapter/composition object. Service stays frozen. |
| No `refusal` stop reason | Domain has no such terminal. Inventing one in the adapter violates F9. |
| Own codec | Framing contract is small; community SDKs were not audited. |
| Load projects log, prompt does not remember | EventStore already has history; `RunTurn` does not. Honest about milestone 8. |
| Prompt in a goroutine | A blocking prompt would prevent reading `session/cancel` on the single NDJSON reader. |

## 9. PR Plan

Single implementation PR on this slice: spec + adapter + composition wiring + guard + tests + implemented contract. Further PRs only if the focused spec is later extended (auth, resume, v2).
