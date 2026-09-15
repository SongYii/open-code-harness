# Internal Provider Closure — Verification Evidence

- Date: 2026-09-15
- Scope: approved internal steps 1–2; no public Provider SDK
- Baseline: `bd762556b9039996aa2059207397c66d6d6fa0b6`
- Implementation: working-tree changes on that baseline, not yet committed
- Design: [Provider startup extensibility](../superpowers/specs/2026-09-15-provider-startup-extensibility-design.md)
- Plan: [internal closure](../superpowers/plans/2026-09-15-provider-internal-closure.md)
- Contracts: [Provider](provider-adapter.md), [replay](provider-replay.md),
  [Composition](composition-root.md), [startup extensions](startup-extensibility.md)

## Implemented and deliberately unchanged

Both real HTTP adapters run the shared suite for concatenated Unicode text,
ordered tools, completion, startup/stream failures, classified cancellation
and independent stream cursors. Messages retains whole-response validation and
explicit native usage/replay assertions; scripted/legacy cases still reject
unexpected completion metadata. Native protocol tests remain separate.

Builtin models have once-owned close for private standard HTTP transports.
Injected nonstandard transports remain borrowed; supplied standard transports
are cloned and only the clone is closed. Model close requires drained streams
and permanently rejects subsequent calls, not concurrent teardown of active requests.

Composition registers resources as acquired and uses `Assembly.Close` for
startup rollback and normal shutdown: admission stop, cancel/drain, MCP,
localexec, Provider, Host/store/lease, existing telemetry. Failed/timed-out leaf
cleanup stops renewal without explicitly releasing ownership or closing the
potentially live store. A late result cannot make cached failed Close succeed.

No request flags/body schema, durable schema, replay state, registration SDK,
OTel attributes or paid model calls changed. Current constructors do no network
I/O; no asynchronous factory, global factory test hook or new startup-timeout
setting was introduced.

## Findings that changed the implementation

1. Exact text chunk counts in the shared suite excluded legitimate Messages
   coalescing. Assertions now compare semantic output and native metadata.
2. The old concurrent test recorded two calls but ignored their errors. The
   scripted model deliberately rejects a different-than-expected request, so
   the test could pass with one usable stream. The common fixture now makes
   two valid calls and requires both to complete independently. Native tests
   remain responsible for distinct request identities; the scripted model's
   exact-request rule was not weakened.
3. An initial strengthened concurrency fixture used an empty Chat completion
   that the adapter rejected. It now emits real text. Existing native empty
   output tests keep that responsibility; decoding was not changed for a test.
4. Host.Store rejects access after drain starts. The close-order test initially
   mistook that admission rejection for premature database close. It now
   retains the pre-drain store and tests matching lease renewal during Provider
   teardown, not an unrelated facade error.
5. The actual missing model-wide resource was the private HTTP pool, not a
   decoding worker needing a new supervisor. Request cancellation/per-stream
   Close still own active I/O; Assembly drain waits for those calls.

## Executable evidence and limits

| Test | Evidence and limit |
| --- | --- |
| Both adapters' `TestModelContract`, `testkit.TestScriptedModel` | Semantic text/tools/completion, valid independent calls and native Messages metadata; not chunk timing or arbitrary-provider certification |
| `TestBuiltinProviderCloseOwnsOnlyPrivateConnections` | Actual model keep-alive socket closes; source client's existing socket is reused; post-close requests rejected |
| `TestBuiltinProviderCloseDoesNotCloseBorrowedTransport` | A borrowed custom transport's close hook is never invoked; no ownership transfer claimed |
| `TestAssemblyProviderCloseAfterDrainBeforeLeaseRelease` | Open registers Provider; active admission delays close; matching lease still exists during close; once-owned close and healthy successor |
| `TestAssemblyProviderUnprovenTeardownRetainsLease` | Failed/timed-out close caches failure, retains store/lease, refuses immediate successor and does not retry; natural lease expiry still requires supervisor discipline |
| `TestProviderStartupRollbackAllowsHealthySuccessor` | Real post-Provider MCP startup failure permits healthy restart for both routes; no idle startup socket exists before the builtin's first request |

