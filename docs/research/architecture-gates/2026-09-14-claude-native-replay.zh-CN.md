# Claude 原生回放：调研结论与接入门槛

日期：2026-09-14。英文[调研记录](2026-09-14-claude-native-replay.md)为准。
状态：调研完成；DeepSeek Messages experimental 路线已接通，**尚不是原生 Claude Provider**。
本文没有把待定的压缩方案写成已经批准或实现的合同。

后续决定：用户已选择 **DeepSeek 官方 Anthropic Messages 兼容接口**作为首个目标。
已完成[固定版本 SDK 本地验证](../../../experiments/anthropic-sdk/README.md)，随后在主模块
引入 v1.72.0，替换手写内容累积并接通 `deepseek-messages`。当前实现以
[回放合同](../../architecture/provider-replay.zh-CN.md)和[证据台账](../../architecture/provider-replay-evidence.md)
为准。下文原型限制与当时“尚未实现”的记录保留为历史调研证据；当前已实现独立状态、
严格投影、原生请求、HTTP/取消边界、CLI/eval 及本地工具/重启/压缩/audit 验证。
首轮 12 次受限 DeepSeek Messages 调用只取得部分验收证据；9 月 15 日经授权追加 8 次，
非空带签名助手尾部跨压缩、重开后的精确回传和审计门禁通过。两轮合计 20 次仍在原总
预算内；首轮未捕获的流失败仍未解释，不能宣称通用可靠性认证。详见证据台账。
原生 Claude 前缀保持和公开 SDK 门槛仍未完成。

## 已确定的目标与复用来源

