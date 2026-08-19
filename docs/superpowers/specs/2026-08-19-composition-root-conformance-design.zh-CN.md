# 组合根与跨 Adapter 一致性（Slice 5）中文阅读版

**状态：** 已接受设计

**日期：** 2026-08-19

**上级：** [基础架构宪章](2026-08-11-open-code-harness-architecture-design.md)

**证据：** [组合根与跨 Adapter 一致性架构门](../../research/architecture-gates/2026-08-19-composition-root-and-conformance.zh-CN.md)

**本切片不得改变的已实现合同：** [领域事件](../../architecture/domain-events.md)、[Engine 纵切](../../architecture/engine-vertical-slice.zh-CN.md)、[EventStore v2](../../architecture/eventstore-v2.zh-CN.md)、[Provider adapter](../../architecture/provider-adapter.zh-CN.md)、[Tool Runtime](../../architecture/tool-runtime.zh-CN.md)、[SQLite 规范 EventStore](../../architecture/sqlite-eventstore.zh-CN.md)、[JSONL 审计副本](../../architecture/jsonl-audit-replica.zh-CN.md)、[Runtime Host](../../architecture/runtime-host.zh-CN.md)

英文版本 [2026-08-19-composition-root-conformance-design.md](2026-08-19-composition-root-conformance-design.md) 是规范文本；本文是与之完整同步的中文阅读版。两者若有分歧，以英文为准。

## 1. 决策摘要

六个切片各自已实现并已验证。**它们从未与彼此装配在一起过。** 本切片收口这个缺口，且不增加任何能力：它引入具体实现被接线的**唯一位置**，并重新分配既有一致性套件，使真实 adapter 去跑今天只有 double 在跑的合同。

承重决策如下：

1. **一个组合根，形态是库。** `internal/harness/composition` 返回一个已装配、可关闭的值。`cmd/och` 是读配置并调用它的薄二进制。装配由测试断言，而非靠启动进程。
2. **根是唯一被允许导入 adapter 的包**，且该许可由现有架构守卫**强制执行**，不只是写在文档里。其他所有包保持禁止，未声明 owner 的包同样保持禁止。
3. **`enginescenariotest` 对 SQLite adapter 运行**，与内存 adapter 并行，套件本身不变。
4. **`engine/modeltest` 拆分**为「任何 `engine.Model` 都满足的传输中立合同」与「只有进程内实现才能表达的 double 记账套件」。`openaicompat` 跑前者。
5. **一个装配测试**在单进程内串起 Application、SQLite、经本地 `httptest` 回放既有 SSE fixture 的 `openaicompat`、`workspacefs`、`localexec` 与 `runtime.Host`，不联网、不用凭据。

若其中任何一项需要改动已实现合同，那是**要上报的发现**、要单独排期的切片，而不是在此处顺手做掉的改动。

## 2. 目标

1. 一个具名、被测试的组合根，装配全部已实现 adapter。
2. 根之上的薄二进制，证明装配可以作为程序被触达。
3. `enginescenariotest.Run` 对 SQLite 全绿。
4. 传输中立的 `engine.Model` 合同对 `openaicompat` 全绿。
5. 一个端到端装配测试，覆盖"调用工作区工具并提交到临时目录下真实 SQLite 文件"的一个 turn。
6. 一个「恰好只允许一个包导入 adapter」的架构守卫。
7. 以上全部为无密钥、无网络验证。

## 3. 非目标

1. ACP、TUI、MCP、Context Engine。
2. 插件内核、DI 容器、服务定位器、基于反射的接线。
3. 生成式组合图（架构门 R2；待根稳定后再议）。
4. 默认验证路径中调用真实 provider（架构门 R4）。
5. 新端口、新事件、新错误码、任何合同变更（架构门 F7.5）。
6. 配置文件格式、flag 人机工程、守护进程化，以及超出装配测试所需的日志策略。
7. 多 host、多工作区、多租户装配。

## 4. 组合根包

`internal/harness/composition` 暴露：

- `Config` —— 描述一次装配的扁平、有界值。
- `Open(context.Context, Config) (*Assembly, error)` —— 校验 `Config`，按依赖顺序构造各 adapter，返回运行中的装配，或返回 nil 装配加一个错误。**部分构造绝不泄漏**：返回错误前释放每一个已成功构造的资源。
- `Assembly` —— 持有已构造的 `*application.Service`、`*runtime.Host` 与 `application.EventStore`。访问器只读。
- `(*Assembly) Close() error` —— 幂等、有序关闭。错误用 join 聚合，绝不吞掉。

