# 可观测性与 OpenTelemetry 设计就绪调研

**状态：** 完整调研证据；建议尚不是已接受的规范设计

**日期：** 2026-09-12

**范围：** 在 [2026-09-09 可观测性架构门](2026-09-09-observability.zh-CN.md)
的六项目代码比较之上，补齐 OpenTelemetry 官方标准、Go 实现、GenAI/MCP
语义约定、Collector 运维、实测依赖成本，以及它们和本项目现有边界的映射。
本文件不引入依赖，也不实现遥测。英文文件是调研正本，本文是中文阅读版。

## 结论先行

本项目已经有“耐久的可观测性”：规范领域事件、可校验 JSONL 审计副本、会话
转录和评测证据。OpenTelemetry 不应复制这些记录，更不能成为第二真相源。
它真正能补的是一个有边界、有损、面向运行期的视图：**任务正在执行时，各步骤
耗时多久、谁调用了谁、关键路径在哪里。**

建议第一刀只有以下内容：

1. **只做 Trace。** Go 的 trace/metric 已稳定，但 log 仍是 RC；GenAI 与 MCP
   语义约定仍是 Development。
2. **只发元数据。** prompt、回复、reasoning、工具参数与结果、路径、命令、自由
   文本错误和 baggage 都不得离开进程。官方同样建议默认不采集 GenAI 内容。
3. **项目自己的 port + `noop`、`memory`、OTLP/HTTP adapter。** 采用最贴合本
   项目的 DeepSeek Harness 接缝形状，并吸收 Pi 把 noop/memory 当一等实现的做法。
4. **显式开启，用户自备 Collector/backend。** 关闭时不构造 SDK、线程、队列或
   网络客户端；开启代表操作者明确提供 OTLP 端点并知晓元数据会离开进程。
5. **配置错误启动失败，运行期导出失败不影响 Turn。** 队列、导出和关闭都有硬
   上限；队列满可以丢 span，但不能卡住 Agent，也不能静默。
6. **第一刀不跨进程传播 trace context。** 不修改模型 HTTP、ACP 或 MCP 消息。
7. **先不做原生 metric 和 OTel log。** 常用请求量、错误率和耗时分布可由
   Collector 的 span-metrics connector 从 trace 生成。

因此，它不会影响模型提示词缓存：推荐方案不改请求正文、不增工具定义、不增模型
可见字段，也不向模型服务注入 header。Go 进程里的 `context.Context` 变化对模型
不可见，相同请求仍保持字节一致。

## 六个参考项目告诉了我们什么

2026-09-09 调研读了固定 commit 的 Codex、Grok Build、DeepSeek Harness、Kimi
Code、Pi 和 Maka Agent：

| 项目 | 做法 | OTel | 大致生产代码量 |
| --- | --- | --- | ---: |
| Codex | 独立子系统，trace + metric，多种 exporter | 是 | 3,731 行 |
| Grok Build | OTel、fastrace、Sentry、profile、系统/会话指标、统一日志 | 是 | 20,329 行 |
| DeepSeek Harness | telemetry port + 可装载 OTel adapter | 是 | 528 + 332 行 |
| Kimi Code | 自建 telemetry client/transport/sink | 否 | 1,196 行 |
| Pi | 自建包，含 noop 与 memory 实现 | 否 | 935 行 |
| Maka Agent | 只记录成本和调用 | 否 | 458 行 |

OTel 不是成熟 Agent 的统一答案。最值得参考的是 DeepSeek Harness 的边界，而不是
照搬 Codex 的数据内容或 Grok Build 的规模。Codex 默认会发送工具参数和 2 KiB
工具输出预览；这符合它的产品选择，但不符合 OCH 当前的安全默认值。OCH 应从类型上
根本没有内容字段，而不是先接收内容再寄希望于脱敏。

## 官方标准目前成熟到哪里

OpenTelemetry 的主要信号是 trace、metric 和 log。当前 Go trace 与 metric 稳定，
log 为 Release Candidate；独立的 GenAI 规范仓库把模型 span、Agent span、GenAI
metric 与 MCP 约定都标为 Development。

所以可以依赖稳定 trace API/SDK，但不能把仍会变化的 `gen_ai.*`/`mcp.*` 名字写
进领域层。项目内部应持有小而版本化的 OCH 词汇，只在 OTel adapter 中做当前规范
映射。以后规范改名，改 adapter 即可，事件、应用层和工具接口不动。

