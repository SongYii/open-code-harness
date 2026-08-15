# 生产 Runtime 持久化、恢复与客户端边界

**状态：** 已接受设计
**日期：** 2026-08-13
**权威级别：** 完整同步阅读副本
**规范权威：** `2026-08-13-runtime-persistence-recovery-client-design.md`

## 1. 决策摘要

Open Code Harness 采用 **SQLite-backed 规范 Event Log + Transactional Export Outbox + 可验证 JSONL 审计副本**。

- SQLite 中的不可变领域事件是唯一在线提交权威。
- 可变 Read Model、Session Head 和 Snapshot 都可重建。
- JSONL 无损、可读、可移植；只有完整导出通过 Manifest 验证时，才能用于重建新数据库。它不是第二个在线权威。
- 每次原子 Append 都有调用方稳定的 `AppendID`、精确 Expected-Version CAS、规范 Request Digest 和持久回执。
- 一个 Runtime Host 通过可续约 Lease 与单调递增的 Fencing Token 拥有数据库。
- 启动恢复把遗留的 Running Assistant Item/Turn Pair 原子终止为 `process_crash`，绝不自动重复 Model 或 Tool 副作用。
- Core 继续是纯 Go、无 CGO、跨平台单二进制。
- TypeScript TUI 是独立 ACP v1 Client。ACP 是外层 Adapter，不是内部领域模型。
- 生产 Stream Read 分页；紧凑写侧 Aggregate 和 Transcript Projection 取代无界完整历史加载。

已接受的比较证据记录在 `docs/research/architecture-gates/2026-08-13-runtime-persistence-recovery-client.zh-CN.md`。

## 2. 背景

已实现的 Engine 纵切已经建立纯 Domain 决策与 Replay、Session/Turn/Assistant Message Item 状态机、Application 持有的 Command 与 Durability 边界、原子 Admission/Terminal Batch、内存 EventStore 的 Expected-Version 行为、一次有界 Model Call、显式 Terminal 语义以及确定性 Contract Suite。

该纵切明确不提供生产 Persistence、Crash Recovery、ACP 或 TUI。当前 EventStore 还规定任何非空 Append Error 都代表未提交，且 `Load` 返回完整 Stream。这些假设对于已完成的内存里程碑有效，但对于生产数据库过强或无界。本文定义它们经过审慎设计的 v2 演进。

## 3. 目标

1. 提供本地生产持久化，并明确 Atomicity、Concurrency、Idempotency、Corruption、Backup 和 Recovery 合同。
2. 保持 Event Sourcing：领域状态来自不可变事实，而不是可变状态表。
3. 让每个失败边界都可以通过 Contract Test、Fault Injection、Replay Fixture 和跨平台测试复现。
4. 在不实现第二个文件事务权威的前提下，让 Session 证据可检查、可移植。
5. 防止 Split-Brain Runtime Host 与崩溃后恢复的旧 Writer。
6. 暴露适用于本项目 TUI 和第三方 IDE 的标准 ACP v1 客户端边界。
7. 保持 Go Core 与 TypeScript Client 独立发布。
8. 架构、计划、合同、调研与评测证据均保留完整同步中文文档。

## 4. 非目标

- 分布式或多节点 EventStore。
- 将活动数据库放在 NFS、SMB、云同步或网络文件系统。
- 把 JSONL 作为并发在线写入权威，或自动合并进活动数据库。
- 崩溃结果不确定时自动重试 Model Call 或外部 Tool。
- 把远程 ACP Transport 作为稳定 v0 承诺。
- A2A、云端控制平面、多租户、计费或远程 Agent Cluster。
- 在 Go Binary 内嵌 Node、Bun 或 TypeScript Runtime。
- 强制 Go Core 与 TypeScript TUI 打包为同一个物理制品。
- 通用插件 ABI 或 Go Dynamic Plugin。
- Context、Provider、Tool、Policy 或 MCP 的完整实现；它们继续拥有独立设计门。

## 5. 架构不变量

1. **唯一提交权威：** 当且仅当 SQLite Append Transaction 提交时，在线领域事实才存在。
2. **唯一执行权威：** 一个数据库最多有一个未过期且通过 Fencing 的 Runtime Host 可以 Append。
3. **精确重试：** 一个 `AppendID` 命名一个不可变请求；使用不同字节重用它必须报错。
4. **原子生命周期：** Terminal Assistant Item 与其 Turn 在同一 Append Batch 中终止。
5. **事实先于交付：** ACP 或其他 Runtime Terminal Notification 之前先提交持久 Terminal Fact。
6. **投影可丢弃：** 删除 Projection、Snapshot、Export Checkpoint 或 Staging File 不能删除规范事实。
7. **不盲目重复副作用：** 不确定的 Model/Tool 结果必须显式表达，绝不静默重试。
8. **资源有界：** 每个 Stream、Batch、Payload、Queue、Transaction、Shutdown 和外部副作用都有上限或 Deadline。
9. **Fail Closed：** 未知 Schema、Event Invariant 破坏、Digest 不一致或 Ownership 未解决时停止修改。
10. **协议隔离：** ACP、SQLite、Provider、Tool 和 TUI 类型永远不进入 Domain Package。

## 6. 系统形态

```text
Clients: TypeScript TUI · IDE · other ACP v1 clients
                  │ JSON-RPC 2.0 / stdio in v0
ACP Adapter: schema · capability · validation · projection · backpressure
Runtime Host: composition · lease/fencing · recovery · lifecycle
Application: commands · orchestration · append identity · transactions
Domain: commands · events · compact aggregate · replay invariants
Engine / Provider / Tool / SQLite / Audit adapters
Evaluation: contract · replay · fault injection · observability
```

依赖方向向内。Composition Code 可以导入所需 Adapter；Domain 不导入任何 Adapter。

## 7. 规范存储模型

### 7.1 `store_metadata`

Singleton Row 包含 Storage Format Version、当前全局 `head_commit_position`、供下一 Outbox Envelope 使用的 `head_audit_digest`、数据库创建和最后 Migration 元数据。Append Transaction 自身递增 Position；回滚不消耗 Position，因此已提交 Batch 具有连续全局顺序。`head_audit_digest` 提供新 Envelope 的 `previousDigest`，并在同一事务中替换为新 Envelope Digest。

