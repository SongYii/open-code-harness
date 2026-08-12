# Engine Vertical Slice Completion Evidence

- Scope: the pre-v0 internal Engine vertical slice described by
  [Implemented Engine Vertical Slice](engine-vertical-slice.md)
- Plan: [Industrial Engine vertical slice implementation plan](../superpowers/plans/2026-08-12-engine-vertical-slice.md)
- Status: Tasks 1–10 implemented and locally verified; not GA

This ledger is the public completion record. The plan remains the frozen
implementation sequence and intentionally retains its original checkboxes; an
unchecked plan box is not used as evidence. Commit history, executable gates,
and the commands below support the completion statement.

## Architecture gates

| Gate | Evidence | Adopted outcome |
| --- | --- | --- |
| Assistant Item lifecycle | [Task 1 gate](../research/architecture-gates/2026-08-12-task-1-assistant-item-lifecycle.md) | Closed payload, corrupt-state rejection, stable terminal data, one timestamp per batch, schema-v1 compatibility |
| Application and EventStore | [Tasks 3–4 gate](../research/architecture-gates/2026-08-12-tasks-3-4-application-eventstore.md) | Exact stream-length CAS, authoritative Load, atomic append, defensive copies, explicit fault and ID semantics |
| Engine stream and runtime | [Tasks 5–6 gate](../research/architecture-gates/2026-08-12-tasks-5-6-engine-stream-runtime.md) | Synchronous ownership, bounded bytes, exact cleanup, runtime payload/ordinal rules, error-tree safety |
| Application orchestration | [Tasks 7–9 gate](../research/architecture-gates/2026-08-12-tasks-7-9-application-orchestration.md) | Atomic admission, pure preflight, exact append acceptance, context ownership, result/error algebra, recovery boundary |

## Task and commit ledger

| Task | Delivered evidence | Commits |
| --- | --- | --- |
| 1 | Assistant Item recorded lifecycle and timestamp invariants | `01d0e80`, `16ccf32` |
| 2 | Atomic assistant Item/Turn commands and replay | `146de60` |
| 3 | Application ports, typed errors, deterministic sources | `668b385`, `4af5b5b`, `9e128d8` |
| 4 | Atomic in-memory EventStore and reusable contract | `dfc26b3`, `7df3fa9` |
| 5 | Engine stream/runtime contracts and formal adapters | `4e4b188`, `042ca3f`, `37e6637` |
| 6 | Synchronous bounded TurnRunner and cleanup semantics | `14c4250`, `73b553a`, `c5a5521`, `9b0b134` |
| 7 | Atomic admission and Session service | `ab81791`, `77433a0` |
| 8 | Successful durable RunTurn orchestration | `9fd3be5`, `20c462b` |
| 9 | Durable failure, cancellation, delivery, and concurrency semantics | `801fe8d`, `3ada13b` |
| 10 | Reusable vertical scenarios, race gates, canonical fixture, dependency gate, and implemented docs | `712d54c` plus its final-review fix commit |

Architecture-gate commits are `4c948da`, `d97839b`, `c0430f6`, and `c32a44d`.
Task 10's review-fix SHA is intentionally described by history rather than
prewritten before that commit exists.

## Executable completion gates

The final review runs these commands from the repository root and requires
every command to exit zero:

```bash
gofmt -w .
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
git diff --check
```

The local Markdown-link checker resolves every relative Markdown link. Focused
tests additionally exercise the reusable scenario contract, same-Session and
32-Session concurrency, canonical JSONL fixture bytes and replay, recursive AST
dependency classification, and mutation probes for error/runtime observations.

## Deferred GA blockers

The milestone is complete only within its stated internal scope. GA remains
blocked on production persistence and running-Turn reconciliation/recovery.
Provider contracts, tools and policy, workspace sandboxing, ACP, TUI, public
SDK/protocol stability, context/compaction, MCP, subagents, durable runtime
telemetry, and production evaluation infrastructure remain separate,
not-yet-implemented milestones.

---

## 中文证据台账

