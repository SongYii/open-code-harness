# MCP Client Adapter Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-30

**Scope:** Milestone 9 (`docs/README.md`) is "MCP client adapter — not designed
yet." This gate establishes the primary-source facts a design needs before it
can specify one: what the Model Context Protocol (MCP) actually requires of a
client at its current revision, how six official reference agent projects
place an MCP client relative to their own tool-execution and approval
pipelines, and where this project's own `internal/harness/tools` port
architecture already anticipates — and where it does not yet support — an
MCP-sourced tool. It does not design or implement anything.

This is architecturally the **opposite placement** from the ACP-native
client gate immediately before it. That gate researched a client sitting
outside `internal/harness/` entirely, consuming this project's own agent as
a peer process. An MCP client adapter is the reverse: it sits **behind**
`internal/harness/tools`'s existing ports, converting external MCP tools
into this project's own `domain.ToolSpec` so they flow through the same
Policy `Decide` table, `Approver` slot, and audit trail as the four builtin
workspace tools — exactly what the foundational charter already states
(`docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md`
line 20: "外部工具和资源优先通过 MCP 接入；内置工具与 MCP 工具进入统一的工具执行、权限和审计管线").

English is normative. The Chinese file is a synchronized reading copy.

## Comparison set and pinned commits

Per Documentation rule 8, each was fetched with
`scripts/fetch-reference.sh <owner/repo> <sha>` into the gitignored
`.reference/` directory and read directly. Per Documentation rule 7, the six
reference agent projects named there (Pi, Kimi Code, Grok Build, Codex, Maka,
DeepSeek Harness) were re-verified at each repository's current default-branch
HEAD as of this gate — five of six had moved since the exec-sandboxing and
ACP-native-client gates last pinned them; `grok-build` had not.