SQLite 在写事务中串行化 Metadata Row 更新，因此即使两个 Append 属于不同 Session Stream，也能获得唯一确定的全局顺序。

### 7.2 `event_streams`

每个 Session 一行，保存 `session_id`、当前 Stream `version`、创建和最后 Append Commit Position。Version 是最后提交的 Stream Event Sequence，是 CAS Head，不是领域状态。

### 7.3 `event_appends`

每个原子 Batch 一行，保存唯一 `append_id`、唯一 `commit_position`、`session_id`、`expected_version`、首尾 Sequence、Event Count、`command_id`、`request_digest`、`writer_fencing_token`、Audit Format Version、Previous/Batch Audit Digest 与 `committed_at`。该行同时是 Batch Header 和持久 Idempotency Receipt。

### 7.4 `events`

每个不可变 Event 保存联合唯一的 Session/Sequence、全局唯一 `event_id`、所属 `append_id` 与 Batch 顺序、`command_id`、Event Type、Event Schema Version、UTC `occurred_at`、规范 JSON Payload 与 SHA-256 Digest。

Store 分配 Stream Sequence 与全局 Commit Position。Application 在首次调用前持有 `AppendID`、`CommandID`、`EventID`、Schema、Occurred-At 与 Payload，使请求在重试期间完全稳定。

### 7.5 `export_outbox`

每个已提交 Append 一行，保存 Commit Position、Append ID、Audit Format Version、规范 Batch Envelope Byte、Envelope Digest 和诊断状态。它与事件在同一事务写入。Pending 期间保存精确 Envelope，避免 Exporter 重新编码活动 Append 时产生漂移。Envelope 在全局 Append Transaction 内按 Commit Position 构造 Hash Chain，而不是由异步 Exporter 决定顺序。

Sealed Segment 与 Manifest 验证完成且 SQLite Checkpoint 已提交后，可以清理精确 Outbox Envelope。永久保留的 Append Row、Event Byte、Format Version 与 Expected Digest 足以用冻结的旧版 Codec 重建；重建结果必须精确匹配保存的 Digest，否则 Fail Closed。Audit Codec Registry 随 Binary 版本化，`event_appends.audit_format_version` 是唯一选择键；任何已提交 Format 的 Codec 都不能从受支持 Upgrade Path 中删除。缺失 Codec 返回 `StoreCorrupt`，Export/Import Fail Closed，并由永久 Round-Trip Fixture 覆盖。这样无需永久保留每个 Payload 的第二份完整副本。

### 7.6 Admission 与历史 Identity Index

该持久 Identity Table 只在 Command Admission 写入，不是 Projection。它保存 `RunTurnRequestID`、Versioned Request Digest、Session、Command、Turn、Item 与 Admission `AppendID`。Unique Request ID 阻止两个 Admission Transaction 启动同一 Logical Request。Terminal Status 从 Canonical Stream 重建，不由该表独立宣称。

`domain_identities` 在 Compact Aggregate 不保留 Completed Turn 的前提下维持当前 Domain 的历史唯一性规则。每个 Creation Event 写入 `(session_id, identity_kind, identity_id, introducing_event_id)`，并对 `(session_id, identity_kind, identity_id)` 建 Unique Constraint。同一 Append Transaction 在提交 Creation Event 前插入 `turn` 或 `item` Identity；重复时映射为现有 Domain Error，并中止整个 Append。它是由 Canonical Event 派生、同步维护的 Integrity Index：可以离线重建和验证，但 Live Store 不得省略或绕过。

### 7.7 可重建表

- `session_heads`：最小状态与 Active Turn/Item Candidate Index；
- `transcript_entries`：TUI 与未来 Context Consumer 使用的分页历史；
- `snapshots`：经过验证的 Aggregate 加载加速；
- `export_checkpoints`：可重建 Exporter Progress。

这些表不能单独证明领域状态。Recovery Candidate 通过 Head 发现，再用权威 Stream Replay 确认。

### 7.8 Runtime Ownership

`runtime_leases` 保存 `runtime_id`、单调递增 `fencing_token`、`lease_expires_at` 与 `last_heartbeat_at`。Lease 时间权威是 SQLite 自身的 `unixepoch('subsec')`，Caller 不提供 Wall Time。每个新领域 Append 在同一写事务验证：Runtime ID 相同、Fencing Token 相同且 `lease_expires_at >= sqlite_now`。Takeover 使用同一 `BEGIN IMMEDIATE` 串行化，只有旧 Lease 已过期才递增 Token。时钟前跳可能提前撤销 Host，后跳可能延迟 Takeover，但都不能让两个 Token 同时写入；异常优先保证 Safety。

`export_leases` 单独协调后台 Audit Exporter，具有 Expiry 与 Exporter Fencing Token，但不授权领域 Append。Consistent Export 写独立目标目录，不共享该 Lease。

### 7.9 SQLite 运行配置

- 默认 Driver 使用 `modernc.org/sqlite`，保持生产构建纯 Go 与 `CGO_ENABLED=0`。
- 打开数据库时配置并验证 WAL、`synchronous=FULL`、Foreign Key、Bounded Busy Timeout 与显式 Checkpoint Policy。
- Dedicated Serialized Writer Connection 持有 `BEGIN IMMEDIATE`；Read 使用有界 Pool，需要跨 Page 一致性时使用显式 Read Transaction。
- 所有等待由 Caller Context 与配置限定；Busy Database 不触发无界隐藏重试。
- 活动数据库只支持 Local Filesystem。启动时拒绝或显著诊断已知 Network/Synchronization Location。

## 8. EventStore v2 合同