- 范围：[已实现 Engine 纵切](engine-vertical-slice.zh-CN.md)所定义的 pre-v0 内部合同
- 计划：[工业级 Engine 最小纵切实施计划](../superpowers/plans/2026-08-12-engine-vertical-slice.zh-CN.md)
- 状态：Task 1–10 已实现并完成本地验证；不是 GA 版本

本台账是公开完成记录。计划保留为冻结的实施顺序，并有意保留原始复选框；未勾选的计划
框不作为完成证据。完成结论由提交历史、可执行架构门和下述验证命令共同支撑。

### 架构门

| 架构门 | 证据 | 已采纳结果 |
| --- | --- | --- |
| Assistant Item 生命周期 | [Task 1 架构门](../research/architecture-gates/2026-08-12-task-1-assistant-item-lifecycle.md) | 封闭 payload、拒绝损坏前态、稳定终态数据、单批次统一时间、schema-v1 兼容 |
| Application 与 EventStore | [Tasks 3–4 中文架构门](../research/architecture-gates/2026-08-12-tasks-3-4-application-eventstore.zh-CN.md) | 精确 stream-length CAS、权威 Load、原子 append、防御副本、明确 fault 与 ID 语义 |
| Engine stream 与 runtime | [Tasks 5–6 中文架构门](../research/architecture-gates/2026-08-12-tasks-5-6-engine-stream-runtime.zh-CN.md) | 同步所有权、有界字节、准确清理、runtime payload/ordinal 规则、error tree 安全 |
| Application 编排 | [Tasks 7–9 中文架构门](../research/architecture-gates/2026-08-12-tasks-7-9-application-orchestration.zh-CN.md) | 原子准入、纯 preflight、精确 append 验收、context 所有权、结果/错误代数、恢复边界 |

### Task 与提交

| Task | 已交付证据 | 提交 |
| --- | --- | --- |
| 1 | Assistant Item 记录生命周期与时间不变量 | `01d0e80`、`16ccf32` |
| 2 | 原子 Assistant Item/Turn 命令与 Replay | `146de60` |
| 3 | Application 端口、类型化错误、确定性 source | `668b385`、`4af5b5b`、`9e128d8` |
| 4 | 原子内存 EventStore 与可复用合同 | `dfc26b3`、`7df3fa9` |
| 5 | Engine stream/runtime 合同与正式测试 adapter | `4e4b188`、`042ca3f`、`37e6637` |
| 6 | 同步有界 TurnRunner 与清理语义 | `14c4250`、`73b553a`、`c5a5521`、`9b0b134` |
| 7 | 原子准入与 Session service | `ab81791`、`77433a0` |
| 8 | 成功路径的 durable RunTurn 编排 | `9fd3be5`、`20c462b` |
| 9 | durable 失败、取消、delivery 与并发语义 | `801fe8d`、`3ada13b` |
| 10 | 可复用纵切场景、race 门、canonical fixture、依赖门与实现文档 | `712d54c` 及其终审修复提交 |

架构门提交为 `4c948da`、`d97839b`、`c0430f6`、`c32a44d`。Task 10 的终审
修复 SHA 在提交产生前不预写，直接以提交历史为准。

### 可执行完成门

终审在仓库根目录执行以下命令，并要求全部退出码为零：

```bash
gofmt -w .
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
git diff --check
```

本地 Markdown 链接检查器还会解析所有相对 Markdown 链接。focused tests 另外覆盖可复用
场景合同、同 Session 与 32 Session 并发、canonical JSONL 逐行字节与 Replay、递归 AST
依赖分类，以及错误/runtime 观测的 mutation probes。

### 延后的 GA 阻断项

本里程碑只在声明的内部范围内完成。GA 仍被生产持久化与 running Turn 的
reconciliation/recovery 阻断。Provider 合同、tools/policy、workspace sandbox、ACP、TUI、
公共 SDK/协议稳定性、context/compaction、MCP、subagent、持久 runtime telemetry 和生产
evaluation infrastructure 都是独立且尚未实现的后续里程碑。
