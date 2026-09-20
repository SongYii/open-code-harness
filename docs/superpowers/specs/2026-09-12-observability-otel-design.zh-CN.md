# Trace-only 可观测性与 OpenTelemetry 设计（中文摘要）

- **日期：** 2026-09-12
- **状态：** 2026-09-12 接受
- **英文正本：** [Trace-only Observability and OpenTelemetry — Design](2026-09-12-observability-otel-design.md)
- **调研依据：** [六项目架构门](../../research/architecture-gates/2026-09-09-observability.zh-CN.md)与[设计就绪调研](../../research/architecture-gates/2026-09-12-observability-design-readiness.zh-CN.md)

英文文件是规范正本；本文是同步中文摘要。

## 一句话结论

第一版只增加一条**显式开启、只含元数据、允许丢失的 trace 旁路**，用来回答“一个
Turn 的时间花在哪里”。它不替代事件库、审计、评测或恢复，也不采集提示词、工具
内容和路径。

首个消费者是明确配置自己 OTLP/HTTP Collector 或兼容后端的开发者/运维人员。没有
endpoint 就完全不启动遥测运行时。

## 关键边界

1. 新增无第三方依赖的 `internal/harness/telemetry` 端口，业务层只认识项目自己的
   类型。
2. 只有 `internal/harness/adapters/otel` 可以 import OTel SDK 和 OTLP exporter；
   Composition 唯一负责构造和关闭。
   现有 `adapters/memory` 只提供测试用的确定性记录器，不 import OTel。
3. 只做 trace，不做 OTel log、原生 metric、profile、Sentry、内置 Collector、
   dashboard 或查询 API。需要的 RED 指标由用户在 Collector 中从 span 派生。
4. 不传播 `traceparent`、`tracestate` 或 baggage，不修改模型 HTTP、ACP、MCP 和
   子进程协议。
5. 配置非法在打开 SQLite 前失败；运行后 Collector 失联则允许丢 trace，不能让
   Turn 失败或卡住。
6. 第一版生产代码新增量上限 1,200 行，超过必须回到设计评审。

## 为什么不会泄露正文

端口不是 `map[string]any`，没有任意属性，也没有 raw error。它只允许：

- Session、Turn、Item、Call、Approval、Compaction、Append 等已有 ID；
- purpose、risk、source、policy effect、approval decision、trigger、strategy、状态码
  等封闭枚举；
- token、字节、事件数、chunk 数、覆盖数和布尔值；
- 最多 256 UTF-8 字节的模型名、工具名。

端口中根本不存在 Input、Content、Arguments、Reason、Message、URL、path、argv 或
任意 exception 字段。超长值直接不导出，而不是截断，避免截出半段 secret。

禁止发送：系统/用户提示词、模型输出、工具参数和结果、文件路径、命令参数、审批
原因、错误消息、provider URL、API key、MCP `_meta` 与 baggage。Collector 脱敏只能
是第二道防线，不能替代源头不采集。

## Trace 长什么样

第一版只有九个稳定 span 名：

```text
och.turn
├── och.context.prepare
│   └── och.context.compact
│       └── och.model.request   # 真正的 summary 调用
├── och.model.request           # 正常对话调用
├── och.policy.decide
├── och.approval.wait
├── och.tool.execute
└── och.store.append

och.runtime.reconcile           # 启动恢复的独立 root
och.context.compact             # 手工压缩的独立 root
```

不会给每个 token delta、Domain event、数据库行、Context 消息或扫描单元建 span。

重复的 `RunTurn` 如果只是加入正在执行的同一请求，会得到自己的短 root，并标成
`joined`；真正执行模型/工具的 owner 才有子 span，避免把一次调用算成两次。已经完成
的请求重放标成 `replayed`。

工具参数验证、Policy 或审批拒绝时没有 `tool.execute`，因为工具实际上没运行。
`store.append` 覆盖一次 append 和它的 unknown-outcome 解析过程，不为每个事件或 SQL
建 span。