Existing real Messages turn/summary cancellation, fencing, overflow and crash
tests remain required. Internal admission probes complement those tests; they
do not replace real request execution or prove hostile-code containment.

## Mutation checks

Mutations were applied independently and restored before subsequent tests.

| Mutation | Observed failure |
| --- | --- |
| Omit `assembly.provider.Close()` | `TestAssemblyProviderCloseAfterDrainBeforeLeaseRelease`: `Provider not closed exactly once` |
| Omit OpenAI-compatible private transport close | `TestBuiltinProviderCloseOwnsOnlyPrivateConnections/chat/default`: `owned idle socket survived model Close` |
| Append `!` to Messages visible text | Shared Unicode case rejects `你好 🌍!` versus `你好 🌍`, before native state validation |

These are three targeted falsification results, not exhaustive mutation
coverage. Startup idle-pool cleanup is not separately observable before any
request; rollback and normal shutdown share the tested close implementation.

## Verification log

Passed focused race command:

```sh
go test -race ./internal/harness/adapters/openaicompat ./internal/harness/adapters/anthropic ./internal/harness/testkit ./internal/harness/composition -run 'TestModelContract|TestBuiltinProvider|TestAssemblyProvider|TestProviderStartup|TestScriptedModel$' -count=1
```

Initial sandbox execution could not create a loopback listening socket.
Re-running with local-fixture permissions passed, without external model
endpoints/credentials. No test or production safeguard was weakened for this.

The full repository `go test -race ./... -count=1` passed, including localexec,
both HTTP adapters, Application, Composition, SQLite, runtime, eval, launcher
and SDK packages. Composition took 134.959s and eval 288.991s on this run;
these are test durations, not product-performance measurements.

The focused race command above also passed with `-count=5`. The late-cleanup
assertion was then strengthened and
`go test -race ./internal/harness/composition -run '^TestAssemblyProviderUnprovenTeardownRetainsLease$' -count=5`
passed. Final review made closed Messages models a permanent local rejection
rather than a transient network failure. After that final change,
`go test -race ./internal/harness/adapters/anthropic ./internal/harness/composition -count=1`
passed again (24.121s and 117.356s respectively). The full run and this affected-
package rerun together cover the final production tree.

`go vet` for both adapters, `engine/modeltest` and Composition passed, as did
`go test ./internal/docsguard ./internal/harness/architecture -count=1`,
`git diff --check` and formatting checks. No full-suite failures were suppressed.

Internal steps 1–2 are complete in the working tree. No commit, push or merge
was performed for this implementation turn.

## Remaining boundary / 当前限制

Public DTOs, factory failure semantics, credential lifecycle, implementation
attribution and a real external consumer remain separate gates. The public
API proposal is still draft. This is working-tree evidence, not a release.

本轮只收口内部语义与资源归属，不发布 Provider SDK。本地 HTTP/进程测试不产生
模型费用。公开扩展仍需真实消费者和独立持久化兼容评审；Go 接口不是安全沙箱。

## Follow-up: distinct requests, HTTP2 and the real launcher (2026-09-15)

The user approved three additional local verification gates. This follow-up
adds tests, not production behavior, a new SDK or a paid-provider experiment.

| Gate | Actual path and assertions |
| --- | --- |
| `TestProviderDistinctRequestsAndHTTP2Isolation` | Both adapters, HTTP1 and TLS/HTTP2, successful A/B and cancel-A cases: the server decodes distinct request bodies; both requests are in flight before release/cancellation; B completes after A is cancelled. Text, tool IDs/arguments, usage and Messages private state remain distinct. A's completed metadata is rechecked after B completes. |
| `TestProviderHTTP2BorrowedPoolSurvives` | Warm a real source-client HTTP2 connection, construct and use the model's cloned transport, and prove distinct socket addresses. Model close closes its socket; a later source request reuses its existing HTTP2 socket. |
| `TestStockLauncherProviderInFlightShutdown` | Build the actual `cmd/och` binary with race instrumentation. For both adapters, pause a conversation or automatic summary at a real HTTP request. Compare normal-completion controls, stdin EOF and SIGTERM. Bound shutdown at 8s; EOF completes successfully, SIGTERM follows the current handled-cancellation exit-1 contract rather than signal death. Start a second real binary immediately, load the same session and create another durable session. Verify immutable history, closed/reconciled operation brackets and no unexpected provider retry. |

