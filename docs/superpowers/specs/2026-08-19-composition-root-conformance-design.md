# Composition Root and Cross-Adapter Conformance (Slice 5)

**Status:** Accepted design

**Date:** 2026-08-19

**Parent:** [Foundational architecture](2026-08-11-open-code-harness-architecture-design.md)

**Evidence:** [Composition Root and Cross-Adapter Conformance Architecture Gate](../../research/architecture-gates/2026-08-19-composition-root-and-conformance.md)

**Implemented contracts this slice must not change:** [Domain events](../../architecture/domain-events.md),
[Engine vertical slice](../../architecture/engine-vertical-slice.md),
[EventStore v2](../../architecture/eventstore-v2.md),
[Provider adapter](../../architecture/provider-adapter.md),
[Tool runtime](../../architecture/tool-runtime.md),
[SQLite canonical EventStore](../../architecture/sqlite-eventstore.md),
[JSONL audit replica](../../architecture/jsonl-audit-replica.md),
[Runtime Host](../../architecture/runtime-host.md)

## 1. Decision summary

Six slices are individually implemented and verified. None of them has ever
been assembled with the others. This slice closes that gap and adds no
capability: it introduces the single place where concrete implementations are
wired, and it redistributes existing conformance suites so that the real
adapters run the contracts that today only doubles run.

The load-bearing decisions are:

1. **One composition root, as a library.** `internal/harness/composition`
   returns an assembled, closeable value. `cmd/och` is a thin binary that
   reads configuration and calls it. Assembly is asserted by tests, not by
   launching a process.
2. **The root is the only package permitted to import adapters**, and that
   permission is enforced by the existing architecture guard, not merely
   documented. Every other package stays forbidden, and a package with no
   declared owner stays forbidden.
3. **`enginescenariotest` runs against the SQLite adapter** in addition to the
   memory adapter, with no change to the suite.
4. **`engine/modeltest` splits** into a transport-neutral contract that every
   `engine.Model` satisfies and a double-accounting suite that only in-process
   implementations can express. `openaicompat` runs the transport-neutral one.
5. **One assembly test** exercises Application, SQLite, `openaicompat` over a
   local `httptest` server replaying existing SSE fixtures, `workspacefs`,
   `localexec`, and `runtime.Host` in a single process, with no network and no
   credential.

If any of this requires changing an implemented contract, that is a finding to
report and a separate slice to schedule. It is not a change to make here.

## 2. Goals

1. A named, tested composition root assembling every implemented adapter.
2. A thin binary over the root, proving the assembly is reachable as a program.
3. `enginescenariotest.Run` green against SQLite.
4. A transport-neutral `engine.Model` contract green against `openaicompat`.
5. One end-to-end assembly test covering a turn that calls a workspace tool
   and commits to a real SQLite file in a temporary directory.
6. An architecture guard that permits adapter imports in exactly one package.
7. Keyless, network-free verification for everything above.

## 3. Non-goals

1. ACP, TUI, MCP, and the Context Engine.
2. A plugin kernel, DI container, service locator, or reflection-based wiring.
3. A generated composition graph (gate finding R2; revisit once the root is
   stable).
4. Live provider calls in the default verification path (gate finding R4).
5. New ports, events, error codes, or contract changes (gate finding F7.5).
6. Configuration file formats, flag ergonomics, daemonization, or logging
   policy beyond what the assembly test needs.
7. Multi-host, multi-workspace, or multi-tenant assembly.

## 4. Composition root package

`internal/harness/composition` exposes:

- `Config` — a flat, bounded value describing one assembly.
- `Open(context.Context, Config) (*Assembly, error)` — validates `Config`,
  constructs each adapter in dependency order, and returns a running assembly
  or a nil assembly and an error. Partial construction never leaks: every
  successfully constructed resource is released before returning an error.
- `Assembly` — holds the constructed `*application.Service`, the
  `*runtime.Host`, and the `application.EventStore`. Accessors are read-only.
- `(*Assembly) Close() error` — idempotent, ordered shutdown. Joined errors,
  never a swallowed one.

