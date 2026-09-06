# MCP Client Adapter Completion Evidence

**Status:** Evidence ledger for milestone 9 (plan Tasks 1–8, with Task 4a inserted during implementation)

**Contract:** [MCP client adapter — implemented contract](mcp-client.md)

**Design:** [MCP client adapter design](../superpowers/specs/2026-08-30-mcp-client-adapter-design.md)

**Plan:** [MCP client adapter implementation plan](../superpowers/plans/2026-09-04-mcp-client-adapter.md)

**Re-verification:** [MCP implementation-time re-verification](../research/architecture-gates/2026-09-04-mcp-implementation-reverification.md)

## Commits

Every commit below is a real, resolvable commit in this repository's history.

| Commit | Task | Content |
| --- | --- | --- |
| `27f60de` | Re-verification | Implementation-time re-verification of the accepted design |
| `8b3901e` | Re-verification | Three places the design met code that did not match it |
| `99c739c` | Task 1 | Untrusted tool-name qualification; architecture-table entry |
| `881fb95` | Task 4a | Source-aware schema validation in `tools`, degrading rather than dropping |
| `f5cbd57` | Task 2 | Long-lived confined command entry point in `localexec` |
| `9ef68c1` | Task 3 | Official Go SDK behind a confined-command port |
| `0674a5e` | Task 4 | Bounded discovery and `ToolSpec` projection |
| `929580b` | Task 5 | Composition wiring into the one tool catalog |
| `2fba322` | Task 6 | Process-group teardown with proof |
| `fd1a844` | Task 7 | Dispatch by source; charter §12.1 |