| Project | Repository | Commit | Observed | Why fetched |
| --- | --- | --- | --- | --- |
| MCP specification | `modelcontextprotocol/modelcontextprotocol` | `ca4ab30` | 2026-08-30 | The protocol's own normative source; first fetch for this project |
| MCP Go SDK | `modelcontextprotocol/go-sdk` | `21c18c6` | 2026-08-30 | The official, pure-Go client/server SDK; directly relevant given this project's own `CGO_ENABLED=0` constraint |
| Codex | `openai/codex` | `88f7765` (was `dde85b4`) | 2026-08-30 | Rust; ships a dedicated `codex-rmcp-client` crate and MCP-specific approval/policy types |
| Kimi Code | `MoonshotAI/kimi-code` | `cbe0a77` (was `9619277`) | 2026-08-30 | TypeScript; wires MCP server configuration through its own ACP-adapter layer rather than a harness-internal port |
| Grok Build | `xai-org/grok-build` | `bc7f02e` (unchanged) | 2026-08-30 | Rust; dedicated `xai-grok-mcp` and `xai-computer-hub-mcp-adapter` crates alongside its own `permission/state.rs` |
| Maka | `maka-agent/maka-agent` | `b832348` (was `d093ba5`) | 2026-08-30 | TypeScript; a self-contained `packages/mcp` with transport security, OAuth, and bounded tool discovery |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb5` (was `cd5ef81`) | 2026-08-30 | TypeScript; its `mcp-client` package bridges directly into a `ToolRuntime`, the closest analog to this project's own `tools` port |
| Pi | `earendil-works/pi` | `853a80d` (was `59a71b2`) | 2026-08-30 | Re-verified per rule 7; checked directly rather than assumed absent |

`Pi`'s current HEAD is bit-for-bit identical to `badlogic/pi-mono`'s own
pinned commit from an earlier gate (`853a80d26c9...`, author Armin Ronacher,
"Add [Unreleased] section for next cycle") — the two repositories currently
share a most-recent commit. This gate did not investigate why (a merge, a
mirror, or a rename) since it has no bearing on the MCP question; it is
recorded here only so a reader is not confused seeing the same SHA cited
under two different repository names across gates.

## What this project already has

Before comparing external projects, the facts already fixed by this
project's own code:

- **`tools.SourceMCP` and `domain.RiskNetwork` already exist as unused
  placeholders.** `internal/harness/tools/catalog.go:17` defines
  `SourceMCP = "mcp"` beside `SourceBuiltin`, and `validateSpec`
  (`catalog.go:140-144`) already accepts either as a legal `ToolSpec.Source`.
  `docs/architecture/tool-runtime.md` states this explicitly: "Source
  `builtin` is shipped; source `mcp` is catalog-legal so a later adapter can
  project into the same type. There is no MCP client." This gate is the
  first step toward that adapter.
- **Policy's table denies `RiskNetwork` unconditionally, in every mode,
  today.** `internal/harness/policy/engine.go:91`: `if input.Network ||
  input.Risk == domain.RiskNetwork { return Decision{Effect: EffectDeny,
  RuleID: RuleNetworkDenied, ...} }` — this check runs before the
  mode-specific table and has no exception, not even under
  `ModeAllowWrites`. If an MCP tool is classified `RiskNetwork` because
  invoking it means calling out to an external server, today's Policy engine
  hard-denies every MCP tool call in every mode without a design change. A
  design must decide explicitly how MCP tools get classified — reusing
  `RiskRead`/`RiskWrite`/`RiskExec` per declared tool risk (leaving
  `RiskNetwork` denied as it is today, for a genuinely different concern), or
  extending the Policy table with new cells for network-risk tools.
- **The Step loop's tool dispatch is a closed four-case switch, not an open
  registry.** `internal/harness/application/pipeline.go:232-286`'s
  `invokeTool` is `switch spec.Name { case tools.NameReadFile: ... case
  tools.NameWriteFile: ... case tools.NameListDir: ... case tools.NameExec:
  ... default: return CodeUnknownTool }`. `parseToolArgs`
  (`pipeline.go:346-362`) decodes a single fixed `toolArgs` struct
  (`path`, `content`, `depth`, `argv`, `cwd`) and validates required fields
  per hardcoded name. There is no port through which a dynamically
  discovered tool — arbitrary name, arbitrary JSON Schema, arbitrary
  arguments shape — could be invoked today. A design has real work here: an
  MCP tool cannot be added by appending an entry to `DefaultWorkspaceSpecs`
  the way a fifth builtin could; it needs either a generic
  `map[string]any`-typed fallback branch keyed by `spec.Source ==
  tools.SourceMCP` routed to a new invocation port, or a broader dispatch
  refactor.
- **`Catalog` names must be unique across the whole catalog, and the catalog
  is built once, statically, at composition.** `tools.NewCatalog`
  (`catalog.go:41-57`) rejects a duplicate name regardless of source; there
  is no per-source namespace. `composition/assembly.go:141`:
  `tools.NewCatalog(tools.DefaultWorkspaceSpecs())` is the catalog's only
  construction site, called once at `composition.Open`. An MCP tool named
  `read_file` on some external server would collide with the builtin
  `read_file` outright unless a design adopts a namespacing convention (see
  DeepSeek Harness's `mcp__<server>__<tool>` below), and there is currently
  no path for the catalog to grow or shrink after startup at all — relevant
  to the spec's own `notifications/tools/list_changed` (below).
- **This project's own ACP v1 adapter already parses and fail-closed-rejects
  `mcpServers` on two of its three session-establishing calls.**
  `internal/harness/adapters/acp/protocol.go:77,100` declare `MCPServers
  []json.RawMessage \`json:"mcpServers,omitempty"\`` on `sessionLoadParams`
  and `sessionResumeParams`; `server.go:236` and `server.go:431` both read
  `if len(params.MCPServers) > 0 || len(params.AdditionalDirectories) > 0 {
  return s.out.writeError(message.ID, codeInvalidParams, "invalid params") }`
  — any ACP client naming a non-empty `mcpServers` list on `session/load` or
  `session/resume` is rejected outright
  (`TestSessionLoadRejectsNonEmptyMCPServers` /
  `TestSessionResumeRejectsNonEmptyMCPServers`-shaped coverage, per
  [ACP v1 adapter](../../architecture/acp-v1.md)). `sessionNewParams`
  (`protocol.go:66-68`) does not declare an `MCPServers` field at all, so
  `session/new` silently ignores one rather than rejecting it — an existing
  asymmetry worth a design's attention, not introduced by this gate. This
  means the ACP protocol itself already carries a mechanism for a *client*
  (e.g., Zed) to tell an agent which MCP servers to use **per session** —
  this project's agent side already refuses that input entirely today,
  which is a real, current design fork: statically configure MCP servers at
  `composition.Open` (matching how `localexec`/`workspacefs` are wired
  today), or eventually also accept them per-ACP-session (a strictly larger,
  currently-declined surface).

## The protocol itself, read at its current revision (2026-07-28)

