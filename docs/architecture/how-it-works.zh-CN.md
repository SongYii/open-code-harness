# 项目实现通俗导读

- 状态：持续维护的通俗导读
- 最后核对：2026-09-10
- 英文规范真源：[how-it-works.md](how-it-works.md)
- 整体地图：[当前系统架构](current-system.zh-CN.md)

这里是理解项目的短入口。每个模块都回答同样五个问题。精确类型和边界在实现合同
里，提交号和可复跑测试在证据账本里。

证据是历史记录。旧文档写“SQLite 尚未实现”，只能表示当时如此，不能表示项目
现在的状态。本文描述当前仓库。

<!-- contract: docs/architecture/current-system.md -->
## 当前系统架构

### 解决什么问题

模块各自写得正确，不代表组合后仍有清楚边界；需要明确谁能调用谁、谁掌握事实。

### 用户能看到什么

贡献者可以从一张地图找到包归属、请求流程、恢复流程和持久化权威。

### 真实实现

[当前架构合同](current-system.md)负责总览，
`internal/harness/architecture/dependencies_test.go` 自动执行包依赖矩阵。

### 遇到的问题与修复

审计发现未知生产包会进入宽松默认路径，Web 客户端也没被完整隔离。现在未知包
直接失败，客户端整棵目录都会检查。见[证据](current-system-evidence.md)。

### 仍未完成

导入关系测试不能证明运行时一定正确，各模块仍要用自己的测试证明行为。项目仍是
pre-v0。

<!-- contract: docs/architecture/domain-events.md -->
## Domain 事件与状态机

### 解决什么问题

如果 Session 和 Turn 只是随意改字段，崩溃和重试后就无法判断发生过什么。

### 用户能看到什么

每次合法变化都有明确命令和事件；重放相同事件会得到相同状态，非法变化返回稳定
错误。

### 真实实现

`internal/harness/domain` 保存命令、决策、事件应用、编码和重放规则，详见
[实现合同](domain-events.md)。

### 遇到的问题与修复

它从简单 Session/Turn 扩展到模型、工具、上下文和指令事实，同时保持旧事件可重放。
这是最早模块，当时还没有证据账本制度，所以踩坑记录明显弱于后续模块；我们不编造
不存在的历史。

### 仍未完成

Domain 只保证规则，不负责磁盘持久化或外部 I/O。

<!-- contract: docs/architecture/engine-vertical-slice.md -->
## Engine 纵切

### 解决什么问题

模型流可能正常结束、失败或被取消；如果没人统一收尾，Turn 会出现假完成。

### 用户能看到什么

每次模型尝试都有输出上限、确定的清理顺序、明确错误类型和唯一终态。

### 真实实现

`internal/harness/engine` 定义模型流端口，Application 驱动并记录结果。见
[合同](engine-vertical-slice.md)和[证据](engine-vertical-slice-evidence.md)。

### 遇到的问题与修复

实现和后续装配审查发现流使用哪个 Context 不够明确。测试固定了取消快照和清理顺序，
尚未完全消除的歧义继续公开记录。

### 仍未完成

Engine 是内部执行边界，不是公共插件接口，也不选择具体模型供应商。

<!-- contract: docs/architecture/eventstore-v2.md -->
## EventStore v2

### 解决什么问题

写入超时后，调用方可能不知道数据到底有没有提交；盲目重试会重复，直接放弃会丢失。

### 用户能看到什么

写入带预期版本和稳定身份，可以区分冲突、完全相同的重试以及结果未知。

### 真实实现

Application 拥有 Store 接口和追加流程，Domain 计算内容摘要。见
[合同](eventstore-v2.md)和[历史证据](eventstore-v2-evidence.md)。

### 遇到的问题与修复

该切片删除了宽泛的旧接口，加入结果未知时的查询确认。旧证据写“SQLite 尚未开始”
只是当时快照，现在已经过时。

### 仍未完成

接口本身不提供磁盘耐久性；生产持久化由 SQLite Adapter 提供。

<!-- contract: docs/architecture/provider-adapter.md -->
## 模型 Provider Adapter

### 解决什么问题

不同模型服务的 HTTP 格式和错误如果进入 Agent Loop，会到处出现供应商判断。

### 用户能看到什么

OpenAI 兼容流会变成统一模型流，并记录用量、限制输入输出、归一化错误。

### 真实实现

`internal/harness/adapters/openaicompat` 实现 `engine.Model`，能力配置描述模型差异。
见[合同](provider-adapter.md)和[证据](provider-adapter-evidence.md)。

### 遇到的问题与修复

