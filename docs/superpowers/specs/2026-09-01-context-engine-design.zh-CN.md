# Context Engine：预算化历史、持久压缩与恢复（中文阅读版）

**状态：** 已接受——2026-09-01

**日期：** 2026-09-01

**里程碑：** 8，Context Engine

**稳定性：** v1.0 前新增 Go 包、port、command、event 和配置均为 `internal`；`och compact-session` 为 `experimental`。本切片不承诺 ACP 扩展兼容性。

**规范源：** [2026-09-01-context-engine-design.md](2026-09-01-context-engine-design.md)。若中英文语义不一致，以英文规范源为准。

**调研依据：** [Context Engine 架构门](../../research/architecture-gates/2026-09-01-context-engine.zh-CN.md)

## 1. 决策摘要

Open Code Harness 新增一个独立 Context Engine。无工具与有工具 Turn 都从 canonical Session events 构造模型可见请求。引擎按当前路由模型容量计量完整 provider-neutral envelope，对超大 Tool Result 做 bounded projection，在工具配对安全的边界选择历史前缀，并且只有 durable checkpoint 已提交后才能让它替代模型请求中的源前缀。

Canonical EventStore 与 JSONL 审计永不被压缩。Checkpoint 是带精确 coverage、SHA-256 source digest、format version、token 证据和 predecessor lineage 的 lossy、disposable projection。正常 variant 是 LLM rolling summary；确定性 tail reset 不声明任何旧历史事实，只在 hard capacity 无法等待可信 summary 时安全降级。

压缩同步且是一等生命周期：

```text
context.compaction.started
  → context.compaction.completed(checkpoint)
  | context.compaction.failed
```

自动准备发生在首个 Provider call 前以及工具 Step 之间；可信 startup overflow 可以在严格 per-Turn 上限下压缩并重试；手动压缩复用同一 transaction 且拒绝 active Turn。没有后台推测式 compactor。

## 2. 当前问题

现有 Application 存在两条上下文路径：

- model-only `runSingleAttempt()` 只发送当前 `Input`；
- Tool Step loop 读取完整事件流，用 `projectPriorTurns()` 加历史，再用 4 MiB `MaxProjectionBytes` 做唯一上限。

因此：是否有 Tool Catalog 会改变记忆；byte size 代替 model token；长 Session 只能失败；构造请求会把完整 stream 读进内存；持久 `ModelRequestRecorded` 不能独立等价于真实 Provider envelope。

新设计必须保持 EventStore append-only CAS、bounded Domain aggregate、明确 command/event identity、unknown-outcome resolution、Runtime fencing、取消/恢复/审计确定性与 model-neutral Application。

## 3. 完整目标

1. 工具/无工具统一历史投影；
2. 消息、Tool Schema、framing 与输出预留的确定性 token budget；
3. bounded 两遍 EventStore scan 和 Turn/Step 安全边界；
4. 不改源事实的超大 Tool Result projection；
5. 有结构校验与 shrink proof 的 rolling summary；
6. durable summary/reset checkpoints；
7. pre-turn、mid-turn、manual、startup-overflow 四种触发；
8. 每次 Provider dispatch 前记录完整实际 request；
9. memory/SQLite latest-checkpoint projection 与 canonical rebuild；
10. incomplete compaction 的 Runtime Host 调和；
11. fixture、failure、cancel、concurrency、race、fuzz、mutation、长 Session 证据；
12. 双语 implemented contract、evidence ledger 与诚实 milestone 更新。

## 4. 非目标

- vector/RAG/embedding/semantic 或跨 Session memory；
- 删除、重写、squash canonical events；
- 没有真实 Adapter 的 provider-native opaque compact；
- 后台 summary、通用 Provider retry、ArchiveRead；
- rewind/branch/context editing；
- MCP、TUI、OpenTelemetry、milestone 10 eval runner；
- ACP compact 方法或自定义 live update；
- 对所有 OpenAI-compatible endpoint 承诺 tokenizer-exact。

## 5. 核心架构决定

