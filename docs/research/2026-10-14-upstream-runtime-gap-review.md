# 2026-10-14 官方源码基准与 Harness 差距核对

本次面向 `fix/mac-mini-ci` 的可验收目标是：nightly 失败可在功能分支复核；命令输出与回收不确定时，不产生普通成功结果、不让模型在同一 Turn 自动重试；落盘、实时通知、追踪与重新加载结果保持同一失败代码。本文是中文原始核对记录。通过这些契约不能证明产品整体与行业项目打平。

## 比较对象与固定版本

| 对象 | 固定源码 | 比较层次 |
| --- | --- | --- |
| Codex CLI rust-v0.162.1 | `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d` | 终端编码代理产品；本次只比较命令生命周期、输出收集和安全边界 |
| LangGraph 1.2.15 | `08a345637fb2bd499f69c90056a22c478a468817` | 状态图编排框架；比较恢复、pending writes、持久化策略，不作为 OS 沙箱基准 |
| OpenHands UI v1.25.0 | `50c68a12c9bd92faf16db8c29681190f467ca726` | 产品/UI；它的 [package.json](https://github.com/OpenHands/OpenHands/blob/50c68a12c9bd92faf16db8c29681190f467ca726/package.json) 依赖 TypeScript client 1.53.0 |
| OpenHands software-agent-sdk v1.53.0 | `54daf056bd863bb46f922a2fe9324dd736b37ff6` | 执行/会话 SDK；与上述 UI 分开固定，不混用旧 Python runtime |

没有安装或执行上游项目，以下是源码核对加 Harness 本地行为测试，不是相同机器、任务、模型的横向性能测量。现场同时记录了上游 main SHA，但不把正在变化的 main 当作本文依据。

## 源码证据与可复现差距

| 契约 | 上游代码依据 | Harness 当前证据 | 差距与下一项验收 |
| --- | --- | --- | --- |
| 命令超时、取消、yield 分开 | Codex [exec.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/exec.rs) 的 `ExecExpirationOutcome`；[oneshot.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/core/src/unified_exec/oneshot.rs) 的执行预算 | `adapters/localexec/runner.go`：调用方 Context 与 CommandSpec.Timeout 分开；`TestCancelKillsProcessGroup` / `TestTimeoutKillsProcessGroup` | Harness exec 是同步调用，没有交互命令 yield/session 产品面。yield 只是交回控制权，不能用对应测试名证明进程已杀。下一项需明确 job/session 与严格截止时间的独立契约。 |
| 继承 pipe 不得无限等待 | Codex `exec.rs` 的 IO drain 上限 | `output_capture.go` 的独立 500ms pipe drain（`confined.go` 保留 raw command 的 WaitDelay）；本轮 `TestRunCannotReportOrdinaryResultAfterPipeDrainDeadline` 使用真实 leader/后代/pipe、登记握手、独立有限寿命 | 原实现把 ErrWaitDelay 变成 `exit -1` 普通结果；非零退出还会掩盖这个错误，因此本轮独立收集管道结果。本轮转成 `exec_output_incomplete` 并结束 Turn。不会宣称后代脱离进程组后也能回收。 |
| 进程组回收与父进程死亡 | Codex [process_group.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/utils/pty/src/process_group.rs) 的组信号、Linux parent-death signal | `command_lifetime.go` 单 Wait、有界终止；`resource_monitor.go` 记录 Darwin 组身份；`TestDarwinTerminationCannotClaimSuccessWhileGroupDescendantsLive` 负对照故意只杀 leader | 本轮将回收不明保留为 `exec_teardown_unproven`，取消不能覆盖它；Runner 闩锁拒绝后续 Run/ConfinedCommand.Start。仍缺 Linux/Mac 的脱组后代、Harness 被强杀后的全面回收契约与验收。原始 Cmd().Start 是低层调用，不能声称受到闩锁约束。 |
| 输出有界 | Codex `exec.rs` 的捕获上限与通知计数 | `runner.go` 合并 stdout/stderr 的 64KiB cap；`TestOutputCapKillsAndTruncates` | byte cap 已有真实测试。无限输出 + 慢消费者 + 父进程/树 RSS 的整套压力实验本轮未完成。MCP、Provider、exec 的边界需各自测量，不能借一处 cap 推导所有通道。 |
| 沙箱与资源保证独立 | Codex [sandboxing/manager.rs](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/sandboxing/src/manager.rs) 的环境/策略选择 | `enforcement.go` 分 filesystem/network/memory/CPU；既有 required bwrap CI；[Mac 验证记录](../validation/mac-mini-resource-repair.md) | Mac CPU/RSS 为 partial 采样，Seatbelt 当前不可用。Linux cgroup 需要实际委派；没有 VM、凭据或系统权限修改。下一项必须在能提供隔离/委派的独立环境验证，不能升级文字声明。 |
| 检查点与恢复 | LangGraph [pregel/_loop.py](https://github.com/langchain-ai/langgraph/blob/08a345637fb2bd499f69c90056a22c478a468817/libs/langgraph/langgraph/pregel/_loop.py) 的 checkpoint/pending writes 和 durability；[types.py](https://github.com/langchain-ai/langgraph/blob/08a345637fb2bd499f69c90056a22c478a468817/libs/langgraph/langgraph/types.py) 的恢复输入 | SQLite EventStore + `runtime/reconcile.go`；`TestReconcileInterruptedTool`、`TestRecoveryLostAcknowledgementResolvesThroughReplay`；`TestMessagesProcessCrashRecovery` 真实 SIGKILL 边界 | Harness 当前恢复会关闭悬空 Tool/Turn，保留不确定结果，不自动继续原执行。没有 LangGraph 通用并行节点恢复图；A 成功/B 失败只重跑 B 的契约没有实现。若新增并行图，要固定 checkpoint backend 版本、跨新进程验证，InMemory 不算持久化。 |
| 超时不能靠线程取消冒充 OS 回收 | LangGraph [pregel/_executor.py](https://github.com/langchain-ai/langgraph/blob/08a345637fb2bd499f69c90056a22c478a468817/libs/langgraph/langgraph/pregel/_executor.py) 的 sync/async 等待；[_retry.py](https://github.com/langchain-ai/langgraph/blob/08a345637fb2bd499f69c90056a22c478a468817/libs/langgraph/langgraph/pregel/_retry.py) 的 async timeout 和晚到 writes 抑制 | Runtime admission/drain 的拒绝与等待；localexec 独立管进程 | graceful drain 不会杀正在运行的同步线程；async cancel 也不是 OS kill。下一项对拒绝取消的 Provider/MCP/扩展分别验证晚到结果不得再提交。 |
| 结果先落盘，再对外通知 | OpenHands [local_conversation.py](https://github.com/OpenHands/software-agent-sdk/blob/54daf056bd863bb46f922a2fe9324dd736b37ff6/openhands-sdk/openhands/sdk/conversation/impl/local_conversation.py) 的持久化 callback 排在外部 callback 前；中断无 Observation 的 Action 补 AgentError | Application `commitTerminalAppend` + 本轮命令故障通知；`TestUnprovenCommandEndsTurnWithMatchingDurableAndLiveFailure` 核对单终态和结果重建；`TestAssemblyPipeDrainFailurePersistsAcrossReopen` 验证真实命令/SQLite、先落盘后通知及重开不重跑 | 合成错误或 Failed 事实不证明外部副作用没有发生。本轮错误文本明确要求先检查再重试。跨外部系统的 exactly-once 不成立；需幂等键或独立意图/结果协议。 |
| 完整编码产品体验 | Codex CLI/TUI 与 OpenHands UI 是产品面；LangGraph 是框架 | ACP v1、CLI、实验 web bridge；README 明确完整 TypeScript TUI 未实现 | 项目选择/连接诊断、跨设备体验、模型任务质量、成本与端到端编码基准未测。功能列表和测试数量不能作为产品打平结论。 |

## 本轮实现边界

新增 tools 端口错误区分输出不完整、等待结果不明和回收不明。Application 将它们提交成一致的 ToolCallFailed + TurnFailed，给出固定代码；继续 Provider 的路径停止。重新构建 Service 后同 RequestID 读取原失败，不执行命令。ACP 的已提交 failed 结果优先于迟到取消，`TestCommittedPromptFailureCannotBecomeSuccessfulCancellation` 使用真实协议取消核对不会回成普通 cancelled。回收闩锁作用于已有 Runner 的受管启动，不会清理一个未知外部进程，也不会在新进程重启后持久化该闩锁。确认残留资源和副作用后才能由操作者恢复执行环境。

公开引用的三次尝试共享 30 秒预算，仅重试 429/502/503/504 与临时网络错误；404、403、持续 503 仍失败。既有两项历史 dead-link 记录保留。新增 workflow_dispatch 可在功能分支启用 context-matrix/soak；手动验收不触发自动 nightly issue 通知；scheduled reporter 同时纳入 Darwin 原生资源 lane，避免漏报。gRPC 从 v1.83.1 升至修复版 v1.83.2（根模块及两个独立 SDK 示例），处理 [GO-2026-6443](https://pkg.go.dev/vuln/GO-2026-6443)；升级前本项目没有到达该问题符号的调用链。

## 验收入口

在隔离 checkout 使用已核验 Go 1.26.9 或同系列后续修复版本。以下均为 fixture，不使用模型凭据：

```sh
go test -race ./internal/docsguard -run '^TestCitation' -count=1
DOCSGUARD_CHECK_EXTERNAL_LINKS=1 go test ./internal/docsguard -run '^TestExternalCitationsResolve$' -count=1 -v
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go test -race ./internal/harness/adapters/localexec -run '^Test(RunCannotReportOrdinaryResultAfterPipeDrainDeadline|DarwinTerminationCannotClaimSuccessWhileGroupDescendantsLive)$' -count=3 -timeout=2m
go test -race ./internal/harness/application -run '^TestUnprovenCommand' -count=1
go test -race ./internal/harness/runtime -run '^Test(ReconcileInterruptedTool|RecoveryLostAcknowledgementResolvesThroughReplay|DrainTimeoutIsTruthfulAndAdmissionStaysClosed)$' -count=1
```

补充真实 SQLite 闭环：`go test -race ./internal/harness/composition -run '^TestAssemblyPipeDrainFailurePersistsAcrossReopen$' -count=1`；取消与 drain 竞态：`go test -race ./internal/harness/adapters/localexec -run '^TestRunCancellationWhileOnlyDescendantHoldsOutput$' -count=10`。

Darwin 残留负对照只在 Darwin 编译/运行；没有在 Linux 运行该测试的证据。完整离线回归、精确 SHA 的 CI 和本地运行明细见本轮验收交付记录。人工 extended lane 命令为：

```sh
gh workflow run ci.yml --repo SongYii/open-code-harness --ref fix/mac-mini-ci -f extended_validation=true
```

下一优先项是可观测的执行诊断与独立 Linux 树生命周期核对，再讨论并行恢复或完整 UI。行业比较继续按真实任务的成功/失败、故障边界与原始证据迭代。
