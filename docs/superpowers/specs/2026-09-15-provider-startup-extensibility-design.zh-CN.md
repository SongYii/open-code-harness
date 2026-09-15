# Provider 启动时可插拔：合同草案与架构评审

- 状态：内部步骤 1–2 已获准实施；公开 SDK 部分仍是草案
- 日期：2026-09-15
- 代码基线：`bd762556b9039996aa2059207397c66d6d6fa0b6`
- 范围：启动时选定、可信的 Go Provider 实现
- 英文源：[Provider Startup Extensibility](2026-09-15-provider-startup-extensibility-design.md)；有歧义时以英文源为准，公开 SDK 部分仍未获批
- 既有合同：[启动扩展](../../architecture/startup-extensibility.md)、[协议回放](../../architecture/provider-replay.md)、[Provider adapter](../../architecture/provider-adapter.md)

## 1. 结论与当前状态

用户于 2026-09-15 授权实施内部步骤 1–2，见[内部收口计划](../plans/2026-09-15-provider-internal-closure.md)。
内部步骤现已实现并通过本地验证，见[工作区证据](../../architecture/provider-internal-closure-evidence.md)。
这不代表公开 API 获批或冻结。现有 builtin 构造不执行网络 I/O，实际资源是私有
HTTP transport；本切片无需异步 factory 或新增启动超时。模型级关闭在流排空后
执行，只关闭自有 transport；注入的非标准 transport 仍归调用方。

建议让“如何调用模型”可替换，但不替换 agent 本身。请求准备、工具执行、压缩、
溢出恢复、事件提交和崩溃恢复继续由现有核心负责。只加 factory 注册表不够：
真正缺的是回放身份、资源归属，以及不同实现共用的验收合同。

