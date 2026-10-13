# Mac mini 阶段验收与资源限额阻碍

日期：2026-10-13（UTC）。本报告以中文为权威文本，描述实测结果，不改变安全合同。
功能修复基线：`5b4971b29a91af132d03fa36cbc08938086ff397`，分支 `fix/mac-mini-ci`。
本轮没有真实模型请求、额外系统授权或 GUI/手机动作；原 main 没有切换或合并。

## 已通过的范围

| 检查 | 结果与范围 |
| --- | --- |
| 工作区别名与规范词法路径 | 真实 workspace alias 正向通过；read/list 的反斜杠参数采用已校验的 ScopeResult.Clean，子目录 AGENTS.md 进入后续请求；两种 symlink 越界拒绝仍通过 |
| 静态与相关回归 | application、architecture、workspacefs race 回归、vet、build 通过 |
| 对应远端 CI | run `38435813653` checkout 上述 SHA，6 个适用 job 成功；包含 Linux race、三轮重复 race、漏洞扫描、bwrap 隔离、Darwin/Windows 交叉 build+vet |
| 显式夜间 Context 矩阵 | 9 个 fixture 集合全部运行，含 4 个 in-process 与 5 个 ACP 集合；54.27 秒，退出码 0，无 skip。实际使用本地模拟 Provider 和新建 och 子进程，不是真实模型质量评测 |

矩阵命令：

```sh
OCH_EVAL_SCHEDULED_CONTEXT_MATRIX=1 go test -json -race ./cmd/och-eval \
  -run '^TestContextScheduledLane' -count=1 -timeout=8m
```

另有 12 分钟进程截止和 6 分钟单测试 watchdog。新增证据仅在本地 `evidence/`。

## 十轮 soak 未通过

```sh
go test -json -race ./... -count=10 -timeout=40m
```

额外设置整个进程 35 分钟截止、单测试 6 分钟 watchdog，未与 Context 矩阵叠加。
实际运行 1312.06 秒，退出码 **1**，watchdog 未触发：35 个测试包通过，
`internal/harness/adapters/localexec` 失败。
`TestRlimitBracketSetsAndRestoresRLIMIT_CPU` 第一轮通过、后 9 轮失败，软限制为 2 而非 30。
冷启动独立单测也失败（退出码 1）：恢复后 `{Cur:30, Max:31}`，原值为无限。
失败日志未删除，未改断言、资源限额或 OS 权限让检查变绿。

原因是 `beginRlimitBracket` 在 agent 父进程中降低 CPU 硬上限，随后恢复操作被拒绝，
代码忽略恢复错误；每个 Runner 的 mutex 也无法序列化多个 Runner 对进程级限额的修改。
相关 Darwin 实现与原 main 的文件哈希相同，是既有运行时缺陷。
任务执行环境的 CPU/地址空间限制在探针前后均为无限，没有被测试子进程污染。

## 独立子进程资源探针与修复边界

| 探针 | 实测 |
| --- | --- |
| CPU 硬限制恢复 | 降为 1/2 后不能恢复原无限硬上限，独立子进程仍为 1/2 |
| 忽略 SIGXCPU | 已设置软/硬值 1/2 的 C 忙循环忽略 SIGXCPU 后运行到 3.000356 秒 CPU 时间，自行退出 42；墙钟 3.988 秒，12 秒 watchdog 未触发。不能凭配置值宣称本机强制硬终止 |
| RLIMIT_AS | 使用与原实现相同的资源编号 5 和 768 MiB 软上限，原生 setrlimit 返回 -1 / EINVAL(22)，限制保持无限；原代码也忽略该设置错误 |

拟采用共享 `buildConfinedCommand` 的 trusted child-only launcher，在子进程设置限额，
再 exec 同一原命令；保留位置参数、LookPath/ErrDot、环境、进程组和原 Seatbelt 包装。
不会在 Go 手工 fork 后运行普通 runtime 代码，也不会只降低父进程 softlimit。

但不能把 launcher 配好等同于原文档的硬保证：本机 kernel 的 CPU 忽略信号行为及
RLIMIT_AS 拒绝需要能力合同决策。最小系统 shell 草案在地址空间设置失败时立即退出 125，
不启动目标，因此现有命令测试被阻断；shell 还需处理额外 SHLVL 等环境项。
草案未完成、未提交，已撤回；本地保留实验 patch 和失败证据。

下一步需选择并审阅：保留内核设置、准确披露部分/不可用资源保护的执行合同，
或设计额外的子进程 CPU/内存监控以满足更强保证。新 native launcher 的构建部署及
外部资源监控属于需要单独审阅的执行边界，不在本轮擅自加入。
不能通过去掉 hardlimit、忽略 setup 错误、放宽权限或跳过恢复断言解决此阻碍。

## 隔离测试的真实范围

- Mac `TestSeatbeltConfinementDeniesWritesOutsideWorkspace` 和
  `TestSeatbeltConfinementDeniesNetwork` 因 `sandbox-exec probe failed: signal: abort trap` 跳过。
  正常宿主入口重跑仓库既有最小 deny-default 探针仍 SIGABRT（退出 -6），未绕过 OS 限制。
- Linux CI 的真实 bwrap 文件系统、网络、宿主进程隐藏测试通过。
- `TestCgroupCPUQuotaThrottlesParallelWork`、`TestCgroupMemoryQuotaKillsAMemoryGrowingCommand`、
  `TestCPUControllerFailureLeavesMemoryQuotaActive` 因宿主 CPU/memory cgroup delegation
  不可用而跳过。job 成功不代表这些实际 quota 已验证。
- Darwin/Windows job 在 Ubuntu 交叉 build+vet，未原生执行。
- soak 的浏览器互操作因自动发现未找到 Chrome 跳过；显式 MCP 集合未 opt-in，另有
  helper-only、外部引用及 schedule-only guard 的预期 skip。九集合矩阵已另行显式运行，
  不在 soak 中重复 10 次。当前报告不把这些 skip 写成实测通过。

## GUI 既有结果与产物保留

GUI 基线为 `bca2ed041bc813c8353782951a071f9afc8eb19a`：此前 226 项离线、Ruff、原生构建、
自有夹具 16/16 通过，含右键/双击；正常关闭标记已确认。原始本地报告为
`gui-agent/runs/20261014-004409-ea887639/report.json`。
本轮没有重跑 GUI、触发私有 GUI CI、查询账单或声称产生费用。Android 真机、真实模型和
iOS 后端未测；iPhone-use/iCTRL 仍仅为设计。

原始大日志、资源探针和实验 patch 保存在任务本地 `evidence/`，没有上传。
Context 评测 Attempt 使用 Go t.TempDir，正常测试结束后清理；持久证据是 JSON/text 日志。
Harness `.eval/`、GUI `runs/`、`build/`、`.venv/` 被 Git 忽略。
GUI `docs/validation/macos15-2026-10-12/` 的旧 0/16 阻断与清理归档已跟踪，保留历史原貌。
本报告只汇总脱敏结果，不上传截图、密钥、设备签名配置或原始请求。
