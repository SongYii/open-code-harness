# ACP v1 Adapter Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-22

**Scope:** Milestone 6 (ACP v1 adapter and conformance) primary-source
verification. Records the then-public protocol surface of the Agent Client
Protocol and the agent-side adapter topologies of the required comparison
set; fixes which protocol version the first adapter targets, where a
transport adapter lives inside this repository's strictly layered Go module,
how ACP sessions map onto the implemented Session/Turn state machine and
EventStore replay, how the Policy Decide table bridges to
`session/request_permission`, and how an ACP agent is verified keylessly.

This document is research evidence. It does not change any implemented
contract and does not authorize copying reference-project types, package
layouts, schemas, or runtime.

English is the normative research record. The Chinese file is a synchronized
reading copy.

## Questions

1. What does ACP require of an agent side, and what is the minimal correct
   baseline surface for milestone 6?
2. Which protocol version should the first adapter target — v1 or the newer
   draft v2?
3. How do verified implementations map ACP sessions, prompts, streaming
   updates, and turn settlement onto an internal session/turn model backed by
   durable events, and which mapping fits this repository's Session/Turn
   state machine, EventStore pinned-read replay, and Runtime Host?
4. Where does a transport adapter live in this repository's layering, who
   imports it, and what does that do to the dependency guard?
5. How do verified implementations bridge tool-permission decisions to
   `session/request_permission`, and what happens when the client fails,
   cancels, or never answers?
6. How do implementations verify an ACP agent without network access or
   credentials?

## Verified primary sources

All observed from official repositories on 2026-08-22 by resolving each
default branch to a commit and reading that commit's tree and files through
`scripts/fetch-reference.sh`. Commits are the observed state, not
endorsements.

