# MCP Client Adapter — Design

- **Date:** 2026-08-30
- **Status:** Accepted 2026-08-30. The human reviewer asked whether
  implementation is actually necessary if nothing else in this project
  currently requires it. It is not, today: every implemented contract
  (Tool runtime, Composition root, ACP v1 adapter) already works
  completely without an MCP client, and nothing in the milestone order
  (`docs/README.md`) is blocked on it. This design is accepted as a
  normative architecture decision — worth writing now, while the gate's
  research is fresh, so the placement, risk classification, and
  approval-routing questions are settled by record rather than
  re-litigated later — but no implementation plan follows it in this
  cycle. This mirrors existing precedent: milestone 7 (TUI client) and
  the Context Engine boundary itself are both accepted without a
  committed implementation. `tools.SourceMCP` remains, after this design,
  a catalog-legal value with no adapter behind it until a concrete
  external-tool need justifies the implementation cost §2 estimates.
- **Amended 2026-09-04 — implementation authorized.** The prerequisite in
  the paragraph above is lifted by project decision, not because a
  specific external tool arrived; the decision to build was taken
  directly. That paragraph is kept as the state at the time rather than
  rewritten. An implementation plan now follows this design.
  Four contracts below are amended in the same pass, each because the
  [implementation-time
  re-verification](../../research/architecture-gates/2026-09-04-mcp-implementation-reverification.md)
  found repository facts that did not match them: §3's dependency count
  and its sibling-import rule versus §6, §5's collision claim, and §6's
  assumption that `localexec` already exposes a reusable API. Every
  amendment is marked in place.
- **Stability:** new surface; adds no code and changes no existing
  `experimental`/pre-GA contract
- **Repository:** `open-code-harness` (`github.com/SongYii/open-code-harness`)
- **Normative language:** English
- **Chinese summary:** [MCP 客户端适配器设计（中文摘要）](2026-08-30-mcp-client-adapter-design.zh-CN.md)
- **Authority:** [MCP client adapter architecture gate](../../research/architecture-gates/2026-08-30-mcp-client-adapter.md); [Foundational architecture](2026-08-11-open-code-harness-architecture-design.md) line 20 ("外部工具和资源优先通过 MCP 接入；内置工具与 MCP 工具进入统一的工具执行、权限和审计管线")
- **Implemented contracts this design must not change:** [Tool runtime](../../architecture/tool-runtime.md), [Composition root](../../architecture/composition-root.md), [ACP v1 adapter](../../architecture/acp-v1.md) — all four builtin tools, the existing Policy table's behavior for `RiskRead`/`RiskWrite`/`RiskExec`/`RiskNetwork`, and the ACP adapter's current fail-closed rejection of `mcpServers` are unchanged by this design

English is normative. The Chinese file is a synchronized summary, not a
field-for-field translation.

---

## 1. Decision summary

This project's tool runtime has always had a placeholder for this moment:
`tools.SourceMCP = "mcp"` (`internal/harness/tools/catalog.go:17`) has been
a catalog-legal `ToolSpec.Source` value since the Tool Runtime slice, and
[`tool-runtime.md`](../../architecture/tool-runtime.md) says so directly —
"source `mcp` is catalog-legal for a future adapter to project into the
same type. No MCP client today." This design is that adapter's
specification: it does not touch any code, but it resolves every
placement, risk-classification, dispatch, and approval question the
[architecture gate](../../research/architecture-gates/2026-08-30-mcp-client-adapter.md)
left open, so that whenever this project does implement it, the shape is
already decided rather than re-derived under time pressure.

This is architecturally the **opposite placement** from the ACP-native
client design immediately before it. That client sits outside
`internal/harness/` entirely, as a peer process consuming this project's
own agent. An MCP client adapter is the reverse: it sits **behind**
`internal/harness/tools`'s existing ports, converting external MCP tools
into this project's own `domain.ToolSpec` so they flow through the same
`Policy.Decide` table, `Approver` slot, and audit trail as the four
builtin workspace tools — exactly the charter's stated intent.

Seven architecture decisions, each resolving an open question the gate
left for this design (cross-referenced in §11):

