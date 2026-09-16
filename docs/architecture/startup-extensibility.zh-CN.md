# 启动时可插拔架构：已实现首阶段与后续蓝图

**状态：** 首阶段已实现；非 GA。**日期：** 2026-09-12。

**公开 API 稳定级别：** `sdk/och` 与 `sdk/contextpolicy` 均为 **experimental**，
暂不承诺源码兼容，请固定已验证的版本或提交。已实现、是否 GA 与 API 稳定级别是不同维度。

英文 [startup-extensibility.md](startup-extensibility.md) 为规范文本，本文为同步阅读版。
本方案更新扩展与生命周期边界，不替换事件、checkpoint、工具安全或 OTel 合同。

## 架构判断

生命周期和历史保留修复独立于 SDK 也有明确价值。SDK 是针对所请求的启动可插拔
用例开展的实验，不是已有独立采用证据的稳定 API。目前尚无记录在案的真实外部
采用者；仓库自写 example 证明外部模块可编译并跑通 ACP，不证明独立生产使用。
转为 stable 前，须展示两个真实实现，以及至少一个真实外部消费者的使用与兼容性
反馈；自写示例不满足该采用门槛。实验性不降低持久事件完整性、重放及下述回滚要求。

采用「第三方 Go 扩展 + 自定义编译启动器 + 启动时选定、运行期不变」。不做热加载、
Go 二进制插件、任意事件 hook 总线或第二套 agent loop。首个扩展只控制上下文压缩
触发与保留边界，摘要模型和 checkpoint 格式保持原样。

我们的结构优势在于可执行行为与持久事实已经分开：请求准入、追加身份与未知提交
解析、fencing、重放、安全文件修改和 ACP 都有独立合同。策略可以变化，但历史与
恢复不必跟着第三方代码变化。这是审计与复现方面的优势，不能据此声称性能、生态
或模型效果领先；本阶段没有新增项目间性能对照。

原来的障碍是具体边界问题：外部模块不能导入 `internal`；Assembly 暴露的原始
Service 绕过 Host 准入；租约取消没有管到这些调用；续租卡在数据库锁时不能及时
fence；失租后同一实例会恢复接单；命令执行器未纳入关闭；规划逻辑有多个直接入口。
发布内部类型别名或引入通用插件内核并不能解决这些问题。

[2026-08-15 对照](../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.zh-CN.md)
保留为历史证据，不再被理解为全面禁止启动时组合。这里采用显式启动组合思想，
不复制 DeepSeek 的类型、运行时或产品界面；ACP 继续是客户端边界。

## 归属与启动 API

调用链是自定义 main / `cmd/och`（信号）→ `sdk/och.Run` → `internal/launcher`
（共享 flags、命令和 streams）→ composition（校验、构造、资源归属）→
runtime.Host（准入、取消、排空、fence）与 managed Service → application →
contextengine → `sdk/contextpolicy.Policy`。

`sdk/contextpolicy` 只依赖标准库。Domain 自己拥有身份 DTO，不引用 SDK。
application/contextengine 能依赖策略合同，不能依赖启动 SDK；具体 adapter 仍由
composition 构造，runtime 只保留既有 SQLite 特例。架构测试新增覆盖 SDK 和
launcher；独立模块编译验证外部用户不需要内部类型。

公开调用为 `och.Run(ctx, args, och.Streams{In, Out, Err}, och.Extensions{ContextPolicies})`。
与官方二进制共享 flags、ACP、`compact-session`、`export-session` 和关闭逻辑。
调用方负责信号；Run 负责装配生命周期；ACP 会关闭调用期间拥有的输入，stdout
只输出协议帧，诊断进入指定 Err。暂不公开 Service、Store、Domain、engine 或 Eval SDK。

每次启动独立持有注册表，条目包含 ID、版本、JSON factory。重复/保留 ID、未知选择、
空 factory/实例、错误配置或版本不匹配都在数据库及进程创建前拒绝，无全局注册。

- `-context-policy`：空值或 `builtin` 使用原算法。
- `-context-policy-config`：最多 64 KiB 的非敏感 JSON 对象，默认 `{}`。
- `-context-policy-version`：可选版本锁定，供 eval 使用。

核心规范化空白与对象键顺序，拒绝重复键、尾随值，保留数字字面形式，再计算 SHA-256。
配置会出现在命令行和评测材料中；摘要不能保护低熵秘密，因此禁止放凭据。Builtin
不接受自定义 config/version，继续使用原来的预算 flags。factory 只解析配置并构造
不可变、可并发调用的策略。注册本身不会改变默认选择。

