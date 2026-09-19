# 工具策略启动扩展设计

- **状态：** 已接受实施
- **日期：** 2026-09-18
- **范围：** 启动时选择、受信任的 Go 工具授权策略
- **英文规范源：** [Tool Policy Startup Extensibility Design](2026-09-18-tool-policy-startup-extensibility-design.md)
- **既有合同：** [启动时可插拔架构](../../architecture/startup-extensibility.zh-CN.md)、
  [工具运行时与策略](2026-08-16-tool-runtime-policy-design.md)及
  [工具授权核心防线证据](../../architecture/tool-authorization-guard-evidence.md)

若中英文出现差异，以英文规范为准。

## 1. 决策

发布 **experimental** 的 `sdk/toolpolicy`，并接入现有自定义 launcher 的
启动组合面。launcher 可以注册受信任的 Go 策略，并在打开资源前选择其中一个；一次
运行期间选择不可改变。这不是插件内核：不增加全局注册、运行时注册、热更新、Go
二进制插件、hook 瀑布、服务定位器或第二套工具循环。

核心仍是最终授权者。任何自定义策略都必须由
`internal/harness/policy.Guard` 包裹；空名称、网络风险、未知或自相矛盾的风险元数据、
工作区外访问都在调用扩展前由核心拒绝。策略报错或返回非法结果时，核心在返回后关闭式
失败。审批仍是 Application 独立拥有的端口：策略可以请求审批，但不能冒充用户批准。

自定义决策携带可选的持久实现身份；Builtin 各模式不写身份，历史事件字节保持完全
不变。只有出现两个真实实现和一个真实外部采用者并获得兼容反馈后，才考虑把公开 API
转为稳定。仓库自带 example 只证明编译与集成，不算独立采用。

## 2. 为什么必须组成一个完整切片

只加身份字段没有生产写入者，因为当前 Application 只构造内置表策略；只做注入又会让
审计和评测无法证明决定来自哪个实现。因此最小可用切片同时包含：

1. 只接收元数据的公开决策合同；
2. 启动期局部注册和选择；
3. 不可绕过的核心 guard 与独立审批；
4. 持久身份和评测身份。

本设计不顺便抽象 Provider、执行环境、存储、Eval 或事件写入。

## 3. 信任、权威与数据边界

策略是受信任的进程内 Go 代码，必须确定性、并发安全、配合取消，不执行 I/O、启动
进程、访问网络、创建后台任务，也不得在返回后继续修改结果。宿主无法强停一个阻塞的
Go 回调，也不能证明其确定性。panic 不伪装成普通拒绝；恶意或有缺陷的代码仍由进程级
监管处理。不可信策略将来必须单独设计进程边界。

策略只收到脱离核心状态的值：工具名、`read|write|exec` 风险、已与风险核对过的
mutation 位、核心算出的工作区内标记，以及供路径规则使用的可选有界路径字面量。它看
不到完整参数、文件内容、模型消息、事件/目录指针、文件系统、命令运行器、MCP 客户端、
审批器、存储、Provider、租约、Telemetry 或任何变更能力。网络和非法输入在公开回调前
已经被核心拒绝。DTO 不含核心提供的 slice、map、pointer 或 interface。

策略可以返回 `allow`、`deny`、`require_approval`。对于已通过核心准入的输入，它可以
比内置模式更宽松或更严格，但不能推翻核心拒绝，也不能代替审批结果。文件新鲜度、OS
沙箱、MCP 传输安全、目录校验和 observed-state 写保护仍是各自独立的权威。

## 4. 公开合同

新增只依赖标准库的 `sdk/toolpolicy`：

```go
type Risk string // read | write | exec
type Effect string // allow | deny | require_approval

type Input struct {
    Name        string
    Risk        Risk
    Mutates     bool
    WorkspaceIn bool
    PathLiteral string
}

type Decision struct {
    Effect Effect
    RuleID string
    Reason string
}

type Policy interface {
    Decide(context.Context, Input) (Decision, error)
}

type Registration struct {
    ID      string
    Version string
    Factory func(json.RawMessage) (Policy, error)
}
```

