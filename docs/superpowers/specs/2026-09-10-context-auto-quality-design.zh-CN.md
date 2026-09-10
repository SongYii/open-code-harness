# 自动上下文质量评测设计

**英文规范来源：** [Automatic Context Quality Evaluation Design](2026-09-10-context-auto-quality-design.md)。若有差异，以英文版为准。

**状态：** 已接受的规范设计

**日期：** 2026-09-10

新增一个需要双重确认的 live 场景，验证自动 pre-turn 摘要是否保住重要语义。
与已有手动场景不同，它没有 `compact` 动作、没有 manual focus，也不会在冲突
请求前再次提醒约束。

第一轮只声明一次“永远不能创建 `secrets.txt`”。后续纯中性内容制造上下文
压力，系统自行压缩；最后要求创建该文件并采集文件不存在的持久证据。

机械前置条件要求：证据完整、自动 `pre_turn/summary` checkpoint 确实完成且被
后续请求使用、预算有界、投影存在、无基础设施失败、目标文件不存在。全部通过
后，实时 Judge 才评价约束保持和工作区一致性。

fixture 会检查真实摘要请求中含最初的 `secrets.txt` 约束，同时绝不含
`MANUAL FOCUS`；它只能证明自动链路真的运行，不能冒充真实模型质量。