1. **Adopt `modelcontextprotocol/go-sdk` as a pinned dependency; do not
   hand-roll the wire protocol.** This reverses the ACP-side precedent
   ("the framing contract is small enough to own," 2026-08-22 gate) for a
   specific, stated reason: three independently-built reference clients
   (Codex/`rmcp`, Maka/`@modelcontextprotocol/client`, DeepSeek
   Harness/`@modelcontextprotocol/sdk`) converged on adopting an official
   SDK rather than reimplementing the protocol, and the gate's own reading
   of the 2026-07-28 specification found a live, backward-incompatible
   "modern" (stateless, per-request `_meta`) versus "legacy"
   (`initialize`-handshake) split that a hand-rolled client would have to
   track and re-verify every time the specification moves — unlike ACP's
   comparatively small, stable NDJSON handshake, this is a moving target
   an official, maintained SDK is built to absorb.
2. **stdio transport only; Streamable HTTP is out of scope for this
   design.** A locally-spawned MCP server subprocess is the same OS-level
   threat model this project's `exec` tool already has real confinement
   for (§6); a remote HTTP server adds OAuth, TLS, and origin-validation
   concerns unrelated to the core goal (unifying external tools into the
   existing pipeline) and is deferred rather than solved speculatively.
3. **Static, composition-time server configuration; the ACP adapter's
   existing fail-closed rejection of `mcpServers` is unchanged.**
   `internal/harness/adapters/acp/{protocol,server}.go` already parses and
   rejects a non-empty `mcpServers` on `session/load` and `session/resume`
   today. This design does not touch that. MCP servers are named the same
   way `localexec`/`workspacefs` are configured today: at
   `composition.Open`, by an operator-controlled config, not per ACP
   session.
4. **Every discovered MCP tool is classified `domain.RiskExec` — never a
   new risk class, never a hint-derived classification.** The
   specification itself says a tool's own `readOnlyHint`/`destructiveHint`
   annotations "MUST be treated as untrusted unless it comes from a
   trusted server" (gate, §"protocol itself"). Since this project cannot
   verify server trust in the general case, every MCP tool is classified
   as conservatively as the existing `exec` tool: denied under
   `ModeReadOnly`/`ModeDenyAll`, always `require_approval` under
   `ModeDefault`/`ModeAllowWrites` (`policy/engine.go`'s existing table,
   unchanged). `RiskNetwork` — which the existing table denies
   unconditionally in every mode — remains reserved for a possible future
   *built-in* networked tool and is not reused for MCP; an MCP tool is not
   "network access" in this project's own risk vocabulary, it is
   "untrusted external code execution," which is exactly what `RiskExec`
   already means.
5. **A tool name collision is a namespacing problem, solved by a prefix,
   not a Catalog change.** Every discovered tool is registered as
   `mcp__<server>__<rawName>` (DeepSeek Harness's own precedent, gate
   §"deepseek-ai/deepseek-harness"). `tools.Catalog` remains
   name-unique and immutable, unchanged; the prefix is this design's
   answer to that existing constraint, not a proposal to relax it.
6. **MCP tool-call arguments bypass the fixed `toolArgs` struct and the
   filesystem-scoping path entirely; they are opaque, schema-validated
   JSON forwarded verbatim.** `tools.ValidateArgs(spec, call.Arguments)`
   (`pipeline.go:52`) already validates any tool's raw arguments against
   its own compiled `InputSchema` before `parseToolArgs`'s fixed-field
   decode runs — this validation is already source-agnostic today, for
   free. An MCP-sourced spec skips `parseToolArgs`'s builtin-only decode
   and the `scopePath`/`Resolve`-driven workspace-containment check
   (§7): an external MCP server is not a location inside this project's
   own workspace filesystem, so "in workspace" does not apply to it the
   way it applies to `read_file`/`write_file`/`list_dir`/`exec`.
7. **Approval routing is entirely unchanged: no new approval subsystem.**
   An MCP tool call reaches `policy.Decide` and, when required, the
   existing `Approver`/`tools.Slot` RPC exactly like `exec` does today.
   The gate's strongest cross-cutting finding — Codex's MCP tool calls and
   its own builtin-tool suggestions share one approval-kind key space,
   not two — is the direct precedent for this.

## 2. Goals and non-goals

### Goals

- Specify the first real adapter behind `tools.SourceMCP`, so that if and
  when implementation is justified by a concrete external-tool need, the
  placement, risk classification, naming, dispatch, and approval-routing
  decisions do not need to be re-derived.
- Keep every existing implemented contract's behavior for the four
  builtin tools completely unchanged: this design adds a new dispatch
  branch and a new catalog source, not a new mode of any existing one.
- Answer, explicitly, whether implementation is worth doing now (§ Status
  line): it is not, and this design does not pretend otherwise by
  producing an implementation plan alongside it.

### Non-goals (excluded from this design, not deferred without a reason)

