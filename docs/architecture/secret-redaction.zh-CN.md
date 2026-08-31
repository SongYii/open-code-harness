# 已实现 Secret 脱敏合同

**状态：** 已实现；尚非 GA

**稳定级别：** `internal`——一个 Go 包和 application 层行为，不是公开线协议

**成熟度：** pre-v0，尚非通用可用（GA）发布

**权威来源：** [Secret 脱敏设计](../superpowers/specs/2026-08-31-secret-redaction-design.md)

**完成证据：** [Secret 脱敏完成证据](secret-redaction-evidence.md)

**包：** `internal/harness/redact`（正则模式集）；`internal/harness/application`（两个调用点）；`internal/harness/adapters/openaicompat`（迁移后的调用方）

本文记录当前代码和测试已经强制执行的行为。它是内部 Go 合同，不是稳定的公共协议。若本文与英文版冲突，以英文 [secret-redaction.md](secret-redaction.md) 为准。

## 范围

本合同关闭了 [`SECURITY.md`](../../SECURITY.md) 点名的那个缺口：工具调用结果、工具调用失败消息，以及模型最终的助手消息文本，在被持久化、复制进 JSONL 审计轨迹、或投影到 ACP `session/update`（无论现场还是重放）之前，都会被扫描一小组形似 secret 的子串并脱敏。这是一个硬编码的、按形状匹配的规则，不是基于熵的启发式，也不是一个穷尽式的 secret 扫描器；本合同之前的[架构调研门](../research/architecture-gates/2026-08-30-secret-redaction.md)已经证明，研究过的六个参考项目里没有一个真正解决过这个问题。

## `internal/harness/redact`

`Text(s string) string`（`redact.go`）按固定顺序应用一组正则，把每个匹配到的 secret 的*值*替换成字面标记 `[redacted]`——绝不是空字符串，这样读者能分清"这里有个 secret 被摘掉了"和"这个字段本来就是空的"。如果某个模式捕获了一个键名或 header 前缀，会保留它；只有值本身变成标记（`TestTextRedactsAuthorizationHeader`、`TestTextRedactsGenericKeyValueAssignment`）。

| 形状 | 处理方式 | 测试 |
| --- | --- | --- |
| `Authorization` header | 整行剩余部分变成 `[redacted]`；标签保留 | `TestTextRedactsAuthorizationHeader` |
| `Bearer` token | 单个 token 变成 `[redacted]` | `TestTextRedactsBearerToken` |
| Provider 风格 secret key | `sk-`、`sk-ant-`、`sk-proj-` 前缀 | `TestTextRedactsProviderStyleSecretKeys` |
| 通用 key/value 赋值 | 大小写不敏感的 `key`/`token`/`secret`/`password`/`credential`，可以独立出现，也可以作为下划线连接标识符的尾部（如 `API_KEY`），后面跟 `:`/`=` | `TestTextRedactsGenericKeyValueAssignment` |
| AWS access key ID | `AKIA`/`ASIA` + 16 位字母数字 | `TestTextRedactsAWSAccessKeyID` |
| GitHub token | `gh[pousr]_...`、`github_pat_...` | `TestTextRedactsGitHubTokens` |
| PEM 私钥块 | `-----BEGIN ... PRIVATE KEY-----` 到匹配的 `END` 行，整块算一次匹配 | `TestTextRedactsPEMPrivateKeyBlockAsOneMatch` |

原计划要单独写一条 `?key=`/`&key=` 查询字符串正则，实现过程中被去掉了：等通用 key/value 正则的取值边界也改成遇到 `&` 就停之后，一条独立的查询字符串规则对它自己的两个测试来说从没真正触发过——变异测试把它揪出来，证明这是一个安全关键包里的死代码，不是覆盖率的净增益（`TestTextRedactsQueryStringKeyPreservingParameterName` 和 `TestTextRedactsQueryStringKeyAfterAmpersand` 单靠通用正则就都能满足）。剩下的每一条规则都做过独立的变异验证：禁用它会让它自己专属的测试因为正确的原因失败。

`redact.Text` 对已经脱敏过的输出是幂等的：对一段已经处理过的字符串再跑一遍，不会再有任何变化，因为 `[redacted]` 标记（或者后面跟着标记的保留标签）本身不会命中任何一条规则。`openaicompat` 自己那两个测试辅助函数正是靠这条性质来断言"没有残留的未脱敏 secret 形状"，而不用去硬编码到底哪些标签会被保留下来（见下文"整合"一节）。

## 脱敏发生在哪里

