# Web Trajectory UI and Browser Transport — Design

- **Date:** 2026-08-31
- **Status:** Accepted 2026-08-31.
- **Stability:** new, `experimental` client surface. Changes nothing in
  `internal/harness/adapters/acp`, `cmd/och`, or any implemented contract;
  additive only, mirroring how the ACP-native client shipped with zero
  changes to the ACP v1 adapter itself.
- **Repository:** `open-code-harness` (`github.com/SongYii/open-code-harness`)
- **Normative language:** English
- **Chinese summary:** [网页轨迹 UI 与浏览器传输设计（中文摘要）](2026-08-31-web-trajectory-ui-design.zh-CN.md)
- **Authority:** [Web trajectory UI and browser transport architecture gate](../../research/architecture-gates/2026-08-31-web-trajectory-ui.md); [Client surface reuse and post-Slice-B sequencing decision](../../research/architecture-gates/2026-08-30-client-surface-and-security-sequencing.md) (standing decision, not reopened)
- **Implemented contracts this slice must not change:** [ACP v1 adapter](../../architecture/acp-v1.md) (wire shape, clip bounds, "Never projected on ACP" list — all unchanged), [ACP-native client](../../architecture/acp-native-client.md) (`internal/client/acp`, `cmd/acp-client` — untouched, reused only as a spawning-flag precedent, not as a dependency)

English is normative. The Chinese file is a synchronized summary, not a
field-for-field translation.

---

## 1. Decision summary

A browser trajectory UI is an ACP v1 client, exactly like `cmd/acp-client`,
reachable over a WebSocket instead of stdio. This design adds exactly one
new thing to carry ACP's existing wire bytes to a browser tab, and nothing
else:

1. **A new binary, `cmd/acp-web-bridge`, that is a dumb relay, not a second
   ACP client implementation.** It spawns `och -acp` (or any ACP v1 agent
   binary, mirroring `-agent`/`-cwd` from `cmd/acp-client`) exactly as
   `cmd/acp-client` already does, then pumps bytes: one subprocess stdout
   line becomes one WebSocket text frame (trailing `\n` stripped); one
   incoming WebSocket text frame becomes one subprocess stdin line
   (trailing `\n` appended). It never parses JSON-RPC, never knows a
   method name, and never reduces a trajectory. All ACP semantics —
   `initialize`, `session/new`/`load`, `session/prompt`,
   `session/request_permission`, trajectory reduction — are implemented
   fresh in the browser's own TypeScript, which is a genuine, independent
   ACP v1 client implementation, not a thin wrapper around
   `internal/client/acp` (which cannot run in a browser).
2. **The bridge serves its own static frontend on the same origin it
   upgrades WebSocket connections on**, so there is exactly one origin to
   reason about, one port, and one process per invocation — no separate
   dev server, no CORS story.
3. **Two independent checks gate every WebSocket upgrade**: the `Origin`
   header, if present, must exactly equal the bridge's own serving origin
   (rejecting a hostile tab in another origin that tries to open a
   WebSocket to this loopback port — the threat Codex's own Origin-header
   rule, read in the gate, demonstrates is real and cheap to close); and a
   random per-invocation token, printed to stderr with the ready URL and
   carried as a query parameter (browsers cannot attach custom headers to
   a WebSocket handshake), must match exactly (defense against another
   local account on a shared host, which Origin alone does not stop).
   Binding is hardcoded to loopback in this slice; there is no flag to
   change it.
4. **Live rendering accepts ACP's "Never projected on ACP" boundary as-is
   in this slice.** No token usage, no provider-reported latency, no
   `finishReason` — the browser gets exactly what any ACP client gets
   today. A coarse, honest substitute is still available for free: the
   browser can timestamp each notification and RPC response at local
   receipt time and derive a wall-clock span per turn and per tool call
   from that, without any protocol change. This is disclosed as an
   approximation (subject to network/render jitter), never presented as
   provider-reported timing.
