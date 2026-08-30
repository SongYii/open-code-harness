# ACP-Native Client Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-30

**Scope:** Per the
[client surface and security sequencing decision](2026-08-30-client-surface-and-security-sequencing.md),
identify whether a genuinely ACP-native reference client already exists
worth studying, ahead of designing "a minimal ACP-native client sufficient
to send a prompt and render a trajectory view from `session/update`
notifications and `och export-session` output" — step 2 of that decision's
sequencing, now that exec sandboxing and resource quotas (step 1) is
implemented. This gate does not design or implement anything.

This project's own [ACP v1 adapter architecture
gate](2026-08-22-acp-v1-adapter.md) already researched the **agent/server**
side of ACP in depth (wire framing, lifecycle, permission bridging,
cancellation, verification) and this project implements exactly that side
today (`adapters/acp`, see [Implemented ACP v1
adapter](../../architecture/acp-v1.md)). This gate is the mirror image: the
**client** side — the thing that spawns an agent subprocess over stdio,
drives `initialize` → `session/new` → `session/prompt`, and turns the
resulting stream of `session/update` notifications into something a human
can read. It does not re-derive wire-level facts (framing, lifecycle
ordering, permission-request shape) that the 2026-08-22 gate already
established from the same specification; it cites that gate for those and
focuses on what is genuinely new here: real client-side architectures.

English is normative. The Chinese file is a synchronized reading copy.

## Comparison set and pinned commits

Per Documentation rule 8, each was fetched with
`scripts/fetch-reference.sh <owner/repo> <sha>` into the gitignored
`.reference/` directory and read directly — not recalled from memory or
from a project's marketing page. Three sources, not six: each was read in
depth rather than skimming a larger set, matching this project's own
stated research value that depth beats breadth once a pattern converges
across independently-built implementations.

| Project | Repository | Commit | Observed | Why chosen |
| --- | --- | --- | --- | --- |
| Zed | `zed-industries/zed` | `399258f` | 2026-08-30 | The protocol's originating, most authoritative client; the 2026-08-22 gate already established that other agents (codex-acp) were built to plug into Zed first |
| acp-go-sdk | `coder/acp-go-sdk` | `0845a3b` | 2026-08-30 | The most directly relevant reference for idiomatic Go client patterns, from a credible maintainer (Coder), given this project's own pure-Go, `CGO_ENABLED=0` constraint |
| Toad | `batrachianai/toad` | `dd4f90e` | 2026-08-30 | A minimal terminal client rendering a trajectory — architecturally the closest published shape to what step 2 of the sequencing decision actually asks for, not a full IDE. Built by Will McGugan (creator of Rich/Textual). **License: AGPL** — noted here as a citation fact; this gate's own no-copying rule (below) applies regardless of any project's license |

`MoonshotAI/kimi-code`, already pinned by the 2026-08-22 gate for its
agent/server-side ACP work, was not re-fetched: its own repository
structure (as read at that gate — `packages/acp-server`,
`packages/acp-adapter`) does not suggest a client-side counterpart, and
this gate's three chosen sources already converge strongly enough (below)
that a fourth shallow read would add breadth without adding signal.

`deepseek-ai/deepseek-harness` — already pinned at `cd5ef81` by the
2026-08-30 exec-sandboxing gate, re-observed here at the same commit,
2026-08-30 — is a different case: it does ship an extensive client/
frontend (`packages/client/ui-*`, dozens of packages), addressed directly
below rather than in this table, since it was checked specifically to
answer whether it is adoptable, not chosen as a converging fourth
comparison point.

## Per-project findings

### coder/acp-go-sdk — the minimal Go client shape

`example/client/main.go` is a complete, runnable minimal client in ~270
lines. Its structure is the plainest possible answer to "what must a
client actually do":

- **Process spawning**: `exec.CommandContext` with `StdinPipe()` /
  `StdoutPipe()` piped to the connection, `Stderr` left as the client's own
  stderr (never mixed into the ACP stream — the same stdout/stderr
  discipline the 2026-08-22 gate found on the agent side, required in the
  same direction here: the agent must not corrupt stdout, and the client
  must not either, since ACP is symmetric NDJSON on one pipe pair).