```go
type EventStore interface {
    ReadStream(context.Context, ReadStreamRequest) (StreamPage, error)
    Append(context.Context, AppendRequest) (CommitReceipt, error)
    ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error)
    FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error)
}

type ReadStreamRequest struct {
    SessionID     domain.SessionID
    AfterSequence uint64
    Limit         uint32
    HeadVersion   *uint64
}

type StreamPage struct {
    Records           []domain.RecordedEvent
    HeadVersion       uint64
    NextAfterSequence uint64
    End               bool
}

type AppendRequest struct {
    AppendID        domain.AppendID
    SessionID       domain.SessionID
    ExpectedVersion uint64
    CommandID       domain.CommandID
    RuntimeID       RuntimeID
    FencingToken    uint64
    Admission       *CommandAdmission
    Events          []ProposedEvent
}

type CommandAdmission struct {
    RunTurnRequestID RunTurnRequestID
    RequestDigest    Digest
    TurnID           domain.TurnID
    ItemID           domain.ItemID
}

type ProposedEvent struct {
    ID            domain.EventID
    SchemaVersion uint32
    OccurredAt    time.Time
    Event         domain.Event
}

type CommitReceipt struct {
    AppendID       domain.AppendID
    CommitPosition uint64
    FirstSequence  uint64
    LastSequence   uint64
}

type ResolveAppendRequest struct {
    AppendID      domain.AppendID
    RequestDigest Digest
}

type AppendResolution struct {
    Kind    AppendResolutionKind
    Receipt *CommitReceipt
}

type FindCommandRequestRequest struct {
    RunTurnRequestID RunTurnRequestID
    SessionID        domain.SessionID
    RequestDigest    Digest
}

type CommandRequestRecord struct {
    RunTurnRequestID  RunTurnRequestID
    RequestDigest     Digest
    SessionID         domain.SessionID
    CommandID         domain.CommandID
    TurnID            domain.TurnID
    ItemID            domain.ItemID
    AdmissionAppendID domain.AppendID
}

type CommandRequestLookup struct {
    Kind   CommandRequestLookupKind
    Record *CommandRequestRecord
}
```

实施规格可以机械调整名称，但 Ownership 与 Semantics 是规范要求。

`AppendResolutionKind` 只能是 `Committed`、`NotFound` 或 `IdentityMismatch`；仅 `Committed` 时 `Receipt` 非空。`CommandRequestLookupKind` 只能是 `Found`、`NotFound` 或 `IdentityMismatch`；仅 `Found` 时 `Record` 非空。两个操作都可返回 `StoreUnavailable` 或 `StoreCorrupt` Error，但绝不能把它们编码成 Absence。`FindCommandRequest` 同时比较 Session 与 Digest；已有 Request ID 的任一项不匹配时返回 `IdentityMismatch`，且不泄露其他 Session 的 Record。`Found` 只证明 Admission Identity 已提交；Application 必须读取固定 Head 的 Canonical Session Stream，才能判断 Request 是 Running 还是 Terminal。

`command_requests.run_turn_request_id` 全局唯一。Session 内 Turn 与 Item 的唯一性由 `domain_identities` 独立强制；Record Field 与 Admission Receipt 插入后不可变。

### 8.1 Identity 职责

- `CommandID` 关联一次 Application Command；一次 `RunTurn` 的 Admission 与 Terminal Append 可以共享它，但它不是 Store Idempotency Key。
- `AppendID` 精确标识一次原子请求；一个 Command 的不同 Append 使用不同 ID，同一 Append 的重试复用它。
- `EventID` 在首次 Append 前稳定地标识一个不可变 Event。
- `commit_position` 对 Batch 全局排序；`sequence` 对 Session 内 Event 排序。

### 8.2 Request Digest

Request Digest 是对 Versioned、Length-Delimited Canonical Envelope 的 SHA-256；显式包含 Digest Format Version、Session ID、Expected Version、Command ID、`hasAdmission`，以及 Admission 存在时的 RunTurnRequestID、RunTurn Request Digest、TurnID、ItemID，随后包含 Ordered Event Count 与每个 Event 的 ID、Type、Schema Version、Occurred At、Canonical Payload，禁止拼接无 Framing 字符串。Admission Presence Bit 始终编码；存在时四个 `CommandAdmission` Field 全部编码，即使 Turn/Item ID 也出现在 Event Payload 中。这样同一 `AppendID` 下任何 Persistent Side Effect 变化都会返回 `AppendIdentityMismatch`。`AppendID` 是 Receipt Key，不进入自身 Digest；Runtime ID 与 Fencing Token 是新提交授权，不属于不可变请求身份。Canonical JSON、UTF-8、Timestamp Precision 与 Field Ordering 都版本化。

### 8.3 Transaction Algorithm

```text
BEGIN IMMEDIATE
  查询 append_id
    已存在且完整 request digest 相同 -> 返回原 receipt
    已存在但不同        -> AppendIdentityMismatch
  对新 append 验证 writer lease 与 fencing token
  若包含 command admission，查询 run_turn_request_id
    已存在且 Session/digest 相同 -> CommandRequestConflict
    已存在但 identity 不同       -> CommandIdentityMismatch
  检查 event_streams.version
    不匹配              -> VersionConflict
  验证 ID、payload、limit、schema 与 uniqueness
  在 domain_identities 预留 Creation Event 的 Turn/Item ID
  递增 global commit position 并分配连续 sequence
  若存在 admission，则插入 command_requests
  插入 event_appends、完整 events、同步最小 projection 与 export_outbox
  更新 event_streams、head_commit_position 与 head_audit_digest
COMMIT
```

Batch 要么全部可见，要么全部不存在。JSONL 不参与事务，也不能改变成功结果。

Receipt Resolution 有意先于 Fencing Validation。已被 Fencing 的进程可以确认其完全相同的请求已经提交，但不能创建新提交；继任 Host 也能使用新 Token 解析 Unknown Outcome，而不改变不可变请求身份。

### 8.4 精确重试

相同 `AppendID` 与 Digest 返回原 Receipt，即使 Stream 已继续前进；相同 ID 与不同 Digest 返回 `AppendIdentityMismatch`。Event/Sequence Uniqueness 不能替代 Receipt。Store 不重新 Decide Command，也不重试 CAS Conflict。

### 8.5 Error Algebra