The matrix contains 22 leaf scenarios: 8 distinct-request cases, 2 borrowed-pool
cases and 12 launcher cases. Each launcher case uses a real successor process;
neither lease expiry waits nor lease edits can make takeover pass. HTTP2 cases
check `ProtoMajor == 2`, and concurrent A/B must share the same socket address.
An HTTP1 fallback is a failure, not evidence of HTTP2 support.

Process-exit checks do **not** prove Model.Close ran: the operating system also
closes sockets after process death. In-process socket/lease-order tests remain
the evidence for Provider resource ownership. These gates do not certify a
remote provider, public SDK, arbitrary HTTP2 extensions or Windows process
supervision. The launcher runtime matrix explicitly skips Windows/short mode;
all cases ran on Linux here.

### Fixture findings, not production fixes

- The first Messages fixture put non-empty tool input in a block-start frame.
  The real decoder correctly rejected it. The fixture now uses an empty start
  object followed by an input JSON delta; production validation was unchanged.
- A failed assertion initially let test cleanup call Close concurrently with
  another consumer's Next, violating the existing single-owner stream contract.
  Cleanup now cancels and joins both consumers before closing streams, even on
  failure. The race report was in the test harness, not a new concurrency
  guarantee to add to production streams.
- Chat cancellation carries the Engine cancellation code without requiring
  the same wrapped failure shape as Messages. The assertion now checks the
  shared Engine cancellation contract, not an invented common cause type.
- Conversation purpose may be omitted (legacy default), and the minimum
  accepted trigger percentage is 60. The launcher fixture now respects both:
  it normalizes absent attribution when observing requests and supplies enough
  real history to trigger summary at a valid configured threshold.

### Negative controls

Production mutations used isolated Go source overlays in a temporary directory;
repository production source was not edited. Each control failed at its intended
assertion, not compilation or an unrelated earlier validation:

| Control | Focused case and observed rejection |
| --- | --- |
| Overlay Messages request construction to always send input `A` | `messages/http2/cancel=false`: `request names crossed` |
| Run with `GODEBUG=http2client=0` | `chat/http2/cancel=true`: `requested HTTP version was not actually negotiated` |
| Overlay stock main without SIGTERM registration (also removing the unused import); pass overlay through `GOFLAGS` so the child build receives it | `chat/conversation/sigterm`: `wrong signal exit: signal: terminated` |

The HTTP2 control is a protocol-downgrade fixture, not a production mutation.
The signal control distinguishes handled shutdown from a dead process whose
sockets happened to disappear.

### Verification

The complete new matrix passed three times under race detection:

```sh
go test -race ./internal/harness/composition -run 'TestStockLauncherProviderInFlightShutdown|TestProviderDistinctRequestsAndHTTP2Isolation|TestProviderHTTP2BorrowedPoolSurvives' -count=3
```

Observed duration: 114.841s, not a product-performance benchmark. A subsequent
test-only tightening gives borrowed-pool HTTP calls explicit timeout/context
bounds. After that final test change, the full Composition race regression
passed:

```sh
go test -race ./internal/harness/composition -count=1
```

Observed duration: 146.326s. `go vet ./internal/harness/composition`,
`go test ./internal/docsguard ./internal/harness/architecture -count=1`,
`git diff --check` and formatting checks also passed. This follow-up does not
claim another full-repository run; the preceding implementation's full-repository
result is recorded above.

All three follow-up gates are complete in the working tree. No production fix
was needed for this follow-up; fixture corrections are recorded above. No paid
model calls, commit, push or merge were performed.