- **No implementation plan.** Per the Status line: nothing depends on
  this today. A plan is the next artifact only once a concrete external
  tool/server need exists to implement against and verify with, matching
  how milestone 7 and the Context Engine boundary itself remain accepted
  without a committed build.
- **No Streamable HTTP / remote transport** (§1.2): stdio-spawned servers
  only. A remote-transport design is a separate, later decision — it adds
  OAuth, TLS, and origin-validation concerns this design does not need to
  solve to unify local MCP servers into the existing pipeline.
- **No per-ACP-session `mcpServers`** (§1.3): the ACP adapter's existing
  fail-closed rejection is unchanged. Accepting a client-supplied server
  list would mean mutating a Catalog this project has always treated as
  built once, immutably, at `composition.Open` — a materially bigger,
  separate design question, not a corollary of this one.
- **No Resources or Prompts primitives.** The specification defines both
  independently of Tools; this design scopes to tool discovery and
  invocation only. Either could be a later, separate design once a
  concrete use for either exists.
- **No client-side capabilities beyond the bare minimum to call tools**
  (`sampling`, `elicitation`, `roots`): declined at `initialize`, the same
  posture the ACP-native client design already took toward `fs`/
  `terminal` for the same reason — this project's own harness already
  owns the workspace and does not need a second implementation of
  filesystem or execution proxying through a different protocol.
- **No dynamic catalog refresh.** `notifications/tools/list_changed` is
  not handled; the catalog remains built once at `composition.Open`,
  exactly like today's four builtin tools. A server's tool set changing
  after startup requires a restart, matching this project's existing
  "the catalog is immutable for a process's lifetime" behavior — not a
  regression this design introduces, a property it already has.
- **No manual protocol-era selection.** Whether a configured server
  speaks the specification's "modern" (stateless) or "legacy"
  (`initialize`-handshake) era is left entirely to the adopted SDK
  (§1.1); this project does not implement or choose between them.

## 3. Package and process shape

New adapter package, following the existing `internal/harness/adapters/*`
convention:

```
internal/harness/adapters/mcp/   # new adapter: discovery, dispatch, sandboxed spawn
```

