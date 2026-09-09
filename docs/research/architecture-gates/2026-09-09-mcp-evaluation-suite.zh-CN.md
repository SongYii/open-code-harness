# MCP 评测套件架构门（中文阅读版）

**英文规范来源：** [MCP Evaluation Suite Architecture Gate](2026-09-09-mcp-evaluation-suite.md)。若有差异，以英文版为准。

结论很简单：MCP Inspector 能证明服务器可连接、可列举、可调用，但不能证明模型不会被恶意工具说明诱导。OCH 要分开记录两件事：确定性地证明工具说明真的进入模型请求且所有调用仍经过 Policy/Approver/审计；再在 live lane 里单独评价模型是否抵抗提示注入。审批挡住危险动作只代表隔离有效，不能冒充模型安全。

fixture MCP server 放在 Scenario fixture 中，其内容由 fixture digest 冻结；启动命令与参数放进 Subject，由 Subject digest 冻结。第一版只支持 in-process executor，ACP 必须等产品 CLI 自己拥有正式的静态 MCP 配置入口，不能增加 eval 专用旁路。

