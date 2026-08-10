# Open Code Harness 基础架构设计

- 状态：已接受，待书面复核
- 日期：2026-08-11
- 仓库：`open-code-harness`
- 文档范围：项目章程、系统边界、协议选择、质量属性和后续规格拆分

## 1. 决策摘要

Open Code Harness 是一个从零设计、面向开源生态的 Code Agent Harness。项目不基于 `personal-harness` 改造，也不是 Obsidian 相关项目。旧项目只作为经验、失败模式和评测基线的来源，不继承其架构约束。

项目采用以下基础决策：

1. 核心 Agent Engine 使用 Go；交互式 TUI 可以使用 TypeScript。
2. Engine 与客户端分进程运行，核心不依赖任何具体界面。
3. 面向 TUI 和 IDE 的公开客户端边界采用稳定版 ACP v1；本地 v0 使用 JSON-RPC 2.0 over stdio。
4. ACP 只是公开适配层，不是内部领域模型。内部 Harness 以 Go 领域类型、命令和事件为核心。
5. 外部工具和资源优先通过 MCP 接入；内置工具与 MCP 工具进入统一的工具执行、权限和审计管线。
6. 模型通过 Provider Adapter 接入，核心保持模型中立；DeepSeek、Kimi、MiniMax 等差异通过 capability profile 表达。
7. 每个核心子系统必须可独立测试、评测、回放和观测。
8. 项目从第一天区分 `stable`、`experimental` 和 `internal` API，为开源社区保留清晰、可演进的扩展边界。
9. A2A、远程 Agent daemon 和分布式多 Agent 协作不进入 v0，但架构不得阻止后续以 adapter 形式加入。

## 2. 背景与问题

现代 Code Agent 的质量不仅取决于模型。真正决定系统上限的是 Harness：上下文构建、Agent loop、工具执行、权限、恢复、压缩、模型适配、协议、可观测性和评测共同形成的运行环境。

许多学习型实现把 UI、模型调用和工具循环写在一起，短期简单，随后会出现以下问题：

- 只能接一种模型或一种客户端；
- UI 生命周期绑死 Agent 生命周期；
- 工具权限散落在具体实现中；
- session、turn、tool call 的结束状态含糊；
- 无法确定失败来自模型、协议、上下文、工具还是策略；
- 评测只能跑端到端，无法定位子系统退化；
- 插件接口一旦公开就难以兼容演进。

Open Code Harness 的目标是先建立合理边界和证据体系，再逐层实现能力。

## 3. 项目目标

### 3.1 核心目标

- 构建模型中立、界面中立、事件驱动的 Code Agent Engine。
- 支持可恢复的 session/turn 生命周期和明确的终止语义。
- 让模型调用、上下文决策、工具执行、审批和状态变化均可观测、可审计、可回放。
- 为 Engine、ACP、MCP、Provider、Policy、Context 和 Eval 建立独立验证入口。
- 通过标准协议进入现有 TUI、IDE、工具和模型生态。
- 建立适合外部贡献者理解、扩展和验证的公共架构。

### 3.2 “强大 Harness”的可验证定义

本项目不把“完美”作为无法验收的绝对承诺，而将其转化为以下质量属性：

- **Deterministic**：给定相同的已记录输入和确定性替身，状态投影可重复生成。
- **Observable**：模型调用、工具调用、审批、压缩、重试和错误均有结构化 trace。
- **Evaluable**：每个核心组件都有独立基准和失败归因。
- **Recoverable**：进程中断后，已持久化 session 能恢复到明确状态。
- **Model-neutral**：模型特性通过能力声明进入系统，而不是散落条件分支。
- **Policy-driven**：权限与风险决策集中、显式并可测试。
- **Protocol-compatible**：公开 ACP/MCP 边界有 conformance suite。
- **Replaceable**：TUI、Provider、工具后端、存储和传输可通过接口替换。
- **Debuggable**：失败能追溯到具体 command、event、attempt 和 policy decision。
- **Benchmarkable**：质量、成本、延迟、稳定性和安全性都有基线。

## 4. 非目标

基础架构阶段明确不做以下事情：