- `InvalidAppend`：请求非法，确定未提交。
- `VersionConflict`：CAS 拒绝，确定未提交。
- `AppendIdentityMismatch`：错误重用 ID，当前请求未提交。
- `CommandRequestConflict`：另一个 Append 已用相同 Request ID 与 Identity 完成 Admission；当前 Append 未提交，Application 必须读取 Winner。
- `CommandIdentityMismatch`：Request ID 被用于不同 Session 或 Digest；当前请求未提交。
- `DomainIdentityConflict`：Creation Event 在同一 Session 重用了历史 Turn/Item ID；Batch 未提交，Application 按 Identity Kind 映射到现有 Domain Error。
- `WriterFenced`：Host 不再拥有数据库，未提交。
- `StoreUnavailable`：Commit Attempt 前失败，未提交。
- `CommitOutcomeUnknown`：COMMIT 可能成功，但数据库持续不可用，无法解析 Receipt。
- `StoreCorrupt`：存储不变量失败，修改 Fail Closed。

Pre-COMMIT Failure 可以返回确定未提交 Error。一旦尝试 COMMIT，就不能因为第二连接暂时看不到 Receipt 而推断确定未提交。Adapter 按经过验证的 Driver 行为 Finalize 或 Quarantine 原 Connection，再从新 Connection 执行一次有界 Receipt Lookup；匹配 Digest 返回成功，Absent 或 Lookup Unavailable 都返回 `CommitOutcomeUnknown`。调用方只能以同一 ID Resolve 或 Retry 完全相同请求。

SQLite Result-Code Test 覆盖 COMMIT 时的 Busy、Full、I/O、Interrupted 与 Close/Rollback 行为；禁止无界隐藏重试。`ResolveAppend` 按 `AppendID` 与 Request Digest 只读查询，返回 `Committed(receipt)`、`NotFound` 或 `IdentityMismatch`；无法执行 Lookup 时返回 `StoreUnavailable`，绝不能返回 `NotFound`。

### 8.6 Application Unknown-Outcome 状态机

每个 `RunTurn` 接收 Application 级、Caller-Stable `RunTurnRequestID`。Request Identity/Digest 与 Admission 在同一事务注册到 `command_requests`。本项目 TypeScript TUI 通过 Namespaced ACP `_meta.openCodeHarness.requestId` Extension 发送；其他 ACP Client 保持兼容，但若未协商并提供该 Extension，则不承诺跨 Connection Exactly-Once。

Exact Duplicate Request 永远不启动第二次 Model Call：Terminal Request 返回重建结果；Live Execution 允许附着或观察；没有本地 Execution 的 Running Request 等待 Startup/Live Reconciliation，不能新建 Turn；同 Request ID 不同 Digest 返回 `CommandIdentityMismatch`。

Application 在分配新 Execution Identity 前调用 `FindCommandRequest`。不存在时生成 ID，并只在 Admission Append 包含 `CommandAdmission`。Uniqueness Race 返回 `CommandRequestConflict`，Application 随后读取 Winner；只有拥有已提交 Admission 后才调用 Model。Versioned Request Digest 覆盖 Session ID、Prompt Content、Attachment、Selected Mode 与所有会改变 Execution Semantics 的 Option。

Append 返回 `CommitOutcomeUnknown` 时，Application 在 Live Execution Registry 保留完整不可变 Append Intent，为 Session 设置 Operational `append_outcome_unknown` Admission Gate（不是 Domain State），并阻止新 Admission。Bounded Resolver 使用相同 Identity 持续执行 `ResolveAppend` 或 Exact `Append`；不重新 Decide，也不重跑 Model。原 ACP Prompt 在 Connection 与 Resolution Budget 允许时保持 Pending；仍无法解析时返回稳定 Unknown-Outcome Error，Client 不能把它理解为可用新 Request ID 重发的授权。

Resolution 按 Stage 处理。Unknown Admission 必须在 Model Call 前解决；若已提交，只有原 Live Request 仍拥有 Execution 且未取消时才继续调用 Model，否则 Application 在不调用 Model 的情况下提交 `request_abandoned` Interruption。Unknown Terminal Append 发生在 Model Effect 后，只能解析为原 Terminal Receipt 或执行 Exact Terminal Append；绝不再次调用 Model。

Cancel 与 Disconnect 采用以下 Winner Rule。Live Registry 必须在尝试 Terminal Append 前，原子地从 `running` 前进到 `terminal_append_in_flight`，使 Cancel 只能观察到一个明确 Phase：

| 观察到的 Phase | Cancel Action | Durable Winner |
| --- | --- | --- |
| `running` | 停止 Model Delivery，并提交 `client_disconnected` 或请求指定的 Cancellation | 第一个成功的 Terminal CAS；Loser Reload 后返回 Winner |
| `terminal_append_in_flight` | 停止 Delivery，只记录 Operational Cancel Intent | Resolve 或 Exact Retry 原 Terminal Append；不得提交 Interruption |
| `terminal_outcome_unknown` | 保持 Session Admission Gate，并解析保留的 Terminal Intent | 原 Completed/Failed Terminal Outcome 始终获胜 |
| `terminal_committed` | 不产生 Domain Mutation | 已提交 Terminal Outcome |

因此，Model 已产生结果后的 Disconnect 不能用 `client_disconnected` 覆盖该结果。若 Running Phase 的 Interruption 在 CAS 上输给 Terminal Append，Application Reload 固定 Head 的 Stream，并为该 Request ID 报告已提交 Terminal Outcome。Fault Matrix 覆盖 Terminal Attempt 前 Cancel、COMMIT 期间 Cancel、Lost Acknowledgement 后 Cancel 与 Receipt Resolution 后 Cancel。

进程死亡后不额外用文件持久化 Uncertain Intent：权威数据库要么包含 Terminal Receipt/Event，要么不包含。Startup Replay 在已提交时返回 Terminal Result，否则把仍 Running 的 Execution 关闭为 `process_crash`。使用同一 `RunTurnRequestID` 重连只能观察该持久结果，不会再次调用 Model。

## 9. 分页 Replay、Aggregate 与 Projection

现有 Complete-Stream `Load` 和保留全部历史的 `Session.Turns` Map 不是生产规模合同。