Design amendments landed alongside: four on 2026-09-04 (§3's sibling-import
contradiction, §3's stale dependency count, §5's false collision claim, §6's
missing API) and one on 2026-09-05 (§5's schema drop rule), each marked in
place in both language copies rather than rewritten.

## The design was wrong in five places, and the implementation found four of them

Recorded because the count matters: an accepted design met real code five
times and lost.

| Clause | What was wrong | Found by |
| --- | --- | --- |
| §3 vs §6 | §3 forbids importing a sibling adapter; §6 requires reusing `localexec`. `localexec` is a sibling — the existing `TestForbiddenImport` would have caught it on the first build. | Reading the guard before writing code |
| §6 | "Reuse `localexec`'s confinement" had no API to reuse: `Run` runs to completion with its temp directory and cgroup registration scoped to the call. | Reading `runner.go` |
| §5 | "A collision can only happen via a `server.Name` collision" is false — the `mcp__` prefix is not injective, and raw names come from an untrusted server. | Reading `validateSpec` |
| §3 | "The SDK would be the second non-test dependency" predated the web bridge's `coder/websocket`; and the SDK brings seven transitive modules. | Counting them |
| §5 (amended) | The 2026-09-04 amendment's own replacement rule — sanitize to `[a-zA-Z0-9_-]` — **keeps** the separator in the alphabet, so it does not restore injectivity. | **Task 1's own failing test** |

The last row is the one worth dwelling on: an amendment written the previous
day, reasoned carefully, was wrong, and only a test that actually ran the rule
exposed it. The corrected rule excludes underscore from the part alphabet.

## Two claims read at an untagged commit did not both survive the release

The re-verification was read at SDK commit `21c18c6`, which carries no tag,
while the design requires pinning a release. Task 3 pinned `v1.7.0` and
re-read every load-bearing claim against the module that entered the build.

| Claim | Outcome at `v1.7.0` |
| --- | --- |
| `CommandTransport` takes a caller-built `*exec.Cmd` | held exactly |
| Shutdown ladder: close stdin → SIGTERM → `Process.Kill()` | held exactly |
| Production import set (six modules, `oauth2` unavoidable) | held exactly |
| "Three live protocol versions" | **wrong — four**, adding `2025-03-26` |

## Dependency posture, measured

`go mod tidy` after the import added exactly the seven modules the
re-verification predicted and no others: `google/jsonschema-go`,
`yosida95/uritemplate/v3`, `segmentio/encoding` (+ `segmentio/asm`),
`golang.org/x/sync`, `golang.org/x/time`, `golang.org/x/oauth2`.
`golang-jwt/jwt/v5` is confirmed **absent**, as predicted.

`go list -deps ./internal/harness/adapters/mcp` lists `golang.org/x/oauth2`
and `golang.org/x/oauth2/internal` in the **production** graph, so the
unavoidable-OAuth finding is proven by the toolchain rather than inferred from
imports. `govulncheck ./...` reports **no vulnerabilities** across the
enlarged graph, and `go mod tidy -diff` is clean.

`SECURITY.md`'s dependency statement was rewritten. Its claim that
`modernc.org/sqlite` was the only non-test dependency **was already untrue
before this work**; there are now five, listed with why each exists, plus an
explicit paragraph naming the OAuth dependency no code here calls.

## Mechanism → test → mutation result

Every row reflects a mutation actually performed and observed in this
repository's working history — weakening a guard, confirming the dependent
test fails, restoring it — not a claim that a test exists.

| Mechanism | Test | Mutation result |
| --- | --- | --- |
| Name injectivity (Task 1) | `TestQualifyToolNameSeparatesDistinctPairs` | Restoring the design's original `[a-zA-Z0-9_-]` alphabet makes it fail — this is what proved the amendment wrong. Caught, restored. |
| Lossy-part disambiguation | same | Dropping the per-part hash makes it fail. Caught, restored. |
| Length cap | `TestQualifyToolNameCapsLengthWithAStableSuffix` | Dropping the truncation suffix makes it fail. Caught, restored. |
| Builtin strictness unchanged (Task 4a) | `TestBuiltinSchemaValidationIsUnchanged` | Removing the source guard in `validateSpec` makes it fail. Caught, restored. |
| Builtin per-call strictness | `TestValidateArgsNeverDegradesForABuiltin` | Removing the source guard in `ValidateArgs` makes it fail. Caught, restored. |
| Degraded is not absent | `TestValidateArgsDegradesToObjectCheck…` | Dropping the object check accepts `[]` and `"text"`. Caught, restored. |
| Process group on the confined command (Task 2) | `TestConfinedCommandCarriesSetpgid` | Dropping `Setpgid` makes it fail. Caught, restored. |
| Child environment whitelist | `TestConfinedCommandEnvironmentIsAWhitelistNotTheParent` | Using `os.Environ()` makes it fail, showing the fixture variable reaching the child. Caught, restored. |
| Temp directory ownership | `TestConfinedCommandCloseReleasesItsTemporaryDirectory` | Skipping removal makes it fail. Caught, restored. |
| Tool-count bound (Task 4) | `TestDiscoverRejectsAServerExceedingTheToolBound` | Raising the bound past the hostile fixture makes it fail. Caught, restored. |
| Definition-size bound | `TestDiscoverRejectsAnOversizedToolDefinition` | Dropping the check makes it fail. Caught, restored. |
| Fixed risk classification | `TestDiscoveredToolsAreAlwaysExecRiskAndMutating` | Deriving risk from the server (`RiskRead`, non-mutating) makes it fail. Caught, restored. |
| One bad tool does not fail the harness | `TestDiscoverDropsOneUnusableTool…`, `TestProjectToolDropsASchemaThatIsNotAJSONObject` | Admitting an unregisterable tool makes both fail. Caught, restored. |
| Degradation is auditable | `TestDiscoverRecordsWhichToolsWillBeLooselyChecked` | No longer recording it makes the test fail. Caught, restored. |
| Fail-closed on an unreachable server (Task 5) | `TestOpenFailsClosedWhenAConfiguredServerCannotBeReached` | Letting it degrade makes it fail. Caught, restored. |
| Duplicate server names | `TestOpenRejectsTwoServersConfiguredWithTheSameName` | Removing the check makes it fail. Caught, restored. |
| No leak on partial failure | `TestOpenTearsDownEarlierServersWhenALaterOneFails` | Not closing already-connected servers makes it fail. Caught, restored. |
| Close stops servers | `TestCloseStopsEveryConnectedServer` | Skipping their close makes it fail. Caught, restored. |
| Builtins are not displaced | `TestOpenWithNoMCPServersIsUnchanged`, `TestOpenRegistersDiscoveredTools…` | Registering only MCP specs makes both fail. Caught, restored. |
| Group escalation (Task 6) | `TestShutdownLeavesNoGrandchildBehind` | Signalling the process instead of the group leaves 1 process alive in the group. Caught, restored. |
| No false teardown proof | `TestShutdownReportsUnprovenReap…` | Dropping the leader check reports success while the process is alive — this is the bug the test found in the first implementation. Caught, restored. |
| Escalation exists at all | `TestShutdownLeavesNoGrandchildBehind` | Removing escalation entirely leaves the grandchild alive. **See the note below.** |
| Tool failure is a Turn event (Task 7) | `TestExternalToolFailureIsATurnEventNotATransportError` | Returning an error instead makes it fail. Caught, restored. |
| Dispatch by source | `TestInvokeToolRoutesAnExternalSpecBySourceNotByName` | Removing the branch makes it fall through to unknown-tool. **See the note below.** |
| Port need by source, not risk | `TestCatalogPortNeedsRequiresTheExternalPortForAnMCPSpec` | Deriving from `Risk` makes it fail. Caught, restored. |
| External result bound | `TestExternalResultIsBoundedLikeEveryOtherToolResult` | Dropping the bound makes it fail. Caught, restored. |

## Two mutations initially caught nothing, and that is recorded rather than fixed quietly

Both were aimed at a test that did not exercise the code being mutated. Both
were re-aimed, and both then failed. The pattern is worth naming because a
mutation that catches nothing looks identical to a mechanism that is well
guarded.

**Task 6, removing escalation entirely.** First aimed at
`TestShutdownReapsAServerThatIgnoresSIGTERM`, which stayed green: the SDK's
own ladder ends at `Process.Kill()`, and SIGKILL cannot be ignored, so a
single stubborn process was never the gap this task closes. That test is now
annotated in place as a regression guard on the SDK's behaviour rather than
proof of this project's. Re-aimed at `TestShutdownLeavesNoGrandchildBehind`,
the mutation fails.

**Task 7, dispatching by name instead of source.** First aimed at
`TestExternalDispatchForwardsRawArgumentsVerbatim`, which stayed green because
that test calls the forwarding function directly and never goes through
`invokeTool`. `TestInvokeToolRoutesAnExternalSpecBySourceNotByName` was added
to go through the entry point, with a companion proving the branch does not
swallow the builtins; the mutation then fails with "fell through to the
unknown-tool default".

## Real findings this work surfaced

Facts established by running code, not by reading it.

- **The SDK's server side refuses to publish a non-object input schema.**
  `AddTool` panics with `can't marshal input schema to a JSON object`, so a
  server built on this SDK cannot send one and the fixture provably cannot
  produce it. Only another MCP implementation could, which is why the defense
  exists and why that case is covered as a direct projection test.
- **A second `cmd.Wait()` races the SDK's own.** The SDK owns `Wait` inside
  its `Close`; calling it from the teardown ladder fails with "no child
  processes". Found by running the tests.
- **`groupGone` reported a false success in its first form.** Checking only
  the group means a non-leader process reads as gone while alive.
- **The Windows cross-build broke on the unix build tag**, and the
  verification command that should have caught it used `;` rather than `&&`,
  so it printed "cross ok" over a failing build. Both fixed; the command is
  now gated.
- **An environment switch cannot reach a confined child.** A Task 5 test
  skipped for this reason, which is the three-name whitelist working. The
  fixture gained argv selection so the test exercises something real.
- **Two test expectations were wrong rather than the code**: an absolute
  argv0 outside the workspace is denied by design, and a truncated result's
  documented shape is `prefix + \n[truncated]` with the marker appended after
  the bound, as the builtins do.

## Verification

Run on this branch, gated so a failing step cannot be reported as success:

```
gofmt -l .                                   # clean
go vet ./...                                 # clean
GOOS=windows go vet ./...                    # clean
GOOS=darwin  go vet ./...                    # clean
go test -race ./... -count=1                 # pass
go mod tidy -diff                            # clean
go run golang.org/x/vuln/cmd/govulncheck@latest ./...   # No vulnerabilities found.
CGO_ENABLED=0 go build ./...                 # clean
```

`internal/harness/adapters/mcp` holds 37 tests, most driving a real MCP server
subprocess built from `testdata/fixtureserver` over a real stdio transport
rather than a hand-written double: a real handshake, real discovery including
hostile listings, real calls, and real teardown including a server that spawns
its own child and one that ignores SIGTERM.

## Known limitations and open blockers (not GA)

See the contract's own [Maturity and known limits](mcp-client.md#maturity-and-known-limits).
Summarized: no Streamable HTTP or OAuth, no process-group teardown on Windows,
no server restart, no per-session configuration, an inherited rather than
measured 256-tool bound, two bounds that disagree by inheritance, no MCP
evaluation suite, and no proof that a real model resists prompt injection
carried in a server's tool descriptions or results.
