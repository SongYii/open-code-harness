# SQLite 规范 EventStore 架构门（中文阅读版）

**状态：** 完整研究证据

**日期：** 2026-08-16

**范围：** Slice 2（SQLite 规范 EventStore）第一手来源重核查。记录必需对照集
在当前公开状态下的持久化契约、SQLite 适配器门的采纳/拒绝边界，以及 Tool
Runtime 合同落地后 Slice 2 作为下一个实施切片的顺序确认。

本文档是研究证据。它不改变 EventStore v2 的行为，也不授权复制参考项目的
类型、模式或运行时。

英文版为规范研究记录，本文件是同步的中文阅读版。

## 问题

1. Provider 适配器与 Tool Runtime 合同落地之后，Slice 2（SQLite 规范
   EventStore）是否是正确的下一个实施切片？
2. 已接受 Runtime 设计中的 Slice 2 范围——迁移、事务/CAS、精确重试、
   fencing、投影、备份——在重核查后的公开实现面前是否仍然正确？
3. SQLite 适配器应当采纳哪些观察到的持久化契约，哪些与章程或已接受设计
   冲突？
4. 在 2026-08-15 对照一天之后的重核查中，官方 DeepSeek Harness 的持久化
   seam 贡献了什么，对一个规范存储而言它在哪里不足？
5. 哪些子系统专属的权威来源必须加入对照集以覆盖 SQLite 语义？

## 重核查的第一手来源

均于所列日期从官方仓库观察。提交号是观察到的状态，不是背书。

