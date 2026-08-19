# Runtime Host 与崩溃恢复实施计划（中文阅读版）

> **给代理工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 按任务逐条实施本计划。步骤使用复选框（`- [ ]`）语法跟踪。

**目标：** 在 Application 服务与 SQLite 存储之上构建唯一 Runtime Host：带确定性恢复追加的崩溃调和、带 fencing 反应的有界心跳循环、优雅关停、第二宿主诊断，以及审计导出器生命周期的归属。

**架构：** 宿主只拥有策略——租约机制、fencing 谓词与 `session_heads` 投影来自 Slice 2；调和读取规范流并通过正常端口追加终态事实。宿主包内没有任何领域规则与存储权威。

**技术栈：** Go 1.26、确定性时间用 `testing/synctest`、标准并发原语、race 工具。

英文版本 [2026-08-16-runtime-host-recovery.md](2026-08-16-runtime-host-recovery.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 全局约束

- 规范：`docs/superpowers/specs/2026-08-16-runtime-host-recovery-design.md`；研究证据：Slice 4 架构门。
- 包 `internal/harness/runtime` 只导入 Application、Domain、SQLite 适配器与标准库；架构依赖测试扩展到它。
- EventStore 端口、错误代数、领域事件与一致性套件不得改变。
- 调和完成前命令不可用；导出延迟绝不阻塞就绪。
- 恢复只追加终态事实，`AppendID` 由固定命名空间（`SessionID`、`TurnID`、`ItemID` 或 `no_item` 哨兵、`process_crash`）确定性推导；丢失确认后的精确重试复用该 `AppendID`。
- 绝不自动重放模型或工具。
- 心跳间隔与截止期有界且可配置；截止期严格短于租约期限。
- 每个行为都是 TDD。每个任务以 `gofmt`、聚焦测试、`go test ./... -count=1`、并发变更时 `-race`、评审与一个小提交收尾。
- 英文为规范；中文计划是同步阅读版并一同提交。
- 任务 4（导出器接线）要求 Slice 3 已合并；任务 1–3 与 Slice 3 无关。

## 文件地图

| 路径 | 职责 |
| --- | --- |
| `internal/harness/runtime/doc.go` | 包范围：唯一宿主，仅策略 |
| `internal/harness/runtime/reconcile.go` | 候选枚举、重放确认、恢复批次构造 |
| `internal/harness/runtime/reconcile_test.go` | 调和矩阵 |
| `internal/harness/runtime/host.go` | 启动顺序、接纳门、就绪、关停 |
| `internal/harness/runtime/heartbeat.go` | 有界续约循环与 fencing 反应 |
| `internal/harness/runtime/heartbeat_test.go` | 确定性时间心跳矩阵 |
| `internal/harness/runtime/host_test.go` | 生命周期、诊断、导出器接线 |
| `internal/harness/architecture/dependencies_test.go` | 新包的导入规则 |
| `docs/architecture/runtime-host.md` | 已实现合同 |
| `docs/architecture/runtime-host-evidence.md` | 提交、验证、排除项 |

---

### 任务 1（PR 1）：调和

**文件：**
- 创建：`doc.go`、`reconcile.go`、`reconcile_test.go`

**步骤：**
- [ ] 失败测试：以活动会话、运行中 Turn 与运行中 Assistant Item 结束的流恰好产生一个恢复批次（带 `process_crash` 的 `assistant.message.interrupted` + `turn.interrupted`），会话保持活动，原 `CommandID` 血统保留。
- [ ] 失败测试：恢复 `AppendID` 是固定命名空间输入的确定性函数；两次调和推导出同一 ID。
- [ ] 失败测试：恢复后重放同一流不再追加（经精确重试语义幂等）；丢失恢复确认解析到原回执。
- [ ] 失败测试：无活动 Item 的遗留运行 Turn 只追加带 `no_item` 哨兵 `AppendID` 的 `turn.interrupted`。
- [ ] 失败测试：活动 Item 引用缺失、终态、不匹配或多个的运行 Turn 返回 `StoreCorrupt` 且不修复。
- [ ] 失败测试：干净流无操作；`session_heads` 中重放确认已不再运行的候选被跳过。
- [ ] 提交 `runtime: crash reconciliation with deterministic recovery appends`。

### 任务 2（PR 2）：宿主骨架

**文件：**
- 创建：`host.go` 及 `host_test.go` 中的测试

**步骤：**
- [ ] 失败测试：启动按父设计顺序执行——打开、迁移、取租约、枚举、调和、就绪；就绪前的命令以稳定分类错误失败。
- [ ] 失败测试：无法获取租约的第二个进程以指名持有者的稳定诊断退出启动；不做调和也不做导出。
- [ ] 提交 `runtime: host startup order, admission gating, and readiness`。

### 任务 3（PR 3）：心跳与 fencing 反应

**文件：**
- 创建：`heartbeat.go`、`heartbeat_test.go`

**步骤：**
- [ ] 确定性时间失败测试：持有时在间隔内续约成功；过期经既有存储谓词 fence 追加。
- [ ] 失败测试：续约失败立即停止接纳、通过 Application 服务请求取消在途工作并停止导出器；不删除任何东西；不尝试接管。
- [ ] 失败测试：静止后的重新获取经正常过期接管路径取下一令牌。
- [ ] 失败测试：心跳配置边界（截止期严格短于租约期限）被校验。
- [ ] 提交 `runtime: bounded heartbeat with fencing reaction`。

### 任务 4（PR 4）：关停、诊断、导出器接线

**文件：**
- 修改：`host.go`；扩展 `host_test.go`

**步骤：**
- [ ] 失败测试：优雅关停停止接纳、有界等待取消在途工作、在分段边界停止导出器并通过使租约过期释放；后继取下一令牌。
- [ ] 失败测试：导出器只在就绪后启动且其延迟绝不阻塞就绪。
- [ ] 扩展架构依赖测试到 `internal/harness/runtime`。
- [ ] 提交 `runtime: graceful shutdown, diagnostics, and exporter wiring`。

### 任务 5（PR 5）：文档与证据

**步骤：**
- [ ] 发布已实现合同与证据台账（双语）；更新 README 索引与里程碑状态。
- [ ] 最终门：`gofmt`、`go vet ./...`、`go test ./... -count=1`、`-race`、三平台 CGO-free 构建；提交 `runtime: contract and evidence`。

## 最终完成门

- 调和矩阵完整且带确定性与幂等；心跳矩阵在 `synctest` 下确定；启动顺序可审计；第二宿主诊断稳定；关停顺序被证明；导出器生命周期有归属；合同与证据带可见排除项发布。
