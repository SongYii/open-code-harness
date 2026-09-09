# 当前系统架构与边界收口设计

- 状态：已接受
- 日期：2026-09-09
- 范围：按当前实现整理整体架构，并让包边界默认拒绝
- 英文规范真源：[2026-09-09-current-system-architecture-design.md](2026-09-09-current-system-architecture-design.md)

## 1. 问题

基础章程描述项目目标，各个已交付模块也有实现合同，但仓库缺少一份
“系统现在实际长什么样”的权威总览。控制流、状态权威和包归属分散在许多
模块文档中。

可执行依赖门也有相同缺口：多数 `internal/harness` 目录有明确归属，但未知
生产目录会进入较宽松的默认规则。`contextengine` 和 `redact` 当前正走这条
默认路径。客户端隔离规则也只覆盖 `internal/client/acp`，没有穷尽覆盖独立的
`acpweb` 客户端。

## 2. 决策

新增一份描述当前实现、而非未来愿景的**实现合同**。它必须集中说明：

1. 包和层的归属；
2. 依赖方向及刻意保留的例外；
3. 命令、模型、工具、事件和恢复的端到端流程；
4. 持久事实、派生投影、工作区状态和进程生命周期各自由谁掌权；
5. 公共协议与进程边界；
6. 各模块详细实现合同的链接。

总览只负责索引和跨模块边界，不重复定义模块内部行为。模块内部发生分歧时，
详细模块合同优先。代码若改变拓扑或包归属，必须在同一个 PR 更新总览和可执行
边界登记。

## 3. 默认拒绝的包归属

`internal/harness` 下每个含生产 Go 文件的目录都必须匹配显式 owner。
纯测试目录不算生产目录；`testkit` 及具名 contract-test 包是显式测试支持例外。

未分类生产目录本身就是架构违规，无论它导入什么。生产代码不再有宽松默认
路径。本次把 `contextengine` 和 `redact` 正式登记为 owner，并保留现有的
host/network 限制。

## 4. 客户端边界

`internal/client` 整棵目录树都属于 ACP 客户端一侧。它不能导入
`internal/harness`，Harness 也不能反向导入 `internal/client`。规则自动覆盖
未来新客户端包，不再逐个目录补洞。

`cmd/och` 与 `cmd/och-eval` 从 composition/eval 入口进入 Harness；
`cmd/acp-client` 与 `cmd/acp-web-bridge` 保持独立客户端进程，不得导入 Harness
实现。

## 5. 文档与门禁联动

文档权威表登记当前架构合同、中文阅读版和证据账本。根 README 同时链接基础
章程和当前架构，让读者区分“未来方向”和“当前事实”。测试必须证明：

- `contextengine`、`redact` 已明确归类；
- 即使只导入无害标准库，未知生产包仍会失败；
- 测试支持例外精确到目录边界；
- `acp`、`acpweb` 及未来所有 `internal/client` 子目录都与 Harness 隔离；
- 当前仓库不存在未分类生产 Harness 包。

## 6. 非目标

本次不移动包、不改变公共 API、不引入新框架、不声称 import 测试能替代运行时
一致性或协议测试，也不扩展 Windows runtime 或设计 OpenTelemetry。

## 7. 验收

当前架构合同发布、生产包全部显式归类、客户端整树隔离、针对性回归能证明门禁
确实会失败、`docsguard` 与全仓 race 测试通过后，本次收口完成。