The specification repository publishes dated revisions under both
`docs/specification/<date>/` and `schema/<date>/`; `2026-07-28` is the
current one (`draft/` is in-progress work beyond it). Reading it directly
mattered here specifically because this revision is a significant departure
from the initialize-handshake shape this project's own charter
(`2026-08-11`, written before this revision existed) and most currently
deployed MCP servers still implement — a design cannot assume the protocol
is what it was.

- **Two incompatible eras coexist by design, and the spec names them
  explicitly.** `basic/versioning.mdx:34-39`: **Modern** (`2026-07-28` and
  later) conveys protocol version, identity, and capabilities as per-request
  `_meta` fields and has no session-establishing handshake at all. **Legacy**
  (`2025-11-25` and earlier) is the familiar `initialize` →
  `notifications/initialized` handshake that establishes a session-scoped
  connection. A **dual-era** implementation supports both. The
  specification's own compatibility matrix (`versioning.mdx:159-172`)
  states plainly that a legacy-only client talking to a modern-only server
  simply fails, with no fall-forward path — era selection is something an
  implementation must actively handle, not something that degrades
  gracefully on its own.
- **Modern MCP is stateless by declared design.**
  `basic/index.mdx:182-219`: "MCP is a stateless protocol: all the
  information needed to process a request is contained in the request
  itself... Clients SHOULD NOT use an individual task, thread, or
  conversation as the lifetime boundary for the stdio process," and
  explicitly: "an open connection, such as a STDIO process, is not a
  conversation or session." Every modern request carries
  `_meta["io.modelcontextprotocol/protocolVersion"]` (required) and
  `_meta["io.modelcontextprotocol/clientCapabilities"]` (required) inline
  (`basic/index.mdx:365-392`); a request missing either is rejected with
  `-32602`.
- **Version negotiation on the modern era is per-request, not a handshake
  result.** A server that does not support the requested version returns
  `UnsupportedProtocolVersionError` (`-32022`) naming its own supported set
  (`basic/versioning.mdx:41-78`); `server/discover` exists for a client to
  learn this proactively but is optional on the modern era.
- **stdio framing is exactly this project's own already-familiar shape.**
  `basic/transports/stdio.mdx:7-21`: one JSON-RPC message per line, no
  embedded newlines, server stderr is free-form and the client "SHOULD NOT
  assume stderr output indicates error conditions" — the same NDJSON framing
  and stdout/stderr discipline this project's own `adapters/acp/codec.go`
  (agent side) and `internal/client/acp/wire.go` (client side, from the
  immediately prior gate/plan) already implement for ACP. Shutdown is
  close-stdin-then-wait-then-escalate-to-SIGTERM/SIGKILL
  (`stdio.mdx:87-104`), and an unexpectedly-exited server "SHOULD" be
  restarted by the client since the protocol is stateless and any in-flight
  request is simply lost (`stdio.mdx:109-115`) — a real operational
  difference from ACP, where this project's own agent process is the thing
  being spawned and is not expected to restart mid-session.
- **Streamable HTTP dropped protocol-level sessions in this same revision.**
  `basic/transports/streamable-http.mdx:14-25`: 2026-07-28 removed the GET
  stream endpoint and removed protocol-level sessions entirely; every
  request is its own POST, answered either as a single JSON object or an
  SSE stream scoped to that one request (`streamable-http.mdx:70-91`).
  Servers "SHOULD bind only to localhost" when running locally and "MUST
  validate the Origin header" to prevent DNS rebinding
  (`streamable-http.mdx:54-68`) — the same DNS-rebinding concern this
  project's own recent `-provider-allow-insecure-loopback` escape hatch
  (ACP-native-client plan, Task 5) already had to reason about for a
  different HTTP surface.
- **`tools/list` and `tools/call` are the two methods that matter for a
  client.** `server/tools.mdx:78-133`: `tools/list` takes an optional
  `cursor` and returns `tools`, `nextCursor`, and (new in this revision)
  `ttlMs`/`cacheScope` for list caching. `tools/call` takes `name` and
  `arguments` and returns `content` (an array of typed blocks — text,
  image, audio, resource link, embedded resource) and/or
  `structuredContent` validated against an optional `outputSchema`
  (`server/tools.mdx:404-576`).
