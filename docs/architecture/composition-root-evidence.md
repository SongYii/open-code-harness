# Composition Root Completion Evidence / 组合根完成证据

**Status:** Complete evidence ledger

**Date:** 2026-08-19

**Design:** [Composition Root and Cross-Adapter Conformance (Slice 5)](../superpowers/specs/2026-08-19-composition-root-conformance-design.md)

**Plan:** [Composition root and conformance implementation plan](../superpowers/plans/2026-08-19-composition-root-conformance.md)

**Contract:** [Composition Root — Implemented Contract](composition-root.md)

This ledger records what was done, what was found, and what was not done.
Completion is claimed from the evidence below, not from checkbox state.

英文为规范记录；下方「中文摘要」是同步阅读版，分歧以英文为准。

## Task commits

| Task | Commit | Subject |
| --- | --- | --- |
| 1 | `a103895` | engine/modeltest: split the transport-neutral contract out of Run |
| 2 | `bad80ff` | openaicompat: satisfy the transport-neutral model contract |
| 3 | `8a3b003` | sqlite: run the Engine scenario suite against the durable store |
| 4 | `e776d4a` | composition: add the tested root and make the dependency guard stricter |
| 5 | this commit | assembly test, cmd/och, contract, and this ledger |

## Verification commands

All keyless and network-free.

```bash
gofmt -l .
go vet ./...
go test -race ./... -count=1
go test -race ./... -count=3                                    # determinism gate
GOOS=windows go build ./... && GOOS=darwin go build ./...
go test ./internal/harness/testkit -run TestScriptedModel -v     # 28 subtests
go test ./internal/harness/adapters/openaicompat -run TestModelContract -v
go test ./internal/harness/adapters/sqlite -run TestEngineScenarioContract -count=5
go test ./internal/harness/composition -count=1
```

## Completion criteria

| Criterion | Result |
| --- | --- |
| `enginescenariotest` green against memory and SQLite | Yes. Same seven scenarios, same names, same outcomes. |
| `modeltest.RunContract` green against the double and `openaicompat` | Yes. Five transport-neutral cases against both. |
| `modeltest.Run` green against the double with no lost case | Yes. 27 subtests before, 28 after: one addition, no removal. |
| Adapter imports permitted in `composition` only | Yes, with one documented exception: `runtime` may import `sqlite`. |
| Unowned packages still forbidden | Yes, and asserted directly rather than implied. |
| `cmd/och` builds on every cross-build platform | Yes, `GOOS=windows` and `GOOS=darwin`. |
| Assembly test with no network, credential, or sleep | Yes. |
| No implemented contract document edited | Yes. |

## Findings

### F1. Clock and IDGenerator had no production implementation

`application.Clock` and `application.IDGenerator` were satisfied only by
`testkit`, and no production package may import `testkit`. The harness
therefore could not be assembled outside a test, in any configuration. This
is the integration gap the slice was written to close, and it appeared as a
compile error the moment a real root was attempted.

Resolved by `adapters/system`, which is new production code but not a new
port: it implements two ports that already existed.

### F2. `engine.ModelStream.Next` has an ambiguous context contract — open

`Next` takes a context per call, which reads as a promise that cancelling it
interrupts that call. `testkit.ScriptedModel` implements that reading.
`openaicompat` cannot: `Next` blocks in `scanner.Scan` on the response body,
and that read is interrupted by the context the request was issued with — the
one given to `Stream` — not by an unrelated context handed to `Next`. A direct
probe confirmed `Next` never returns when a foreign context is cancelled.

Production is unaffected: `engine/runner.go` derives one `streamCtx` and
passes it to both `Stream` and every `Next`, so the combination the probe
exercised is one the runner never produces, and turn cancellation works.

No production code was changed. The contract case now shares one context
between `Stream` and `Next`, matching how the port is actually driven, and the
double's stronger assertion moved to a case only in-process implementations
run. **The port question — must `Next`'s context be independently effective,
or does the port mean the stream's context — is unanswered and belongs to a
future slice.**

### F3. The dependency guard treated "unowned" as "unrestricted"

The walk checked imports only when a directory had a declared owner. A new
package under `internal/harness` that nobody had classified could import any
adapter. Fixed: unowned directories are now checked against a default deny.

### F4. Runtime's adapter deny-list was an enumeration

`ownerRuntime` denied four named adapters, so every adapter added later
silently widened its reach — `adapters/system` had already become importable
there before anyone intended it. Fixed: it denies the adapters root with
`sqlite` carved out, so a new adapter is denied by default.

### F5. `enginescenariotest` found no divergence between adapters

All seven scenarios pass against SQLite with the same outcomes as against
memory. Nothing was fixed. Recorded because the absence of a divergence is
the result: for these scenarios the memory adapter was not satisfying an
assumption the durable adapter breaks.

## Verified by construction

Two guard properties were checked by introducing a violation, observing the
failure, and reverting:

```
application imports sqlite      → forbidden package dependency
unowned package imports sqlite  → forbidden package dependency from an unowned package
```

Two assertions were mutation-checked the same way. Changing `NextCalls() != 3`
to `!= 99` inside the extracted model contract fails the double's suite,
proving the moved assertions still execute. Setting the assembly test's policy
to `deny_all` makes the turn answer `"policy denied this tool"` and fails the
test, proving the real policy path is exercised rather than bypassed.

## Deferred GA blockers

- No soak test of a long-lived assembly.
- No process-level crash injection against a running assembly.
- No verification against a live provider; every lane is fixture-driven.
- No performance characterization of the assembled path.
- No `Approver` in the assembly, so any tool the policy table routes to
  approval fails closed. Interactive approval arrives with a client.
- `cmd/och` has flags only: no configuration file, precedence rules, process
  supervision, or logging policy.

## 中文摘要

本切片收口集成：六个已实现切片此前各自验证、从未合装。

**结论**：`composition` 成为唯一可点名 adapter 的包（`runtime` 可依赖 `sqlite`
是唯一且有文档的例外），并由架构守卫穷举断言；`enginescenariotest` 现在同时
对 memory 与 SQLite 运行；`modeltest` 拆分后真实 HTTP adapter 满足传输中立
合同；一个端到端装配测试在无网络、无凭据、无 sleep 的条件下跑通含工具调用的
完整 turn，断言基于从数据库读回并重放的持久事件流。

**五条发现**中两条值得强调。其一：`Clock` 与 `IDGenerator` **此前没有生产实现**，
只有 `testkit` 满足，而生产代码不得导入 testkit——这套东西在任何配置下都不可能
被真正装配起来。其二：`engine.ModelStream.Next` 的 context 语义**存在歧义且未
解决**——按计划要求上报而非擅改，生产路径不受影响，端口问题留给后续切片。

守卫的两条性质与两条断言都以「先制造违规、观察失败、再还原」的方式验证过。
