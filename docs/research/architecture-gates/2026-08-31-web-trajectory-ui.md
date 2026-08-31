# Web Trajectory UI and Browser Transport Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-31

**Scope:** The [2026-08-30 client surface and security sequencing
decision](2026-08-30-client-surface-and-security-sequencing.md) ordered
exec sandboxing and resource quotas, then a minimal ACP-native client,
before "broader UI investment." Both prior steps are now designed,
implemented, and verified (the exec sandboxing/resource-quota slices
through 2026-08-31, and the Go-native [ACP-native
client](../../architecture/acp-native-client.md)). This gate researches
what a *browser*-rendered trajectory view — the "broader UI investment"
that sequencing named but did not design — would need beyond what
`cmd/acp-client` already proves: primarily, a transport carrying ACP v1's
existing JSON-RPC messages to a browser tab, and an interaction design for
rendering them. It re-verifies, per Documentation rule 7, the same six
reference projects at their 2026-08-31 state, reads a seventh subsystem
(Codex's own `app-server`) discovered specifically because it is the one
reference-project component that already bridges one JSON-RPC protocol to
a browser-reachable transport, and reads this project's own ACP v1 and
session-transcript contracts for what data such a UI would actually have
available. This gate does not design or implement anything.

English is normative. The Chinese file is a synchronized reading copy.

## Standing decision this gate does not reopen

The 2026-08-30 sequencing gate already settled, and this gate treats as
fixed:

1. **ACP v1 remains the sole public client boundary.** Any UI this project
   builds is an ACP client, full stop — it consumes `session/update`,
   `session/request_permission`, and the session-lifecycle RPCs already
   specified in [`acp-v1.md`](../../architecture/acp-v1.md), never a new,
   parallel application protocol. This is what keeps the harness
   model-neutral and UI-neutral, per the project charter.
2. **DeepSeek Harness's web UI is not integrated, forked, or protocol-matched.**
   Its interaction *properties* — a turn-grouped ledger, an explicit tool
   pipeline, log reconstructability — were already accepted as design
   language at the 2026-08-15 gate's Adopt column. Its actual frontend
   code, data model, and backend protocol are out of bounds; nothing here
   authorizes copying any of it (Documentation rule 6/8, and the
   2026-08-30 gate's Decision item 1).
3. A browser transport is therefore an additional **carrier** for ACP v1's
   existing wire messages, not a new protocol surface competing with ACP.

## What this project already has

- **ACP v1 wire contract**: JSON-RPC 2.0 over newline-delimited JSON on
  stdin/stdout (`ServeACP`, `cmd/och -acp`). Live `session/update` carries
  tool cards with clip bounds; `session/request_permission` is a normal
  in-flight RPC a client answers interactively. Session lifecycle
  (`session/list`, `session/resume`, `session/close`, `session/delete`) is
  capability-gated and already specified.
- **`Never projected on ACP`** (`acp-v1.md`, verbatim): "Usage tokens,
  latency, `finishReason`, `providerRequestID`; policy rule IDs;
  `model.request.recorded`; audit digests / commit positions; raw provider
  SSE; domain error codes (fixed JSON-RPC messages remain); subagent
  origin, plans, thoughts, terminals, diffs, ACP v2 fields; verdicts." This
  is a deliberate boundary, not an oversight — the wire protocol withholds
  exactly the fields a token-usage-and-timing display would want live.
- **`cmd/acp-client`**: a real, working Go ACP client (`internal/client/acp`)
  that spawns an agent over stdio, renders a live trajectory to a terminal,
  and answers permission requests — the reference shape any browser client
  now extends, not replaces.
- **Session transcript export** (`docs/architecture/session-transcript.md`,
  `och export-session`): a separate, richer, *historical* JSONL projection
  read directly from SQLite. Its `model.usage.recorded` fact carries
  `inputTokens`, `outputTokens`, `cachedInputTokens`, `latencyMs`,
  `finishReason`, and `providerRequestID` — precisely the fields ACP's live
  wire withholds — but only as a one-shot file for a completed (or
  in-progress-but-already-committed) session, not a live feed a browser
  could subscribe to today.

## Comparison set and pinned commits

Per Documentation rule 8, each was fetched with
`scripts/fetch-reference.sh <owner/repo> <sha>` into the gitignored
`.reference/` directory and read directly. Per Documentation rule 7, all
six are re-verified at today's date; five (`pi-mono`, `kimi-code`,
`grok-build`, `codex`, `maka-agent`) are the same commits the 2026-08-31
exec CPU/disk quota gate re-verified earlier today, and `deepseek-harness`
is unchanged since the same gate (`0a53fb55`).

| Project | Repository | Commit | Observed | UI form factor found |
| --- | --- | --- | --- | --- |
| Pi (`pi-mono`) | `badlogic/pi-mono` | `853a80d` | 2026-08-31 | Terminal (`packages/tui`); a separate transport-agnostic client library (`packages/client`) exists, but no browser UI |
| Kimi Code | `MoonshotAI/kimi-code` | `8f2c60b` | 2026-08-31 | Terminal, vendoring Pi's own TUI as `packages/pi-tui`; no browser UI |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-31 | Full-screen Rust TUI; supports ACP for editor embedding, per its own README; no browser UI |
| OpenAI Codex | `openai/codex` | `a9519cb` | 2026-08-31 | Rust TUI (`codex-rs/tui`); **also** a JSON-RPC `app-server` subsystem exposing multiple transports (below) |
| Maka (Apache, incubating) | `apache/maka` (mirrored as `maka-agent/maka-agent`) | `ef94235` | 2026-08-31 | Desktop app (`apps/desktop`); `packages/ui` is a React component library rendered inside that desktop shell, not a page served over a network protocol |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb55` | 2026-08-31 | The only browser web UI in this set (`packages/client/web`, launched via `npx @deepseek-ai/dsh web`), including a dedicated `ui-trajectory` package |

**Only one of six reference projects renders a trajectory in a browser at
all.** The rest converge on a terminal UI; this project's own prior
2026-08-30 client-surface gate already chose the same starting point
(`cmd/acp-client`) before this broader-UI step.

## DeepSeek Harness's trajectory and approval design (read for interaction language only)

Read directly from `packages/client/ui-trajectory/README.md` and
`packages/client/ui-approval/README.md` at the pinned commit:

- **A turn-aware event ledger, not a flat chat log.** User, Assistant,
  Tool, and nested Subtool records are individually selectable rows. Thick
  rules mark Turn boundaries; compact inline markers identify Steps. A
  standalone compaction request gets its own chronological "Between turns"
  section, while a numbered compaction stays inside its owning turn.
- **Selecting a record opens a local inspector**, not an expand-in-place
  row: token usage, duration, Input, Output, Timing, and durable images
  render in a separate panel, keeping the ledger itself dense.
- **A fixed timing Overview above the ledger** projects real record
  start/duration left-to-right; Assistant spans split recorded
  time-to-first-token from decoding. Dragging selects an interval, wheel
  gestures zoom, a right-click clears the selection, and a right-drag pans
  an already-zoomed view. "In-flight Time stays blank" for a still-running
  record — the view explicitly refuses to fabricate a duration for
  something not yet finished, per the package's own stated limitation.
- **Long ledgers are virtualized**: only the visible row window plus a
  small overscan is mounted; the view opens at the tail and follows new
  records until the operator scrolls up, which suspends following so new
  activity does not interrupt inspection of history.
- **A pending permission request takes over the composer position**, not a
  modal: `ui-approval`'s own README states it "takes over the Conversation
  composer, optionally renders correlated Tool detail, and returns the
  user's decision to the waiting Host request" — the approval surface
  replaces where the operator would otherwise be typing, with the relevant
  tool call's detail shown inline, and its own stated limitation is that
  it "exposes transient decisions only" (allow-once / reject), with
  persistent policy left to a different, Host-side surface.

None of this is a data model or protocol this project can adopt directly —
it is built on DeepSeek Harness's own `ConversationNode`/`RequestView`
session projection, which this project's charter already rejected
reusing. What is reusable is the *shape* of the interaction: group by
turn, inspect on select, show timing as a dedicated overview rather than
inline text, and let a pending approval occupy the input position rather
than interrupt with a dialog.

## New finding: Codex's `app-server` already bridges one JSON-RPC protocol to a browser-reachable transport

`codex-rs/app-server/README.md`, read directly at the pinned commit,
documents `codex app-server` as "the interface Codex uses to power rich
interfaces such as the Codex VS Code extension" — architecturally the same
problem this gate is researching: one JSON-RPC 2.0 protocol (no `jsonrpc`
header on the wire, otherwise standard), multiple transports:

- **stdio** (`--stdio`, the default): newline-delimited JSON, the same
  shape this project's own ACP v1 already uses.
- **websocket** (`--listen ws://IP:PORT`): "one JSON-RPC message per
  websocket text frame" — **the README states this transport is
  "experimental / unsupported. Do not rely on it for production
  workloads,"** a disclosed caution from a well-resourced, shipping
  project, not an unresearched corner this gate should treat as solved.
- **unix socket**: intended for a *local* control-plane client; `codex
  app-server proxy` opens one raw connection to a fixed control socket
  path and proxies bytes to stdin/stdout, and the proxied stream itself
  carries a websocket HTTP Upgrade handshake followed by websocket frames
  — the same wire codec is reused even over a non-network transport.
- **A concrete browser-facing defense already shipped**: when the
  websocket listener is active, it also serves `GET /readyz` and `GET
  /healthz`. `/healthz` "returns 200 OK when no Origin header is present,"
  and "any request carrying an Origin header is rejected with 403
  Forbidden." Since a browser always attaches an `Origin` header to a
  cross-origin (and same-origin `fetch`) request, this specific rule
  blocks arbitrary web-page JavaScript from probing or driving these
  endpoints while leaving plain command-line health checks (which send no
  `Origin`) unaffected — a real, working answer to "how do you stop a
  hostile tab in the operator's own browser from reaching a
  localhost-bound agent process," a threat class no prior gate in this
  project has named.
- **Backpressure is explicit and typed**: bounded queues between
  transport ingress, request processing, and outbound writes; when ingress
  saturates, new requests get JSON-RPC error `-32001`, `"Server overloaded;
  retry later"`, documented as retryable with backoff.

This is the one genuinely new, load-bearing finding of this gate: the
mechanism shape a design would need — one message schema, a pluggable
transport crate, a named browser-defense rule, typed backpressure — is a
proven pattern at a major reference project, but that same project labels
the browser-reachable transport itself experimental and unsupported even
there. Nothing in the five other reference projects (including DeepSeek
Harness, whose own web UI's actual client-to-backend transport was not
traced by this gate beyond its component READMEs) offers a comparably
concrete, primary-sourced transport design to compare against.

## Cross-cutting synthesis

- **The interaction design and the transport design come from different,
  disjoint reference projects.** DeepSeek Harness is the only source for
  "what should a browser trajectory view look like"; Codex's `app-server`
  is the only source for "how do you carry a JSON-RPC agent protocol to a
  browser." A design phase needs both, and neither alone.
- **This project's own "Never projected on ACP" boundary is a real
  constraint on the interaction design, not a detail to design around
  later.** DeepSeek Harness's timing Overview and per-record token-usage
  inspector depend on exactly the fields (usage, latency) ACP's live wire
  deliberately excludes. The session-transcript export already carries
  those fields, but only as a one-shot historical file, not a live
  subscription. A first browser-UI slice therefore faces a real choice
  this gate does not resolve: render live with the fields ACP actually
  sends (no live usage/timing), layer the transcript export on top only
  for completed turns or sessions, or treat this as evidence that "Never
  projected" itself should be revisited — the last option is a
  protocol-level change bigger than one client and not this gate's, or
  even a client design's, to decide unilaterally.
- **A browser introduces a genuinely new untrusted-input class this
  project has not previously named.** Every existing threat-model
  statement in `SECURITY.md` treats the model as untrusted input reaching
  a local workspace. A browser tab reachable over any network transport
  (even loopback-only) adds a second, different attacker: arbitrary web
  content in another tab on the same machine (a DNS-rebinding /
  same-machine CSRF shape), which Codex's Origin-header rule defends
  against concretely. A design must state whether the bridge is
  loopback-only, whether it needs a rule like Codex's, and whether any
  token/credential is required — this gate surfaces the precedent, not the
  answer.
- **No reference project's transport or UI code may be copied**
  (Documentation rule 6/8, restated from every prior gate); only the
  mechanisms above — pluggable transport over one schema, Origin-based
  browser defense, typed backpressure, turn-grouped ledger with a
  dedicated timing overview and composer-position approval — are
  available to inform a design.

## Open questions a design must resolve, not answered by this gate

- **Bridge shape**: a new small server process/mode translating ACP v1
  JSON-RPC 1:1 between stdio (talking to `och -acp`) and a browser-reachable
  transport, versus teaching `och -acp` itself to speak that transport
  directly. Whether that transport is WebSocket (as Codex uses, with its
  own disclosed "experimental/unsupported" caveat), Server-Sent Events for
  the server-to-client direction plus HTTP POST for requests, or something
  else.
- **Network exposure and authentication**: loopback-only by default (matching
  this project's own `-provider-allow-insecure-loopback` precedent's
  loopback-only carve-out philosophy), whether an Origin-header rule or
  equivalent is warranted, and whether any bearer token or similar is
  required before a design can call the bridge safe to run on a developer
  machine.
- **Live vs. historical data reconciliation**: whether a first slice
  accepts ACP's live "Never projected" boundary as-is (no live usage/token/
  timing display), whether it augments a *finished* session's view by
  separately reading the transcript export, and whether "the timing
  Overview" as a feature is in scope for a first slice at all given this
  gap.
