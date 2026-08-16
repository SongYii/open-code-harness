# JSONL 审计副本架构门（中文阅读版）

**状态：** 完整研究证据

**日期：** 2026-08-16

**范围：** Slice 3（JSONL 审计副本与导入）第一手来源重核查：导出格式
与组帧、发布一致性、导入校验、索引与 manifest，以及事务性 outbox
模式。

本文档是研究证据。它不改变已接受设计，也不授权复制参考项目的格式。

英文版为规范研究记录，本文件是同步的中文阅读版。

## 问题

1. 参考集是否确认已接受的副本设计——事务性 outbox、带哈希链的批次
   信封、不可变密封分段、manifest 世代、仅入新库的校验导入？
2. Slice 3 应当采纳哪些观察到的发布与修复机制？
3. 哪些观察到的导入与容忍行为与章程冲突？

## 重核查的第一手来源

| 来源 | 观察状态 |
| --- | --- |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | `47f9438`，2026-08-13 |
| [OpenAI Codex](https://github.com/openai/codex) | `9ded177`，2026-08-16 |
| [Kimi Code](https://github.com/MoonshotAI/kimi-code) | `84da662`，2026-08-16 |
| [Pi agent core](https://github.com/badlogic/pi-mono) | `d3ab2af`，2026-08-16 |
| [Grok Build](https://github.com/xai-org/grok-build) | `5163763`，2026-08-15 |
| [事务性 outbox](https://microservices.io/patterns/data/transactional-outbox.html) | 模式权威 |

## 观察到的契约与边界

| 来源 | 观察到的契约 | Slice 3 决策 | 边界 |
| --- | --- | --- | --- |
| microservices.io | 在"更新业务实体的同一事务"中写 outbox 消息；中继异步发布并保序；投递至少一次，因此"消费者必须幂等，比如跟踪已处理消息的 ID"。 | 确认已实现的"追加事务内 outbox + 按区间与摘要幂等发布"设计。 | — |
| Codex | 迁移状态机："canonicalize into a staged JSONL file, project that staged file into SQLite, verify the projection, then atomically publish it... we always leave behind either the original legacy rollout or a recoverable paginated rollout"。 | 确认先验证后发布的顺序：即我们的 staging → sync → 重开 → 校验 → 密封序列。 | — |
| Codex | 导入按内容摘要去重并有导入台账；`session_index.jsonl` 的 "Name updates are append-only; the most recent entry wins"。 | 台账思路记录给未来的可续导入；不属于 Slice 3 范围（我们的导入是进新库的单事务）。 | Codex 导入"信任源文件（未见签名/校验步骤）"——拒绝；我们的导入在落地前过八层校验。 |
| DeepSeek Harness | JSONL 默认"stored as checksummed concatenated Zstandard frames by default or raw lines by configuration"；"append resolves only after durability"；写失败会"rolls the file back to its prior byte length"。 | 确认校验和组帧的默认姿态；我们的逐行信封摘要加分段/manifest 摘要以无损方式覆盖同一地面。 | 其 raw-lines 配置"no checksums"——作为模式被拒绝；我们永远做摘要。 |
| DeepSeek Harness | 残尾处理："only a torn final record is discarded"；格式拒绝"distinct from SessionPersistenceCorruptionError because nothing is damaged"。 | 导入采纳：staging 导入的残尾最后一行被拒绝（绝不静默丢弃），格式版本拒绝与损坏区分。 | — |
| Pi | 原子发布："Build a complete sibling temporary file, then atomically rename it over the destination... the destination is untouched until the rename commits"；残尾以"atomically publishing the valid prefix"修复。 | 确认 manifest 的 staging+rename 发布。分段密封已用 写入-sync-改名。 | 前缀重发布修复属于活写者模型；我们的密封分段不可变，用重建取代。 |
| Kimi Code | 索引追加依赖 POSIX 单写原子性（"well under PIPE_BUF"）；读取时跳过畸形索引行。 | 单写原子性对我们不足（分段是多行的）；拒绝。 | 读取时失败开放跳行被处处拒绝，包括导入。 |
| Grok Build | `FlushAndAck` 仅在 "`flush_pending()` finishes writing to disk" 后完成；破坏性重写有备份门控："back up the on-disk history first, and only rewrite if the backup landed: recoverability gates the destruction"。 | 持久化后确认的屏障与我们的导出器检查点语义一致。 | 全历史替换式重写（其压缩 strip）与不可变审计分段矛盾——拒绝。 |

## 拒绝的形态

1. **未校验导入**（Codex 外部迁移信任源）：每条导入行在落地前通过
   八层校验。
2. **读取时失败开放跳行**（Kimi wire-scan）：畸形行拒绝或隔离，绝不
   静默丢弃。
3. **可选摘要**（DeepSeek Harness raw-lines 模式）：摘要永远开启。
4. **副本的全历史替换重写**（Grok Build strip）：密封分段与 manifest
   世代不可变；损坏触发隔离与从规范字节重建。
5. **以单写原子性作为持久化叙事**（Kimi 索引追加）：多行工件先
   staging、sync、校验、再发布。

## 发现

### F1. 已接受的副本设计得到确认

事务性 outbox 权威源加上参考集中的先验证后发布与备份门控破坏模式，
与已实现的追加事务集成、staged 发布、不可变世代相吻合。

### F2. 采纳清单

摘要永远开启；staging → sync → 重开 → 校验 → rename 发布；按区间与
摘要的至少一次幂等投递；格式拒绝与损坏区分；导入对残尾最后一行
拒绝。

### F3. 拒绝清单

未校验导入；失败开放跳行；可选摘要；副本重写；对多行文件的单写
原子性主张。

### F4. 记录待后用

Codex 的导入台账与内容摘要去重（面向可续导入）；Grok Build 的磁盘满
闩锁已由 Slice 2 分类测试覆盖。

## 证据边界

- 观察是所列提交上的时间点快照；未对参考项目做运行时测试。
- Pi 仓库已迁移（badlogic/pi-mono → earendil-works/pi）；下个门需更新
  链接。
- 本门不实现任何东西；Slice 3 的规范、计划与证据台账引用本文档。