可直接参考 [独立启动器示例](../../examples/keep-last-n/README.md)：模块只导入两个
SDK 包，实现并注册 `keep_last_n_turns`，编译成自己的可执行文件。

## 策略能力与核心不变量

`Policy.Plan(context.Context, Input) (Decision, error)` 收到独立的元数据：触发原因、
Force、预算、当前请求估算、旧 checkpoint 覆盖位置，以及核心生成的安全候选。
不传入消息正文、事件指针、provider、文件系统、store 或 checkpoint 修改能力。

候选只覆盖连续历史前缀，在完整 Turn 边界切分；最新/活动 Turn、已有 protected-tail
下限和完整工具调用结果对始终保留。每个候选带单元数量、保留轮数和保留请求估算；
尚未生成的新摘要不计入候选估算。候选在已有扫描结果上单遍生成，不增加 store 读取。
策略可以多保留，不能越过核心保留底线。

Decision 只能保留全部输入，或选择本次提供的候选 ID。Go 值语义隔离 Force 和
slice header（包括长度），回调前的局部复制不是额外防线。候选元素共享底层数组，
但资格校验使用核心独立拥有的 map，不读可被修改的 ID。因此修改 DTO 既不能授予
未提供 ID 的资格，也不能撤销原本合法的 ID。自动压缩可早于/晚于默认
阈值，但存在合法候选时，不能否决手动压缩、provider overflow、既有 usage anchor
强制，或估算超过 hard budget 的压力。没有候选就没有合法裁剪；仍由既有预算和
overflow 路径决定是否失败，不能虚构成功。

普通请求、mid-turn、手动、checkpoint 失效全量重规划、usage anchor 和 overflow
都经过统一规划入口；默认仍直接走原 selector。非法候选及回调错误归类为
`context_policy_invalid`，绝不偷偷退回 builtin；取消正常传播。合法决定后仍由核心
使用现有摘要器、checkpoint 校验、确定性 reset 阶梯、提交解析、materialization、
剪枝及模型请求证据记录。

真正的裁剪依据是**已提交覆盖范围**，不是计划。滚动摘要失败且未超过 hard budget 时，
必须保留旧 checkpoint 加其后的全部原始历史，不能采用失败轮次提出的新切点。
本阶段修复了这条真实存在的历史遗漏路径。

策略与 factory 都是可信同进程 Go 代码：要求确定性、无 I/O/后台工作、线程安全且
配合取消。Host 无法强杀挂死的 Go 函数、隔离任意 panic 或证明第三方的确定性。
不可信插件需要未来的进程隔离，不用每次开超时 goroutine 假装实现隔离。

## 生命周期与故障行为

Host 在同一锁内检查准入与登记操作；工作 context 同时跟随调用方和永久 Host
context。登记直到全部清理（包括终态追加）结束才释放。公开服务和外部 store
访问器受管；内部 use case 与清理追加使用原始 service/store，不会在关闭过程中
重复准入。ACP 也跟随 Host 取消。

Assembly 只通过 `Ready()` 与只读 `Done()` 提供生命周期观察，不再交出 Host 对象。
`Done()` 表示停止准入，不表示清理完成，调用方仍须调用 `Close()`；预先取得的
service/store 门面也必须通过准入。守卫固定 Assembly 的完整导出方法与字段边界，
新增能力须显式评审；规划引用守卫禁止绕过策略入口直接调用 selector，应用侧统一
经过 `planContext`，把函数保存为变量也不能绕开该检查。
门面拒绝矩阵枚举 Service/EventStore 全部方法，覆盖调用方取消、停止准入与清理完成；
仅返回请求校验错误不能算准入已执行的证据。

正常顺序：停止准入 → 取消并排空（此时继续心跳，允许终态写入）→ MCP → localexec
runner → Provider → Host 循环、匹配租约释放及 store → 既有 telemetry adapter。并发 Close
共享首次结果；构造失败也通过同一个 `Assembly.Close` 回收已取得的命令执行器与
Provider。Builtin 只关闭自己的 HTTP transport，不关闭借用的非标准 transport。
OTel 子系统和 span 属性未重做。

一个续租 worker 配合独立、按最后成功确认时间计时的 watchdog。被 fence 或超过
截止期就永久取消工作、停止导出及接单。即使 worker 卡在 SQLite mutex，也不能
拖延这一反应；迟到的成功不能复活实例。卡住的 worker 仍计入关闭等待，不能假报
完成。只有新 Host/进程才能获取租约并执行既有恢复流程。

