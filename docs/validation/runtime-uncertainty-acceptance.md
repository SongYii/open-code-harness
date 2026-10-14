# Harness 命令结果不确定性验收

核心功能提交：`920658c60fcadcbbb23ac3d4b2e555b5ba02e07a`；本文所在追加提交修复 ACP 对已提交失败与迟到取消的优先级。分支 `fix/mac-mini-ci`，基于已验收的 `64a643e`。本文是中文验收指南。main、GUI 和其他开发进程未修改；未合并或部署。

## 可以验收的变化

1. 命令进程退出与输出收集分别判定。真实后代持有 stdout/stderr 时，500ms drain 到期产生明确失败；0 和非零退出均覆盖。正常完整输出的非零退出仍提供其已知退出码。
2. `exec_output_incomplete`、`exec_wait_unproven`、`exec_teardown_unproven` 结束当前 Turn，禁止同一轮模型继续重试。错误文本保留副作用可能已发生的事实。回收不明时，默认 Runner 拒绝后续受管进程启动。
3. 同一失败代码进入 ToolCallFailed、TurnFailed、Application 内部运行时通知和追踪。ACP 对外工具卡显示 failed 和固定说明，JSON-RPC 错误仍为通用 -32603；本文的稳定代码由 transcript 核验。先落盘，再通知。已提交的失败优先于迟到取消，ACP 不再把它改写为普通 cancelled。重建 Application 或关闭并重开 SQLite 后，同 RequestID 返回原结果；提交结果不明时不伪造终态、不发失败完成通知、不重新执行。
4. 文档链接仅对临时状态做三次有界重试，共享 30 秒预算；404、403 与持续 503 仍失败。功能分支可手动启用完整 nightly lane，手动运行不发送 nightly issue 通知。scheduled reporter 纳入 Darwin lane。
5. 既有 Go 1.26.9/x/net 修复重新通过验证；根模块及两个独立 SDK 示例将 gRPC 1.83.1 升为修复版 1.83.2。详细扫描已经无已知漏洞报告。

## 真实行为证据

在本机从功能 SHA 编译独立 `och`，通过 stdio ACP 和只监听 loopback 的 SSE Provider fixture 验收。使用的只是脚本自己设置的固定 fixture 字符串，没有读取真实模型凭据。

| 场景 | 实际结果 | Provider 请求数 |
| --- | --- | --- |
| 完整输出，正常 `exit 7` | tool completed，内容保留 `exit 7`，Turn 正常结束 | 2（调用工具前、消费结果后） |
| 后代持 pipe，leader `exit 0` | tool failed；`exec_output_incomplete`；Turn failed | 1 |
| 后代持 pipe，leader `exit 7` | tool failed；`exec_output_incomplete`；Turn failed | 1 |
| 第二个真实 och 进程加载上述三个 Session | 加载的工具状态分别为 completed/failed/failed，两个副作用文件仍各只有一行 | 0 次新请求 |

共 3 次 allow-once 审批。第一进程 PID 16034、第二进程 PID 16058；它们均正常退出。两个失败案例的实际文件副作用已经完成，因此这里的 Failed 不能解读成“命令没有产生效果”。命令临时目录被回收，本轮 fixture 进程检查为空，继承 CPU/AS 限制仍为无限制。

组合级 `TestAssemblyPipeDrainFailurePersistsAcrossReopen` 另外使用真实 SQLite、审批、命令与本地 HTTP/SSE，验证失败落盘先于对外通知。此测试的 close/reopen 发生在同一 Go 测试进程；上表的独立 CLI 实验才是两个真实进程的历史加载证据。它不是运行中工具被 SIGKILL 后自动恢复执行的证明。

## 检查结果与范围