5. **Turn grouping needs no new wire field.** `toolCallId` is already
   `turnID + "/" + callID` (`acp-v1.md`), and the client itself knows a
   turn starts the moment it calls `session/prompt` and ends when that
   call's response arrives; on replay, `user_message_chunk` marks the same
   boundary. The turn-grouped ledger DeepSeek Harness's `ui-trajectory`
   uses as its central organizing idea is derivable entirely client-side.
6. **Scope for this slice: one bridge process, one subprocess, one active
   browser connection, one session.** No multi-tab fan-out, no
   `session/list`/`resume`/`delete` in the browser, no transcript-export
   integration, no TLS, no configurable bind address. Each is a named,
   reasoned non-goal (§2), not an oversight.

## 2. Goals and non-goals

### Goals

- Make the trajectory this project already produces (ACP `session/update`,
  `session/request_permission`) viewable and drivable in an ordinary
  browser tab, without adding a second application protocol or a second,
  competing notion of what a "client" is.
- Reuse the turn-grouped-ledger / per-record-inspector / composer-position-
  approval interaction language the [gate](../../research/architecture-gates/2026-08-31-web-trajectory-ui.md)
  found compelling in DeepSeek Harness's `ui-trajectory`/`ui-approval`,
  without reusing any of its code, data model, or backend protocol — the
  2026-08-30 sequencing decision's standing rule.
- Name, concretely, the new untrusted-input class a browser introduces
  (a hostile page in another tab reaching a loopback-bound agent process)
  and close it with the same kind of disclosed, verifiable mechanism this
  project already uses for exec sandboxing and secret redaction, not a
  hand-wave.

### Non-goals (excluded from this slice, not deferred without a reason)

- **Live token usage, latency, `finishReason`, or a TTFT/decode-split
  timing overview.** These require fields ACP v1's wire contract
  deliberately withholds (§1.4). Reconciling that is a protocol-level
  question bigger than one client and outside this design's authority;
  the transcript export already has the data for a *finished* session,
  but wiring that into the live browser view is a separate, later slice.
- **Multiple simultaneous browser viewers of one session.** The bridge
  holds exactly one subprocess and relays to exactly one active WebSocket
  connection at a time. A new connection replaces the previous one's wiring
  (letting a tab refresh or reconnect work naturally, since the
  subprocess itself is untouched and simply has no reader/writer for a
  moment); it does not fan out to several tabs at once, and there is no
  arbitration for which tab may send input. True multi-viewer fan-out is a
  materially different, harder problem (broadcast plus write arbitration)
  left to a future slice if ever needed.
- **`session/list`/`session/resume`/`session/delete` in the browser UI.**
  This slice's scope matches `cmd/acp-client`'s own scope exactly: one
  session per bridge invocation, created fresh or resumed via the same
  `-resume <sessionId>` flag shape. Reconnecting the *same* browser tab to
  the *same* session works because the frontend records the session id in
  its own URL (`history.replaceState` after `session/new` succeeds), not
  because the bridge tracks sessions.
- **Non-loopback network exposure and TLS.** The bridge binds
  `127.0.0.1` only, hardcoded, with plain `http://`/`ws://`. Loopback
  traffic never leaves the machine's own kernel network stack, so TLS's
  confidentiality-in-transit property is moot for this default; if a later
  slice ever allows binding elsewhere, TLS becomes mandatory then, not
  retrofitted now. There is no flag to change the bind address in this
  slice.
- **A specific frontend framework or build tool.** The gate explicitly
  left this to an implementation plan; this design fixes only the
  contract (TypeScript, a genuine ACP v1 client implementation, static
  assets embedded into the bridge binary at build time so the shipped
  artifact is one self-contained binary with no runtime npm/node
  dependency), not the library.
- **Reusing `internal/client/acp` inside the bridge.** That package parses
  and reduces ACP JSON-RPC for a Go terminal renderer; the bridge parses
  nothing. Importing it into the bridge would be dead weight and an
  invitation to let Go-side protocol logic leak into what must stay a
  transport-only relay.

## 3. The bridge: a dumb relay, not a second client