| Source | Observed state | ACP entry points |
| --- | --- | --- |
| [agentclientprotocol/agent-client-protocol](https://github.com/agentclientprotocol/agent-client-protocol) | `83dad56`, Rust schema crate + MDX docs, 2026-08-22 | `docs/protocol/v1/*.mdx`, `docs/protocol/v2/*.mdx` (draft), `schema/`, official Rust and TypeScript libraries; community registry at `docs/libraries/community.mdx` |
| [zed-industries/codex-acp](https://github.com/zed-industries/codex-acp) | `296069e`, Rust, 2026-08-22 | `src/codex_agent.rs`, `src/thread.rs`, `src/lib.rs`; README banner states development moved to `agentclientprotocol/codex-acp` |
| [MoonshotAI/kimi-code](https://github.com/MoonshotAI/kimi-code) | `d4e0ad4`, TypeScript, 2026-08-22 | `packages/acp-server/` (engine v2 via klient facade), `packages/acp-adapter/` (engine v1 via SDK facade), `test/e2e-turn.test.ts` |
| [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness) | `b150a55`, TypeScript/Cordis, 2026-08-22 | `packages/acp/acp/src/index.ts`, `examples/acp-agent/cordis.yml`, `packages/acp/acp/tests/harness.ts` |

Two naming facts matter for later citation. The specification repository has
moved from `zed-industries/agent-client-protocol` to the
`agentclientprotocol` organization (HTTP 301 on the old API path); earlier
gates citing the old location should be read against the new one. And
`codex-acp`'s own README marks it as the legacy Zed adapter, with active
development continuing in the new organization on top of Codex's App Server;
this gate reads it as the most complete published example of an ACP-to-core
translation layer, not as the ecosystem's current state.

Community Go libraries exist — `coder/acp-go-sdk`, `ironpark/acp-go`,
`eino-contrib/acp`, `spachava753/acp-sdk`, listed by the specification's own
registry. None was fetched or verified for this gate.

## Ecosystem convergence

Every verified implementation, across two languages and three core
architectures, converges on the same properties.

1. **Everyone serves the v1 wire today; v2 exists but is gated by its own
   authors.** The v2 migration guide describes v2 as "a consolidation
   release" whose "protocol surface as a whole is still labeled draft," and
   instructs implementers to negotiate the version per connection while
   keeping v1 working: "an Agent that drops v1 cuts itself off from existing
   Clients." Kimi Code's packages pin `@agentclientprotocol/sdk ^0.23.0`
   (v1-era) and `^1.3.0`; codex-acp pins `agent-client-protocol =0.14.0`.
2. **The adapter is a translation layer between two event systems, with no
   business logic of its own.** codex-acp translates between ACP requests
   and codex-core's `Op` submissions / `Event` streams; Kimi's acp-server
   translates between ACP methods and engine events (`assistant.delta`,
   `tool.call.*`, `turn.ended`) through pure mapper modules (`convert.ts`,
   `events-map.ts`); DeepSeek's plugin translates between ACP and committed
   session-log events only.
3. **Turn settlement comes from an internal end-of-turn signal, not from a
   timeout heuristic.** Kimi settles exclusively on `turn.ended`
   (`settlement ... relies solely on turn.ended`); codex-acp resolves the
   prompt on `TurnComplete`/`TurnAborted`. DeepSeek is the outlier,
   resolving after quiescence gates (`admissionDone` + `agent.whenIdle()` +
   ordered output drain).
4. **stdout discipline is enforced structurally.** codex-acp compiles with
   `#![deny(clippy::print_stdout, clippy::print_stderr)]`; DeepSeek's ACP
   composition forbids stdout loggers because "stdout carries ACP JSON-RPC";
   Kimi redirects `console.*` to stderr before anything else runs.
5. **Verification drives real JSON-RPC framing over in-memory duplex
   streams against the real assembled engine, with only the model faked.**
   No verified project makes a stdio subprocess part of its default test
   gate; spawning the real binary is a separate snapshot lane (DeepSeek).

## Observed contracts and boundary

### C1. Transport framing is small and strict

ACP v1 is JSON-RPC 2.0 over newline-delimited UTF-8 messages with no
embedded newlines. The agent MUST NOT write anything non-ACF to stdout
(`transports.mdx`: "MUST NOT write anything to its `stdout` that is not a
valid ACP message"), MAY log to stderr, and SHOULD support stdio. The
specification also permits custom transports that preserve message format —
which is exactly the seam every verified project uses for testing.

### C2. The lifecycle is initialize → authenticate? → session/new | load | resume → prompt turns

`initialize` negotiates `protocolVersion` (v1 baseline: `"protocolVersion":
1`) and exchanges capability objects. `session/new` takes a cwd and MCP
server list and returns a sessionId. `session/load` restores an existing
session and replays history as `session/update` notifications before the
response; `session/resume` restores without replay. All four verified
implementations advertise load support, and all replay from their durable
log: codex-acp re-emits rollout items through `replay_history`;
Kimi's acp-server projects stored context history through
`projectHistoryToSessionUpdates` ("the ONE differentiator" between load and
resume); DeepSeek's sessions are durable-inbox-backed so history is already
committed log content.

### C3. One prompt request settles exactly once, with a stop reason

A `session/prompt` request ends when the agent responds with a `StopReason`
(`end_turn`, `cancelled`, `refusal`, …). Everything else during the turn —
message chunks, thoughts, tool calls, plans, usage — arrives as
`session/update` notifications. Concurrent prompts on one session are
rejected deterministically: Kimi proactively rejects with `-32600` via
`assertNoActiveTurn()` because "the engine would otherwise silently queue";
DeepSeek returns `invalidParams("a prompt is already in flight for this
session")` from a single per-session slot reserved synchronously; codex-acp
serializes per-session work through a single actor mailbox so overlap cannot
arise.

### C4. Permission bridging is fail-closed everywhere

Tool approval crosses the wire as the reverse-RPC method
`session/request_permission`, carrying typed options
(`allow_once`/`allow_always`/`reject_once`/`reject_always`) plus the pending
tool call for display; when the turn is cancelled the client MUST answer
with outcome `"cancelled"`. Every implementation defaults to rejection:

- Kimi: any RPC failure maps to `{decision:'rejected'}`; `approve_always`
  becomes a session-scoped allow rule in the engine.
- codex-acp: cancel/unselected outcomes default to `ReviewDecision::Abort`;
  unsupported elicitations are auto-declined.
- DeepSeek: the approval seam composes answerers over a fail-closed
  waterfall — under policy `'never'` every ask is rejected without
  prompting; an unanswered `'ask'` chain falls through to `'unavailable'`;
  and the ACP answerer "never infers a durable grant from an unknown client
  response," contributing strictly one-shot choices.

### C5. Error hygiene keeps internals off the wire

Kimi maps auth failures (on both prompt-launch rejection and
turn-failure paths) to `auth_required` so clients can drive re-auth, busy to
`-32600`, and everything else to a fixed `-32603 "session prompt failed"`
whose e2e explicitly asserts raw engine messages never leak. Unknown session
ids error only on requests (`invalid_params`) and are swallowed on
notifications (`session/cancel`). codex-acp uses typed error constructors
and logs notification send failures instead of failing. Disposal settles
in-flight prompts as cancelled so no client request hangs after teardown.

### C6. Cancellation has three windows, and all are handled

Pre-turn (before any model call), launch race (cancel arrives before the
turn id is known — Kimi buffers early turn-scoped events, sets
`cancelRequested`, issues an unaddressed cancel, and re-issues it addressed
once the id lands), and active turn (`session/cancel` /
`$/cancel_request` routed into the same internal cancellation path). The
prompt response then carries `stopReason: cancelled`.

### C7. Keyless verification = real framing + real engine + scripted model

Kimi's flagship `e2e-turn.test.ts` boots "the FULL agent-core-v2 engine and
the real ACP wire (ND-JSON over an in-memory stream) ... only fakes the
network LLM call": a scripted provider is injected by shadowing the DI seed,
the server runs behind `runAcpServerWithStream` over cross-wired
PassThrough pairs, and a raw NDJSON test client asserts exact notification
sequences, permission bridging, `-32600` concurrency rejection, error
hygiene, and cancellation across all three windows. DeepSeek's
`tests/harness.ts` states the same pattern in one line: "In-memory ACP
transport fixture over the real agent factory and loop." Snapshot suites
that spawn the built binary against recorded transcripts exist as a separate
keyless lane (record mode uses the live API).

## Rejected shapes

### R1. Coupling the adapter to another project's core

codex-acp pins thirteen-plus `codex-rs` crates by tag (`rust-v0.137.0`) and
vendors one outright. That coupling is correct for Zed — it does not own the
Codex core — and irrelevant here: this repository owns its core, and the
adapter must sit on its own ports. Nothing about the vendoring shape is
adopted.

### R2. Targeting v2 first

The migration guide itself labels v2 draft and instructs side-by-side
delivery behind negotiation. Milestone 6 targets v1 only; the design must
not foreclose adding v2 negotiation later, but nothing in this slice may
depend on v2 surface.

### R3. Adopting an unverified community Go SDK wholesale

Four Go libraries appear in the specification's registry; none was audited
at a commit for this gate, and the framing contract (C1) is small enough to
own. Whether the slice verifies-and-pins one library or owns a minimal codec
behind a port is a decision for the focused specification, informed by the
dependency rules: either way the choice must be pinned, and conformance must
be checked against the specification's own schema rather than trusted to a
moving library.

### R4. Quiescence-based settlement

DeepSeek resolves a prompt only after whole-agent idleness plus ordered
output drain. That folds steering and autonomous work into one response —
an interesting property, recorded here — but the settlement boundary becomes
a liveness heuristic rather than a durable fact. This repository's turns end
when a `turn-ended` event commits; settlement adopts the Kimi/codex rule:
resolve exactly once, on the event.

### R5. Business logic in the adapter

Mode catalogs, slash-command semantics, and permission presets living in
adapter code (observed in codex-acp's preset mapping and Kimi's slash-intent
handling) belong, in this repository, to the Application-owned Step loop and
Policy Decide table. The adapter translates; it does not decide.

## Findings

### F1. Milestone 6 targets ACP v1

Per C2 and R2: serve `protocolVersion: 1`, negotiate down honestly, and
structure the codec so v2 negotiation is additive later. The minimal correct
baseline is: initialize (+ authenticate if credentials exist),
session/new, session/load with replay, session/prompt with update
streaming, session/cancel, and `session/request_permission` bridging.
Session modes, config options, terminals, elicitations, and slash-command
advertisement are optional v1 surface and out of scope for the first slice.

### F2. The adapter is a new transport package owned like every other adapter

An `adapters/acp` package consumes Application/Runtime ports only, imports
no other adapter, and is imported by exactly one production package: the
composition root. The Slice 5 dependency guard already enforces precisely
this shape — one named owner, absence means forbidden — so the guard needs
no relaxation; the spec must confirm the new package lands inside the
existing exception rather than creating a second one.

### F3. Session maps to our Session identity; load is replay; settlement is the turn-ended event

Our EventStore v2 already provides identity ownership, digest-chained
appends, pagination, and pinned reads — the exact substrate C7-style replay
needs. The mapping: ACP `sessionId` ↔ durable Session identity;
`session/load` replays committed history through pinned reads projected into
`session/update` notifications before responding; `session/prompt` submits
the existing Application command; the request resolves exactly once when the
turn-ended event commits, mapped to `end_turn` / `cancelled` / refusal-class
stop reasons per F5. DeepSeek-style quiescence is rejected (R4).

### F4. Concurrency rejection is deterministic and local

Because the Engine's step loop owns one turn per session, a second
concurrent `session/prompt` is rejected synchronously with an invalid-request
error before touching the engine — the Kimi `assertNoActiveTurn` shape, not
a queue.

### F5. Stop reasons are constrained by the implemented result algebra, which has a refusal gap

The implemented turn terminals are exactly `completed`, `failed`, and
`interrupted` (`internal/harness/domain/state.go:14-19`); cancellation
exists only as the `caller_canceled` interruption code. There is no
refusal- or policy-blocked turn terminal: a denied approval or policy reject
today resolves through failure/interruption paths, not through a distinct
terminal state. The v1 mapping is therefore: `completed → end_turn`,
`interrupted(caller_canceled) → cancelled`; everything else stays on the
JSON-RPC error channel as a fixed message — the adapter never invents a
`refusal` stop reason. Exposing a genuine refusal-class stop reason requires
a domain contract change (a new terminal or code), which per F9 belongs to
the focused specification as an explicit decision, not to the adapter.

### F6. Permission bridging is a thin, fail-closed projection of Policy Decide — and the injection path is an open design point

Policy Decide `ask` outcomes become one `session/request_permission` per ask
with at minimum `allow_once`/`reject_once`; transport failure, client
cancellation, and teardown all default to reject (C4). Always-allow scope
mapping waits until the Policy contract gains a scoped rule — reported as a
candidate, not changed inside this slice.

Bridging also has an unsolved wiring problem this gate verifies but does not
design: `composition.Config` declares `Approver tools.Approver`
(`internal/harness/composition/config.go:41`), the assembly never propagates
it into the application configuration (only `Commands` is wired,
`internal/harness/composition/assembly.go:130`), so every assembly falls
back to `DenyApprover`; and `application.Service` freezes its dependencies
at construction (`internal/harness/application/service.go:74`), so an ACP
per-request approver cannot be swapped in afterwards. Making
`session/request_permission` work therefore requires either assembly-level
propagation of the approver plus a dynamic approver seam (provider or
replacement notification), or a new port — a contract decision that belongs
to the focused specification.

### F7. Verification extends the Slice 5 assembly test, keylessly

The e2e gate speaks real newline-delimited JSON-RPC over an in-memory duplex
pair around the full composition root — SQLite store, workspace tools,
Runtime Host, scripted provider over `httptest` fixtures — and asserts the
C2/C3/C4/C5/C6 behaviors above. A real-binary smoke lane (build the binary,
spawn it, drive one turn) is a separate non-gating check. No network, no
credentials, no subprocess in the default gate path (R6 retained).

### F8. Adopt summary

1. Target ACP v1 only; keep v2 additive-by-design.
2. New `adapters/acp` transport package behind existing ports; imported only
   by the composition root; dependency guard unchanged.
3. Minimal baseline: initialize, authenticate-if-needed, session/new,
   session/load with pinned-read replay, prompt with streamed updates,
   cancel, permission bridging.
4. Settlement exactly once, on the committed turn-ended event; concurrent
   prompts rejected locally.
5. Fail-closed permission projection of Policy Decide; the approver
   injection path (assembly propagation + dynamic seam or new port) is an
   explicit decision for the focused specification.
6. Fixed stop-reason and error mappings with no internal leakage.
7. Keyless e2e over in-memory NDJSON duplex around the real assembly;
   optional real-binary smoke lane.

### F9. Reject summary

1. No v2 surface, no quiescence settlement, no business logic in the
   adapter.
2. No wholesale adoption of an unverified community SDK; any dependency is
   pinned and conformance-checked against the spec schema.
3. No new ports or contract changes: the adapter exposes what Slices 1–5
   already implement. If exposure requires a contract change, that is a
   finding for the focused specification, not a silent edit.
4. No live model calls or spawned processes in the default verification
   path.

## Evidence limits

- Repository trees were read at the commits listed above; behavior was
  inferred from source and tests, not from executing any reference project.
- The specification was read as documentation plus schema layout at
  `83dad56`; the generated JSON schema was not diffed against the official
  libraries.
- Community Go SDKs are cited only as registry entries; none was fetched,
  audited, or licensed-reviewed.
- ACP v2 was read only through its migration page; the v2 draft surface was
  not audited.
- codex-acp is the legacy Zed adapter; the successor repository was not
  examined.
- No claim is made about any project's private or unreleased implementation.
