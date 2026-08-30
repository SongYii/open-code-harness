# SQLite 规范 EventStore — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**权威：** [SQLite 规范 EventStore（Slice 2）设计](../superpowers/specs/2026-08-16-sqlite-canonical-eventstore-design.md)

**端口：** [EventStore v2](eventstore-v2.md) 中的 `application.EventStore` 接口与 `StoreError` 代数；[ACP 会话生命周期（切片 B）](acp-session-lifecycle-evidence.md) 为该接口新增 `ListSessionHeads`

**包：** `internal/harness/adapters/sqlite`

英文版本 [sqlite-eventstore.md](sqlite-eventstore.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


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

迁移 4（`session head catalog`，切片 B）原地替换 `session_heads`。已有数据
的表无法直接新增无默认值的 `NOT NULL` 列，因此迁移 4 先建一张具备最终形态
的影子表 `session_heads_v4`——`session_id`、`workspace_root NOT NULL`、
`status CHECK (idle|running|closed|deleted)`、`active_turn_id`、
`active_item_id`、`updated_at_commit_position NOT NULL REFERENCES
event_appends(commit_position)`——按稳定的 `session_id` 顺序扫描每一行
`event_streams`，用 `domain.Replay` 对每条流做规范重放，再插入推导出的行。
若该会话已有版本 3 的旧行，则与重放结果交叉核对：旧的 `active` 只与推导出
的 `running` 比较相等；`idle` 与 `closed` 精确比较；推导出的 `deleted` 与
旧的 `idle` 或 `closed` 比较相等——`session.deleted` 出现得比这次迁移早，
迁移 4 之前的逐次追加投影根本没有处理它的分支，所以一个在迁移 4 存在之前
就被删除的会话，其旧状态会停留在删除前的那个值（只可能是 idle 或
closed，因为删除要求会话是 idle，绝不可能是 active）；active turn/item ID
与 `updated_at_commit_position` 也必须一致；任何其他不一致，或没有匹配
`event_streams` 行的孤儿旧行，都是 `sqlite database corrupt`。没有旧头行的
版本 3 会话则直接重建。每条流都验证完毕后，迁移丢弃旧表、把影子表改名为
`session_heads`，并创建：

```sql
CREATE INDEX session_heads_visible_by_workspace
ON session_heads (workspace_root, updated_at_commit_position DESC, session_id DESC)
WHERE status <> 'deleted';
```

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
事务内校验 `runtime_id`、`fencing_token` 与期限。`Authority` /
`CurrentAuthority` 在写锁下返回活的租约状态，因此过期接管造成的令牌
轮转对下一次追加可见。宿主生命周期（心跳调度、接管策略、启动调和）
属于 Slice 4，本切片有意缺失。

## 投影、备份、重建

`session_heads` 是唯一同步投影。追加路径应用领域转移语义；
`RebuildAndVerifySessionHeads` 通过独立重放规范流来校验同一语义，并把任何
不一致报告为损坏。投影绝不被当作权威。每次追加都从重放出的
`session.created` 根推导 `workspace_root`（绝不来自调用方传入的值），并在
写入规范追加的同一个 `BEGIN IMMEDIATE` 事务里，连同
`status`/`active_turn_id`/`active_item_id`/`updated_at_commit_position` 一起
upsert；`session.deleted` 事件在同一次写入中把 `status` 转为 `deleted`。
`Backup` 生成一致快照副本（因纯 Go 驱动未导出 Online Backup API 而使用
`VACUUM INTO`），并在报告成功前校验副本的模式版本、连续性与不变量
计数是否与活库一致。

## 会话头目录（`ListSessionHeads`，切片 B）

`ListSessionHeads(ctx, ListSessionHeadsRequest{WorkspaceRoot, Cursor,
Limit}) (SessionHeadPage, error)` 打开一个普通读事务，绝不向调用方泄露
SQL。`WorkspaceRoot` 必须已是规范值（已应用
`application.CanonicalWorkspaceRoot` 且未改变）；`Limit` 必须在 `1..256`
之间；Service 调用方把 `Limit` 固定为 50。它在 SQL 里过滤
`status <> 'deleted'`——线上的 `SessionHeadStatus` 从不出现 `deleted`
值——按 `updated_at_commit_position` 关联 `event_appends` 得到
`committed_at_unix` 并转换为 UTC，再用上面那个部分索引按
`(updated_at_commit_position, session_id)` 降序请求 `Limit + 1` 行。多出
的那一行只用于判断是否要设置 `NextCursor`，绝不返回给调用方。

cursor 对本包之外不透明：JSON 对象 `{"v":1,"p":<最后返回行的提交位置>,
"s":"<其会话 id>"}` 的 base64url（严格解码，无 padding）编码，上限
512 字节，且只由最后一行真正返回的行构造。畸形、超限、非规范 base64
或版本不对的 cursor 是校验错误，不是静默空页。每一页都是一次 SQLite
快照；该端口不保证多页快照，并发追加可以把某个会话移动到另一页。

每个 EventStore 实现（SQLite 与共享的内存/一致性适配器）都针对同一个端口
契约与夹具套件实现 `ListSessionHeads`，因此不论用哪个适配器，ACP
`session/list` 看到的可见状态与 cursor 行为都一致。

Runtime Host 启动调和使用的 `ActiveSessions` 枚举 `status = 'running'`——
这是 v4 的拼写，不是迁移 4 已改写掉的旧 `active` 值。

## OpenReader

`OpenReader(ctx, ReaderConfig) (*Reader, error)` 仅为钉住的 `ReadStream`
打开 Path。它是加法接口：既有 `Open` 不变。`Reader` 不是第二个
EventStore——没有 `Append`、`ResolveAppend`、`FindCommandRequest`。

`ReaderConfig` 是读画像：`Path`、`BusyTimeout`（默认 5s；允许范围
100ms–60s，与 `Config.BusyTimeout` 相同）、`DeniedPathPrefixes`（与
`Open` 同一诊断）以及 `WALAutoCheckpoint`（默认 1000；仅作为读侧
pragma）。它不含 `RuntimeID` 或 `LeaseDuration`。

打开画像在返回前校验：

- WAL。**不**设置 `immutable=1`（必须看见活写者的最近一次提交）。
- `synchronous=FULL`、`foreign_keys=1`、有界 `busy_timeout`。
- `query_only=1` 与 `mode=rw`（缺失文件被拒绝，绝不创建）。
- `DeniedPathPrefixes`——`Open` 会拒绝的网络或同步位置在此同样拒绝。
- `user_version` 必须等于本二进制的最新迁移。更新为
  `FormatNewerError`。更旧以 “writer must migrate first” 拒绝。
  OpenReader 不运行 `migrate`。
- 损坏元数据与 `Open` 读路径一样失败封闭。

OpenReader 不获取 `runtime_leases` 或 `export_leases`。活写者可继续持有
fencing 租约；读者在 `SQLITE_BUSY` 上等待至多 `BusyTimeout`，而不是立即
失败。`ReadStream` 与写者使用同一钉住头分页函数。

`composition.ExportSession` 传入 `ReaderConfig{Path: databasePath}` 并取
默认值。会话转录 JSONL 记载于 [会话转录](session-transcript.md)；它不是
本适配器的审计副本，也不填充 `transcript_entries`。

## 排除项

- 审计信封、摘要链与 outbox 维护——Slice 3（列以零值存在）。
- Runtime Host 生命周期：心跳调度、接管、崩溃调和、优雅关停——
  Slice 4。
- `transcript_entries`、`snapshots`、`export_checkpoints`——仅模式。
  会话转录 JSONL 是导出投影，不是这些表。
- JSONL 审计副本、ACP、TUI——不在本适配器写者合同范围内。
  `OpenReader` 是会话导出使用的加法读路径。
- GA 阻塞项：没有进程级崩溃注入框架、没有长时间浸泡或损坏模糊
  测试、除租约谓词测试外没有多进程写者证据。
