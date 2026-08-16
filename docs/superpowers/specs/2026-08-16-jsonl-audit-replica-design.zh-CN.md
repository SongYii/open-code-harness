# JSONL 审计副本与导入（Slice 3）中文阅读版

**状态：** 草案，待架构门证据

**日期：** 2026-08-16

**父设计：** [生产 Runtime 持久化、恢复与客户端边界](2026-08-13-runtime-persistence-recovery-client-design.md)

**架构门：** `docs/research/architecture-gates/2026-08-16-jsonl-audit-replica.md`（证据收集中；本规范按父设计第 10 节与 7.5 节冻结切片决策）

**已实现基线：** [SQLite 规范 EventStore](../../architecture/sqlite-eventstore.md)

## 1. 决策摘要

Slice 3 激活 Slice 2 创建但未维护的审计链，并构建导出器、副本布局、
重启状态机、一致性导出与导入。SQLite 仍是唯一在线提交权威；JSONL 是
完整、无损的审计副本——绝不是在线提交点、不是对等权威、不会静默
覆盖活库。

承重切片决策：

1. 审计编解码器注册表随二进制版本化；
   `event_appends.audit_format_version` 是其唯一选择键。已提交格式的
   编解码器不能从受支持的升级路径中移除。缺失的编解码器是
   `StoreCorrupt`；导出与导入失败封闭。
2. 追加事务在同一个 `BEGIN IMMEDIATE` 内新增（端口与错误代数不变）：
   批次信封计算、`event_appends` 审计列、一条 `export_outbox` 行、
   `head_audit_digest` 维护。哈希链由提交位置顺序创建，而不是由异步
   导出器创建。
3. 单写者回填迁移以编解码器 v1 编码每条 Slice 3 之前的追加，从创世
   起按提交位置构建链。回填中的任何摘要不匹配都失败封闭。
4. 导出器是库组件（`ExportOnce`、有界循环）；宿主驱动的调度属于
   Slice 4。
5. 导入只写入新的或空的数据库。禁止自动合并进活动数据库。

## 2. 目标

- 每个原子追加一行 JSONL（批次信封，格式版本 1），带按提交位置排序
  的 `previousDigest`/`batchDigest` 哈希链。
- 分段、manifest 跟踪、不可变的副本布局，发布经过校验。
- 崩溃收敛的导出器重启，绝不信任单一可变检查点。
- `export --consistent` 产出经校验、自包含、固定提交位置的副本。
- 导入按父设计八步校验，且只进新库。
- 分歧处理严格按父设计策略表执行。

## 3. 非目标

- 脱敏导出（独立命令，后续切片）。
- 宿主心跳调度与导出器生命周期归属——Slice 4。
- JSONL 的任何写入者角色、任何自动合并导入、任何对等权威比对 API。

## 4. 审计编解码器 v1

信封是父设计 10.2 节的形态，采用规范 JSON 编码：`formatVersion` 1、
`commitPosition`、`appendId`、`commandId`、`sessionId`、
`expectedVersion`、`firstSequence`、`lastSequence`、`committedAt`、
`previousDigest`、`events`（规范事件负载）、`batchDigest`。
`batchDigest` 是对不含自身的规范信封字节的 SHA-256；`previousDigest`
按提交位置链接到前一批次。链种子是固定的创世常量。编解码器 v1 的
往返夹具按每个已提交格式版本冻结在树中。

规范事件负载与 SQLite 适配器存储的冻结 `domain` 记录编码相同；信封
事件字节必须精确复现存储的 `events.payload_digest`，否则导出失败
封闭。

## 5. 追加事务集成

在 `events` 批次插入之后、COMMIT 之前，事务现在还：

1. 以编解码器 v1（未来格式存在后，按行的 `audit_format_version`
   选择）计算信封字节；
2. 写入 `event_appends.audit_format_version`、`previous_audit_digest`
   与 `batch_audit_digest`；
3. 把精确的规范信封插入 `export_outbox`（发布挂起期间逐字保留——
   导出器绝不以不同方式重新编码活动追加）；
4. 把 `store_metadata.head_audit_digest` 推进到新批次摘要。

精确重试解析仍返回原回执而不重算信封。端口、错误代数与可见性原子性
不变；这是 Slice 2 声明的约束。

## 6. 回填迁移 3

