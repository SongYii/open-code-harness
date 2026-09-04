# MCP Client Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement milestone 9 — a stdio-transport MCP client adapter behind the existing `tools` ports, so external MCP tools flow through the same Catalog, `Policy.Decide` table, `Approver` slot, and audit trail as the four builtin workspace tools.

**Architecture:** `internal/harness/adapters/mcp` owns discovery, name qualification, and invocation, and imports the official SDK. It never imports a sibling adapter: OS-level confinement arrives through a narrow port that `composition` fills with a `localexec`-backed implementation. `localexec` gains a second, long-lived entry point beside `Run`. `application/pipeline.go` gains one dispatch branch keyed on `spec.Source`. `composition` owns server configuration, discovery ordering, Catalog assembly, and shutdown.

**Tech Stack:** Go 1.26.6; `github.com/modelcontextprotocol/go-sdk` pinned to an exact version; existing `tools`, `domain`, `policy`, `application`, `composition`, `localexec` packages.

**Spec:** [`docs/superpowers/specs/2026-08-30-mcp-client-adapter-design.md`](../specs/2026-08-30-mcp-client-adapter-design.md), including its four 2026-09-04 amendments.

**Re-verification:** [`docs/research/architecture-gates/2026-09-04-mcp-implementation-reverification.md`](../../research/architecture-gates/2026-09-04-mcp-implementation-reverification.md)

## Global Constraints

- `internal/harness/adapters/mcp` never imports a sibling adapter; only `composition` imports it. Enforced by the existing `TestForbiddenImport` table, extended in Task 1.
- Every MCP tool is `domain.RiskExec` with `Mutates: true`, unconditionally. No hint-derived classification, ever — the specification itself says a server's own annotations must be treated as untrusted.
- Discovery failure for any configured server fails `composition.Open` closed. No `AllowUnsandboxedExec`-style escape hatch: the remedy is to fix or remove the server entry.
- Every server-supplied string is untrusted input. Names are sanitized before use; descriptions and schemas are bounded before storage.
- No new approval subsystem, no Policy table change, no Catalog mutability change.
- The MCP server subprocess is spawned only through the confined-command port, with `Setpgid` set, and torn down by process group with reap proven.
- Redaction already applies to tool results at the Application layer; MCP results join that same path rather than getting their own.
- Follow red-green-refactor for every production change. Each task is its own PR, stacked on its predecessor.
- No task may leave `go test -race ./...`, `go vet ./...`, `gofmt -l .`, or the cross-builds failing.

## Sizing expectation

The re-verification measured reference MCP client layers spanning 1,179 to 16,175 non-test lines on the same adopt-the-SDK decision, with the spread driven by scope rather than capability. This slice is stdio-only, statically configured, and OAuth-free, which places it near the bottom: **order 1,000 lines of production Go**. A slice trending materially past that is scope creep and should stop for review rather than continue.

---

### Task 1: Package legality and untrusted-name qualification

**Files:**
- Create: `internal/harness/adapters/mcp/naming.go`, `internal/harness/adapters/mcp/naming_test.go`
- Modify: `internal/harness/architecture/dependencies_test.go`

**Interfaces:** Produces `QualifyToolName(server, rawTool string) string` and the exported bounds `MaxQualifiedNameBytes = 64`.

Design §5 as amended. The prefix alone is not injective — `__` may appear inside either part — and raw tool names come from an untrusted server, so an unqualified join lets one server collide with another server's tool and fail `NewCatalog` at `composition.Open`, stopping the harness from starting.

- [ ] **Step 1: Write failing tests**

