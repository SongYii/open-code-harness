# Tool Runtime Completion Evidence

- Scope: the pre-v0 internal tool runtime described by
  [Implemented Tool Runtime Contract](tool-runtime.md)
- Design: [Tool Runtime, Policy, and minimal workspace tools](../superpowers/specs/2026-08-16-tool-runtime-policy-design.md)
- Status: execute-plan PRs 1–9 implemented and locally verified; not GA

This ledger is the public completion record. The design remains the frozen
PR sequence (design PRs 1–7 plus this docs PR). Commit history, executable
gates, and the commands below support the completion statement.

Application owns the Step loop. Policy is a pure Decide table. Four
builtins run behind ports. `workspacefs` and `localexec` are not an OS
sandbox. Default `go test` is keyless and uses `t.TempDir()` only.

## Architecture gates

| Gate | Evidence | Adopted outcome |
| --- | --- | --- |
| DeepSeek Harness sequencing | [2026-08-15 comparison](../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.md) | After Provider, ship Tool/Policy before SQLite / ACP / TUI; do not adopt Cordis / everything-is-a-plugin as the kernel |
| Tool/Policy design | [2026-08-16 design](../superpowers/specs/2026-08-16-tool-runtime-policy-design.md) | Application-owned Step loop, pure Policy Decide, four builtins, mid-loop EventStore resolve, no plugin kernel |

## PR and commit ledger

Base of this stack is parent `ca2fe5d` (`Merge pull request #13`). This
branch already contains execute-plan PRs 1–8. Short SHAs below are from
`git log` on
`execute-plan/3f46444a-pr-9-docs-tool-runtime-implemented-contract-and-evidenc`.

| PR | Delivered evidence | Commits |
| --- | --- | --- |
| 1 | Domain tool/policy/approval events; item-only assistant complete; `InterruptToolTurn` / `FailToolTurn`; `FinishReason=tool_calls` | `eeb2e02` |
| 2 | Engine `tool_call` grammar; `ModelRequest.Messages` / `Tools`; unique call ids | `f251db9`, `c2d63e7` |
| 3 | Pure `policy.Decide`; shipped `ModeAllowWrites`; `NewService` default `ModeDefault` | `153811d`, `2faf939` |
| 4 | `tools` catalog, JSON Schema subset, lexical scope, executor ports; `list_dir` depth omitted ≡ 1 | `c06ee8a`, `38c92c2` |
| 5 | Reconstruct multi-step and tool-item CommandID walks; old 2–6 shapes remain legal | `af0efdd` |
| 6 | Bounded Step loop, pipeline, mid-loop `step_append_*` + `ResolveAppend` (table A2) | `3d97b50`, `72b02e7` |
| 7 | `workspacefs` realpath jail; `localexec` `enforcement=partial` | `235a5b8` |
| 8 | `openaicompat` send `tools` and assemble `tool_call*` when `NativeTools` is supported\|required | `d331bf5`, `2c9a169` |
| 9 | Implemented contract, Chinese reading copy, and this ledger | this commit |

## Executable completion gates

The following commands were run from the repository root on this branch and
exited zero:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
```

Focused tests that name the contract include:

```text
policy:     TestNewAcceptsShippedModes
            TestDecideDefaultTable
            TestDecideAllowWritesEveryCell
            TestDecideDoesNotInterpretPathLiteral
tools:      TestDefaultWorkspaceSpecsLockedContracts
            TestValidateArgsDefaultWorkspaceSpecs
            TestCheckScopeLexicalDeniesEscapesWithoutIO
            TestNewCatalogRejectsUnsupportedSchemaKeywords
domain:     TestDecideCompleteAssistantMessageReturnsItemOnly
            TestDecideToolTurnCompositesAndBareTurnRejection
            TestApplyAssistantMessageCompletedWithToolCallsLeavesTurnRunning
            TestApplyCompactToolItemAndEligibility
engine:     TestTurnRunnerAcceptsTextThenOneToolCall
            TestTurnRunnerAcceptsTwoReadFileCallsWithDistinctIDs
            TestTurnRunnerRejectsDuplicateToolCallIDs
            TestTurnRunnerForwardsMessagesAndToolsWithoutConsultingProfile
application:
            TestNewServiceToolComposition
            TestTwoStepReadFileSuccess
            TestOversizedToolBatchFailsWithoutStarting
            TestMaxStepsFailsAfterDurableTools
            TestEnvelopeLimitFailsWithoutStream
            TestLoggedEnvelopePersistsAtBudget
            TestCancelDuringExecuteUsesInterruptToolTurn
            TestToolDenialsContinueTurnWithFrozenText
            TestTableA2UnknownStartedDoesNotExecute
            TestTableA2UnknownToolTerminalDoesNotReexecute
            TestTableA2UnknownStepStartDoesNotStream
            TestReconstructRequestResultAcceptsTwoStepSuccess
            TestReconstructRequestResultAcceptsInterruptToolTurn
            TestEmptyCatalogKeepsNilMessagesAndTools
