# SQLite 规范 EventStore 实施计划（中文阅读版）

> **给代理工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 按任务逐条实施本计划。步骤使用复选框（`- [ ]`）语法跟踪。

**目标：** 实现第一个持久化规范 EventStore——一个位于不变的 `application.EventStore` 端口之后的纯 Go SQLite 适配器：完整形态的版本化迁移、带精确重试与 fencing 的单个 `BEGIN IMMEDIATE` 追加事务、钉住头的分页读取、经过校验的备份，以及 COMMIT 处的结果码故障证据。

**架构：** 端口、错误代数与一致性套件由 EventStore v2 合同冻结；本计划只增加一个适配器。一条专用写者连接串行化所有变更；读取使用有界池与显式读事务；SQLite 是唯一提交权威。后续切片的表存在于模式中，但没有任何 Slice 2 代码路径维护它们。

**技术栈：** Go 1.26、`database/sql`、`modernc.org/sqlite`（第一个外部依赖）、`testing`、在确定性时间有帮助处使用 `testing/synctest`、race/benchmark 工具、GitHub Actions。

## 全局约束

- 规范：`docs/superpowers/specs/2026-08-16-sqlite-canonical-eventstore-design.md`；第 4–13 节为强制。研究证据：`docs/research/architecture-gates/2026-08-16-sqlite-canonical-eventstore.md`。
- 仅 Slice 2：没有审计信封或摘要链、没有 outbox 维护、没有心跳调度器/接管策略/崩溃调和、没有转录/快照/上下文投影、没有 JSONL、没有 ACP、没有 TUI。
- `application.EventStore` 端口、`StoreError` 代数与 `eventstoretest` 套件不得改变。适配器以 harness 工厂通过 `eventstoretest.Run`；harness 辅助只能通过适配器自身的连接操作租约行。
- `internal/harness/adapters/sqlite` 只导入 `application`、`domain`、驱动与标准库；架构依赖测试扩展到新包。
- 存储只分配每会话序号与全局提交位置。Application 拥有的请求身份不被触碰。
- 限制与内存适配器一致：每规范 Event 负载 8 MiB、每追加 64 个事件、每编码追加请求 16 MiB、每读取页 256 条记录。规范事实被拒绝，绝不截断。
- 没有无界隐藏重试。繁忙等待受配置与调用方 context 约束；未知结果协议在新连接上执行恰好一次有界回执查找。
- 模式一次性创建到完整目标形态。编码契约的唯一性位于 DDL：`(session_id, sequence)` 唯一、`event_id` 全局唯一、`append_id` 唯一、`commit_position` 唯一且连续、`run_turn_request_id` 全局唯一、`(session_id, identity_kind, identity_id)` 唯一。
- 审计链列从迁移 1 起存在并保持零值；任何东西都不得创建阻塞 Slice 3 单写者回填的形态。
- `synchronous=FULL`；回执仅在 COMMIT 持久化之后返回。打开后 `journal_mode` 必须实际为 `wal`，否则失败封闭并诊断。
- 租约时间权威是 SQLite 的 `unixepoch('subsec')`；启动测试断言捆绑的 SQLite 版本支持它。
- 每个行为都是 TDD：先观察到预期失败，再实现，然后运行聚焦与全量测试。
- 每个任务以 `gofmt`、聚焦测试、`go test ./... -count=1`、任务改变并发时的 `go test -race ./... -count=1`、独立评审门与一个小提交收尾。
- 英文为规范。中文计划是完整同步的阅读版并一同提交。

## 文件地图

| 路径 | 职责 |
| --- | --- |
| `internal/harness/adapters/sqlite/doc.go` | 包范围与运行画像摘要 |
| `internal/harness/adapters/sqlite/config.go` | 有界配置：池大小、busy 超时、checkpoint 策略、拒绝列表前缀 |
| `internal/harness/adapters/sqlite/open.go` | Open：驱动注册、pragma、画像校验、租约获取、损坏门 |
| `internal/harness/adapters/sqlite/migrations.go` | 有序版本化迁移、迁移历史、格式版本门 |
| `internal/harness/adapters/sqlite/migrations_sql.go` | 创建完整目标形态的迁移 1 DDL |
| `internal/harness/adapters/sqlite/append.go` | 按规范第 7 节的追加事务 |
| `internal/harness/adapters/sqlite/validate.go` | 追加共享的请求限制、ID、模式版本与规范性检查 |
| `internal/harness/adapters/sqlite/read.go` | 读事务内的 ReadStream 钉住分页 |
| `internal/harness/adapters/sqlite/lookup.go` | ResolveAppend 与 FindCommandRequest |
| `internal/harness/adapters/sqlite/lease.go` | runtime_leases 获取、续约与每次追加的所有权谓词 |
| `internal/harness/adapters/sqlite/errors.go` | SQLite 结果码分类与 StoreError 映射 |
| `internal/harness/adapters/sqlite/fault.go` | 提交边界处测试可见的故障注入（生产为 nil） |
| `internal/harness/adapters/sqlite/backup.go` | 经过校验的一致备份副本 |
| `internal/harness/adapters/sqlite/rebuild.go` | 离线 session_heads 重建与校验 |
| `internal/harness/adapters/sqlite/*_test.go` | 适配器测试与 `eventstoretest.Run` 接线 |
| `internal/harness/architecture/dependencies_test.go` | 导入规则扩展到新包 |
| `docs/architecture/sqlite-eventstore.md` | 已实现合同 |
| `docs/architecture/sqlite-eventstore-evidence.md` | 提交、验证输出、依赖清单、基准基线、排除项 |