| ID | 决策 |
| --- | --- |
| CE-01 | `internal/harness/contextengine` 只拥有纯 projector、meter、planner、checkpoint validator、materializer。 |
| CE-02 | Application 拥有生命周期、Provider call、CAS append、取消与 overflow retry。 |
| CE-03 | 当前 `CapabilityProfile.ContextWindowTokens/MaxOutputTokens` 是容量权威。 |
| CE-04 | 通用 meter 保守且确定；匹配的 durable provider usage 只能上调、不能下调估算。 |
| CE-05 | 自动压缩只在干净 pre-Provider 边界同步执行。 |
| CE-06 | 第一遍 pinned plan，必要时第二遍 pinned summarize/materialize。 |
| CE-07 | 优先 Turn 边界；超大 Turn 可在 closed Step 间切；永不拆 Tool pair。 |
| CE-08 | 正常 `rolling_summary_v1`，hard-limit fallback `source_tail_reset_v1`。 |
| CE-09 | Checkpoint 是 canonical log event；latest 表只是可重建 index。 |
| CE-10 | Tool Result projection 是 request shaping，不是第二 checkpoint authority。 |
| CE-11 | Conversation Provider 每次 dispatch 前都提交完整 envelope。 |
| CE-12 | 本里程碑只用 active conversation route 摘要；独立 Summary Provider 延后。 |
| CE-13 | 只有尚未交付 delta 的 startup overflow 可恢复。 |
| CE-14 | 手动入口是 Application + `och compact-session`；ACP 不变。 |

## 6. 组件与 port

```text
RunTurn / CompactSession
        ↓
Application Context Orchestrator
        ↓                         ↓
contextengine.Engine      ContextSummarizer
 meter/projector/planner    active engine.Model
                           purpose=context_summary
 prune/checkpoint/materialize
        ↓                         ↓
EventStore pages ↔ ContextCheckpointStore
        ↓
context.compaction.*
        ↓
context.prepared + model.request.recorded
        ↓
Provider
```

`contextengine` 不依赖 Application、Adapter、SQLite、ACP、filesystem、clock、random、logger 或 SDK。输入输出都是 owned copies。

Application 依赖 EventStore、ContextCheckpointStore、纯 Engine、ContextSummarizer、Clock、IDGenerator、AuthoritySource 和现有 execution registry；不导入具体 Adapter。

Summarizer 通过共享 bounded stream collector 直接使用 `engine.Model`，不进入 `RunTurn`、不产生 assistant delta、不递归触发 Context Engine。`ModelRequest` 增加 `Purpose(conversation|compaction)` 与请求级 `MaxOutputTokens`；摘要请求无 Tools，Tool Call 视为失败。

`ContextCheckpointStore` 是独立读 port；memory/SQLite 的同一个具体 Store 同时实现它。写入仍只经 `EventStore.Append`，latest index 由 committed completion event 派生。

## 7. Context unit、coverage 与 checkpoint

可切 unit：完整 Turn、closed assistant/tool Step、无 Tool 的 assistant message，以及 pre-turn 尚未提交的 current input。Model request/usage、policy、approval、context operational events 不形成对话 unit。Incomplete historical Tool pair 是 store/domain violation；当前未完成 pair 永远 protected。

Coverage 包含：

```text
coveredEventCount
coveredTurnCount
throughSequence
throughEventID
sourceDigest
```

它只覆盖 compactable conversational source 的有序前缀。Digest 使用可延伸 SHA-256 hash chain：`D0=SHA256("och-context-source-v1\\n")`；每个 source event 令 `Di=SHA256("och-context-source-step-v1\\n" || Di-1 || uint64-big-endian(encoded-length) || domain.MarshalRecordedEvent(event))`；最终 `sourceDigest=Dn`。Rolling successor 从已验证的 predecessor digest 继续，cold rebuild 从 D0 完整重算；不依赖不可持久化的 hash-library 内部状态。

Checkpoint 包含 ID、Session、kind、source/summary/prompt version、coverage、previousCheckpointID、summary/limitations、before/after/tail token、active route/usage、chunk 数与 prune 数。Successor 必须推进 coverage；same-coverage rewrite 必须显式指向当前 checkpoint 且 source digest 不变。

每次 conversation attempt 还有 `ContextDecisionID` 和 `ContextPreparedRecorded`：记录 trigger、attempt、pinned head、checkpoint、raw tail range、预算、meter、可选 usage-anchor request/attempt/observed-input/signed-delta/anchored-estimate、pruned-result provenance 与最终 serialized bytes。随后 `ModelRequestRecorded` 保存真正发送的完整 Messages/Tools。