### 9.1 Command Aggregate

写侧 Aggregate 只保留验证新 Command 所需的 Session Identity、Workspace、Status、Version、Active Turn/Item 与有界 Lifecycle Metadata，不保留无界 Transcript 或 Completed Turn Collection。历史 Message 属于 Transcript Projection，Query 不把 Command Aggregate 当 Read API。历史 Turn/Item 唯一性仍属于 Domain v2 Command Contract，但由事务维护的 `domain_identities` Index 强制，而不是扫描 Compact Aggregate。

### 9.2 Page Reader

`AfterSequence` 是 Exclusive。首个 Request 省略 `HeadVersion`；一次 SQLite Read Transaction 捕获 Stream Head，并只返回不晚于该 Head 的 Record。后续 Request 重复固定 `HeadVersion`，Store 只返回 `AfterSequence < sequence <= HeadVersion`。并发 Append 可以推进 Live Stream，但不能改变 Pinned View。`NextAfterSequence` 是最后返回 Sequence，空 Page 时保持原 Cursor；仅当 Cursor 等于 Pinned Head 时 `End=true`。Client 提供的 Head 高于当前 Stream Head，或其他不可能的 Cursor/Head 组合，返回 `InvalidRead`；只有内部已记录 Read View 对应的 Canonical Event 消失才返回 `StoreCorrupt`。不同 Page 之间不保持 Read Transaction 或 Connection。

把当前 Full-History `Session` 改成 Compact Aggregate 是显式 Domain v2 Breaking Migration，不是仅存储重构。必须先定义 `ApplyCompact`，并用现有 Replay Fixture 证明 Command-Decision Equivalence，再迁移 Application、Adapter 与 Test Double。

### 9.3 Snapshot

Snapshot 保存 Aggregate Schema、Session ID、Covered Sequence、Source Digest/Chain Head、紧凑 Aggregate 和 Implementation Version。Load 时验证 Identity、Schema、Sequence 与 Digest。非法或未知 Snapshot 被忽略并从 Event 重建；关闭 Snapshot 不改变行为。

## 10. JSONL 审计副本

### 10.1 角色

JSONL 是完整无损审计副本、人类可读诊断材料、稳定交换格式、经过验证的灾难恢复来源与社区分析工具的公开表面。它不是在线提交点、并列权威，也不能静默覆盖活动数据库。

### 10.2 Batch Envelope

一行表示一次原子 Append，而不是一个孤立 Event：

```json
{
  "formatVersion": 1,
  "commitPosition": 42,
  "appendId": "append_...",
  "commandId": "command_...",
  "sessionId": "session_...",
  "expectedVersion": 8,
  "firstSequence": 9,
  "lastSequence": 10,
  "committedAt": "2026-08-13T10:00:00Z",
  "previousDigest": "sha256:...",
  "events": [],
  "batchDigest": "sha256:..."
}
```

`batchDigest` 认证除自身之外的规范 Envelope；`previousDigest` 构造有序 Hash Chain。Manifest 与 Segment Checksum 检测删除、插入、乱序、截断和修改。

### 10.3 布局

```text
audit/
├── manifest.json                         # 可丢弃 latest-generation hint
├── manifests/
│   └── 000000002000-<head-digest>.json  # immutable generation
├── segments/
│   ├── 000000000001-000000001000-<digest>.jsonl
│   └── 000000001001-000000002000-<digest>.jsonl
└── staging/
    └── <exporter-id>.partial
```

Sealed Segment 不可修改；文件名记录首尾 Commit Position 与 Digest。每个 Immutable Manifest Generation 记录 Format、全部 Segment Range、Byte Size、SHA-256 与全局 Chain Head；`manifest.json` 只是可替换 Hint，Startup 可以发现最高有效 Generation。Staging 不属于有效副本，可以丢弃。发布前完成 File `Sync`、Close、重新打开和 Digest Verification。跨平台 Rename Atomicity 不是领域正确性前提。

### 10.4 Export Algorithm

1. 通过 SQLite 获取有界 Exporter Lease。
2. 按 Commit Position 读取后续已提交 Outbox Row。
3. 验证保存的 Envelope Digest。
4. 写入有界 Staging Segment。
5. Sync、Close、重新打开并验证。
6. 发布 Sealed Segment。
7. 写入、Sync 并验证新的 Immutable Manifest Generation，再 Best-Effort 更新可丢弃 Latest Hint。
8. 最后更新 SQLite Export Checkpoint。

执行是 At-Least-Once；发布按 Commit Range 与 Digest 幂等。同 Range 同 Digest 表示已完成；同 Range 不同 Digest 隔离副本并触发重建。Export Failure 永远不回滚或伪造领域 Append。

### 10.5 Exporter Restart 状态机

Exporter Startup 不信任单个 Mutable Checkpoint：

1. 丢弃 Incomplete Staging File；它们是 Derived Data。
2. 扫描并验证 Immutable Manifest Generation 及其 Sealed Segment，并与 SQLite Append/Outbox Digest 对照。
3. 选择不超过 SQLite Head 的唯一最高连续有效 Generation；同一 Head 存在两个冲突有效 Generation 时隔离副本。
4. 对未被该 Generation 引用的 Sealed Segment，只有 Filename、Byte、Range、Chain Predecessor 与 SQLite Digest 都是精确 Next Range 时才 Adopt，否则 Quarantine。
5. 用 Canonical SQLite Byte 与冻结 Audit Codec 重建缺失或非法 Derived Segment。
6. 从已验证 Generation 重新计算并事务更新 `export_checkpoints`；Checkpoint Ahead/Behind 是待修复证据，不是 Authority。
7. 从下一个 Commit Position 继续。

因此无论崩溃发生在 Segment Publication 后、Manifest Generation Publication 后或 Checkpoint Update 前，都通过同一 Inventory Algorithm 收敛。平台存在经过验证的实现时执行 Directory Sync；缺少等价 Primitive 最多导致断电后 Derived File 丢失，不能创建领域事实，Restart 会重建。Conformance Matrix 在所有支持平台覆盖每个 Publication Boundary。