官方还明确区分：有持续时间的操作适合 span；某个时刻发生的状态变化适合 event。
OCH 已经有耐久领域事件，因此不该把每条事件再复制成 OTel log/span。OTel 只包围
真正有持续时间和因果关系的操作。

## Go 依赖成本实测

2026-09-12 使用 Go 1.26.6 和 OTel Go v1.46.0，在 `/tmp` 的干净模块中实测：

| 引入面 | 非标准库 package | module |
| --- | ---: | ---: |
| 只引入 `otel/trace` API | 9 | 3 |
| trace SDK + OTLP/HTTP exporter | 172 | 21 |

和项目当前 47 个 module 相比，完整 SDK/exporter 会新增 19 个原本没有的 module
路径。即使选择 HTTP，仍会带入 OTLP protobuf/gateway 与 gRPC 相关依赖，因此“HTTP
就很轻”并不成立。

这还没有证明二进制大小和运行期开销。正式设计必须继续量：开启/关闭二进制增量、
noop 和常见 Turn 的分配/耗时、队列打满、端点失联时关闭、精确版本的依赖许可与漏洞。
建议第一刀生产代码上限为 **1,200 行**；超过就重新收缩范围。

## 三套东西的职责不能混

| 问题 | EventStore/审计/评测 | OTel trace |
| --- | --- | --- |
| 发生了什么、状态是否提交 | 真相源，可回放、可校验 | 有损、可能采样，不能裁决 |
| 评测是否通过 | 绑定证据与 Score 裁决 | 不能裁决 |
| 模型实际收到什么 | `ModelRequestRecorded` 完整记录 | 内容刻意不采集 |
| token 和模型耗时 | 耐久 usage 事实 | 适合实时关联 |
| 哪一步最慢 | 离线重建成本高，部分区间没有 | 核心价值 |
| 崩溃时谁仍在运行 | 事后恢复能判断耐久状态 | 运行时更直观，但崩溃会丢 |
| 全实例错误率/P95 | 需要另做聚合 | Collector/backend 擅长 |

任何 telemetry 回调都不得进入 EventStore 事务、改变领域决定、影响权限或评测结论。

## 七个开放问题的答案

### 1. 工具内容出不出进程

**不出，而且第一刀不提供开启内容的开关。**

禁止：所有对话/推理文本、工具参数/结果/失败文本、argv/env、路径/文件/diff、密钥、
含 query 的 URL、供应商原始响应、自由文本 policy/approval reason、baggage。

允许：封闭的状态/错误类别、数量、字节数/token 数、耗时、builtin/MCP 来源类别、风险
类别、adapter family、请求用途、finish reason、压缩 trigger/strategy 和关联 ID。
关联 ID 可用于 span 查审计，但不得成为 metric 维度。

Collector 脱敏只能做第二道防线。官方也说明敏感数据识别最终是实现者责任，并建议
最小化收集；不能把“Collector 会删”当成源头发送内容的理由。

### 2. 直接依赖还是 port

**用项目 port。** Domain/Application/Engine/Tool/Context/Runtime 不出现 OTel 类型；
Composition 统一构造和关闭。port 可以传 `context.Context` 维持进程内父子关系，但
不能暴露 `Tracer`、`Span` 或 OTel attribute 类型。

关闭的承诺应是“不构造运行期 SDK 状态、不启动网络”，不是“go.mod 里没有依赖”。
除非后续测出明确二进制或供应链收益，否则不为此引入 build tag、嵌套 module 或第二
个二进制的复杂度。

### 3. trace、metric 还是 log

**先 trace。** 它补充耗时、嵌套和因果；log 会重复领域事件且 Go log 尚未稳定；
metric 容易建立第二套聚合词汇和高基数风险。Collector 可以从 span 生成 RED 指标。
只有出现具体 dashboard/SLO 且从 span 派生不经济时，才增加原生 metric。

### 4. 规模边界

第一刀生产代码不超过 1,200 行，只做一种 signal。明确排除 log、原生 metric、profile、
Sentry、tail sampling、内置 Collector/backend、查询 API、UI、内容采集、远程配置和
动态插件。

### 5. exporter 失联怎么办

启用但配置非法时，`composition.Open` 在创建耐久资源前失败；启动后 Collector 失联，
Turn 继续。队列满时丢 span 并做限频本地诊断，绝不阻塞应用。导出超时独立取消，不
借用应用重试。关闭时先停应用/MCP/Host，让终态 span 入队，再用剩余独立时限关闭
telemetry；超时可返回诊断，但不能倒写已经完成的 Turn。