## 8. 预算合同

对 context window `W` 与 max output `O`：

```text
safety = max(512, ceil(W * 0.02))
hardInput = W - O - safety
trigger = floor(hardInput * TriggerPercent / 100)
target = floor(hardInput * TargetPercent / 100)
protectedTail = floor(hardInput * TailPercent / 100)
summaryOutputCap = min(O, max(128, floor(hardInput * 0.10)))
```

Composition 拒绝 `O + safety >= W`。默认 trigger/target/tail 为 80/55/25；summary chunks 8（最大 16）；per-Turn overflow compaction 2（最大 3）；compaction timeout 2 分钟（最大 10 分钟）；单请求最多记录 64 个 pruned result；现有 4 MiB envelope cap 不得被放宽。

默认 meter `och_wire_estimate_v1`：文本/JSON `ceil(UTF-8 bytes/3)`；每消息固定 8；每 Tool Call/Result 再加 16；每 Tool Schema 为 16 加 canonical JSON bytes/3。它刻意高估典型 ASCII。

最终 dispatch 使用 `budgetEstimate=max(wireEstimate, anchoredEstimate?)`。Usage anchor 只有在 committed events 能证明以下全部条件时才合格：它是满足其余规则且 `InputTokens>0` 的最新 prior completed conversation attempt；request/usage 由 Session/Turn/Item/AttemptIndex 精确配对；adapter/endpoint/model/purpose/meter 及 Tool Schemas 等非消息 envelope 完全相同；当前消息 surface 可由旧 request surface 通过同一 meter 可定价的有序 append 或 checkpoint/rewrite replacement 推导。合格时 `anchoredEstimate=max(0, observedInputTokens+signedSurfaceDelta)`。

`signedSurfaceDelta` 包含 sampled request 之后持久化的 assistant output、Tool Result 和任何删改。原始 `OutputTokens` 可能含未持久化 reasoning，不能直接相加；Engine 合同下 `CachedInputTokens` 是 `InputTokens` 子集，只作 audit/billing，不能重复相加。缺失、为零、route/tool/meter 不匹配、畸形或无法证明的 observation 一律忽略。Usage 不反向改写旧决定、不降低 deterministic estimate，也不单独代表不同形状的请求；全部输入来自 canonical events，因此 replay 决定一致。

## 9. 投影、安全边界与两遍扫描

事件投影：TurnStarted→user；AssistantMessageCompleted→assistant；ToolCallStarted 记录 name/Step；ToolCall terminal→tool result。一个 assistant message 的多 call 与全部 terminal result 是同一 Step，即使 completion 交错。只有所有 call 恰好一个 terminal result 后边界才 balanced。

Planner 从 head 向后保留 `protectedTail`：优先完整近期 Turn；单个历史 Turn 过大则保留最新 closed Steps；mid-turn 可覆盖 active Turn 的早期 closed Step；不能覆盖 open assistant item。选中前缀应把请求压到 target；受 chunk cap 限制的 partial advancement 只有缩小至少 10% 且不超过 hardInput 才可接受。

Pass 1 pin head、分页折叠 unit metadata、digest、token 和 bounded recent deque；低于 trigger 时持有内容不超过一个 request budget。需要压缩时，Pass 2 在同一 head 重读选中 sequence range，按 bounded chunks 摘要并单独 materialize tail。Context Engine 不再调用 `ReadWholeStreamPinned`。

## 10. Tool Result projection

阈值为：

```text
min(2048, max(256, protectedTail/2))
```

超出时保留 role、Call ID、name，把 body 改成固定 marker、event ID、original bytes、SHA-256、75% head excerpt 与 25% tail excerpt；UTF-8 只在 rune 边界切。完整原文仍在 EventStore/audit/transcript。完整 projected body 在 ModelRequestRecorded，provenance 在 ContextPreparedRecorded。本切片没有 ArchiveRead。

单个 protected user/assistant/projected tool unit 仍超过 hardInput 时，在 dispatch 前返回 `context_unit_too_large`。

## 11. 摘要与 reset