- **Session-management scope**: a single-session live viewer (mirroring
  `cmd/acp-client`'s own current scope) versus surfacing
  `session/list`/`resume`/`delete` in the browser UI itself in the same
  slice.
- **Permission-request placement**: composer-position takeover (DeepSeek
  Harness's approach) versus a modal or inline row — a real design
  decision informed, not dictated, by the reference read above.
- **Rendering technology**: this gate did not research browser-side
  framework choices (React vs. something else) or build tooling; it
  treats that as an implementation-plan-level decision once a design fixes
  the data flow and transport.

## Evidence limits

- Every citation above traces to a specific pinned commit read in this
  session (table above) or to files read directly inside `.reference/`;
  no claim is from memory, a search-engine summary, or a project's
  marketing page. An earlier attempt in this same research pass to
  characterize DeepSeek Harness via web search produced inconsistent,
  templated-marketing-looking results (an implausible reported star count,
  an unrelated framework attribution) across several third-party sites;
  those results were discarded entirely and every claim in this document
  was re-derived from the actual pinned checkout instead.
- This gate read only the `ui-trajectory` and `ui-approval` package
  READMEs for DeepSeek Harness, and only `app-server`'s own README plus
  `app-server-transport/src/lib.rs`'s public exports for Codex — not
  either project's full client or server implementation. Deeper
  behavioral claims (e.g., exact websocket framing edge cases, or how
  DeepSeek Harness's own frontend actually talks to its backend) were not
  traced and are not claimed here.
- This gate does not authorize copying any file path, constant name,
  component name, or configuration shape verbatim from any reference
  project — only the mechanisms and architectural choices they represent,
  per this project's standing rule for every prior gate's comparison set.
- "Current state" here means 2026-08-31. A future gate revisiting any of
  these projects must re-fetch and re-read per Documentation rule 7,
  rather than reuse this document's characterization.
- This gate does not choose a design. The next step is a normative design
  for a browser transport bridge and a browser trajectory UI, informed
  by — not dictated by — the findings above, and constrained by the
  2026-08-30 sequencing gate's standing decision that ACP v1 remains the
  sole client protocol.
