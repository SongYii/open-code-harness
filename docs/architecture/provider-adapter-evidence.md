# Provider Adapter Completion Evidence

- Scope: the pre-v0 internal provider adapter described by
  [Implemented Provider Adapter Contract](provider-adapter.md)
- Design: [Provider contract and first real adapter](../superpowers/specs/2026-08-15-provider-adapter-design.md)
- Status: PR 1–5 implemented and locally verified; not GA

This ledger is the public completion record. The design remains the frozen
five-PR sequence. Commit history, executable gates, and the commands below
support the completion statement.

`adapters/openaicompat` implements `engine.Model` over a scripted
`http.RoundTripper`. It is not a plugin kernel, vendor SDK, or live-network
client. Default `go test` is keyless.

## Architecture gates

| Gate | Evidence | Adopted outcome |
| --- | --- | --- |
| DeepSeek Harness sequencing | [2026-08-15 comparison](../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.md) | After EventStore v2 Slice 1, ship Provider before Tool/Policy; do not adopt Cordis / everything-is-a-plugin as the kernel |
| Provider design | [2026-08-15 design](../superpowers/specs/2026-08-15-provider-adapter-design.md) | `engine.Model` HTTP adapter, Chat Completions SSE, capability profiles, classified failures, reconstructable request/usage facts |

## PR and commit ledger

Base of this stack is `0dd0e67` parent `4881d69`. This branch already contains
PR 1–4. Short SHAs below are from `git log` on
`execute-plan/3529b50b-pr-5-docs-implemented-provider-adapter-contract-and-evi`.

| PR | Delivered evidence | Commits |
| --- | --- | --- |
| 1 | `model.request.recorded` / `model.usage.recorded`; 2-or-3-event `Decide`; reconstruction 2/3/4/5/6 shapes | `f731487`, `7259c73` |
| 2 | Capability profile, `AttemptStats`, `ProviderFailure`, `Next` remap, Application unwrap and terminal-by-type | `21f3c6e`, `7105fdb` |
| 3 | `adapters/openaicompat` scripted transport, SSE fixtures, architecture owner `net/http` + `os` | `c53231d`, `60e40af`, `4881d69` |
| 4 | `RunTurn` through openaicompat fixtures; one Stream per Request ID | `0dd0e67` |
| 5 | Implemented contract, Chinese reading copy, and this ledger | this commit |

## Executable completion gates

The following commands were run from the repository root on this branch and
exited zero:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./internal/harness/domain ./internal/harness/engine \
  ./internal/harness/application ./internal/harness/adapters/openaicompat \
  ./internal/harness/architecture -count=1
go test ./... -count=1
go test -race ./... -count=1
```

Focused tests that name the contract include:

```text
domain:     TestDecideStartAssistantTurnWithRequestReturnsThreeEvents
            TestDecideStartAssistantTurnRejectsRequestMessages
            TestDecideRecordModelUsageIsVersionOnly
            TestApplyModelFactsAreVersionOnly
            TestApplyCompactModelFactsAreVersionOnly
            TestCompactDecisionsMatchFullStateForModelFacts
            TestModelFactEventJSONRejectsNonStrictPayloads
engine:     TestRequestIdentityValidateAcceptsCompositionIdentity
            TestRequestIdentityValidateRejectsMalformedFields
            TestProviderFailureErrorNeverRendersSecrets
            TestTurnRunnerPreservesClassifiedInvalidStreamFromNext
            TestTurnRunnerDoesNotRemapClassifiedTransientAsEOF
            TestTurnRunnerCopiesCompletedUsageAndObserverStats
            TestTurnRunnerSnapshotsBeforeCancelOnFailAndCancel
application:
            TestReconstructRequestResultAcceptsExactRequestShapes
            TestReconstructRequestResultRejectsMisplacedCompanions
            TestRunTurnRequestIdentityAdmitsThreeEvents
            TestRunTurnPrependsObservedUsageBeforeTerminal
            TestRunTurnObservedStatsWithoutIdentityDoNotPersistUsage
            TestRunTurnProviderAuthPersistsClassifiedFailureCode
            TestRunTurnContentFilterPersistsPermanentUsageWithoutFinishReason