### 10.6 一致性导出与导入

`export --consistent` 在 SQLite Read Snapshot 中固定 Target Commit Position，输出截至该位置的所有 Batch，并验证 Self-Contained Manifest。普通文件复制不是受支持的 Export Procedure。

Import 只写入新数据库或空数据库，并依次验证 Manifest/Segment Digest、连续 Commit Position 与 Hash Chain、Payload Digest、每个 Session 的连续 Sequence、Expected-Version 转换、已知 Schema 与纯 Upcaster、完整 Domain Replay Invariant，最后重建 Head 与 Transcript Projection。禁止自动合并进活动数据库。

### 10.7 分歧策略

| 情况 | 必须执行的动作 |
| --- | --- |
| SQLite 比 JSONL 新 | Exporter 追赶 |
| JSONL Segment 缺失 | 从 SQLite/Outbox 重建 |
| JSONL Digest 不一致 | 隔离并重建副本，不修改 SQLite |
| JSONL 存在 SQLite 没有的 Batch | 宣告副本非法，绝不自动 Import |
| Manifest 损坏 | 从已验证 Segment 与 SQLite 重建 |
| SQLite 损坏且存在完整验证导出 | Import 到新数据库并显式切换 |
| 两者都损坏 | Fail Closed |

### 10.8 Backup 与 Privacy

主要 Backup 是 SQLite Online Backup API 生成的一致性副本，可以附带经过验证的 JSONL Export。只有 Manifest 完整覆盖声明 Head 时，JSONL 才能单独称为 Backup。

Data Directory 默认 Owner-Only Permission。无损 Audit Export 与可分享 Redacted Export 是不同命令。Redaction 写新文件，绝不修改规范 Segment。Raw Prompt、Model Output、Path、Tool Argument 与 Secret 默认排除在 Telemetry 之外。

## 11. Runtime Host 与 Crash Recovery

### 11.1 单 Host 与 Fencing

一个 SQLite 数据库只允许一个活动 Runtime Host。一个 Host 可以管理多个 Session，但 v0 stdio 每个进程暴露一个 Client Connection。第二个进程无法获取 Lease 时，以稳定诊断退出。

Acquire 或 Takeover 在事务中递增 Fencing Token。当前 Host 使用有界 Deadline Heartbeat。无法确认 Ownership 时停止接收新 Execution 并取消本地工作。恢复运行的旧进程不能 Append，因为每个事务都会验证旧 Token。

### 11.2 启动顺序

```text
打开数据库
→ 验证格式并执行 migration
→ 获取 Runtime lease 与 fencing token
→ 枚举 running candidate
→ 对每个 candidate 执行 ReadStream + replay
→ append recovery terminal fact
→ 开始接收 command
→ 启动后台 JSONL exporter
```

Reconciliation 完成前 Command 不可用；Audit Export Lag 不阻塞 Runtime Ready。

### 11.3 Recovery Transition

权威 Replay 结束于 Active Session、Running Turn 与 Running Assistant Item 时，生成一个原子 Batch：

```text
assistant.message.interrupted(code = "process_crash", message = "")
turn.interrupted(reason = "process_crash")
```

Session 保持 Active；原 `RunTurn` `CommandID` 保持 Correlation Lineage；Recovery 使用固定 Namespace，从 Session ID、Turn ID、Item ID 与 `process_crash` 确定性派生 `AppendID`。Acknowledgement 丢失时复用该 ID。重复 Reconciliation 返回 Receipt 或观察到已有 Terminal State，不能产生第二个 Terminal Pair。不引入长期 `recovering` Domain State。

既有 Domain 允许通过独立 `StartTurn` 形成“Running Turn、无 Active Item”的合法 Stream。Recovery 对该形态只追加 `turn.interrupted(reason = "process_crash")`；Stable `AppendID` 使用同一 Namespace 与显式 `no_item` Sentinel。Running Turn 若引用 Missing、Terminal、Mismatched 或多个 Active Item，不做猜测修复，而是由 Replay/Reconciliation 返回 `StoreCorrupt`。Domain v2 只有通过显式 Migration 才能删除 Standalone Transition。

### 11.4 不自动 Replay Model 或 Tool

Running Event 无法说明旧进程是在发送请求前、Stream 中、Provider 完成后、Terminal Commit 中，还是 Commit 成功但 Acknowledgement 丢失后崩溃。自动重复可能造成重复成本、回答、文件修改、Shell Command 或远程副作用。

Recovery 只关闭不确定执行。只有新的用户意图与新的 `RunTurnRequestID` 才创建新 Turn、`CommandID`、`AppendID` 与 Event ID，并可用 `retryOfTurnID` 记录 Lineage；使用相同 Request ID 的 Transport Retry 只能观察原 Durable Outcome。未来 Tool Runtime 必须先增加 Invocation Identity、Effect Classification、Prepare/Start/Result Boundary、Reconciliation Adapter 与显式 Safe-Retry Policy，才能自动重试 Tool。

## 12. Go Core 与 TypeScript TUI 边界

### 12.1 仓库布局

```text
cmd/open-code-harness/          Go composition-root binary
internal/harness/domain/        pure commands, events, invariants
internal/harness/application/   use cases and transaction boundaries
internal/harness/engine/        bounded agent execution
internal/harness/runtimehost/   lease, recovery, worker lifecycle
internal/harness/adapters/
  sqlite/                       canonical EventStore
  jsonlaudit/                   export/import
  acp/                          ACP agent adapter
  providers/                    provider adapters
  tools/                        built-in and MCP tool adapters
contracts/acp/                  pinned upstream stable schema
contracts/extensions/           namespaced Harness extensions
contracts/fixtures/             cross-language fixtures
clients/tui/                    TypeScript ACP client
evals/                          scenario and regression evidence
```

### 12.2 ACP 角色

ACP v1 是公开 Client Projection。它负责 Initialization、Version/Capability Negotiation、Session Setup/Load、Prompt、Update、Cancel、Permission、Filesystem、Terminal 与 Protocol Error Mapping；不拥有 Agent Loop、Domain State、Storage Transaction 或 Tool Policy。