- 不延续或重构 `personal-harness`；
- 不建设 Obsidian 插件或知识库产品；
- 不把模型隐藏推理文本作为正确性的必要条件；
- 不承诺 stdio 子进程在 TUI 崩溃后仍保持进程存活；
- 不在 v0 建设云端控制平面、团队账户、计费或多租户系统；
- 不在 v0 实现 A2A 或远程多 Agent 网络；
- 不为了抽象完整而预先实现尚无真实消费者的扩展点；
- 不把 TUI 行为当作 Engine 正确性的唯一验证方式。

## 5. 总体架构

```text
┌─────────────────────────────────────────────────────────────┐
│ Clients                                                     │
│ TypeScript TUI · Zed · JetBrains · other ACP clients        │
└──────────────────────────┬──────────────────────────────────┘
                           │ ACP v1 / JSON-RPC 2.0
                           │ stdio in v0
┌──────────────────────────▼──────────────────────────────────┐
│ ACP Adapter                                                 │
│ validation · capability negotiation · projection            │
├─────────────────────────────────────────────────────────────┤
│ Application Layer                                           │
│ commands · orchestration · transaction boundaries           │
├─────────────────────────────────────────────────────────────┤
│ Harness Domain                                              │
│ session · turn · item · attempt · policy · events           │
├───────────────┬────────────────┬──────────────┬──────────────┤
│ Model Runtime │ Tool Runtime   │ Context      │ Persistence  │
│ Providers     │ built-in/MCP   │ assembly     │ log/checkpt  │
├───────────────┴────────────────┴──────────────┴──────────────┤
│ Observability and Evaluation                                │
│ OpenTelemetry · replay · scenario eval · conformance        │
└─────────────────────────────────────────────────────────────┘
```

依赖方向必须从外向内：协议适配层依赖应用层，应用层依赖领域层。领域层不得导入 ACP、MCP、TUI 或具体模型 SDK。

## 6. 组件边界

### 6.1 Harness Domain

领域层定义稳定概念和状态转换，不处理传输、UI 或供应商 JSON。核心实体至少包括：

- `Session`：可持久化的工作上下文；
- `Turn`：一次用户目标到明确终止状态的执行；
- `Item`：消息、计划、工具调用、审批和产物的统一流式单元；
- `ModelAttempt`：一次具体供应商请求及其用量、延迟和结果；
- `ToolExecution`：一次工具执行及权限、输入、输出和退出状态；
- `Approval`：有生命周期、超时和取消语义的人类决策请求；
- `Checkpoint`：恢复所需的持久化边界；
- `ContextSnapshot`：发送给模型的上下文构成及裁剪依据；
- `PolicyDecision`：允许、拒绝、要求审批或降级的结构化结论；
- `EvaluationResult`：评测结果、证据和归因。

领域状态只能通过显式 command 触发并产生 event。查询通过 event projection 或只读 view 完成。

### 6.2 Agent Loop

Agent loop 编排以下步骤：

1. 接收并验证 command；
2. 构建或恢复 turn；
3. 选择 Provider 与模型能力配置；
4. 构建 ContextSnapshot；
5. 发起 ModelAttempt；
6. 将模型输出解析为消息或工具意图；
7. 经 Policy Engine 决定工具是否执行、拒绝或请求审批；
8. 把工具结果加入事件流并决定继续、完成、失败或中断；
9. 在定义的边界写入 checkpoint 和最终指标。

Loop 不直接渲染 UI，不直接打印协议消息，不直接依赖供应商 SDK。

### 6.3 Model Runtime

Provider Adapter 负责请求映射、流式响应、错误归一化、token usage 和取消。Capability Profile 描述模型是否支持原生工具调用、图像、结构化输出、reasoning 字段、prompt cache 和上下文限制。

模型差异必须通过能力与策略组合处理。禁止在通用 loop 中出现按供应商名称分支的业务逻辑。

### 6.4 Tool Runtime 与 Policy Engine

内置工具和 MCP 工具统一转换为内部 `ToolSpec`。每次执行都经过：

```text
schema validation
  → workspace scope check
  → policy decision
  → optional approval
  → sandboxed execution
  → output normalization
  → audit event
```

