# Secret 脱敏设计（中文摘要）

**状态：** 已接受（2026-08-31）；本文是与英文规范同步的中文摘要，不是逐字翻译。两者若有分歧，以英文 [2026-08-31-secret-redaction-design.md](2026-08-31-secret-redaction-design.md) 为准。

调研门发现本项目自己 `openaicompat/classify.go` 的 `redactSecrets`（四个硬编码正则，只用在一条路径上）是最贴近的先例，而且六个参考项目里没有一个真正解决了 `SECURITY.md` 点名的那个缺口：工具调用结果和事件负载今天原封不动地流过领域事件、JSONL 审计和 ACP `session/update` 投影，中间没有任何扫描。

## 核心决策（六条，逐一对应调研门留下的开放问题）

1. **新建一个不依赖任何东西的包 `internal/harness/redact`，取代 `openaicompat` 里那份私有拷贝**——一份实现、一套测试，`application`、`openaicompat` 都能直接导入，不引入新的循环依赖风险（它自己除了标准库 `regexp` 什么都不导入）。
2. **只在一个地方、往上游、在字符串即将变成领域命令之前脱敏一次——不是在下游 N 个投影点各自补一遍。** `application/pipeline.go` 的 `completeToolAndContinue`/`failToolAndContinue`，和 `application/loop.go` 里构造 `domain.CompleteAssistantMessage`/`CompleteAssistantTurn` 的地方，各自在调用 `domain.Decide` 之前脱敏一次 `content`/`message`/`runResult.Text`。脱敏后的字符串会被持久化、审计、实时投影、重放投影——一个卡点保护住所有下游消费者，直接回应调研门自己指出的问题：这个 session 自己刚加的那个实时投影点，如果不这样设计，将来完全可能又冒出第三个、第四个投影点却忘了套脱敏。
3. **范围是工具调用结果/失败消息，以及助手消息的最终文本——不包括工具调用参数，也不包括实时 `model.text.delta` 流式分片。** 工具参数同时也是驱动真实工作区写入或 `exec` 执行的实际输入；在使用前脱敏这个值会把工具的真实效果搞坏（想象一下把 `write_file` 的 `content` 参数脱敏之后再写——文件本身就被写坏了）。只脱敏"展示副本"（线协议上的 `rawInput`、审计轨迹里的那份）而不动"执行副本"是一个合理的未来扩展，但那是一条独立、更难的第二条脱敏路径，这次不需要解决，因为 `SECURITY.md` 点名的本来就是结果，不是参数。实时 `model.text.delta` 是模型完整消息组装完成之前就到达的任意字节分片；一个 secret 完全可能横跨两个分片，而且今天没有任何累积缓冲区可供脱敏。这次只对流式结束后组装完的完整文本脱敏一次——持久化、审计、可重放的记录被完整脱敏；只有那条瞬时的、逐字符的实时流没有被扫描（这是一个明说的、接受下来的残余风险，不是悄悄放过的缺口）。
4. **检测方式是一个小的、形状明确的硬编码正则集——在本项目已有的基础上扩展，不是上一个基于熵的启发式。** 调研门已经证明六个参考项目里没有一个解决了这个问题；现在就上一个无边界的启发式，等于用一个已知、有界的漏报率（漏掉未知形状的 secret）去换一个无边界的误报率（误伤一个 coding agent 日常合法读写的 hex/base64/git SHA 内容）——对第一版来说是个更差的交易。
5. **脱敏在本项目已有的大小裁剪/截断逻辑之前跑，永远不会在之后。** 因为脱敏现在发生在上游、针对完整未截断的字符串（决策 2 的直接结果），调研门原本担心的那个失败模式——一个 secret 横跨*截断*边界、两侧都只匹配到正则的一半——根本不会发生：`toolTextContent` 的 16 KiB 裁剪、`MaxToolResultBytes` 这些截断逻辑现在都跑在这次新脱敏步骤的下游，没有改动。
6. **这次不给 Provider API key 配一个等价于 `RedactedString` 的 Go 类型。** Codex 和 Grok Build 收敛到的编译期构造时脱敏模式是真实存在、值得以后采纳的，但今天基于约定的保护（`APIKeySource.APIKey()` 只在发请求时才被调用，从不存储、从不记日志——每个调用点都直接核实过）到目前为止零事故；现在引入一个包装类型是一个独立、更小的改进,不是关闭 `SECURITY.md` 点名的那个真实缺口（工具结果和事件负载，不是 API key 本身，API key 从来就不是那个没被保护的面）所必需的。

## 范围之外（这次明确不做，不是漏做）

不脱敏工具调用参数（`rawInput`）；不脱敏实时 `model.text.delta` 流式分片；不做基于熵或机器学习的检测；不给 Provider API key 配 `RedactedString` 式的类型；不新增任何领域概念、Policy 规则、Risk 分类或 ACP 线协议字段——这次改的是三个已有字符串字段*里面装的字节*，不动任何合同的形状；不做持久化存储加密（`SECURITY.md` 紧挨着的那条独立缺口）。

## 落地位置

新包 `internal/harness/redact`（只导入标准库 `regexp`/`strings`），跟 `domain`、`policy`、`tools` 平级，不塞进其中任何一个。`application` 层在 `pipeline.go` 的两个工具收尾函数和 `loop.go` 构造助手消息完成事件的两处各调用一次；`openaicompat` 的 `redactSecrets` 被删除，`safeMessage`/`startupFailure` 直接调用共享的 `redact.Text`。

## 检测模式表（详见英文版 §4）

复用并扩展 `redactSecrets` 已有的 `Authorization`/`Bearer`/`sk-`/查询参数 `key=` 四条，新增：泛化的 `key/token/secret/password/credential = 值` 赋值形状（`.env` 文件、shell 导出最常见的真实 secret 形状）、AWS Access Key ID（`AKIA`/`ASIA` 前缀）、GitHub token（`ghp_`/`gho_`/`github_pat_` 等前缀）、PEM 私钥块。泛化赋值那一条是这张表里唯一有真实误报风险的一条（英文版 §9 有一个具体的误报例子），这里明说、接受下来，不是发现问题之后才补的免责声明。

## 一个行为变化

`redactSecrets` 今天对 `Authorization`/`Bearer`/`sk-` 是直接替换成空字符串；新的 `redact.Text` 统一换成 `[redacted]` 标记——一个刻意的、明说的行为变化：既然这个函数现在被更多地方共用，一个读者应该能分清"这里本来就是空的"和"这里有个 secret 被摘掉了"，空字符串替换做不到这一点。`TestProviderFailureErrorNeverRendersSecrets` 会在实现阶段跟着改。

## 风险

详见英文版 §8：泛化赋值模式的误报；未知形状 secret 的漏报（决策 4 的既定权衡）；实时流残余风险（§6 已明说）；未来新投影点绕开这次的上游脱敏点（决策 2 的落点已经把这个风险降到最低，除非未来出现一条全新的、绕开 `application` 现有构造点的原始字符串来源）；把 `openaicompat` 迁移到新的 `[redacted]` 标记会改变一个已经上线、已测试的合同的可观察输出（对应测试在同一次改动里一起更新，是刻意的行为变化，不是意外回归）。

## 下一步

跟 MCP 客户端适配器设计不一样，这次不推迟：它关闭的是一个**现在就存在**的缺口（工具结果和事件负载今天就在流过本项目已经上线的 Provider adapter 和 ACP 面，不是一个还没人用到的能力），而且 §2 的目标范围足够窄，可以直接进入实施计划。下一步是针对这份设计写实施计划，不是像 MCP 那样"已设计、未规划实现"。