- **Two distinct error channels, and the spec is explicit about which one a
  client should feed back to the model.** `server/tools.mdx:738-785`:
  *Protocol errors* (unknown tool, malformed request) are ordinary JSON-RPC
  errors ("Clients MAY provide protocol errors to language models, though
  these are less likely to result in successful recovery"). *Tool execution
  errors* (an API failure, bad input) are a **successful** JSON-RPC response
  whose result carries `isError: true` alongside `content` describing what
  went wrong ("Clients SHOULD provide tool execution errors to language
  models to enable self-correction"). This project's own
  `application/pipeline.go` already has an directly analogous two-channel
  split for its four builtins — `failToolAndContinue` (a domain-recorded
  tool failure the model sees, e.g. `CodeExecTimeout`) versus an `err != nil`
  application-level failure that aborts the turn — so mapping MCP's
  `isError: true` onto the existing `failToolAndContinue` path and MCP's
  JSON-RPC protocol errors onto the existing application-error path looks
  like a natural fit, not a new concept, though this gate does not decide
  the mapping.
- **The specification itself already states the human-in-the-loop
  expectation this project's Policy/Approver pipeline already
  implements for builtins.** `server/tools.mdx:31-43`: "For trust & safety
  and security, there SHOULD always be a human in the loop with the ability
  to deny tool invocations," and under Security Considerations
  (`tools.mdx:787-803`): clients "SHOULD prompt for user confirmation on
  sensitive operations" and "SHOULD show tool inputs to the user before
  calling the server, to avoid malicious or accidental data exfiltration."
  The spec does not mandate a specific consent mechanism (tool annotations
  like `readOnlyHint`/`destructiveHint` exist as advisory metadata a client
  "MUST consider... to be untrusted unless they come from trusted servers,"
  `tools.mdx:302-307`) — consistent with this project's own charter's
  instinct to route MCP tools through the same Policy/Approver pipeline
  already enforced on builtins, rather than trusting a server's
  self-declared annotations as a substitute.
- **Resources and Prompts are separate, real primitives this gate does not
  scope in or out.** Servers may additionally expose `resources/list` +
  `resources/read` (addressable, URI-identified content, distinct from tool
  results) and `prompts/list` + `prompts/get` (parameterized prompt
  templates) — both independently capability-gated
  (`server/resources.mdx`, `server/prompts.mdx`, not read in depth for this
  gate beyond confirming they exist and are optional server capabilities).
  Whether a first slice of this project's own MCP client adapter needs
  either, beyond tools, is a design question this gate surfaces but does
  not answer.

## The official Go SDK's client shape

`modelcontextprotocol/go-sdk` `21c18c6`, `examples/client/listfeatures/main.go`
(28 lines to a working client) is the clearest evidence for what adopting
this SDK would remove from a hand-rolled implementation:

```go
transport = &mcp.CommandTransport{Command: exec.Command(args[0], args[1:]...)}
// or: transport = &mcp.StreamableClientTransport{Endpoint: *endpoint}
client := mcp.NewClient(&mcp.Implementation{Name: "mcp-client", Version: "v1.0.0"}, nil)
cs, err := client.Connect(ctx, transport, nil)
...
for tool, err := range cs.Tools(ctx, nil) { ... }       // auto-paginating iterator
cs.CallTool(ctx, &mcp.CallToolParams{...})
```

- **`CommandTransport` wraps an already-built `*exec.Cmd`**, the same
  spawn-your-own-subprocess shape this project's own `cmd/acp-client`
  (immediately prior plan) already uses for its `-agent` flag — a client
  built on this SDK would reuse that same pattern for an `mcp-server`-style
  command config rather than inventing a second one.
- **Era handling is internal to the SDK, not exposed as a decision the
  caller makes.** `mcp/client.go:414`'s `discover` method and
  `mcp/client.go:513`'s `usesNewProtocol()` show the `Client` type
  negotiating modern-vs-legacy itself; `ClientSession.InitializeResult()`
  (`client.go:508`) is populated either way. A design adopting this SDK
  inherits dual-era support essentially for free; a hand-rolled client would
  have to implement the entire `server/discover` probe-and-fall-back
  procedure (`stdio.mdx:121-154`) itself.
- **`Tools`/`Resources`/`ResourceTemplates`/`Prompts` are `iter.Seq2[T,
  error]`** (`client.go:1551,1564,1577,1590`) that drain `tools/list`'s
  cursor pagination internally — a design adopting this SDK does not need
  to implement cursor-walking itself; a hand-rolled one would (see Maka's
  own hand-rolled `discoverMcpTools`, below, for what that looks like when
  a project chooses not to delegate it).
- **`AddRoots`/`RemoveRoots`, `createMessage` (sampling), and `elicit`**
  (`client.go:662,680,737,864`) are the client-side capabilities (`roots`,
  `sampling`, `elicitation`) the spec's `basic/index.mdx` overview lists
  under "Client Features" — all present in this SDK's `Client`, all
  independently something a design can decline the same way this project's
  own ACP-native client already declines `fs`/`terminal` (immediately
  prior plan): the SDK does not force a client to implement more than the
  capabilities it advertises.

## Per-project findings

### openai/codex — MCP tool calls share the *same* approval kind as builtin tool suggestions

- **Adopts the official SDK rather than hand-rolling.**
  `codex-rs/Cargo.toml:406`: `rmcp = { version = "=3.1.3", default-features
  = false }`, pinned to an exact version (not a range) — the same discipline
  this project already applies to its own dependencies. `codex-rmcp-client`
  (`codex-rs/rmcp-client/Cargo.toml`) is a real crate built on top of it,
  not a passthrough: it adds its own OAuth flow (`oauth.rs`,
  `oauth_client_registration.rs`, `oauth_callback.rs`), retry
  (`streamable_http_retry.rs`), a `stdio_server_launcher.rs`, and a
  `local_stdio_transport.rs` distinct from a remote one — the SDK owns wire
  mechanics; Codex owns the operational layer around it (auth, retry,
  process lifecycle).
- **MCP tool-call approvals reuse the same discriminator space as Codex's
  own built-in tool-suggestion approvals, not a separate mechanism.**
  `codex-rs/protocol/src/mcp_approval_meta.rs` defines
  `APPROVAL_KIND_MCP_TOOL_CALL` beside `APPROVAL_KIND_TOOL_SUGGESTION` under
  one shared `APPROVAL_KIND_KEY`, alongside shared `PERSIST_KEY`
  (`session`/`always`) and `SOURCE_KEY`/`CONNECTOR_ID_KEY` fields an MCP
  approval populates that a builtin approval leaves empty. This is the
  single clearest piece of evidence, across every project this gate
  checked, for the charter's own "unified... pipeline" instinct actually
  holding up in a shipped implementation: Codex did not build MCP tool
  approval as a parallel system.
- **A separate, admin-level allowlist governs which MCP servers may even be
  configured, independent of per-call approval.**
  `codex-rs/protocol/src/mcp_policy.rs` defines `EnvironmentMcpPolicy` —
  an environment owner can require named MCP servers to match an exact
  command/URL identity or a matcher (`Exact`/`Prefix`/`Regex`) before Codex
  will use them at all. This is a second, distinct consent layer above the
  per-tool-call approval question: *which servers are allowed to be wired
  in* versus *is this specific call from an already-wired server allowed to
  run*. This project's own composition root has no equivalent for its
  builtins today (there is exactly one `localexec`/`workspacefs` pair,
  named once), so this is a genuinely new question an MCP design introduces
  rather than one this gate can resolve by precedent alone.

### MoonshotAI/kimi-code — MCP server config is threaded through ACP itself, not owned by a harness-internal port

- `packages/acp-adapter/src/mcp.ts:1-26` translates an inbound ACP
  `session/new`'s `McpServer[]` array (the ACP schema's own
  client-to-agent field for naming MCP servers per session, discriminated
  by `type: 'http' | 'sse' | 'acp' | 'stdio'`) into kimi's own kernel
  `Record<string, McpServerConfig>` shape. kimi does not implement its own
  MCP wire client visible in this search — its MCP support is entirely
  "accept what the ACP client already told me to use," relying on its own
  `agent-core`'s `loadMcpServers`/kernel layer (not examined further; out of
  this gate's `packages/acp-adapter` focus) to actually speak MCP.
  `type: 'acp'` transport is explicitly warn-and-dropped as unsupported
  (`mcp.ts:14-16`).
- This is direct, working confirmation of the fact this gate's own
  "What this project already has" section surfaced independently from this
  project's own code: ACP's `session/new`/`session/load`/`session/resume`
  schema genuinely does carry a per-session `mcpServers` field a real
  client (kimi's own upstream ACP clients) can and does populate — this
  project's ACP v1 adapter currently refuses it outright rather than
  ignoring it silently, which is a stricter stance than kimi's own
  (kimi *accepts and uses* the field; this project's adapter *rejects any
  non-empty value*).

### xai-org/grok-build — MCP sits beside a dedicated permission-state module, not inside the agent's core loop

- `crates/codegen/xai-grok-mcp` and `crates/common/xai-computer-hub-mcp-adapter`
  are separate crates from `xai-grok-workspace`, which itself has both
  `src/mcp.rs` and `src/permission/state.rs` as sibling files — this gate
  did not read either crate's internals in depth (time-boxed to the
  approval/consent question, already well-evidenced by Codex above), but
  the file adjacency itself is a second, independent data point (after
  Codex) for "permission/approval state and MCP wiring are deliberately
  colocated," not an incidental finding of one project alone.

### maka-agent/maka-agent — a self-contained MCP package with explicit, bounded discovery

- `packages/mcp/src/tool-discovery.ts` imports `Tool` from
  `@modelcontextprotocol/client` (the official TypeScript SDK) rather than
  hand-rolling the wire client, but hand-rolls tool-list pagination on top
  of it: `discoverMcpTools` (`tool-discovery.ts:52+`) walks `tools/list`
  cursor-by-cursor itself against explicit, named bounds —
  `DEFAULT_TOOL_DISCOVERY_LIMITS = { maxPages: 1_000, maxTools: 1_000,
  maxDefinitionBytes: 16 * 1_048_576 }` (`tool-discovery.ts:26-30`) — a
  concrete answer to a question this project's own documentation rule 4
  ("A design names its... resource bounds") would otherwise leave open:
  an MCP server is untrusted external input, and unbounded pagination or
  an oversized tool-schema payload from a malicious or buggy server is a
  real resource-exhaustion vector a design must bound explicitly, the same
  way this project already bounds `MaxListDirEntries`, `MaxArgvItemBytes`,
  etc. for its own builtins.
- `packages/mcp/src/credential-coordinator.ts`, `oauth.ts`, and
  `transport-security.ts` (not read in depth) exist as separate files from
  tool discovery/binding — further evidence that "MCP client" in a mature
  implementation is not one adapter but a small subsystem: transport
  security, credential/OAuth handling, and tool discovery/binding are kept
  as distinct concerns even within one package.

### deepseek-ai/deepseek-harness — the closest structural analog to this project's own `tools` port

- `packages/mcp/mcp-client/src/tools.ts:1-13` names itself explicitly: "Tool
  bridge: discovers MCP tools, registers them on the harness ToolRuntime
  under deterministic server-qualified public names, and handles re-sync
  when the server's tool list changes." This is the same shape this
  project's own `tools.Catalog` + `application.Service.invokeTool` occupies
  (a name-keyed, invocable tool set consumed by one Step loop), read from
  an independent codebase that solved the same MCP-into-existing-tool-
  pipeline problem this project's own design will have to solve.
- **The naming-collision answer this gate's own "What this project already
  has" section flagged as unresolved has a concrete, working precedent
  here.** `tools.ts:5-9`: "every MCP tool has the stable identity
  `(serverName, rawName)`; the model-facing public name is
  `mcp__<serverName>__<rawName>`, normalized to the DeepSeek function-name
  constraints. The raw name is only ever sent on the wire (`tools/call`);
  the public name is never parsed to recover it." This directly answers
  how a `read_file` tool from an external MCP server can coexist in one
  name-unique `Catalog` beside this project's own builtin `read_file` (this
  project's `tools.NewCatalog` already rejects duplicate names outright,
  per "What this project already has" above) — prefix the model-visible
  name with the server identity, and keep the raw MCP name purely as
  wire-level state the model never sees or has to reproduce.