workspacefs:
            TestListDepthAndCap
            TestResolveSymlinkEscapeDoesNotReadOrWrite
            TestResolveDoesNotCreateOrTruncate
localexec:  TestEnforcementPartial
            TestArgvOnlyNoShellExpansion
            TestScrubbedEnv
            TestTimeoutKillsProcessGroup
openaicompat:
            TestNewAcceptsNativeToolsSupportedAndRequired
            TestStreamSendsToolsAndMessages
            TestStreamAssemblesSupportedToolCalls
            TestStreamJustUnder4MiBProjectionAccepted
            TestRunTurnHTTPReadFileThenCompletes
architecture:
            TestProductionDependencyBoundaries
            TestForbiddenImport
            TestOsExecOnlyInLocalExec
            TestAllowAllProductionException
```

`go.mod` contains only the module path and `go 1.26`. No vendor SDK import is
present.

## Deferred blockers

This milestone is complete only within its stated internal scope. The
following remain unimplemented and are not implied by this ledger:

- MCP client, MCP transport, remote tool hosts
- OS Seatbelt / bwrap / Landlock sandbox backends
- ACP approval UI and TypeScript TUI
- parallel tools, background exec, PTY, web fetch, apply-patch
- SQLite canonical EventStore, JSONL audit replica
- durable Runtime host and crash continuation of a running Step loop
- Context Engine, prompt construction, compaction
- Application retry, multi-provider routing, vendor SDKs
- live-network or live-key CI
- plugin kernel

GA remains blocked on those milestones.

---

## 中文证据台账

- 范围：[已实现 Tool Runtime 合同](tool-runtime.zh-CN.md)所定义的 pre-v0 内部合同
- 设计：[Tool Runtime、Policy 与最小工作区工具](../superpowers/specs/2026-08-16-tool-runtime-policy-design.zh-CN.md)
- 状态：execute-plan PR 1–9 已实现并完成本地验证；不是 GA

本台账是公开完成记录。设计保留为冻结的 PR 顺序。完成结论由提交历史、
可执行门和下述验证命令共同支撑。

Application 拥有 Step 循环。Policy 是纯 Decide 表。四个内置工具走端口。
`workspacefs` 与 `localexec` 不是 OS 沙箱。默认 `go test` 无密钥，且只用
`t.TempDir()`。

### 架构门

| 架构门 | 证据 | 已采纳结果 |
| --- | --- | --- |
| DeepSeek Harness 交付顺序 | [2026-08-15 对照](../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.zh-CN.md) | Provider 之后做 Tool/Policy，再做 SQLite / ACP / TUI；不把 Cordis / 一切皆插件当内核 |
| Tool/Policy 设计 | [2026-08-16 设计](../superpowers/specs/2026-08-16-tool-runtime-policy-design.zh-CN.md) | Application 拥有 Step 循环、纯 Policy Decide、四个内置工具、中途 EventStore resolve、无插件内核 |

### PR 与提交

本栈基线为 `ca2fe5d`（`Merge pull request #13`）的父提交。本分支已包含
execute-plan PR 1–8。短 SHA 来自本分支 `git log`。

| PR | 交付证据 | 提交 |
| --- | --- | --- |
| 1 | 领域工具/策略/审批事件；助手完成拆成 Item-only；`InterruptToolTurn` / `FailToolTurn`；`FinishReason=tool_calls` | `eeb2e02` |
| 2 | Engine `tool_call` 语法；`ModelRequest.Messages` / `Tools`；唯一 call id | `f251db9`、`c2d63e7` |
| 3 | 纯 `policy.Decide`；已交付 `ModeAllowWrites`；`NewService` 默认 `ModeDefault` | `153811d`、`2faf939` |
| 4 | `tools` 目录、JSON Schema 子集、词法 scope、执行端口；`list_dir` depth 省略 ≡ 1 | `c06ee8a`、`38c92c2` |
| 5 | 重建多 Step 与工具 Item 的 CommandID 走查；旧 2–6 形状仍合法 | `af0efdd` |
| 6 | 有界 Step 循环、管线、中途 `step_append_*` + `ResolveAppend`（表 A2） | `3d97b50`、`72b02e7` |
| 7 | `workspacefs` realpath 监狱；`localexec` `enforcement=partial` | `235a5b8` |
| 8 | `openaicompat` 在 NativeTools supported\|required 时发送 `tools` 并组装 `tool_call*` | `d331bf5`、`2c9a169` |
| 9 | 已实现合同、中文阅读版与本台账 | 本提交 |

### 可执行完成门

上述英文节中的 `gofmt`、`go vet`、`go test ./...` 与 `go test -race ./...`
均已在本分支仓库根目录执行且退出码为零。

### 剩余阻塞

本里程碑只在其合同范围内完成。MCP 客户端、OS 沙箱、ACP/TUI、并行工具、
SQLite、JSONL、Runtime Host/崩溃续跑、Context Engine、Application 重试、
厂商 SDK、连网 CI 和插件内核仍未实现，不能由本台账暗示。GA 仍被这些后续
里程碑阻断。
