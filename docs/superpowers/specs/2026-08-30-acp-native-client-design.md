# Minimal ACP-Native Client — Design

- **Date:** 2026-08-30
- **Status:** Accepted 2026-08-30. The human reviewer raised whether this
  slice's scoping choices (declining capabilities, a narrowed
  `sessionUpdate` variant set, hand-rolled framing) risk deviating from
  ACP as a de facto industry standard. None of them do: capability
  decline and graceful handling of an unrecognized variant are the
  protocol's own extensibility mechanism, not departures from it, and
  hand-rolled framing is an implementation-strategy choice orthogonal to
  wire compatibility. The one real open question the review surfaced is
  genuine and already this design's stated primary goal, not a new gap:
  the existing agent-side adapter has only ever been driven by this
  repository's own scripted fixtures, never a real independent client, so
  whether it is actually spec-compliant in practice remains unverified
  until §7's real integration test exists — accepted as this slice's
  acceptance criterion, not deferred as an unstated risk.
- **Stability:** new surface; does not change any existing `experimental`/pre-GA contract
- **Repository:** `open-code-harness` (`github.com/SongYii/open-code-harness`)
- **Normative language:** English
- **Chinese summary:** [最小 ACP 原生客户端设计](2026-08-30-acp-native-client-design.zh-CN.md)
- **Authority:** [Client surface and security sequencing decision](../../research/architecture-gates/2026-08-30-client-surface-and-security-sequencing.md) (step 2 of its sequencing); [ACP-native client architecture gate](../../research/architecture-gates/2026-08-30-acp-native-client.md)
- **Implemented contracts this slice must not change:** [ACP v1 adapter](../../architecture/acp-v1.md), [Tool runtime](../../architecture/tool-runtime.md), [Composition root](../../architecture/composition-root.md)

English is normative. The Chinese file is a synchronized summary, not a
field-for-field translation.

---

## 1. Decision summary

This project's ACP v1 adapter (`internal/harness/adapters/acp`) implements the
**agent** side of the Agent Client Protocol: it has never been driven by
anything but this repository's own scripted test fixtures and, informally, by
an operator typing raw JSON-RPC lines by hand. There is no **client** —
the process that spawns an agent over stdio, sends `session/prompt`, and turns
the resulting stream of `session/update` notifications into something a human
can read — anywhere in this repository.

This design adds one: a small, standalone Go program that speaks ACP v1
against this project's own agent (`och -acp`, or any other ACP v1 agent,
since nothing in the wire protocol is project-specific), renders a live
trajectory of one prompt in plain terminal output, and answers
`session/request_permission` interactively. It is deliberately narrow —
the "minimal" client the sequencing decision's step 2 asks for, not the
fuller `TypeScript TUI client` milestone 7 already names as separate, later
work (§2).

Four architecture decisions, each directly resolving an open question the
gate left for this design (cross-referenced in §9):