Construction order is fixed and explicit: SQLite store, then Runtime Host,
then provider Model, then workspace filesystem and command runner, then the
tools catalog and policy, then `application.Service`. Shutdown is the reverse.

The package constructs; it does not decide. It contains no domain transition,
no retry policy, and no branch that exists only for tests.

## 5. Configuration and bounds

`Config` carries only what an assembly cannot derive:

| Field | Meaning | Bound |
| --- | --- | --- |
| `WorkspaceRoot` | Absolute path jailing all filesystem tools | Must exist, must be a directory, canonicalized before use |
| `DatabasePath` | SQLite database file | Parent directory must exist |
| `RuntimeID` | Writer identity for the fencing lease | Non-blank, valid UTF-8, unpadded |
| `AuditDirectory` | JSONL audit replica destination | Optional; empty disables the exporter |
| `Provider` | Base URL, model name, API key source, timeouts | Base URL required; key read from the environment, never from `Config` literals in tests |
| `Policy` | `policy.Mode` | Defaults to `policy.ModeDefault` |
| `Limits` | Step, tool-call, approval, and exec bounds | Defaults from `application.DefaultConfig()` |

`Config.Validate()` is total and fail-closed: every field is checked before
any resource is constructed. An invalid `Config` constructs nothing.

No field may widen a bound that an implemented contract already fixes. Where
a bound exists in `application.Config`, `sqlite.Config`, or `runtime.Config`,
`composition.Config` forwards it and never redefines it.

## 6. Lifecycle and shutdown

`Open` returns only after the Runtime Host has completed startup
reconciliation, so a returned `Assembly` is ready to accept a turn. If
reconciliation fails, `Open` fails and releases everything already built.

`Close` stops admission, waits for the host's loops with a bounded timeout,
closes the store last, and returns the joined result. Calling `Close` twice is
safe and returns the first result. A caller that abandons an `Assembly`
without `Close` leaks the SQLite handle and the host goroutines; this is
stated, not defended against.

## 7. Dependency guard extension

`internal/harness/architecture` gains an `ownerComposition` for
`internal/harness/composition`. The forbidden-import table changes as follows:

- `ownerComposition` may import `domain`, `engine`, `application`, `policy`,
  `tools`, `runtime`, and every package under `internal/harness/adapters`.
- Every other owner keeps its current prohibitions unchanged, including the
  existing rule that `application` may not import any adapter.
- A directory with no declared owner remains forbidden from importing
  adapters. The test asserts this explicitly, so that adding a package does
  not silently inherit the composition exception.
- `cmd/och` may import `composition` and the standard library only.

The guard is a test, so this extension is itself covered by the existing
`TestClassifyProductionDirectory` table.

## 8. Scenario suite over the durable store

`internal/harness/adapters/sqlite` gains a test that calls
`enginescenariotest.Run` with a `Harness` whose `Store` is a real `Open`ed
store over a temporary directory, mirroring the existing
`adapters/sqlite/conformance_test.go` shape for `eventstoretest`.

The suite must not change. If a scenario passes against `adapters/memory` and
fails against SQLite, the defect is in the adapter or in an assumption the
memory adapter accidentally satisfies, and it is fixed as part of this slice.
If the suite itself proves to encode a memory-only assumption, that is a
contract finding: it is reported, and the suite is corrected only if the
correction preserves every behavior the memory adapter is already asserting.

## 9. Model contract split

`engine/modeltest` is reorganized without losing coverage:

- `RunContract(*testing.T, Contract)` — behaviors observable through any
  transport: ordered `text_delta* tool_call* completed` grammar with exact
  request delivery, mid-stream error propagation, a step that blocks until
  cancellation, and independent concurrent streams.
- `Run(*testing.T, Factory)` — unchanged entry point for in-process doubles.
  It calls `RunContract` and then the double-accounting cases:
  `ReturnNilStream`, `ReturnStreamOnStartupError` precedence, and `Close`
  accounting. `testkit.ScriptedModel` keeps exactly its present coverage.

`Contract` carries the factory plus matchers, because a transport adapter
reports a startup failure as a classified `ProviderFailure` rather than the
sentinel value an in-process double returns:

```go
type Contract struct {
    Factory           Factory
    MatchStartupError func(error) bool // nil means require identity
    MatchStreamError  func(error) bool // nil means require identity
}
```

`openaicompat` runs `RunContract` with a factory that serves each `Config` as
SSE bytes from a local `httptest` server, and with matchers asserting the
documented `ProviderFailure` classification. Fixtures already in
`adapters/openaicompat/testdata/sse` are reused where they express the case;
new fixtures are added only where no existing one does.

The double-only knobs are not deleted and are not faked for HTTP. They remain
what they are: assertions about an in-process implementation's return values.

## 10. Assembly test

One test in the composition package's external test package builds a real
assembly and drives one turn end to end:

- Workspace: `t.TempDir()`, seeded with a file the turn will read.
- Store: a real SQLite database in that directory.
- Provider: `openaicompat` pointed at an `httptest.Server` replaying an SSE
  script in which the model requests `read_file` and then answers from the
  tool result.
- Policy: `policy.ModeDefault`, so the read is allowed without approval and
  the assertion covers the real policy path rather than an allow-all bypass.
- Host: started, reconciled, and closed through `Assembly.Close`.

Assertions: the turn completes; the durable event stream replays to the same
state; it contains `tool.call.started`, `policy.decision.recorded`, and
`tool.call.completed`; and the assistant's final text reflects the file
contents. No network, no credential, no sleep-based synchronization.

A second, smaller test asserts that `Open` with an invalid `Config` constructs
nothing observable: no database file is created, and no goroutine outlives the
call.

## 11. Binary

`cmd/och` reads configuration from flags and the environment, calls
`composition.Open`, waits for `SIGINT`/`SIGTERM`, calls `Close`, and exits
non-zero on error. It contains no logic that is not either flag parsing or
signal handling. It is built by CI on every platform in the cross-build matrix
and is not otherwise tested in this slice.

## 12. Failure semantics

- `Config.Validate` errors are returned before construction, name the field,
  and are not wrapped in an adapter error type.
- A construction failure returns the underlying adapter error unwrapped enough
  for `errors.As` to reach `*application.Error` and `*application.StoreError`.
- `Open` never returns a non-nil `Assembly` with a non-nil error.
- `Close` returns `errors.Join` of every stage; one stage failing does not
  skip a later stage.
- The assembly adds no new error code and no new error category.

## 13. Resource bounds

Every bound is inherited, not invented: `application.DefaultConfig` limits for
steps, tool calls, approval and exec timeouts, and result size; `sqlite.Config`
limits for pool size, busy timeout, and payload size; `runtime.Config` limits
for heartbeat interval and deadline. The composition root adds exactly one new
bound: a shutdown timeout for `Close`, defaulting to 10s, above which it
returns a timeout error rather than blocking forever.

## 14. Delivery plan

Five tasks, one pull request each, in
[the implementation plan](../plans/2026-08-19-composition-root-conformance.md).

## 15. Completion criteria

1. `go test -race ./... -count=1` green, including the new assembly test.
2. `enginescenariotest.Run` green against both `adapters/memory` and
   `adapters/sqlite`.
3. `modeltest.RunContract` green against both `testkit.ScriptedModel` and
   `adapters/openaicompat`; `modeltest.Run` still green against
   `testkit.ScriptedModel` with no lost case.
4. The architecture guard permits adapter imports in `composition` only, and
   asserts that an unowned package is still forbidden.
5. `cmd/och` builds on every platform in the cross-build matrix.
6. No implemented contract document required an edit. If one did, the change
   is recorded as a finding in the evidence ledger with its own follow-up.
7. An evidence ledger records every task commit, the verification commands,
   and every deferred GA blocker.

## 16. Exclusions

- ACP, TUI, MCP, Context Engine, evaluation, OpenTelemetry.
- Configuration file formats, config precedence rules, and flag ergonomics
  beyond the minimum.
- Process supervision, restart policy, and daemonization.
- Multi-host or multi-workspace assembly.
- Generated composition documentation.
- GA blockers: no soak test of a long-lived assembly, no crash injection at
  the process level, no verification against a live provider, and no
  performance characterization of the assembled path.