早期账本主要列测试，没有详细踩坑叙述，这是文档缺口。后续原生工具消息和 Secret
脱敏仍保持在 Adapter 内，没有把供应商分支塞进 Application。

### 仍未完成

目前只有一类 OpenAI 兼容 Provider，没有多 Provider 路由或真实密钥 CI。

<!-- contract: docs/architecture/tool-runtime.md -->
## Tool Runtime

### 解决什么问题

模型提出的读写文件、执行命令请求都不可信，不能跳过范围、权限、上限和审计直接运行。

### 用户能看到什么

工具调用先校验，再判断风险和审批，然后受限执行并记录结果，Loop 才会继续。

### 真实实现

Application 管理步骤循环，`policy` 判断风险，`tools` 定义接口，文件和进程 Adapter
执行 I/O。见[合同](tool-runtime.md)和[证据](tool-runtime-evidence.md)。

### 遇到的问题与修复

第一版明确只有部分操作系统保护；后续加入沙箱、配额、安全文件修改和 MCP，但都复用
原来的 Policy/审批路径。

### 仍未完成

Shell 执行不受文件已读版本保护，不同平台能提供的隔离能力也不同。

<!-- contract: docs/architecture/observed-file-mutation.md -->
## 基于已读状态的安全文件修改

### 解决什么问题

Agent 可能覆盖自己从未读过的文件，或者覆盖它读取之后别人刚改的内容。

### 用户能看到什么

修改必须携带上次读取产生的隐藏版本；没读过或已经变化都会拒绝，并告诉 Agent 下一步。

### 真实实现

Application 保存每个 Session 的观察，`workspacefs` 比较版本并通过临时文件后再发布。
见[合同](observed-file-mutation.md)和[证据](observed-file-mutation-evidence.md)。

### 遇到的问题与修复

三条初始测试其实没证明声称的机制；一次 10 KiB 替换还能膨胀到 327 MiB，另漏了一种
创建冲突。后来用独立实例和真正危险的变异重写测试，并限制输出。

### 仍未完成

检查到发布之间不是文件系统事务，外部程序不遵守这套协议，`exec` 也能绕开它改文件。

<!-- contract: docs/architecture/sqlite-eventstore.md -->
## SQLite EventStore

### 解决什么问题

内存 Store 无法跨进程重启保存数据，也无法安全协调多个写入进程。

### 用户能看到什么

Session 和 Checkpoint 能恢复；写入在事务中完成；围栏租约会阻止旧进程继续写。

### 真实实现

`internal/harness/adapters/sqlite` 实现事件、Checkpoint、审计链、备份和只读路径。
见[合同](sqlite-eventstore.md)和[证据](sqlite-eventstore-evidence.md)。

### 遇到的问题与修复

一次偶发测试最初被判断为 CPU 抢占。确定性复现证明：单独打开 SQLite 后租约 30 秒
硬过期，只有 Runtime Host 才续租。测试和权责说明随后修正。

### 仍未完成

断电设备测试和长时间真实租约测试仍有限；单独使用 Store 不能假定有人续租。

<!-- contract: docs/architecture/jsonl-audit-replica.md -->
## JSONL 审计副本

### 解决什么问题

运维需要可搬运、可查看的审计数据，但不能因此制造第二个写入权威。

### 用户能看到什么

系统导出带摘要链的 JSON Lines，能发现篡改和半写文件，并能在导出崩溃后继续。

### 真实实现

SQLite 在事件事务内维护审计链；Exporter 依次暂存、封口、发布 Manifest 和 Checkpoint。
见[合同](jsonl-audit-replica.md)和[证据](jsonl-audit-replica-evidence.md)。

### 遇到的问题与修复

实现基本符合设计；唯一记录的偏差是文件名使用短摘要，Manifest 仍保存完整摘要。残留
临时文件和副本丢失都有恢复测试。

### 仍未完成

还没有长时间恶意导入/导出测试或真实设备断电证明。

<!-- contract: docs/architecture/runtime-host.md -->
## Runtime Host 与崩溃恢复

### 解决什么问题

有持久化数据库还不够，必须有人负责启动修复、续租、后台导出、失权反应和关闭顺序。

### 用户能看到什么

进程先修复未完成工作再接请求；失去租约就停止接单；关闭时只释放自己的租约。

### 真实实现

`internal/harness/runtime` 管理恢复和生命周期，Composition 在服务客户端前启动它。
见[合同](runtime-host.md)和[证据](runtime-host-evidence.md)。

### 遇到的问题与修复

原账本记录没有设计偏差。后来的 SQLite 失败澄清了边界：Heartbeat 属于 Runtime，
不是 SQLite Adapter 自己的功能。