---

### 任务 1（PR 1）：驱动、打开画像与完整形态迁移

**文件：**
- 修改：`go.mod`、`go.sum`
- 创建：`internal/harness/adapters/sqlite/doc.go`、`config.go`、`open.go`、`migrations.go`、`migrations_sql.go`
- 创建：`internal/harness/adapters/sqlite/open_test.go`、`migrations_test.go`
- 修改：`internal/harness/architecture/dependencies_test.go`

**步骤：**
- [ ] 加入固定版本的 `modernc.org/sqlite`；记录 `go version -m` 与传递许可证集合供证据台账。确认 `CGO_ENABLED=0 go build ./...` 仍通过。
- [ ] 失败测试：打开数据库应用并回报 `journal_mode=wal`、`synchronous=FULL`、`foreign_keys=ON` 与配置的 busy 超时，通过池读回而不是盲目断言。
- [ ] 实现 `Open(config)`：解析符号链接、应用可配置拒绝列表前缀检查作为诊断、打开数据库、应用 pragma 并校验 `journal_mode` 实际为 `wal`；任何不匹配失败封闭为 store_unavailable 并指名位置。
- [ ] 失败测试（单元）：日志模式校验拒绝 `delete`、`truncate`、`memory`、`off`；只接受 `wal`。
- [ ] 失败测试：打开 `user_version`/`store_metadata.format_version` 较新的数据库时以指向升级的消息拒绝，绝不报告损坏；较旧则向前迁移；相等为无操作。
- [ ] 实现迁移执行器：`schema_migrations` 历史、有序步骤、单写者 `BEGIN IMMEDIATE`、`user_version` 与 `store_metadata` 在最后一步的同一事务内更新。
- [ ] 失败测试：迁移 1 创建规范第 6 节的每张表与每条唯一性约束，且 `store_metadata` 是单例（插入第二行失败）。
- [ ] 失败测试：篡改的元数据（不可能的格式版本、缺失单例）使打开以 store_corrupt 失败并拒绝所有变更路径。
- [ ] 失败测试：`SELECT sqlite_version()` 满足 `unixepoch('subsec')` 的最低要求；低于它打开失败封闭。
- [ ] 扩展架构依赖测试到新包。
- [ ] gofmt、聚焦测试、`go test ./... -count=1`、此处不需要 race、评审、提交 `sqlite: driver, open profile, and full-shape migrations`。

### 任务 2（PR 2）：追加事务

**文件：**
- 创建：`internal/harness/adapters/sqlite/append.go`、`validate.go`、`errors.go`
- 创建：`internal/harness/adapters/sqlite/append_test.go`

**步骤：**
- [ ] 失败测试：一次 Append 提交原子批次——回执携带 `commit_position=1` 与连续序号；回读精确保留提议的 `EventID`、模式版本、UTC `occurred_at` 与规范负载字节。
- [ ] 在专用写者连接上实现规范第 7 节事务：先回执解析、再租约谓词（Task 4 前以仍读 `runtime_leases` 的临时宽容谓词通过）、版本 CAS、校验、身份预留、位置递增、序号分配、准许插入、回执插入、事件批次插入、`event_streams` upsert、`session_heads` upsert、COMMIT。
- [ ] 失败测试：精确重试——流前进后同一 `AppendID` 与摘要返回原回执；不同摘要返回 append_identity_mismatch；任何表都没有重复行。
- [ ] 失败测试：`ExpectedVersion` 不匹配返回携带实际版本的 version_conflict，且该追加在任何表都不留痕迹（全索引回滚断言）。
- [ ] 失败测试：准许——`command_requests` 插入、同一身份的 command_request_conflict、Session/摘要不匹配的 command_identity_mismatch 且不泄露其他 Session 的记录。
- [ ] 失败测试：重复的创建 Turn/Item 身份返回携带身份种类的 domain_identity_conflict 并回滚整个批次。
- [ ] 失败测试：限制——负载超 8 MiB、超 64 事件、请求超 16 MiB 各返回 invalid_append；什么都不提交。
- [ ] 实现结果码分类：有界内的 busy/locked 到 store_unavailable 并保留码为 cause；约束与完整性失败到各自代数码；不变量违规到 store_corrupt。对文档化结果码全表单元测试分类。
- [ ] gofmt、聚焦测试、`go test ./... -count=1`、评审、提交 `sqlite: append transaction with exact retry, CAS, admission, and identities`。

### 任务 3（PR 3）：钉住读取、解析与命令查找