脱敏只在一个地方、往上游跑一次——在一个模型可见的字符串即将变成领域命令的那个点，绝不在下游的某个投影或导出边界，也绝不在本项目已有的大小裁剪/截断逻辑之后跑，所以从脱敏的角度看，一个 secret 永远不可能横跨截断边界（截断逻辑看到的永远已经是脱敏过的内容）。

- **`internal/harness/application/pipeline.go`**：`completeToolAndContinue` 在构造 `domain.CompleteToolCall{Content: ...}` 和随后同一个函数里的 `engine.RuntimePayload{..., Content: ...}` 之前，把 `content` 脱敏一次。`failToolAndContinue` 对 `message` 做同样对称的处理，在 `domain.FailToolCall{Message: ...}` 和它自己的 `RuntimePayload` 发送之前脱敏一次。一个脱敏过的值同时保护了持久化的领域事件、JSONL 审计副本，以及同一个工具调用结果/失败文本的实时和重放两条 ACP 投影路径。
- **`internal/harness/application/loop.go`**：`runResult.Text` 在它变成领域命令的两个地方都被脱敏——`runStepLoop` 里带工具调用提议的中间步骤 `domain.CompleteAssistantMessage`，以及 `completeAssistantTurn` 里 turn 最终文本的 `domain.CompleteAssistantTurn`。`completeAssistantTurn` 脱敏后的那份还得流进 `commitTerminalAppend` 的 `text` 参数，而这个参数最终会变成调用方看到的 `RunTurnResult.Text`（`owned.result.Text = text`）——这是一个跟领域事件不同的、独立的第二泄露面，在实现过程中被发现并堵上，不是想当然认为不存在。

`FailAssistantTurn`/`InterruptAssistantTurn` 的 `Message`/`Code` 字段从来不是脱敏目标：直接核实过 `durableFailure`（`turn.go`）永远只返回 `displayFailureSentence` 的固定、按代码索引的句子，哪怕背后是一个真实的 `engine.ProviderFailure`（它自己的 `SafeMessage` 已经脱敏过了，见下文），也只取它的 `Code`，`SafeMessage` 整个被丢弃。任何工具调用参数、任何实时 `model.text.delta` 流式分片，都从不被脱敏（见"排除项"）。

## 跟 Provider adapter 已有先例的整合

`internal/harness/adapters/openaicompat` 原本自己有一份很窄、早就存在的脱敏——四个硬编码正则，只用在一条路径上，`engine.ProviderFailure.SafeMessage`（provider HTTP 失败响应体）。那份私有实现已经删掉；`safeMessage` 和 `startupFailure` 直接调用 `redact.Text`。这是一个明说的行为变化：旧实现把匹配到的 secret 替换成空字符串，所以一个硬编码"`Authorization`/`Bearer `/`sk-` 绝不能出现在任何分类或持久化文本里"的测试，跟新的、保留标签的行为冲突了（`"Authorization: [redacted]"` 合理地包含了 `Authorization` 这个词）。这个目录下两个共享的 `assertNoSecrets` 测试辅助函数（`openaicompat` 和 `openaicompat_test` 两个测试包各一个）都被改写成断言 `redact.Text` 对这段文本是幂等的——一个更本质、不依赖具体模式集的不变量，不用硬编码到底哪些标签会存活下来。

## 排除项

本已实现合同不提供：

- **工具调用参数脱敏。** 工具的参数同时也是驱动真实工作区写入或 `exec` 执行的实际输入；在使用前脱敏这个值会把工具的真实效果搞坏。只脱敏"展示副本"（ACP 线协议上的 `rawInput`、审计轨迹）而不动执行副本是一个合理的未来扩展，本合同没有实现它。
- **实时 `model.text.delta` 流式脱敏。** 分片是在模型完整消息组装完成之前就到达的任意字节片段；一个 secret 完全可能横跨两个分片，而没有累积缓冲区可供脱敏。只有组装完成的完整文本（`engine.RunResult.Text`）会在变成领域事件之前被脱敏一次。
- **基于熵或机器学习的 secret 检测。** 对形状未知（没有可识别前缀）的 secret 有一个有界、已知的漏报率，这是刻意接受的权衡，用来换取不对本项目自己合法的高熵内容（git SHA、base64 块、UUID）产生一个无边界的启发式误报率。
- **给 Provider API key 配一个编译期构造时脱敏的类型**（一个等价于 `RedactedString` 的 Go 类型）。这个 key 今天是靠结构性手段保护的——`APIKeySource.APIKey()` 只在发请求时才被调用，从不在那次调用之后继续存储——但靠的是代码评审约定，不是编译器强制的类型。
- **持久化存储加密。** 一道独立的防线（`SECURITY.md` 紧挨着的"未加密"那条）；在写入前脱敏一个 secret，和给写入的存储本身加密，不能互相替代。