先让两个现有 adapter 通过共同的语义测试，再用真实实现补齐内部资源生命周期。
不要为了抽象图完整直接发布 `sdk/provider`。实现新的公开扩展点之前，先明确
一个真实使用项目及其接入需求。仓库自写 example 是编译门禁，不是独立采用证据。
这遵循[宪章的非目标](2026-08-11-open-code-harness-architecture-design.md#4-非目标)。

目前 `sdk/och.Extensions` 只开放 context-policy 注册；DeepSeek Chat Completions
和 Messages 是内部实现，不是公开 Provider 插件。本草案不改变这些状态。
不做原生 Claude 回放、热切换、第二套工具循环、替代摘要器、存储/执行环境扩展，
也不修改 OTel。

## 2. 从代码查到的关键问题

以下是开放扩展边界前要处理的问题，不代表现有固定 adapter 部署都有生产缺陷。

| 发现 | 代码依据 | 设计要求 |
| --- | --- | --- |
| 核心已有足够窄的模型端口 | [engine/model.go](../../../internal/harness/engine/model.go) 的 `Model` / `ModelStream` | 保留一套请求/流端口，不公开 Application / Domain |
| 实现身份不能直接替换回放协议身份 | [profile.go](../../../internal/harness/engine/profile.go) 的 `RequestIdentity`；[loop.go](../../../internal/harness/application/loop.go) 的 `acceptProviderState` 核对协议、模型、端点 | 注册 ID 与 `AdapterFamily` 分离；公开切片获批后才增加独立归因 |
| 回放状态是核心可理解的有类型值 | [provider_state.go](../../../internal/harness/domain/provider_state.go) 的校验与拷贝 | 首阶段仅支持已有协议；不能开放任意 JSON 状态袋 |
| Composition 尚无通用 Provider 资源归属 | [assembly.go](../../../internal/harness/composition/assembly.go) 的构造、失败清理与 `Close` | 自定义客户端必须有构造回滚、排空后关闭和无法证明回收的处理 |
| 标称传输中立的测试固定了分块数量 | [modeltest/contract.go](../../../internal/harness/engine/modeltest/contract.go) 精确要求两个文本块和第三个完成事件 | 提取语义断言，不要求所有协议按同样方式分块 |
| Messages 有意整体验证后才暴露输出 | [anthropic/stream.go](../../../internal/harness/adapters/anthropic/stream.go) 的 `Next` | 不能为通过共同测试而提前放行工具或伪造分块 |
| 对话与压缩共用一个 model/runner | [assembly.go](../../../internal/harness/composition/assembly.go) 的 summarizer 构造 | 在 Composition 注入，避免两条路径各选一套 Provider |
| Eval 的 Provider 选择目前是封闭集合 | [eval/model.go](../../../internal/harness/eval/model.go) 与 [acp_argv.go](../../../internal/harness/eval/acp_argv.go) | 同步冻结评测身份并拒绝不支持的路径，不能只加运行参数 |

现有架构的可利用优势是：模型端口已经与持久化编排分离。接下来要证明外部实现
进入后，这个分离仍然成立。这轮代码评审不证明优于其他项目、性能提升，或新的
线上 Provider 认证。

## 3. 信任与权限

Provider 必须读取模型可见消息、工具定义/结果、回放所需的私有协议状态，并接收
当前路线的凭证。它与 context policy 不同：**不是只接触元数据的插件**，而是
必须信任、会处理正文的代码。

API 不传 Service、EventStore、事件指针、工作区文件系统、审批回调、工具执行器
或租约。请求转成脱离的值，接收输出再复制进核心；不公开 `internal/` 别名或
第三方 SDK 类型。嵌套切片、工具参数字节、可选状态块必须深拷贝，并保留有语义
的 nil/空值差异。

这是 API 与所有权隔离，不是进程内安全沙箱。Go 插件仍能自行使用 OS/网络；若它
在返回后与核心并发修改同一值，深拷贝也不能保证安全。合同须禁止返回后继续修改，
要求并发请求状态独立。隔离恶意代码需要另行设计进程边界。

## 4. 建议的边界

### 4.1 把三种身份分开

1. 实现注册身份：命名空间 ID、实现版本、规范化非敏感配置摘要，回答“选了哪份
   实现”。评测另外用可执行文件 hash 固定产物；版本字符串本身不能固定代码。
2. 路线描述：模型、规范化非敏感端点、能力档位、显式请求控制，回答“请求什么”。
3. 回放协议：核心认识的协议/版本，回答“保留的状态如何校验和发回”。

首阶段保留已有 family 的值与语义：

| 当前配置选择 | `AdapterFamily` / 建议采用的回放档位 | 状态合同 |
| --- | --- | --- |
| 空或 `openaicompat` | `openai_compat` | 此路线不使用私有 Provider 状态 |
| `deepseek` | `deepseek_thinking_v1` | 有类型、绑定路线的 reasoning 状态 |
| `deepseek-messages` | `deepseek_messages_v1` | 有类型、有顺序、绑定路线的 Messages blocks |

外部实现可以实现这些已有合同。注册新名字不等于发明新回放协议。不支持任意 JSON
状态、插件自行提供状态校验器，也不声称所有 Anthropic 端点都兼容。原生 Claude
的前缀/签名约束仍须通过[独立门槛](../../research/architecture-gates/2026-09-14-claude-native-replay.md)。

开始接单前一次性校验并冻结描述，不能每次向可变插件重新索取能力。上下文/输出
上限必须能生成有效核心预算，并支持当前工具目录。不认识或不支持的控制项启动
即拒绝，不能静默丢弃。

更换注册实现不等于获得迁移旧状态的权限。协议/模型/端点不匹配须拒绝；即使描述
完全一致，也不能据此证明跨实现回放等价。明确验证所需的切换，或新建 session，
不能自动删掉不兼容状态。

### 4.2 候选 API 职责，不提前冻结 Go 签名

具体命名和配置 schema 等真实消费者明确后再评审；以下不是已承诺发布的类型。

| 接口面 | 允许的职责 | 不给予的权限 |
| --- | --- | --- |
| 注册/描述校验 | 纯启动选择、版本/配置校验、冻结描述 | 网络访问、全局注册、发现或运行中更换 |
| Factory `Open` | 用有界启动 context 和当前凭证构造模型资源 | Store/租约访问、执行 turn、另起路由循环 |
| Provider `Stream` | 用脱离的请求开始一次调用；支持独立并发请求 | 执行工具、压缩、SDK 自主重试 |
| Stream `Next` / `Close` | 单一所有者消费事件、关闭本次 I/O | 关闭后继续发布、未受管理的解码 worker |
| Provider `Close(ctx)` | 排空后关闭 Provider 级资源 | 释放租约、关闭 store |

公开 DTO 对应已有语义字段：关联 ID、输入/消息、工具定义、归因用途、显式输出
上限和 reasoning effort；私有回放仅能表达已支持的类型。SDK 保持仅依赖标准库，
Composition 转换为 engine/domain 值，不用通用 options map 搭第二套无校验 API。

非敏感配置必须有界、有 schema、可规范化。保留 builtin ID，资源打开前拒绝重复
注册与版本不匹配，复制每次启动的注册列表。凭证值不进入配置摘要、argv、事件或
诊断。首阶段复用选定的凭证环境变量机制，不发明通用 secret-store 回调；若实际
消费者需要轮换凭证或其他认证方式，先评审需求，再冻结 factory 签名。

### 4.3 请求、完成与错误

- `Purpose` 只做归因。正文差异必须来自显式请求字段，不能暗中按对话/压缩分叉。
- 保留 `text_delta* tool_call* completed`。验收拼接后文本及完整工具调用的顺序，
  不验收固定 chunk 数。Usage 与私有状态只在成功完成时出现；工具 ID、参数、输出、
  状态块、token 算术继续受核心上限约束。
- 实时文本不等于持久化完成。核心校验 completion 且流关闭成功，才提交成功或
  执行工具；无效输出不能产生工具副作用。
- `Next` / `Close` 维持单一所有者，`Stream` 与消费共用请求 context。取消必须解除
  I/O 阻塞；核心不能强停任意 Go 函数，超时包装 goroutine 不代表回收完成。
- 公开分类错误映射为现有有界词表；启动/流阶段由失败发生的调用边界决定，不接受
  插件自称的阶段。未知错误用固定安全诊断，不持久化 SDK 原始错误、响应正文或
  任意 `SafeMessage`；状态码、错误码、请求元数据均经有界校验。
- 禁用 SDK 重试。既有核心拥有重试与有界启动 `context_overflow` 压缩路径；晚到
  的流错误不能伪装成启动溢出。Provider 不得自行截断、摘要、改写状态或修改提示后重试。
- 若开放 attempt stats，复制终态快照，区分未知 usage 与零；与 completed usage
  同时存在时要求一致。失败统计不能伪造成功 usage 事实。

### 4.4 生命周期与无法证明回收

建议顺序：

```text
纯校验/冻结注册与描述（无资源）
  → 获取既有 Host 执行权
  → 打开 Provider → 构造 runner、summarizer 与其他叶子资源
  → 暴露受管 Service
  → 停止准入 / 取消 / 排空（保留执行权以写终态）
  → 关闭叶子资源，包括 Provider
  → 停 Host / 匹配释放租约 / 关 store → 既有 telemetry 关闭
```

`Open` 成功后资源归 Assembly；此后任何构造失败都必须回收它。`Open` 失败时，
factory 负责清理其部分构造，不得留下活资源。桥接层拒绝 nil-success 与
resource-plus-error；后者返回的资源必须纳入失败清理，不能丢弃。

在公开 factory 前须明确：**部分构造无法证明清理完成如何上报**。它不能被当成
普通可重试启动错误，然后在 Provider 仍可能工作时释放所有权。此状态、卡住的
`Open`、Provider 关闭失败/超时，都进入既有 abandon/终止旧进程纪律：不能报告
干净退出、主动释放租约或原地重开。租约仍可能自然过期；监管器必须先终止旧进程
再启动后继。类型化错误无法识别撒谎的实现，这仍是可信代码的责任。

Provider 关闭只执行一次，放在排空之后、Host/store 释放之前，共用既有 shutdown
预算，不能每个资源重新获得完整超时。保持 MCP/localexec 次序，在叶子阶段追加
Provider 回收并补齐所有对应构造回滚路径。不改变 OTel 归属与属性。Builtin 只关闭
自己拥有的客户端，不能关闭全进程共享的 HTTP transport。

启动 context 只约束构造，不是返回对象的终身 context。每次调用使用核心请求
context。首阶段不允许后台模型调用或独立工作循环；闲置客户端资源仍由 Provider
拥有至关闭。启动超时取值与“无法证明清理”表达必须在生命周期设计获批时定清，
不能藏在 goroutine helper 里。

## 5. 持久化、兼容与评测

建议在 `model.request.recorded` 增加独立的可选实现归因：ID、版本、规范化
非敏感配置摘要。字段名/schema 尚未冻结。Domain 自有类型，不别名 SDK 类型；
clone/校验、严格 codec、transcript/export、评测证据一起同步。

Builtin/default 完全省略新值。用 golden 验证默认请求正文、事件 payload 字节、
审计 hash 不变；只有 `omitempty` 不能证明所有路径都未赋值。

新 reader 必须读旧历史；旧严格 reader 可能拒绝新增归因，即使 envelope 版本没变。
首次启用前做已验证备份。回滚需保留支持新字段的 reader，或恢复启用前备份并接受
后续工作丢失；不能剥字段、改历史或重算审计链。历史重放不加载、不执行扩展。

首个公开切片通过 Agent Client Protocol（ACP）评测自定义 launcher。Subject 冻结
注册/版本/规范化非敏感配置及摘要，保留模型/端点/档位，Executor 固定可执行文件
hash。Stock in-process 路径在另行支持前明确拒绝自定义选择，不增加第二套评测
注册表。对话和核心摘要共用被选 Provider，不自动扩展单独配置的 quality judge。

## 6. 验收与变异验证

共同语义、原生协议格式、恶意桥接输入分层测试；fixture 须能产生目标故障，不要求
两个 adapter 使用相同 wire frame 或 chunk 数。

| 门槛 | 必备证据 | 必须被拒绝的反例 |
| --- | --- | --- |
| 共同模型语义 | 两个真实 adapter 的完整 Unicode 文本、有序工具、终态、取消、独立并发 | 丢/重排文本或工具，截断输入后仍成功 |
| 原生协议 | 各自格式错误、缺终止标记、状态投影、usage 合计、资源上限 | Messages 解码未完成就暴露工具，cached token 加法溢出 |
| 值/返回形状边界 | 嵌套请求/结果脱离；nil-success、stream-plus-error 与清理计数 | 去掉深拷贝再修改同一切片，无效返回对的资源未回收 |
| 阶段与安全错误 | 启动/流/关闭故障、固定诊断、取消、有界溢出 | 晚到错误触发溢出重试，原始错误把植入凭证带入输出 |
| 生命周期 | 每个构造点之后失败、活跃调用中 Close、阻塞 I/O、关闭失败/超时、fencing、并发 Close | Provider 未回收就释放租约，有活任务仍报告关闭完成 |
| 回放与压缩 | 重启后工具续接、摘要使用选定实现、旧 checkpoint 后摘要失败仍保留历史 | 替换协议/端点、静默丢私有状态或 checkpoint 后历史 |
| 默认兼容 | 默认请求/事件/hash golden，旧 fixture，新归因严格往返 | 默认意外写归因，接收未知/无效持久字段 |
| 消费者与评测 | 真实外部接入、独立模块编译/ACP、可复现 Subject/二进制身份 | 未注册选择被接收，自定义 Subject 静默退回 builtin in-process |

每条安全承诺都应删除对应防线或直接破坏目标不变量，观察目标测试变红。变异仍绿
不算证明，须查是否被更早的其他校验挡住。编译 example 只证明包边界，不证明
消费者适配、安全 I/O 或恶意代码隔离。

## 7. 建议实施顺序

这是供评审的顺序，**不是已获批公开 API 的实施计划**。每个获批切片还需要具体
任务、实现合同、证据及双语通俗导读。

1. **内部语义合同。** 修改 `engine/modeltest` 和两个 adapter 的测试，提取不依赖
   chunk 的断言，用本地 fixture 跑真实实现并保留各自严格协议测试。验收共同
   成功/取消/并发与原生拒绝案例；不新增 SDK、参数、wire 或持久化 schema。
2. **内部 Provider 资源归属。** 在 Composition 和现有 adapter 中明确真实客户端
   所有权、启动界限、清理失败，补齐排空后关闭及构造回滚；不做通用注册框架。
   验收生命周期故障矩阵、变异证据，保持默认运行语义。
3. **消费者检查点。** 记录外部项目负责人/仓库或明确的私有接入目标、协议档位、
   认证/配置/生命周期及可运行验收场景。如果只是换兼容 URL，就用已有配置；如果
   需要新回放协议，先过独立前置门槛。没有这些证据就停在有实际收益的内部切片。
4. **接受并实现最小公开纵切。** 同时冻结 DTO、选择/配置、清理失败表达与持久化
   归因。候选改动在 `sdk/provider`、`sdk/och`、launcher/Composition 桥接、
   Domain/codec/transcript、eval 身份；同步架构归属/import 门禁，不公开内部或
   vendor 别名。选择、转换、生命周期、重启/压缩、ACP 证据一起交付，不能只交付
   一个无法完成可恢复 turn 的注册表。
5. **独立接入与稳定性评审。** 跑真实消费者场景、收集 API/兼容反馈、发布锁版本
   接入说明。公开面仍标 experimental、不承诺源码兼容。升 stable 仍要求两个真实
   实现和一个真实外部消费者，不能用自写 example 或 fixture 凑采用证据。

步骤 1–2 不需要付费模型调用。后续线上验证需明确调用/费用预算且限定结论；旧的
DeepSeek fixture/线上证据不能自动认证一个新实现。

## 8. 尚待决定

内部步骤 1–2 完成后，下一步是消费者检查点，不是自动发布 SDK。需要明确：**哪个外部项目
要提供自己的模型实现，它有什么需求是已有 endpoint 参数做不到的？** 这个答案
决定是否真需要扩展点。

消费者需求、启动截止时间/部分清理合同、精确非敏感配置 schema、持久化归因
schema 均评审完成后，才适合冻结公开 API。本草案把未决事项明确留下，不把它们
包装成已实现能力。