### 仍未完成

Kill-9、时钟跳变和长时间真实租约测试仍需扩大。

<!-- contract: docs/architecture/composition-root.md -->
## Composition Root

### 解决什么问题

如果各层自行创建 Adapter，测试可能使用真实程序永远不会采用的接线，层之间也会耦合。

### 用户能看到什么

唯一生产装配负责选 Adapter、检查配置、启动 Runtime、开放 ACP 和按顺序关闭。

### 真实实现

`internal/harness/composition` 是唯一普遍允许点名具体 Adapter 的包。见
[合同](composition-root.md)和[证据](composition-root-evidence.md)。

### 遇到的问题与修复

开发发现生产 Clock/ID 缺失、“未归类等于无限制”以及 Adapter 黑名单会漏掉未来包。
这些都变成真实实现和穷尽式自动测试。

### 仍未完成

Composition 的内部接口不是稳定公共插件 API。

<!-- contract: docs/architecture/acp-v1.md -->
## Agent Client Protocol v1 Adapter

### 解决什么问题

客户端需要稳定协议，但不应该依赖内部 Domain 事件或掌握 Agent Loop。

### 用户能看到什么

独立客户端可通过 stdio 上的 JSON-RPC 创建/加载 Session、发 Prompt、取消、审批并接收
实时或重放更新。

### 真实实现

`internal/harness/adapters/acp` 校验 Agent Client Protocol（ACP）v1 并投影事件。
见[合同](acp-v1.md)、[基础证据](acp-v1-evidence.md)和
[生命周期证据](acp-session-lifecycle-evidence.md)。

### 遇到的问题与修复

基础账本几乎没写踩坑。后续 Session 生命周期实现修正了原状态图，并补上双向连接状态机
和故意破坏测试。

### 仍未完成

没有 ACP v2 和认证；stdio 子进程生命周期仍跟随父进程。

<!-- contract: docs/architecture/session-transcript.md -->
## Session Transcript

### 解决什么问题

内部事件虽然耐久，但不适合作为用户查看或外部消费的对话导出格式。

### 用户能看到什么

`och export-session` 输出有版本、大小限制、Snapshot、事实目录和完成尾标的 JSON Lines。

### 真实实现

`internal/harness/transcript` 读取 EventStore 并生成独立投影。见
[合同](session-transcript.md)和[组合证据](conversation-and-transcript-evidence.md)。

### 遇到的问题与修复

早期证据主要是映射测试和哈希，不是开发故事。后续生命周期加入删除事实，但没有把
Transcript 变成可写回 EventStore 的格式。

### 仍未完成

不支持 Transcript 导入，Runtime 事实、Subagent 来源和部分脱敏仍有限。

<!-- contract: docs/architecture/acp-native-client.md -->
## ACP 原生终端客户端

### 解决什么问题

只用 ACP 服务端自己的测试替身，不能证明独立程序真的能使用协议。

### 用户能看到什么

`acp-client` 可启动 Agent、管理 Session、展示执行轨迹并处理审批。

### 真实实现

`internal/client/acp` 与 Harness 完全隔离，入口是 `cmd/acp-client`。见
[合同](acp-native-client.md)和[证据](acp-native-client-evidence.md)。

### 遇到的问题与修复

映射表和真实进程测试抓到了 Wire Update 与客户端轨迹整理之间的差异，最终测试证明的
是独立互操作，不是共享 Go 类型。

### 仍未完成

它是最小终端客户端，不是完整 TypeScript TUI。

<!-- contract: docs/architecture/secret-redaction.md -->
## Secret 脱敏

### 解决什么问题

工具输出或模型文本可能含 API Key，若直接保存就会进入永久事件和导出。

### 用户能看到什么

已识别 Secret 会在工具完成/失败和最终助手文本持久化前被替换。

### 真实实现

`internal/harness/redact` 是纯函数包，Application 在两个持久化边界调用它。见
[合同](secret-redaction.md)和[证据](secret-redaction-evidence.md)。

### 遇到的问题与修复

故意删除匹配规则、绕过每个调用点，测试都必须失败；Provider 日志也改为复用同一包，
不再维护第二套规则。

### 仍未完成

流式增量和工具参数没有全面脱敏，固定模式也不可能识别所有未知 Secret。

<!-- contract: docs/architecture/web-trajectory-ui.md -->
## Web 执行轨迹界面

### 解决什么问题

浏览器不能安全地直接读写 Agent stdio，但另造应用协议又会复制 ACP 语义和安全规则。

### 用户能看到什么

本地网页按 Turn 展示活动和审批，Agent 语义仍全部是 ACP Frame。