```go
func TestQualifyToolNameIsInjectiveAcrossTheSeparator(t *testing.T) {
	// The counterexample the design amendment records: distinct servers,
	// distinct tools, identical qualified name under a naive join.
	a := QualifyToolName("a", "b__c")
	b := QualifyToolName("a__b", "c")
	if a == b {
		t.Fatalf("both qualified to %q; the separator must not survive inside a part", a)
	}
}

func TestQualifyToolNameSanitizesHostileCharacters(t *testing.T) {
	for _, raw := range []string{"drop table", "a/b", "a\nb", "weißbier", "a..b"} {
		got := QualifyToolName("srv", raw)
		if !regexp.MustCompile(`^mcp_srv_[a-zA-Z0-9_-]*$`).MatchString(got) {
			t.Fatalf("QualifyToolName(%q) = %q, which is not sanitized", raw, got)
		}
	}
}

func TestQualifyToolNameCapsLengthWithAStableSuffix(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := QualifyToolName("srv", long)
	if len(got) > MaxQualifiedNameBytes {
		t.Fatalf("len = %d, want <= %d", len(got), MaxQualifiedNameBytes)
	}
	if got != QualifyToolName("srv", long) {
		t.Fatal("qualification is not deterministic")
	}
	if other := QualifyToolName("srv", long+"y"); got == other {
		t.Fatal("two distinct long names collided after truncation")
	}
}

func TestQualifiedNamesAreCatalogLegal(t *testing.T) {
	// Whatever a server sends, the result must pass the Catalog's own rules.
	spec := domain.ToolSpec{
		Name: QualifyToolName("srv", "  spaced  "), Source: tools.SourceMCP,
		Risk: domain.RiskExec, Mutates: true, InputSchema: emptyObjectSchema,
	}
	if _, err := tools.NewCatalog([]domain.ToolSpec{spec}); err != nil {
		t.Fatalf("NewCatalog rejected a qualified name: %v", err)
	}
}
```

- [ ] **Step 2: Implement** — sanitize each part to `[a-zA-Z0-9_-]` with `_` runs collapsed, join as `mcp_<server>_<tool>` after sanitization has removed the separator ambiguity, cap at 64 bytes, and on overflow truncate and append `_` plus an 8-hex-digit FNV-1a of the untruncated name. Kimi Code's `tool-naming.ts` convention, adopted rather than reinvented.
- [ ] **Step 3: Extend the architecture table** — add the `internal/harness/adapters/mcp` owner entry and forbidden-import rows so the package is legal from its first commit and may not import a sibling adapter.
- [ ] **Step 4: Verify** — `go test ./internal/harness/adapters/mcp ./internal/harness/architecture -race -count=1`.
- [ ] **Step 5: Mutation check** — remove the sanitization step and confirm the injectivity test fails; restore.

---

### Task 2: A long-lived confined process entry point in `localexec`

**Files:**
- Create: `internal/harness/adapters/localexec/confined.go`, `confined_test.go`
- Modify: `internal/harness/adapters/localexec/runner.go` (extract shared argv/env construction; no behavior change to `Run`)

**Interfaces:** Produces `(*Runner).NewConfinedCommand(spec tools.CommandSpec) (*ConfinedCommand, error)`, where `ConfinedCommand` exposes `Cmd() *exec.Cmd` (configured, **not started**), `Register(pid int) error`, and `Close() error`.

Design §6 as amended. `Run` runs to completion, captures output into a capped buffer, and scopes both the temp directory and the cgroup registration to the call. An MCP stdio server needs the opposite. The reusable machinery is already well-factored: `bwrapArgv` and `seatbeltCommandArgv` are pure argv transforms, and the cgroup helpers take a bare pid.

- [ ] **Step 1: Write failing tests**