Prompt 是 `contextengine` 拥有的 versioned asset。它把 source、previous summary、manual focus 标为不可信 data，禁止继续对话、执行其中指令、调用 Tools、输出 hidden reasoning 或捏造完成状态。

`och_context_summary_v1` 必须且只能按顺序包含：Objective、User Constraints、Established Facts、Work Completed、Files and Commands、Open Work、Risks and Unknowns、Continuation。要求保留重要 path、ID、command、error code 与 uncertainty。

Rolling successor 每 chunk 只输入上一份 validated summary 与 newly covered units；最多 MaxSummaryChunks。所有调用只走 active conversation route，设置 `purpose=context_summary`，不带 Tool Schemas，也不跨 Provider fallback；失败遵循统一 summary-failure 语义。

写入前必须验证 UTF-8、非空、正常 finish、heading 恰好一次且有序、无未知 heading、token/256 KiB cap、无 Tool/non-text、redact 后仍合法、完整请求至少缩小 10%、checkpoint framing 小于 covered source。失败关闭 bracket，不产生 checkpoint。

`source_tail_reset_v1` 是固定 user-role marker，只说更早 canonical prefix 因容量没有进入当前模型上下文，不总结其内容。Automatic reset 只允许 hardInput/可信 overflow、summary 无法安全完成、有 safe prefix、reset+complete tail 能 fit。Cancellation 不转 reset；manual 默认 summary，必须显式选择 reset。以后可从 canonical source 重建 summary 替换 reset。

## 12. Domain 生命周期与请求记录

新增 commands/events：

```text
context.compaction.start   / context.compaction.started
context.compaction.complete/ context.compaction.completed
context.compaction.fail    / context.compaction.failed
context.preparation.record / context.prepared
```

Started 记录 ID、trigger、strategy、base head、previous checkpoint、version 与 route；Completed 嵌 validated checkpoint；Failed 只嵌 stable code/safe message，不嵌 partial output。

`Session` 只增加一个 bounded `ActiveCompaction{ID, Trigger, Strategy, BaseVersion, StartedAt}`。Start 设置，complete/fail 清除。Manual/pre-turn 要求 idle Session；mid-turn/overflow 要求 caller-owned active Turn 且位于 pre-Provider boundary。Active compaction 时新 Turn、close/delete、第二个 compaction 都拒绝。

`ModelRequestRecorded` 增加 Purpose、AttemptIndex、ContextDecisionID 并保存完整 envelope；ModelUsageRecorded 增同一 attempt index。生产首请求 admission batch：

```text
turn.started
assistant.message.started
context.prepared
model.request.recorded
```

后续 Step 是 assistant started + context prepared + full request；overflow retry 在同一 item 追加下一 attempt 的 prepared/request pair。

## 13. SQLite、恢复与重放

Migration 5 新增一行/Session 的 `context_checkpoint_heads`，只存 checkpoint event sequence/ID、checkpoint ID、covered sequence、source digest 与 commit position。读取时 join canonical completion event 并核对。Adapter 在接受 completed 前独立验证 coverage/hash chain：初始 checkpoint 从 D0 扫描，successor 从 indexed predecessor digest 扫 newly covered range，same-coverage rewrite 要求 digest 完全相同；只有 proof 通过才在同一 append transaction 更新 index，任何错误让 event/index 一起 rollback。因此正常 lookup 有界，也不需要盲信 Application 提交的 digest。

Migration/backfill、verified import 与 `RebuildAndVerifyContextCheckpointHeads` 从 canonical events 选择 furthest valid coverage；same-coverage 依 predecessor lineage，最终 tie 用 canonical sequence。Index 与 event 不一致是 `store_corrupt`，不能静默信任。

每次 replay 重新检查 ID/schema/kind/format、coverage、source proof、lineage、summary structure、当前 route budget 与 checkpoint+tail fit。Model switch 或更紧配置可拒绝旧 checkpoint。

Runtime startup 把 unmatched started 关闭为 `ContextCompactionFailed{runtime_recovered}`；不合成 summary/reset；继续复用 stable recovery Append ID、unknown-outcome resolver 和 fencing。先关闭 compaction，再 terminalize enclosing Turn。

## 14. 四条流程

### Pre-turn

