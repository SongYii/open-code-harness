# open-code-harness 代码评审报告

> **评审对象**：`/home/ubuntu/code/open-code-harness` @ `31f37d1`（2026-08-22）
> **评审方法**：三路并行源码深读（适配器层 / 核心循环与状态机 / 工具系统与文档真实性）+ 本机实证验证
> **对照基准**：Codex、DeepSeek Harness、Kimi Code、Grok Build、pi 五个开源 harness 的源码级分析
> **评审日期**：2026-08-22

> **修订记录**
> - v1（2026-08-22）：初版。
> - v2（2026-08-22）：经外部 review 修订——(a) 撤回原 P0-1 "-race 稳定失败" 定性，改为间歇性缺陷并附双方复现数据，待最小化复现后重定级；(b) 原 P0-5 能力空洞移出缺陷清单，并入"能力路线图"章节；(c) 明确 Session 丢弃已完成 turn 为设计意图而非实现违约（`domain/state.go:93` 注释明示）；(d) 修复文档内链失效问题。编号保持 v1 不变以维持讨论引用。
> - v3（2026-08-22）：第三环境独立复核——(a) P0-1 在新环境再次复现（含**单用例独立失败**），合并数据更新至三环境，削弱"纯机器负载"假设；(b) P0-2/P0-3 经第二评审人逐行读码独立证实机制成立；(c) 补充 P1-7 的装配层证据（`composition.Config.Approver` 字段存在但 assembly 从不传播，Service 构造后依赖冻结）。

---

## 一、总体判断

**把"存储正确性"做到了 95 分，把"agent 循环"做到了 40 分。**

优点是真实且突出的：Decide/Apply 纯函数分离、append intent + unknown-outcome 三段式解析、AST 级架构守卫、"model-visible means logged" 式的事件溯源纪律、合同文档与代码的高度吻合。这些在同类项目中属最高档。

但存在两类问题：
1. **已实现部分存在会在生产中引爆的具体缺陷**（P0-2/P0-3/P0-4，均经代码证实）；
2. **防御预算与功能供给严重失衡**——9 相位租约状态机 + 1047 行手写 JSON codec 的复杂度，服务的是一个尚无重试、无压缩、无多轮记忆的 agent 循环（详见"能力路线图"章节）。

## 二、实证结果

| 检查项 | 结果 |
|---|---|
| `go build ./...` | ✅ 通过 |
| `go vet ./...` | ✅ 干净 |
| 全量测试（非 race，15 包） | ✅ 通过 |
| `go test -race ./internal/harness/adapters/sqlite/` | ⚠️ **间歇性失败**（环境 A：5 轮 4 败；环境 B：2 轮 0 败；环境 C：3 轮 2 败，见 P0-1 三环境数据） |

race 失败详情（失败时）：

```
--- FAIL: TestConformance/limits_copies_cancellation_and_corruption (39-43s)
    cases.go:296: rejected over-limit request leaked identities:
    store/writer_fenced (session=session-request-plus-one expected=0 actual=0 ...)
```

非 race 下稳定通过。失败率随环境/负载变化，属竞态窗口型 flaky，而非确定性缺陷。

## 三、P0 缺陷（会真实引爆）

### P0-1【v2 已降级 → P1-flaky】conformance 套件在 -race 下间歇性失败
> **v2 修订**：初版声称"2/2 稳定复现"，经第二环境验证（`-race -count=2` 两轮通过）不成立。
> **v3 补充**：第三环境独立复现——`-race -count=2` 全套 **FAIL**、单用例 `-run 'TestConformance/limits' -count=1` **FAIL**、全套 `-count=1` PASS。三环境合并：A 5 轮 4 败、B 2 轮 0 败、C 3 轮 2 败，**合计 10 轮 6 败**。单用例独立失败表明该窗口不纯依赖机器负载；**缺陷真实存在但非确定性**，维持 P1-flaky 定级，待最小化复现后重定级。

超限请求在竞态时序下走了 `store/writer_fenced` 拒绝路径，触发"被拒请求不得泄漏身份"不变量断言失败（`cases.go:296`）。说明**限流拒绝与 fencing 判定之间存在顺序/时序依赖**，直接违背本项目 "deterministic verification" 的立身承诺。
**建议**：先最小化复现（单用例即可失败，可从 `-run 'TestConformance/limits' -race -count=N` 起步），定位 over-limit 检查与 lease 校验的判定顺序；根因修复前不建议 `t.Skip` 掩盖——这是对确定性验证承诺的违反，应保持 CI 可见（如允许失败的独立 job + 告警）。