| ACP 操作 | 内部权威 |
| --- | --- |
| `initialize` | 协议/能力协商 |
| `session/new` | `Application.CreateSession` |
| `session/load` | 权威 Replay 加 Client Projection |
| `session/prompt` | `Application.RunTurn` |
| `session/update` | Runtime Signal 与 Durable Fact 的投影 |
| `session/cancel` | 当前执行的幂等取消 |
| Permission Request | Policy/Approval 用例 |
| fs/terminal request | Policy 之后的 Tool Runtime Adapter，不能绕过 |

内部 Session ID 可以直接作为 ACP 不透明 `sessionId`。其他 Identity 保持内部，除非经过评审的 Projection 或 Namespaced Extension 显式暴露。

### 12.3 Delivery Ordering 与 Disconnect

```text
commit domain terminal events
→ send terminal session/update
→ return session/prompt result
```

Notification Failure 不能撤销 Durable Fact。重连通过 `session/load` 与 Transcript Projection 恢复；Server 不承诺重现所有 Ephemeral Token Delta。

- stdout 只包含 Protocol Frame；Log 与 Diagnostic 写 stderr。
- 一个 Serialized Writer 独占 stdout。
- Output Queue 同时有 Item 与 Byte Bound。
- 高频 Text Delta 可合并，但 Lifecycle、Permission 与 Terminal Update 不得静默丢弃。
- 持续阻塞会取消仍处于 `running` 的 Execution，并尝试提交 `client_disconnected` Interruption；Terminal Append 一旦开始，则改用 §8.6 的 Terminal-Winner Rule。
- 进程突然死亡在下次启动时按 `process_crash` 协调。
- `session/cancel` 幂等；没有 Running Turn 时是 No-Op。

### 12.4 Protocol Source of Truth

固定版本的官方 Stable ACP v1 Schema 是 Go Wire Source of Truth。Go Wire Type 从 Schema 生成或机械验证，并隔离在 ACP Adapter。TypeScript TUI 使用官方 `@agentclientprotocol/sdk`。项目 Extension 使用 `_meta`、Custom Capability 与下划线前缀 Method，不改变标准语义。Generated Artifact 禁止手改；CI 验证上游 Checksum 与 Generation Drift。ACP v2 Draft 保留 Experimental，不能进入 Stable v0 Capability Declaration。

### 12.5 Release Artifact

- `open-code-harness`：Linux、macOS、Windows 的纯 Go、无 CGO、单文件 Core Binary。
- `@open-code-harness/tui`：独立版本 TypeScript ACP Client。

Installer 或平台 Bundle 可以同时安装两者；Go 单二进制保证不要求内嵌 TUI Runtime。

## 13. 资源上限

初始默认值显式可配置，但必须位于硬验证范围内：

| 资源 | 默认上限 |
| --- | ---: |
| 单 Canonical Encoded Event Payload | 8 MiB |
| 单次 Append Event 数 | 64 |
| 编码后的 Append Request | 16 MiB |
| EventStore Read Page | 256 Records |
| ACP Output Queue | 256 Items 且 8 MiB |
| Active JSONL Segment | 64 MiB 或 10,000 Batches |
| SQLite Write Operation | 必须有 Caller Deadline |
| Runtime Shutdown | 有界 Graceful Deadline |

超过上限返回稳定 Error。任何组件都不能截断规范数据、增长无界 Queue 或把 OOM 当成 Control Flow。

现有 1 MiB Assistant UTF-8 Output Limit 继续作为 Application Limit。更大的 Encoded-Event Limit 覆盖确定性 JSON Escaping 与 Wrapper Overhead。Codec Test 必须证明每个合法最大 Assistant Output 都可以 Terminalize；未来 Payload Shape 必须给出相同 Bound Proof，或在 Admission 前使用显式 Chunk Event。

## 14. 可观测性

三类证据面保持分离：

1. Domain Audit：SQLite Event 与已验证 JSONL，是可恢复事实。
2. Operational Diagnostic：结构化 stderr Log，在可用时包含 Runtime、Session、Turn、Item、Command、Append 与 Trace Correlation。
3. Metric/Trace：可替换 OpenTelemetry Adapter，默认关闭内容属性。

最低指标覆盖 Append Latency/Conflict/Unknown Outcome、Replay Latency、Recovery Candidate/Result、Export Lag/Failure、Active Turn、Cancellation Latency、ACP Queue Pressure/Coalescing、Provider Latency/Usage/Failure 与 Tool Approval/Denial/Failure。用户内容和原始 Identity 不能成为高基数 Metric Label。

## 15. Migration 与兼容性

- SQLite Migration 只向前、排序、带 Checksum 且事务化。
- Migration-Specific Exclusive SQLite Transaction 与 Migration Ledger 阻止并发 Migrator；该事务在有效 Runtime Lease 存在时拒绝修改 Schema。Migration 在新 Runtime Lease Acquisition 与 Command Admission 前完成。
- Destructive Migration 前生成一致性 Backup。
- 已提交 Event Byte 永远不原地改写。
- Canonical Storage 与 Audit 永久保留原 Event Byte、Schema 与 Digest。确定性纯 Upcaster 只在 Decode/Replay Layer 执行，不能重写 Import 或既有 Event Row。永久 Fixture 把历史 Raw Byte 映射到当前 Logical Event，并验证原 Digest。
- 未知 Event Type 或 Schema Version Fail Closed。
- 不支持自动 Downgrade；Rollback 使用兼容 Backup，或把已验证 Export 导入新数据库。
- Project SemVer、Event Schema、Audit Format、Migration、ACP Protocol 与 ACP Schema Artifact 使用不同版本号。

## 16. 安全与隐私

- Data Directory 与 Audit File 默认 Owner-Only Permission。
- Workspace Root 与 Additional Directory 显式声明，并在 Scope Check 前 Canonicalize。
- 对已知不受支持的 Network/Synchronization Filesystem，活动数据库位置必须拒绝或明确警告。
- Credential、Prompt、Model Output、Tool Argument、Shell Command 与 File Content 默认不进入 Telemetry。
- Executable Configuration 只接受 Trusted Configuration Layer。
- Audit Export 与 Shareable Redacted Export 是独立显式命令。
- Plugin、ACP Client 与 MCP Server 不能绕过 Policy 或 Runtime Fencing。
- Recovery 不会在缺少显式 Reconciliation Contract 时重复高风险副作用。