构造顺序固定且显式：SQLite store → Runtime Host → provider Model → 工作区文件系统与命令执行器 → 工具目录与 policy → `application.Service`。关闭顺序相反。

该包只做构造，不做决策。其中没有领域状态转移、没有重试策略、没有任何只为测试而存在的分支。

## 5. 配置与边界

`Config` 只携带装配无法自行推导的信息：

| 字段 | 含义 | 边界 |
| --- | --- | --- |
| `WorkspaceRoot` | 约束全部文件系统工具的绝对路径 | 必须存在、必须是目录、使用前规范化 |
| `DatabasePath` | SQLite 数据库文件 | 父目录必须存在 |
| `RuntimeID` | fencing 租约的写者身份 | 非空、合法 UTF-8、无首尾空白 |
| `AuditDirectory` | JSONL 审计副本目标目录 | 可选；为空则禁用导出器 |
| `Provider` | base URL、模型名、API key 来源、超时 | base URL 必填；key 从环境读取，测试中绝不写成 `Config` 字面量 |
| `Policy` | `policy.Mode` | 默认 `policy.ModeDefault` |
| `Limits` | step、工具调用、审批、exec 边界 | 默认取自 `application.DefaultConfig()` |

`Config.Validate()` 是全量且 fail-closed 的：在构造任何资源之前检查每个字段。非法 `Config` 不构造任何东西。

任何字段都不得放宽已实现合同已固定的边界。凡 `application.Config`、`sqlite.Config`、`runtime.Config` 中已有的边界，`composition.Config` 只转发、绝不重新定义。

## 6. 生命周期与关闭

`Open` 只在 Runtime Host 完成启动重整（startup reconciliation）之后返回，因此返回的 `Assembly` 已可接受 turn。若重整失败，`Open` 失败并释放已构建的一切。

`Close` 停止准入，以有界超时等待 host 的循环，最后关闭 store，返回 join 后的结果。重复调用 `Close` 是安全的，返回首次结果。调用方若丢弃 `Assembly` 而不 `Close`，将泄漏 SQLite 句柄与 host goroutine；此点如实声明，不做防御。

## 7. 依赖守卫扩展

`internal/harness/architecture` 为 `internal/harness/composition` 新增 `ownerComposition`。禁止导入表变更如下：

- `ownerComposition` 可导入 `domain`、`engine`、`application`、`policy`、`tools`、`runtime`，以及 `internal/harness/adapters` 下的全部包。
- 其他每个 owner 的现有禁止项**全部不变**，包括「`application` 不得导入任何 adapter」。
- **未声明 owner 的目录仍然禁止导入 adapter。** 测试对此显式断言，使得新增包不会悄悄继承组合例外。
- `cmd/och` 只可导入 `composition` 与标准库。

守卫本身是测试，因此该扩展也被现有的 `TestClassifyProductionDirectory` 表覆盖。

## 8. 场景套件跑在持久化 store 上

`internal/harness/adapters/sqlite` 新增一个测试，用 `Harness` 调用 `enginescenariotest.Run`，其 `Store` 是在临时目录上真实 `Open` 出来的 store，形态对齐既有的 `adapters/sqlite/conformance_test.go`（后者用于 `eventstoretest`）。

**套件不得修改。** 若某场景对 `adapters/memory` 通过而对 SQLite 失败，缺陷在 adapter，或在于某个内存 adapter 恰好满足的假设，并在本切片内修复。若套件本身被证明编码了「仅内存成立」的假设，那是一条合同**发现**：先上报；仅当修正能保住内存 adapter 当前断言的每一条行为时，才修正套件。

## 9. 模型合同拆分

`engine/modeltest` 在不损失覆盖的前提下重组：

- `RunContract(*testing.T, Contract)` —— 任何传输都可观察的行为：精确的请求投递与有序的 `text_delta* tool_call* completed` 文法、流中错误传播、阻塞至取消的步骤、并发独立流。
- `Run(*testing.T, Factory)` —— 面向进程内 double 的入口**保持不变**。它先调用 `RunContract`，再跑 double 记账用例：`ReturnNilStream`、`ReturnStreamOnStartupError` 优先级、`Close` 记账。`testkit.ScriptedModel` 保持**恰好**当前的覆盖。

`Contract` 除工厂外还携带匹配器，因为传输 adapter 会把启动失败报成一个分类过的 `ProviderFailure`，而不是进程内 double 返回的哨兵值：

