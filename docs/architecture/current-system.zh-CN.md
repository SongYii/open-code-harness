# 当前系统架构

- 状态：已实现合同
- 与代码最后核对：2026-09-09
- 英文规范真源：[current-system.md](current-system.md)
- 设计：[当前系统架构与边界收口](../superpowers/specs/2026-09-09-current-system-architecture-design.zh-CN.md)

本文描述系统现在实际存在的整体结构。基础章程负责未来方向，各模块合同负责模块
内部细节，本文负责跨模块拓扑、包归属、权威边界和依赖方向。

## 1. 整体结构

独立客户端只通过 ACP v1 与 Agent 进程通信。ACP Adapter 把请求交给
Application；Application 编排 Context、Model、Policy 和 Tool；合法状态变化由
Domain 产生事件并追加到 EventStore。MCP 和内置工具共用同一条策略、审批、脱敏
和审计路径。Provider HTTP 隐藏在 `engine.Model` 后面。

Composition 是唯一可以普遍点名具体 Adapter 的生产包。唯一刻意保留的例外是
Runtime 可以依赖 SQLite，因为 Runtime Host 负责规范 Store 的租约和生命周期。

## 2. 包归属与允许依赖

`internal/harness` 下每个生产目录都必须匹配下表；未登记目录会直接导致架构测试
失败。表中是允许的直接内部依赖：

| 包/owner | 职责 | 可直接依赖 |
| --- | --- | --- |
| `domain` | 命令、事件、状态机、重放约束 | 无 |
| `redact` | 纯 Secret 脱敏 | 无 |
| `engine` | 模型与流式端口 | `domain` |
| `policy` | 纯风险决策 | `domain` |
| `tools` | 工具规格与执行端口 | `domain` |
| `agentinstructions` | 固定系统提示与指令渲染 | `domain` |
| `contextengine` | 上下文预算、投影、压缩、checkpoint | `domain`、`redact` |
| `application` | Turn/Step 编排和事务边界 | 上述服务包与 `domain` |
| `adapters/*` | ACP、Provider、文件、进程、MCP、存储等外部 I/O | 各自明确列出的端口；Adapter 之间禁止互相导入 |
| `runtime` | 租约、恢复、心跳、Exporter 生命周期 | `application`、`domain`、仅 `adapters/sqlite` 例外 |
| `transcript` | Session 只读投影/导出 | `application`、`domain` |
| `composition` | 生产装配与关闭 | 所有被装配 Adapter 及其端口；禁止依赖 Eval/Testkit |
| `eval` | Scenario、证据与评分 | Application/Composition 等公共生产入口；禁止直接构造 Adapter |

完整逐包 allowlist 位于
`internal/harness/architecture/dependencies_test.go`，由测试执行。`testkit` 及具名
contract-test 目录是显式测试支持，不是生产 owner。

## 3. 客户端与入口

`internal/client` 整棵树位于 ACP 进程边界外：它不能导入 Harness，Harness 也不能
反向导入它。`cmd/acp-client` 和 `cmd/acp-web-bridge` 同样不得导入 Harness。
`cmd/och` 是生产 Agent 入口；`cmd/och-eval` 通过 Eval/Composition 驱动生产路径。

## 4. 一次 Turn 的控制流

1. ACP 校验协议和 workspace admission，并调用 Application；
2. Application 取得 Session 权威，协调 workspace instructions，并让 Context
   Engine 构建请求；
3. 模型经 `engine.Model` 调用；
4. 工具意图先过 schema 和 Policy，高风险操作再等审批；
5. 允许的内置/MCP 工具通过 Adapter 执行；持久化前先脱敏；
6. Domain 把命令决定成事件，EventStore 以乐观并发原子追加；
7. ACP 只投影已接受事件。UI 收到消息不是提交边界。

Application 掌握循环和追加顺序；Adapter 掌握外部 I/O；Domain 掌握合法状态转换。

## 5. 数据与生命周期权威

| 内容 | 权威 | 非权威/派生形式 |
| --- | --- | --- |
| Session/Turn 事实 | 规范 EventStore 追加日志 | replay state、ACP update、transcript |
| 生产存储 | SQLite EventStore | Memory 仅是 conformance/test 实现 |
| 上下文压缩 | 经 coverage/digest 验证的 checkpoint | materialized request 与 summary 是投影 |
| 审计副本 | 发布前的 SQLite audit-chain 状态 | JSONL 是副本，不是第二写入者 |
| 工作区内容 | 主机文件系统 | 已读 fingerprint 只决定是否允许修改 |
| 工具权限 | Policy 决策与审批结果 | 模型的 tool intent 只是请求 |
| Adapter 装配 | Composition | Application 不选择具体 Adapter |
| 租约和恢复 | Runtime Host | 客户端连接不拥有恢复生命周期 |
| 评测结论 | 冻结身份与已提交证据 | Judge 不能覆盖失败的确定性前置条件 |

Harness 事实以事件追加为发布边界。Checkpoint 和导出只能加速或展示重放，不能
改写历史。

## 6. 修改边界的规则

新增生产包或内部依赖时，必须在同一个 PR：选择/新增 owner、更新可执行 allowlist
和回归测试、更新本文及相关模块合同，并解释任何向上或同级依赖。owner 不清楚
表示架构决策尚未完成，不能先走默认路径。

## 7. 当前未覆盖能力

本文不新增完整 TypeScript TUI、Windows runtime enforcement、OpenTelemetry、远程
daemon/A2A、Streamable HTTP MCP/OAuth 或第二类 Provider，也不把 pre-v0 宣称为 GA。
