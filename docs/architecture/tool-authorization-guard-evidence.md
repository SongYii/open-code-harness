# Tool Authorization Guard Evidence / 工具授权核心防线证据

**Status:** Internal boundary implemented; no public policy plugin API.
**Date:** 2026-09-17.
**Roadmap:** [Startup extensibility](startup-extensibility.md) and
[启动时可插拔架构](startup-extensibility.zh-CN.md).

## Implemented boundary

`policy.Guard` is now the final authorization authority around an internal
decision strategy. `policy.New` always returns a guarded built-in strategy.
For empty names, network access, unknown or inconsistent risk metadata, and
out-of-workspace access, the core returns a denial without calling the strategy.
For inputs that reach the strategy, only the three declared effects with
non-empty valid UTF-8 rule and reason fields are accepted. Strategy errors and
invalid decisions return no usable decision, and Application's existing error
path records a deny rather than executing the tool.

The other two authorities did not move. `tools.Catalog` still validates risk
and mutation consistency at construction and returns defensive copies.
Application still owns the separate `tools.Approver` port: a
`require_approval` policy decision is not an approval and cannot execute until
that port grants it. Default policy modes, event shapes and transcript codecs
were not changed.

## Executable evidence

- `policy.TestGuardRejectsCoreDenialsBeforeCallingStrategy` wraps an explicitly
  permissive strategy and proves every core denial happens without invoking it.
- `policy.TestGuardDelegatesValidDetachedInput` proves a valid value-only input
  reaches the strategy exactly once and its valid decision survives unchanged.
- `policy.TestGuardFailsClosedOnStrategyErrorsAndInvalidDecisions` covers a
  strategy error, unknown effect, missing fields and invalid UTF-8.
- `policy.TestGuardRejectsNilStrategies` covers both a nil interface and a
  typed-nil implementation, preventing a delayed panic at decision time.
- Existing catalog defensive-copy/validation tests and Application approval,
  denial, timeout, domain-codec and transcript tests remain the evidence for
  immutable risk ownership, separate approval and persisted-byte compatibility.

Two temporary source mutations were run and then restored:

| Mutation | Observed result |
| --- | --- |
| Remove the pre-strategy `coreDeny` branch | All seven hostile-input cases fail because the permissive strategy returns `allow` |
| Remove post-strategy decision validation | Unknown effect, empty rule, blank reason and invalid UTF-8 cases fail |

Observed targeted validation after restoring production code:

```sh
go test ./internal/harness/policy -count=1
go test ./internal/harness/application ./internal/harness/domain \
  ./internal/harness/transcript ./internal/harness/tools \
  ./internal/harness/architecture -count=1
```

Both commands passed with a task-specific writable Go build cache. The final
tree also passed `go test -race ./... -count=1`; this required normal loopback
socket permissions because several integration fixtures bind local listeners.

## Explicit exclusions

This slice does not add a startup registry, configuration schema, public SDK,
dynamic loading, runtime hot swap, durable policy identity or a third-party
consumer. It does not claim isolation from arbitrary in-process Go code: such
code could block or mutate process state before returning, which is why no
untrusted strategy loading surface is published. No provider call, paid model
traffic, persisted schema migration or OTel implementation change is included.

## 中文摘要

本切片把“策略建议”和“最终授权”分开。工具名称、网络、风险一致性和工作区边界由
核心先判断；命中核心拒绝时根本不调用策略。合法输入才交给策略，而策略返回后仍须通过
effect、规则、原因与 UTF-8 校验。策略报错或返回非法结果时没有可执行的授权结果。

风险目录仍由 `tools.Catalog` 构造、校验并防御性复制；审批仍是 Application 持有的
独立端口，`require_approval` 不等于已经批准。删除核心拒绝与删除输出校验的两次临时
变异均让对应测试准确失败，恢复后定向测试通过。

本轮没有公开策略 SDK、启动注册表、热替换、持久策略身份或第三方加载能力，也不宣称
能隔离任意进程内 Go 代码。没有发生模型调用、付费流量、持久化迁移或 OTel 实现修改。