Base URL 使用 `https://api.deepseek.com/anthropic`，初始模型候选采用官方示例中的
`deepseek-flash`，不使用会被服务器映射的 Claude 别名。兼容表明确了部分功能缺省/忽略，
但没有证明其 signature 必须非空，也没有证明它遵循 Claude 的历史前缀绑定；不能把原型的
签名限制和 Claude 的上下文约束直接套过去。
[DeepSeek 官方兼容说明](https://api-docs.deepseek.com/guides/anthropic_api/)。

已经查到实际代码，不只是调用示例：

- [Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go/blob/main/messageutil.go)：
  提供 Message.Accumulate、签名组装和 ToParam；优先验证能否复用。
- [Fantasy 的 Anthropic 适配器](https://github.com/charmbracelet/fantasy/blob/main/providers/anthropic/anthropic.go)：
  复用官方 SDK，处理 reasoning 元数据与工具结果映射；
  [Crush 的依赖文件](https://github.com/charmbracelet/crush/blob/main/go.mod)确认存在实际应用消费者。
- [Vercel AI SDK 的转换器](https://github.com/vercel/ai/blob/main/packages/anthropic/src/convert-to-anthropic-prompt.ts)：
  提供另一套 thinking/signature 回传实现，可参考规则和测试，但不为 Go 项目引入 TypeScript 运行时。

许可证分别检查为官方 SDK 的 MIT、Fantasy 和 Vercel AI SDK 的 Apache-2.0，链接见英文记录。
没有复制第三方代码或增加生产依赖；验证专用依赖固定在独立清单中。最初源码核查来自 main；
随后已下载、检查并编译测试 v1.72.0，commit 为 `03d7ef5861e6db02581610bf60bf0abe57bcb52a`。

源码核对也暴露了原型限制：官方 SDK 支持多个未结束 block 按 index 接收后续事件，原型
只有一个 active block。我用临时本地正例验证，确实被原型拒绝；诊断文件已移除。
这不是线上 DeepSeek 流，说明的是原型覆盖范围比上游小。

另一方面，SDK 不是审计完整性校验器：所检查的 accumulator 会对截断工具输入做修复，
错误对象也可能保留原始 HTTP 内容。我们的完成判定、原始输入上限和无损检查、错误脱敏、
事务准入仍须自己保留，不能直接把 SDK 的返回结果当成可提交事实。

固定版本验证现已实现：复用现有正反例，确认多个 block 同时打开、两次请求间的签名/参数/
工具结果回传；测试配置关闭 SDK 自动重试、使用受控 HTTP client，并禁止环境凭据自动加载。
还实测到缺 message_stop 时可无错 EOF、307 可表现为空流、usage-only 事件会清空 SDK 对象的
结束原因。三个定向变异分别移除环境隔离、打开重试、允许重定向，均在对应断言失败并已恢复。
这些是 SDK 行为验证，不是生产防线已经落地。下一步替换重复的解析/组装，不维护两套生产解析器。
再接我们的持久化、工具循环、重启和压缩；不引入别人的完整 agent loop 来取得第二份历史。

## 结论

现有架构值得保留的是一份规范事件历史、Application 拥有工具循环、Host 统一管生命周期。
Claude 接入的困难不主要在工厂注册，而在于回放需要有序 block，以及部分模型对历史前缀
的绑定。不能把 Claude 的多个 thinking/signature 塞进 DeepSeek 的一个 reasoning 字符串。

本轮已经写出 `internal/harness/adapters/anthropic` 的内部响应解析器及测试：

- 保留 text、thinking、redacted_thinking、tool_use 原顺序；保留空 thinking 与签名。
- 组装工具参数分片，避免大整数经过 float64 丢精度；usage 累计值覆盖而非相加。
- 只有 block 全部闭合、结束原因匹配且收到 message_stop 才返回完整结果。
- 截断、重复 JSON 字段、非法 Unicode、错误事件、未知必要内容都返回固定错误和空结果。
- 有明确的内容、签名、参数、行、事件、总读取量、JSON 深度限制，不截断后继续回放。
- 展示文本不包含 thinking；普通文本如果会被既有 secret 检查改写，则拒绝而非生成两份
  不同的历史。签名是不可解释的协议材料，不当作普通文本改写。

这里的“无损”是保留字段值和 block 顺序，不保证 JSON 空白/键顺序，也不代表验证了远端
签名。仅通过内存 fixture；没有 HTTP、启动 flag、SQLite 回放、engine.Model 或线上验收。
该包只允许依赖 redact，不得借用另一个 Adapter 或获得存储能力；公开 Provider SDK 未增加。

## 调研发现的关键约束

官方文档说明：adaptive 可以完全不产生 thinking；omitted 模式可以仅有签名而无 thinking
delta；工具调用之间也可能继续 thinking。结束标记、分片和 usage 都必须按原生协议处理。
[流式协议](https://platform.claude.com/docs/en/build-with-claude/streaming)、
[Thinking](https://platform.claude.com/docs/en/build-with-claude/thinking)。

工具结果必须位于紧接 assistant 的 user 消息中，不能沿用 OpenAI 的 tool 角色；完整的
assistant block（含 redacted_thinking）要原样回传。
[工具结果格式](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls)、
[多轮工具工作流](https://platform.claude.com/docs/en/build-with-claude/thinking-tool-workflows)。

更重要的是，Fable 5.1 对 2026-08-31 起新建账户有前缀绑定要求，覆盖 system、tools 和
此前消息。这不是所有 Claude 模型的统一规则，但足以说明：**只保留尾部 block 原文，
却改掉它前面的历史，仍可能无法续跑**。
[官方 append-only 说明](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-fable-5-1#keep-the-conversation-history-append-only)。

## 接下来按什么顺序实现

1. 先验证并采用合适的官方 SDK 能力，再建立独立版本的 Messages 持久化状态，严格 codec、
   深拷贝、与可见文本/工具调用一致性、事务提交。
2. 原生请求映射及 HTTP 生命周期；校验模型/端点及真正发出的请求前缀，错误不能泄露正文。
3. 确定目标模型允许的历史变换，再接预算、剪裁和压缩，不能偷偷套用 DeepSeek 规则。
4. 接 composition、CLI、eval；验收真实工具循环、SQLite 重开、audit 重建、取消和失租。
5. 用户明确目标端点/模型及调用预算后，另行进行线上验收。
6. 两个协议都完整实现后才讨论公共 Provider SDK；转 stable 还需要真实外部消费者。

压缩有三种候选边界：严格 append-only 并在前缀变化时拒绝；完整 Turn 结束后建立不带
任何旧 block 的全历史摘要 checkpoint；或者另做 provider 原生上下文管理。第二种改变
保留尾部的语义，第三种增加新的远端状态，都不能当成普通接线自动启用。活跃工具链不允许
中途 reset；放不下就安全失败。前缀 digest 只能发现本地改写，不等于验证远端签名。

当前 response codec 尚不接受 server tools、图像、citation、fallback、input transformations
等额外块，也不把 refusal/max_tokens 等结束当作成功完成。它是明确的受限子集。

## 验证与需要的协助

英文记录列出了测试命令、边界正例、反例及最终变异证据。此次没有付费调用，没有读取密钥，
没有修改 OTel，也没有变更旧事件格式或启用路径。解析器完成不等于第二 Provider 已完成。

首个验收目标已确定为 DeepSeek 官方 Messages，模型候选为 deepseek-flash。
现在不需要发送 API key；未来密钥只从运行环境读取。外部消费者可以后续确定，不能把仓库
自己的 example 算作已经满足了公开 SDK 的真实需求门槛。
