# Provider 协议状态回放：DeepSeek 首个切片

状态：已实现内部切片，experimental，非 GA。日期：2026-09-13。
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

`ProviderState` 仅含 `protocol`、`modelID`、`endpointID`、`reasoningContent`。
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
没有调用付费或线上 DeepSeek；fixture 只证明 harness 行为，不证明远端当前接受性、
tokenizer、延迟或模型质量。

下一刀是 Claude native Messages，保留它自己的原始 block、signature 关联和各模型历史约束，
不能硬塞进 `reasoningContent`。[官方依据](https://platform.claude.com/docs/en/about-claude/models/extended-thinking-models)。
两个真实协议实现后再确定共享 replay envelope、考虑公开 Provider SDK。
转 stable 仍需要真实外部消费者，仓库自写 example 不算。