完成 request validation/idempotency/ownership 后加载 Session、分配 IDs、读 latest checkpoint、对 incoming input+Tools 做 Pass 1；需要时先提交 compaction；随后 materialize 并原子追加 admission+prepared+full request，最后才 dispatch Provider。

### Mid-turn

Tool Result 全部提交后、下一 assistant item dispatch 前，在 post-tool pinned head 上规划；必要时由当前 Turn owner 压缩；随后 append assistant started+prepared+full request，再 dispatch。

### Overflow

只拦截没有任何 delta/tool call 的 `context_overflow`。关闭 stream、检查 cancel/cap、强制 plan、要求至少 10% reduction、提交 summary/reset、追加 attemptIndex+1 request，再重试。无 safe prefix、无缩小或 cap exhausted 都走原有 durable `context_overflow` failure。其他 Provider error 不进入。

### Manual

`Service.CompactSession` 接收 Session、strategy（默认 summary）和最大 4 KiB focus。它要求 active idle Session，走同一 planner/bracket/checkpoint。`och compact-session` 通过正常 composition/Runtime lease 运行，不能与另一 live writer 并行；stdout 输出一个稳定 JSON，日志写 stderr。Cancel 会 bounded cleanup 为 failed，不能静默 reset。

## 15. 失败、并发与安全

新增稳定内部 code：`context_budget_invalid`、`context_projection_invalid`、`context_unit_too_large`、`context_compaction_busy`、`context_nothing_to_compact`、`context_summary_failed`、`context_summary_invalid`、`context_checkpoint_invalid`、`context_compaction_limit`。已有 `context_overflow`、store/cancel/unknown-outcome 语义不变。

Candidate completion append 未 committed/resolved 前不可使用；version conflict 使 candidate 失效并在同一 deadline 下最多重规划一次；request append 失败意味着 Provider 尚未调用；summary failure 低于 hard 可继续 source-derived request，到 hard 自动路径才可 reset；runtime delivery 不改变 durability。

Local Session compaction registry + durable ActiveCompaction + Store CAS 共同串行化。Cancel 在 page/summarizer-call/ID/append 前后检查；start 已提交后的 cleanup 用 `WithoutCancel` 与 bounded terminal commit。自动流程没有 background pointer 或 late candidate。

Summary request 复用 active conversation Provider 与 credential；本里程碑不增加第二 Provider、credential、transport 或跨 Provider history path。DeepSeek Harness 有独立 `summarizationProvider/summarizationModel` 的真实先例，Grok Build 也有专用 compaction model，但本项目在出现明确成本、容量或合规消费者前延后该配置面。Summary 先 `redact.Text` 后验证/持久；无 Tools；checkpoint 用固定 framing 的 user-role context message，不提升成 system policy。Tool excerpt 中 marker 要 escape；所有 content/error/focus 都有 UTF-8、byte/token cap。

## 16. 资源上限

- EventStore page：256；
- live raw conversation：一个 hardInput envelope + 一个 open unit；
- tail metadata：protectedTail + 一个 unit；
- serialized request：4 MiB；
- summary output：summaryOutputCap 且 256 KiB；
- chunks：默认 8、最大 16；
- summary calls：active route 每 chunk 最多 1；
- overflow compaction：默认 2、最大 3/Turn；
- pruned results：64/request；
- manual focus：4 KiB；
- compaction：默认 2 分钟、最大 10 分钟；
- active compaction：1/Session；
- latest index：1 row/Session。

Canonical event/audit 在磁盘上随 Session 生命周期增长，这是 durable history，不是 live working context。Benchmark 必须证明 heap 取决于模型预算/page size，而不是历史 event 数量。

## 17. Adapter、协议与配置

OpenAI-compatible Adapter 支持完整 mixed-role history、请求级 output cap/purpose；把 `InputTokens` 规范化为包含 cached input 的完整 prompt occupancy，把 `CachedInputTokens` 视作其子集并拒绝 cached 大于 total input；同时保留 closed overflow classification。Memory/SQLite 共用 ContextCheckpointStore conformance；SQLite fault/migration/backup/import/rebuild 都覆盖新 projection。

