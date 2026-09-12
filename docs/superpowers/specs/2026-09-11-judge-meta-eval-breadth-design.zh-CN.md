# Judge 元评测广度设计

**英文规范来源：** [Judge Meta-evaluation Breadth Design](2026-09-11-judge-meta-eval-breadth-design.md)。若有差异，以英文版为准。

**状态：** 已接受的规范设计

**日期：** 2026-09-11

## 问题

原有对抗样例能检查 JSON、评判项、总分、引用路径和矛盾处理，但仍会接受两种
“格式正确、无法复核”的确定性答案：一是判断了对话与审计两类证据，却只引用
对话文件；二是给出 `pass/fail`，理由却是空白。旧的 known-fail 样例本身就有
第一个问题。

## 约定

`pass` 或 `fail` 除原有规则外，还必须满足：

1. 冻结 criteria 声明的每一种证据角色，都至少有一条真实展示给 Judge 的路径
   出现在 `evidenceReferences`；
2. `rationale` 去掉空白后必须有内容。

任何一项不满足，都返回一条真实的 `Indeterminate`，说明会被限长、脱敏，并保留
本次调用用量；它不是 Go 错误，也不会偷偷重试。`indeterminate` 可以没有引用或
理由，因为“没有足够材料说明”本来就是它的合法含义。缺失、矛盾证据仍沿用原有
优先级和结构化字段。

## 兼容与边界

不修改冻结的 `och_quality_judge_v1` 和 JudgeConfig schema。现有全局
`evidenceReferences` 已要求列出 Judge 实际依赖的路径，而证据包构建阶段本来就把
每个声明角色当成必需项；实现只需记录实际展示路径所属的角色并在返回后核对。

这能证明“每类材料都引用过”，不能证明某一句结论严格由某一条路径推出。若要逐
criterion 引用，需要另起提示词/协议版本。真实模型的语义正确率仍应通过校准衡量，
不能由解析器测试冒充。

## 验收

- known-fail 同时引用 transcript 与 audit，仍得到确定性 fail；
- 少引 audit 的正确 JSON 被按角色覆盖原因降为 Indeterminate；
- 空白理由的正确 JSON 被按理由缺失降为 Indeterminate；
- 原有缺失、矛盾、indeterminate 与单次调用规则不变；
- 分别删除两条新守卫时，对应测试必须因预期机制缺失而失败。