- **Wire sequence**: `Initialize` (declaring `ClientCapabilities.Fs` and
  `.Terminal`) → `NewSession` → `Prompt`. Nothing else is required to hold
  one turn.
- **The `Client` interface** (`types_gen.go:9431`) is exactly seven
  methods: `ReadTextFile`, `WriteTextFile`, `RequestPermission`,
  `SessionUpdate`, and four terminal methods
  (`CreateTerminal`/`KillTerminal`/`TerminalOutput`/`ReleaseTerminal`/
  `WaitForTerminalExit`). A client that declines the `fs` and `terminal`
  capabilities at `initialize` only ever needs `RequestPermission` and
  `SessionUpdate` to actually do something meaningful; the rest exist to
  satisfy the interface and can return a fixed "not supported" response.
  This is the genuine floor: two real methods.
- **Rendering is a bare `switch` over `SessionUpdate`'s populated field**
  (`AgentMessageChunk`, `ToolCall`, `ToolCallUpdate`, `Plan`,
  `AgentThoughtChunk`, `UserMessageChunk`) printed with `fmt.Println` — no
  state is kept across updates in this example. It is honest about being a
  demo, not a reference for trajectory rendering; see Toad and Zed below
  for that.
- **Transport implementation** (`connection.go`): a single reader
  goroutine uses `bufio.Scanner` with a growable buffer (1 MiB initial, 10
  MiB max) for NDJSON framing — not `bufio.Reader.ReadString`, to bound one
  oversized line without failing outright. Two different concurrency
  disciplines apply to what arrives on that one stream: inbound
  **requests** from the agent (like `session/request_permission`) are each
  dispatched on their own goroutine, tracked in an `inflight` map keyed by
  request id so a later `$/cancel_request` can cancel exactly that
  handler's context; inbound **notifications** (`session/update`) are
  pushed onto a bounded, sequence-numbered channel and drained by one
  dedicated goroutine, which is what actually guarantees update ordering —
  a client that dispatched notifications on their own goroutines the same
  way it does requests would have no ordering guarantee at all, and a
  reducer keyed by `toolCallId` (see Toad/Zed) depends on that guarantee
  to be correct.

### batrachianai/toad — a minimal terminal client, in production