**文件：**
- 创建：`internal/harness/adapters/sqlite/read.go`、`lookup.go`
- 创建：`internal/harness/adapters/sqlite/read_test.go`

**步骤：**
- [ ] 失败测试：`ReadStream` 以与内存适配器一致的独占 `AfterSequence`、`Limit` 与 `End`/`NextAfterSequence` 语义分页，钉住在单个 `HeadVersion` 快照。
- [ ] 在有界池上以显式读事务实现读取，使钉住的分页序列观察单个 WAL 快照；无法被一致服务的钉住头返回 invalid_read，绝不静默空页。
- [ ] 失败测试：`ResolveAppend` 只读并按摘要返回 committed/not_found/identity_mismatch；`FindCommandRequest` 比较 Session 与摘要并返回 identity_mismatch 而不泄露其他 Session 的记录。
- [ ] 失败测试：写者事务期间的并发读者只观察完整批次（无半可见追加）且绝不在超出 busy 超时的情况下阻塞。
- [ ] gofmt、聚焦测试、`go test ./... -count=1`、`go test -race ./... -count=1`、评审、提交 `sqlite: pinned pagination, append resolution, and command request lookup`。

### 任务 4（PR 4）：fencing、未知结果与一致性

**文件：**
- 创建：`internal/harness/adapters/sqlite/lease.go`、`fault.go`
- 创建：`internal/harness/adapters/sqlite/lease_test.go`、`fault_test.go`、`conformance_test.go`
- 修改：`internal/harness/adapters/sqlite/append.go`（替换宽容谓词）

**步骤：**
- [ ] 失败测试：open 获取单例租约（`BEGIN IMMEDIATE`，缺失/过期以新令牌取得，活动的外来租约被拒绝）；续约只用 `unixepoch('subsec')` 延长期限并更新心跳。
- [ ] 以真实谓词替换宽容追加谓词；失败测试：过期或错误令牌返回 writer_fenced；租约过期拒绝追加直到重新获取。
- [ ] 实现测试 harness：`RotateAuthority` 通过适配器自身的连接过期并重取租约；`FailNext` 在 before_commit、after_commit_before_ack 与 resolve；`CorruptReceipt` 通过写者连接破坏存储摘要。
- [ ] 失败测试：after_commit_before_ack 处故障返回 commit_outcome_unknown；有界新连接查找把摘要匹配解析为原回执；缺失或查找不可用返回 commit_outcome_unknown——绝不是确定性未提交。
- [ ] 失败测试：COMMIT 处结果码——两条写者连接之间的真实繁忙争用有界映射为 store_unavailable；提交中途的 context 中断运行同一单查找协议；注入的 full/IO 错误通过与分类单元测试相同的路径分类。
- [ ] 失败测试：突然终止后重开——在 WAL 活动中途不清洁关停地关闭所有句柄、重开、校验不变量并观察无半可见批次。
- [ ] 失败测试：不同会话上的并行追加者通过单写者连接串行化，全局 `commit_position` 连续且无空洞。
- [ ] 接线 `eventstoretest.Run(t, factory)`；全套一致性测试以套件零改动通过。
- [ ] gofmt、聚焦测试、`go test ./... -count=1`、`go test -race ./... -count=1`、评审、提交 `sqlite: fencing lease, unknown-outcome protocol, and conformance`。

### 任务 5（PR 5）：备份、重建、基准与证据

**文件：**
- 创建：`internal/harness/adapters/sqlite/backup.go`、`rebuild.go`、`backup_test.go`、`rebuild_test.go`、`benchmark_test.go`
- 创建：`docs/architecture/sqlite-eventstore.md`、`docs/architecture/sqlite-eventstore-evidence.md` 及两份 zh-CN 阅读版
- 修改：`docs/README.md`

**步骤：**
- [ ] 校验纯 Go 驱动的备份设施；用它实现一致副本，设施不可用时用 `VACUUM INTO`——证据台账记录实际发布的机制与原因。
- [ ] 失败测试：备份到目标后打开副本，先校验模式版本与核心不变量（行数、连续性、唯一性）再报告成功；受损的源使备份失败。
- [ ] 失败测试：从规范流重建 `session_heads` 复现已维护投影；注入的不匹配被报告为损坏。
- [ ] 基准：追加吞吐与延迟分布、分页读取吞吐、备份时长；在证据台账记录样本。
- [ ] 记录依赖与许可证清单（`go mod graph`、`go version -m`）。
- [ ] 发布带显式排除项（审计链未激活、宿主生命周期缺失、额外投影未维护）的已实现合同与证据台账；更新 README 索引与里程碑状态。
- [ ] 最终门：`gofmt`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./... -count=1`、linux/darwin/windows 的 `CGO_ENABLED=0` 构建、评审、提交 `sqlite: backup, projection rebuild, benchmarks, and evidence`。

## 最终完成门

- 规范第 15 节：套件零改动通过一致性、第 13 节每个测试类有证据、三平台 CGO-free 门、依赖清单已记录、合同与台账带可见排除项发布。
- 没有残留 v1 或临时命名；通过一致性不需要任何端口、套件或 Domain 改动。