- Also uses the official TypeScript SDK (`@modelcontextprotocol/sdk/client`,
  `tools.ts:16-17`) — the third of three independently-built, two-language
  projects in this comparison set (with Codex's `rmcp` and Maka's
  `@modelcontextprotocol/client`) to adopt an official SDK rather than
  hand-roll the wire protocol, discussed further below.

### earendil-works/pi — no MCP client, checked directly

The only "mcp" hit anywhere in `pi`'s source tree
(`packages/coding-agent/src/utils/tool-result-images.ts:15`) is an
incidental code comment — "extensions, MCP bridges, screenshot tools" — in
a doc comment about tools that produce images, not MCP client code. No
`package.json` in the repository depends on `@modelcontextprotocol/sdk` or
any MCP-named package. This is a genuine negative finding, checked directly
rather than assumed: `pi` does not implement an MCP client at its current
commit, unlike the other five reference agents in this comparison set.

## Cross-cutting synthesis

- **Three independently-built projects, two languages, one answer:
  adopt the official SDK rather than hand-roll the wire protocol.** Codex
  (`rmcp`, Rust, pinned `=3.1.3`), Maka (`@modelcontextprotocol/client`,
  TypeScript), and DeepSeek Harness (`@modelcontextprotocol/sdk`,
  TypeScript) all build their MCP client layer on top of an official SDK
  and spend their own engineering effort on the surrounding operational
  concerns (auth, retry, discovery bounds, tool-naming, approval routing)
  instead. This is the opposite conclusion from the 2026-08-22 ACP v1
  gate's reasoning for the *agent* side ("the framing contract is small
  enough to own") and the still-open question the 2026-08-30 ACP-native-
  client gate left for the *client* side of ACP — but MCP's protocol
  surface (tool/resource/prompt discovery with pagination and caching, two
  transports each with their own dual-era compatibility procedure, OAuth
  for HTTP, sampling/elicitation/roots as client capabilities) is
  substantially larger than ACP's, and the convergence above suggests that
  size difference is exactly why all three chose differently for MCP than
  this project chose for ACP's agent side. A design should treat "adopt
  `modelcontextprotocol/go-sdk`" as the leading candidate, not a foregone
  conclusion equivalent to the ACP precedent.
