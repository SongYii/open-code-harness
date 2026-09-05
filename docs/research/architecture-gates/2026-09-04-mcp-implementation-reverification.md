# MCP Client Adapter: Implementation-Time Re-verification

**Status:** Research evidence. Records comparison and verification inputs for
the implementation slice that follows the accepted
[MCP client adapter design](../../superpowers/specs/2026-08-30-mcp-client-adapter-design.md).
It informs that slice; it does not amend the design, and nothing here becomes
a requirement without being adopted into the design or its plan.

**Date:** 2026-09-04

**Why this exists:** the accepted design asks for exactly this. Its §1.1
delegates protocol-era handling to the adopted SDK and its risk table states
the compatibility risk is "re-verified against the SDK's own then-current
release at implementation time, not solved by this project's own code." This
document is that re-verification, plus a re-reading of how the reference
projects actually build their MCP client layer, done by reading the code
rather than by re-reading the 2026-08-30 gate's conclusions.

## Pinned sources

Every claim below was read at these exact revisions.

| Source | Revision | Date |
| --- | --- | --- |
| `modelcontextprotocol/go-sdk` | `21c18c6` | 2026-08-28 |
| MCP specification | `ca4ab30` | 2026-08-28 |
| Codex | `67cc3c318d` | 2026-09-01 |
| Grok Build | `bb7f39d` | 2026-08-31 |
| Kimi Code | `ab565e081` | 2026-09-01 |
| Maka | `afbcabdc7` | 2026-09-01 |
| DeepSeek Harness | `dd6322d6` | 2026-08-31 |
| Pi | `853a80d26` | 2026-08-28 |

The Go SDK is at **the same commit the 2026-08-30 gate pinned**. Nothing in
the SDK has moved since the design was accepted, so the design's §1.1
reasoning is re-verified rather than revised. What has moved is this
repository.

## 1. The design's core justification holds

`mcp/shared.go:51-53` carries three live protocol versions —
`2026-07-28`, `2025-11-25`, `2025-06-18` — with version negotiation
(`shared.go:94`, citing the 2025-11-25 lifecycle spec). The `_meta`-carried
request identity the gate described as the "modern" era is handled at
`shared.go:555,662,682`, and `shared.go:636` marks an older mechanism
deprecated as of `2026-07-28`.

The specification checkout carries five schema revisions —
`2024-11-05`, `2025-03-26`, `2025-06-18`, `2025-11-25`, `2026-07-28`. Five
revisions in under two years is the argument for adopting a maintained SDK,
stated as a countable fact rather than an impression.

Maka shows the same split as literal packaging: its `packages/mcp/package.json`
depends on `@modelcontextprotocol/client` `2.0.0`, `@modelcontextprotocol/sdk`
`1.30.0`, `@modelcontextprotocol/server` `2.0.0`, **and**
`@modelcontextprotocol/server-legacy` `2.0.0` — four MCP packages, one of them
named for the legacy era.

Pi still has no MCP client at all, unchanged from the 2026-08-30 gate.

## 2. The Go SDK leaves process control with the caller

This is the finding that most affects the implementation, and it corrects an
assumption worth stating because it was wrong: that adopting the SDK would
move subprocess creation outside this repository's own
`TestOsExecOnlyInLocalExec` guard, which walks `internal/harness` and sees
only first-party source.

It does not. `mcp/cmd.go:20-26` is the whole transport type:

```go
type CommandTransport struct {
	Command *exec.Cmd
	TerminateDuration time.Duration
}
```

**The caller constructs the `exec.Cmd`.** `Connect` only takes pipes and calls
`Start` (`cmd.go:29-47`). So `cmd.Env`, `cmd.SysProcAttr` (and therefore
`Setpgid`), and binary path resolution all remain this project's own code, and
the `exec.Command` call site stays inside `internal/harness` where the
architecture guard can see it. The existing `BuildChildEnvironment`
(whitelisted child environment, never `os.Environ()`) and `ResolveACPBinary`
(hash-pinned binary) patterns from `internal/harness/eval` apply directly.

The TypeScript SDK inverts this, and DeepSeek Harness records the resulting
compromise in its own source
(`packages/mcp/mcp-client/src/transport.ts`): "The MCP SDK owns the actual
spawn, so this transport shares the scrub definition rather than the spawn
path." Its `StdioClientTransport` receives an env built by
`scrubbedParentEnv()` because it cannot receive the spawn itself. The Go SDK
requires no such compromise, which makes it a better fit for this project than
the reference projects' own SDKs are for theirs.