迁移 3 增加 `export_leases` 表（父设计 7.8）并在一个单写者事务中
回填：按提交位置顺序用编解码器 v1 编码每条既有 `event_appends` 行、
填充其审计列、插入其 `export_outbox` 行，并调和
`head_commit_position`/`head_audit_digest`。回填确定性被断言：重新
编码任何行必须精确复现存储摘要，否则迁移中止、打开失败封闭。迁移 3
不触碰 `events`、`event_streams` 或领域表。

## 7. 副本布局与发布

严格按父设计 10.3 节布局：可丢弃的 `manifest.json` 提示、不可变的
`manifests/<head-position>-<head-digest>.json` 世代、密封不可变的
`segments/<first>-<last>-<digest>.jsonl`、可丢弃的 `staging/`。密封
要求 写入 → sync → close → 重开 → 校验字节与摘要 → 发布。分段边界：
1,000 个提交位置或 4 MiB，先到为准（文件名区间保持按位置）。导出器
运行时持有 `export_leases` 行；该租约绝不授权领域追加。

发布按提交区间与摘要幂等：相同区间与摘要即已完成；相同区间不同摘要
隔离副本并触发重建。导出失败绝不回滚或伪造领域追加。

## 8. 导出器重启状态机

启动清单按父设计 10.5：丢弃 staging；对 SQLite 摘要校验不可变
manifest 世代及其密封分段；选择不晚于 SQLite 头的唯一最高连续有效
世代（同一头两个冲突的有效世代隔离副本）；只有当日标区间严丝合缝时
才收养未被命名的密封分段；从规范字节与冻结编解码器重建缺失或无效的
派生分段；从已验证世代事务性地重算 `export_checkpoints`（超前或落后
于 manifest 的检查点是修复证据，绝不是权威）；从下一提交位置恢复。

一致性测试矩阵覆盖每个发布边界：分段发布后崩溃、manifest 发布后
崩溃、检查点更新前崩溃、staging 中途崩溃——每个都通过同一清单收敛。

## 9. 一致性导出与导入

`ExportConsistent(target)` 在 SQLite 读快照中固定目标提交位置，发射
截至该位置的全部批次，并写入自包含的 manifest 世代。普通文件拷贝
不是受支持的导出过程。

`Import(path)` 只写入新的或空的数据库，并按序校验：manifest 与分段
摘要；连续提交位置与批次哈希链；事件负载摘要；每会话连续序号；
expected-version 到 last-sequence 的转移；已知模式与确定性升格器；
完整领域重放不变量；重建 heads 投影。任何失败都以分类错误拒绝导入；
部分导入的staging库被丢弃。禁止自动合并进活动数据库。

## 10. outbox 修剪

密封分段与 manifest 世代校验通过且其 SQLite 检查点提交后，可修剪
已覆盖的 `export_outbox` 信封行。永久的 `event_appends` 行、事件
字节、格式版本与摘要保留；从冻结编解码器重建必须精确复现存储摘要，
否则失败封闭。

## 11. 测试证据

1. 编解码器 v1 往返与链夹具；信封每个字段的篡改检测。
2. 追加集成：审计列、outbox 行与头摘要同批次事务化（故障点下全有
   或全无）。
3. Slice 3 之前数据库上的回填确定性；损坏即中止。
4. 发布矩阵：每个崩溃边界收敛；幂等重发布；摘要分歧隔离。
5. 导入：全验证通过路径；八步中每一步失败都拒绝；拒绝写入非空
   数据库。
6. 分歧策略表作为可执行测试。
7. 基准：导出吞吐、导入吞吐、信封计算带来的追加开销。

## 12. 交付计划

1. **审计编解码器 v1**——信封、摘要、链、冻结夹具。
2. **事务集成与回填**——迁移 3、`export_leases`、追加事务内的信封
   维护、确定性门。
3. **导出器与重启状态机**——staging/密封/manifest/检查点发布、清单
   收敛、修剪。
4. **一致性导出与导入**——快照导出、八步校验导入、分歧矩阵。
5. **文档与证据**——已实现合同、证据台账、双语、索引更新。

## 13. 排除项

没有脱敏导出；没有宿主拥有的导出器调度；没有 JSONL 写入权威；没有
自动合并；没有跨副本 diff API；没有审计轨的压缩。
