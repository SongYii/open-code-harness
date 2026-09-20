# OpenTelemetry 可观测性：通俗说明

- 状态：已实现合同
- 实现日期：2026-09-12
- 英文真源：[observability-otel.md](observability-otel.md)
- 实现证据：[observability-otel-evidence.md](observability-otel-evidence.md)

## 它解决什么问题

事件库能回答“最后真正写进去了什么”，但不擅长回答“一次请求慢在哪里”。这次新增
的 Trace 会把一次 Turn 里的上下文整理、模型请求、策略判断、人工审批、工具执行和
数据库写入串成一棵有耗时的树。

它只是诊断副本，不是第二份真相。Trace 丢了，不会影响 Turn，也不能证明数据已提交；
是否提交仍只看 EventStore。

## 怎么开启

默认完全关闭。只有设置 `-otel-traces-endpoint` 才会创建 OTel 组件并向明确的
`/v1/traces` 地址发送 OTLP/HTTP。采样率用 `-otel-traces-sample-ratio` 设置。
真实网络必须用 HTTPS；本机测试只有同时写明
`-otel-allow-insecure-loopback`，才允许 `127.0.0.1`/`::1` 的 HTTP。

## 真实实现重点

项目先定义了自己的窄接口，而不是让业务代码到处引用 OTel。这个接口只认识固定的
操作名、结果和字段名，所以调用方不能随手塞入 Prompt、错误全文或任意标签。
只有 `adapters/otel` 知道 OTel SDK，Composition 负责创建和关闭它。

导出有硬边界：队列最多 256 个 Span，每批最多 64 个，一秒刷新，一次网络请求最多
三秒，不自动重试，也不读取代理环境变量。队列满了就计数并丢弃，绝不让正常 Turn
等它。Collector 挂了只在 stderr 给一条限频且不含地址/错误原文的提示。

一次数据库追加即使出现“写入结果未知”，随后进入精确查询或重试，也仍是一个
`och.store.append`。这样图里看到的是一次业务发布，不是几次底层网络/数据库动作。

## 为什么不会降低模型缓存命中

Trace 只在进程内部传递 Context，并另发一份 OTLP 数据。它不改 Prompt、消息顺序、
Tool Schema、模型 URL、请求 Header 或 JSON，也不向 Provider/MCP/ACP/子进程注入
`traceparent`。测试把开关前后的真实 Provider 请求逐字节比较，结果完全相同。

## 隐私边界

允许发送的是关联 ID、固定分类、计数、Token 用量和结果。禁止发送 Prompt、模型输出、
总结正文、工具参数/结果、路径、命令、审批理由、原始错误、API Key、Provider/Collector
地址和 MCP 元数据。测试把这些内容放入真实请求，再直接搜索原始 OTLP 字节，确认没有
出现。

## 开发中踩到的坑

- 第一个“泄密测试”用的字符串长得像合法工具名，实际上测的是元数据。后来改成真正
  的正文/密钥形状，并让它走完整 Application 和 Provider 路径。
- 安全包装器最初遇到非法结束字段会直接不结束 Span，可能留下半条记录。现在一定只
  结束一次，并标成固定的 `dropped/invalid_telemetry_metadata`。
- 强制丢弃基准第一次卡住，是假 HTTP Server 的处理函数没有退出信号；修的是测试夹具，
  生产代码的三秒上限没有放宽。
- 全量测试首次有两个 localexec 平台探测用例受并行宿主状态影响而失败，单独复跑均
  通过；它们的调用链不经过 Trace。

## 还没有什么

没有原生 Metrics、OTel Logs、Profile、Dashboard、内置 Collector、跨进程 Trace
传播或查询 API。依赖和二进制确实明显变大，准确数字写在证据账本，不能把这个成本
藏起来。

