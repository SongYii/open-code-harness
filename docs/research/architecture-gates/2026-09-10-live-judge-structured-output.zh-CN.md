# Live Judge 结构化输出调研

**英文规范来源：** [Live Judge Structured-output Research](2026-09-10-live-judge-structured-output.md)。若有差异，以英文版为准。

**日期：** 2026-09-10

## 为什么要改

同一个不可变 Attempt 第一次用 DeepSeek 判了两次：一次通过；另一次刚好
输出 4096 token，JSON 被截断，于是严格解析器给出 Indeterminate。现有提示词
已经明确要求 JSON、给出完整示例并禁止额外文字，因此只改提示词解决不了
协议层问题。

## 调研结论

DeepSeek 当前的 Chat Completions API 支持
`response_format: {"type":"json_object"}`。JSON Output 指南要求提示词包含
JSON 字样和输出示例，并提醒要留足输出预算、偶尔仍可能返回空内容；现有 Judge
提示词已经满足前两项。

DeepSeek V4 默认开启思考且默认推理强度为 high，也支持
`thinking: {"type":"disabled"}`。这个 Judge 做的是有界分类，不需要先消耗一段
不可控的长推理，因此应关闭思考，把输出额度留给最终 JSON。

官方资料：

- [Chat Completions API](https://api-docs.deepseek.com/api/create-chat-completion/)
- [JSON Output](https://api-docs.deepseek.com/guides/json_mode/)
- [Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode/)

## 决策

每份 JudgeConfig 固定 `responseFormat: "json_object"`。厂商专有的
`thinkingMode` 是可选静态配置：仓库内 DeepSeek 配置固定为 `disabled`，没有该
扩展的服务可以省略。二者都通过 OpenAI-compatible adapter 发送并记入可审计
请求身份。空输出、截断和坏 JSON 继续保守判为 Indeterminate。

不在 `RunJudge` 内偷偷自动重试。一次付费调用对应一条追加式 Score 和独立用量；
需要复判时由操作者再运行一次，第二次观察清楚可见，不与第一次混在一起。
