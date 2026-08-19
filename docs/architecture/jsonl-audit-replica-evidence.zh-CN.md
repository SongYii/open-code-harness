# JSONL 审计副本完成证据（中文阅读版）

**状态：** Slice 3 完整证据台账

**合同：** [JSONL 审计副本 — 已实现合同](jsonl-audit-replica.md)

**分支：** `agent/jsonl-audit-replica`

英文版本 [jsonl-audit-replica-evidence.md](jsonl-audit-replica-evidence.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 提交

| 提交 | 任务 | 内容 |
| --- | --- | --- |
| `056d0ff` | 文档 | Slice 3 与 Slice 4 双语规范和计划（含宿主恢复门） |
| `3feadff` | 文档 | Slice 3 与 Slice 4 双语架构门 |
| `721bdf4` | 任务 1 / PR 1 | 审计编解码器 v1：冻结信封、摘要链、篡改夹具、失败封闭注册表 |
| `721bdf4`（审计链） | 任务 2 / PR 2 | 追加事务内的链维护、迁移 3 与编解码器 v1 回填及确定性门 |
| `b41e884` | 任务 3 / PR 3 | 崩溃收敛导出器：staging/密封/manifest/检查点发布、重启清单、规范重建、outbox 修剪 |
| （一致性导出与导入） | 任务 4 / PR 4 | `ExportConsistent`、八步校验导入、逐层拒绝矩阵 |
| （本台账） | 任务 5 / PR 5 | 基准、已实现合同、证据 |

## 验证证据

命令与观察结果（Apple M1，go 1.26.4）：

- `go test ./... -count=1` —— 全部包通过；追加事务加入信封维护后
  `eventstoretest` 一致性仍零改动全绿。
- `go test ./internal/harness/adapters/sqlite/ -count=1 -race` —— 通过。
- 编解码器：规范字段顺序、无损往返、摘要覆盖每个字段（formatVersion
  由构造守卫加解码时摘要校验）、创世常量、注册表失败封闭。
- 链集成：审计列/outbox/头摘要与批次原子（故障点回滚证明）；精确
  重试保持单一信封；回填确定性精确复现已维护链；错误预置摘要中止。
- 发布矩阵：幂等重发布；增量导出写入不可变新世代；staging 残留被
  丢弃；落后于 manifest 的检查点收敛；丢失的副本从规范数据再生出
  字节一致的分段；篡改分段与冲突世代隔离；外来活动导出租约拒绝。
- 导入：全验证通过路径；篡改、缺失、残尾、错误头摘要的副本全部
  拒绝；摘要合法但序号跳空的手工信封被深层捕获；非空目标拒绝。

## 基准样本

```text
BenchmarkAppend1Event-8       20    335821 ns/op    14388 B/op     336 allocs/op
BenchmarkAppend8Events-8      20    740879 ns/op    48133 B/op     896 allocs/op
BenchmarkReadStreamPaged-8    20   4170244 ns/op  2893469 B/op   66921 allocs/op
BenchmarkBackup-8             20   2357612 ns/op    13517 B/op     184 allocs/op
BenchmarkExportOnce-8         10   24056229 ns/op  2461054 B/op   50337 allocs/op
BenchmarkImportAudit-8        10   11096696 ns/op  2881996 B/op   66861 allocs/op
```

信封维护带来的追加开销：单事件 336µs 对 Slice 3 前的 288µs（约
17%）；8 事件 741µs 对 602µs（约 23%）。`ExportOnce` 排空 100 追加
（201 事件）的库并含完整重启清单；`ImportAudit` 校验并落地 50 追加
的副本。

## 与已接受设计的偏差

机制上无偏差。分段文件名中的摘要前缀取前六字节以便阅读；manifest
记录完整摘要。

## 延迟的 GA 阻塞项

- 目录同步警告（派生文件可能丢失；领域事实不会）的断电设备测试。
- 租约拒绝测试之外的多进程导出器争用。
- 长时间副本浸泡与针对导入的对抗性模糊测试。
