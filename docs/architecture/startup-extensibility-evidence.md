# Startup Extensibility Evidence / 启动扩展实施证据

**Status:** Complete evidence ledger for the first slice; not GA.
**Public API stability:** `sdk/och` and `sdk/contextpolicy` are experimental;
source compatibility is not yet promised. Stable promotion/adoption evidence
is incomplete; the implementation status above does not claim otherwise.
**Date:** 2026-09-12; boundary follow-up 2026-09-13.
**Contract and subsequent plan:** [Startup extensibility](startup-extensibility.md).
**Reading copy:** [启动时可插拔架构](startup-extensibility.zh-CN.md).

This ledger describes working-tree implementation and observed local tests,
not a release or a new benchmark. No implementation commit was created by
this task. OTel adapter implementation and telemetry schema were not changed.

## Implemented boundaries

| Boundary | Executable evidence |
| --- | --- |
| Failed rolling summary cannot adopt a speculative cut | `application.TestFailedRollingSummaryRetainsAllHistoryAfterOldCheckpoint` creates a real earlier committed summary and checks complete retained messages after the next summary fails |
| Host admission/cancellation/drain | `runtime.TestDrainCancelsAndWaitsForCleanupWhileRejectingNewWork`, `TestDrainTimeoutIsTruthfulAndAdmissionStaysClosed`, `TestAdmissionFollowsCallerCancellation` |
| Blocked renewal/permanent fencing | `runtime.TestHeartbeatWatchdogFencesBlockedRenewal`, `TestHeartbeatNeverRegainsLease`; the blocked worker stays accounted for until returned |
| Service and external store cannot bypass teardown | `composition.TestAssemblyCloseDrainsTurnsAndManualCompaction`, including concurrent Close and opening a clean successor |
| Immutable local startup registry | `composition.TestContextPolicyRegistryFailsClosed`, `TestInvalidPluginStartupCreatesNoDatabase` |
| Safe candidate validation/default parity | `contextengine.TestPolicyDefaultParityAndCandidateSafety`; the original combined `TestPolicyInvalidAndForcedDecisionsFailClosed` was split into the isolated review regressions listed below |
| Config identity and strict events | `contextpolicy.TestCanonicalConfig`, `domain.TestContextPolicyRoundTripAndLegacyOmission` |
| Frozen eval identity / no stock in-process fallback | `eval.TestCustomContextPolicyIdentityAndACPFlags` |
| Independent-module consumer test | `och_test.TestExternalLauncherACPAndPolicyEvidence`: builds a project-owned example module, drives ACP turns, compacts, shuts down, then exports SQLite evidence containing policy identity; this is not evidence of real external adoption |
| Dependency ownership | Existing architecture suite now also inspects `internal/launcher`, `sdk/och`, `sdk/contextpolicy` |

CLI tests moved with the shared implementation from `cmd/och` to
`internal/launcher`; they were not removed or replaced with SDK-only mocks.
The example has its own go.mod/go.sum and is built read-only by the root test.
No real external adopter is currently documented. Two real implementations and
a real external consumer with usage/compatibility feedback are required before
stable promotion; the self-authored example does not satisfy that gate.

## Observed validation — original implementation snapshot

Use `GOCACHE=/tmp/och-architecture-review-gocache` where the default build cache
is read-only. Loopback integration tests were executed outside the restricted
network sandbox, against local fixtures with synthetic keys, not live providers.

```sh
go test ./...
go test -race ./internal/harness/runtime ./internal/harness/composition \
  ./internal/harness/application ./internal/harness/contextengine \
  ./internal/harness/domain ./internal/harness/eval ./internal/launcher ./sdk/...
go test ./internal/docsguard ./internal/harness/architecture
go vet ./...
GOOS=darwin go build -buildvcs=false ./cmd/och ./sdk/...
GOOS=windows go build -buildvcs=false ./cmd/och ./sdk/...
```

The listed race package set passed. Targeted regression, domain codec,
composition, launcher, external ACP, documentation and architecture tests,
go vet, and both listed cross-builds passed. That full-suite run was not entirely
green: it observed two unchanged localexec failures with bwrap available:

- `TestEnforcementReportsNoneWithoutAPlatformBackend`: expects no backend even
  though the actual backend reports full filesystem/network confinement.
- `TestRunKillsOnResourceLimitSignal`: expects the PID written inside the
  namespace (2) to equal the host PID registered with its scripted cgroup quota.

These matched the baseline in that implementation environment; no localexec
source/test was edited to hide them. The subsequent review supplied by the user
reported a full-suite pass and did not reproduce either failure. That is a
reviewer-reported result, not a new full-suite run by this follow-up. Both
observations are retained; neither establishes a universal outcome across
environments. No Windows runtime, live-provider, long-duration soak, malicious-plugin
containment, or performance result is claimed by these tests.

## Review follow-up — validation mechanism and experimental API

This follow-up changes comments, tests, and documentation, not runtime behavior,
persisted formats, or OTel. The policy comment distinguishes value-copied scalars
and slice headers from shared candidate elements. The separate core-owned
boundary map is the authority for candidate eligibility.

The former combined mutation test is now isolated into:

- `TestPolicyForcedVetoIgnoresCallbackScalarMutation`: changing only the callback's
  `Force` copy cannot veto forced compaction; checks the specific rejection reason.
- `TestPolicyRejectsForgedCandidateAfterSharedElementMutation`: changing a shared
  candidate ID cannot authorize that forged ID; checks the specific rejection reason.
- `TestPolicyAcceptsOriginalCandidateAfterSharedElementMutation`: replacing the
  visible IDs cannot revoke the original legal candidate; the complete plan must
  equal the unmodified control.
- `TestPolicyHardBudgetVetoFailsClosed`: hard-budget enforcement is checked
  separately from explicit Force and candidate mutation.

Mutation experiments used temporary source files and `go test -overlay=...`,
without replacing production files in the working tree. Each overlay maps
`internal/harness/contextengine/policy.go` to a copy with only the stated mutation.

| Mutation | Observed result | Interpretation |
| --- | --- | --- |
| Move scalar capture after the callback | All `TestPolicy` tests pass | Equivalent under the current value-passed signature; capture timing is not a mutation barrier |
| Validate directly from the caller's metadata value, without scalar capture | All `TestPolicy` tests pass | Same value-semantics protection; an expected surviving equivalent mutation |
| Rebuild the eligibility map from callback-visible candidate IDs after the callback | Both forged-ID rejection and original-ID acceptance tests fail | Shared-element mutation must not become validation authority |
| Disable forced-veto rejection | Explicit-Force subtests and the hard-budget veto test fail | The actual forced-compaction guard is exercised |

The experimental label is synchronized across both SDK package docs, the example,
the root/docs indexes, and the English/Chinese architecture documents. It does not
relax durable-event integrity, replay, or rollback requirements. No external
adopter or stable compatibility commitment is claimed.

The following follow-up checks passed (using the same writable GOCACHE above):

```sh
go test -race ./internal/harness/contextengine ./sdk/contextpolicy \
  ./internal/harness/architecture ./internal/docsguard
go test ./internal/harness/application \
  -run '^TestFailedRollingSummaryRetainsAllHistoryAfterOldCheckpoint$' -count=1
go test ./sdk/och -run '^$'
git diff --check
```

The last Go command is a compile-only check, not a new external ACP integration
run. This follow-up did not rerun the full suite.

## Boundary closure — 2026-09-13

The earlier managed accessors did not close every internal escape hatch:
`Assembly.Host()` still returned a complete Host, whose `Store()` yielded the
raw SQLite store. This was not exposed by the public SDK, and the launcher only
used it to observe cancellation, but internal callers could bypass the managed
store or invoke host-only teardown. The accessor is now removed. Assembly offers
`Ready()` and receive-only `Done()` instead; `Done()` means cancellation/stopped
admission, not that resources have finished closing. The launcher uses this
narrow signal and still calls Assembly.Close.

Executable guards and regressions:

- `composition.TestAssemblyExposesOnlyManagedCapabilitiesAndLifecycleObservation`
  freezes the complete exported method set and rejects exported fields. A
  compile-time interface assertion pins the approved signatures, including the
  receive-only channel. This catches renamed accessors, not just `Host` by name.
- `composition.TestAssemblyCloseDrainsTurnsAndManualCompaction` now covers four
  combinations: turn/manual compaction and close/terminal fencing. It retains
  service/store handles and Done before cancellation; both handles must reject
  new work with `runtime.ErrNotReady`, while Done closes before blocked callback
  cleanup completes. Fencing is induced through the internal Abandon seam before
  Close, not by claiming a new end-to-end lease-expiration test; the existing
  heartbeat regressions separately exercise renewal failures and the watchdog.
- `architecture.TestProductionContextPlanningUsesSingleGateway` checks production
  references in internal/cmd/sdk. Selector references remain in the core policy
  gateway, and application references to contextengine.Plan are restricted to
  planContext. `TestPlanningBoundaryGuardDetectsBypasses` includes positive controls
  and negative source fixtures for import aliases, function values, dot imports,
  wrong functions/receivers in the approved file, and extra core selector uses.
  This is a source architecture guard, not a sandbox for malicious Go code.

Temporary Go overlays verified three actual source regressions without replacing
working-tree production files:

| Mutation | Observed rejection |
| --- | --- |
| Restore `Assembly.Host()` | Surface guard rejects additional `Host` capability |
| Add renamed `Escape() any` returning the Host | Surface guard rejects additional `Escape` capability |
| Return the raw store from Assembly.Store | All four lifecycle cases fail: cached store reads succeed after admission stops, instead of returning ErrNotReady |

Observed validation passed, with the writable GOCACHE above:

```sh
go test -race ./internal/harness/runtime ./internal/harness/composition \
  ./internal/harness/application ./internal/harness/contextengine \
  ./internal/harness/architecture ./internal/launcher ./sdk/... -count=1
go test -race ./internal/harness/composition \
  -run '^TestAssemblyCloseDrainsTurnsAndManualCompaction$' -count=10
go test ./internal/docsguard -count=1
go vet ./internal/harness/composition ./internal/harness/architecture \
  ./internal/launcher ./sdk/...
git diff --check
```

The first attempt inside the network sandbox could not open loopback listeners
for composition/launcher fixtures. The listed race command was then rerun with
approved loopback access and `-count=1`; every listed package passed, including
the independent SDK ACP integration. The repeated lifecycle command passed all
four scenarios ten times. These are targeted checks, not a new full-suite run,
live-provider exercise, or long-duration soak.

No provider/environment/storage extension, public SDK signature change, persisted
format change, or OTel implementation change is included in this boundary closure.

## Whole-repository verification and facade matrix — 2026-09-13

A subsequent `go test ./... -count=1` ran with approved local integration-test
permissions. Every package other than localexec passed, including eval, SQLite,
composition, launcher, OTel and the independent SDK ACP test. Localexec reproduced
exactly the two failures recorded in the original implementation snapshot:
`TestRunKillsOnResourceLimitSignal` could not match namespace PID 2 to the quota's
host PID; `TestEnforcementReportsNoneWithoutAPlatformBackend` observed full
filesystem/network confinement rather than the assumed absence of a backend.
No localexec source/test modifications were made. This new local observation does
not invalidate the earlier reviewer-reported pass in another environment, and it
does not support claiming that the full suite is green here.

The follow-up then expanded the facade regression from explicit read checks to
all eight Service methods and all five EventStore methods, enumerated from their
interfaces so new methods join the matrix automatically. Each of the existing
four lifecycle scenarios now checks all methods in three states: a cancelled
caller while the assembly remains ready, stopped admission before callback
cleanup, and completed teardown. Rejections must match context.Canceled or
runtime.ErrNotReady respectively; an incidental request-validation/storage error
does not count as admission enforcement. The existing valid-read negative checks
and successful session creation/operation startup remain as controls.

Two further temporary overlays bypassed admission only in managedStore.Append
and only in managedStore.ResolveAppend. The corresponding new stopped-admission
subtest failed in each case: it reached the store and returned invalid_append
instead of ErrNotReady. This checks the individual write/resolve paths, not just
the identity of the store wrapper.