| 来源 | 观察状态 | 与持久化相关的入口 |
| --- | --- | --- |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | developer preview，MIT，TypeScript/Cordis，2026-08-16 | `docs/subsystems/persistence.md`、`docs/subsystems/session.md` |
| [OpenAI Codex](https://github.com/openai/codex) | `73abda8`，2026-08-16 | `codex-rs/rollout`、`codex-rs/state`（SQLite）、`codex-rs/thread-store` |
| [Kimi Code](https://github.com/MoonshotAI/kimi-code) | `6b72345`，2026-08-15 | `packages/agent-core` 会话存储、记录持久化、`docs/en/guides/sessions.md` |
| [Grok Build](https://github.com/xai-org/grok-build) | `5163763`，2026-08-15 | `xai-grok-shell/src/session/persistence.rs`、`xai-sqlite-journal` |
| [Pi agent core](https://github.com/badlogic/pi-mono) | `086c32e`，2026-08-15 | `packages/agent` 会话日志、JSONL 存储、一致性测试套件 |
| [Maka](https://github.com/maka-agent/maka-agent) | `2666a57`，2026-08-16 | `ARCHITECTURE.md`、runtime-resume 架构 |
| [SQLite WAL 文档](https://www.sqlite.org/walformat.html) | 子系统权威 | 预写日志格式与并发语义 |

[DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) 仍是
社区、非权威上下文。营销页面、非官方镜像和插件市场不是第一手证据。

## 生态收敛

无论语言或架构如何，所有重核查的项目都收敛到同一持久化形态。这是本门
中最强的证据：

1. 追加式事实/事件日志是唯一权威；状态由重放推导，绝不作为第二份可变
   转录存储。
2. 压缩是一条遮蔽旧事实的新事实或新记录；规范行永不被重写或删除。
3. 崩溃写入造成的残缺尾行被修复或丢弃；尾部之前的损坏是硬错误，绝不
   被静默跳过。
4. 带开放边界的崩溃在恢复时被合成地闭合；已持久化的工作不被截断，模型
   或工具的效果不被静默重放。
5. 不支持的格式版本作为一种与损坏不同的条件被拒绝。
6. 日志之上的索引、摘要和查询结构是可重建的投影，绝不是第二权威。

章程与 EventStore v2 已经断言全部六条。Slice 2 不重新审议它们，而是在
SQLite 之上实现它们。

## 观察到的契约与边界

| 来源 | 观察到的契约 | Slice 2 决策 | 边界 |
| --- | --- | --- | --- |
| DeepSeek Harness | 单一 `SessionPersistence` seam；SQLite 逐事件一行原样存储，字段一一对应，"no parallel persisted event type"；`SCHEMA_VERSION` pragma 在其他检查之前把关结构。 | 采纳逐事件一行原样存储，以及带 pragma 门控的版本检查——对更新版本以指向升级方向的错误拒绝。 | 不复制其列名或 seam 形态；我们的 Store 端口、AppendID 与回执按 EventStore v2 保持存储所有。 |
| DeepSeek Harness | 持久化是内存追加之后的异步批量 flush；`append` 仅在持久化后完成；失败的后台写入暂停自动重试。 | 拒绝其作为提交权威。我们的持久化追加就是在线事实；终态事实先提交后交付。 | 单个 SQLite 事务内的批量 I/O 是优化，绝不是提交边界的推迟。 |
| DeepSeek Harness | JSONL 后端默认按校验和的 Zstandard 帧串接存储，可配置为原始行。 | 记录为 Slice 3 审计导出信封的首要候选。 | JSONL 在任何切片中都不是在线权威。 |
| DeepSeek Harness | 被放弃的会话 "leave nothing behind"（惰性物化）；修订令牌在事务中变更，仅做相等性比较。 | 两者都采纳。 | — |
| DeepSeek Harness | 崩溃修复只对冷会话追加合成闭合；活动会话拒绝修复；只有 "a torn final record is discarded"。 | 证实我们的调和姿态；完整细节属于 Slice 4。 | — |
| Codex | SQLite 状态数据库使用 `journal_mode(WAL)`、`synchronous(Normal)`、`busy_timeout(5s)`、48+ 个有序 SQL 迁移、通过连接池做事务。 | 采纳 WAL 纪律、显式 synchronous 模式、busy 超时和有序版本化迁移作为 Slice 2 基线。 | 不采纳其 JSONL+SQLite 双活动面；见拒绝项 R3。 |
| Codex | 跨进程写者互斥是建议性文件锁；没有 fencing 令牌；公开议题 [#36869](https://github.com/openai/codex/issues/36869) 记录了写锁绕过。 | 直接的第一手证据：仅建议性锁不够。Slice 2 存储设计与 Slice 4 租约要求 fencing 令牌。 | 他们的缺陷是证据，不是可复制的模式。 |
| Codex | `codex migrate-rollouts` 将遗留 JSONL 迁入 SQLite 支持的分页历史；`LocalThreadStore` "persists history through `codex-rollout` JSONL files and persists queryable metadata through the SQLite state database"。 | 验证 SQLite 规范化的顺序：他们正在向我们首先构建的方向迁移。 | — |
| Codex | 读取时不可解析的 rollout 行被跳过并计数："failed to parse line as JSON ... continue"。 | 拒绝。失败开放式的跳过违反我们失败封闭的未知模式边界，也违反 DeepSeek Harness 自己的拒绝立场。 | Slice 3 的任何导入必须拒绝或隔离，绝不静默跳过。 |
| Grok Build | `DurableAppendError::{NotCommitted, Committed, AcknowledgementLost}`——提交结果三元组，确认丢失是显式未知状态。 | 对 EventStore v2 追加错误代数的独立确认。保留我们的代数；在 SQLite 事务上实现三元组。 | 不复制错误命名。 |
| Grok Build | 单一 actor 串行化所有会话写入；`FlushAndAck` 仅在 "`flush_pending()` finishes writing to disk" 之后完成；重写先备份，失败的备份会阻止重写。 | 采纳持久化之后确认的屏障，以及迁移与备份工具的备份门控破坏性操作。 | — |
| Grok Build | SQLite 日志模式本地为 WAL 但 NFS 上为 TRUNCATE，因为 "WAL does not work over a network filesystem"；这些 SQLite 数据库 "are all rebuildable indexes/caches"。 | 采纳显式日志模式选择并记录 NFS 退化，以及投影的可重建索引角色。 | — |
| Grok Build | `ENOSPC`/`EDQUOT` 上的磁盘满闩锁并带健康探测。 | 采纳为 Slice 2 必需的资源限制测试类。 | — |
| Kimi Code | 批量追加，每批 `fsync`，创建时一次父目录 fsync；容忍截断的尾行，其余一律硬失败。 | 采纳回执前 fsync 与数据库创建时的目录 fsync。 | 拒绝其一次错误后粘性毒化的持久化行为；我们的错误代数做分类并允许精确重试。 |
| Pi | 单一一致性套件把内存与 JSONL 后端钉在同一个契约上；残缺尾行通过原子重发布有效前缀修复。 | 证实我们 `eventstoretest` 的形态；SQLite 适配器必须通过同一套件，外加适配器专属的故障注入。 | — |
| Pi | 有界开放操作修复（`limit: 2` 个悬空开放）对应单个在途操作加至多一个前驱。 | 记录为 Slice 4 启动调和的边界姿态。 | — |
| Maka | "We store only the facts... Projections, such as UI threads, are derived views that can always be rebuilt"；"Resume Is Not Retry"；压缩 "does not modify or delete canonical RuntimeEvents"。 | 采纳其词汇与不变量：投影可重建、恢复由事实驱动。 | Maka 没有数据库机制；它贡献不变量，不贡献实现。 |

## 拒绝的形态

1. **失败开放的损坏跳过**（Codex rollout 读取）。未知或不可解析的条目
   必须拒绝或隔离；计数器不是契约。
2. **异步 flush 作为提交权威**（DeepSeek Harness）。提交是在线事实。
3. **JSONL 与 SQLite 作为对等在线权威**（DeepSeek Harness 对等后端；
   Codex 双活动面，重命名不一致记录于议题
   [#16405](https://github.com/openai/codex/issues/16405)）。SQLite 是唯一
   提交权威；JSONL 是 Slice 3 的审计与导入。
4. **仅建议性锁的写者互斥**（Codex，有公开的绕过记录）。任何锁或租约
   守护写入的地方都要求 fencing 令牌。
5. **粘性毒化持久化**（Kimi Code 错误处理）。以带精确重试解析的分类
   错误代数取代。
6. **无存储重放的投影可见性**（Pi 头部在打开时快照条目；之后的条目
   "validated, but not replayed"）。我们的投影在读取时从存储推导。
7. **复制参考项目的模式、类型名、插件 seam 或迁移编号。** 参考项目不是
   依赖，不捐赠任何东西。

## 发现

### F1. Slice 2 是正确的下一个切片

2026-08-15 的顺序结论说 SQLite、恢复、ACP 与 TUI 在"工具使用循环合同
存在之后"恢复。Tool Runtime 合同已实现并验证。本门中没有观察到的任何
东西改变该顺序。

### F2. 已接受的 Slice 2 范围被确认并被加强

生态收敛（六条共同契约）加上 Codex 自身的 JSONL→SQLite 迁移验证了设计。
本门新增两个显式测试类：磁盘满闩锁行为，以及 NFS 下的日志模式选择。

### F3. fencing 是必需品，不是过度设计

没有重核查的参考实现 fencing 令牌。Codex 公开的写锁绕过议题是直接证据：
单机建议性锁正是我们已接受设计所防御的失败模式。

### F4. 采纳摘要

逐事件一行原样存储；带升级方向拒绝的 pragma 门控版本化迁移；WAL 加显式
synchronous 模式、busy 超时与日志模式选择；SQLite 事务之上的提交结果
三元组；回执前 fsync；备份门控的破坏性操作；SQLite 内可重建投影；作为
受测资源限制的磁盘满闩锁。

### F5. 拒绝摘要

失败开放跳过；异步 flush 权威；双活动存储；无 fencing 的锁；粘性毒化
持久化；头部冻结的投影可见性；复制参考形态。

### F6. DeepSeek Harness 边界在重核查中成立

2026-08-15 的采纳/拒绝表一天后仍然正确。其持久化 seam 贡献格式拒绝、
崩溃闭合、残缺尾行与惰性物化契约。其 SQLite 后端没有记录 WAL、事务、
CAS 或 fencing 纪律——Slice 2 的工业核心必须来自我们已接受的设计加
SQLite 自身语义，而不是 DeepSeek Harness 形态。

### F7. 顺序细节吸收进后续切片

校验和压缩审计帧与残缺尾行导入规则记录给 Slice 3 门；合成崩溃闭合与
有界调和记录给 Slice 4 门。本门既不设计也不实现它们。

## 证据边界

- DeepSeek Harness 是声明会破坏兼容性的 developer preview；WAL、事务、
  CAS 文档的缺失是文档的缺失，不是代码中已验证的缺失。
- 观察是所列提交与日期上的时间点快照。未对参考项目做运行时测试、模糊
  测试或基准测试。
- 未发布的不变量仍然未知。议题链接记录观察时的报告缺陷，不是已修复
  状态。
- 本门不实现任何东西。Slice 2 的规范、计划与证据台账随后另行产出并引用
  本文档。
