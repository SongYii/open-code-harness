# EventStore v2 Contract 架构门

**状态：** 调研证据完成

**日期：** 2026-08-13

**范围：** 实现物理 SQLite Adapter 之前的 Contract Migration。

本架构门把已接受的 Runtime Persistence 调研收窄到第一个交付 Slice。它决定数据库实现开始前，Domain、Application、Adapter Contract、Test Double 与 Conformance Test 必须具备哪些保证；不选择 SQL Schema 或 Driver 机制。

## 问题

1. 精确 Append Retry 与普通 Event-ID 去重有什么区别？
2. Client、Application 与 EventStore 分别拥有哪些 Identity？
3. 持续写入时，分页 Reader 如何观察一个不可变 Stream Head？
4. Commit Outcome Unknown 时 Application 应如何处理？
5. 现有 Coding Agent 的哪些做法能作为证据，哪些不足以满足我们的 Runtime Contract？

## 一手资料对比

| 来源 | 已观察合同 | 采用 | 边界 |
| --- | --- | --- | --- |
| [KurrentDB/EventStoreDB Append 文档](https://docs.kurrent.io/clients/tcp/dotnet/21.2/appending) | Expected Revision 提供乐观并发；在同一 Expected Revision 重试相同 Event Identity，可以在不重复 Event 的情况下得到成功确认。禁用并发检查会削弱幂等性。 | 精确 Expected-Version CAS、Append 前稳定 Identity、全有或全无的有序 Batch，以及显式确认语义。 | 只比较 Event ID 不足以满足 Batch Receipt、Admission Side Effect、Request Digest 与提交后 Resolution 要求。 |
| [Temporal History Service](https://github.com/temporalio/temporal/blob/main/docs/architecture/history-service.md) | Workflow History 可以恢复相关状态；Mutable State 与生成的 Task 和状态转换保持事务协调。单调 Range ID 用于隔离 Shard Ownership。 | 不可变语义 History、派生 Serving State、事务注册的 Side Work 与单调 Ownership Epoch。 | 分布式 Shard、Replication、Task Queue 和自动 Workflow/Activity 行为不属于本地 Contract Slice。 |
| [Maka Runtime 与 Resume 架构](https://github.com/maka-agent/maka-agent/blob/main/docs/architecture/runtime-resume-architecture.md) | Recovery 区分 Repair、Continuation 与 Retry，并在外部副作用不确定时避免盲目 Replay。 | Stage-Aware Recovery，以及未知持久化结果绝不授权重复 Model 或 Tool Effect 的规则。 | 公开设计没有定义我们的精确 Go Interface 或 AppendID Receipt 语义。 |
| [OpenAI Codex Live Writer](https://github.com/openai/codex/blob/main/codex-rs/thread-store/src/local/live_writer.rs) | Canonical JSONL 先 Flush，之后才 Materialize SQLite；SQLite 被明确视为可重建数据，可以落后但绝不能领先。 | 让权威与 Projection 顺序显式且可测试。 | 它的 JSONL 权威与 Repair Path 是不同物理设计，不是 EventStore v2 Adapter Contract。 |
| [OpenCode Session Schema](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/session/session.sql.ts)、[Goose SessionManager](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/session/session_manager.rs)、[Crush Session Service](https://github.com/charmbracelet/crush/blob/main/internal/session/session.go)、[Hermes Session 文档](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/sessions.md) | SQLite-backed Coding Agent 普遍把持久 Session/Message Row 作为恢复来源。Goose 还展示 WAL、Bounded Busy Wait 与 `BEGIN IMMEDIATE`；Hermes 明确称 SQLite 为 Canonical。 | SQLite 权威在运维上是常规选择；持久事实必须与瞬态 Bus/UI State 分离。 | 可变 Transcript CRUD 不能证明不可变领域权威、精确 Append Receipt、Lost-ACK Recovery 或 No-Replay 外部副作用安全性。 |

## 结论

### F1. Receipt Identity 必须覆盖完整 Append

一个稳定 `AppendID` 标识一个原子请求。其 Digest 覆盖 Session、Expected Version、Command、可选 Admission Record，以及每个有序 Proposed Event 的 ID、Schema、Occurred At、Type 与 Canonical Payload。用同一 ID 表达任何不同持久副作用都会产生 Identity Mismatch。Event ID 仍然必要，但不能代替 Batch Receipt。

### F2. Unknown Outcome 是一等结果

Commit 前失败可以确定没有提交；尝试 Commit 后失败，在解析 Receipt 前不能转译为缺失。因此 Contract 区分 `StoreUnavailable` 与 `CommitOutcomeUnknown`，并暴露只读 `ResolveAppend`。

### F3. Request Admission 与 Event Append 属于同一事务

`RunTurnRequestID` 由 Caller 稳定提供且全局唯一。其 Versioned Digest、Session、Command、Turn、Item 与 Admission Append 和 Admission Event 一起注册。重复 Request 观察已有 Execution 或 Result，绝不启动第二次 Model Call。

### F4. Pagination 固定逻辑 Head，而不是长期占用 Connection

第一页捕获 `HeadVersion`；后续页重复该值，只返回不晚于它的 Record。调用之间不保留 Read Transaction，并发 Append 不能改变逻辑视图。

### F5. Compact Command State 与 Transcript Query 是不同模型

写侧 Aggregate 只保留 Command Decision 所需的有界状态；历史 Transcript 属于 Projection。历史 Turn/Item 唯一性仍是同步 Integrity Rule，不能因为 Completed Object 离开 Compact Aggregate 而丢失。

## 已采纳架构门决策

1. Slice 1 是明确的 Breaking Contract Migration；不得用 Compatibility Shim 保留含糊的 v1 `Load`/`Append -> []RecordedEvent` 语义。
2. Domain 增加经过验证的 `AppendID`；Application 在首次 I/O 前拥有 `AppendID`、`EventID`、Event Schema、Occurred At 与稳定 Request Identity。
3. Store 只拥有 Stream Sequence 与 Global Commit Position。
4. `ReadStream`、`Append`、`ResolveAppend` 与 `FindCommandRequest` 组成完整 EventStore v2 Interface。
5. 每个 Adapter 与 Test Double 必须通过同一个 Shared Conformance Suite。
6. Application 不得对保留的 Append Intent 重新 Decide，也不得为解决 Persistence Uncertainty 而重复 Model Effect。
7. 只有确定性 Equivalence Test 证明 Compact Form 产生相同 Decision 后，才替换当前 Full-History Aggregate。
8. SQLite Implementation、JSONL Export、Runtime Lease Acquisition、ACP 与 TUI 属于独立 Slice；本 Slice 只表达它们需要的字段，不实现虚假的 Production Behavior。

## 证据限制

- 公开文档说明可观察行为，不能证明未披露保证。
- 未发布的不变量记为 Unknown，而不是被证伪。
- 不复制外部类型名；本项目拥有自己的 Contract 与 Test。
- 链接的 OpenCode `dev` Schema 变化很快，任何实现层复用前都必须重新核验。
