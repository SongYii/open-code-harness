# Web Trajectory UI — Implemented Contract

**Status:** Implemented; not GA

**Stability:** `experimental` until v1.0

**Maturity:** pre-v0; not a general availability release

**Authority:** [Web trajectory UI and browser transport design](../superpowers/specs/2026-08-31-web-trajectory-ui-design.md)

**Evidence:** [Web trajectory UI evidence ledger](web-trajectory-ui-evidence.md)

**Packages:** `internal/client/acpweb` (subprocess relay, Origin/token upgrade gate, HTTP/WebSocket server); `cmd/acp-web-bridge` (the binary: flags, embedded frontend); `cmd/acp-web-bridge/web` (the independent TypeScript ACP v1 client, turn-grouped ledger, composer-position permission UI)

This document records behavior enforced by the current code and tests. It
is an internal contract, not a stable public protocol. The Go half is a
Go contract; the TypeScript half is a browser-side contract with no
compiler-enforced link to the Go side beyond the wire bytes both sides
agree on.

## Scope

A browser trajectory viewer for ACP v1 sessions. `cmd/acp-web-bridge` is
a dumb relay, not a second ACP client: it spawns an ACP v1 agent over
stdio (mirroring `cmd/acp-client`'s own `-agent`/`-cwd` shape) and
carries its wire bytes, unparsed, to exactly one browser WebSocket
connection at a time. Every ACP v1 semantic — `initialize`,
`session/new`/`load`, `session/prompt`, `session/request_permission`,
trajectory reduction — is implemented independently in the browser's own
TypeScript, served from the bridge's embedded frontend assets. ACP v1
remains the sole client protocol (the 2026-08-30 client surface and
security sequencing decision, reaffirmed by the design this document
implements): the bridge introduces no new application protocol.

## The relay (`internal/client/acpweb.Relay`)

Spawns one agent subprocess (`NewRelay`, mirroring `cmd/acp-client`'s own
`exec.Command`/`StdoutPipe`/`StdinPipe` pattern) and pumps its NDJSON
stdout/stdin lines to and from whichever `Conn` (a real WebSocket
connection, or a fake in a test) is currently active (`SetConn`). The
relay never parses JSON-RPC, never inspects a method name, and never
reduces a trajectory:

| Direction | Framing |
| --- | --- |
| Subprocess stdout → active `Conn` | One NDJSON line (trailing `\n` stripped, using a `bufio.Scanner` buffered to `MaxRelayFrameBytes` — matching `internal/client/acp`'s own `decodeFrames` technique for avoiding `bufio.ErrTooLong` on a legitimate large frame) becomes one `Conn.WriteMessage` call |
| Active `Conn` → subprocess stdin | One `Conn.ReadMessage` result becomes one subprocess-stdin line (`\n` appended) |

Stdout is **dropped, not buffered**, while no `Conn` is active — this is
a live view only, matching this project's existing "a dropped
`session/update` write is swallowed" precedent for a client with nothing
currently listening. `MaxRelayFrameBytes` (2 MiB) is deliberate headroom
above ACP v1's own 1 MiB outgoing-frame bound (`acp-v1.md`'s Clip
bounds table), not a second independent limit the relay itself
enforces.

**Reconnection is a rewire, not a restart.** `SetConn` swaps the active
connection and returns the previous one so the caller (the HTTP server)
can close it; the subprocess is never restarted or informed — from its
perspective its stdin briefly has no writer, which an ACP v1 agent
already handles correctly.

## Browser defense: Origin allowlist and per-invocation token
(`internal/client/acpweb.security.go`)

A browser introduces a new class of untrusted input this project's
threat model had not previously named: a hostile page in another tab on
the operator's own machine, reachable to any loopback port regardless of
which page actually served the operator's UI. Every WebSocket upgrade
(and the `/config` endpoint, which reveals a real workspace path) passes
two independent, mandatory checks before anything is relayed:

- **`CheckOrigin(selfOrigin, requestOrigin)`**: a *present* `Origin`
  header must equal the bridge's own serving origin exactly. An absent
  header is accepted by this check alone — the token check below still
  gates it.
- **`ValidateToken(want, got)`**: `crypto/subtle.ConstantTimeCompare`
  against a `crypto/rand`-generated, per-invocation token, printed to
  stderr with the ready URL (`http://127.0.0.1:<port>/?token=<token>`) —
  the same logged-escape-hatch precedent this project already uses for
  exec sandboxing.

`UpgradeAllowed` requires both; a deny returns `403` with no
distinguishing detail on which check failed. Neither Origin binding nor
the token substitutes for the other: Origin defends against a hostile
*browser tab*; the token defends against another *local account or
process* that can reach the loopback interface but never saw the printed
URL.

## Network exposure

`-listen` (default `127.0.0.1:0`, an OS-assigned ephemeral port) accepts
only a port; the host is hardcoded to `127.0.0.1` and rejected at flag
parse time otherwise. There is no flag to bind elsewhere. Plain
`http://`/`ws://` only — no TLS. This is a **local development tool**,
not hardened for exposure beyond loopback; see `SECURITY.md`.

## `cmd/acp-web-bridge`

Flags: `-agent` (required), `-cwd` (required), `-resume` (optional,
reaches the frontend via `/config`, not used by any Go-side ACP call
since the bridge never makes one), `-listen` (default `127.0.0.1:0`).
Prints `acp-web-bridge: ready at http://host:port/?token=...` to stderr.
Signal handling and cleanup order (close the agent's stdin, then wait for
exit) mirror `cmd/acp-client`.

The frontend is embedded via `//go:embed web/dist` at build time. **`go
build ./...` alone does not produce a working binary with real assets**
unless the frontend was already built at least once (`cd
cmd/acp-web-bridge/web && npm ci && npm run build`) — the embedded
directory otherwise falls back to whatever is on disk. See the root
`README.md`'s Development section.

## The independent TypeScript ACP v1 client (`web/src/acp-client.ts`)

A genuinely separate implementation of the same wire contract
`internal/client/acp` satisfies in Go — not a wrapper around it, which
cannot run in a browser. `AcpClient` classifies an inbound frame exactly
the way `internal/client/acp/wire.go`'s `message.isResponse`/
`isRequest`/`isNotification` do (an id with no method is a response; an
id with a method is an inbound call; a method with no id is a
notification), implements `initialize`, `session/new`, `session/load`,
`session/prompt`, and `session/cancel` (sent as a fire-and-forget
notification, matching the Go client's own cancellation semantics)
against `acp-v1.md`'s exact param/result shapes, and answers inbound
`session/request_permission` via a caller-supplied `Handler` — the same
split `internal/client/acp.Handler` uses.

`WebSocketTransport` queues every `send()` call made before the
underlying `WebSocket` finishes its connection handshake (`CONNECTING`
→ `OPEN`) and flushes them in order once it opens. This is load-bearing,
not defensive: `AcpClient`'s own constructor-adjacent `initialize()`
call happens well before that handshake can complete, and the browser
throws on a `send()` to a still-`CONNECTING` socket — discovered as a
real bug by this contract's own required real-browser interoperability
proof (below), not assumed correct in advance.

## Turn-grouped ledger (`web/src/ledger.ts`)

No new ACP wire field was needed: `toolCallId` already encodes
`turnID + "/" + callID` (`acp-v1.md`), and this project's own
single-flight prompt rule (`acp-v1.md`: "Concurrent prompts on one
session are `-32600`") means at most one turn is ever open live at a
time. `Ledger` attributes tool-shaped updates (`tool_call`/
`tool_call_update`) by parsing `toolCallId` — never by guessing from
context — placing a malformed one in an `unassigned` bucket instead of
crashing or silently dropping it; a plain text chunk (which carries no
identifier of its own on the wire) is attributed to whichever turn
`beginTurn`/`endTurn` currently has open. Every derived timing field
carries an `approximateTiming: true` marker so the rendering layer
cannot show it without the label: this is a **local, receipt-time
approximation**, never a provider-reported value — ACP v1's own "Never
projected on ACP" list (`acp-v1.md`) withholds token usage, latency, and
`finishReason` from every client, browser or otherwise, and this
contract does not attempt to work around that boundary.

## Rendering (`web/src/ui.ts`)

`TrajectoryView` renders the ledger (one row per turn separator and
record) and opens a per-record inspector on selection showing
`rawInput`, content, status, and the labeled timing approximation.
Virtualized/windowed rendering for very long sessions is explicitly out
of scope for this slice: every loaded record is mounted directly.

**Permission requests take over the composer position**, not a modal
(`showPermissionRequest`): the input area an operator would otherwise
type a prompt into is replaced with the correlated tool call's
title/kind and allow-once/reject controls, restoring immediately once
answered. A second request arriving while one is pending — not expected
under ACP's own one-prompt-in-flight rule, but defended anyway — is
queued and rendered only after the first is answered.

## Application wiring (`web/src/main.ts`)

Fetches `/config` for the workspace path and any `-resume` session id,
constructs `AcpClient` over a `WebSocketTransport` pointed at `/ws` with
the token read from the page's own URL, and wires `Ledger`/
`TrajectoryView` to the client's `Handler` callbacks. Records the created
or resumed session id in the page's own URL (`history.replaceState`) so
a tab refresh naturally becomes a `session/load` for the same session —
the bridge itself tracks no session state. Shows the resolved
`stopReason` (e.g. `[end_turn]`) after each turn.

## Real interoperability proof

`TestInteropRealBrowserCompletesAnApprovedWriteFile`
(`cmd/acp-web-bridge/interop_test.go`) builds this repository's own real
`och` binary from source, spawns it through this package's own real
`run()` (the exact code `main()` calls), and drives the served page with
a real, independently controlled headless Chrome instance over the
Chrome DevTools Protocol (`chromedp`) — not a mock, not this
repository's own scripted fixtures beyond the model provider itself.
Everything else is real: the agent subprocess, the WebSocket relay, the
independent TypeScript ACP v1 client running inside that real browser,
the interactive permission approval rendered by the real UI (a real
click on the real `.permission-allow` button), and the `write_file` tool
call actually executing against a real workspace directory. This test
found and drove the fix for `WebSocketTransport`'s connection-race bug
(above) — the first time anything exercised the frontend's own timing
against a real, asynchronous WebSocket handshake rather than a
synchronous fake.

The test is gated on a real Chrome/Chromium executable being available
(`CHROME_EXECUTABLE` env var, or `google-chrome`/`chromium`/
`chromium-browser` on `PATH`); it skips cleanly with a stated reason
otherwise, per this project's own established pattern for
environment-dependent tests, rather than asserting behavior no test
actually observed on a host lacking a browser.

## Explicit exclusions

This implemented contract does not provide:

- **Live token usage, latency, `finishReason`, or a TTFT/decode-split
  timing overview.** ACP v1's wire contract withholds these fields from
  every client. The session-transcript export already has this data for
  a *finished* session, but wiring that into the live browser view is a
  separate, later slice.
- **Multiple simultaneous browser viewers of one session.** Exactly one
  active `Conn` at a time; a new connection replaces the previous one's
  wiring. No fan-out, no write arbitration between tabs.
- **`session/list`/`session/resume`/`session/delete` in the browser UI.**
  Scope matches `cmd/acp-client`'s own: one session per bridge
  invocation.
- **Non-loopback network exposure and TLS.** `127.0.0.1` only, hardcoded;
  plain `http://`/`ws://` only.
- **A specific frontend framework beyond what ships.** Vite + vanilla
  TypeScript + Vitest, no UI framework, matching this project's
  minimalism discipline for a single-session, single-viewer page.
- **Virtualized/windowed ledger rendering** for very long sessions.