- 提交前完整离线 race：36 个包，4205 个测试实例通过；最终修改的命令故障与组合用例又作专项确认。
- 功能 SHA 的 Mac 原生 localexec：`-race -count=10`，422.96 秒，1050 个测试实例通过。20 次 skip 全部来自两项当前环境不可用的 Seatbelt 隔离测试，资源测试无 skip。
- 文档门禁、公开引用门禁、go vet、go build、模块 tidy diff 和两个独立 SDK 示例检查通过。公开引用仍明确保留既有两项历史 dead-link 豁免，不能写成每个历史引用均已验证内容。
- 核心 SHA 的 [普通 CI](https://github.com/SongYii/open-code-harness/actions/runs/38610450667) 已有 7 个适用 job 全部通过。追加提交的普通 CI 和核心 SHA 的扩展 nightly 结果随最终交付消息提供；不能把排队或运行中写成通过。

## 本机复现

使用隔离目录 `task-2/harness`。本机已有项目专用 Go 1.26.9/Node 22 环境可用，不修改系统默认工具链：

```sh
source ../harness-env.sh
git rev-parse HEAD
go test -race ./internal/harness/application -run '^TestUnprovenCommand' -count=1
go test -race ./internal/harness/adapters/localexec -run '^Test(RunCannotReportOrdinaryResultAfterPipeDrainDeadline|RunCancellationWhileOnlyDescendantHoldsOutput|DarwinTerminationCannotClaimSuccessWhileGroupDescendantsLive|DarwinSignalRechecksExitRace)' -count=3 -timeout=2m
go test -race ./internal/harness/composition -run '^TestAssemblyPipeDrainFailurePersistsAcrossReopen$' -count=1 -timeout=1m
go run golang.org/x/vuln/cmd/govulncheck@latest -show verbose ./...
DOCSGUARD_CHECK_EXTERNAL_LINKS=1 go test ./internal/docsguard -run '^TestExternalCitationsResolve$' -count=1 -v
```

协议取消复核：`go test -race ./internal/harness/adapters/acp ./internal/harness/application -run '^Test(CommittedPrompt|UnprovenCommand)' -count=10`。

Darwin 专用测试只在 Darwin 运行。全新 checkout 的完整 suite 需要先按 CI 构建 `cmd/acp-web-bridge/web` 的前端资源；这里的已验收 checkout 已完成该构建。独立 CLI 黑盒脚本和原始 stdout/JSONL、数据库、marker、构建信息在本机 `task-2/evidence/nightly-research/`，每次使用新的目录保存，不覆盖旧失败证据。

扩展 CI 的入口：

```sh
gh workflow run ci.yml --repo SongYii/open-code-harness --ref fix/mac-mini-ci -f extended_validation=true
```

## 边界与下一项

[固定版本的官方源码比较和差距清单](../research/2026-10-14-upstream-runtime-gap-review.md) 区分 Codex 产品执行层、LangGraph 编排层、OpenHands UI 与 software-agent-sdk。没有运行这些上游项目做同任务横向评测，不能宣称行业整体打平。

Mac CPU/RSS 仍是 partial 采样；Seatbelt 当前不可用。Linux cgroup 全保证仍需实际委派，Windows/Darwin amd64 只有交叉构建证据。原始 `Cmd().Start` 属于低层调用，不受新的受管启动闩锁约束；闩锁也不会跨新进程持久化。脱离进程组的后代、父进程强杀后 OS 树资源的全面回收、无限输出加慢消费者的父进程/树 RSS 压力测量、通用并行节点恢复与真实模型编码质量均未完成。

相同最终 fixture 已在独立 `64a643e` 源码快照复现旧错误：0 退出被呈现为 `exit -1`，7 退出被呈现为普通 `exit 7`，均无错误返回。快照只注入最终测试和用于断言编译的错误常量声明，未修改旧执行逻辑；当前分支未回退。

下一项应在独立 Linux 环境实测：正常 leader 退出且后台成员关闭 stdio 后，是否仍有存活进程或迟到副作用。源码疑点是没有采样 monitor 时 `cleanupGroup(nil)` 不做正常结束清理；本轮没有把这一源码推断写成已经复现或已经修复。随后补执行诊断，再考虑可交互长命令和并行恢复。

main 的 nightly issue #214 保持原状；功能分支检查通过也不会自动修复 main。合并后仍需观察 main 的真实 scheduled run。
