# MCP Client Adapter — Implemented Contract

**Status:** Implemented; not GA (see [Maturity and known limits](#maturity-and-known-limits))

**Authority:** [MCP client adapter design](../superpowers/specs/2026-08-30-mcp-client-adapter-design.md), including its 2026-09-04 and 2026-09-05 amendments

**Implemented plan:** [MCP client adapter implementation plan](../superpowers/plans/2026-09-04-mcp-client-adapter.md)

**Completion evidence:** [MCP client adapter evidence ledger](mcp-client-evidence.md)

**Chinese reading copy:** [MCP 客户端适配器 — 已实现合同](mcp-client.zh-CN.md)

**Packages:** `internal/harness/adapters/mcp` (discovery, qualification, invocation, teardown), `internal/harness/adapters/localexec` (the confined long-lived command it consumes through a port), `internal/harness/composition` (configuration, wiring, the command factory, the call router), `internal/harness/application` (dispatch), `internal/harness/tools` (the `ExternalTools` port and source-aware schema validation)

This document records behavior the code and tests enforce today. It is an
internal Go contract, not a stable public protocol, and not a GA guarantee.

## Scope

An MCP server is an external program this harness spawns over stdio and asks
for tools. Every tool it offers is projected into this project's own
`domain.ToolSpec` and registered in **the same** `tools.Catalog` as the four
builtin workspace tools, so it flows through the same `Policy.Decide` table,
the same `Approver` slot, and the same audit trail. There is no second
approval subsystem, no second policy table, and no MCP-specific bypass.

Excluded, deliberately: Streamable HTTP transport, OAuth, per-session server
configuration, server restart after death, and process-group teardown on
Windows. Each exclusion is stated with its reason in
[Maturity and known limits](#maturity-and-known-limits).

## The SDK boundary

The wire protocol is not implemented here. `github.com/modelcontextprotocol/go-sdk`,
pinned to an exact version, owns framing, the initialize handshake, protocol
version negotiation across the four versions it carries, and `tools/list`
cursor pagination.

This reverses the ACP-side precedent, where this project owns its own wire
implementation three times over, and the reversal is deliberate: ACP's
NDJSON framing is small and stable, while the MCP specification has shipped
five schema revisions with a live backward-incompatible split between a
`_meta`-carrying "modern" era and an `initialize`-handshake "legacy" one.
Charter §12.1 states the general rule — research before building, and state
the reason whenever the decision is to own an implementation instead.

## Configuration and admission

`composition.Config.MCPServers` is a static list read once at `Open`. It **is**
the admission control for which servers may exist: there is no per-session
configuration, and the ACP adapter's existing fail-closed rejection of
`mcpServers` on `session/load`/`session/resume` is unchanged.

Empty — the default, and what almost every assembly uses — means no MCP
client is constructed at all and the assembly is exactly what it was before
this capability existed (`TestOpenWithNoMCPServersIsUnchanged`).

`ServerConfig.Validate` rejects an empty or whitespace-padded name and an
empty command before any process exists.

## Confinement, and why the adapter never imports `localexec`

Each server is spawned through the same OS-level confinement `localexec`
applies to the `exec` tool — bwrap plus a cgroup v2 quota on Linux, Seatbelt
plus `RLIMIT_AS` on macOS — with a whitelisted three-name child environment
(`PATH`, `HOME`, `TMPDIR`; never `os.Environ()`) and its own process group.

The design forbids an adapter from importing a sibling adapter while also
requiring this reuse. Those two rules contradicted each other, and the
resolution is a port: `mcp.CommandFactory` and `mcp.Command` are declared by
the consumer, and `composition` — the one package permitted to import both —
supplies the `localexec`-backed implementation. `localexec` owes nothing to
MCP, and a differently-confined provider could be substituted without either
package learning about the other.

`localexec.NewConfinedCommand` returns the command **configured but
unstarted**, because an MCP stdio server's stdin and stdout are the protocol
transport: the SDK's own `CommandTransport` attaches the pipes and calls
`Start`. The handle owns the private temporary directory and quota membership
for the process's lifetime, which `Run`'s one-shot shape scopes to a single
call.

One difference from `Run` is disclosed rather than hidden: `Run` holds the
macOS pre-`Start` `RLIMIT_AS` bracket around its own `cmd.Start`, and here the
caller owns `Start`, so the bracket is exposed as `StartBracket()` and taken
around the SDK's `Connect`.

## Discovery

At `Open`, for each configured server in order: spawn, run the SDK's
handshake, then list tools.

| Bound | Value | Why |
| --- | --- | --- |
| `MaxToolsPerServer` | 256 | Matches `tools.MaxListDirEntries`, this project's existing round bound for an externally supplied list. A server may paginate without end; this is what stops it. |
| `MaxToolDefinitionBytes` | 65536 | One tool's description plus schema. Closes the other exhaustion route: a single tool with an enormous description. |

**Two failure modes are deliberately different.** A breached bound fails the
**whole server**, because admitting an arbitrary prefix of a misbehaving
server's tools would be worse than admitting none. A single unusable tool —
one whose schema cannot be registered — drops **that tool only**, with a
stated reason, because letting it reach `tools.NewCatalog` would fail the
catalog and with it `composition.Open`, handing one malformed tool a denial
of service over the entire harness.

Each surviving tool becomes a `domain.ToolSpec`:

| Field | Value |
| --- | --- |
| `Name` | `mcp_<server>_<tool>`, qualified per [Tool names](#tool-names) |
| `Description` | passed through verbatim |
| `InputSchema` | passed through **verbatim** — the same field is what the Provider adapter sends to the model, so a stand-in would hide the tool's real parameters |
| `Source` | `tools.SourceMCP` |
| `Risk` | `domain.RiskExec`, **always** |
| `Mutates` | `true`, **always** |

The classification is fixed, never derived. The specification requires a
tool's own `readOnlyHint`/`destructiveHint` to be treated as untrusted absent
server trust this project cannot establish, so every MCP tool is classified as
conservatively as builtin `exec`.

`DiscoveryResult` also reports which tools were **dropped and why**, and which
will have their arguments **checked loosely** at call time. The second list
is a contract commitment: degradation is auditable, not invisible.

## Tool names

A server's raw tool name is untrusted input. `QualifyToolName` produces a
Catalog-legal, collision-resistant name:

- each part is sanitized to `[a-zA-Z0-9-]` — ASCII letters, digits, hyphen,
  and **not underscore**, which is reserved as the separator. Excluding the
  separator from the part alphabet is the entire mechanism that makes the join
  injective;
- a part that sanitization **altered** additionally carries an 8-hex-digit
  FNV-1a suffix of its original, because sanitization is lossy (`a/b` and
  `a.b` both reduce to `a-b`). An already-legal name passes through untouched,
  so ordinary tool names stay legible to the model;
- the qualified name is capped at `MaxQualifiedNameBytes` (64), with overflow
  truncated and given a stable suffix hashed over the untruncated name.

Without this, the prefix is not injective: server `a` with tool `b__c` and
server `a__b` with tool `c` would produce the same name. Raw names come from
an untrusted server and `NewCatalog` fails closed at `composition.Open`, so a
hostile server could otherwise choose a name colliding with a **different**
server's tool and stop the harness from starting.

Sanitization also removes control characters, which `tools.validateSpec`
does not reject inside a name.

One residual edge is stated rather than hidden: a legal raw name shaped like
`<something>-<8 hex digits>` occupies the same shape a lossy encoding
produces, and a real collision needs that coincidence plus an FNV-1a
collision. This disambiguates against a startup denial of service; it does not
authenticate.

## Schema validation

`tools.compileSchema` was written for this project's own four builtin tools:
twelve keywords applied recursively, four permitted type values, mandatory
`additionalProperties: false`. Published MCP tools use full JSON Schema, so
requiring it of them rejected a per-property `description`, `"type":"number"`,
`"type":"boolean"`, `$schema`, `title`, `anyOf`, and `default` — in practice
every tool of every healthy server, while startup reported success.

Validation is therefore **source-aware**, and degrades rather than discards:

- registration requires a `SourceMCP` schema to be a JSON object bounded by
  `tools.MaxMCPSchemaBytes` (32 KiB) with no trailing content;
- `ValidateArgs` tries `compileSchema` per call. A server that does publish a
  compilable schema gets exactly the builtin-strength check. When compilation
  fails, the check degrades to requiring one well-formed JSON object with
  nothing trailing it — degraded is not absent.

The builtin path is unchanged and proven so by test, not asserted. Strictness
protects this project's own filesystem and exec paths from arguments the model
invented; an MCP server writes and executes its own tool and owns rejecting
arguments its schema forbids. What guards the harness is independent of schema
strictness: `RiskExec`, the approval gate, and confinement.

## Dispatch

`application.invokeTool` branches on `spec.Source`, not `spec.Name` — an
external tool's name is chosen by the operator and the server, so the closed
four-name switch can never match it. Ahead of that branch two builtin-only
steps are skipped: `parseToolArgs`'s fixed-field decode, and the
`scopePath`/`Resolve` containment path, since an external server is not a
location inside this workspace and "in workspace" is not a question that
applies to it. `WorkspaceIn` is true for every external call.

Skipping containment is not a relaxation: every external tool is `RiskExec`
and mutating, so `ModeReadOnly`/`ModeDenyAll` deny it outright and
`ModeDefault`/`ModeAllowWrites` require approval, exactly as for builtin
`exec`.

Arguments are forwarded verbatim as the model produced them. A tool that
**runs and reports its own failure** becomes a tool failure inside the Turn
(`CodeExternalToolFailed`, carrying the tool's own message, or
`ToolTextExternalFailed` when it said nothing); only a call that **cannot
reach** the tool is an error that ends the Turn. The distinction is
load-bearing: a tool failure is an ordinary event the model can read and react
to, so conflating them would let a routine "file not found" tear down a
session.

Results are bounded by `MaxToolResultBytes` and carry the same
`prefix + \n[truncated]` shape every other truncated tool result does.

The `tools.ExternalTools` port lives beside `FileSystem` and `CommandRunner`
so Application dispatches without importing an adapter.
`catalogPortNeeds` derives the requirement from `Source` rather than `Risk`:
an MCP tool is always `RiskExec` but touches neither workspace port, so
deriving it from `Risk` would demand a command runner the tool never uses.

Routing is by exact qualified name. A name no configured server claims is
refused rather than broadcast or guessed at, which would let one server answer
for another's tool.

## Startup and shutdown

`Open` fails closed on three conditions, each mutation-tested: a server that
cannot be reached, a server that breaches a discovery bound, and two servers
configured with the same name. There is deliberately no
`AllowUnsandboxedExec`-style escape hatch — an operator who configured a
server asked for its tools, and starting without them while reporting success
would make them look absent rather than broken. A failure part-way through
tears down the servers already connected.

`Assembly.Close` stops every connected server **before** shutting the host
down: they are leaves of the assembly, and stopping them first means a slow
server cannot delay the writer's own lease release.

Teardown runs the SDK's own stdio shutdown first — close stdin, wait,
SIGTERM, Kill — then escalates past the two things it does not do:

- its last rung signals the **process alone**, so a server that spawned
  children of its own would leave them orphaned. Escalation signals the
  process group, which the confined command carries a group for;
- it returns without proving collection. Signalling is not reaping.

Proof comes from a clean return of the SDK's close, which means its own
`Wait` returned, and past that from probing with signal 0. Both the group and
the leader must be gone: if a process is ever not a group leader,
`kill(-pid, 0)` addresses a group that may not exist and returns `ESRCH` while
the process is alive, and treating that as proof would report a false success.
`mcp.ErrTeardownUnproven` reports the case where neither establishes it,
rather than assuming success.

Errors that are the expected consequence of deliberately terminating a server
— a broken transport, a signal exit — are not surfaced as faults.

## Maturity and known limits

Implemented, **not GA**. Each of these is a stated boundary, not an oversight:

- **No Streamable HTTP, no OAuth.** stdio only. Accepting remote transport
  would require defending against server-supplied metadata naming
  `https://169.254.169.254/…` or a private-range address — blind SSRF from
  inside the network — which is a defense this repository has already reasoned
  about once for its Provider adapter and would have to rebuild here against
  server-controlled input. `golang.org/x/oauth2` is nonetheless in the build
  graph, reached via `mcp` → `auth` → `oauthex`; no code here calls it.
- **No process-group teardown on Windows.** Process groups and the signals
  addressing them are POSIX. This repository already refuses ACP subprocess
  supervision on Windows rather than approximating a kill-only-the-parent
  substitute, and the same stance applies: a Windows build gets the SDK's
  ladder alone, and a server that spawns children can leave them running.
- **No server restart.** A server that dies mid-session leaves a Catalog entry
  whose process is gone; the call fails. Supervised restart is a larger
  contract with its own identity questions.
- **No per-session configuration.** Servers are named once at `Open`.
- **`MaxToolsPerServer = 256` is inherited, not measured.** It comes from
  `tools.MaxListDirEntries`; nothing has measured a real server against it.
- **Two bounds disagree by design-inheritance, not by decision.** Discovery
  admits a definition up to 64 KiB while registration admits a schema up to
  32 KiB, so a schema between them clears discovery and is dropped at
  registration. The outcome is safe — one tool dropped with a reason, server
  survives — and `TestDefinitionBoundIsWiderThanTheCatalogsSchemaBound` pins
  the asymmetry so a reader does not assume the constants agree.
- **No MCP evaluation suite.** The evaluation system can host one; its absence
  blocks nothing here.
- **Prompt-injection resistance is not proven.** Tool descriptions and results
  come from an untrusted server and reach the model. Redaction applies to tool
  results on the existing Application path, and every MCP tool is
  approval-gated, but no automated test here demonstrates that a real model
  resists an injected instruction.
