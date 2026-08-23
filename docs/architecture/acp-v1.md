# ACP v1 Adapter — Implemented Contract

**Status:** Implemented; not GA

**Authority:** [ACP v1 Adapter (Milestone 6) design](../superpowers/specs/2026-08-22-acp-v1-adapter-design.md)

**Evidence:** [ACP v1 adapter completion evidence](acp-v1-evidence.md)

**Package:** `internal/harness/adapters/acp`

## Scope

ACP v1 JSON-RPC 2.0 over newline-delimited UTF-8. The adapter translates
initialize, session/new, session/load, session/prompt, session/cancel, and
session/request_permission onto the existing Application service. It owns
no domain rules.

Composition exposes `ServeACP`. `cmd/och -acp` serves stdin/stdout and
writes diagnostics only to stderr.

## Mapping

- `protocolVersion` is `1`. `loadSession` is advertised. `authMethods` is empty.
- `session/new` creates a Session at the assembly workspace. A non-empty
  `cwd` that does not equal that workspace is `-32602`.
- `session/load` replays `turn.started` and `assistant.message.completed`
  as `session/update` before the RPC result.
- `session/prompt` runs `RunTurn`. `model.text.delta` becomes
  `agent_message_chunk`. A catalog-backed turn prefixes the model prompt
  with prior user/assistant/tool messages from the event log. Settlement
  is the committed turn: `completed` → `end_turn`; caller-canceled
  interrupt → `cancelled`; anything else → `-32603` `session prompt failed`.
- Concurrent prompts on one session are `-32600`.
- `session/cancel` cancels the in-flight prompt context; unknown IDs are ignored.
- Permission bridging is `tools.Slot`: allow-once grants, every other
  outcome including transport failure denies.

## Exclusions

ACP v2, resume, terminals, slash commands, authenticate, token-aware
compaction, and subprocess stdio as the default test gate.