## 3. The SDK's shutdown ladder is weaker than this project's

`mcp/cmd.go:69-108` implements the specification's stdio shutdown: close
stdin → wait `TerminateDuration` (default 5s) → `SIGTERM` → wait →
`Process.Kill()` → wait → return `unresponsive subprocess`.

Two gaps against this repository's own established practice:

- `Process.Kill()` signals **the process only, not the process group**. An
  MCP server that spawns children of its own can leak them. This project's
  `escalateCancel` deliberately signals the process group, and its own ACP
  executor comment states the reason: `exec.CommandContext`'s ctx-triggered
  kill "can only reach a process's direct children, not the whole process
  group."
- It does not **prove reap**. It returns an error if the process is
  unresponsive, but the caller gets no positive proof of collection. This
  project's ACP restart path refuses to launch a successor until reap is
  proven, and classifies unproven reap as `indeterminate` rather than
  success.

Since §2 leaves `SysProcAttr` with the caller, the implementation can set
`Setpgid` and own the teardown, using `CommandTransport.Close` only as the
first, gentlest rung. This should be an explicit plan task rather than an
assumption that the SDK's ladder is sufficient.

## 4. The SDK's dependency footprint is the real cost

Verified by reading production (non-test) imports only. Importing
`github.com/modelcontextprotocol/go-sdk/mcp` transitively requires:

| Module | Reached via |
| --- | --- |
| `github.com/google/jsonschema-go` | `mcp` directly |
| `github.com/yosida95/uritemplate/v3` | `mcp` directly |
| `github.com/segmentio/encoding` (+ `segmentio/asm` indirect) | `mcp` → `internal/json` |
| `golang.org/x/oauth2` | `mcp` → `auth` → `oauthex` |
| `golang.org/x/sync` | `mcp` directly |
| `golang.org/x/time` | `mcp` directly |

`golang-jwt/jwt/v5`, `google/go-cmp`, and `golang.org/x/tools` appear in the
SDK's own `go.mod` but are **test-only** within it (zero non-test files
reference them), so they do not enter this project's build graph.

Two consequences the plan must handle rather than discover:

- **`golang.org/x/oauth2` arrives even though this design is stdio-only with
  no OAuth.** Package `mcp` imports `auth`, which imports `oauthex`, which
  imports `oauth2`. There is no stdio-only slice of this SDK that avoids it.
  This is not a defect — it is the price of the package boundary — but it
  must be disclosed rather than found later by `go mod tidy`.
- The module graph goes from **4 non-test dependencies to roughly 11**. For a
  repository whose CI runs `go mod tidy -diff` and `govulncheck`, and whose
  `SECURITY.md` advertises a one-dependency posture, this is a material
  change of stance and deserves a deliberate sentence in `SECURITY.md`, not a
  silent `go.mod` diff.

Grok Build shows the failure mode this can produce. Its
`crates/codegen/xai-grok-mcp/Cargo.toml` describes the crate as one that
"**Quarantines** rmcp + reqwest 0.13 (rmcp 2.1 requires reqwest >= 0.13.2
while the rest of the workspace uses reqwest 0.12)" — an SDK dragging a
transitive version conflict, contained by confining it to a single crate.
That containment is structurally the same as this design's own placement rule
(one adapter package, importable only by `composition`), which is
independent corroboration that the placement decision is right.

## 5. Adopting an SDK does not make the adapter small

Measured non-test source in each reference project's MCP client layer:

| Project | Lines | SDK |
| --- | --- | --- |
| Codex (`codex-rs/rmcp-client`) | **16,175** (21,170 with tests) | `rmcp` |
| Grok Build (`crates/codegen/xai-grok-mcp`) | 8,897 | `rmcp` 2.1 |
| Maka (`packages/mcp`) | 4,536 | four `@modelcontextprotocol/*` packages |
| Kimi Code (`agent-core-v2/src/mcpCore`) | 1,550 | `@modelcontextprotocol/sdk` ^1.29 |
| DeepSeek Harness (`packages/mcp/mcp-client`) | **1,179** | `@modelcontextprotocol/sdk` ^1.12 |

A 14x spread across projects that all made the same adopt-the-SDK decision.
The spread is scope, not capability. Codex's tree carries OAuth, an EMA
identity/auth policy, Streamable HTTP with redirect and retry handling,
elicitation, an in-process transport, an executor-process transport, and
protocol-mode selection. DeepSeek's carries connection, transport selection,
tool registration, and an invariant helper — five files.