Event codec 增加严格 context events；JSONL 正常哈希/导入；transcript 增 bounded compaction/prepared facts，completed 包含 checkpoint metadata/summary。ACP prompt 自动受益，但 session/load 仍显示 canonical conversation，不能用 checkpoint 替换用户可见历史，也不伪造 ACP live updates。

`composition.Config.Context` 提供 trigger/target/tail、chunks、overflow cap、prune cap 与 timeout。Zero scalar 使用默认；关系非法在构造任何资源前失败。Summarizer 接收已经构造好的 active conversation Provider；不引入 provider registry、第二 Provider config 或 plugin kernel。

构造顺序：Host/store → conversation Provider/runner → Context Engine/summarizer → tools/catalog → Application → ACP。任何中途失败沿现有 release path 释放。

## 18. 验证与发布

纯包需要 budget/meter golden、中文/代码/JSON/schema fixture、usage anchor 精确匹配/append-replacement delta/全部 identity-envelope mismatch/zero-missing-malformed/non-lowering 矩阵、投影/边界矩阵、Tool pair/prefix/fits fuzz、digest/lineage/current-budget、summary/redaction/reset goldens。

Application 场景覆盖工具/无工具等价、完整 request、四 trigger、active-route summary/cancel/no-cross-provider-fallback、usage anchor 接受/拒绝/signed delta/route-tool-meter mismatch/zero-missing usage/cached-input 不重复计数、chunk cap/non-shrink/reset/oversized unit、所有 append phase 的 success/failure/unknown/conflict、duplicate RunTurn join、dispatch-before-append 禁止、overflow retry 与 crash recovery。

Store/Host 覆盖 memory/SQLite conformance、projection transaction rollback、migration/import/rebuild/corruption、reconciliation idempotency、JSONL/transcript/backup/reader。

完成证据至少包含：

```text
go test ./...
go test -race ./...
go vet ./...
```

以及 fuzz smoke、architecture/doc guards、SQLite fault、100/1,000/10,000 Turn benchmark 与 mutation。Mutation 必须能杀死 trigger 比较、reserve、Tool boundary、source digest、append-before-use、shrink validator、reset gate、overflow cap 与 current-budget replay check。

发布时新增双语 implemented contract、evidence ledger、authority/root README、milestone 8、CLI/help/getting-started、配置/隐私/运维说明。真实模型质量 eval 与更广 Provider 覆盖前仍标记 not GA；fixture/fault 只证明机制，不证明普适摘要质量。

## 19. 实现边界

后续 plan 可以细化文件名，但不能把边界重新揉回 `application/loop.go`：

```text
internal/harness/contextengine/   budget/meter/source/projector/planner/prune/checkpoint/materialize/prompt/validation
internal/harness/domain/          context IDs/commands/events/codec/apply/decide
internal/harness/application/     ports/orchestration/manual/overflow
internal/harness/engine/          purpose/output cap/bounded collector
internal/harness/adapters/        memory/sqlite/openaicompat
internal/harness/runtime/         incomplete compaction reconciliation
internal/harness/transcript/      facts
internal/harness/composition/     Context config
cmd/och/                          compact-session
```

## 20. 被拒绝方案与评审红线

拒绝 in-memory-only summary、重写事件、第二 Context 数据库权威、后台压缩、正常路径 tail-only truncation、只信上一请求 usage、没有实现的 native-compactor interface。本里程碑还延后独立 Summary Provider：pinned `0a53fb55` 的 DeepSeek Harness 在 `packages/compaction/compaction-basic/src/config.ts` 暴露成对的 `summarizationProvider/summarizationModel`，`bc7f02e` 的 Grok Build 也有专用 compaction model；但本项目没有第二 Provider 的部署消费者。未来必须由具体成本、容量或合规需求驱动，并明确 strict failure 与 fallback，不能静默跨越 privacy boundary。

评审应拒绝任何能够：commit 前使用 summary、拆 Tool pair、覆盖非前缀事件、信任与 event 冲突的 index、发送超过 hard/byte cap 的请求、delta 后 retry、把 summary history 路由到 active conversation Provider 以外、把 cancel 变 reset、让 live heap 随 Session lifetime 增长、把 checkpoint 当 canonical history、或在缺少 recovery/conformance/race/mutation/benchmark/docs/evidence 时宣布 milestone 完成的实现。