`cmd/acp-web-bridge` reuses exactly `cmd/acp-client`'s own subprocess
lifecycle shape — `-agent <path>` (required), `-cwd <path>` (required),
everything after `--` forwarded as the agent's own argv, one fixed cleanup
order (close the subprocess's stdin, then wait for exit) — because
spawning and reaping an ACP agent subprocess correctly has nothing to do
with which transport renders its output, and this project does not
duplicate a correctness-sensitive pattern it has already gotten right
once. It adds `-listen <host:port>` (default `127.0.0.1:0`, an
OS-assigned ephemeral port) instead of rendering to `os.Stdin`/`os.Stdout`.

Per browser connection (at most one active at a time, §2):

```
subprocess stdout  --[one NDJSON line, \n stripped]-->  one WS text frame
one WS text frame  --[\n appended]-->                   subprocess stdin
```

- **No JSON parsing, no method inspection, in the bridge.** The relay does
  not know `session/prompt` from `session/update` from a permission
  request; it does not need to, because ACP v1 is already a duplex,
  self-describing JSON-RPC stream on both sides of the pipe it is
  bridging. This is deliberately the same shape Codex's own `app-server`
  uses across its stdio/websocket/unix transports (per the gate): one
  message schema, transport is just how the bytes travel.
- **Existing bounds do the enforcement work.** `och -acp`'s own codec
  already fails closed on an oversize incoming line (`maxFrameBytes = 1
  MiB`, tears down `Serve`, per `acp-v1.md`'s Clip bounds) and its own
  projector already bounds every outgoing frame to the same limit. The
  bridge sets its WebSocket server's own max-message-size comfortably
  above 1 MiB (headroom, not a new independent limit) so it never clips a
  legitimately-sized ACP frame; it does not re-implement or duplicate
  `och -acp`'s own clipping logic.
- **Reconnection is a rewire, not a restart.** When the active WebSocket
  connection closes (tab closed, refreshed, network drop) and a new one
  arrives, the bridge re-points its two pump goroutines at the new
  connection; the subprocess itself is never restarted or informed — from
  its perspective, its stdin briefly had no writer, which `och -acp`
  already handles correctly since it is normal for a slow or thinking
  client.

## 4. Browser defense: Origin allowlist and a per-invocation token

A browser introduces a genuinely new class of untrusted input this
project's threat model has not previously named: arbitrary web content
loaded in another tab on the operator's own machine, which can attempt to
open a WebSocket to any `ws://127.0.0.1:PORT` it can guess or scan for,
regardless of which page served the operator's actual UI. Binding to
loopback does **not** defend against this — a hostile page's own
JavaScript runs on the same machine and can reach any loopback port. Two
independent checks run on every WebSocket upgrade request, both before the
handshake completes:

1. **Origin allowlist.** The bridge knows its own serving origin
   (`http://127.0.0.1:<port>`, the exact host:port it is listening on). If
   the upgrade request carries an `Origin` header, it must equal that
   value exactly, or the upgrade is refused with `403`. A request with no
   `Origin` header at all (a non-browser client, or a browser same-origin
   request that happens to omit it) is not rejected on this basis alone —
   the token check below still applies. This is the browser-specific half
   of the defense: a hostile origin's own WebSocket attempt always carries
   its own `Origin`, and it will never equal this bridge's own.
2. **Per-invocation token.** At startup, the bridge generates a random
   token (a fixed-length, cryptographically random value) and prints the
   full ready URL, `http://127.0.0.1:<port>/?token=<token>`, to stderr —
   the same "logged, operator-visible" precedent this project already uses
   for exec sandboxing's own escape hatch. The served page's own script
   reads the token from its URL and includes it as a WebSocket query
   parameter (`/ws?token=...`), since browser JavaScript cannot attach
   arbitrary headers to a WebSocket handshake. The bridge compares it in
   constant time and refuses the upgrade on any mismatch, including a
   missing token. This is the non-browser-specific half: it defends
   against another account or process on a shared host that can reach the
   loopback interface but was never handed the token, which the Origin
   check alone does not stop.

Both checks are mandatory and independent; neither substitutes for the
other. Failing either produces the same `403` with no distinguishing
detail on the wire (matching this project's own established pattern of
not leaking which specific check failed to an unauthenticated caller).

## 5. Live trajectory data: what the browser actually gets, and what it approximates

The browser's own ACP client implementation receives exactly the messages
any ACP v1 client receives — nothing added, nothing withheld beyond what
`acp-v1.md`'s existing "Never projected on ACP" list already withholds
from every client. Concretely, this means the record inspector DeepSeek
Harness's design inspired can show: tool name and kind, `rawInput`,
completed/failed content, and pending/in-progress/completed/failed status.
It cannot show, in this slice: token counts, provider-reported latency,
`finishReason`, or a time-to-first-token/decode split — those fields never
reach any ACP client today, browser or otherwise.

**A coarse timing view is still honest and available**, and this design
treats it as in-scope precisely because it requires no protocol change:
the browser stamps its own local receipt time on the `user_message_chunk`
(or the moment it calls `session/prompt`) that opens a turn, and on the
final notification or RPC response that closes it, deriving a wall-clock
span per turn and per tool card from locally observed timestamps alone.
This is disclosed to the operator as a local, approximate measurement
(subject to render and event-loop jitter), never labeled or presented as
provider-reported latency — the distinction between "a fact this client
observed" and "a fact the provider reported" stays visible, matching this
project's own "fact, not a promise" discipline for `Enforcement` levels
applied here to timing data instead of resource bounds.

## 6. Frontend contract (implementation-plan chooses the library)

- **Language:** TypeScript, compiled to static JS/CSS/HTML assets.
- **It is a real ACP v1 client**, independently implementing `initialize`,
  `session/new`/`session/load`, `session/prompt`,
  `session/request_permission` handling, and `session/cancel` against the
  same wire contract `acp-v1.md` and `internal/client/acp` already satisfy
  in Go. This is genuine new client-protocol code, not a reuse of
  anything Go-side, and must be verified against the real contract, not
  assumed correct because a Go implementation of the same protocol
  already exists (§7).
- **Turn-grouped ledger**: group records by turn, using the client's own
  knowledge of when it called `session/prompt` (live) or
  `user_message_chunk` boundaries (replay via `session/load`, if a later
  slice adds it) — no new field needed, per §1.5.
- **Per-record inspector**: selecting a record opens a detail view for
  `rawInput`/content/status/the local timing approximation from §5,
  informed by, not a copy of, DeepSeek Harness's inspector design.
- **Permission requests take over the input position** rather than a
  modal dialog, showing the correlated tool call's detail inline — the one
  interaction-design choice this document fixes now rather than leaving
  open, since it has no technical dependency on anything else in this
  design and a design phase is the right place to settle a UI decision
  that otherwise has no natural owner.
- **Assets are embedded into the `cmd/acp-web-bridge` binary at build
  time** (Go's `embed.FS` over the frontend's own build output directory)
  so the shipped artifact remains one self-contained binary consistent
  with every other binary this project ships — no separate static file
  server, no runtime Node.js dependency.

## 7. Verification and acceptance

- **Bridge relay tests** (new, mirroring `cgroup_linux_test.go`'s and
  `sandbox_darwin_test.go`'s own style of testing the real mechanism, not
  a mock): a fake subprocess (a small test binary or `exec.Command`
  wrapping a script) and a real WebSocket connection from a Go test
  client, asserting byte-for-byte line-to-frame and frame-to-line
  translation in both directions, including a multi-line burst and a
  frame at exactly the configured max-message-size boundary.
- **Reconnection test**: first WebSocket connection closes mid-session;
  a second connects; both observe the same underlying subprocess without
  it being restarted (assert on the subprocess's own PID/start time being
  unchanged across the swap).
- **Origin and token tests**: a matching-origin, matching-token upgrade
  succeeds; a mismatched or absent-when-required Origin is refused with
  `403` before any subprocess bytes are relayed; a missing or wrong token
  is refused identically; a request with no `Origin` header but a correct
  token succeeds (covering a non-browser test client and any legitimate
  same-origin request that omits it).
- **Real interoperability proof, matching `TestInteropRealAgentCompletesAnApprovedWriteFile`'s
  own standard**: build the real `och` binary, spawn it through the real
  `cmd/acp-web-bridge` binary, drive it end to end through a real
  WebSocket client (not a mock, not fixtures) against a local scripted
  provider fixture, and confirm a `write_file` tool call — including its
  interactive permission approval — completes and the turn reaches
  `end_turn`. This is the Go-side proof; the frontend needs its own
  equivalent (below).
- **A frontend real-interop proof is required before this slice is
  called done, at the same evidentiary bar as the Go-side proof above —
  not a lower bar just because the code is new to this project's stack.**
  A headless-browser or equivalent automated test drives the actual
  compiled frontend against a real spawned bridge+`och` process and
  completes one full turn, including an interactive permission approval,
  end to end. The exact tool (a headless browser automation library) is
  an implementation-plan choice; the requirement that it be a *real*
  end-to-end run, not a component-level unit test standing in for it, is
  fixed here.
- Mutation checks on the Origin/token checks specifically: each
  independently disabled must make its own dedicated test fail for the
  right reason.
- `go vet`, `gofmt`, `CGO_ENABLED=0 go build ./...`, `go test -race
  ./... -count=1` for the Go side; whatever the chosen frontend
  toolchain's own equivalent static-check/build/test commands are for the
  TypeScript side, documented in the implementation plan.

## 8. Risks

| Risk | Mitigation |
| --- | --- |
| A hostile page in another browser tab opens a WebSocket to the bridge's loopback port. | §4: Origin allowlist rejects any non-matching Origin before the handshake completes. |
| Another local account or process on a shared host reaches the loopback port without ever seeing the operator's browser. | §4: the per-invocation token is required independently of Origin; a caller that never saw the printed URL cannot supply it. |
| The browser's independently-implemented TypeScript ACP client drifts from the real wire contract, since it is new code, not a reuse of the Go implementation. | §7: a required, real end-to-end interoperability proof against the actual `och -acp` binary, at the same bar as the Go client's own proof, not a lower one. |
| An operator mistakes the local, receipt-time-based timing approximation (§5) for provider-reported latency. | The frontend contract (§6) requires the distinction stay visibly labeled; this is a disclosed approximation, never presented as a provider fact. |
| A second tab silently takes over the active connection, confusing an operator who forgot the first tab was open. | Disclosed, intentional v1 behavior (§2), not a defect; true multi-viewer fan-out is a named non-goal for a later slice if ever needed. |
| Scope creep toward session list/resume/delete, transcript-export integration, or a richer timing overview during implementation, since all are adjacent and visible in the gate's own findings. | §2's non-goals are each stated with their own reason; an implementation plan fixes the task boundary the same way every prior plan in this project's history has. |

## 9. How this design answers the gate's open questions

Cross-referencing `2026-08-31-web-trajectory-ui.md`'s "Open questions a
design must resolve" directly:

1. *Bridge shape* → §1.1, §3: a new binary, a dumb byte relay reusing
   `cmd/acp-client`'s own spawn-flag shape, not a protocol-aware second
   client and not a new mode grafted onto `och -acp` itself.
2. *Network exposure and authentication* → §1.3, §4: loopback-only,
   hardcoded, no bind-address flag; dual Origin-allowlist and
   per-invocation-token checks, both mandatory.
3. *Live vs. historical data reconciliation* → §1.4, §5, §2: v1 accepts
   ACP's live boundary as-is, adds only a disclosed, locally-derived
   coarse timing approximation that needs no protocol change; transcript-
   export integration for finished-session usage/latency data is an
   explicit non-goal for a later slice.
4. *Session-management scope* → §2: single-session viewer only, matching
   `cmd/acp-client`'s own scope; reconnection to the same session works via
   a frontend-owned URL, not bridge-side session tracking.
5. *Permission-request placement* → §6: composer-position takeover,
   decided now since it has no technical dependency elsewhere in this
   design.
6. *Rendering technology* → §6, §2: TypeScript and the embedding
   requirement are fixed; the specific framework and build tool are left
   to the implementation plan, per the gate's own deferral.
