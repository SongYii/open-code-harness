# EventStore v2 Completion Evidence

- Scope: the pre-v0 internal EventStore v2 contract described by
  [Implemented EventStore v2 Contract](eventstore-v2.md)
- Plan: [EventStore v2 contract migration implementation plan](../superpowers/plans/2026-08-13-eventstore-v2-contract.md)
- Status: Tasks 1–9 implemented and locally verified; Slice 1 complete; not GA

This ledger is the public completion record. The plan remains the frozen
implementation sequence and intentionally retains its original checkboxes; an
unchecked plan box is not used as evidence. Commit history, executable gates,
and the commands below support the completion statement.

The in-memory adapter is a deterministic reference. It is not durable
production storage. SQLite implementation has not begun.

## Architecture gates

| Gate | Evidence | Adopted outcome |
| --- | --- | --- |
| EventStore v2 contract | [EventStore v2 gate](../research/architecture-gates/2026-08-13-eventstore-v2-contract.md) | Exact append, receipt identity, pinned pagination, admission, unknown outcome |
| DeepSeek Harness sequencing | [2026-08-15 comparison](../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.md) | Finish Slice 1 before Provider/Tool; do not automatically continue into SQLite/ACP/TUI |

## Task and commit ledger

| Task | Delivered evidence | Commits |
| --- | --- | --- |
| 1 | Stable identities, digest value, Store types, typed errors | `d4908e2`, `09b0f3a` |
| 2 | Canonical framed digest and resource checks | `ce2c929`, `9f9f50b` |
| 3 | Compact command aggregate and chronology equivalence | `783fcb4`, `a7c95b7`, `e6836e9` |
| 4 | Memory reference adapter and shared v2 conformance | `1cf80db`, `792cf35`, `7d79132`, `0f89806`, `03fc0bd` |
| 5 | Application append ownership and pinned read | `f8a67af`, `c2fe016`, `7a5ed74`, `b034fee`, `8c4123b`, `c9cafc1` |
| 6 | Durable request admission and one live execution | `591c122`, `08f9012`, `87b3006`, `1dd23e1`, `c7d6a15`, `bb42803`, `07d4227`, `05317a4` |
| 7 | Unknown-outcome resolution and cancellation winner | `2ce9a87` |
| 8 | Compact Session cutover and deletion of every v1 Store surface | `48b7ac4` |
| 9 | Implemented contract, Chinese reading copy, evidence, and CI gates | this commit |

Planning and comparison commits that remain part of the Slice 1 history include
`08f2d6b` and `fe204ed`.

## Executable completion gates

The final review runs these commands from the repository root and requires
every command to exit zero:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
go test ./internal/harness/architecture -run TestNoEventStoreV1Surface -count=1
go test ./internal/harness/application -run 'TestDigest|TestResolveAppendIntent' -count=1
go test ./internal/harness/domain -run 'TestCompact|TestReplay' -count=1
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
```

Local evidence also includes a 10-second digest fuzz smoke, a 10-second compact
replay fuzz smoke, and one memory EventStore benchmark sample. Those commands
are recorded in the verification section when this commit is prepared; they are
not a substitute for the CI race and architecture gates.

## Benchmark baseline

Captured on the Task 9 verification host before this commit
(`goos=darwin`, `goarch=arm64`, `cpu=Apple M1`):

```text
BenchmarkEventStore-8    10000    1817588 ns/op    4252868 B/op    10192 allocs/op
```

Command:

```bash
go test ./internal/harness/adapters/memory -run '^$' -bench BenchmarkEventStore -benchmem -benchtime=1s
```

The memory adapter is a correctness reference. Its nanoseconds-per-operation
figure is a regression tripwire, not a production capacity claim. This sample
creates one isolated Session append per iteration and therefore includes
identity-index and receipt work, not a warmed single-stream append.

## Deferred blockers

Slice 1 is complete only within its stated contract. The following remain
unimplemented and are not implied by this ledger:

- SQLite canonical EventStore
- JSONL audit replica, export, and import
- durable Runtime host, fencing lease, and crash recovery
- ACP v1 adapter
- TypeScript TUI
- Provider, Tool/Policy, Context Engine

GA remains blocked on those milestones.

---

## 中文证据台账

- 范围：[已实现 EventStore v2 合同](eventstore-v2.zh-CN.md)所定义的 pre-v0 内部合同
- 计划：[EventStore v2 Contract Migration 实施计划](../superpowers/plans/2026-08-13-eventstore-v2-contract.zh-CN.md)
- 状态：Task 1–9 已实现并完成本地验证；Slice 1 完成；不是 GA

本台账是公开完成记录。计划保留为冻结的实施顺序，并有意保留原始复选框；未勾选的
计划框不作为完成证据。完成结论由提交历史、可执行门和下述验证命令共同支撑。

内存适配器是确定性参考实现，不是可持久化的生产存储。SQLite 实现尚未开始。

### 任务与提交

| 任务 | 交付证据 | 提交 |
| --- | --- | --- |
| 1 | 稳定 Identity、Digest、Store 类型与错误 | `d4908e2`, `09b0f3a` |
| 2 | 规范 Framed Digest 与资源校验 | `ce2c929`, `9f9f50b` |
| 3 | Compact Command Aggregate 与时间顺序等价 | `783fcb4`, `a7c95b7`, `e6836e9` |
| 4 | 内存参考适配器与共享 v2 Conformance | `1cf80db`, `792cf35`, `7d79132`, `0f89806`, `03fc0bd` |
| 5 | Application Append 所有权与钉住分页读 | `f8a67af`, `c2fe016`, `7a5ed74`, `b034fee`, `8c4123b`, `c9cafc1` |
| 6 | 持久 Request Admission 与单一 Live Execution | `591c122`, `08f9012`, `87b3006`, `1dd23e1`, `c7d6a15`, `bb42803`, `07d4227`, `05317a4` |
| 7 | Unknown-Outcome Resolution 与 Cancellation Winner | `2ce9a87` |
| 8 | Compact Session 切换并删除全部 v1 Store 表面 | `48b7ac4` |
| 9 | 已实现合同、中文阅读版、证据与 CI 门 | 本提交 |

### 剩余阻塞

Slice 1 只在其合同范围内完成。SQLite、JSONL 审计副本、持久 Runtime Host/恢复、
ACP、TUI、Provider、Tool/Policy 和 Context Engine 仍未实现，不能由本台账暗示。