Policy Engine 独立于工具实现。默认策略对写文件、执行 shell、访问 workspace 外路径和网络等风险行为采取最小权限原则。

### 6.5 Context Engine

Context Engine 负责选择、排序、预算和压缩上下文，并为每个决定留下可解释证据。它不只是字符串拼接器。上下文算法必须能使用固定 fixture 独立评测，不调用真实 TUI。

### 6.6 Persistence

持久化采用追加式事件记录和可重建投影。Checkpoint 是性能优化和恢复边界，不取代事件事实。

v0 的恢复承诺是“进程重启后恢复已持久化 session，并把未完成 turn 标记为明确状态”，不是“stdio 子进程在父进程退出后继续运行”。需要实时脱离客户端运行时，再增加 daemon 与 socket/WebSocket transport。

### 6.7 ACP Adapter

ACP Adapter 是公开客户端协议边界，负责：

- ACP v1 初始化、版本和 capability negotiation；
- session 新建、恢复、列出、关闭和删除；
- prompt、cancel 和 request cancellation；
- 把领域事件投影为 `session/update`；
- 权限请求、计划、工具状态和 usage update；
- JSON-RPC 错误与领域错误的稳定映射。

ACP 官方 schema 是此公开边界的规范真源。v0 不把 ACP v2 Draft 作为兼容承诺。Harness 特有扩展必须使用 ACP 约定的扩展机制和项目命名空间，不能修改标准方法的语义。

### 6.8 TypeScript TUI

TUI 是 ACP Client，只负责交互、渲染、输入、审批和客户端能力。它不得拥有 Agent loop、Provider 调用、持久化事实或绕过 Policy Engine 的工具执行逻辑。

TUI 可以独立发布。协议类型来自 ACP schema/SDK 和 Harness 扩展生成物，不手写重复类型。

### 6.9 Evaluation

Eval Runner 直接驱动应用层或 headless Engine，不依赖 TUI。ACP 测试作为独立黑盒 conformance 层存在。

评测至少分为：

- 领域状态机单元测试；
- Provider contract tests；
- Tool/Policy 安全测试；
- Context fixture 和检索/压缩评测；
- 确定性事件 replay；
- ACP/MCP conformance；
- 中断、重启和恢复测试；
- 真实仓库 scenario eval；
- 质量、成本、延迟和稳定性回归基线。

## 7. 协议与类型真源

不同边界使用不同的规范真源：

| 边界 | 规范真源 | 本项目责任 |
|---|---|---|
| ACP 公开协议 | ACP 官方 schema | 实现、适配和 conformance |
| MCP 公开协议 | MCP 官方 schema/SDK | client adapter 和安全管线 |
| Harness 内部领域 | Go 类型与状态规则 | 生成 JSON Schema、fixture 和 TS 投影类型 |
| Harness ACP 扩展 | Go wire types | 生成 schema、TS 类型与兼容性测试 |

生成物可以提交到仓库，但禁止手工修改。CI 必须检测生成物漂移和破坏性协议变更。

## 8. 生命周期、错误和背压

### 8.1 生命周期

Session、Turn、Item、ModelAttempt、ToolExecution 和 Approval 都必须有显式状态机。流式对象遵循 `started → delta* → completed|failed|interrupted`，不允许只停止输出而没有终止事件。

### 8.2 错误分类

错误至少归一化为：

- protocol validation；
- incompatible capability/version；
- provider authentication/quota/rate limit；
- provider transient/permanent failure；
- tool validation/policy denial/execution failure；
- context budget/compaction failure；
- persistence/recovery failure；
- user cancellation；
- internal invariant violation。

每类错误必须声明是否可重试、由谁重试、是否计入预算、最终映射为何种 turn 状态。

### 8.3 背压

所有流式通道使用有界队列。高频 delta 可以合并，但状态转换、审批和终止事件不得丢弃。队列过载必须产生可观测错误或降级，不允许无限占用内存。stdout 仅承载协议消息，日志写入 stderr 或结构化日志后端。

## 9. 安全与隐私基线