### P0-2 恢复 append 的 "exact retry" 被墙钟破坏，可永久楔死启动
- digest 覆盖每个事件的 `OccurredAt`：`application/digest.go:48`
- 恢复事件时间戳每次现取：`runtime/reconcile.go:86`
- 场景：恢复 append 已 COMMIT 但回执丢失（`StoreCodeCommitOutcomeUnknown` 正为此存在）→ 重启后同一 `recoveryAppendID` 因 digest 不同被判 `AppendIdentityMismatch`（`sqlite/append.go:264-266`）→ Launch 失败（`runtime/host.go:115`），且每次重启重复失败。

注释声称 "A lost recovery acknowledgement retries the exact same append"（`reconcile.go:21-26`），实现不成立。
> **v3 交叉验证**：第二评审人独立读码证实同一机制链（`reconcile.go:86` 现取 `r.now().UTC()`、digest 覆盖 `OccurredAt`、同 `AppendID` 载荷不一致即 mismatch）。
**建议**：恢复事件改用确定性时间戳（从持久化的 intent 重放），而非 `r.now()`。

### P0-3 lease token 轮转后 Authority 快照脱节，心跳自愈形同虚设
- 租约过期接管递增 fencing token：`sqlite/lease.go:61`
- composition 装配时一次性快照 authority 注入 Service：`composition/assembly.go:146`
- 心跳失联自愈后清除 lostLease、恢复准入：`heartbeat.go:70-78`

token 轮转后 Service 持有的旧 authority 与 `verifyLeaseForAppend`（`lease.go:129`）永不匹配 → 所有业务 append 返回 `WriterFenced`，无机制推送新 authority，只能整体重启。fencing reaction 对真实写入路径无效。
> **v3 交叉验证**：第二评审人独立证实——`Store.Authority()` 返回 open 时快照（`lease.go:107`），`NewService` 将其冻结为不可变字段（`service.go:74,116`），心跳重获租约（`heartbeat.go:51-56`）后无任何路径更新 Service 持有的 token。
**建议**：Service 改为 authority provider（动态获取）或 leaseRegained 时主动通知 Service 更新。

### P0-4 文件系统监狱的 TOCTOU 击穿链，与 SECURITY.md 声明矛盾
- jail 流程：EvalSymlinks 解析出字符串 → 之后按字符串重新 open，两步间有窗口：`adapters/workspacefs/fs.go:75-130,210-222`
- localexec 仅在超时/取消时杀进程组，仅 Setpgid：`localexec/runner.go:102-106`、`proc_unix.go:8`

攻击链：一次被批准的 exec 用 `setsid` 把后台进程留在进程组外 → 在窗口期 symlink swap → `read_file`/`write_file` 越界。SECURITY.md 分别承认 jail 与 exec 两个单点的局限，但**组合链不在任何清单里**，而 jail 被标为 Enforced（SECURITY.md:31-33）。
**建议**：openat2/O_NOFOLLOW 语义或对已解析 fd 操作；短期至少将该场景写入 Not enforced 清单。

### P0-5【v2 已移出缺陷清单 → 见"能力路线图"】
> **v2 修订**：重试/压缩/多轮记忆属能力缺口而非正确性或安全缺陷，不应与 P0 混列。且 Session 丢弃已完成 turn 在 `domain/state.go:93` 注释中为明示的设计决策，非文档违约。内容并入第八章"能力路线图"。

## 四、P1 缺陷（高优先级）

