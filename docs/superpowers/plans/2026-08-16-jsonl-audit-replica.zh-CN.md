# JSONL 审计副本与导入实施计划（中文阅读版）

> **给代理工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 按任务逐条实施本计划。步骤使用复选框（`- [ ]`）语法跟踪。

**目标：** 在追加事务内激活审计链，以编解码器 v1 回填 Slice 3 之前的历史，并交付崩溃收敛的导出器、副本布局、一致性导出与校验导入——SQLite 始终是唯一在线提交权威。

**架构：** 追加事务新增信封/outbox/头摘要维护，端口与错误代数不变。导出器是带清单式重启状态机的库组件。导入只写新库并在成功前校验八层。

**技术栈：** Go 1.26、`database/sql`、`modernc.org/sqlite`、`crypto/sha256`、基于冻结领域编解码器的 `encoding/json`、确定性时间处用 `testing/synctest`、race/benchmark 工具。

英文版本 [2026-08-16-jsonl-audit-replica.md](2026-08-16-jsonl-audit-replica.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 全局约束

- 规范：`docs/superpowers/specs/2026-08-16-jsonl-audit-replica-design.md`；研究证据：Slice 3 架构门。
- `application.EventStore` 端口、`StoreError` 代数与 `eventstoretest` 套件不得改变。
- SQLite 是唯一在线提交权威；除导出器外任何提交决策绝不写 JSONL，任何运行时路径绝不把 JSONL 当权威读。
- `event_appends.audit_format_version` 是唯一编解码器选择键；已提交格式的编解码器不能从受支持升级路径移除；缺失编解码器是 `StoreCorrupt`。
- 链种子是固定创世常量；`previousDigest` 按提交位置链接。
- 分段边界：1,000 个提交位置或 4 MiB，先到为准。
- 发布：写入 → sync → close → 重开 → 校验字节与摘要 → 发布。按提交区间与摘要幂等；分歧隔离并重建。
- 导出失败绝不回滚或伪造领域追加。
- 导入只写新的或空的数据库并校验父设计八步；禁止自动合并。
- 每个行为都是 TDD。每个任务以 `gofmt`、聚焦测试、`go test ./... -count=1`、并发变更时 `-race`、评审与一个小提交收尾。
- 英文为规范；中文计划是同步阅读版并一同提交。

## 文件地图

| 路径 | 职责 |
| --- | --- |
| `internal/harness/adapters/sqlite/auditcodec.go` | 编解码器注册表、信封 v1 编解码、批次摘要、链常量 |
| `internal/harness/adapters/sqlite/auditcodec_test.go` | 每格式版本的往返、链、篡改夹具 |
| `internal/harness/adapters/sqlite/append.go` | 追加事务内的信封维护 |
| `internal/harness/adapters/sqlite/migrations.go` | 代码驱动迁移步骤支持；迁移 3 |
| `internal/harness/adapters/sqlite/migrations_sql.go` | `export_leases` DDL |
| `internal/harness/adapters/sqlite/backfill.go` | 带确定性门的编解码器 v1 回填 |
| `internal/harness/adapters/sqlite/exporter.go` | `ExportOnce`、staging/密封/manifest/检查点发布、清单重启 |
| `internal/harness/adapters/sqlite/exporter_test.go` | 发布与崩溃边界矩阵 |
| `internal/harness/adapters/sqlite/auditimport.go` | 八步校验的新库导入 |
| `internal/harness/adapters/sqlite/auditimport_test.go` | 校验层拒绝 |
| `internal/harness/adapters/sqlite/benchmark_test.go` | 导出/导入/追加开销基准 |
| `docs/architecture/jsonl-audit-replica.md` | 已实现合同 |
| `docs/architecture/jsonl-audit-replica-evidence.md` | 提交、验证、基准、排除项 |

---

### 任务 1（PR 1）：审计编解码器 v1

**文件：**
- 创建：`auditcodec.go`、`auditcodec_test.go`

**步骤：**
- [ ] 失败测试：编码一个批次产生规范定义的精确信封字段顺序与规范 JSON；解码无损往返。
- [ ] 失败测试：`batchDigest` 覆盖除自身外的每个信封字段；翻转任何单字段都破坏它。
- [ ] 失败测试：链常量——创世种子稳定；第一批次的 `previousDigest` 等于种子。
- [ ] 失败测试：信封事件字节精确复现存储的 `payload_digest`；不匹配失败封闭。
- [ ] 失败测试：注册表按 `audit_format_version` 解析编解码器 v1；缺失编解码器报告为损坏。
- [ ] 实现带冻结夹具的编解码器 v1；提交 `sqlite: audit codec v1 with chain and tamper fixtures`。

### 任务 2（PR 2）：事务集成与回填

**文件：**
- 修改：`append.go`、`migrations.go`、`migrations_sql.go`
- 创建：`backfill.go` 及测试

**步骤：**
- [ ] 失败测试：新追加填充 `event_appends` 审计列、插入精确的 `export_outbox` 信封并推进 `head_audit_digest`——与批次原子（故障点证明全有或全无）。
- [ ] 失败测试：精确重试不重算也不复制信封行。
- [ ] 扩展迁移执行器支持代码驱动步骤；迁移 3 创建 `export_leases` 并按提交位置顺序以编解码器 v1 回填每条既有追加，在一个事务内填充审计列、outbox 行与 `head_audit_digest`。
- [ ] 失败测试：回填确定性——预置数据库回填一次；重跑迁移是无操作；预置损坏摘要使打开失败封闭。
- [ ] 失败测试：追加错误代数与回执解析不变；一致性套件零改动仍全绿。
- [ ] 提交 `sqlite: audit chain in append transaction with codec v1 backfill`。

### 任务 3（PR 3）：导出器与重启状态机

**文件：**
- 创建：`exporter.go`、`exporter_test.go`

**步骤：**
- [ ] 失败测试：`ExportOnce` 按提交位置顺序排空 outbox 行，经 staging → sync → close → 重开 → 校验 → 密封分段 → 不可变 manifest 世代 → 事务性检查点更新。
- [ ] 失败测试：1,000 位置或 4 MiB 的分段边界；文件名记录位置区间与摘要。
- [ ] 失败测试：发布幂等——相同区间与摘要即完成；相同区间不同摘要隔离并从规范字节重建。
- [ ] 失败测试：重启清单从每个边界收敛：分段发布后崩溃、manifest 发布后崩溃、检查点前崩溃、staging 中途崩溃；同一头两个冲突有效世代隔离；未命名分段仅作为严丝合缝的下一区间被收养。
- [ ] 失败测试：被修剪的 outbox 行从冻结编解码器重建并精确复现存储摘要。
- [ ] 失败测试：导出器租约（`export_leases`）协调运行且绝不授权领域追加。
- [ ] 提交 `sqlite: crash-convergent audit exporter with restart inventory`。

### 任务 4（PR 4）：一致性导出与导入

**文件：**
- 创建：`auditimport.go`、`auditimport_test.go`

**步骤：**
- [ ] 失败测试：`ExportConsistent(target)` 在读快照中固定位置、发射截至该位置的全部批次并写入自包含 manifest。
- [ ] 失败测试：导入新库校验全部八层并落地可用存储（读取服务导入的流；heads 投影已重建）。
- [ ] 失败测试：八层中每层被违反时以分类错误拒绝并丢弃 staging。
- [ ] 失败测试：导入拒绝非空数据库。
- [ ] 失败测试：分歧策略表作为可执行测试（七行）。
- [ ] 提交 `sqlite: consistent export and eight-step verified import`。

### 任务 5（PR 5）：基准、文档与证据

**步骤：**
- [ ] 基准：导出吞吐、导入吞吐、信封计算的追加开销；记录样本。
- [ ] 发布已实现合同与证据台账（双语）；更新 README 索引与里程碑状态。
- [ ] 最终门：`gofmt`、`go vet ./...`、`go test ./... -count=1`、`-race`、三平台 CGO-free 构建；提交 `sqlite: audit benchmarks, contract, and evidence`。

## 最终完成门

- 一致性套件零改动仍全绿；审计链与每个批次事务化；每个发布边界收敛；导入拒绝每个被违反层；分歧表可执行；基准已记录；合同与证据带可见排除项发布。