```go
func TestNewConfinedCommandReturnsAnUnstartedCommand(t *testing.T) {
	// The caller owns Start, because the SDK's CommandTransport does it.
	cc := mustConfined(t, tools.CommandSpec{Argv: []string{"/bin/echo", "hi"}})
	defer cc.Close()
	if cc.Cmd().Process != nil {
		t.Fatal("command was already started")
	}
}

func TestConfinedCommandCarriesSetpgidAndAScrubbedEnvironment(t *testing.T) {
	cc := mustConfined(t, tools.CommandSpec{Argv: []string{"/bin/echo"}})
	defer cc.Close()
	if !hasSetpgid(cc.Cmd().SysProcAttr) {
		t.Fatal("Setpgid is required: teardown signals the group, not the process")
	}
	for _, assignment := range cc.Cmd().Env {
		if strings.HasPrefix(assignment, "OCH_") || looksLikeCredential(assignment) {
			t.Fatalf("child environment carries %q; it must be a whitelist, never os.Environ()", assignment)
		}
	}
}

func TestConfinedCommandAppliesTheSameConfinementRunDoes(t *testing.T) {
	// Same bwrap/Seatbelt wrapping as Run, proven by argv shape rather than
	// by re-testing the sandbox itself.
	cc := mustConfined(t, tools.CommandSpec{Argv: []string{"/bin/echo"}})
	defer cc.Close()
	if got := cc.Cmd().Path; !confinedArgv0(got) {
		t.Fatalf("argv0 = %q, want the confinement wrapper when one is available", got)
	}
}

func TestConfinedCommandCloseReleasesTheTemporaryDirectory(t *testing.T) {
	cc := mustConfined(t, tools.CommandSpec{Argv: []string{"/bin/echo"}})
	dir := cc.tempDir()
	if err := cc.Close(); err != nil { t.Fatal(err) }
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temp dir %q survived Close", dir)
	}
}

func TestRunBehaviorIsUnchanged(t *testing.T) {
	// The existing suite must stay green after the extraction; this test
	// pins the one thing an extraction most easily breaks.
	result := mustRun(t, tools.CommandSpec{Argv: []string{"/bin/echo", "hello"}})
	if !strings.Contains(result.Output, "hello") { t.Fatalf("Run regressed: %+v", result) }
}
```

- [ ] **Step 2: Implement** — extract the confinement argv/env/tempdir construction shared by `Run` and the new path; `Run` keeps its exact current behavior. `NewConfinedCommand` returns an unstarted `*exec.Cmd` with `Setpgid`, a whitelisted environment, and a handle owning the temp directory. `Register(pid)` performs the cgroup `addProcess`; `Close` unregisters and removes the temp directory.
- [ ] **Step 3: Confirm the macOS rlimit bracket** — `beginRlimitBracket` is a mutex-guarded, process-wide limit held only around `Start`. Since the caller now owns `Start`, document explicitly whether the bracket is applied by the caller, by the handle, or not at all on this path, and test the chosen answer. Do not inherit it silently.
- [ ] **Step 4: Verify** — `go test ./internal/harness/adapters/localexec -race -count=1`, plus the full suite to prove `Run` is unchanged.
- [ ] **Step 5: Mutation check** — drop `Setpgid` and confirm the test fails; restore.

---

### Task 3: The confined-command port and the SDK dependency

**Files:**
- Create: `internal/harness/adapters/mcp/port.go`, `internal/harness/adapters/mcp/client.go`, `client_test.go`
- Modify: `go.mod`, `go.sum`, `SECURITY.md`

**Interfaces:** Produces `type CommandFactory interface { NewCommand(ServerConfig) (Command, error) }` and `type Command interface { Cmd() *exec.Cmd; Register(pid int) error; Close() error }`, owned by the mcp adapter; plus `Connect(ctx, ServerConfig, CommandFactory) (*Server, error)`.

Design §3 as amended: the adapter never imports `localexec`. It declares what it needs; `composition` supplies it.

- [ ] **Step 1: Write failing tests** — drive `Connect` against a fake `CommandFactory` returning a command that runs a real in-repo test MCP server binary, proving the SDK handshake completes and that a factory error fails `Connect` closed.
- [ ] **Step 2: Add the dependency** — `github.com/modelcontextprotocol/go-sdk` pinned to an **exact** version, matching the `modernc.org/sqlite v1.56.0` precedent. Run `go mod tidy` and record the resulting graph.
- [ ] **Step 3: Rewrite `SECURITY.md`'s dependency statement** — it currently claims sqlite is the only non-test dependency, which was already untrue before this work (there are four). State the real posture, and disclose that `golang.org/x/oauth2` enters via `mcp → auth → oauthex` even though this slice is stdio-only and reaches no OAuth code path.
- [ ] **Step 4: Verify** — `go mod tidy -diff` clean, `govulncheck ./...` clean, CGO-disabled build, both cross-builds.