### 真实实现

`cmd/acp-web-bridge` 做 Frame 转发、Origin 和单次 Token 检查；`web/src` 下 TypeScript
实现独立客户端和 UI。见[合同](web-trajectory-ui.md)和
[证据](web-trajectory-ui-evidence.md)。

### 遇到的问题与修复

浏览器端到端测试和故意破坏测试证明 Origin、Token 两道检查都实际生效，而且客户端
没有偷偷导入 Harness。

### 仍未完成

只支持一个本地 Viewer，没有非本机部署、多 Viewer 或完整 Session 管理器。

<!-- contract: docs/architecture/context-engine.md -->
## Context Engine

### 解决什么问题

长对话会超过模型输入限制；静默删文本会导致行为无法解释，也无法在重启后恢复。

### 用户能看到什么

系统按预算选择历史，只在安全 Turn 边界裁切，压缩大工具结果，分块总结，并从已验证
Checkpoint 继续。

### 真实实现

`internal/harness/contextengine` 负责纯规划，Application 管理四种触发，SQLite/Memory
保存 Checkpoint。见[合同](context-engine.md)和[证据](context-engine-evidence.md)。

### 遇到的问题与修复

开发发现 Reset 摘要没覆盖原文、每次线性重扫、Usage Anchor 没接线、裁剪上限没传入、
多块总结未实现；每项都补了针对性测试和修复。

### 仍未完成

不同真实模型和代码库上的质量证据仍不足，机制已实现但还不能宣称 GA。

<!-- contract: docs/architecture/system-prompt-workspace-instructions.md -->
## System Prompt 与 Workspace Instructions

### 解决什么问题

仓库指令会在 Session 中途变化；每轮盲目重读追加会降低 Prompt Cache，并让旧指令在
重启或压缩后残留。

### 用户能看到什么

模型收到一个版本固定的 System Prompt 和有上限的分层 `AGENTS.md`；变化记录为
set/replace/remove，并能跨总结、重置和重启恢复。

### 真实实现

`internal/harness/agentinstructions` 发现和渲染指令，Application 观察变化，Domain 与
Checkpoint 保存身份。见[合同](system-prompt-workspace-instructions.md)和
[证据](system-prompt-workspace-instructions-evidence.md)。

### 遇到的问题与修复

最终采用追加 Delta，让未变化 Turn 保持稳定请求前缀。DeepSeek 真实调用发生过，但
确定性前置条件失败，所以没有调用 Judge；证据诚实标为 Partial。

### 仍未完成

尚未证明真实模型质量；仓库指令也永远不能授予工具权限。

<!-- contract: docs/architecture/mcp-client.md -->
## Model Context Protocol 客户端 Adapter

### 解决什么问题

外部工具需要进入 Agent，但不能每个 Server 都写专用集成，更不能绕过 Policy 和审计。

### 用户能看到什么

配置的 stdio Model Context Protocol（MCP）Server 会贡献受限且不重名的工具，并走
正常审批、执行、脱敏和事件路径。

### 真实实现

`internal/harness/adapters/mcp` 包装锁定版本的官方 SDK；Composition 提供受限进程端口。
见[合同](mcp-client.md)和[证据](mcp-client-evidence.md)。

### 遇到的问题与修复

实现推翻了五项设计假设，包括 SDK 协商/依赖、外部名称、沙箱归属和关闭行为。两次
最初故意破坏测试什么也没抓到，重写后才接受结论。

### 仍未完成

没有 Streamable HTTP/OAuth、Server 自动重启或大规模 Catalog 容量测量。

<!-- contract: docs/architecture/evaluation.md -->
## Evaluation 评测系统

### 解决什么问题

一个端到端分数无法说明失败来自模型、Context、工具、协议、证据收集还是 Judge。

### 用户能看到什么

冻结的 Scenario 可走进程内和 ACP 两条路径，发布已提交证据、离线重评；真实模型和
Judge 调用必须显式同意。

### 真实实现

`internal/harness/eval` 管理 Scenario/Subject/Executor 身份、证据选择、确定性检查、
Judge 评分和方差文档。见[合同](evaluation.md)和[证据](evaluation-evidence.md)。

### 遇到的问题与修复

完整 Context 矩阵曾在每个 PR 跑四遍；两个 Judge Fixture 什么也没证明；无引用判决
曾被相信；“没有调用”检查甚至可以在没看任何证据时通过。现在都有接线和回归保护。

### 仍未完成

仍没有成功 Live Judge 样本、校准后的方差策略或第二类 Provider。OpenTelemetry 是
另一项尚未设计的工作。