## 17. 验证策略

### 17.1 Domain Property

State Transition、Terminal Irreversibility、Sequence Continuity、Item/Turn 原子终止、Deterministic Replay 与 Compact-Aggregate Equivalence 使用 Table、Property 与 Fuzz Test，不需要数据库或网络。

### 17.2 EventStore Conformance

每个 Adapter 运行同一套 Suite，覆盖 CAS、Atomic Batch、精确 AppendID Retry、Identity Mismatch、Commit Receipt、Post-Commit Acknowledgement Loss、Context Cancellation、Fencing、Concurrency、Pagination、Defensive Ownership 与 Corruption Detection。

### 17.3 Crash 与 Fault Matrix

在 Begin、Validation、Receipt Lookup、Event Insert、Projection Update、Outbox Insert、COMMIT、Acknowledgement、Segment Write、Sync、Publish、Manifest Update、Checkpoint Update、Lease Heartbeat、Takeover 与 Recovery Append 注入失败。真实 Subprocess Kill/Restart Test 补充 Mock。

### 17.4 Replay、Migration 与 Round Trip

所有历史 Schema Fixture 永久可 Replay。SQLite → JSONL → 新 SQLite 必须保持 Event、Stream Head、Append Receipt、Aggregate State、Transcript Projection 与声明 Digest 相同。

### 17.5 Protocol 与跨语言

使用 Schema Golden Test、Malformed-Message Test、In-Memory Duplex Test、真实 stdio Subprocess Black-Box Test，以及 Go Agent/官方 TypeScript SDK Test。测试 Slow Client、Full Queue、Cancellation、EOF、Reconnect/Load、stdout Pollution 与 Terminal-Delivery Failure。

### 17.6 平台与性能

CI 使用 `CGO_ENABLED=0` 构建并测试 Linux、macOS、Windows；在支持的平台运行 Race Test、Fuzz Smoke、Static Analysis、Migration Fixture 与平台文件行为测试。Benchmark 持久保存 Append、Replay、Startup Recovery、Export、Import 与 ACP Streaming 结果。没有存档证据就不做 Throughput 或 Scale 声明。

## 18. 文档与开源质量

每个主要 Architecture、Research Gate、Implementation Plan、Implemented Contract 与 Evaluation Ledger 均具有：

- 定义要求时使用英文 Normative Document；
- 完整同步中文阅读版，绝不是摘要；
- 匹配的 Section Structure、Decision Identifier、Link 与 Status；
- 在索引中声明 Authority 与 Implementation State。

如果翻译发生分歧，英文是机械权威，以便国际贡献者只有一条冲突规则。分歧本身是文档缺陷，发布前必须修复。

公开发布还要求 Contribution Instruction、Security Policy、Code of Conduct、ADR/RFC Process、Stability/Deprecation Policy、Compatibility Matrix、Reproducible Build、Dependency/License Inventory、SBOM、Signature、Checksum，以及数据 Upgrade/Backup/Recovery 文档。

## 19. 实施拆分

本设计不是一份单体实施计划。它通过六个串行工业级 Slice 交付；每个 Slice 都需要聚焦的一手资料 Architecture Gate、批准的中英文 Specification、中英文 Plan、TDD Implementation、独立 Review 与 Completion Evidence：

1. **EventStore v2 Contract**：AppendID、Proposed Event Metadata、Receipt、Error Algebra、Paginated Read、Compact Aggregate Boundary、Durable RunTurn-Request Admission 与 Application Unknown-Outcome State Machine。这是对每个 Adapter、Test Double、Append Helper、ID/Time Ownership、Domain State、Codec Fixture 与 Error Mapping 的显式 Breaking Migration；它必须在 SQLite Implementation 前完成。
2. **SQLite Canonical EventStore**：Migration、Transaction/CAS、Exact Retry、Fencing、Projection、Backup。
3. **JSONL Audit 与 Import**：Outbox、Envelope、Segment、Manifest、Consistent Export、Verification、New-Database Import。
4. **Runtime Host 与 Crash Recovery**：Lease、Heartbeat、Takeover、Startup Reconciliation、Graceful Shutdown。
5. **ACP v1 Adapter**：stdio、Capability Negotiation、Mapping、Cancellation、Backpressure、Conformance。
6. **TypeScript TUI**：官方 SDK、View State、Approval UX、Transcript-Driven Test、Packaging。

后续每个子系统 Architecture Gate 都必须重新核验当时仍公开、且与该 Slice 直接相关的 Pi、Kimi Code、Grok Build、Codex、Maka 与官方 DeepSeek Harness 实现及对应权威系统。DeepSeek-Reasonix 只作为社区上下文。本次证据不能替代该核验。参考证据指导决策，但绝不绕过本地 Contract、Test、License Review 或 Architecture Review。完成 Slice 1 并不强制立刻实现 Slice 2–6；见 [DeepSeek Harness 对照与交付顺序](../../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.zh-CN.md)。

## 20. 完成标准

一个 Slice 只有满足以下条件才标记为 Implemented：

- 中英文 Design 与 Plan 已接受；
- 一手资料 Architecture Evidence 已记录；
- 与 Slice 相关的 Normal、Failure、Cancellation、Concurrency、Race、Crash、Recovery、Replay 与 Resource-Limit 行为均已测试；
- Public Schema 与 Fixture 已版本化；
- 适用时 Linux/macOS/Windows 无 CGO Gate 通过；
- Benchmark 与 Compatibility Evidence 已保存；
- 独立 Review 没有未解决的 Correctness Finding；
- Implemented Contract 与 Documentation Index 已更新；
- Explicit Exclusion 与剩余 GA Blocker 继续可见。

成功运行不等于完成。没有上述证据的能力只能标记为 Experimental 或 Not Implemented。
