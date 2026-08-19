# JSONL 审计副本 — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**权威：** [JSONL 审计副本与导入（Slice 3）设计](../superpowers/specs/2026-08-16-jsonl-audit-replica-design.md)

**基线：** [SQLite 规范 EventStore](sqlite-eventstore.md)

**包：** `internal/harness/adapters/sqlite`

英文版本 [jsonl-audit-replica.md](jsonl-audit-replica.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 范围

Slice 3 在追加事务内激活审计链，以编解码器 v1 回填 Slice 3 之前的
历史，并交付崩溃收敛的导出器、副本布局、一致性导出与八步校验导入。
SQLite 仍是唯一在线提交权威；JSONL 是完整、无损的审计副本——
绝不是在线提交点、不是对等权威、不会静默覆盖活库。

## 审计编解码器 v1

每个原子追加一行 JSONL，字段顺序冻结：`formatVersion`（1）、
`commitPosition`、`appendId`、`commandId`、`sessionId`、
`expectedVersion`、`firstSequence`、`lastSequence`、`committedAt`
（RFC3339Nano）、`previousDigest`、`events`（逐字的规范记录负载）、
`batchDigest`。`batchDigest` 是对不含自身的规范信封字节的 SHA-256；
`previousDigest` 从固定创世种子按提交位置链接批次。编解码器注册表按
`event_appends.audit_format_version` 解析；缺失编解码器即损坏，导出
与导入失败封闭。

## 追加事务集成

在同一个 `BEGIN IMMEDIATE` 内、事件批次之后、COMMIT 之前：计算
信封、写入 `event_appends` 审计列、把精确规范信封保留进
`export_outbox`、推进 `head_audit_digest`——全部与批次原子。精确
重试返回原回执而不重新编码。端口与错误代数不变；一致性套件零改动
仍全绿。

## 回填（迁移 3）

迁移 3 创建 `export_leases` 并在一个单写者事务中按提交位置顺序以
编解码器 v1 回填每条 Slice 3 之前的追加，填充审计列、outbox 行与头
摘要。与重算不一致的预置摘要失败封闭中止。全新数据库以创世摘要
打开。

## 导出器与重启状态机

`ExportOnce` 先跑清单：丢弃 staging；对 SQLite 摘要校验不可变
manifest 世代及其密封分段；选择不晚于 SQLite 头的唯一最高连续有效
世代（同一头两个冲突有效世代隔离副本）；从已验证证据重算检查点
——超前或落后于 manifest 的检查点是修复证据，绝不是权威。待导出
位置优先使用保留的 outbox 信封，否则从规范字节重编码；重编码必须
精确复现存储摘要。每个分段的发布：staging → sync → close → 重开 →
校验 → 改名为不可变的 `segments/<first>-<last>-<digest>.jsonl`；
分段边界 1,000 位置或 4 MiB。发布按提交区间与摘要幂等；分歧隔离。
已验证世代与事务性检查点之后，修剪已覆盖的 outbox 行（摘要保留在
`event_appends` 上）。

`ExportConsistent(target)` 固定目标位置，把截至该位置的全部批次
发射到全新目录，并写入命名目标批次摘要的自包含 manifest 世代。它
绝不触碰导出器检查点。普通文件拷贝不是受支持的导出过程。

## 导入

`ImportAuditReplica` 按序校验：manifest 与分段摘要；连续提交位置与
批次哈希链；事件负载规范性与摘要；每会话连续序号；expected-version
转移；已知模式版本；完整领域重放；落地后重建 heads 投影。残缺最后
一行拒绝整个导入——绝不静默丢弃。导入只写新的或空的数据库；禁止
自动合并进活动数据库。

## 排除项

- 脱敏导出（独立命令，后续切片）。
- 宿主拥有的导出器调度与生命周期（Slice 4 接线）。
- `command_requests` 不属于审计信封，导入不重建。
- GA 阻塞项：无目录同步警告的断电设备测试；除租约拒绝测试外无
  多进程导出器争用；无长时间副本浸泡。
