# Runtime 持久化、恢复与客户端边界架构门

**状态：** 调研证据完成
**日期：** 2026-08-13
**范围：** Open Code Harness 的生产 EventStore、审计导出、崩溃恢复、ACP 边界以及客户端/Runtime 分离。

本文记录已批准 Runtime 持久化与客户端边界设计所使用的一手资料。它是调研证据，不是兼容性承诺，也不代表所有参考系统都提供相同的正确性合同。

英文版本 [2026-08-13-runtime-persistence-recovery-client.md](2026-08-13-runtime-persistence-recovery-client.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 决策问题

1. SQLite 还是 JSONL 应当成为物理提交权威？
2. 提交确认丢失时，如何确保重试仍然精确？
3. 进程死亡后如何协调未完成执行？
4. TypeScript TUI 应与 Go Core 共享 Runtime 状态，还是成为协议客户端？
5. Coding Agent 的哪些做法可以复用，哪些依赖更弱的持久性或平台假设？

## 证据比较

| 系统 | 一手资料 | 已观察到的设计 | 采用 | 不推导或不照搬 |
| --- | --- | --- | --- | --- |
| OpenAI Codex | [thread-store README](https://github.com/openai/codex/blob/main/codex-rs/thread-store/README.md)、[live writer](https://github.com/openai/codex/blob/main/codex-rs/thread-store/src/local/live_writer.rs)、[writer lock](https://github.com/openai/codex/blob/main/codex-rs/thread-store/src/local/writer_lock.rs)、[state migrations](https://github.com/openai/codex/blob/main/codex-rs/state/src/migrations.rs) | 规范 rollout JSONL 在可重建 SQLite 元数据视图之前写入并 flush；per-thread 跨进程锁、回填与 migration checksum 支撑这一选择。 | 保留人类可读的无损历史、显式 writer 所有权、带 checksum 的 migration 和可重建投影。 | JSONL 权威并不天然是精确 CAS、lost ACK 重试和三平台行为下最好的绿地选择。锁、扫描、漂移和修复机制都是它的成本。 |
| OpenCode | [Session Schema](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/session/session.sql.ts)、[CLI 数据库与 Session 命令](https://opencode.ai/docs/cli/) | SQLite 保存规范化的 Session、Message、Part、Todo、Session Message 和 Permission 记录，是 Session 列表、导出和数据库检查背后的持久恢复来源。Runtime Bus 与 SSE Event 用于通知 Consumer，但本身不是持久历史。 | 显式数据库工具、规范化产品投影、服务端统一拥有持久状态，以及与持久事实分离的瞬态交付 Event。 | 可变 Message/Part Row 和通知 Event 不能证明不可变领域事件权威、精确 Append Receipt、Fencing 或 Lost-ACK Recovery 合同。快速变化的 `dev` Schema 必须在实施复用前重新核验。 |
| Goose | [Session Manager 与 SQLite Storage](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/session/session_manager.rs) | `SessionManager` 将 Session、Conversation 和 Usage Ledger 的读写统一路由到 SQLite。Schema 初始化使用 `BEGIN IMMEDIATE` 串行化并发首次启动的 Writer，并把旧 Session 导入数据库。 | 串行化 Writer Admission、有界数据库等待、面向 WAL 的运行方式、事务内 Message/Session 更新、显式 Migration 和单向 Legacy Import。 | `replace_conversation` 与可变 Transcript CRUD 属于产品持久化语义，不是 Append-only Audit 或领域事件合同。 |
| Crush | [仓库架构](https://github.com/charmbracelet/crush/blob/main/AGENTS.md)、[Session Service](https://github.com/charmbracelet/crush/blob/main/internal/session/session.go) | Go Service 通过 sqlc 和 Migration 对 SQLite 执行 Session CRUD；Session 从数据库读取，多表删除使用事务。明确仅用于 UI 的 Estimated Usage 保留在内存中，不与持久事实竞争。 | Go/sqlc Repository 边界、Migration 纪律、事务范围内多表变更，以及持久事实与瞬态 UI 状态的明确区分。 | 可变 Session CRUD、内部 Pub/Sub 和事务删除不提供不可变 Event Replay、Expected-Version Append 或不确定副作用协调。 |
| Hermes Agent | [Session 持久化文档](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/sessions.md) | SQLite 保存完整 Session Metadata 与 Message History，文档明确称它是 Gateway Message 的 Canonical Store。JSONL 是导出格式；Legacy Mirror 是兼容产物，不是第二权威。 | 在产品文档中明确权威边界、让导出从属于数据库，并围绕单一 Canonical Store 组织 Resume、Routing Continuity 与 FTS History。 | Canonical Transcript Storage 本身不能证明不可变 Batch Event、Expected-Version CAS、AppendID Receipt、Writer Fencing 或不确定外部副作用的 No-Replay Recovery。 |
| Kimi Code | [数据位置](https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/configuration/data-locations.md)、[包映射](https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md) | `wire.jsonl` 是完整 replay/resume 记录；application/server、engine、provider、execution environment 和 transcript 包分离。 | consumer-owned 协议投影、按记录顺序 replay 和显式包边界。 | Transcript 幂等不能证明 EventStore 原子 CAS 或精确提交重试。 |
| Maka | [架构](https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md)、[Runtime Core 草案](https://github.com/maka-agent/maka-agent/blob/main/docs/architecture/runtime-core-architecture-draft.md)、[恢复架构](https://github.com/maka-agent/maka-agent/blob/main/docs/architecture/runtime-resume-architecture.md) | 语义 Runtime Event Log 是事实权威；UI、Context 和 Recovery 是投影。恢复区分 repair、continuation 与 retry，并拒绝在不确定时盲目 replay。 | 不可变事实、外部副作用前后的短持久事务、投影/信号之前的终止事实，以及不自动重试不确定副作用。 | 公开设计不能证明我们的 SQLite CAS、AppendID 或跨平台文件合同。 |
| DeepSeek-Reasonix | [v2 规范](https://github.com/esengine/DeepSeek-Reasonix/blob/main-v2/docs/SPEC.md) | Go、无 CGO 分发、append-only transcript 和完整 JSONL Session 持久化。 | 纯 Go 可移植性、完整会话保存和可测试 transcript 行为。 | Transcript 格式本身不是事务型多进程 EventStore。它是社区项目证据，不是规范标准。 |
| Pi | [monorepo](https://github.com/badlogic/pi-mono)、[SDK](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/sdk.md)、[Session 格式](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/session-format.md)、[RPC 模式](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/rpc.md) | Model API、Agent Core、Coding-Agent Session、TUI 和 Web UI 独立成包。`AgentSession` 拥有生命周期、历史、压缩和事件。集成者可以选择进程内 SDK 或逐行分帧的子进程 RPC。Session 是带版本的 JSONL 树，以 `id`/`parentId` 在单文件中支持分支。 | 小而可组合的 Runtime API、独立于 UI 的订阅、进程隔离协议模式、严格行分帧、带版本历史和一等分支概念。 | Pi 优先优化可改造性与本地可检查性。直接 JSONL Session 权威和加载时 migration 不提供我们要求的事务 CAS、AppendID 回执、fencing 或原子跨 Session 协调。它的自定义 RPC 是有用证据，但 ACP 仍是我们的公开标准。 |
| Grok Build | [仓库/构建支持](https://github.com/xai-org/grok-build)、[Shell/ACP/Session 文档](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-shell/README.md)、[贡献政策](https://github.com/xai-org/grok-build/blob/main/CONTRIBUTING.md) | Rust composition root 分离 TUI、Agent Shell/Runtime、Tools、Workspace、Sampling、ACP helper、Chat State、Crash Handling 和 PTY 测试。产品支持交互、headless 与 `grok agent stdio` ACP 模式。`updates.jsonl` 是权威会话历史；原始模型 chat、plan、rewind point、compaction checkpoint、signal、feedback 和 subagent 使用独立持久形式。工具时间/输出上限与可信配置层是显式的。 | composition-root 分离、ACP stdio 编辑器边界、headless 对等能力、有界工具、Workspace 隔离、崩溃检测、PTY 黑盒测试，以及即使在同一语言内也分离客户端和 Runtime。 | 多种持久文件会产生协调义务。公开 README 将从该代码树构建 Windows 标为 best-effort，低于我们的 CI 合同；公开贡献政策不接受外部贡献，因此不是本项目的社区治理模型。 |
| KurrentDB / EventStoreDB | [追加事件](https://docs.kurrent.io/clients/python/v1.3/appending-events)、[投影教程](https://docs.kurrent.io/getting-started/use-cases/time-travel/tutorial-3) | 原子 expected-revision append 和稳定 Event identity，使确认丢失后的重试仍然精确。投影状态和 checkpoint 可重建并一起更新。 | expected-version CAS、不可变 Event identity、精确重试回执、原子批次语义和可重建投影。 | 本地优先 v0 不需要专用分布式事件数据库。 |
| Temporal | [History Service 架构](https://github.com/temporalio/temporal/blob/main/docs/architecture/history-service.md) | 不可变 Workflow History 是逻辑权威；Mutable State 及 transfer/timer task 以事务方式一起更新，用于服务和分发。 | 将语义权威与物理格式分开；使用事务注册的 outbox work，而不是 dual write。 | Temporal 的分布式 shard ownership 与队列远超本地优先里程碑。 |
| SQLite | [原子提交](https://sqlite.org/atomiccommit.html)、[WAL](https://sqlite.org/wal.html)、[Backup API](https://sqlite.org/backup.html) | 本地事务提供原子提交和 WAL 恢复；Online Backup API 产生一致性副本。WAL 是数据库恢复机制，不是领域 Event Log，并有文件系统限制。 | SQLite 事务作为唯一提交点、只支持本地文件系统、经过明确配置的 WAL durability，以及使用 Online Backup 作为主要备份。 | SQLite WAL 文件不是公开审计日志，不能当作审计日志暴露。活动数据库不支持放在 NFS、SMB 或同步盘。 |
| modernc SQLite | [Go Package 文档](https://pkg.go.dev/modernc.org/sqlite) | 由 C 翻译为 Go 的 database/sql SQLite Driver 支持在目标操作系统上的无 CGO 构建路径。 | 作为默认生产 Driver，并在 CI 验证所有目标平台。 | Driver 可移植性本身不能证明我们的 Transaction、PRAGMA、Backup 或 Filesystem 行为；它们仍是 Adapter Contract。 |
| Transactional Outbox | [Debezium Outbox 文档](https://debezium.io/documentation/reference/stable/transformations/outbox-event-router.html) | 业务状态与待发布记录在一个数据库事务中提交；发布过程异步且幂等。 | 每个 JSONL 导出批次都与其事件在同一 SQLite 事务注册。 | SQLite 与文件的同步 dual write 无法提供一个可移植的原子提交。 |

## SQLite 权威评估

“SQLite 权威”包含两个有本质差异的合同。OpenCode、Goose、Crush 与 Hermes 证明了**会话/Transcript 权威**：进程重启后，持久 Session 与 Message 事实从 SQLite 读取，而不是以内存 Bus、UI State、Search Index 或 Export 为准。这有力说明，本地 Coding Agent 把 SQLite 作为产品恢复来源并不是反常的运维模型。

Open Code Harness 要求更严格的**Runtime 领域权威**合同。不可变 Event Stream 必须裁决每个已接受的生命周期事实，可变 Head、Transcript Row、Snapshot 与 JSONL 都只能派生。在本次审阅的公开实现中，没有一个文档化了以下完整组合：原子多 Event Append、Expected-Version CAS、Caller-Stable `AppendID` 与 Request Digest、提交后 Receipt Resolution、Runtime Fencing、Transactional Audit Outbox，以及不确定副作用的 No-Replay Recovery。缺少公开证据并不证明某个未公开内部机制不存在；它意味着我们不能从这些对比对象继承相应保证。

因此，这组对比增强而非改变了已选架构。Goose 提供具体 SQLite 运维机制；Crush 是最接近的 Go 实现参考；Hermes 给出最清晰的 Canonical Store 产品语义；OpenCode 提供丰富的规范化 Session Schema 与数据库运维表面。它们的可变 Transcript 模型仍是 Projection 与 Tooling 的参考，而不是 Canonical Immutable EventStore 的替代。

Codex 是有价值的反例：其源码明确把 SQLite 视为可重建 View，允许其落后、但绝不能领先于 Canonical JSONL。因此，仅仅在 Agent 中发现 SQLite 数据库不足以证明 SQLite 权威；恢复顺序与写入优先级才决定分类。

## Pi 评估

Pi 的价值在于其架构足够小，能够完整阅读。Model Layer、通用 Agent Core、Coding-Agent Session、TUI 和 Web UI 保持为独立包。`AgentSession` 暴露 prompt、abort、compaction、navigation 和 event，而不要求使用内置 TUI。它的 RPC 模式明确服务跨语言和进程隔离集成，并对 LF framing 定义得足够严格，甚至指出了通用 line reader 的风险。

它的 Session 格式与 Open Code Harness 采用不同取舍。带版本、append-only 的 JSONL 树天然适合分支和人工检查，自动 migration 也更重视易用性。公开合同没有宣称精确 expected-version CAS、原子多事件批次、提交后回执解析、持久 writer epoch 或经过验证的副本 manifest。因此我们采用 Pi 的可组合性与 fixture 思路，而不采用它的持久化合同。

## Grok Build 评估

Grok Build 与 Go Core/TypeScript Client 决策尤其相关。尽管它的 Runtime 与内置 TUI 都使用 Rust，仓库仍分离 composition root、pager/TUI、shell/runtime、tools、workspace、sampler、protocol helper、chat state 和 crash handling。同一个 shell 支持 TUI、headless 与 ACP stdio 入口。这验证了“一个 Runtime、多种外部交付模式”，而不是在每个 UI 中复制 Agent Loop。

其公开 Session 布局也展示了富本地 Agent 的代价：ACP update、原始模型历史、plan、rewind data、compaction checkpoint、signal、feedback 和 subagent 分别持久化。`updates.jsonl` 被定义为权威会话历史，其他文件服务不同消费者。这有利于检查与快速产品迭代，但必须为分歧和恢复定义清晰规则。Open Code Harness 则在一个 SQLite 事务中提交领域事实、AppendID 回执、最小投影与 Export Outbox；JSONL 保持无损和可移植，但不成为第二个在线权威。

Grok Build 还提供了积极的运维范例：工具 timeout 与 byte limit、可执行 credential helper 的可信配置层、headless 支持、ACP 兼容、PTY 测试，以及公开源码构建默认不启用的 telemetry。我们采用这些原则，同时要求比其公开构建树声明更强的 Linux/macOS/Windows 验证。

## 存储方案比较

### A. SQLite 权威状态表加附带日志

否决。可变状态表会掩盖语义 Event Source，并削弱确定性 replay。

### B. SQLite-backed 规范 Event Log 加事务型 JSONL 副本

选定。不可变 Event row 同时是语义与物理在线权威。Outbox row 与事件在同一事务提交，后台 Exporter 生成可验证 JSONL batch envelope。这样既保留可检查性，又不产生两个提交权威。

### C. JSONL 规范日志加 SQLite 投影

这是多个 Agent 已验证的有效方案，但不适合本项目。在纯 Go Linux/macOS/Windows 合同下，它要求实现一个跨平台 WAL：writer fencing、partial-tail repair、corruption policy、精确 receipt、segment publication、manifest、compaction、projection checkpoint 和 drift repair。只有当“可独立写入的原始 JSONL”本身成为产品需求时，这项成本才合理。

## 客户端边界方案比较

### A. 把 Go Core 嵌入 TypeScript TUI

否决。它要么需要 FFI/CGO，要么把 JavaScript Runtime 嵌入 Go，要么复制生命周期与状态所有权。

### B. 发明项目专用 JSONL RPC

否决其作为公开边界。Pi 证明这种方案可以小而有效，但它会创造新的生态合同和重复客户端工作。

### C. ACP v1 over stdio

选定。TypeScript TUI 使用官方 ACP SDK 并启动纯 Go Agent binary。Go Adapter 将 ACP 映射到 Application 用例。Domain Event、SQLite Record 和内部 Runtime Signal 不成为公开 ACP 类型。

## 已采纳决策

1. SQLite 不可变事件是唯一在线提交权威。
2. JSONL 是通过事务 Outbox 注册的无损、可验证、可重建审计副本。
3. 调用方稳定的 `AppendID` 和 request digest 在提交确认丢失后提供精确重试。
4. 一个活动 Runtime Host 通过 lease 和单调增长的 fencing token 拥有一个数据库。
5. 启动恢复使用 `process_crash` 终止遗留的 running Item/Turn pair；绝不自动重复模型或工具调用。
6. 生产 reader 分页；compact command aggregate 与 transcript projection 取代无界完整历史状态加载。
7. Go Core 是无 CGO 单二进制；TypeScript TUI 是独立 ACP Client 和发布物。
8. ACP v1 stdio 是唯一稳定 v0 客户端传输。远程 draft transport 保持 experimental，不进入兼容承诺。
9. 每个实施里程碑都要重新做聚焦的一手资料架构门，并重新核验当时仍公开且与该 Slice 直接相关的实现；适用时必须纳入 Pi、Grok Build、OpenCode、Goose、Crush 与 Hermes。

## 证据限制

- 公开代码和文档只能说明可观察到的实现选择，不能证明未公开的生产运维或保证。
- 未记录某项不变量应标记为未知，不能据此断言项目缺少该不变量。
- Grok Build 定期从更大的私有 monorepo 同步；本文只把公开代码树作为证据。
- Pi 的仓库和 package identity 曾发生变化；上述链接标明本次评审使用的源码路径。
- OpenCode 的公开 `dev` Branch 变化很快；链接 Schema 是带日期的证据，不承诺每个发布版本都具有相同 Table。
- 所有参考项目都不是默认代码来源。复用任何实现之前仍必须完成 license 与 provenance 审查。