排空与叶子资源关闭共享配置的 shutdown 界限。超时/无法证明回收时如实报错，保持
拒绝接单并停止续租，不主动释放租约，也不关闭可能仍被使用的 store；部分资源可
持续到进程终止。Close 不是重试或原地重启接口，租约仍按数据库规则自然过期。
调用方/监管器必须先终止旧进程，再启动后继，不能把超时理解为已静止。

## 事件、评测与回滚

仅自定义策略在 `context.prepared`、`context.compaction.started` 增加可选
`policy: {id, version, configDigest}`，transcript 同步投影。Builtin 省略字段，旧
payload 和 hash 不变。Domain 校验并深拷贝身份；事件 envelope schema 与 checkpoint
格式不变，不改写旧日志。

新 reader 可以读旧日志，缺失身份代表旧默认策略。切换策略后既有 checkpoint 仍
可经核心校验复用，历史重放不执行插件。但**旧严格 reader 不能读取新增的自定义
策略事实**。首次启用前应做已验证备份；回滚应保留能读新字段的 reader，或恢复
启用前备份（会丢失后续工作），不能删字段或重算审计链冒充兼容。

Eval 的可选 `SubjectContext.Policy` 冻结 ID、版本、配置摘要与配置，并纳入 Subject
digest，嵌套配置也规范化键顺序。ACP argv 携带选择、版本锁和配置；已有二进制
hash 冻结真实实现。首阶段自定义策略评测仅支持 ACP，自带 in-process BuildConfig
明确拒绝，不引入另一个评测注册表或 Eval SDK。

## 验证与限制

新增覆盖默认等价、强制触发、候选边界与被修改 DTO、取消、注册隔离与启动拒绝、
身份严格编解码/深拷贝、旧 checkpoint 摘要失败、阻塞续租、永久 fencing、排空
超时、Turn/手动回调期间 Close、并发 Close、eval 身份/argv，以及独立模块的真实
ACP 压缩与导出。既有 replay、SQLite、transcript、CLI 和依赖架构测试继续保留。

实施时观察到两项与环境相关的 localexec 失败：无条件假设没有 backend，以及
PID namespace 内 PID 与登记的宿主 PID 比较。后续评审者报告全量通过，未复现两项
失败。二者是不同执行环境下的观测，不是普遍失败结论；[证据台账](startup-extensibility-evidence.md)
保留两份结果及来源，不通过削弱生产沙箱来统一结果。ACP 集成需要本地端口和子进程权限。

不声称已经提升性能。Scan 仍会累积扫描窗口；首次加载、失效 checkpoint 的全历史
扫描，以及 application 的全量重放，并未因此变成有界内存。没有热替换、替代
摘要器、新 checkpoint 格式或任意写事件的扩展。

## 后续实施顺序与内部进展

[Provider 合同与架构评审](../superpowers/specs/2026-09-15-provider-startup-extensibility-design.zh-CN.md)
区分已获批的内部语义验收/生命周期与仍待评审的公开扩展；[内部证据](provider-internal-closure-evidence.md)
记录共同语义测试和资源归属。公开 SDK 仍由明确的外部接入需求决定，不能提前冻结 API。

1. Provider：围绕已有 engine port 设计公开请求/响应 DTO 与 adapter，保持能力、
   usage 和失败合同，不能公开内部别名。
2. Execution environment：内部首刀已去掉 MCP 对本地 `exec.Cmd` 的依赖，将长驻
   进程监管收回 localexec，见[实施计划](../superpowers/plans/2026-09-15-mcp-process-ownership.md)。
   文件与单次命令端口、真实 enforcement 报告和安全文件写入不变；公开执行环境
   扩展与容器/远程后端仍须真实需求，不随本切片发布。
3. 工具授权策略：纯决策 DTO、不可变风险目录、不可绕过的核心 guard；审批保留独立端口。
4. 有实际需求再扩 eval/storage：离线评价读取规范证据；替代存储先过完整追加、
   resolve、fencing、审计和恢复一致性测试，不是声明一个插件接口就算支持。

任何实验性公开 API 转为 stable 前，均须证明两个真实实现与一个真实外部消费者，
不能只依赖仓库自写示例。本阶段尚未满足该采用门槛。始终保留一套
编排、一份持久事实和一个生命周期归属。
