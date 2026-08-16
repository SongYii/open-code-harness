# SQLite 规范 EventStore — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**权威：** [SQLite 规范 EventStore（Slice 2）设计](../superpowers/specs/2026-08-16-sqlite-canonical-eventstore-design.md)

**端口：** [EventStore v2](eventstore-v2.md) 中不变的 `application.EventStore` 接口与 `StoreError` 代数

**包：** `internal/harness/adapters/sqlite`

## 范围

EventStore v2 端口之后的纯 Go（`CGO_ENABLED=0`）SQLite 适配器。SQLite
是唯一在线提交权威。Slice 2 交付：经过校验的打开画像、完整形态的版本化
迁移、带精确重试的追加事务、钉住头的分页读取、fencing 租约原语、经过
校验的备份，以及离线投影重建。它以零套件改动通过共享的
`eventstoretest` 一致性套件。

## 打开画像

每次打开时在迁移之前配置并校验：`journal_mode` 必须实际为 `wal`
（其他任何值都带位置诊断失败封闭）、`synchronous=FULL`、外键强制、
有界 busy 超时、显式 WAL checkpoint 策略，以及面向
`unixepoch('subsec')` 的捆绑 SQLite 版本门。可配置拒绝列表对已知的
网络或同步位置给出诊断。一条专用写者连接拥有每个 `BEGIN IMMEDIATE`
事务；读取使用带显式读事务的有界池。

## 模式

有序版本化迁移一次性创建完整目标形态（迁移 1）加回执验证索引
（迁移 2）：`store_metadata`（单例）、`event_streams`、`event_appends`
（唯一 `append_id`、`commit_position`；审计链列在 Slice 3 前保持零值）、
`events`（唯一 `(session_id, sequence)`、全局唯一 `event_id`、规范记录
负载与摘要）、`command_requests`（全局唯一 `run_turn_request_id`）、
`domain_identities`（唯一 `(session_id, identity_kind, identity_id)`）、
`runtime_leases`（单例）、`export_outbox`/`transcript_entries`/
`snapshots`/`export_checkpoints`（存在、后续切片前不维护）与
`schema_migrations`。

由更新格式标记的数据库以指向升级的错误拒绝；`user_version` 与历史
不一致是损坏；篡改的元数据失败封闭。

## 追加事务

在写者连接上 `BEGIN IMMEDIATE`：按 `AppendID` 与请求摘要解析回执
（精确重试在流前进后返回原回执；摘要不匹配是
`AppendIdentityMismatch`；解析到的回执与其实际提交的事件交叉核对）、
租约所有权谓词（否则 `WriterFenced`）、准许查找
（`CommandRequestConflict`/`CommandIdentityMismatch`）、版本 CAS
（`VersionConflict`）、限制与身份校验
（`InvalidAppend`/`DomainIdentityConflict`）、全局提交位置递增、连续
序号分配、回执/准许/事件/身份写入、同步 `session_heads` upsert 与头
位置更新。批次要么整体可见要么整体不存在。

结果码映射：有界内的 busy/locked 到 `StoreUnavailable` 并保留码；被
串行化写者路径上的约束与完整性失败是 `StoreCorrupt`；环境失败是
`StoreUnavailable`。一旦尝试 COMMIT，失败绝不被转换为确定性未提交：
适配器释放或隔离写者连接，在新连接上执行恰好一次有界回执查找，
否则返回带 `MayHaveCommitted` 的 `CommitOutcomeUnknown`。

## 读取路径

`ReadStream` 每次调用服务钉住在单个 WAL 快照的独占 `AfterSequence`
分页；无法服务的钉住头是 `InvalidRead`，绝不静默空页。`ResolveAppend`
只读（committed/not_found/identity_mismatch）。`FindCommandRequest`
同时比较 Session 与摘要，绝不泄露其他 Session 的记录。

## fencing 租约原语

Open 在自己的 `BEGIN IMMEDIATE` 中获取单例 `runtime_leases` 行：缺失
或过期的租约以单调递增的令牌取得；活动的外来租约拒绝打开；同
runtime 重开即续约。`RenewLease` 延长期限并更新心跳；续约过期租约被
fence。SQLite 的 `unixepoch('subsec')` 是唯一租约时钟。每次追加在写
事务内校验 `runtime_id`、`fencing_token` 与期限。宿主生命周期（心跳
调度、接管策略、启动调和）属于 Slice 4，本切片有意缺失。

## 投影、备份、重建

`session_heads` 是唯一同步投影，通过与
`RebuildAndVerifySessionHeads` 共享的事件类型转移走线推导；后者重放
规范流并把任何不一致报告为损坏。投影绝不被当作权威。`Backup` 生成
一致快照副本（因纯 Go 驱动未导出 Online Backup API 而使用
`VACUUM INTO`），并在报告成功前校验副本的模式版本、连续性与不变量
计数是否与活库一致。

## 排除项

- 审计信封、摘要链与 outbox 维护——Slice 3（列以零值存在）。
- Runtime Host 生命周期：心跳调度、接管、崩溃调和、优雅关停——
  Slice 4。
- `transcript_entries`、`snapshots`、`export_checkpoints`——仅模式。
- JSONL、导入/导出、ACP、TUI——不在范围内。
- GA 阻塞项：没有进程级崩溃注入框架、没有长时间浸泡或损坏模糊
  测试、除租约谓词测试外没有多进程写者证据。