openaicompat:
            TestNewRejectsInvalidConfig
            TestStreamRequestMapping
            TestClassifyHTTPErrors
            TestRunTurnHTTPSuccessRecordsRequestUsageAndReplay
            TestRunTurnHTTP401PersistsProviderAuth
            TestRunTurnHTTP429QuotaVersusRateLimit
            TestRunTurnHTTPCancelWinsWithoutSecondStream
            TestRunTurnHTTPFindCommandRequestPreventsSecondStream
architecture:
            TestProductionDependencyBoundaries
            TestForbiddenImport
            TestClassifyProductionDirectory
```

`go.mod` contains only the module path and `go 1.26`. No vendor SDK import is
present.

## Deferred blockers

This milestone is complete only within its stated internal scope. The
following remain unimplemented and are not implied by this ledger:

- Tool Runtime, Policy, approvals, MCP
- SQLite canonical EventStore, JSONL audit replica
- durable Runtime host and crash recovery
- ACP v1 adapter and TypeScript TUI
- Context Engine, prompt construction, compaction
- Application retry, multi-provider routing, vendor SDKs
- live-network or live-key CI
- plugin kernel

GA remains blocked on those milestones.

---

## 中文证据台账

- 范围：[已实现 Provider Adapter 合同](provider-adapter.zh-CN.md)所定义的 pre-v0 内部合同
- 设计：[Provider 合同与第一个真实 Adapter](../superpowers/specs/2026-08-15-provider-adapter-design.zh-CN.md)
- 状态：PR 1–5 已实现并完成本地验证；不是 GA

本台账是公开完成记录。设计保留为冻结的五步 PR 顺序。完成结论由提交历史、
可执行门和下述验证命令共同支撑。

`adapters/openaicompat` 在 scripted `http.RoundTripper` 上实现 `engine.Model`。
它不是插件内核、厂商 SDK 或连网客户端。默认 `go test` 无密钥。

### 架构门

| 架构门 | 证据 | 已采纳结果 |
| --- | --- | --- |
| DeepSeek Harness 交付顺序 | [2026-08-15 对照](../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.zh-CN.md) | EventStore v2 Slice 1 之后先做 Provider，再做 Tool/Policy；不把 Cordis / 一切皆插件当内核 |
| Provider 设计 | [2026-08-15 设计](../superpowers/specs/2026-08-15-provider-adapter-design.zh-CN.md) | `engine.Model` HTTP 适配器、Chat Completions SSE、Capability Profile、分类失败、可重建请求/用量事实 |

### PR 与提交

本栈基线为 `0dd0e67` 的父提交 `4881d69`。本分支已包含 PR 1–4。短 SHA 来自
本分支 `git log`。

| PR | 交付证据 | 提交 |
| --- | --- | --- |
| 1 | `model.request.recorded` / `model.usage.recorded`；2 或 3 事件 `Decide`；重建 2/3/4/5/6 形状 | `f731487`、`7259c73` |
| 2 | Capability Profile、`AttemptStats`、`ProviderFailure`、`Next` 重映射、Application unwrap 与按类型找终态 | `21f3c6e`、`7105fdb` |
| 3 | `adapters/openaicompat` scripted transport、SSE fixture、架构 owner `net/http` + `os` | `c53231d`、`60e40af`、`4881d69` |
| 4 | 经 openaicompat fixture 跑通 `RunTurn`；一个 Request ID 一次 Stream | `0dd0e67` |
| 5 | 已实现合同、中文阅读版与本台账 | 本提交 |

### 可执行完成门

上述英文节中的 `gofmt`、`go vet`、focused package、`go test ./...` 与
`go test -race ./...` 均已在本分支仓库根目录执行且退出码为零。

### 剩余阻塞

本里程碑只在其合同范围内完成。Tool/Policy、SQLite、JSONL、Runtime Host/
恢复、ACP、TUI、Context Engine、Application 重试、厂商 SDK、连网 CI 和插件
内核仍未实现，不能由本台账暗示。GA 仍被这些后续里程碑阻断。
