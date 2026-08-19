# SQLite 规范 EventStore（Slice 2）中文阅读版

**状态：** 已接受设计

**日期：** 2026-08-16

**父设计：** [生产 Runtime 持久化、恢复与客户端边界](2026-08-13-runtime-persistence-recovery-client-design.md)

**证据：** [SQLite 规范 EventStore 架构门](../../research/architecture-gates/2026-08-16-sqlite-canonical-eventstore.md)

**本适配器必须满足的已实现合同：** [EventStore v2](../../architecture/eventstore-v2.md)

英文版本 [2026-08-16-sqlite-canonical-eventstore-design.md](2026-08-16-sqlite-canonical-eventstore-design.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 1. 决策摘要

本切片实现第一个持久化规范 EventStore：一个位于已实现
`application.EventStore` 端口之后的纯 Go SQLite 适配器。存储模型、追加
事务、精确重试规则与错误代数以父设计第 7、8 节为规范，此处仅在 Slice 2
冻结边界的地方重述。本规范不改变端口、一致性测试套件或 Domain 行为。

本规范的承重决策是切片决策，不是重新设计：

1. 模式通过版本化迁移一次性创建到完整目标形态。属于后续切片的表
   （`export_outbox`、`transcript_entries`、`snapshots`、
   `export_checkpoints`）从第一个迁移起就存在，但 Slice 2 代码不维护
   它们。
2. fencing 从第一次追加起就是真实的。`runtime_leases` 的获取与续约
   原语以及每次追加的所有权谓词在本切片交付；宿主生命周期策略
   （心跳调度、接管、调和、优雅关停）是 Slice 4。
3. 追加事务遵循父设计算法，但不包含 `export_outbox` 信封插入与审计
   链维护。Slice 3 在同一事务形态内加入这些步骤；端口与错误代数不变。
4. 备份是带拷贝后校验的 SQLite Online Backup API。

## 2. 目标

- 一个 CGO-free、纯 Go 的 SQLite EventStore，不改一行地通过共享的
  `eventstoretest` 一致性套件。
- 在单个 `BEGIN IMMEDIATE` 事务内实现精确追加重试、CAS、准许身份与
  领域身份强制。
- 通过显式读事务的钉住头分页读取。
- 每次追加都经过已验证的 fencing 谓词。
- 失败封闭的损坏与模式版本处理。
- 一致的、经过校验的备份操作。
- COMMIT 处的 SQLite 结果码故障证据，覆盖 busy、full、I/O、
  interrupted 以及 close/rollback 行为。

## 3. 非目标

- JSONL 审计导出、outbox 编码、分段、清单与导入——Slice 3。
- Runtime Host 生命周期：心跳调度、接管、启动调和、优雅关停——
  Slice 4。
- `transcript_entries`、`snapshots` 与面向 Context 的投影——后续
  消费者。
- ACP、TUI 与任何客户端边界。
- 多库分发、复制或静态加密。

## 4. 包与驱动

- 包 `internal/harness/adapters/sqlite` 实现适配器。它只依赖
  `application`、`domain` 和驱动。
- 驱动：`modernc.org/sqlite`，由父设计选定，保证生产构建保持
  `CGO_ENABLED=0`。这是仓库第一个外部依赖；完成证据必须记录它引入的
  依赖与许可证清单。
- 访问层是 `database/sql`。一条专用写者连接拥有所有
  `BEGIN IMMEDIATE` 事务。读取使用有界连接池，并在多语句一致性重要时
  使用显式读事务。连接数量有配置且有界。

## 5. 运行画像

每次打开时配置并校验，先于任何迁移或租约写入：

1. `PRAGMA journal_mode = WAL`——实际返回的模式必须是 `wal`；无法建立
   WAL（例如在网络文件系统上）是失败封闭的打开错误并带诊断，吸收架构门
   中 Grok Build 的 NFS 发现。
2. `PRAGMA synchronous = FULL`，按父设计；提交在回执返回前 fsync。
3. 外键强制开启；有界 busy 超时；显式 WAL checkpoint 策略。
4. 数据库路径必须解析到本地文件系统。已知的网络或同步位置在打开时被
   拒绝并给出醒目诊断。
5. 模式版本门：`PRAGMA user_version`（以及 `store_metadata` 的格式
   版本）先于任何其他检查读取。由更新格式写入的数据库被拒绝并给出指向
   升级方向的错误，绝不报告为损坏——即架构门确认的 DeepSeek Harness
   契约。

繁忙的数据库绝不导致无界的隐藏重试：每次等待都受调用方 context 与
配置约束。

## 6. 模式与迁移

- 迁移是有序、版本化的 SQL 步骤，记录在迁移历史表中；`store_metadata`
  携带存储格式版本与创建/最后迁移元数据。
- 迁移 1 创建完整的父设计目标形态：`store_metadata`、`event_streams`、
  `event_appends`、`events`、`command_requests`、`domain_identities`、
  `runtime_leases`、`export_outbox`、`session_heads`、
  `transcript_entries`、`snapshots`、`export_checkpoints`。
- `event_appends` 上的审计链列与 `store_metadata` 的 `head_audit_digest`
  从迁移 1 起存在并保持零值，直到 Slice 3 激活审计链。Slice 3 在其冻结
  编解码器下以单写者迁移一次性回填；Slice 2 不得创建使该回填不可能的
  形态。
- 编码契约的唯一性位于模式中：`(session_id, sequence)` 唯一、
  `event_id` 全局唯一、`append_id` 唯一、`commit_position` 唯一且连续、
  `run_turn_request_id` 全局唯一、`(session_id, identity_kind,
  identity_id)` 唯一。
- 所有变更 SQL 参数化；动态 SQL 仅限固定语句形态。

## 7. 追加事务

父设计 8.3 节算法是规范。Slice 2 按以下形态执行：

```text
BEGIN IMMEDIATE
  解析 append_id 回执（摘要一致 -> 原回执；
                       摘要不一致 -> AppendIdentityMismatch）
  校验运行时租约谓词（runtime_id、fencing_token、
                      lease_expires_at >= sqlite_now）
  Admission 存在时做准许查找
  读取 event_streams.version（不匹配 -> VersionConflict）
  校验请求限制、ID、模式版本、负载规范性与批次事件唯一性
  在 domain_identities 中预留创建事件身份
  递增 store_metadata.head_commit_position
  分配连续的流序号
  准许时插入 command_requests 行
  插入 event_appends 回执行
  插入完整 events 批次
  upsert event_streams
  upsert session_heads 投影
COMMIT
```

推迟到 Slice 3 加入同一形态的步骤：导出 outbox 信封插入与审计摘要
维护。加入它们不得改变端口、错误代数或可见性原子性；该约束是本规范的
一部分。

批次要么整体可见要么整体不存在。回执解析先于 fencing 校验，因此被
fence 的进程可以得知其精确请求已提交，但不能创建新提交。

## 8. 读取路径

- `ReadStream` 在显式读事务内服务 `(AfterSequence 独占, Limit,
  HeadVersion)` 分页，使钉住在同一 `HeadVersion` 的分页序列观察到单个
  WAL 快照。
- `End`、`NextAfterSequence` 与 `HeadVersion` 遵循已实现的 EventStore
  v2 合同；不再匹配任何可检索快照状态的钉住头是无效读取，不是静默的
  空页。
- 读取绝不取写锁，也绝不在超出有界 busy 超时的情况下阻塞写者连接。

## 9. fencing 与租约原语

- `runtime_leases` 持有单例数据库作用域、`runtime_id`、单调递增的
  `fencing_token`、`lease_expires_at` 与 `last_heartbeat_at`。
- 打开在 `BEGIN IMMEDIATE` 内获取租约：缺失或过期的租约以新令牌
  取得；被其他运行时持有的活动租约被拒绝。续约延长
  `lease_expires_at` 并更新 `last_heartbeat_at`。SQLite 的
  `unixepoch('subsec')` 是唯一租约时钟；调用方绝不提供墙钟时间。
- 每次追加在写事务内校验父设计谓词：`runtime_id =
  request.runtime_id AND fencing_token = request.fencing_token AND
  lease_expires_at >= sqlite_now`；失败映射为 `WriterFenced`。
- 谁调度续约、何时尝试接管、启动如何调和崩溃的持有者，是 Slice 4 的
  策略。本切片交付存储机制，不交付宿主。

## 10. 错误映射

- COMMIT 之前的确定性失败按父代数映射：`InvalidAppend`、
  `VersionConflict`、`AppendIdentityMismatch`、
  `CommandRequestConflict`、`CommandIdentityMismatch`、
  `DomainIdentityConflict`、`WriterFenced`、`StoreUnavailable`。
- 有界内的 SQLite busy/locked 映射为 `StoreUnavailable` 并保留结果码
  作为 cause；它绝不伪装成重试循环。
- 一旦尝试 COMMIT，错误绝不被转换为确定性未提交。适配器按已验证的
  驱动行为终结或隔离原连接，然后在新连接上执行恰好一次有界回执查找：
  摘要匹配返回原回执；缺失或查找不可用返回 `CommitOutcomeUnknown`。
  调用方只能用同一 `AppendID` 解析或重试该精确请求。
- 存储不变量失败（连续性、唯一性假设、回读摘要不匹配、意外的模式
  形态）映射为 `StoreCorrupt`；从此刻起变更失败封闭。
- 任何写入处的磁盘满（`SQLITE_FULL`）作为独立的资源限制类测试，
  依据架构门的采纳清单。

## 11. 投影

- `session_heads` 是唯一在追加事务内同步更新的投影：从规范事件推导的
  最小状态与活动 Turn/Item 候选索引。
- 它绝不被接受为领域状态的独立证明；它浮出的恢复候选必须由权威流
  重放确认。
- 离线的重建并校验操作从规范流重建 `session_heads`，任何不匹配报告为
  损坏。
- `transcript_entries`、`snapshots` 与 `export_checkpoints` 仅存在于
  模式中；没有任何 Slice 2 代码路径读写它们。

## 12. 备份

- 备份操作通过 SQLite Online Backup API 向调用方提供的目标生成一致
  副本，随后打开副本并校验模式版本与核心不变量后才报告成功。
- 父设计的命名规则保持有效：经过校验的备份副本是主备份；与 JSONL
  导出配对是 Slice 3。

## 13. 故障注入与测试

适配器不改一行地通过 `eventstoretest`，并追加适配器专属证据：

1. COMMIT 处的结果码测试：busy、full、I/O error、interrupted 与
   close/rollback 行为，各自断言错误代数结果。
2. 未知结果协议：确认丢失的 COMMIT 通过有界新连接查找解析；缺失给出
   `CommitOutcomeUnknown`，绝不给出虚假的未提交。
3. 并发：不同会话上的并行追加者通过单写者连接串行化，
   `commit_position` 连续且无空洞。
4. 重开：进程突然终止（模拟 WAL 写入期间的崩溃）后重开的数据库能够
   打开、校验并提供一致状态，没有半可见批次。
5. 日志模式校验：无法建立 WAL 的位置在打开时失败封闭。
6. `SQLITE_FULL` 下的磁盘满闩锁行为。
7. fencing：携带过期令牌的追加失败 `WriterFenced`；租约过期拒绝追加
   直到重新获取。
8. 基准：追加吞吐与延迟、分页读取吞吐、备份时长记入证据台账。

## 14. 交付计划

五个经过评审的 PR：

1. **驱动、打开与迁移**——引入依赖、打开时画像校验、模式版本门、
   完整目标形态的迁移 1、失败封闭的损坏路径。
2. **追加事务**——回执解析、CAS、准许、领域身份、回执与事件写入、
   `session_heads` upsert、COMMIT 前错误映射。
3. **读取路径**——读事务内钉住分页、`ResolveAppend`、
   `FindCommandRequest`、读池边界。
4. **fencing 与未知结果**——租约获取/续约、每次追加谓词、COMMIT
   结果码测试、有界回执查找协议。
5. **备份、重建、基准与证据**——Online Backup 拷贝、`session_heads`
   重建校验、基准记录、已实现合同与证据台账更新。

## 15. 完成标准

- `eventstoretest` 对 SQLite 适配器通过且套件零改动。
- 第 13 节的每个测试类都有记录证据。
- Linux/macOS/Windows CGO-free 门通过。
- 依赖与许可证清单已记录。
- 已实现合同文档与证据台账落地并显式声明排除项：审计链未激活、宿主
  生命周期缺失、额外投影未维护。

## 16. 排除项

- 没有审计信封、摘要链或 outbox 维护。
- 没有心跳调度器、接管或崩溃调和。
- 没有转录、快照或上下文投影。
- 没有任何导入、导出或 JSONL。
- 没有 ACP 或 TUI 面。
- 没有厂商驱动备选；驱动决策只能由带基准证据的新门重新审议。