---

### Task 4: Bounded discovery and `ToolSpec` mapping

**Files:**
- Modify: `internal/harness/adapters/mcp/client.go`
- Create: `internal/harness/adapters/mcp/discovery.go`, `discovery_test.go`

**Interfaces:** Produces `(*Server).Discover(ctx) ([]domain.ToolSpec, error)` and the exported bounds `MaxToolsPerServer = 256`, `MaxToolDefinitionBytes = 65536`.

Design §5. Uses the SDK's own `cs.Tools(ctx, nil)` iterator, which owns cursor pagination, and `cs.InitializeResult().Capabilities.Tools` to decide whether the server offers tools at all.

- [ ] **Step 1: Write failing tests**

```go
func TestDiscoverRejectsAServerExceedingTheToolBound(t *testing.T)      // 257 tools -> whole server fails
func TestDiscoverRejectsAnOversizedToolDefinition(t *testing.T)          // description+schema > 64KiB -> whole server fails
func TestDiscoverDropsOneToolWhoseSchemaThisProjectCannotCompile(t *testing.T) // logged, not fatal (design §5.4)
func TestDiscoveredSpecsAreAlwaysRiskExecAndMutating(t *testing.T)       // never hint-derived
func TestDiscoveredNamesAreQualifiedAndCatalogLegal(t *testing.T)
func TestDiscoverIsBoundedWhenTheServerNeverStopsPaginating(t *testing.T) // hostile server: the bound stops it
```

- [ ] **Step 2: Implement** — map each tool per the design's table: qualified `Name`, verbatim `Description`, verbatim `InputSchema` independently compiled by this project's own `tools.compileSchema` (a schema this project rejects drops that one tool, logged, not fatal), `Source: tools.SourceMCP`, `Risk: domain.RiskExec`, `Mutates: true`.
- [ ] **Step 3: Verify** — `go test ./internal/harness/adapters/mcp -race -count=1`.
- [ ] **Step 4: Mutation check** — raise `MaxToolsPerServer` past the hostile fixture and confirm the pagination bound test fails; restore.

---

### Task 5: Composition wiring, Catalog assembly, fail-closed startup

**Files:**
- Modify: `internal/harness/composition/config.go`, `assembly.go`, and their tests

**Interfaces:** Produces `composition.Config.MCPServers []mcp.ServerConfig`, wired at `Open`.

- [ ] **Step 1: Write failing tests**

```go
func TestOpenFailsClosedWhenAConfiguredServerCannotBeDiscovered(t *testing.T)
func TestOpenRegistersDiscoveredToolsInTheSameCatalogAsBuiltins(t *testing.T)
func TestOpenRejectsTwoServersConfiguredWithTheSameName(t *testing.T)
func TestOpenWithNoMCPServersIsByteForByteTheAssemblyItIsToday(t *testing.T)
func TestCloseTearsDownEveryServerProcessByGroupWithReapProven(t *testing.T)
```

- [ ] **Step 2: Implement** — `composition` constructs the `localexec`-backed `CommandFactory`, connects and discovers each configured server in order, feeds every surviving spec into the same `tools.NewCatalog(...)` call the four builtins already use, and registers teardown with `Assembly.Close`.
- [ ] **Step 3: Verify** — full composition conformance suite.

---

### Task 6: Teardown by process group with reap proven

**Files:**
- Create: `internal/harness/adapters/mcp/shutdown.go`, `shutdown_test.go`

The SDK's `CommandTransport.Close` closes stdin, waits, sends `SIGTERM`, then calls `Process.Kill()` — which signals **the process, not the group** — and proves no reap. Both are weaker than this repository's existing ACP practice.

