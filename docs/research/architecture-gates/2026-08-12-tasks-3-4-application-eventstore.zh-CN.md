# Task 3–4 架构门：Application 与 EventStore 边界

- 日期：2026-08-12
- 范围：Engine 计划 Task 3–4，并影响 Task 7–10
- 评审结论：**READY_WITH_AMENDMENTS**
- 评审时实现状态：尚未开始

英文版本 [2026-08-12-tasks-3-4-application-eventstore.md](2026-08-12-tasks-3-4-application-eventstore.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 核心证据与取舍

| 项目 | 一手资料与事实 | 我们的决定 |
| --- | --- | --- |
| OpenAI Codex | [`app-server README`](https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md)：Turn 内有持久化 Item，同时向客户端发送 Item/Turn 通知；realtime 与持久 Thread Item 明确分离，客户端可关闭部分通知。 | 采用“持久事实与 delivery signal 分离”；公开资料没有 CAS/原子 append 合同，不能臆推。 |
| Maka | [`ARCHITECTURE.md`](https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md)：Runtime Host 是唯一执行权威，Runtime Event Log 是事实源，context/UI/recovery/compaction 是 projection。 | 采用唯一 Application 命令权威、canonical replay 和非权威 projection；公开文档未定义 expected-version CAS。 |
| Kimi Code | [`AGENTS.md`](https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md)、[Session 存储](https://moonshotai.github.io/kimi-code/en/guides/sessions.html)、[Wire replay](https://moonshotai.github.io/kimi-cli/en/customization/wire-mode.html#replay)：transcript 合同与 engine 解耦，拥有按 scope 的批序号；wire.jsonl 按记录顺序只读重放。 | 采用消费端合同所有权、单调 scope 顺序和只读 replay；延期 subscription/catch-up，不能据此推出原子 EventStore/CAS。 |
| Pi | [`session-manager.ts`](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/session-manager.ts)：Session 是 append-only JSONL tree，历史保留，模型上下文从历史/compaction path 派生。 | 采用 append-only 历史与派生上下文；不照搬同步文件修改、rewrite 或缺少显式乐观并发合同的部分。 |
| MiniMax Mini-Agent | [`Mini-Agent`](https://github.com/MiniMax-AI/Mini-Agent)：项目明确定位 minimal demo，提供完整 loop、持久笔记、上下文、日志与集成测试。 | 只作端到端学习材料；公开内容不能支持 CAS、原子批次、replay 权威或防御复制决策。 |
| DeepSeek-Reasonix | [`ARCHITECTURE.md`](https://github.com/esengine/DeepSeek-Reasonix/blob/v1/docs/ARCHITECTURE.md)：provider 特化架构分离 immutable prefix、append-only log 和 volatile scratch，并保持 append order。 | 采用有序 append-only 历史和 transient scratch 分离；拒绝在 Application/EventStore 引入 provider cache 优化。它是 `esengine` 社区项目，不是 DeepSeek 官方仓库，也无公开 CAS 证据。 |
| KurrentDB / EventStoreDB | [批量追加原子性](https://docs.kurrent.io/server/v22.10/http-api/introduction#batch-append-operation)、[expected revision 并发控制](https://docs.kurrent.io/clients/rust/legacy/v4.0/appending-events#handling-concurrency)：客户端提交期望 stream revision，版本不符则失败；多事件追加全成或全败。 | 作为 CAS 与原子有序批次的工业存储对齐基准；我们的端口保持更小且不绑定数据库。 |

## 必须修订的合同

1. **精确定义 version。** Session version 等于权威、连续 recorded-event stream 的记录数；`ExpectedVersion` 必须精确相等。成功追加 N 条后版本为 `ExpectedVersion + N`，Append 只返回本次新记录。Load 返回完整 stream；缺失 stream 返回空结果，由 Application 决定是否是 `session_not_found`。
2. **不要虚构事务型 IDGenerator。** Store 原子性表示最终赋值前记录/version 不可见。context、请求、CAS、预提交故障阶段拒绝时 Clock/ID 调用均为零；进入候选批次后 Clock 精确调用一次。后期 replay/source 失败可以留下未入库的 Event ID；它是不透明唯一标识，不要求无空洞。已提交 sequence 仍由持久 stream 长度派生，必须连续。
3. **增强可复用 contract suite。** Harness 同时暴露 `FailNextLoad` 与 `FailNextAppend`；测试 Load 故障按 Session 隔离、只消费一次、不改状态，下一次 Load 正常且防御复制。还要断言缺失 stream 和 Append 返回形状。
4. **增加 adapter 专属故障测试。** nil Clock/IDGenerator 不 panic；成功精确一次 Clock；早期拒绝零 source 调用；第 N 个 Event ID 失败、zero/超 RFC3339 范围 Clock 都不改变记录/version；底层 cause 可经 `errors.Is/As` 找到。
5. **Replay 是权威，snapshot 是缓存。** Task 3–4 不提供 snapshot port。未来 snapshot/index/transcript/UI state 都只能是可丢弃 projection，不得决定 CAS、stream version 或 sequence。
6. **重试与错误归属不变。** Store 不 reload、不重新 Decide、不自动 retry。未来 retry 必须由调用者明确执行 Load → Replay → Decide → Append，并定义 Command ID/幂等语义。VersionConflict 保持类型化并映射 conflict；其他 Load/Append cause 映射 persistence 且保留 error chain；稳定 Error 文案绝不拼接原始错误。
7. **持久化与通知背压解耦。** EventStore 不增加 subscription API。RuntimeSink/未来 server adapter 管 bounded delivery、catch-up/backpressure，不能阻塞、回滚或重定义 atomic append。

## Adopt / Reject / Defer

| 决策 | 内容 |
| --- | --- |
| Adopt | Application-owned ports；唯一命令权威；全量 replay；精确 per-Session CAS；原子有序批次；批内一个 UTC 时间；不同不透明 Event ID；防御复制；类型化 conflict；显式 retry ownership；确定性一次性故障。 |
| Reject | adapter 自行 Decide；Store 内部 CAS retry；snapshot/projection 权威；持久化 token delta；subscriber 成功作为 commit 语义；无空洞/事务型 IDGenerator 承诺；从顶级项目臆推未公开保证。 |
| Defer | 生产 EventStore、幂等 key、snapshot/checkpoint、迁移、subscription、catch-up cursor、backpressure、global order、多 stream 事务、compaction、retention 和自动 retry policy。 |

上述内容同步进入英文规范、中文阅读版与 Task 3–4 实施计划后，本架构门结论为 **READY**。
