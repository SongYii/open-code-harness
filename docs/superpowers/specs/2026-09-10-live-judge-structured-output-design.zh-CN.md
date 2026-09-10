# Live Judge 结构化输出设计

**英文规范来源：** [Live Judge Structured-output Design](2026-09-10-live-judge-structured-output-design.md)。若有差异，以英文版为准。

**状态：** 已接受的规范设计

**日期：** 2026-09-10

**调研：** [Live Judge 结构化输出调研](../../research/architecture-gates/2026-09-10-live-judge-structured-output.zh-CN.md)

## 范围

让现有严格 JSON Judge 使用服务端结构化输出，同时不削弱证据校验、成本记录和
追加式历史。本切片不改变 Subject，也不做隐藏重试。

## 约定

`JudgeProvider` 必须写 `responseFormat: "json_object"`；`thinkingMode` 是可选的
厂商扩展，唯一允许的非空值是 `disabled`，仓库内 DeepSeek 路由会固定它。
OpenAI-compatible adapter 分别发送为 `response_format.type` 与（设置时）
`thinking.type`，把 structured output 能力标为 required，
并把两个静态协议选择写入 `RequestIdentity`；Application 使用该身份时也会写入
`model.request.recorded`。

未知值在联网前拒绝。空内容、坏 JSON、截断 JSON 或语义无效输出仍产生一条
Indeterminate，并保留服务端报告的用量。

## 重试边界

`RunJudge` 只调用一次 `JudgeCaller`。需要复判时重新运行 `och-eval judge`，追加
一条新 Score，不替换也不隐藏旧结果，从而保留波动、用量和成本证据。

## 验收

- 请求体测试实际看到两个服务端字段。
- Adapter 与 JudgeConfig 在调用前拒绝非法值。
- 坏响应测试证明内部没有偷偷发第二次请求。
- 仓库内 JudgeConfig 与 EvalSet 的规范摘要一致。
- 原有严格语义校验和确定性前置门保持不变。