This design is stdio-only with static composition-time configuration and no
OAuth, which places the expected implementation near DeepSeek's end: **order
1,000 lines, not 16,000**. That estimate should be stated in the plan so that
a slice trending toward Codex's size is recognized as scope creep rather than
thoroughness.

## 6. Two directly reusable mechanisms

**Tool name qualification (Kimi Code,
`agent-core-v2/src/mcpCore/tool-naming.ts`, 26 lines).** The design's §5
already adopts the `mcp__<server>__<rawName>` prefix, citing DeepSeek
Harness's precedent, and already states that `tools.Catalog` remains
name-unique and immutable. What it does not specify is what happens when the
raw name will not fit that catalog — and Kimi's implementation supplies
exactly the three missing rules:

- every part sanitized to `[a-zA-Z0-9_-]`, with runs of `_` collapsed;
- a 64-character cap on the qualified name;
- on overflow, truncation plus a stable 8-hex-digit FNV-1a suffix, rather
  than a silent clip that could collide.

This is not a cosmetic gap. The design constrains the **configured** server
`Name` ("must be a valid Catalog name component"), but `rawName` arrives from
the MCP server, which this design's own threat model treats as untrusted. A
server can therefore offer a tool whose name contains characters the Catalog
forbids, or one long enough to overflow any downstream limit, or two tools
whose names differ only past a truncation point. Registration must be total
over whatever the server sends: sanitize, cap, and disambiguate
deterministically, or reject the tool by a stated rule — never register
something that breaks the Catalog's own uniqueness invariant. The plan needs
one of those two answers written down.

**Transport trust provenance (Maka,
`packages/mcp/src/transport-security.ts`).** Its header states the rule
plainly: a remotely supplied destination — a redirect `Location`, an OAuth
metadata URL, an authorization endpoint — must not inherit a loopback or
private-range exception unless the user's own configuration pointed the
server there. Otherwise a server's metadata naming
`https://169.254.169.254/…` or `https://192.168.1.1/…` produces blind SSRF
from inside the user's network.

`169.254.169.254` is the same cloud metadata address this project's own
provider design already names as one the adapter must never be allowed to
reach. That is the strongest available evidence for the design's §1.2
decision to defer Streamable HTTP: accepting remote transport would require
rebuilding a defense this repository has already reasoned about once, in a
second place, against server-controlled input. The deferral is a scope
decision with a security dividend, not an omission.

## 7. Three places where the design meets code that does not match it

None of these are reasons to change the design's decisions. All three are
things the implementation plan must resolve, found by reading the code the
design refers to.

### 7.1 §5's collision claim is false, and the gap is attacker-reachable

Design §5 states that a name collision "can only happen via a `server.Name`
collision, since raw tool names are always prefixed."

`tools.validateSpec` (`catalog.go:133-136`) accepts any name that is
non-empty, valid UTF-8, and not whitespace-padded. Nothing forbids `__`
anywhere. So:

| Server `Name` | Raw tool name | Qualified name |
| --- | --- | --- |
| `a` | `b__c` | `mcp__a__b__c` |
| `a__b` | `c` | `mcp__a__b__c` |

Two distinct servers with **distinct** names produce an identical qualified
name. The prefix is not injective, because the separator can appear inside
either part.

This matters beyond tidiness. Raw tool names come from the MCP server, which
this design's own threat model treats as untrusted, while `NewCatalog` fails
closed on a duplicate — at `composition.Open`, before the harness starts. So
one misbehaving or hostile server can choose a tool name that collides with a
*different* configured server's tool and prevent the harness from starting at
all. A startup denial of service is a low-severity outcome, but it is
reachable by external input, and the design currently reasons as though it
were not possible.

The `sanitizeMcpNamePart` + length-cap + stable-suffix approach in §6 closes
this if — and only if — the separator is removed from both parts before
joining. That is a rule the plan must state explicitly rather than inherit by
accident.

Related, and cheap to fix at the same time: `validateSpec` blocks leading and
trailing whitespace but not an *interior* newline, so a server-supplied name
can still contain control characters that corrupt a log line or a rendered
prompt. Sanitizing to `[a-zA-Z0-9_-]` disposes of this too.

### 7.2 §6's confinement reuse has no API to reuse

Design §6 requires each MCP server to be spawned "through the same bwrap +
cgroup v2 (Linux) or Seatbelt + `RLIMIT_AS` (macOS) confinement `localexec`
already applies … rather than duplicating it," and correctly notes the
lifetime differs since an MCP server outlives one call.