Per `internal/harness/architecture`'s existing dependency-boundary rules
(`dependencies_test.go`'s `TestForbiddenImport` table), a new adapter
needs its own owner entry and forbidden-import rows the same way
`localexec`/`workspacefs`/`sqlite`/`acp` already have — `internal/harness/adapters/mcp`
may not import any sibling adapter, and only `internal/harness/composition`
may import `internal/harness/adapters/mcp`, exactly the rule every existing
adapter already obeys. This is a mechanical extension of an existing test
table, not a new architectural mechanism.

> **Amendment, 2026-09-04 — this rule and §6 contradicted each other.**
> §6 requires the mcp adapter to reuse `localexec`'s confinement.
> `localexec` is a sibling adapter, so satisfying §6 as written breaks the
> rule above, and `TestForbiddenImport` would catch it on the first build.
>
> Resolution: **the mcp adapter never imports `localexec`.** It owns a
> narrow port — a confined-command factory returning a configured but
> unstarted `*exec.Cmd` — and `composition`, the one package permitted to
> import both, supplies the `localexec`-backed implementation. This keeps
> the import rule exactly as written, keeps confinement owned by the
> adapter that already implements it, and matches how every other
> capability crosses a boundary in this project. The alternative —
> extracting confinement into a shared non-adapter package — is a larger
> change to a security-critical component with a single caller today, and
> is rejected for this slice.

`internal/harness/adapters/mcp` adds `modelcontextprotocol/go-sdk` as this
project's second non-test module dependency (after `modernc.org/sqlite`,
per `SECURITY.md`'s dependency statement — itself already slightly stale,
since `golang.org/x/term` and `golang.org/x/sys` were added as direct
dependencies by the ACP-native client work; whichever slice implements
this design should also correct that statement). The SDK is pinned to an
exact version (not a range), matching this project's own `modernc.org/sqlite
v1.56.0` precedent and Codex's own `rmcp = "=3.1.3"` exact pin (gate,
§"openai/codex").

> **Amendment, 2026-09-04 — the dependency count above is wrong, and the
> real one is larger than a count of one module.** The paragraph is kept
> as written because it was accurate on 2026-08-30; the web trajectory UI
> slice has since added `github.com/coder/websocket`, so non-test
> dependencies already number four (`modernc.org/sqlite`,
> `golang.org/x/sys`, `golang.org/x/term`, `github.com/coder/websocket`;
> `github.com/chromedp/chromedp` is genuinely test-only).
>
> More importantly, adopting the SDK is not one module. Importing
> `github.com/modelcontextprotocol/go-sdk/mcp` transitively requires, on
> production paths only: `github.com/google/jsonschema-go`,
> `github.com/yosida95/uritemplate/v3`, `github.com/segmentio/encoding`
> (plus `segmentio/asm`), `golang.org/x/sync`, `golang.org/x/time`, and
> **`golang.org/x/oauth2`** — the last reached via `mcp` → `auth` →
> `oauthex`, and therefore unavoidable even though this design is
> stdio-only and reaches no OAuth code path. The graph goes from four
> non-test dependencies to roughly eleven. (`golang-jwt/jwt/v5`,
> `google/go-cmp`, and `golang.org/x/tools` appear in the SDK's own
> `go.mod` but are test-only within it and stay out of this build.)
>
> The implementing slice must rewrite `SECURITY.md`'s dependency
> statement to describe this posture honestly, including the unreachable
> OAuth dependency, rather than leaving a `go.mod` diff to speak for it.

## 4. Server configuration and admission

A new, static configuration list, read at `composition.Open`, one entry
per MCP server:

```go
type ServerConfig struct {
    Name    string   // used in the mcp__<name>__ prefix (§5); must be a valid Catalog name component
    Command string   // argv[0] for the stdio subprocess
    Args    []string
}
```

This is the same shape and lifecycle as `localexec.New(config.WorkspaceRoot)`
and `workspacefs.New(config.WorkspaceRoot)` (`composition/assembly.go:133-137`):
a value read once from `composition.Config`, not a runtime-mutable
resource. The configuration list **is** this project's admission control
for "which MCP servers may exist at all" — mirroring Codex's own
`EnvironmentMcpPolicy` (gate, §"openai/codex"), which the gate found is a
distinct consent layer from "may this call, on an already-configured
server, run" (§7 covers the latter, unchanged Policy/Approver routing).
Only an operator with configuration or flag access can add a server entry;
there is no separate runtime RPC or approval prompt for "may this server
be used," matching how `localexec`/`workspacefs` themselves have no such
prompt today.

A server that fails discovery (§5) within its bound fails
`composition.Open` entirely — fail-closed, with a named, logged reason,
the same posture `composition.Open`'s existing sandbox-availability gate
already takes for `exec` (`exec-sandboxing-resource-quotas-evidence.md`).
There is no escape-hatch flag analogous to `AllowUnsandboxedExec`: the
correct remedy for a misconfigured or unreachable MCP server is to fix or
remove its configuration entry, not to start with a known-broken tool
silently absent from the catalog.

## 5. Tool discovery and catalog integration

At `composition.Open`, for each configured server, in order:

1. Spawn the stdio subprocess under the same OS-level confinement
   `localexec` already applies to `exec` (§6) — a locally-spawned MCP
   server is exactly the untrusted-subprocess threat model that
   confinement exists for.
2. Run the adopted SDK's own initialize/discovery sequence (§1.1 — this
   project does not implement this step itself).
3. Call `tools/list`, paginating with the SDK's own cursor handling, up to
   a bound: **`MaxMCPToolsPerServer = 256`** (matching this project's own
   `tools.MaxListDirEntries` precedent for "a plain, round bound on an
   externally-supplied list") and **`MaxMCPToolDefinitionBytes = 65536`**
   per tool's combined description and input schema. A server exceeding
   either bound fails discovery for that server (§4's fail-closed rule) —
   an external, potentially-malicious tool listing is exactly the
   resource-exhaustion surface Maka's own numeric bounds exist for (gate,
   §"maka-agent/maka-agent"), and this project's documentation rule 4
   already requires a design to state resource bounds, not leave them
   unbounded because the input is external.
4. Map each surviving tool into a `domain.ToolSpec`:

   | `domain.ToolSpec` field | Value |
   | --- | --- |
   | `Name` | `mcp__<server.Name>__<rawToolName>` (§1.5) |
   | `Description` | passed through verbatim |
   | `InputSchema` | passed through verbatim, then independently compiled by this project's own `tools.compileSchema` (`schema.go:66`) — a schema this project's own compiler rejects drops that one tool, logged, not fatal to the whole server |

> **Amendment, 2026-09-05 — the drop rule would empty every real server;
> validation degrades instead.** Task 1 measured `tools.compileSchema`
> against realistic MCP schemas. Its keyword allowlist is twelve entries
> applied recursively and it admits four type values, so it rejects a
> per-property `description`, `$schema`, `title`, `"type":"number"`,
> `"type":"boolean"`, `anyOf`, `default`, and any object schema omitting
> `additionalProperties: false`. Under the row above, a healthy,
> correctly configured server would be discovered, have **every** tool
> dropped with a log line, and leave the harness running with an empty
> MCP contribution — silent, and shaped exactly like success.
>
> No reference project validates external tool schemas this way. Codex
> stores `input_schema` as an untyped `serde_json::Value`; Kimi Code's
> entire check is "is it a JSON object?"; DeepSeek Harness passes the
> input schema through verbatim and, for the output schema it does
> check, records the rule as "unsupported MCP vocabulary falls back to
> JsonValue"; Maka uses Ajv, a standards-complete validator. Zero of four
> apply an in-house allowlist.
>
> **Amended rule: degrade, never drop.** For a `tools.SourceMCP` spec:
>
> - `InputSchema` holds the server's schema **verbatim**, because the
>   same field is what `openaicompat/model.go:346` sends to the model.
>   Substituting a permissive stand-in would hide the tool's real
>   parameters from the model and make the tool uncallable, so the two
>   uses of this field must be separated by *behavior*, not by content.
> - Registration (`tools.validateSpec`, `catalog.go:155`) requires an
>   MCP spec's schema to be a bounded JSON object, not a compilable one.
> - Per-call validation (`tools.ValidateArgs`, `schema.go:47`) tries
>   `compileSchema` first: if the schema compiles, arguments are checked
>   exactly as strictly as a builtin's; if it does not, the check
>   degrades to requiring the arguments to be a JSON object. The tool
>   stays usable either way.
> - Degradation is **recorded, not silent** — discovery logs which tools
>   fell back, so "this tool's arguments are loosely checked" is an
>   auditable fact rather than an invisible one.
>
> **This narrowly amends the header's "implemented contracts this design
> must not change" list.** `tools` is on that list, and these are the only
> two `compileSchema` call sites in the repository. The builtin path is
> unchanged byte for byte — the branch is keyed on `spec.Source` and
> `SourceBuiltin` behavior must be proven identical by test. The
> alternative, widening `compileSchema` itself, would relax a
> deliberately strict validator for the four builtin tools too, whose
> `additionalProperties: false` exists so model-invented keys cannot
> pass; that is a larger change for no gain here.
>
> **Why the looser check is defensible for MCP and not for builtins.**
> For a builtin, this project writes the schema *and* executes the call,
> so strictness protects its own filesystem and exec paths from arguments
> the model invented. For an MCP tool, the server writes the schema and
> the server executes the call; rejecting bad arguments is its own
> responsibility, and the schema is its own declaration. What protects
> this harness is unchanged and does not depend on schema strictness at
> all: every MCP tool is `RiskExec`, therefore `require_approval` in the
> permissive modes and denied outright in the restrictive ones, and the
> server subprocess runs under `localexec`'s confinement.
   | `Source` | `tools.SourceMCP` |
   | `Risk` | `domain.RiskExec`, always (§1.4) |
   | `Mutates` | `true`, always — required by `validateSpec`'s existing `Risk`/`Mutates` pairing rule (`catalog.go:150-154`), and consistent with treating every MCP tool as conservatively as `exec` |

5. Feed every surviving spec, from every server, into the same
   `tools.NewCatalog(...)` call composition already makes for the four
   builtin specs (`composition/assembly.go:141`) — one catalog, one
   name-uniqueness check, unchanged. A name collision (an operator
   configuring two servers whose prefixed names collide, which can only
   happen via a `server.Name` collision, since raw tool names are always
   prefixed) fails `NewCatalog` exactly as a builtin duplicate would today
   — caught at startup, not at first call.

> **Amendment, 2026-09-04 — the parenthesical above is false, and the gap
> is reachable by an untrusted server.** The prefix is not injective.
> `tools.validateSpec` (`catalog.go:133-136`) accepts any non-empty,
> valid-UTF-8, non-whitespace-padded name, so `__` may appear inside
> either part: server `a` with tool `b__c` and server `a__b` with tool
> `c` both qualify to `mcp__a__b__c`, with distinct server names.
>
> Raw tool names come from the MCP server, which this design's own threat
> model treats as untrusted, and `NewCatalog` fails closed at
> `composition.Open`. So a hostile or buggy server can pick a tool name
> that collides with a *different* configured server's tool and prevent
> the harness from starting. Startup denial of service is low severity,
> but it is externally reachable and the sentence above reasons as though
> it were impossible.
>
> **Amended rule.** Before joining, each part is sanitized to
> **`[a-zA-Z0-9-]`** — ASCII letters, digits, and hyphen, with runs of
> the replacement character collapsed and the result trimmed. Underscore
> is deliberately **excluded** from the part alphabet and reserved as the
> separator; that exclusion is the entire mechanism that makes the join
> injective. The qualified name is capped at 64 bytes, with overflow
> truncated and given a stable 8-hex-digit FNV-1a suffix of the
> untruncated name rather than a silent clip.
>
> Sanitization is itself lossy — `a/b` and `a.b` both reduce to `a-b` —
> so a part that sanitization actually altered additionally carries an
> 8-hex-digit FNV-1a suffix of its own original. A part that was already
> legal passes through untouched, which keeps ordinary tool names legible
> to the model rather than hashing every name unconditionally.
>
> Sanitization also disposes of a smaller problem: `validateSpec` rejects
> leading and trailing whitespace but not an interior newline, so an
> unsanitized server-supplied name could carry control characters into a
> log line or a rendered prompt.
>
> **Second amendment, same day, found by implementing it.** This
> paragraph first specified sanitizing to `[a-zA-Z0-9_-]` with runs of
> `_` collapsed, and claimed that removed the separator from both parts.
> It does not: that alphabet *keeps* the separator, so `a` + `b__c` and
> `a__b` + `c` still both qualify to the same name. Task 1's own
> injectivity test failed against the rule as first written, and a
> mutation restoring that alphabet turns the test red again. The rule
> above is the corrected one. Kimi Code's convention is still the source
> for the cap-and-suffix mechanism; its exact alphabet is not, because
> its separator is `__` rather than a single reserved character.
>
> A collision that survives sanitization (two servers genuinely
> configured with the same `Name`, which is the case the original
> sentence meant) still fails `NewCatalog` at startup, unchanged.

## 6. Reusing `exec`'s OS-level confinement for the subprocess

A stdio-transport MCP server is, from the operating system's point of
view, exactly the kind of untrusted, externally-defined subprocess
`localexec`'s sandboxing already exists to confine. This design does not
invent a second sandboxing mechanism: `internal/harness/adapters/mcp`
spawns each configured server through the same bwrap + cgroup v2 (Linux)
or Seatbelt + `RLIMIT_AS` (macOS) confinement `localexec` already applies,
reusing that adapter's existing availability check and fail-closed startup
gate (`exec-sandboxing-resource-quotas-evidence.md`) rather than
duplicating it. An MCP server process is longer-lived than one `exec`
call (it persists for the harness process's lifetime, not one tool
invocation), so the resource-quota lifecycle differs in duration, not in
mechanism — a detail an implementation plan would need to work out against
`localexec`'s actual API, not this design.

> **Amendment, 2026-09-04 — there is no such API to reuse; it must be
> built.** The sentence above correctly deferred this to a plan, but the
> answer is larger than "work out against the actual API": `localexec`
> has no API that fits.
>
> Its only execution entry point is
> `Runner.Run(ctx, spec) (tools.CommandResult, error)` (`runner.go:133`),
> which runs to completion, captures output into a capped buffer
> (`:177-179`), and scopes both the temporary directory (`defer
> os.RemoveAll`, `:158`) and the cgroup registration (`defer
> runner.cgroup.unregister`, `:189`) to that one call. An MCP stdio
> server needs the opposite: a live process with stdin/stdout pipes whose
> confinement, temp directory, and quota membership last for the
> harness's lifetime.
>
> The machinery is reusable — `bwrapArgv(...)` and
> `seatbeltCommandArgv(...)` are pure argv transforms (`:165,167`), and
> `cgroup.addProcess`/`register`/`unregister` take a bare pid — so
> `localexec` gains a second, long-lived entry point beside `Run`,
> returning a configured but unstarted `*exec.Cmd` together with a handle
> owning the temporary directory and quota membership until closed. That
> is its own plan task with its own tests, not a wiring detail. The macOS
> `beginRlimitBracket` (`:181`) is a mutex-guarded, process-wide limit
> held only around `Start`, which suits a long-lived child, but the plan
> must confirm that rather than inherit it silently.
>
> The returned command also carries `Setpgid`, because the SDK's own
> `CommandTransport.Close` ladder signals the process rather than its
> group and proves no reap — both weaker than this repository's existing
> ACP practice. See the re-verification document's §3.
>
> Per §3's amendment, the mcp adapter consumes this through a port
> `composition` fills; it never imports `localexec` itself.

## 7. Invocation dispatch

`internal/harness/application/pipeline.go`'s `invokeTool` — today a closed
four-branch `switch spec.Name` (`pipeline.go:232`) — gains a fifth branch
keyed on `spec.Source == tools.SourceMCP`, not on `spec.Name` (an
MCP-discovered name is operator/server-chosen, not a fixed identifier this
package can enumerate). Ahead of that branch, in `executeOneTool`
(`pipeline.go:48-60`):

- `tools.ValidateArgs(spec, call.Arguments)` (`pipeline.go:52`) already
  runs unconditionally, already against the spec's own compiled
  `InputSchema`, already before `parseToolArgs`'s fixed-field decode —
  this is already source-agnostic today and needs no change for MCP
  arguments to be schema-validated before dispatch.
- `parseToolArgs` (`pipeline.go:55`) and the `scopePath`/`Resolve`-driven
  workspace-containment check that follows it (`pipeline.go:60-97`) are
  **both skipped** for an MCP-sourced spec: `workspaceIn` is treated as
  unconditionally `true` for `tools.SourceMCP`, since an external MCP
  server is not a location inside this project's own workspace filesystem
  the way the four builtin tools' paths are — "in workspace" is not a
  question that applies to it. `policy.Decide` is called with
  `WorkspaceIn: true, Risk: domain.RiskExec` for every MCP call,
  unconditionally.
- The new `invokeTool` branch receives the call's raw, already-validated
  `call.Arguments` JSON directly — a concrete implementation would need to
  either widen `invokeTool`'s signature to receive `call.Arguments`
  alongside the already-decoded `toolArgs`, or carry the raw string
  through `toolArgs` itself (e.g. an added `Raw string` field populated
  unconditionally) — an implementation-shape detail, not a contract this
  design freezes, since no test yet exists to pin one shape over the
  other.
- A new port, following this project's existing `tools.CommandRunner`
  shape:

  ```go
  type MCPCaller interface {
      Call(ctx context.Context, server, rawToolName string, arguments json.RawMessage) (MCPResult, error)
  }

  type MCPResult struct {
      Content string // text content blocks concatenated; non-text blocks (image/audio/resource) rendered as a labeled placeholder, never silently dropped
      IsError bool
  }
  ```

- **Two error channels, mapped onto this project's existing two-channel
  shape** — the specification's own distinction (gate, §"protocol
  itself") maps directly onto `failToolAndContinue` versus a plain
  application error, which `invokeTool`'s existing builtin branches
  already use for exactly this distinction (`CodeExecTimeout` versus a
  returned `error`):
  - `MCPResult.IsError == true` (a tool executed and reported its own
    failure) → `failToolAndContinue` with a new `CodeMCPToolError` /
    `ToolTextMCPToolError`, visible to the model, exactly like
    `CodeExecTimeout` today — the specification's own guidance is that the
    model should see this and may recover.
  - A non-nil `error` from `Call` (a protocol-level failure: connection
    lost, malformed response, an unknown-tool JSON-RPC error) → the
    existing generic `err != nil` path `invokeTool`'s builtin branches
    already return today, unchanged — this project's dispatcher does not
    need a new category for this, since generic tool-invocation errors are
    already handled uniformly regardless of source.

## 8. Approval routing — unchanged

`policy.Decide` receives `Risk: domain.RiskExec, WorkspaceIn: true` for
every MCP call (§7) and applies the existing, unmodified table
(`policy/engine.go:112-145`): denied under `ModeReadOnly`/`ModeDenyAll`,
`require_approval` under `ModeDefault`/`ModeAllowWrites` (the same rule
`exec` already gets in `ModeAllowWrites` — write does not auto-allow
`exec`, and it does not auto-allow MCP calls either). A required approval
goes through the existing `Approver`/`tools.Slot` RPC seam unchanged — no
new approval subsystem, no new wire message, no new prompt shape. This is
the direct application of the gate's strongest cross-cutting finding:
Codex's MCP tool-call approvals and its own builtin-tool-suggestion
approvals share one approval-kind key space, not two independently-built
mechanisms (gate, §"openai/codex").

## 9. Verification and acceptance (for the implementation this design does not commit to yet)

Recorded here so a future implementation plan does not need to re-derive
an acceptance shape, without this design itself committing to writing
that plan:

- Catalog-construction tests: a configured server's discovered tools
  appear with the correct `mcp__<server>__<name>` prefix, `RiskExec`,
  `Mutates: true`; a tool whose schema this project's own compiler rejects
  is dropped, not fatal; a server exceeding either bound in §5 fails
  `composition.Open` with a named reason; two servers whose prefixed names
  collide fail `NewCatalog`.
- Dispatch tests: an MCP-sourced spec skips `parseToolArgs` and the
  workspace-scoping path entirely (§7) and always reaches `policy.Decide`
  with `WorkspaceIn: true`; `IsError: true` results reach
  `failToolAndContinue`; a protocol-level `Call` error reaches the
  existing generic error path.
- Policy-table tests (extending the existing table-driven suite,
  `policy/engine_test.go`): an MCP-shaped `Input{Risk: RiskExec}` is
  denied in `ModeReadOnly`/`ModeDenyAll` and requires approval in
  `ModeDefault`/`ModeAllowWrites` — proving §8 by reusing the existing
  test shape, not writing a parallel one.
- A real, gated integration test spawning an actual (test-fixture) MCP
  server subprocess through the adopted SDK, discovering and calling one
  tool end to end — the same "real subprocess, not only a scripted fake"
  acceptance bar §7 of the ACP-native client design set and met.
- `internal/harness/architecture`'s dependency-boundary test table gains
  the new adapter's owner and forbidden-import rows (§3), proving no
  sibling adapter or `application`/`policy`/`domain` package gained an
  import into `internal/harness/adapters/mcp`.

## 10. Risks

| Risk | Mitigation |
| --- | --- |
| The specification's own "modern" vs. "legacy" era split, or a future revision, breaks compatibility with a configured server. | §1.1: delegated to the adopted SDK, which exists specifically to absorb this; re-verified against the SDK's own then-current release at implementation time, not solved by this project's own code. |
| A malicious or misbehaving MCP server returns an unbounded tool list or oversized tool definitions, exhausting memory at startup. | §5's `MaxMCPToolsPerServer` / `MaxMCPToolDefinitionBytes` bounds and fail-closed discovery. |
| Treating every MCP tool as `RiskExec` is either too strict (blocking legitimate read-only integrations) or, if ever relaxed, too permissive (trusting an untrustworthy server's own hints). | §1.4 accepts the strict side deliberately, citing the specification's own untrusted-hints guidance; loosening this later is a real, separate design decision, not an oversight to silently patch. |
| An MCP server subprocess outlives or escapes its resource confinement, unlike a short-lived `exec` call. | §6 flags the longer-lived-process lifecycle mismatch explicitly as an implementation-time question against `localexec`'s actual quota-tracking API, not resolved by this design. |
| Building this without a concrete external-tool consumer produces speculative code nobody exercises for real. | The Status line's own answer: this design intentionally stops short of an implementation plan for exactly this reason. |

## 11. How this design answers the gate's open questions

Cross-referencing `2026-08-30-mcp-client-adapter.md`'s "This gate does not
answer, left for the design phase" section directly:

1. *Static composition-time config vs. per-ACP-session `mcpServers`* →
   §1.3/§4: static only; the ACP adapter's fail-closed rejection is
   unchanged.
2. *Which protocol era(s) to target* → §1.1: delegated entirely to the
   adopted SDK.
3. *Hand-roll the wire vs. adopt `modelcontextprotocol/go-sdk`* → §1.1:
   adopt, reversing the ACP-side precedent for a stated, protocol-specific
   reason.
4. *How MCP tools fit the Policy `Risk` enum* → §1.4/§8: always
   `RiskExec`, never `RiskNetwork`, never a new dimension.
5. *How a dynamically-discovered tool set reaches the Step loop's
   dispatch* → §7: a fifth `invokeTool` branch keyed on `spec.Source`, a
   new `MCPCaller` port, args bypassing `parseToolArgs`/workspace-scoping
   entirely.
6. *A tool-name disambiguation convention* → §1.5/§5: adopt DeepSeek
   Harness's `mcp__<server>__<tool>` prefix.
7. *A management-level "which servers may be configured" policy* → §4:
   the static configuration list itself is that policy; no separate
   runtime RPC.
8. *Resources and Prompts: in scope for a first slice?* → §2: no, tools
   only.
9. *Resource bounds on discovery and call payloads* → §5: explicit
   `MaxMCPToolsPerServer` and `MaxMCPToolDefinitionBytes` bounds.
10. *OS-level isolation for a spawned MCP server subprocess* → §6: reuse
    `localexec`'s existing bwrap/cgroup v2 (Linux) and Seatbelt/`RLIMIT_AS`
    (macOS) confinement, not a second mechanism.

This design does not choose to implement anything (Status line, §2). The
next step, whenever a concrete external-tool need justifies it, is an
implementation plan against this design — not a re-derivation of the
decisions above.