The expanded lifecycle regression passed with race detection for ten repetitions:

```sh
go test -race ./internal/harness/composition \
  -run '^TestAssemblyCloseDrainsTurnsAndManualCompaction$' -count=10
go vet ./...
```

Both commands passed. The full-suite observation precedes this test-only matrix
expansion; production behavior did not change in this verification follow-up.

## Findings fixed during implementation

1. Existing materialization trusted proposed retained units even when rolling
   summary failed and only the old checkpoint survived. Coverage now comes
   solely from the committed checkpoint.
2. Runtime work cancellation did not own externally obtained service calls;
   explicit admission now includes manual compaction and store accesses.
3. Renewal attempted same-instance takeover and had no independent watchdog.
   Fencing is terminal; a stuck worker cannot suppress cancellation.
4. Assembly did not retain/close localexec. Normal and startup rollback paths
   now own it; unproven teardown does not release the lease as if safe.
5. The external round trip detected a missing strict-codec allowlist entry for
   new policy metadata that unit tests of the builtin path could not expose.
   Both context event types now have strict nested validation and round-trip
   tests; no format rewrite or permissive decoding was used.
6. A supplied diagnostics writer replaces process-global log redirection in
   launcher composition; tests now assert that explicit writer.

## 中文摘要

本记录对应工作区首阶段实现，没有创建提交或发布版本。新增回归、外部独立模块 ACP
压缩/导出与列出的 race 包集合均通过；原实施环境的全量测试有两项 localexec
失败，后续用户提供的评审报告则全量通过、未复现这两项。本轮没有重新执行全量测试；
两份不同来源的环境观测均予保留，不能泛化为必然失败。没有修改其代码来掩盖，
也没有重做 OTel adapter。
本次修复覆盖旧 checkpoint 后历史遗漏、生命周期准入脱节、阻塞续租、原地恢复接单、
命令执行器释放，以及新策略身份的严格事件解码。公开扩展仅限启动器与上下文规划，
稳定级别为 experimental，暂不承诺源码兼容；独立示例是编译/集成门禁，不是实际
外部采用证据。两个真实实现和真实外部消费者的转稳定门槛尚未满足，持久化、重放
与回滚要求不因此放宽。
本轮把强制否决、伪造候选、原合法候选仍可选与硬预算拒绝拆开验证。临时 overlay
变异中，两种仅改变标量读取方式的等价改写仍通过，这是符合 Go 值语义的结果；
改用被策略修改的 ID 重建校验集合、取消强制否决保护，均被相应回归测试检出。
本轮仅改注释、测试和文档，不改变运行逻辑或持久化格式。
未来 provider/环境/授权/存储扩展及回滚限制见中英文合同。

2026-09-13 边界收口移除了 `Assembly.Host()`，改为 `Ready()` 与只读 `Done()`；
取消通知不代表清理完成，仍须调用 `Close()`。新增导出面与规划引用守卫；关闭/隔离
两种情况下，预先取得的 service/store 均受准入约束。恢复完整 Host、改名并以 any
返回 Host、返回原始 store 三种 overlay 变异均被对应测试检出。隔离场景通过内部
Abandon 接缝触发，续租失败与 watchdog 由既有 heartbeat 回归验证，不混称端到端实测。
上述相关包 race（含独立 SDK ACP 集成）、四场景各十轮回归、文档检查和定向 vet
均通过；首次本机端口受沙箱限制后，经授权在沙箱外重跑本地模拟测试通过。未重跑全量。

随后同日继续核验，实际执行了全量 `go test ./... -count=1`：除 localexec 的上述
两项已记录失败外，其他包全部通过，不能声称本机全量为绿。本次补齐 Service 八个
方法与 EventStore 五个方法在调用方取消、停止准入、清理完成三个状态下的拒绝矩阵，
四种生命周期场景各跑十轮 race 均通过，全仓库 vet 通过。分别让 Append、ResolveAppend
绕过准入的 overlay 变异被各自新测试检出。本次仅补测试与证据，未修改生产行为。