- **MCP tool-call consent converges toward reusing an existing
  general-purpose approval mechanism, not building a parallel one** — most
  directly evidenced by Codex sharing one `APPROVAL_KIND_KEY` discriminator
  space between MCP tool calls and its own builtin tool suggestions, and
  independently suggested by Grok Build colocating `mcp.rs` beside
  `permission/state.rs`. This directly supports the charter's own stated
  intent and this project's own existing `tools.Approver`/`Policy.Decide`
  machinery as the right reuse target, rather than motivating a second,
  MCP-specific approval path.
- **A second, distinct consent layer — which MCP servers may be configured
  at all, independent of per-call approval — is real and unaddressed by
  this project's own composition root today.** Codex's
  `EnvironmentMcpPolicy` is the only place this gate found this modeled
  explicitly; this project's `composition.Open` names exactly one
  `localexec`/`workspacefs` pair once, with nothing analogous to "which
  MCP server configurations are this deployment allowed to wire in."
- **External tool names need a disambiguation convention before they can
  enter one name-unique `Catalog`**, and DeepSeek Harness's
  `mcp__<server>__<tool>` scheme is a concrete, working answer to exactly
  the collision this gate's own reading of `tools.NewCatalog` predicts.
- **MCP's own "modern" (2026-07-28) and "legacy" (2025-11-25 and earlier)
  eras are a live compatibility question, not a settled one** — none of the
  six reference agent projects' MCP client code was read closely enough in
  this gate to confirm which era(s) they target (a gap named explicitly
  below), but the specification itself treats this as consequential enough
  to define a formal compatibility matrix and a mandatory probe procedure.
  A design cannot assume "implement `initialize`" is sufficient, nor that
  every real-world MCP server already speaks the stateless modern era.