OTel 标准 batch processor 默认队列 2,048、batch 512、调度 5 秒、导出超时 30 秒；
OCH 不应偷偷继承这些值，应设更小且经过范围校验的显式上限。Collector 官方也说明
队列溢出、重试到期、无持久队列时崩溃都会丢数据，所以 telemetry 天然不能做证据源。

### 6. 关闭模式还带不带依赖

接受源码/module 依赖，禁止运行期激活。实测新增 19 个 module 是真实成本，正式设计
应钉死一个版本并更新 `SECURITY.md`。只有二进制/漏洞实测证明值得，才进一步拆构建。

### 7. 谁来消费

第一个消费者必须写清楚：使用自己 OTLP Collector/backend 的开发者或运维人员，用于
诊断本地或部署后的 `och`。项目不默认绑定任何厂商、不内置可视化后端。验收必须让
一个真实 Collector 兼容接收端在进程外看到完整 Turn 树；只测 memory adapter 不足以
证明产品价值。

## 推荐的第一版 Trace 树

| Span | 父节点 | 只含元数据的用途 |
| --- | --- | --- |
| `och.turn` | 根 | 结果、step/tool 次数、关联 ID |
| `och.context.prepare` | Turn | trigger、token 估计、裁剪数、decision ID |
| `och.context.compact` | Turn/手动根 | trigger、strategy、前后 token、chunk 数、结果 |
| `och.model.request` | Turn/压缩 | adapter、purpose、attempt、finish、usage、结果 |
| `och.policy.decide` | Turn | risk、effect、封闭 rule ID |
| `och.approval.wait` | Turn | granted/denied/timeout/canceled |
| `och.tool.execute` | Turn | builtin/MCP、risk、是否修改、结果字节数、结果 |
| `och.store.append` | 所属操作 | event 数、冲突/错误类别、结果 |
| `och.runtime.reconcile` | 启动根 | 检查/修复数量、结果 |

Span 名必须固定，不能把 session、工具、模型、路径、错误文本拼进名字。不要为每个
流式 text delta 或每条领域事件建 span。OTel 默认 attribute 值长度没有上限，因此
即使第一刀只有元数据，也要自己设置有限长度。

## 为什么不影响缓存

模型缓存通常关心模型可见请求的内容和顺序。推荐方案：

- 不改 system/user/assistant message；
- 不改工具 schema；
- 不改请求 body；
- 第一刀不向 provider 增加 trace header；
- 采样与导出发生在请求构造旁边/之后。

因此开启与关闭 telemetry 的 provider 请求应字节一致。未来实现必须用自动测试锁住
这一点。如果以后要给 MCP `_meta` 注入 `traceparent`，那是另一项协议与安全决定；
当前 MCP OTel 约定仍在 Development，不能顺手加入。

## 后续实现必须提供的证据

1. noop/memory/OTel adapter 共用 contract test。
2. 成功、模型失败、工具拒绝、审批超时、取消、压缩、恢复、关闭的精确父子树。
3. 变异测试证明 port 根本不能接收内容字段。
4. 恶意/挂起 exporter 不能拖慢或弄失败 Turn。
5. 队列打满时有界丢弃并通过 race 测试。
6. 接收端正常、拒绝、挂起三种关闭测试。
7. 关闭模式零 goroutine、零网络。
8. 开启/关闭时 provider 请求字节完全一致，证明缓存中性。
9. 真实 OTLP 接收端看到一棵完整 Turn 树。
10. 精确版本的依赖、二进制、分配、延迟、许可、漏洞和跨平台构建证据。
11. “只含元数据”的文档声明由可执行 attribute allowlist 守住。
12. telemetry 故障时 EventStore、审计与评测行为完全不变。

## 是否现在就实现

研究已经足够进入规范设计，但没有证明它是当前价值最高的实现。正确顺序是：合入并
评审调研；明确第一个真实 Collector/backend 消费者；写 trace-only 规范设计；在设计
PR 中量精确依赖和二进制基线；设计接受后再实现。

如果现实中没人准备读取 OTLP 数据，就停在调研阶段。做一个没人消费的 exporter，会
重复项目已经记录过的“机制休眠装船”问题。

## 来源

完整的一手来源、访问日期和链接见英文正本的 [Sources](2026-09-12-observability-design-readiness.md#sources)。核心来源包括 OpenTelemetry 官方 Go 状态、trace SDK、GenAI/MCP 语义约定、敏感数据与 context propagation 安全指南、Collector resiliency 和 span-metrics connector，以及本仓库 2026-09-09 的六项目固定 commit 调研。