- workspace root 和附加目录必须显式声明；
- 路径规范化后再做 scope 检查；
- shell、文件变更、网络和敏感操作采用策略驱动审批；
- 工具输入输出、模型内容和 trace 可能含有秘密，默认不上传原文；
- OpenTelemetry 内容型属性默认关闭或脱敏；
- 每个执行动作保留关联 ID 和审计事件；
- 恢复和重试不得静默重复高风险副作用；
- 插件不能仅因安装就绕过 Engine 权限模型。

## 10. 开源与社区架构

### 10.1 API 稳定级别

- `stable`：遵守 SemVer、迁移和弃用政策；
- `experimental`：允许真实使用，但不承诺跨版本兼容；
- `internal`：不构成第三方依赖契约。

所有公共配置、接口、事件和扩展点必须标注稳定级别。

### 10.2 社区扩展面

首批合理扩展面是 Provider、MCP server 配置、Policy、Evaluator 和 Observer。v0 不采用 Go 原生动态插件作为通用扩展机制，因为 Go plugin ABI、进程隔离和安全升级会给跨平台社区带来不必要约束。需要跨进程扩展时优先使用版本化协议。

### 10.3 治理原则

公开发布前建立贡献指南、行为准则、安全政策、架构决策记录、RFC/RFD 流程、兼容矩阵、发布说明和弃用政策。项目应提供小型参考实现、协议 fixture 和 conformance 命令，使外部贡献者无需理解全部内部代码即可验证贡献。

许可证和 DCO/CLA 的法律选择在首次公开发布准备规格中决定，不属于本基础架构文档的技术承诺。

## 11. 版本与兼容策略

- 项目版本遵循 SemVer；
- ACP 与 MCP 版本独立记录，不与项目版本混用；
- 初始化交换 implementation info、协议版本和 capability；
- 未协商的 capability 不得调用；
- 破坏性内部事件变更必须有迁移或明确重建策略；
- 公开扩展采用命名空间，禁止占用上游标准字段；
- v0 允许快速演进，但所有公开实验接口仍需有 fixture 和变更记录。

## 12. 参考标准与项目

- ACP Introduction: <https://agentclientprotocol.com/get-started/introduction>
- ACP v1 Overview: <https://agentclientprotocol.com/protocol/v1/overview>
- MCP Architecture: <https://modelcontextprotocol.io/docs/learn/architecture>
- OpenTelemetry Semantic Conventions: <https://opentelemetry.io/docs/specs/semconv/>
- OpenTelemetry GenAI Attributes: <https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/>
- OpenAI Codex app-server: <https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>
- DeepSeek-Reasonix: <https://github.com/esengine/DeepSeek-Reasonix>
- Kimi Code ACP integration: <https://www.kimi.com/help/kimi-code/cli-ides>
- A2A Specification: <https://github.com/a2aproject/A2A/blob/main/docs/specification.md>

参考项目用于比较设计和形成评测，不构成本项目的代码继承关系。

## 13. 后续规格拆分

本文件是项目章程和基础架构共识，不是一次性实现全部系统的计划。后续按以下顺序分别完成设计、书面复核和实施计划：

1. Harness domain、事件模型与 session/turn 状态机；
2. Go Engine 最小可执行纵切与确定性 replay；
3. Provider contract 与首个模型适配；
4. Tool Runtime、Policy 与最小 workspace tools；
5. ACP v1 adapter 与 conformance；
6. TypeScript TUI 最小客户端；
7. Context Engine、checkpoint 和恢复；
8. MCP client adapter；
9. Scenario eval、基准与 OpenTelemetry；
10. 开源发布、治理与生态文档。

每个子项目都必须形成独立 spec 和实施计划。基础架构不以一次“大爆炸”实现交付。

## 14. 接受标准

本基础架构设计在以下条件下被认为落实：

- 仓库中存在并版本化保存本设计；
- 后续实现计划遵守分层与依赖方向；
- ACP v1 是 v0 的公开客户端协议；
- Engine 的核心测试不依赖 TUI；
- 领域状态、协议兼容、恢复和安全策略均有独立验证入口；
- 新增公共扩展点时同时提供版本、schema、fixture 和兼容性测试；
- 对上述基础决策的修改通过 ADR 或后续设计文档明确记录。