```go
type Contract struct {
    Factory           Factory
    MatchStartupError func(error) bool // nil 表示要求身份相等
    MatchStreamError  func(error) bool // nil 表示要求身份相等
}
```

`openaicompat` 以一个工厂运行 `RunContract`：该工厂把每份 `Config` 作为 SSE 字节由本地 `httptest` 服务器提供，并配以断言 `ProviderFailure` 分类的匹配器。`adapters/openaicompat/testdata/sse` 中已有 fixture 能表达该用例时优先复用；仅在无现成 fixture 时新增。

double 专属旋钮**不删除**，也**不为 HTTP 伪造**。它们保持其本来含义：对进程内实现返回值的断言。

## 10. 装配测试

composition 包的外部测试包中有一个测试，构建真实装配并端到端驱动一个 turn：

- 工作区：`t.TempDir()`，预置一个该 turn 将读取的文件。
- Store：该目录下的真实 SQLite 数据库。
- Provider：指向 `httptest.Server` 的 `openaicompat`，回放一段 SSE 脚本——模型先请求 `read_file`，再依据工具结果作答。
- Policy：`policy.ModeDefault`，使读取无需审批即被放行，从而断言覆盖**真实 policy 路径**而非 allow-all 旁路。
- Host：启动、重整，并经 `Assembly.Close` 关闭。

断言：turn 完成；持久事件流回放得到相同状态；流中包含 `tool.call.started`、`policy.decision.recorded`、`tool.call.completed`；助手最终文本反映文件内容。不联网、无凭据、不使用基于 sleep 的同步。

第二个更小的测试断言：`Open` 遇到非法 `Config` 时不构造任何可观察之物——不创建数据库文件，也没有 goroutine 活过该调用。

## 11. 二进制

`cmd/och` 从 flag 与环境读取配置，调用 `composition.Open`，等待 `SIGINT`/`SIGTERM`，调用 `Close`，出错时以非零码退出。其中不含任何既非 flag 解析、也非信号处理的逻辑。CI 在交叉编译矩阵的每个平台上构建它；本切片不对它做其他测试。

## 12. 失败语义

- `Config.Validate` 的错误在构造之前返回，指名字段，且不被包装成 adapter 错误类型。
- 构造失败返回的底层 adapter 错误，其包装程度须保证 `errors.As` 能触达 `*application.Error` 与 `*application.StoreError`。
- `Open` 绝不在返回非 nil 错误的同时返回非 nil `Assembly`。
- `Close` 返回各阶段的 `errors.Join`；某一阶段失败不会跳过后续阶段。
- 装配不新增任何错误码与错误类别。

## 13. 资源边界

每条边界都是**继承而非发明**：step、工具调用、审批与 exec 超时、结果大小取自 `application.DefaultConfig`；连接池大小、busy 超时、载荷大小取自 `sqlite.Config`；心跳间隔与截止取自 `runtime.Config`。组合根只新增**一条**边界：`Close` 的关闭超时，默认 10 秒，超过则返回超时错误而非永久阻塞。

## 14. 交付计划

五个任务，每个一个 PR，见[实施计划](../plans/2026-08-19-composition-root-conformance.zh-CN.md)。

## 15. 完成标准

1. `go test -race ./... -count=1` 全绿，含新增装配测试。
2. `enginescenariotest.Run` 对 `adapters/memory` 与 `adapters/sqlite` 均全绿。
3. `modeltest.RunContract` 对 `testkit.ScriptedModel` 与 `adapters/openaicompat` 均全绿；`modeltest.Run` 对 `testkit.ScriptedModel` 仍全绿且无用例丢失。
4. 架构守卫仅在 `composition` 允许 adapter 导入，并断言未声明 owner 的包仍被禁止。
5. `cmd/och` 在交叉编译矩阵的每个平台上构建通过。
6. 没有任何已实现合同文档需要修改。若确有修改，该变更作为**发现**记入证据台账，并附独立后续项。
7. 证据台账记录每个任务提交、验证命令与全部遗留 GA 阻塞项。

## 16. 排除项

- ACP、TUI、MCP、Context Engine、评测、OpenTelemetry。
- 配置文件格式、配置优先级规则，以及超出最小需要的 flag 人机工程。
- 进程监管、重启策略、守护进程化。
- 多 host 或多工作区装配。
- 生成式组合文档。
- GA 阻塞项：无长时运行装配的 soak 测试，无进程级崩溃注入，无对真实 provider 的验证，无装配路径的性能刻画。