1. **Own the wire framing, do not depend on an external SDK.** The 2026-08-22
   gate already decided this for the agent side ("the framing contract is
   small enough to own"); this design makes the same call for the client
   side, for the same reason, and to keep this project's total non-test
   dependency count unchanged (`SECURITY.md`: `modernc.org/sqlite`, alone).
2. **Render only from live `session/update`; reuse `session/load` for
   resume; do not add a second history path that parses `och
   export-session`'s JSONL.** That JSONL projection and this live client
   solve different problems (durable audit/export vs. an interactive
   session) and this project's own agent already replays history through
   `session/load` — a client-side JSONL reader would be a second,
   redundant way to reconstruct the same history the agent already owns.
3. **Decline the `fs` and `terminal` client capabilities entirely at
   `initialize`.** This project's own agent already confines workspace
   filesystem access and `exec` as its own jailed tools (`tool-runtime.md`)
   and — verified directly in this repository's own
   `internal/harness/adapters/acp/protocol.go` `initializeParams` — does
   not currently parse or act on any client-capabilities the client sends,
   so there is neither a compatibility cost nor a present use for
   implementing either capability.
4. **Plain, line-oriented terminal output for this slice; no TUI framework
   dependency yet.** Matches `acp-go-sdk`'s own example client's shape,
   defers the "how much UI investment" question the sequencing decision
   itself defers to "after this client exists" (step 3), and keeps this
   slice reviewable as a protocol-correctness artifact rather than a UI
   artifact.

## 2. Goals and non-goals

### Goals

- Prove the ACP v1 adapter interoperates with a real, independent client
  process — not only this repository's own scripted fixtures — closing the
  last unverified seam in the agent-side implemented contract.
- Give an operator a working, interactive way to drive a session against
  this project's agent from a terminal: connect, prompt, watch the
  trajectory render live, answer permission requests, cancel, exit.
- Establish the client-side wire and rendering architecture (transport,
  reducer, permission loop) other future clients — including milestone 7's
  fuller TUI, should it build on this one rather than starting over — can
  extend rather than replace.

### Non-goals (excluded from this slice, not deferred without a reason)

- **Not milestone 7.** `docs/README.md` already names milestone 7 as a
  separate, `TypeScript TUI client`, "cross-cutting boundary accepted;
  focused implementation specification not written yet." This slice does
  not write that specification or commit to TypeScript; it delivers a
  smaller, Go-native artifact that milestone 7 may treat as evidence of
  what a fuller client needs, or may ignore entirely.
- **Not a general ACP client.** This client is built and tested against
  this project's own agent's actual behavior (§4's variant set, §6's exact
  permission shape) observed directly from `internal/harness/adapters/acp`
  source, not re-derived from the specification in the abstract. It does
  not claim compatibility with every agent in the wild, though nothing in
  it is intentionally agent-specific either.
- **No wire-level debug log** (a raw request/response/notification dump,
  as Zed's separate `acp_tools` view provides) in this slice. This
  project's own transcript/audit surfaces already give an operator
  visibility this client's own trajectory rendering does not need to
  duplicate; a debug log is easy to add later without disturbing this
  design and is not required to meet this slice's goals.
- **No `fs` or `terminal` client-capability implementation** (§1.3): a
  deliberate scope cut, not an oversight, re-evaluated only if a future
  agent this client needs to support actually requires either.
- **No multi-session, multi-agent, or split-pane UI.** One client process
  drives exactly one session against exactly one agent process for its
  whole lifetime.
- **No `och export-session` JSONL consumption** (§1.2): that tool remains
  a separate, already-implemented offline/audit surface, unchanged and
  untouched by this slice.
- **No new non-test module dependency.** Confirmed against
  `SECURITY.md`'s dependency statement, which this slice does not change.

## 3. Package and process shape

New top-level tree, not under `internal/harness/`:

```
internal/client/acp/     # wire client: transport, session state, reducer
cmd/acp-client/          # the binary: flag parsing, terminal I/O, main loop
```

`internal/harness/adapters/acp` is reserved for the **agent** side and is
subject to `internal/harness/architecture`'s adapter-import rules (only
composition and runtime may name an adapter). This client is not a harness
adapter — it does not implement a `tools.*` port, and nothing in
`internal/harness` may import it — so it belongs outside that tree
entirely, the same way `cmd/och` itself sits outside `internal/harness` as
a consumer of the composition root rather than a part of it. Placing it
under `internal/client/` rather than directly in `cmd/acp-client/` keeps
the wire/session logic unit-testable without a subprocess, mirroring how
`internal/harness/adapters/acp`'s own logic is tested without a real
stdin/stdout pair.

`internal/client/acp` does not import `internal/harness/adapters/acp` (per
§1.1, it owns its own minimal framing) and does not import anything under
`internal/harness/` at all — it is a pure ACP client, decoupled from this
project's own agent implementation at the Go package level even though it
is tested against it as a process. `internal/harness/architecture`'s own
existing dependency-boundary tests are unaffected by this slice: nothing
under `internal/harness/` gains a new import, and nothing new sits inside
it.

### Framing

`internal/client/acp` implements exactly the wire shape the ACP v1
specification and this project's own agent already use: NDJSON-framed
JSON-RPC 2.0 over the agent subprocess's stdin/stdout, one JSON value per
line. This is a few hundred lines at most (request/response/notification
envelope types, an `encoding/json.Decoder` reading line-delimited values,
a response-waiter keyed by request id for the client's own outbound calls,
a dispatcher for inbound notifications and the one inbound call this
client answers, `session/request_permission`) — small enough that owning
a second copy of it, rather than extracting a shared package the frozen,
evidence-backed `adapters/acp` would need to be refactored to depend on,
is the lower-risk choice for both sides.

## 4. Process lifecycle and wire sequence

```
client                                    agent (subprocess, stdio)
  |--- spawn: exec.Command(agentPath, agentArgs...) ------------------->|
  |--- initialize -------------------------------------------------->  |
  |<-- agentCapabilities{loadSession:true, sessionCapabilities:{...}} -|
  |--- session/new{cwd} OR session/load{sessionId,cwd} -------------->|
  |<-- (session/load only) replayed session/update* ------------------|
  |<-- {} ------------------------------------------------------------|
  loop:
    |--- session/prompt{sessionId, prompt} --------------------------->|
    |<== session/update* (agent_message_chunk, tool_call, ...) ========|
    |<== session/request_permission (0 or more times) =================|
    |--- {outcome:{outcome:"selected", optionId}} -------------------->|
    |<-- {stopReason} ---------------------------------------------->  |
  |--- (operator exits) session/cancel, then close stdin -------------->|
```

- **Spawn**: the agent path and argv are operator-supplied flags (this
  client does not hardcode `och -acp`, matching §2's "not project-specific
  by construction" goal even though it is tested against exactly that).
  The agent's stderr is inherited (passed through to the client's own
  stderr) rather than captured, matching this project's own `och -acp`
  precedent of using stderr for diagnostics (`cmd/och/main.go`).
- **`initialize`**: this client sends `protocolVersion: 1` and an empty (or
  absent) `clientCapabilities.fs` / `clientCapabilities.terminal` — an
  explicit decline, not an omission left to default (§1.3). It reads
  `agentCapabilities` back and stores `loadSession` for §4's resume path,
  but does not otherwise branch on capabilities this slice does not use.
- **New vs. resume**: an operator-supplied flag selects `session/new{cwd}`
  (fresh session) or `session/load{sessionId, cwd}` (resume, replaying
  history as `session/update` notifications the same reducer in §5
  consumes — no special-cased "replay mode"). `session/list` is not
  wired into this slice's flag surface (an operator supplies a known
  session id directly); nothing prevents adding a `--list` flag later
  without touching this design's architecture.
