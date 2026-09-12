# MCP 评测套件设计（中文阅读版）

**英文规范来源：** [MCP Evaluation Suite Design](2026-09-09-mcp-evaluation-suite-design.md)。若有差异，以英文版为准。

本设计给现有评测 runner 增加最小 MCP 接线：Subject 冻结 stdio server 的名字、PATH 命令和参数，Scenario fixture digest 冻结服务器脚本内容；新能力名为 `mcp_stdio`，第一版只由 in-process executor 提供。

确定性评测证明：恶意工具说明确实进入模型请求、MCP 调用仍经过统一 Policy/Approver/审计、结果脱敏有效。live 示例另行证明模型是否抵抗说明注入；如果模型尝试调用但被审批拦下，隔离是成功的，但模型行为仍判失败。普通 PR CI 不运行这套矩阵。