`localexec`'s only execution entry point is
`Runner.Run(ctx, spec) (tools.CommandResult, error)`
(`runner.go:133`) — run to completion, output captured into a capped buffer
(`runner.go:177-179`), with the temporary directory (`defer os.RemoveAll`,
`:158`) and cgroup registration (`defer runner.cgroup.unregister`, `:189`)
both scoped to the call. **There is no API that yields a long-lived process
with stdin/stdout pipes**, which is precisely what an MCP stdio transport
needs.

The reusable machinery is real and well-factored — `bwrapArgv(...)` and
`seatbeltCommandArgv(...)` are pure argv transformations (`runner.go:165,167`),
and `cgroup.addProcess`/`register`/`unregister` take a bare pid — so a seam
is available. But it has to be **built**, as a deliberate task with its own
tests, not assumed present. The macOS `beginRlimitBracket` (`runner.go:181`)
also needs thought: it is a mutex-guarded, process-wide limit held only
around `Start`, which happens to suit a long-lived child, but that is worth
confirming rather than inheriting silently.

### 7.3 §3 and §6 contradict each other

§3: `internal/harness/adapters/mcp` "may not import any sibling adapter",
enforced by the existing `TestForbiddenImport` table.

§6: the mcp adapter reuses `localexec`'s confinement, availability check, and
fail-closed startup gate.

`localexec` is a sibling adapter. As written, satisfying §6 breaks §3, and
the existing architecture test would catch it mechanically on the first
build.

The resolution consistent with this project's own ports-and-adapters style is
for `composition` — the one package permitted to import both — to construct
the confined command through `localexec` and inject it into the mcp adapter
behind a small port the mcp adapter owns (a command factory, in the same
spirit as every other port here). The alternative, extracting confinement
into a shared non-adapter package, is a larger change to a security-critical
component that currently has a single caller. The plan should take the first
and say so; either way, this is a real fork in the road that the design left
open, not an implementation detail.

## 8. Stale statements this slice must correct

Both were found by checking the repository rather than trusting its prose.

- **`SECURITY.md:146`** — "The only non-test module dependency is
  `modernc.org/sqlite`". Untrue today, before any MCP work. Non-test
  dependencies now number four: `modernc.org/sqlite` (3 non-test files),
  `golang.org/x/sys` (4), `golang.org/x/term` (1), and
  `github.com/coder/websocket` (1, `internal/client/acpweb/server.go`).
  `github.com/chromedp/chromedp` is genuinely test-only (0 non-test files).
- **The design's own §3** says the SDK would be "this project's second
  non-test module dependency (after `modernc.org/sqlite`)". That was accurate
  on 2026-08-30 and is not now: the web trajectory UI slice added
  `github.com/coder/websocket` afterwards. It would be roughly the eleventh
  counting the SDK's own transitive set from §4. The design already
  instructed whichever slice implements it to correct `SECURITY.md`; that
  instruction now covers the design's own sentence too.

## 9. What this re-verification does not change

Every architecture decision in the accepted design stands. Adopt the SDK;
stdio only; static composition-time configuration; the existing fail-closed
`mcpServers` rejection in the ACP adapter unchanged; one adapter package that
may not import sibling adapters and that only `composition` may import; an
exact version pin.

The prerequisite in the design's §2 — that implementation waits for "a
concrete external-tool need" — is a project decision, not a technical one,
and is not resolved by this document.

## 10. Questions for the implementation plan

1. **Process-group ownership.** Does the adapter set `Setpgid` and own the
   full teardown ladder (§3), or accept `CommandTransport.Close`'s
   process-only kill for a first slice and disclose the leak? The former is
   consistent with this repository's existing practice; the latter is
   smaller.
2. **Where the `oauth2` dependency is disclosed.** `SECURITY.md`, the
   adapter's own contract document, or both — and does an unused-auth
   dependency need an explicit note that no OAuth code path is reachable in
   this slice?
3. **Untrusted raw tool names.** Sanitize-cap-and-hash (§6), or reject a
   non-conforming tool by a stated rule? And does qualification live in the
   MCP adapter, or in the `tools` port where any future external source would
   need it equally?
4. **Failure of a configured server at composition time.** The design makes
   configuration static and composition-time; does one unreachable MCP server
   fail `composition.Open` closed, matching the exec-sandboxing precedent, or
   degrade to a named, logged absence?