`builtin` 为保留 ID。ID/版本为 1–128 个 ASCII 字母、数字或 `._-+`。配置必须是
不含秘密、最大 64 KiB 的 JSON 对象；拒绝重复键和尾随值。宿主规范化对象键顺序但不
改变数字拼写，并计算小写十六进制 SHA-256。Factory 只收到规范字节的私有副本。
nil/typed-nil、重复/保留 ID、未知选择、版本不符、非法配置或 Factory 报错都必须在打开
持久资源前失败。

包导出与 context policy 同语义的 `ValidLabel` 和 `CanonicalConfig`，使 launcher 与
Eval 校验同一份字节身份，而不是分别重写算法。

回调收到当前请求 context。不会用超时 goroutine 假装强停回调；取消只能协作完成。
返回结果只能使用三个 effect；规则最大 128 bytes，原因最大 256 bytes，二者都须非空
且为合法 UTF-8。`PathLiteral` 沿用既有工具路径上限 4096 bytes。报错或非法结果都不会
产生可执行授权。

## 5. 内部边界与数据流

内部 `policy.Engine` 改为 `Decide(context.Context, Input)`；内置表忽略 context。
`Guard` 仍负责调用前核心拒绝和调用后结果校验。Guard 由 Application 对每个注入策略
强制应用，而不是依赖 Composition 自觉包装。

```text
sdk/och.Run
  -> launcher 解析 -tool-policy* 参数
  -> composition 冻结本次运行的注册与规范配置
  -> 把公开 Policy 适配成内部 policy.Engine
  -> application.NewService 强制套 policy.Guard
  -> pipeline 生成脱离状态的元数据并 Decide(ctx, input)
  -> 核心先持久化带身份的决定，再审批或执行
```

`sdk/och.Extensions` 增加 `ToolPolicies []toolpolicy.Registration`。内部 launcher 把位置参数
改成内部 Extensions 结构，避免将来两个 slice 混淆。Composition 负责公开/内部 DTO 转换；
Domain 和 Application 不导入 SDK。

Application 配置增加内部策略和可选的 Domain 身份。没有注入就按现有 Mode 构造内置
策略；注入时 Mode 只能为空/default，且身份必须非 nil 且合法，禁止含糊的“自定义 +
read_only”叠加。stock binary 不注册自定义策略，所以命名未知策略时启动失败，不会静默
回退。

CLI 增加 `-tool-policy`、`-tool-policy-version`、`-tool-policy-config`；既有 `-policy`
继续只选择内置模式。仅注册不会自动选择。选择自定义策略时拒绝非 default 的 `-policy`；
选择 builtin 时拒绝自定义配置或版本。

## 6. 失败语义

- 注册、配置或选择非法：在 Store、Provider、MCP、Host 构造前失败。
- 核心拒绝：持久化既有核心 deny，不调用自定义策略。
- 请求 context 仍有效时，策略报错或返回非法结果：持久化固定 `policy_failed` 规则/原因，
  带所选策略身份，执行次数为零。
- `require_approval`：先持久化，再走既有 Approver；缺失、拒绝或超时仍是拒绝。
- 决策期间调用方取消：走既有 caller-canceled Turn 路径，不伪造 policy failure，绝不执行。
- 策略阻塞或 panic：不宣称进程内可隔离，由宿主/进程监管恢复。
- 决策 append 结果未知：继续使用既有解析规则，在证明提交前零执行。

策略原始错误文本不作为 Reason 持久化，也不能未经安全处理进入诊断。Telemetry 可以记录
既有 effect/rule；本切片不新增策略身份、配置或路径 OTel 属性。

## 7. 持久身份与兼容性