- **Prompt loop**: the client reads one line of operator input at a time
  from its own stdin (not the agent's), sends it as `session/prompt`, and
  blocks rendering `session/update` notifications and answering
  `session/request_permission` calls until the prompt's terminal response
  arrives, then prompts for the next line. `session/cancel` is sent on
  `SIGINT` during an in-flight prompt (mirroring the agent's own existing
  cancellation semantics, `acp-v1.md`); a second `SIGINT` with no prompt
  in flight exits the client, sending no further requests before closing
  the subprocess's stdin.

## 5. Trajectory rendering

A reducer, not an ad hoc per-notification print — the pattern all three
gate sources converged on independently (gate §"Cross-cutting synthesis"):
parse `session/update` once, `switch` on its `sessionUpdate` discriminant,
fold the typed result into session state keyed by `toolCallId` where one is
present, then render from that state rather than from the raw notification.

```go
type toolCall struct {
    id     string
    title  string
    kind   string
    status string // "pending" | "in_progress" | "completed" | "failed"
}

type trajectory struct {
    mu    sync.Mutex
    calls map[string]*toolCall // keyed by toolCallId; insertion order tracked separately for rendering
    order []string
}

func (t *trajectory) apply(update sessionUpdate) render // render is a small value describing what changed, for the terminal writer to print incrementally
```

Scoped to exactly the four `sessionUpdate` variants this project's own
agent emits today, verified directly against
`internal/harness/adapters/acp/project.go` rather than the full ACP
variant set the specification allows: `user_message_chunk`,
`agent_message_chunk`, `tool_call`, `tool_call_update`. An unrecognized
variant (from a future version of this agent, or a different agent
entirely) is rendered as a raw, labeled fallback line rather than
crashing the client or being silently dropped — forward-compatible
without pretending to understand a shape it does not.

Rendering is incremental and line-oriented (§1.4): a new `tool_call`
prints one line naming the call; a `tool_call_update` reprints its status
in place only if the terminal is a TTY (checked once at startup,
`term.IsTerminal` equivalent via `golang.org/x/term`, already an indirect
dependency of nothing in this module today — if this single check is not
worth even that, a plain re-print-as-a-new-line fallback is the non-TTY
and no-dependency path); `agent_message_chunk` and `user_message_chunk`
text is streamed to stdout as it arrives, matching how a human reads a
live conversation.

## 6. Permission requests

`session/request_permission` is a call the agent makes to the client, not
a notification; this client answers it before the prompt loop can
proceed, since this project's own agent blocks the tool's execution on
that answer (`acp-v1.md`, `Decide` as reverse RPC).

This project's own agent's shape, read directly from
`internal/harness/adapters/acp/{protocol,server}.go` rather than assumed
from the specification: exactly two options every time, `optionId`
`"allow-once"` / `"reject-once"`, alongside a `toolCall` naming the call's
id, title, kind, and `"pending"` status. The client prints the tool call's
title and kind, prompts the operator for `y`/`n` on its own stdin (the
same stdin the prompt loop otherwise reads free-text prompts from — the
two never overlap, since the client only reads a permission answer while
one is actually pending), and answers
`{outcome:{outcome:"selected", optionId:<chosen>}}`. An agent that ever
offers a different option set is handled by rendering the option list
generically (name plus a number to choose) rather than by hardcoding the
two-option case as the only shape this client can answer — the two-option
behavior above is what an operator sees against this project's own agent
today, not a hardcoded protocol assumption.

An operator who closes stdin (EOF) while a permission request is pending
answers it as `reject-once` (or the generic option list's declared
"reject"-kind option, if present) — a fail-closed default, not a hang.

## 7. Verification and acceptance

- Unit tests for the reducer (§5): each of the four variants applied in
  isolation and in realistic sequences (a `tool_call` followed by two
  `tool_call_update`s reaching `"completed"`; an unrecognized variant
  rendered as a labeled fallback, not dropped or panicking).
- Unit tests for the wire client (§3-4) against an in-process fake agent
  (a second NDJSON pair driven by the test, not a real subprocess) proving
  the exact sequences in §4's diagram, including the resume path replaying
  `session/update` before `session/load`'s own response.
- Unit tests for the permission loop (§6) against the exact two-option
  shape this project's agent sends, plus the generic N-option fallback,
  plus the EOF-while-pending fail-closed case.
- One real, gated integration test: spawn the actual `och -acp` binary
  (built from this repository) as the subprocess, run one full
  new-session prompt cycle including a real `write_file` call needing
  permission (this project's own default policy mode), and assert the
  turn completes — the acceptance proof that this slice's stated primary
  goal ("prove the ACP v1 adapter interoperates with a real, independent
  client") is actually met, not merely asserted.
- `go vet`, `gofmt`, `CGO_ENABLED=0 go build ./...`,
  `go test -race -count=1 ./...`, and confirmation that
  `internal/harness/architecture`'s existing tests report no new import
  into or out of `internal/harness/` from this slice (§3).

## 8. Risks

| Risk | Mitigation |
| --- | --- |
| A future ACP protocol or agent-side change (new `sessionUpdate` variant, a different permission-option shape) silently breaks rendering. | §5's unrecognized-variant fallback and §6's generic option-list fallback are the mitigation, not a hardcoded assumption that today's shapes are permanent. |
| Terminal rendering assumptions (in-place status reprint) misbehave on a non-TTY (piped output, CI). | §5's TTY check before choosing incremental-reprint vs. plain-append rendering; the plain path has no terminal-control-sequence dependency. |
| This client's own bug is mistaken for an ACP v1 adapter regression, or vice versa, since both are new/young. | §7's fake-agent unit tests isolate wire/reducer correctness from the real agent; the one real integration test isolates true end-to-end interoperability as its own, separately named acceptance point. |
| Scope creep toward milestone 7's fuller TUI during implementation. | §2's non-goals are explicit; a PR plan (next artifact after this design is accepted) fixes the task boundary the same way every other implementation plan in this project's history has. |

## 9. How this design answers the gate's open questions

Cross-referencing `2026-08-30-acp-native-client.md`'s "Open questions a
design must resolve" section directly, so a reviewer can check each one
was actually addressed rather than silently dropped:

1. *Live `session/update` vs. replaying `och export-session`'s JSONL vs.
   both* → §1.2: live only, `session/load` for resume, no JSONL path.
2. *Wire-level observability in scope for a minimal client* → §2: excluded
   this slice, not required to meet its stated goals.
3. *Decline `fs`/`terminal` vs. thin proxies* → §1.3: decline, verified
   against this project's own agent's actual `initializeParams` handling.
4. *Hand-rolled transport vs. `coder/acp-go-sdk` dependency* → §1.1: hand-
   rolled, for the same reason the 2026-08-22 gate already decided this
   for the agent side.
5. *How much terminal-UI investment for a first slice* → §1.4 and §2:
   plain line-oriented output, no TUI framework, milestone 7 remains
   separate.

The DeepSeek Harness frontend-extraction finding (same gate document,
added after the gate's initial commit) does not change any decision here:
this design already commits to a Go, non-DOM, non-framework-dependent
render layer for reasons independent of that finding, so there was never
a point at which this design considered adopting DOM-based components.
