# Judge 语义元评测设计

**英文规范来源：** [Judge Semantic Meta-evaluation Design](2026-09-11-judge-semantic-meta-evaluation-design.md)。若有差异，以英文版为准。

**状态：** 已接受的规范设计

**日期：** 2026-09-11

**调研：** [Judge 语义元评测调研](../../research/architecture-gates/2026-09-11-judge-semantic-meta-evaluation.md)

## 要解决什么

之前的测试能阻止坏 JSON、假引用和没理由的答案，但不能衡量模型“判断得对不对”。
本切片新增一份人工审核的标准答案集，让真实 Judge 对同一批证据作答，再把结果与
标准答案逐条比较。它不重新运行 Subject，不改 Judge 提示词，不增加 provider，也
不凭空制定 GA 通过线。

## 数据与执行

`och.eval.judge-meta-set` 会冻结自身 ID/版本、所绑定的 JudgeConfig 摘要、1–20 次
重复，以及有顺序的案例。每个案例包含预期 verdict、人工解释、标签和小段合成
证据。证据路径必须安全，角色必须来自 JudgeConfig，而且所有声明角色都要出现；
字节上限与生产 Judge 相同。人工解释只用于复核标签，绝不会发给模型。

每个案例都使用生产环境完全相同的 criteria/evidence 包装、冻结提示词、严格 JSON
解析和语义校验。一轮就是一次模型调用和一条观察；坏响应或调用失败记为
Indeterminate，不隐藏重试。取消时在下一次调用前停止，输出明确标记为未完成的
前缀报告，保留已经付费的每条观察、用量、计划/完成调用数和停止原因。

## 怎么看结果

`och.eval.judge-meta-report` 记录 set/config/model 身份、每条观察（包括 Judge 已产出的
每项 criterion 结果；调用或解析在此之前失败时为空）、完整 3×3 混淆
矩阵和原始计数：完全一致、危险通过、错误失败、意外不可判定、以及对“本应不可
判定”材料强行下结论。危险通过单独计算，不能被一个反方向错误在平均准确率里抵消。

首版只报告事实，不设置通过阈值。小样本尚未校准，拍脑袋阈值不应成为 GA 门。

## 付费调用保护

`och-eval judge-meta` 必须同时得到 `-live`、`OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND`，并要求
`-max-calls` 精确等于“案例数 × 重复数”。所有文档、摘要、同意和调用预算检查都在
凭据可能被读取之前完成。普通 PR 只跑无密钥 fixture，不会产生费用。

首批六例覆盖：明确通过、明确失败、无证据自称成功、直接注入判定、仅引用恶意文本
但行为正常、证据互相矛盾。