## 配置

项目配置只暴露：

```go
type Telemetry struct {
    OTLPTraceEndpoint      string
    SampleRatio           float64
    AllowInsecureLoopback bool
}
```

CLI 对应：

```text
-otel-traces-endpoint URL
-otel-traces-sample-ratio RATIO
-otel-allow-insecure-loopback
```

endpoint 为空就是关闭。开启时必须是以 `/v1/traces` 结尾的绝对 HTTPS URL；只有显式
允许且主机是字面量 loopback IP 时才接受 HTTP。禁止 userinfo、query、fragment 和
自定义认证 header。需要认证的厂商后端由本地/受管 Collector 转发。

采样率默认 1.0，可配置 `(0,1]`。项目不读取标准 OTel 环境变量，避免环境在不知情时
开启出口或改写配置。普通 eval 不加入遥测身份和参数，避免它成为隐藏评测证据。

## 队列与失败

固定上限：队列 256 span、单批 64、每秒 flush、单次 export 最多 3 秒、每个 span
最多 32 个属性、属性值最多 256 字节，event/link 都为 0。

适配器使用一个小型非阻塞有界 processor。队列满时只增加 drop 计数并立即返回；不
为每个 span 创建 goroutine，不建无界重试队列。OTLP exporter 自带的 retry 被明确
关闭，一个 batch 只有一次限时 HTTP 请求；专用 HTTP client 不读取系统代理环境。
Collector 失败只向 stderr 输出限频、不含 endpoint/原始错误的固定诊断，不能污染
承载 ACP 的 stdout。

关闭时先停 MCP 和 Runtime，最后在现有 shutdown deadline 内 flush OTel。超时就丢弃
剩余 trace；遥测关闭失败不能覆盖 Harness 自己的错误。

## 为什么不影响模型缓存

实现只把带 span 的 `context.Context` 在进程内向下传，并在旁路发送 OTLP。它不改变：

- 模型 messages、tools、顺序、JSON body、URL 或 header；
- ACP/MCP 帧、能力、`_meta` 或工具参数；
- 子进程 argv、环境、cwd 或 stdio；
- Domain event、checkpoint 和 eval schema。

不过不能只靠这段推理验收。实现必须捕获关闭/开启 trace 时的真实 provider HTTP
请求，逐字节比较 OCH 控制的 method、URL、header 和 body，证明缓存中性。

## 指标基数

Span 上允许高基数 ID，方便回查审计；但从 span 生成指标时只能建议使用这些维度：

```text
span.name、outcome、model purpose、tool source/risk、policy effect、
context trigger/strategy
```

Session/Turn/Item/Call/Approval/Append/provider request/runtime/model/tool/rule/
error code 都不能作为推荐 metric 维度，避免时间序列爆炸。

## 实现验收必须证明什么

- 关闭模式没有 goroutine、队列、export 请求、环境变量隐式行为。
- 内存 tracer 覆盖正常、工具、审批、压缩、取消、失败、join 和 replay 的父子树。
- 本地 OTLP receiver 能解码 protobuf；真实 Collector 兼容端在进程外看到完整 Turn
  树。
- 在提示词、输出、参数、路径、argv、错误、密钥和 MCP 元数据中放唯一 canary，原始
  OTLP bytes 与诊断里全部找不到。
- trace 开/关时 provider 请求逐字节一致。
- Collector 阻塞、报错、队列打满都不拖住 Turn，没有无限内存/goroutine/重试。
- 关闭受 deadline 约束并幂等；OTel 错误不覆盖 Harness 错误。
- 架构变异能抓住 OTel import 越界；隐私和队列变异必须真的把对应测试打红。
- 全量 race、文档门、依赖/漏洞检查和三种 benchmark 通过。
- 完成证据重新记录实际依赖、二进制体积与生产代码行数，不能把调研实验当实现证据。

在这些条件全部满足前，Milestone 10 的可观测性状态是：**已设计，未实现**。