Toad ([Textual](https://github.com/Textualize/textual)-based TUI, Python)
is the closest published shape to this project's actual target. Two
findings matter beyond the general shape already covered above:

- **`src/toad/acp/agent.py`'s `session_update` handler is a structural
  pattern match over the update's `sessionUpdate` discriminant**,
  maintaining exactly one piece of session-scoped state relevant to
  rendering: `self.tool_calls: dict[str, protocol.ToolCall]`, keyed by
  `toolCallId`. A `tool_call` update creates the entry; a
  `tool_call_update` merges non-null fields into the existing entry and
  re-emits the merged result — a small, explicit reducer, not an
  append-only log. Its own comment on the fallback path is worth quoting
  verbatim for the interoperability gotcha it names: "The agent can send a
  tool call update, without previously sending the tool call \*rolls
  eyes\*" — Toad synthesizes a placeholder `tool_call` entry in that case
  rather than dropping the update. Every branch converts the raw wire
  shape into a typed internal message (`self.post_message(messages.X(...))`)
  before it ever reaches a rendering widget — wire parsing and rendering
  are two separate layers connected by Textual's own message-passing, not
  one function that does both.
- **Toad does not keep its own durable copy of trajectory content.**
  `src/toad/db.py` is a small SQLite table of session *metadata* only
  (id, title, `last_used` timestamp, recency listing) — there is no table
  or file that stores message chunks, tool calls, or plan entries. When
  Toad reattaches to an existing session it calls the agent's own
  `session_load` (`src/toad/acp/agent.py:796`) and rebuilds the in-memory
  `tool_calls` state and rendered history from the replayed
  `session/update` notifications the agent sends back, exactly as if they
  were arriving live. History lives in the agent, once, not duplicated on
  the client.

### zed-industries/zed — the same pattern at production scale, independently arrived at

Three crates: `agent_servers` (subprocess lifecycle and the wire), `acp_thread` (the reducer/state model), `acp_tools` (a wire-level debugger, not examined in depth — noted as existing).

- **`agent_servers/src/acp.rs`** spawns the agent via a shell builder
  (handling the Windows-vs-Unix invocation difference explicitly, `cfg!(windows)`),
  piping stdin/stdout/stderr, and taps both the incoming and outgoing line
  streams through an `AcpDebugLog` before they reach the JSON-RPC layer —
  every ACP message, in both directions, is captured for a live inspector
  view (the `acp_tools` crate), not only for the eventual rendered
  trajectory. This is a distinct, deliberate feature (wire-level
  observability) from trajectory rendering itself, and worth carrying
  forward as an open question below rather than assuming this project's
  transcript projection already covers the same need.
- **`acp_thread/src/acp_thread.rs`'s `handle_session_update`
  (line 2549)** is, independently, the same match-over-`SessionUpdate`-
  variant shape acp-go-sdk's example and Toad's `agent.py` both use — three
  unrelated codebases in three languages converge on identical dispatch
  shape for this exact problem. `AcpThread` (the struct that owns this
  method) is a much larger stateful reducer than Toad's — it also tracks
  `plan`, `token_usage`, `cost`, `prompt_capabilities`,
  `available_commands`, per-turn terminal entities, and a
  `StreamingTextBuffer` that deliberately *delays* revealing already-
  received text to the UI on a timer, purely for a smoother typing
  animation — a UI-polish concern with no bearing on correctness, called
  out here only so a later design does not mistake it for a protocol
  requirement.
- **Many of `AcpThread`'s entry types implement `to_markdown(&self, cx:
  &App) -> String`** — the live, in-memory trajectory can render itself as
  Markdown, which is the same shape as a static export, though this gate
  did not trace how far that serialization is actually used for durable
  history versus in-UI copy/quote features; flagged as unresolved rather
  than asserted.
- Zed did not need to be read for wire-sequence or interface-shape
  questions a second time — every fact in this bullet group is new
  information beyond what acp-go-sdk and Toad already established, which
  is why this gate stopped at three sources instead of continuing to a
  fourth.

## Cross-cutting synthesis

- **Three independently-built clients, three languages (Go, Python,
  Rust), one dispatch shape**: parse the wire `SessionUpdate` once, `match`
  / `switch` on its populated variant, and hand a typed internal event to
  a separate rendering layer. None of them do rendering inline inside
  wire-parsing code. This convergence is strong enough to treat "a reducer
  keyed by `toolCallId`, driven by an ordered notification stream, feeding
  a separate render step" as close to settled, the way the exec-sandboxing
  gate treated bwrap-on-Linux as converged after three independent
  projects landed on the same namespace set.
- **Ordering is load-bearing and is a transport-layer responsibility, not
  a client-logic one.** acp-go-sdk's Connection explicitly separates
  concurrently-dispatched inbound *requests* from strictly-ordered,
  single-goroutine-drained inbound *notifications* for exactly this
  reason. A design must decide, explicitly, how this project's own client
  transport preserves `session/update` ordering — the 2026-08-22 gate's
  transport findings (C1, C7) covered the agent side of NDJSON framing but
  did not need to solve this specific ordering-vs-concurrency split, since
  the agent side there only *sends* notifications, it does not need to
  process an ordered stream of them.
- **History replay is the agent's job, not the client's, in the one
  production client that ships without its own transcript store (Toad).**
  Zed's `to_markdown` capability suggests a richer in-app history/export
  story exists there, but this gate did not confirm whether Zed relies on
  its own storage or on `session/load` replay for reattaching to a prior
  session — an open question below, not a finding.
- **Wire-level observability (a raw request/response/notification log,
  independent of the rendered trajectory) is a real, separately-built
  feature in the most mature client (Zed's `acp_tools`)**, not an
  afterthought bolted onto trajectory rendering. Worth surfacing as a
  distinct design question rather than assuming a trajectory view alone
  covers the same need this project's own operators might have.
- **The minimal client-side `Client` interface is small** (per
  acp-go-sdk): two methods actually need real logic
  (`RequestPermission`, `SessionUpdate`) if a client declines filesystem
  and terminal capabilities at `initialize`. This project's charter
  already treats the workspace filesystem and `exec` as *this harness's*
  jailed tools, not the client's — a minimal client built against this
  project's own agent side has a real, cheap opportunity to decline `fs`
  and `terminal` capabilities entirely rather than reimplementing
  read/write/terminal proxying a second time on the client side, since the
  agent (this project) already owns and confines those effects.

## DeepSeek Harness's frontend: checked directly, ruled out

The sequencing decision this gate implements already rejected adopting
DeepSeek Harness's web UI as this project's primary client surface
(architecture, 2026-08-15 gate's Rejected shape 3). A narrower question
remained open going into this gate: even if the whole web UI isn't
adoptable, are any of its individual presentation components — a
trajectory renderer, a tool-call card — cheaply extractable for reuse
under this project's own ACP-native client? Checked directly at
`deepseek-ai/deepseek-harness` `cd5ef81` (`packages/client/ui-trajectory`,
`ui-chat`, `ui-tool`) rather than inferred from the package list. Two
independent findings each rule this out on their own:

- **Wrong rendering target.** `TrajectoryCell.tsx` imports
  `TrajectoryCell.module.css` — CSS Modules, a browser-DOM styling
  mechanism. This project's own milestone list already names its client
  target `TypeScript TUI client` (`docs/README.md`, milestone 7) — a
  terminal renderer, which has no `<div className=...>` equivalent
  regardless of any data-format compatibility. This alone makes the
  component non-portable to this project's chosen target independent of
  everything else.
- **Deep coupling to a private plugin architecture, not a standalone
  component.** `trajectory-contract.ts` imports its core types
  (`ConversationNode`, `RequestView`, `ToolCallBlock`,
  `AssistantMessageNode`) from `@deepseek-ai/dsh-client-ui-conversation/
  client` — DeepSeek Harness's own internal domain model, not ACP wire
  types — and uses TypeScript `declare module` augmentation to register
  itself into that package's `ConversationViewSnapshotMap` and a separate
  `@deepseek-ai/dsh-client-ui-slots` registry. This is DeepSeek Harness's
  own "capability seam" plugin system (`packages/CLAUDE.md` in that
  repository), not a component with an independent public interface.
  Extracting it would mean either vendoring a chain of DeepSeek Harness's
  own internal packages this gate found no evidence are published for
  external, non-DeepSeek-Harness consumption, or stripping every
  DeepSeek-specific type and registration out — at which point the
  original file is a reading reference for what fields a trajectory view
  needs to track, not code being reused.

This matches, from an independent angle, why DeepSeek Harness's own ACP
server is deliberately "automation-only" (2026-08-22 gate): the web UI's
presentation layer is built against a private, richer internal event
model the ACP surface intentionally does not expose, not against ACP
itself — so there was never a client-side ACP counterpart to adopt in the
first place, only a web frontend that happens to live in the same
repository.

`TrajectorySnapshot`'s own field categorization (`eventNodes`,
`eventLocations`, `requests`, `callSchemas`, `partial`, `runningCalls`) is
noted as a plausible reference for what categories of state a trajectory
view needs to track — read, not copied (DeepSeek Harness is MIT-licensed,
which would permit copying with attribution, but the coupling above makes
copying pointless regardless of what the license allows) — should the
design phase find it useful.

## Open questions a design must resolve, not answered by this gate

- **Live `session/update` streaming vs. replaying `och export-session`'s
  JSONL vs. both.** The sequencing decision states "a future client
  renders trajectory from those existing surfaces, not from a foreign data
  model" — pointing at this project's own session-transcript JSONL
  projection and live `session/update` notifications as the two candidate
  sources. Toad's answer (no independent client-side store; always
  replay through the agent's own `session/load`) is directly analogous to
  reading `och export-session` output for history plus live
  `session/update` for the active turn, and is a real, working, minimal
  precedent for exactly that split — but it is a precedent to weigh, not
  an adopted answer: this gate did not verify whether Zed's richer
  `to_markdown`/history story implies a client sometimes wants its own
  durable copy for reasons Toad's simpler design doesn't need to solve
  (offline browsing without a live agent, multi-client fan-out, etc.).
- **Whether wire-level observability (a raw ACP message log, à la Zed's
  `acp_tools`) is in scope for a *minimal* client**, or whether this
  project's own transcript/audit surfaces already give an operator enough
  visibility that a separate debug view is not needed for a first slice.
- **Which of the "declines `fs`/`terminal` capabilities entirely" versus
  "implements them as thin proxies" shapes fits this project's own
  charter better**, given the harness already jails these effects on the
  agent side — this gate surfaces the option (above) but does not decide
  it, since it depends on ACP capability-negotiation specifics (what an
  agent that requires `fs`/`terminal` capabilities from its client would
  actually need, if this project's own agent side ever requires them) the
  2026-08-22 gate did not need to resolve for the agent side alone.
- **Whether a Go client should own a hand-rolled minimal transport
  (mirroring this project's `adapters/acp` server-side codec) or adopt
  `coder/acp-go-sdk` (or another registry SDK) as a pinned dependency.**
  The 2026-08-22 gate already rejected wholesale adoption of an unverified
  community SDK for the *agent* side (R3, "the framing contract is small
  enough to own"); whether that reasoning transfers unchanged to the
  *client* side, where this gate found the same SDK ships a genuinely
  complete, idiomatic reference implementation of exactly the role needed,
  is a real design decision, not a foregone conclusion either way.
- **How much of the trajectory-rendering surface belongs in a terminal UI
  at all for a first slice** (Toad's Textual widget layer, Zed's `gpui`
  entity/render model) versus a plain, unstyled line-oriented CLI output
  closer to acp-go-sdk's own example — a UI-investment-versus-minimalism
  question explicitly deferred by the sequencing decision to "after" this
  client exists, but the design still has to pick a starting point.

## Evidence limits

- Every citation above traces to a specific pinned commit read in this
  session (table above); no claim is from memory or from a project's
  marketing page or README screenshots.
- This gate does not authorize copying any type name, schema, wire-codec
  shape, or rendering-widget structure verbatim from any of these three
  projects — only the mechanisms and architectural choices they
  represent, exactly as the 2026-08-22 and 2026-08-30 (exec sandboxing)
  gates already state for their own comparison sets.
- Zed's `acp_thread` and `agent_servers` crates are large (10,200 and
  ~2,300 lines respectively, in the files read); this gate read the
  update-dispatch, process-spawn, and debug-tap code paths specifically,
  not the entire crate. Broader Zed-specific UI/UX mechanics (the
  `StreamingTextBuffer` typing animation, the full elicitation/mention/
  diff/terminal subsystems) were seen only enough to know they exist, not
  audited.
- Whether Zed's own history/reattachment story relies on client-side
  storage, `session/load` replay, or both was not confirmed; flagged
  explicitly above as unresolved rather than guessed at.
- `kimi-code` and `deepseek-harness` were not re-fetched or re-read for
  this gate; the claim that neither appears to ship a client-side ACP
  counterpart rests on the 2026-08-22 gate's own directory listing of
  their agent/server-side packages, not on a fresh client-focused read.
- "Current state" here means 2026-08-30. A future gate that revisits any
  of these three projects must re-fetch and re-read, per Documentation
  rule 7, rather than reuse this document's characterization.
- This gate does not choose a design. The next step is a normative design
  for a minimal ACP-native client, informed by — not dictated by — the
  findings above.