Domain 自己拥有 `ToolPolicyIdentity {ID, Version, ConfigDigest}`，不引用 SDK 类型。
`RecordPolicyDecision` 与 `PolicyDecisionRecorded` 增加
带 `policy,omitempty` JSON tag 的 `Policy *ToolPolicyIdentity`。Domain 严格校验、复制，并只
接受 `id/version/configDigest` 三个键；摘要必须是 64 位小写十六进制。Application 把
启动时冻结的身份复制到每个自定义决策，包括策略报错形成的关闭式 deny。策略本身不能
提供或修改身份。

默认及所有既有内置模式都写 nil。Golden codec 测试必须证明旧事件字节完全不变，不能
只相信 `omitempty`。新 reader 可以读取旧历史；历史回放只校验事实，不加载或调用策略。

旧严格 reader 可能拒绝含新字段的 opt-in 事件。首次使用自定义策略前须做已验证备份；
回滚只能使用认识该字段的 reader，或恢复备份并丢弃之后写入。禁止剥字段、改写事件或
重算审计链。

`policy.decision.recorded` 继续是 canonical audit/version fact，并继续从 Session
transcript 和 ACP 轨迹投影中省略。JSONL audit replica 已复制规范事件字节，因此自动携带
身份，不破坏 UX transcript 合同。

## 8. Eval 身份

`SubjectPolicy` 增加可选 `ToolPolicy *SubjectToolPolicy`，冻结 ID、版本、规范化非秘密配置
及摘要。自定义策略只允许配 builtin mode `default`；字段缺失时既有 Subject 字节不变。
解码时重新规范化配置并核对摘要。

ACP executor 生成 `-tool-policy/-tool-policy-version/-tool-policy-config`；既有 executable
digest 固定编译后的自定义实现。stock in-process executor 必须拒绝自定义策略，绝不能
静默运行 builtin。审计验证器检查所有出现的带身份决策都与 Subject 一致；没有执行工具
的 Scenario 不需要伪造策略事件。不增加 Eval 插件注册表或新 evaluator API。

## 9. Example 与稳定级别

新增独立 module `examples/deny-tools`，只导入 `sdk/och` 和 `sdk/toolpolicy`，注册一个由
有界工具名列表配置的拒绝策略，并启动共享 launcher。它证明启动选择、核心安全、自定义
持久身份、审批分离和 ACP 互操作。

它仍是项目自有的编译/集成消费者，不满足转稳定门槛；`sdk/toolpolicy` 与扩展后的
`sdk/och.Extensions` 都保持 experimental。

## 10. 验证与变异

测试须覆盖：SDK 隔离和独立 module 构建；规范配置、重复键、64 KiB、标签、重复/保留
注册、版本、nil/typed-nil；核心拒绝不调用宽松策略；策略报错/非法输出产生带身份 deny
且零执行；allow/deny/approval；默认事件字节；严格身份 codec/clone/replay/audit；Subject
摘要与 ACP argv；in-process 拒绝；取消、并发、回调期间 shutdown 以及独立 ACP example。

逐项删除调用前拒绝、调用后校验、身份复制、默认省略和 Subject/runtime 身份一致性，
对应聚焦测试必须失败。被其他前置检查遮住而仍然绿色的变异不算证据。

## 11. 明确排除

不实现运行时热替换、Go binary plugin、全局注册、策略链、hook 排序、记忆式授权、策略
自批审批、参数/内容检查、事件写入、替代执行器、存储/Eval 插件、任意网络放行、panic
恢复、强制终止回调或不可信代码沙箱；不承诺 SDK 稳定，也不宣称已有外部采用。

## 12. 交付顺序

1. 公开 DTO/配置合同与 context-aware 内部 Engine。
2. Domain 身份、严格 codec/clone、字节兼容和 audit 测试。
3. Application 注入、强制 Guard 和带身份失败路径。
4. Composition/launcher 注册、选择参数与启动拒绝。
5. Eval Subject/ACP 身份冻结与核验。
6. 独立 example、ACP 端到端、文档、变异、race 和证据账本。

所有步骤测试先行。在持久身份和强制 Guard 尚未一起走过生产路径前，不发布可选择的
自定义策略入口。