| # | 问题 | 证据 |
|---|---|---|
| 1 | exporter 用 Go 墙钟而主 lease 用 SQLite 单调时钟（时钟域不一致）；30s 租约硬编码不续期，第二个 exporter 可中途接管；staging 文件名固定，跨进程并发写无互斥 | `exporter.go:550-566,272` |
| 2 | `chainDigestAt` 吞掉所有错误返回 genesis digest——fail-open 到合法值比 fail-closed 危险得多；瞬态读失败可伪造 manifest head 并据此错误 prune outbox | `exporter.go:317-327,476,518` |
| 3 | `host.workCancel()` 无锁读 vs heartbeat 持锁写，数据竞争 | `host.go:196` vs `heartbeat.go:77` |
| 4 | import 第八步校验在 COMMIT 之后执行，失败则目标库永久不可重试（撞 "destination is not empty"） | `auditimport.go:288-294,203` |
| 5 | Backup 存在校验与 VACUUM INTO 无共同快照，并发 append 下误报 CorruptError；副本文件未 fsync | `backup.go:29-33,83-85` |
| 6 | `write_file` 用 O_TRUNC 直接覆写：中途出错留半文件、无 fsync、跨 session 并发无互斥——违反自家 "不接受 silent partial writes" 标准 | `fs.go:121` |
| 7 | 审批同步阻塞 30s 即拒、崩溃不留存、无 per-tool/per-path 规则、无 "本会话总是允许" 类记忆、策略零组合子；且装配断线——`composition.Config.Approver` 字段存在（`config.go:41`）但 assembly 只接线 `Commands`（`assembly.go:130`），所有组装实际落到 `DenyApprover` 兜底，Service 构造后依赖冻结（`service.go:74`），外部审批面（如 ACP 每请求审批）无法注入 | `pipeline.go:142-203`、`service.go:20,74`、`config.go:41`、`assembly.go:130`、`policy/engine.go:20-25` |
| 8 | steering 中断注入完全缺失（业界标配）；流式 text delta 不落盘，崩溃后 replay 丢失半截回复 | events 仅有 started/completed（`events.go:16-17`） |
| 9 | stop reason 处理粗糙：FinishReason 由"是否有 toolCalls"反推而非用 provider 真实值；length 截断无差异化处理；终态信号 emit 不对称 | `runner.go:169-171`、`turn.go:431-436` vs `loop.go:380-398` |
| 10 | Tool Runtime 证据台账引用的 SHA（eeb2e02 等）在 git 对象库中不存在——疑似 squash 合并摧毁被引对象；"auditable commits" 各台账执行不一致 | `tool-runtime-evidence.md:32-40` |
| 11 | 后台 exporter 完全吞错（`_, _ = ExportOnce`），审计副本停摆无人知晓；全仓无日志/metrics 埋点 | `heartbeat.go:95`、`cmd/och/main.go:24` |
| 12 | exec 无 RLIMIT_CPU/AS/NPROC/FD 资源配额，仅墙钟+输出上限；PATH 继承宿主 | `localexec/service.go:21-24`、`runner.go:79` |

## 五、P2 缺陷（择要）

- schema 每次工具调用重编译；`compileSchemaObject` 递归无深度上限（接入 MCP 外部工具即成栈耗尽入口）：`tools/schema.go:43,82-120`
- List 吞目录读取错误；depth=2 先全量收集再截断，超大目录内存峰值无界：`fs.go:153-165`
- SSE 细节：裸 `data:` 行被丢弃（SSE 规范合法）、tool call delta 强制要求 index、nativeTools 下文本缓冲无上限、usage 只保留最后 chunk：`openaicompat/stream.go:174-176,306-309,374,405-407`
- 双准入路径冗余：非原子 StartTurn/CompleteTurn 命令族与原子路径并存，无消费者：`decide.go:286-300`
- 工具执行硬编码 switch 四个内置名，`ToolSpec.Source` 有位无实：`pipeline.go:232-283`
- 架构守卫盲区：模块路径硬编码常量、跳过 `_test.go`、拦不住接口注入式隐式耦合：`dependencies_test.go:266,328,706-721`
- 镜像型脆测：`reflect.NumField > 6` 断言 Session 结构体字段数：`compact_test.go:18-20`
- memory store 每次 append 全量深拷贝整个状态，仅可作参考实现：`event_store.go:199`

## 六、做得好的部分（应保持）