- **This project's own ACP v1 adapter already has a fail-closed placeholder
  for exactly this feature.** `session/load` and `session/resume` already
  parse and unconditionally reject a non-empty `mcpServers` field; `kimi-
  code`'s own handling of the same ACP field (accept and wire in) is
  concrete evidence of what a real ACP client (this project's own future
  consumers, or any Zed-family client) may eventually send here — a design
  question this project's own protocol layer has already taken a
  provisional stance on, not a hypothetical.

## Open questions a design must resolve, not answered by this gate

- **Static, composition-time MCP server configuration versus per-ACP-
  session `mcpServers` (or both).** This project's ACP adapter currently
  fail-closes the latter entirely; kimi-code's own precedent shows a real
  client may want to send it. Resolving this also means deciding whether
  `composition.Open`-time server wiring (mirroring how `localexec`/
  `workspacefs` are named today) is sufficient for a first slice, deferring
  the ACP-level question entirely.
- **Which protocol era(s) to target.** Modern (2026-07-28, stateless,
  per-request `_meta`) versus legacy (2025-11-25 and earlier,
  `initialize`-handshake) versus dual-era, and whether adopting
  `modelcontextprotocol/go-sdk` (which already handles this internally,
  per its `usesNewProtocol()`/`discover` methods) resolves this question by
  construction rather than requiring an explicit design decision at all.
- **Hand-roll versus adopt `modelcontextprotocol/go-sdk` as a pinned
  dependency.** Three independent, differently-motivated projects adopted
  an official SDK for MCP specifically (above); this gate surfaces that
  convergence but does not decide whether it overrides this project's own
  precedent (from the ACP v1 gate) of preferring to own small wire
  contracts.
