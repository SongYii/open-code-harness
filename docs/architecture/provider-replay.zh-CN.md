# Provider 协议状态回放：DeepSeek Chat Completions 与 Messages

状态：已实现内部路线，experimental，非 GA。更新：2026-09-14。
英文[合同](provider-replay.md)是规范正文；本文是同一合同的中文阅读版。
这是 [Provider adapter](provider-adapter.md) 与[启动扩展架构](startup-extensibility.zh-CN.md)
的增量，不是公开 Provider SDK。

## 为什么先做这一刀

真正缺失的不是注册表，而是消息结构丢弃了厂商继续工具回合所需的状态。
DeepSeek 当前合同要求：请求带 tools 时，回传此前所有 assistant 消息的
`reasoning_content`，包括没有调用工具的回复；不带 tools 时，服务端忽略该历史字段。
本切片服务这个具体需求，不预先抽象任意 reasoning block。
[官方依据，核对于 2026-09-13](https://api-docs.deepseek.com/guides/thinking_mode/)。

我们保留的结构优势是：一份规范历史、一条 Application 工具循环、一个宿主生命周期。
Adapter 只翻译历史，不保存第二份 transcript，也不获得 EventStore 写权限。
一个协议接通，还不能证明通用插件架构已经成立。

## 显式启用

CLI 使用 `-provider-adapter deepseek`；composition 和 eval 的 `AdapterKind`/
`adapterKind` 使用 `deepseek`。空值或 `openaicompat` 保留旧路径，不根据 URL 或模型名猜测。
已有 endpoint、model、credential-env、workspace、database 和预算参数照常配置，
模型限制必须按实际型号填写。不要在 argv 中放密钥；先备份并使用新会话。

该路径明确发送 `thinking.type=enabled`，记录 family `deepseek_thinking_v1`，
并声明支持 reasoning fields。可显式给 `-provider-thinking-mode enabled`。
支持的 effort 是空值（服务端默认）、`low`、`high`、`max`；对话与摘要可分别设置。
本切片不接受 `none` 或在会话中悄悄关闭 thinking，也不猜测其他 effort 别名的映射。
Eval digest 绑定 adapter 选择；ACP argv 与 in-process 配置一致。
默认路线的 argv 和无新状态时的事件字节不变。

## 边界与数据流

1. Adapter 独立拼接 reasoning delta，不拼进正文，不维护私有历史。
2. Engine 只允许成功的 `completed` 携带 `ProviderState`，校验并复制。
   失败、取消或 Close 失败不返回协议状态；没有 reasoning runtime event。
3. Application 核对 protocol/model/endpoint，执行 secret-shape 拒绝检查，
   与 `AssistantMessageCompleted` 原子提交；提交成功后才执行工具。
4. Domain 的完成事件与请求消息增加可选的类型化字段；严格 codec、深拷贝及原有
   EventStore 事务/audit 机制继续承重。
5. Context Engine 保留尾部 assistant 的状态，并计入预算；覆盖历史时按源事件一起覆盖。
   摘要渲染不读取 reasoning。
6. 下次带 tools 的请求回传每条保留 assistant 的 reasoning。缺失、未知版本、
   不同模型/endpoint 的状态在 HTTP 前拒绝；不伪造空 reasoning，也不剥字段切换路线。

Chat Completions 路线的 `ProviderState` 仅含 `protocol`、`modelID`、`endpointID`、`reasoningContent`。
没有任意 JSON bag、事件指针、执行能力或新公开 SDK 类型。路由绑定不发送给模型。
明确返回的空 reasoning 有合法表示；字段缺失不等于空字符串。

每条 assistant reasoning 上限 256 KiB UTF-8，与可见正文限制独立；超限失败，不截断。
原有请求和投影上限仍生效。新路径还限制单个 SSE event 为 1 MiB / 1,024 条 data 行，
并保留 scanner 单行上限。孤立 Unicode surrogate 拒绝而非自动替换。
成功必须有 reasoning、`stop`/`tool_calls` 和 `[DONE]`；length 截断、缺失终止标记、
message snapshot 与 delta 混合都不能发布状态。旧 SSE 路径不受这些新增限制影响。

## 压缩、恢复与敏感信息

SQLite 关闭重开后从已提交事件恢复状态。现有 planner 按完整 Turn 切割，不拆开活跃工具链。
滚动摘要/reset 只替换已覆盖前缀，保留的 assistant 状态不变；摘要/reset 是 user 消息，
不是缺少 reasoning 的伪 assistant。源摘要链涵盖完成事件的规范字节，也就涵盖协议状态。
压缩与 checkpoint 校验都不改写旧规范事件。

`och_wire_estimate_v1` 对旧消息保持旧算式；新形状增加 8 个 framing token，加上
ceil(reasoning UTF-8 字节数 / 3)。绑定元数据不是模型输入。裸消息估算在不带 tools 时
仍会保守多算，不是精确 tokenizer；实际用量仍以 provider usage 为证据。
摘要仅渲染普通消息正文和已有 framing，不混入协议字段。

不可见不等于不敏感。SQLite 规范事件、请求记录、备份与规范 audit 副本会按现有存储
保护方式明文保存 reasoning；本切片不提供加密或通用 secret 检测。
ACP、runtime 正文、展示 transcript、metrics 和 trace attributes 不应暴露它。
所以展示 transcript 不是回放备份。本切片未修改 OTel 实现。

`redact.Text` 在这里用作拒绝检查：若现有形状规则会改动 reasoning，则完成失败，
不保存改写后的协议内容。Adapter 和 Application 都执行检查，但这不保证识别任意密钥
或敏感文本。原有可见正文、工具结果脱敏不变。

## 兼容与回滚

事件外层 schema 仍为 1，增加严格校验的可选 `providerState`。nil 时整个字段省略，
原有 golden codec 测试继续约束旧字节。新 reader 能读两种形状；旧严格 reader 不能读
包含新字段的事件。独立的协议版本是 `deepseek_thinking_v1`；experimental 不免除审计完整性。

首次启用前制作并验证包含规范数据库/audit 恢复材料的备份。回滚旧程序须恢复启用前备份
（会失去后续工作），或使用理解新格式的 reader。改回 adapter flag 不能迁移已写入的历史。
不允许剥字段、重写审计链或删除状态来欺骗旧 reader。已有缺失 reasoning 的 assistant 历史
不能无损升级，请新建会话；换模型、endpoint、协议也不自动迁移。本切片没有有损迁移流程。

## 验证与下一步

测试覆盖严格嵌套 codec、旧字节、省略/null/重复字段、未知版本、大小/角色限制、深拷贝、
仅完成事件携带状态、secret 拒绝、HTTP 前拒绝缺失/异源状态、分片/空 reasoning、
无工具调用的 assistant 回传，以及不完整流。

`TestDeepSeekThinkingToolsRestartAndCompaction` 经过真实 composition、SQLite、HTTP/SSE、
工作区工具、滚动摘要与两次关闭重开；压缩后逐项核对回传集合等于 checkpoint 覆盖之外的
已提交历史，同时断言摘要与展示导出隔离。最终命令结果见[证据台账](provider-replay-evidence.md)。
冷一致性 audit 导出经独立验证后，与 SQLite 规范历史逐事件比较，包含携带协议状态的请求。
2026-09-14 的受限 Messages 线上调用只取得部分验收证据；2026-09-15 经授权续测，
非空原始 assistant blocks 跨压缩、重开后逐字节回传、远端续接和独立审计门禁均通过。
这仍不是通用可靠性、tokenizer、延迟或模型质量认证；首轮一次未捕获响应的流失败原因
尚未确定，不能用本轮通过倒推其根因。Chat Completions 仍只有 fixture 证据。

下一步是独立审查 Claude 的模型级请求前缀约束，并在早先流失败复现时诊断。
公开 Provider SDK 仍延期；转 stable 需要真实外部消费者，仓库自写 example 不算。

Messages 还通过了 8 个本地在途生命周期场景：普通对话和手动摘要各覆盖正常完成、调用方
取消、宿主关闭与 SQLite 租约到期后的真实心跳失租。验证迟到输出隔离、请求取消、持久
终止/恢复及继任者租约保护；这不能替代持久化边界逐点杀进程验证。详见证据台账。

后续已补六个真实进程强杀边界及同切点放行对照，覆盖模型完成提交前、工具
执行前及副作用后、摘要 checkpoint 提交前后、规范提交后审计尚未导出。
自然租约过期后的恢复、原请求 ID 重试、重复重启和冷审计比对已通过 race。
实测同时修复工具 Item 被错误恢复为 assistant 中断，以及持久请求重建器
不识别 `context.prepared` / `process_crash` 的问题。工具结果未知时只记录
中断，不自动重做；这不承诺外部副作用 exactly-once。后续已增加恢复事务中
再次强杀，以及脚本模型下回合内压缩／overflow retry 的请求重建验证。进一步
补齐混合多会话恢复中途强杀，以及原生 Messages 回合内 checkpoint 提交前后强杀。
原生 Messages 现已支持下述严格限定的 HTTP 400 overflow 识别，复用核心有界压缩重试；
其他状态及不明确的错误正文仍按原有分类拒绝。新增三个强杀场景覆盖 overflow 摘要
提交前后、以及重试回答提交前。其他启动阶段、真实提供方 overflow 验收及导出发布
子步骤仍需独立验证。
完整范围和反向验证见[证据台账](provider-replay-evidence.md)。

## DeepSeek Messages 路线（2026-09-14）

使用 `-provider-adapter deepseek-messages`、
`-provider-url https://api.deepseek.com/anthropic`；实际请求追加 `/v1/messages`。
模型名、密钥环境变量、上下文/输出预算按实际配置，首次启用先验证备份并新建会话。
Composition、CLI、eval、ACP/in-process 使用同一显式选择；默认路线不变。

主模块固定 `anthropic-sdk-go v1.72.0`（MIT），复用请求类型、HTTP 执行和按 index 累积
内容；SDK 类型禁止越过 Adapter 边界。关闭 SDK 环境凭据、自动重试和重定向，不引入其
工具运行器或第二份历史。只接受 HTTP 200 + `text/event-stream` 作为模型流；除下述
HTTP 400 JSON 的有界 overflow 检查外，错误正文不读取、直接关闭，
带请求/响应对象的 SDK 错误被转换为固定安全分类。私有 HTTP transport 的 header/TLS 超时
分别为 30/10 秒，响应 header 上限 64 KiB；body 单次读取空闲超时 60 秒。取消会关闭响应，
Close 仅执行一次且失败不允许提交完成；没有后台 decoder goroutine。

### HTTP context overflow 识别（2026-09-15）

这是实验性路线的内部行为，不增加插件或 SDK API。只检查 HTTP 400 且 MIME 为
`application/json` 的响应，在 SSE 准入之前最多读 4 KiB 加一个越界检测字节，要求
在绝对 2 秒期限内读到 EOF。滴流不会重置期限；取消／超时关闭 body 且仅关闭一次。
读／关闭失败、超时、过大、截断、重复 JSON 键或损坏 Unicode 都放弃识别。与流式
响应相同，注入的 transport 必须支持 Close 解除 Read 阻塞。正文和正文中的 request ID
不进入错误、事件、运行时输出或遥测；只返回固定安全信息、HTTP 状态和稳定错误码。

采用封闭 JSON 错误对象：`type` 必须为 `invalid_request_error`，可选 `code` 只能为
null 或同值，可选 `param` 只能为 null。根 `type` 若存在必须为 `error`；可选
`request_id` 必须为字符串、随后丢弃；未知字段拒绝。消息必须完整匹配受支持的
prompt-too-long 短格式、其 tokens/maximum 数值格式，或 DeepSeek 最大上下文数值
诊断格式。数值必须是正 uint64 且确实超限；DeepSeek 的请求总量必须等于输入加输出，
输出本身还必须小于窗口。引用片段、任意后缀、泛化的 max_tokens 信息、臆造错误码
都不匹配。413 是字节限制，不是 token 诊断；413／422 不进入此解析器。

证据分级：[DeepSeek 兼容文档](https://api-docs.deepseek.com/guides/anthropic_api/)说明接口，
[Anthropic 错误文档](https://platform.claude.com/docs/en/api/errors)说明 JSON 结构和 413 含义，
[上下文文档](https://platform.claude.com/docs/en/build-with-claude/context-windows)说明 400 的
prompt-too-long 诊断。DeepSeek 数值变体来自其仓库中的[用户一手报告](https://github.com/deepseek-ai/DeepSeek-V3/issues/1102)，
不是官方稳定保证，也不是本轮真实接口实测。未来未知格式保守拒绝，不承诺识别所有现网错误。

识别后返回 `CodeModelStartup`／`context_overflow`，Adapter 的 `Retryable=false`，SDK
自动重试仍关闭。只有 Application 可强制压缩、要求请求估算至少缩小 10%、持久化新的
decision 和连续 attempt，再按 `MaxOverflowCompactionsPerTurn` 有界重试。摘要失败或
次数耗尽终止；HTTP 200 中的 SSE 错误保持 stream failure，不提升为 pre-delta overflow。
不动态调低模型窗口、不改历史事件，也不增加 Provider 自有重试／工具循环。

### 完成准入与回放完整性

原始 SSE 校验保留：单行 256 KiB、event 1 MiB / 1,024 data 行、总流 8 MiB；重复字段、
损坏 Unicode、不支持的 shape、错误生命周期和损坏工具参数在 SDK 修复之前拒绝。
正文/thinking/signature 由 SDK 累积；仅工具原始 JSON 分片独立缓冲，因为 SDK 会修复
损坏输入、也可能用 `{}` 哨兵重置合法分片。支持多个未结束 block 按 index 交错更新。
必须所有 block 结束、有 `end_turn`/`tool_use` 和 `message_stop`，且响应关闭成功后才向
Engine 输出。统计停止原因映射到已有 `stop`/`tool_calls`，不扩大旧统计事件词汇。

持久化使用独立版本 `deepseek_messages_v1` 和封闭的 `messagesContent` 类型：按序保存
`text`、`thinking`、`tool_use`。签名区分缺失与真实空串，不伪造；此路线拒绝
`redacted_thinking`。这是 DeepSeek 兼容 profile，不代表实现 Claude 的签名/前缀绑定。
[官方兼容表](https://api-docs.deepseek.com/guides/anthropic_api/)。
旧 `reasoningContent` 字段仍保留但此路线必须为空；旧 Chat Completions 字节不变，
也不能混入 `messagesContent`。

每次完成最多 256 块、总内容 1 MiB、thinking/签名合计 256 KiB、单工具输入 32 KiB、
JSON 深度 64；请求上限 5 MiB。超限失败，不截断。Domain 在命令、事件、Apply 和记录请求上
验证封闭变体、正文投影，以及工具 ID/名称/JSON 值投影；数字不经 float64 舍入。
数组、原始参数、签名指针均深拷贝。Engine 完成准入也检查投影；工具仍在 Application
提交完成事件之后才执行。

请求采用顶层 system、原序 assistant blocks、下一条 user 中成组的 tool results。
缺失/异源状态、投影不一致、工具结果无匹配调用、响应模型别名都 fail closed。
thinking 显式 enabled；effort 仅空/low/high/max，使用 `output_config.effort`。
DeepSeek 忽略 `budget_tokens`，所以不虚构预算。始终发送 `max_tokens`；拒绝仅用于
Chat Completions 的 include_usage/max_completion_tokens 提示。

保留原有完整 Turn 切割、摘要/reset 机制；预算增加 hidden thinking/签名与 block framing，
不重复计入正文/工具参数，不声称是原生 tokenizer。摘要和展示导出不包含 replay blocks；
规范 SQLite、请求记录与 audit 按现有保护方式明文保存这些敏感数据。secret-shape 命中时
拒绝而非改写，opaque 签名不解释也不脱敏；不提供通用密钥检测或加密。

上文回滚限制再次适用：旧 reader 不认识此版本，不能剥字段或重写审计链。已通过本地
工具、重启、压缩和审计 fixture，并有上述受限线上证据；没有证明通用线上可靠性/质量，
也没有完成原生 Claude
前缀保持策略或公开 Provider SDK 稳定性门槛。OTel 未改动。

线上测试还发现并修复输入用量低估：Messages 的普通输入、缓存读取、缓存创建分别计数，
适配器现在校验溢出后相加，形成核心要求的总输入；缓存读取仍是其子集。修复前的规范
审计事件不回写。最后一次实测为 1,648 + 768 = 2,416 输入 token，缓存子集 768。