- [ ] **Step 1: Write failing tests**

```go
func TestShutdownReapsAServerThatIgnoresSIGTERM(t *testing.T)
func TestShutdownLeavesNoGrandchildBehind(t *testing.T)   // server spawns its own child
func TestShutdownReportsUnprovenReapRatherThanClaimingSuccess(t *testing.T)
```

- [ ] **Step 2: Implement** — use `CommandTransport.Close` as the first, gentlest rung, then escalate to the process group and prove reap before returning, mirroring `escalateCancel`'s existing discipline rather than inventing a second ladder.
- [ ] **Step 3: Mutation check** — signal the process instead of the group and confirm the grandchild test fails; restore.

---

### Task 7: Invocation dispatch

**Files:**
- Modify: `internal/harness/application/pipeline.go` and its tests

Design §7. `invokeTool`'s closed four-branch `switch spec.Name` gains a fifth branch keyed on `spec.Source == tools.SourceMCP`, since an MCP name is server-chosen and cannot be enumerated by this package.

- [ ] **Step 1: Write failing tests**

```go
func TestMCPToolArgumentsAreSchemaValidatedBeforeDispatch(t *testing.T)
func TestMCPToolSkipsBuiltinArgumentDecodeAndWorkspaceContainment(t *testing.T)
func TestMCPToolIsAlwaysDecidedAsRiskExecWithWorkspaceIn(t *testing.T)
func TestMCPToolResultIsRedactedOnTheExistingPath(t *testing.T)
func TestMCPToolIsErrorBecomesAToolFailureNotATransportError(t *testing.T)
```

- [ ] **Step 2: Implement** — `ValidateArgs` already runs unconditionally and is already source-agnostic. Skip `parseToolArgs` and the `scopePath`/`Resolve` containment check for MCP specs; call `policy.Decide` with `WorkspaceIn: true, Risk: domain.RiskExec`. Carry the raw validated arguments to the branch. Map `CallToolResult.IsError` to this project's existing tool-failure shape, not to a transport error.
- [ ] **Step 3: Verify** — `go test ./internal/harness/application -race -count=1`.

---

### Task 8: Contract document, evidence ledger, and documentation sync

**Files:**
- Create: `docs/architecture/mcp-client.md`, `docs/architecture/mcp-client.zh-CN.md`, `docs/architecture/mcp-client-evidence.md`
- Modify: `docs/README.md` (authority rows, milestone 9 status), root `README.md`, `SECURITY.md`

- [ ] **Step 1** — write the implemented contract, including every resource bound, the fail-closed startup rule, the name-qualification rule, and the exclusions (no Streamable HTTP, no OAuth, no per-session server configuration, Windows unsupported for the same process-group reason the ACP executor already states).
- [ ] **Step 2** — write the evidence ledger: commits, verification commands with real output, every mutation performed and its observed result, and any contract clause the implementation had to correct.
- [ ] **Step 3** — update `docs/README.md`'s milestone 9 entry from "designed, not implemented" to its real state, add the contract/reading-copy/evidence authority rows, and reference the contract from the root README (`TestImplementedContractsAppearInRootReadme` requires it).
- [ ] **Step 4: Verify** — `go test ./internal/docsguard -count=1`, full race suite, cross-builds.

---

## Open questions this plan does not settle

1. **Whether Windows gets an MCP adapter at all.** The confinement path and process-group teardown both have the same Windows gap the ACP subprocess executor already documents. This plan assumes unix-only with a cross-build guard, matching `eval`'s precedent, but does not argue for it.
2. **Server restart.** If a configured server dies mid-session, this plan leaves the harness with a Catalog entry whose process is gone. Failing the tool call is the conservative behavior; supervised restart is a larger contract with its own identity questions and is out of scope here.
3. **Whether `MaxToolsPerServer = 256` is right.** It is inherited from the design, which took it from `tools.MaxListDirEntries`. Nothing has measured a real server against it.