- **How MCP tools get classified against the existing Policy `Risk`
  enum.** Today's `RiskNetwork` is an unconditional deny in every mode; an
  MCP tool that, say, only reads from an external system is not obviously
  the same risk class as a builtin `exec` mutating the workspace. A design
  must decide whether MCP tools reuse `RiskRead`/`RiskWrite`/`RiskExec`
  per-tool (leaving `RiskNetwork` as a distinct, still-always-denied
  concern), gain a genuinely new Policy table dimension, or something else
  — this gate found no existing precedent in this project's own code for
  how a *dynamically discovered* tool's risk should even be declared, since
  today's four builtins declare theirs as Go source-code constants.
- **How a dynamically discovered tool set reaches the Step loop's
  invocation path**, given `application/pipeline.go`'s `invokeTool` is
  today a closed switch over four fixed names with a single fixed argument
  struct. This is squarely implementation work a design must scope, not
  something this gate's reading of existing reference projects can resolve
  by precedent, since none of them share this project's own specific
  Step-loop architecture.
- **A tool-name disambiguation convention**, if this project adopts one at
  all — DeepSeek Harness's `mcp__<server>__<tool>` is a working precedent
  to weigh, not an adopted answer.
- **An admin-level "which MCP servers may be configured at all" policy**,
  separate from per-call approval — Codex's `EnvironmentMcpPolicy` is the
  only precedent this gate found; this project has no equivalent surface
  for its existing builtins to model it after.
- **Resources and Prompts: in scope for a first slice, or deferred with
  tools alone shipping first?** This gate confirmed both primitives exist
  and are independently capability-gated but did not investigate either in
  depth.
- **Resource bounds on tool discovery and tool-call payloads**, following
  Maka's explicit `maxPages`/`maxTools`/`maxDefinitionBytes` precedent —
  this project's own documentation rule 4 requires a design to name these,
  and no existing bound in this project's own code (builtin tools have
  fixed, compile-time-known counts) currently anticipates an
  externally-supplied, potentially adversarial tool list.
- **Whether MCP server subprocess spawning gets the same OS-level
  confinement (bwrap/cgroup v2 on Linux, Seatbelt/RLIMIT_AS on macOS) this
  project's own `exec` tool now has** (`internal/harness/adapters/
  localexec`) — an MCP server over stdio is, from an OS perspective,
  exactly the kind of untrusted child process that sandboxing work was
  built for, but this gate did not investigate whether any reference
  project applies OS-level confinement to its own spawned MCP server
  processes specifically.

## Evidence limits

- Every citation above traces to a specific pinned commit read in this
  session (table above); no claim is from memory or from a project's
  marketing page.
- This gate does not authorize copying any type name, schema shape, or
  approval-key naming convention verbatim from any reference project —
  only the mechanisms and architectural choices they represent, per the
  same rule every prior gate in this project states for its own comparison
  set.
- Reference-project MCP client code was read for placement, approval
  routing, naming, and dependency choices specifically; this gate did not
  audit any of the six projects' MCP implementations for correctness,
  security, or protocol-conformance, and does not claim any of them are a
  model to copy rather than a data point to weigh.
- Which protocol era(s) (modern/legacy/dual) each of the five reference
  agents with real MCP client code actually targets was not confirmed for
  any of them; this gate found their placement, approval-routing, and
  SDK-adoption choices but did not trace their wire-level version
  negotiation.
- `kimi-code`'s own MCP wire client (as opposed to its ACP-to-kernel config
  translation, which this gate did read) was not located or examined; it
  may live in a kernel package (`@moonshot-ai/agent-core`) outside
  `packages/acp-adapter`, this gate's search scope.
- `grok-build`'s `xai-grok-mcp` and `xai-computer-hub-mcp-adapter` crates
  were confirmed to exist and named alongside `permission/state.rs` but
  were not read in depth beyond that adjacency.
- `pi` and `pi-mono` currently share an identical latest commit across two
  differently-named GitHub repositories; this gate observed but did not
  investigate the reason, since it has no bearing on the MCP question this
  gate researched.
- "Current state" here means 2026-08-30. A future gate that revisits any
  of these projects, or the MCP specification itself (which this gate
  found to be under active, non-backward-compatible revision as recently
  as 2026-07-28), must re-fetch and re-read per Documentation rule 7 rather
  than reuse this document's characterization.
- This gate does not choose a design. The next step is a normative design
  for an MCP client adapter, informed by — not dictated by — the findings
  above.