1. **Policy Decide 表完备且 fail-closed**：4 Mode × 3 Risk 全枚举、未知风险前置 deny、switch 兜底 deny；`AllowAll` 被 AST 守卫限定为测试构造器——全仓库最扎实的部分。
2. **架构守卫质量高于业界平均**：AST 导入边界检查、unowned 包默认拒绝 adapters/testkit、白名单穷尽性测试。
3. **append 单事务、receipt 十字校验、pinned read 快照、WAL/FULL sync、migration 单事务+版本门**：存储核心正确性扎实。
4. **测试整体是真行为测试**：崩溃恢复契约测试（unknown outcome 不重复执行）、historical oracle 对照、eventstoretest 契约套件可供多实现复用。
5. **文档诚实度高**："Not enforced" 安全清单、CI 实际跑的内容超出 README 声称、端到端 "no network no credential" 属实。文档可信度约 8/10（扣分项见 P1-10 与 P0-4）。

## 七、对照五大 harness 的能力差距

| 参照 | 本项目缺口 |
|---|---|
| Codex | token 计量→压缩触发闭环；审批沉淀为规则（ExecpolicyAmendment 式） |
| Kimi | 结构性错误恢复阶梯（分类→退避→降级→带失败上下文续跑）；压缩竞态防御 |
| DeepSeek | waterfall 式扩展缝（工具注册/MCP 插件点）；子代理 |
| Grok | 外部协议面（ACP）；TUI 级端到端测试基建 |
| pi | 方向相反的教训：防御复杂度远超功能供给 |

## 八、能力路线图（v2 自缺陷清单拆出）

以下为能力缺口与业界对照，**不属于缺陷**；其中 Session 丢弃已完成 turn 是 `domain/state.go:93` 明示的设计决策。

1. **零重试语义**：`engine.Error.Retryable/RetryAfter` 已定义（`engine/errors.go:24-25`）但全仓库无人消费；任何瞬时错误直接 terminalize 整个 turn（`application/loop.go:166-171`, `turn.go:396-438`），失败后亦无 resume 入口。
2. **无 token 计量回路、无 compaction**：`ModelUsageRecorded` 记录了 inputTokens/outputTokens 但从不反馈进决策；上下文控制仅有静态 4MB 封包上限，超限即死（`loop.go:149-151,295-304`）。长任务必然死于步数（默认 MaxSteps=8）或字节上限。
3. **跨 turn 上下文为空**：每次 turn 投影从 `{role:user}` 单条开始（`loop.go:46-52`）。作为设计意图成立，但产品化前需要 conversation projection 端口承载多轮语义。

对照五大 harness：

| 参照 | 本项目缺口 |
|---|---|
| Codex | token 计量→压缩触发闭环；审批沉淀为规则（ExecpolicyAmendment 式） |
| Kimi | 结构性错误恢复阶梯（分类→退避→降级→带失败上下文续跑）；压缩竞态防御 |
| DeepSeek | waterfall 式扩展缝（工具注册/MCP 插件点）；子代理 |
| Grok | 外部协议面（ACP）；TUI 级端到端测试基建 |
| pi | 方向相反的教训：防御复杂度远超功能供给 |

## 九、建议处理顺序（v2 修订）

1. **立即**（已证实的正确性/安全缺陷）：
   - P0-2 恢复 append 时间戳确定性
   - P0-3 authority 动态化接线
   - P0-4 安全清单诚实化或 jail 加固（openat2/fd 语义）
   - P1-3 workCancel 数据竞争、P1-2 chainDigestAt fail-open
2. **本里程碑**：
   - 原 P0-1 race flake 最小化复现 + 根因修复
   - P1 其余项按依赖排序
3. **下一里程碑**（能力路线图，非缺陷）：
   - 错误恢复阶梯（Retryable 字段已有，只差消费者）
   - token-aware compaction 接口、异步审批、steering 注入
   - 多轮会话投影（conversation projection 端口）
4. **持续改进**：
   - exporter 统一时钟域 + 结构化日志/metrics
   - evidence ledger 提交可达性治理（避免 squash 证据提交）
   - schema 编译缓存 + 递归深度上限

---
*v3 说明：本报告所有论断均附 `file:line` 证据，可在本仓库评审分支直接核对；P0-2/P0-3 已由两位评审人独立读码证实。P0-1 为间歇性失败，尚无单条命令的确定性复现，但环境 C 的单用例独立失败（`go test -race -run 'TestConformance/limits' -count=1 ./internal/harness/adapters/sqlite/`）是最接近的起点；三环境复现数据见该条目及第二章实证结果。*
