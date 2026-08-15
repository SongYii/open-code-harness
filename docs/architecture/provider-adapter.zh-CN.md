# 已实现 Provider Adapter 合同

- 状态：已实现内部合同
- 稳定级别：v1.0 之前为 `experimental`
- 成熟度：pre-v0，尚非通用可用（GA）发布
- 范围：`engine.Model` 背后的第一个真实 HTTP 适配器。一个 OpenAI 兼容
  Chat Completions SSE 客户端。不是插件内核、厂商 SDK 或工具循环。
- 英文规范设计：[Provider 合同与第一个真实 Adapter](../superpowers/specs/2026-08-15-provider-adapter-design.md)
- 完成证据：[Provider Adapter 证据台账](provider-adapter-evidence.md#中文证据台账)
- 英文已实现合同：[Implemented Provider Adapter Contract](provider-adapter.md)

本文是英文已实现合同的中文语义阅读版，记录当前代码和测试已经强制执行的行为。它是
内部 Go 合同，不是稳定公共协议；pre-v0 阶段若修改合同，设计、实现、测试和双语文档
必须同步变更。

## 已交付能力

`engine.Model` 仍是 Engine 消费端口。`testkit.ScriptedModel` 与
`adapters/openaicompat.Model` 都实现它。Application 准入 Turn，调用
`engine.TurnRunner`，经 EventStore v2 持久化终态。HTTP 适配器拥有线路映射、
SSE、密钥和分类后的 `engine.ProviderFailure`。Application unwrap 该 Cause，
不对同一次模型尝试重试。

厂商差异以 Capability Profile 加 composition-time identity 进入。Application
与 Engine 没有按供应商名的分支。默认 `go test` 使用 scripted
`http.RoundTripper` 与录制 SSE fixture，不需要活密钥，也不打开厂商套接字。

尚未实现 tools、SQLite、ACP、TUI、插件内核和厂商 SDK。

## 包权威与依赖方向

```text
headless caller / composition（当前为测试）
                    |
                    v
internal/harness/application  -----> internal/harness/engine
  命令与持久化权威                    Model 端口、profile、TurnRunner、
                                     ProviderFailure、AttemptStats
                    |
                    v
            internal/harness/domain
             生命周期 + log-only 事实

internal/harness/adapters/openaicompat ----实现----> engine.Model
internal/harness/testkit.ScriptedModel ----实现----> engine.Model
internal/harness/adapters/memory      ----实现----> application.EventStore
```

[`dependencies_test.go`](../../internal/harness/architecture/dependencies_test.go)
用 `TestProductionDependencyBoundaries`、`TestForbiddenImport`、
`TestClassifyProductionDirectory` 强制这些方向：

- `domain` 与 `engine` 不能导入 `net/http`，也不能导入路径段 `provider` /
  `providers`；
- `application` 不能导入 `adapters/*`、`testkit` 或 `net/http`；
- 架构 owner `openaicompat` 可以导入 `net/http` 和 `os`，不能导入 `os/exec`、
  `application`、`testkit` 或其他 `adapters/*`；
- 生产代码仍不能出现 `ScriptedModel`；
- `go.mod` 保持模块 stdlib-only，没有厂商 SDK。

## 消费端口

`engine.Model` / `engine.ModelStream` 方法签名未变。流语法仍是
`text_delta* → completed`。本 Slice 只在 `completed` 上增加可选 usage，并在
每次 `Run` 退出时带上 attempt stats：

```go
type Model interface {
    Stream(context.Context, ModelRequest) (ModelStream, error)
}

type ModelStream interface {
    Next(context.Context) (StreamEvent, error)
    Close() error
}

type StreamEvent struct {
    Type  StreamEventType
    Text  string
    Usage *TokenUsage // 除 completed 外必须为 nil
}

type TokenUsage struct {
    InputTokens, OutputTokens, CachedInputTokens uint64
}

type AttemptStats struct {
    Usage             *TokenUsage
    FinishReason      string
    ProviderRequestID string
    LatencyMs         uint64
}

type RunResult struct {
    Text  string
    Stats AttemptStats
}

type AttemptObserver interface {
    Snapshot() AttemptStats
}
```

`engine.Model` 不加 `Identity()`。只有 `*openaicompat.Model` 有
`Identity()`。HTTP composition 把该值拷进
`application.Config.RequestIdentity`。漏设是 composition 错误，不是类型错误。
Scripted 测试保持 identity 为 nil。

`TurnRunner` 在每次退出时对 `AttemptObserver` 做 type-assert。顺序是先
Snapshot，再 cancel，再 Close（`TestTurnRunnerSnapshotsBeforeCancelOnFailAndCancel`、
`TestTurnRunnerClonesSnapshotUsageBeforeClose`）。失败和取消返回带 Stats、
空 `Text` 的 `RunResult`。成功时从 observer 拷贝 `FinishReason`；失败/取消
清成 `""`。

`Next` 错误映射（`internal/harness/engine/runner.go`，
`TestTurnRunnerPreservesClassifiedInvalidStreamFromNext`、
`TestTurnRunnerDoesNotRemapClassifiedTransientAsEOF`）：

1. 调用方 context 已取消 → `CodeCanceled`；
2. 非 nil 且带合法 Code 的 `*engine.Error` → 保留 Code 和 Cause，不对这棵树走
   `isEOF`；
3. 未分类的 `io.EOF` → `CodeInvalidStream`，Cause 为 nil；
4. 其余 → `CodeModelStream`。

HTTP 适配器的断连错误包装 `provider_transient`，unwrap 链中没有 `io.EOF`
（`TestStreamConnectionDropHasNoEOF`）。

text delta 上带 `Usage` 是 `CodeInvalidStream`
（`TestTurnRunnerRejectsInvalidEventsAndBoundsBeforeDelivery`）。

## Capability Profile 与 composition identity

```go
type CapabilityProfile struct {
    NativeTools, Images, StructuredOutput, ReasoningFields, PromptCache CapabilityTriState
    ContextWindowTokens, MaxOutputTokens uint32
}

type RequestIdentity struct {
    AdapterFamily, ModelID, EndpointID string
    Profile                            CapabilityProfile
    IncludeUsage                       bool
    MaxTokensField                     string
}
```

`RequestIdentity.Validate`（`TestRequestIdentityValidateAcceptsCompositionIdentity`、
`TestRequestIdentityValidateRejectsMalformedFields`）要求：

- `AdapterFamily` 是 lower-snake token（本适配器为 `openai_compat`）；
- `ModelID` 非空、合法 UTF-8、无首尾空白、长度 ≤ 256；
- `EndpointID` 为 host[:port][/path-prefix]，不含 userinfo、query、fragment、
  填充空白或尾斜杠；
- 每个三态必须是 `unsupported`、`supported` 或 `required`，空值非法；token
  字段可为 0（表示未知）；
- `MaxTokensField` 只能是 `""`、`"max_tokens"` 或 `"max_completion_tokens"`。

唯一随包提供的 preset 是 `openaicompat.ProfileTextOnly`。没有按厂商命名的
helper，Application/Engine 也不按供应商名分支。`NativeTools=required` 在
`New` 时拒绝（`TestNewRejectsInvalidConfig` / `native tools required`）。第一
适配器不发送 `tools`、图片、`response_format` 或 cache-control 字段。

## 第一适配器：`internal/harness/adapters/openaicompat`

```go
type APIKeySource interface {
    APIKey() (string, error)
}

type EnvAPIKey struct{ Name string }     // Stream 时 os.Getenv
type StaticAPIKey struct{ Value string } // 仅测试；永不记录

type WireHints struct {
    IncludeUsage   bool
    MaxTokensField string
}

type Config struct {
    BaseURL, ModelID      string
    APIKey                APIKeySource
    Profile               engine.CapabilityProfile
    Hints                 WireHints
    UserAgent             string
    HTTPClient            *http.Client
    IdleTimeout, ResponseHeaderTimeout time.Duration
    MaxRequestBytes, MaxSSELineBytes   int
    AllowInsecureLoopback              bool
}

func New(Config) (*Model, error)
func (*Model) Identity() engine.RequestIdentity
func (*Model) Stream(context.Context, engine.ModelRequest) (engine.ModelStream, error)
```

`New` 不做网络 I/O。空 BaseURL/ModelID、nil 密钥源、URL 含 userinfo、非
loopback 的 `http://`（即使打开 flag，`169.254.169.254` 也拒绝）、required
native tools、非法 profile/hint 或负边界都会 fail-closed
（`TestNewRejectsInvalidConfig`）。仅当 `AllowInsecureLoopback` 时接受
loopback `http://`（`TestNewAcceptsLoopbackHTTPWhenAllowed`）。注入的
client/transport 会被 clone；从不修改 `http.DefaultClient` 或
`http.DefaultTransport`（`TestNewDoesNotMutateDefaultClientOrTransport`）。
重定向关闭：`CheckRedirect` 返回 `http.ErrUseLastResponse`，3xx 分类为
`provider_permanent` 且不跟随（`TestCheckRedirectThreeXXIsPermanent`）。

`Identity()` 拷贝 family `openai_compat`、ModelID、由 BaseURL 导出的
EndpointID、profile 和两个 wire hint（`TestIdentityCopiesProfileAndHints`）。

`Stream` 允许并发调用；每次调用拥有自己的 HTTP 请求
（`TestStreamConcurrentCallsOwnRequests`）。默认值：idle 60s、响应头 30s、
请求 JSON 1 MiB、SSE 行 256 KiB、`User-Agent: open-code-harness` 无版本。

测试 composition 助手 `MustComposeHTTP` 把 `Identity()` 拷进
`Config.RequestIdentity`，只对 loopback fixture host 打开
`AllowInsecureLoopback`，并拒绝非 loopback `http://`
（`TestMustComposeHTTPSetsLoopbackForFixtureServer`、
`TestMustComposeHTTPRejectsNonLoopbackHTTP`）。

## 请求映射

`POST {BaseURL}/chat/completions`，`stream: true`，消息为
`[{role:user, content: Input}]`。没有隐藏 system prompt。

| 字段 | 何时发送 | 测试 |
| --- | --- | --- |
| `stream_options.include_usage` | 仅当 `Hints.IncludeUsage` | `TestStreamRequestMapping` / `include usage`、`omit usage` |
| `max_tokens` / `max_completion_tokens` | 仅当 `MaxOutputTokens > 0` 且 hint 匹配 | `TestStreamRequestMapping` / `max tokens`、`omit max when tokens zero` |
| `Authorization: Bearer <key>` | 必需 | `TestStreamRequestMapping` |
| `Accept: text/event-stream` | 始终 | `TestStreamRequestMapping` |
| `User-Agent: open-code-harness` | 默认 | `TestStreamRequestMapping` |

缺失或空白密钥是 `CodeModelStartup` + `provider_auth`，且不发 HTTP
（`TestStreamMissingAPIKeyIsAuth`）。`EnvAPIKey` 在 `Stream` 时读取，不在
`New` 时读取（`TestEnvAPIKeyIsReadAtStreamTime`）。超大 JSON 是
`provider_permanent` 且不发送（`TestStreamRejectsOversizeRequest`）。已取消
的 context 不发送（`TestStreamCanceledContextDoesNotSend`）。

## SSE 映射

解析标准 SSE `data:` 行。录制 fixture 中的额外 JSON 键（`id`、`object`、
`created`、`model`）被忽略。`Content-Type` 经 `mime.ParseMediaType` 后大小写
不敏感（`TestStreamSuccessEmitsDeltasCompletedAndUsage` 使用
`TEXT/EVENT-STREAM; charset=UTF-8`）。

| 厂商载荷 | 适配器动作 | 测试 |
| --- | --- | --- |
| 非空字符串 `delta.content` | `text_delta` | `TestStreamSuccessEmitsDeltasCompletedAndUsage` |
| 空 / 仅 role 的 `content` | 忽略 | success fixture 第一块 |
| `reasoning_content` / `reasoning` / `reasoning_details` | 忽略；永不进入助手文本 | `TestStreamIgnoresReasoningContent`、`TestRunTurnHTTPReasoningIsolation` |
| 最后一个 object `usage` | `completed.Usage` / `AttemptStats.Usage` | `TestStreamSuccessEmitsDeltasCompletedAndUsage` |
| `input_tokens` / `output_tokens` / `prompt_cache_hit_tokens` | 备选字段映射 | `TestStreamUsageAlternateFields` |
| 小数 usage | `CodeInvalidStream` + `invalid_stream` | `TestStreamRejectsFractionalUsage` |
| `finish_reason=stop` | `completed`，`FinishReason=stop` | `TestStreamSuccessEmitsDeltasCompletedAndUsage` |
| `finish_reason=content_filter` | `CodeModelStream` + `provider_permanent`；`FinishReason=""` | `TestStreamContentFilterAndToolCallsLeaveFinishReasonEmpty` |
| `finish_reason=tool_calls` 或 `delta.tool_calls` | `CodeInvalidStream` + `capability_mismatch`；`FinishReason=""` | `TestStreamContentFilterAndToolCallsLeaveFinishReasonEmpty` |
| 空完成 | `CodeInvalidStream` + `empty_response` | `TestStreamEmptyCompletion`、`TestRunTurnHTTPEmptyCompletion` |
| 非字符串 `content`、非 JSON `data:`、多个 `choices`、超长行 | `CodeInvalidStream` + `invalid_stream` | `TestStreamRejectsNonStringContentAndOversizeLine` |
| 200 但不是 `text/event-stream`；HTTP 201 / 204 | `CodeModelStartup` + `provider_permanent` | `TestStreamNonSSEAndNon200TwoXXFailClosed` |
| `Next` 阻塞时 idle | `CodeModelStream` + `provider_transient` | `TestStreamIdleTimeoutIsTransient` |
| `Next` 期间连接断开 | `CodeModelStream` + `provider_transient`；无 `io.EOF` | `TestStreamConnectionDropHasNoEOF` |
| `ctx` 取消 | `CodeCanceled`；中止 HTTP 请求 | `TestStreamCancelUnblocksNext`、`TestStreamCancelKeepsLatencyAndUsage` |

Usage 字段顺序，以后到的 object 为准：`InputTokens` 先 `prompt_tokens` 再
`input_tokens`；`OutputTokens` 先 `completion_tokens` 再 `output_tokens`；
`CachedInputTokens` 先 `prompt_tokens_details.cached_tokens` 再
`prompt_cache_hit_tokens`。厂商 request id 取 `x-request-id`、
`x-ds-request-id`、`openai-request-id` 中第一个非空值（trim，最长 128
字节）。Latency 是适配器 HTTP 跨度；取消后 `Snapshot` 仍保留
（`TestStreamCancelKeepsLatencyAndUsage`）。

成功 `completed` 之后 `AttemptStats.FinishReason` 只能是 `stop`、`length` 或
`unknown`。失败或取消路径一律为 `""`。`content_filter` 和 `tool_calls` 永不
进入 `FinishReason`。

## 错误与分类

适配器返回 `*engine.Error`，`Code` 仍在
`{CodeModelStartup, CodeModelStream, CodeCanceled, CodeInvalidRequest,
CodeInvalidStream}`。分类放在 `engine.ProviderFailure`：

```go
type ProviderFailure struct {
    Class       FailureClass
    Retryable   bool
    RetryAfter  time.Duration
    Code        string
    HTTPStatus  int
    RequestID   string
    SafeMessage string
}
```

`ProviderFailure.Error()` 只渲染 durable code
（`TestProviderFailureErrorNeverRendersSecrets`）。Application
`durableFailure` 对 `primary.Cause` 做 `errors.As(*ProviderFailure)`；若
code 在允许集合内，则持久化 `failure.Code` 和固定展示句
（`TestRunTurnProviderAuthPersistsClassifiedFailureCode`）。没有
`ProviderFailure` 的 Scripted 错误仍持久化 Engine code。模型 Engine code 的
Application category 仍是 `CategoryModel`；不重试。

封闭的预流状态分类（`TestClassifyHTTPErrors`）：

| HTTP 状态 | Durable code | Class | Retryable |
| ---: | --- | --- | --- |
| 300–399 | `provider_permanent` | permanent | false |
| 401、403 | `provider_auth` | auth | false |
| 429（默认） | `provider_rate_limit` | rate_limit | true |
| 429 + 配额 token/code | `provider_quota` | quota | false |
| 400/413 + overflow token/code | `context_overflow` | permanent | false |
| 400、404、413、422（其余） | `provider_permanent` | permanent | false |
| 408、409、500、502、503、504、529 | `provider_transient` | transient | true |

配额覆盖最多检查 4 KiB，并匹配封闭的子串/code 列表。`Retry-After` 为整数秒
或 RFC1123，上限 1 小时（`TestRetryAfterRFC1123AndCap`）。无状态的
dial/EOF 是 `provider_transient`，且不 unwrap 成 `io.EOF`
（`TestDialFailureIsTransientStartup`）。HTML 正文变成 `http_<status>`
（`TestHTMLErrorBodyUsesStatusFallback`）。

失败 Item/Turn 上持久化的展示句：

| Durable code | 文案 |
| --- | --- |
| `provider_auth` | `provider rejected credentials` |
| `provider_quota` | `provider quota exhausted` |
| `provider_rate_limit` | `provider rate limited` |
| `provider_transient` | `provider temporarily unavailable` |
| `provider_permanent` | `provider rejected the request` |
| `capability_mismatch` | `provider returned an unsupported capability` |
| `context_overflow` | `provider context window exceeded` |
| `empty_response` | `provider returned an empty completion` |

`Retryable` 只是建议。`Service.RunTurn` 不循环。429 配额或限流 Turn 只做一次
`Stream`（`TestRunTurnHTTP429QuotaVersusRateLimit`）。

密钥：`Authorization`、`Bearer `、`sk-` 不得出现在 `Error()`、`SafeMessage`
或已编码事件中（`TestRedactionOnClassifiedMessages`、
`TestRunTurnHTTPSecretRedaction`）。

## Log-only 请求与用量事实

新增 schemaVersion-1 事件。Compact `Session` 的 Apply 只加 Version
（`TestApplyModelFactsAreVersionOnly`、`TestApplyCompactModelFactsAreVersionOnly`、
`TestCompactDecisionsMatchFullStateForModelFacts`）。EventStore v2 方法不变。

当 `StartAssistantTurn.Request != nil` 时，一次 `Decide` 产出：

```text
turn.started
assistant.message.started
model.request.recorded
```

（`TestDecideStartAssistantTurnWithRequestReturnsThreeEvents`）。`Messages`
必须恰好是 `[{role:user, text:Input}]`；其他形状是 `CodeInvalidCommand`
（`TestDecideStartAssistantTurnRejectsRequestMessages`）。`Request == nil`
保持 scripted 的两事件准入。

`RecordModelUsage` 是针对 running item 的 version-only 命令
（`TestDecideRecordModelUsageIsVersionOnly`）。Application 可选地在**同一**
running state 上 `Decide` usage，再 `Decide` 终态命令，然后拼成一次 append
（`TestRunTurnPrependsObservedUsageBeforeTerminal`）。只有
`RequestIdentity != nil` 且观察到 `Usage`、`FinishReason`、
`ProviderRequestID` 或 `LatencyMs` 之一时才前置 usage。没有 identity 时，即使
观察到 stats 也不持久化 usage
（`TestRunTurnObservedStatsWithoutIdentityDoNotPersistUsage`）。

`model.request.recorded` 与 `model.usage.recorded` 的 codec 是严格对象，
`DisallowUnknownFields`，键列表封闭
（`TestModelFactEventJSONRejectsNonStrictPayloads`、
`TestRecordedEventJSONUsesCanonicalEncodingForAllPayloads`）。
`finishReason` 只能是 `stop`、`length`、`unknown` 或 `""`。

`DigestRunTurnRequestV1` 仍只覆盖 Session ID 与精确 UTF-8 Input
（`TestDigestRunTurnRequestV1FramesSessionAndInput`）。Identity 不进入
digest。

## 重建形状

`ReconstructRequestResult` 按 CommandID 收集记录，只接受这些形状
（`TestReconstructRequestResultAcceptsExactRequestShapes`）：

```text
running scripted:   turn.started, assistant.message.started
running HTTP:       turn.started, assistant.message.started, model.request.recorded
terminal scripted:  准入对 + itemTerminal + turnTerminal
terminal HTTP, 无 usage:
                    准入对 + model.request.recorded + itemTerminal + turnTerminal
terminal HTTP, 有 usage:
                    准入对 + model.request.recorded + model.usage.recorded
                    + itemTerminal + turnTerminal
```

这就是 2 / 3 / 4 / 5 / 6 事件形状。usage 出现在终态对之后、多余 request、
未知同 CommandID 类型、turn/item id 不匹配、或 running 请求上出现 usage，
一律 fail-closed（`TestReconstructRequestResultRejectsMisplacedCompanions`）。
终态按**类型**查找，不按下标
（`TestDurableRequestTerminalErrorPreservesFailureAndInterruptionCode` /
`provider auth after usage`）。

经 fixture 的 HTTP `RunTurn`
（`internal/harness/adapters/openaicompat/runturn_test.go`）：

| 用例 | 持久结果 | 形状 |
| --- | --- | --- |
| 成功 fixture | completed 文本 `Hello world`；usage stop / request-id / latency | 6 事件；可重建 | `TestRunTurnHTTPSuccessRecordsRequestUsageAndReplay` |
| HTTP 401 | `provider_auth` | 5 事件（无 usage） | `TestRunTurnHTTP401PersistsProviderAuth` |
| HTTP 429 配额 vs 限流 | `provider_quota` / `provider_rate_limit`；一次 Stream | 失败 | `TestRunTurnHTTP429QuotaVersusRateLimit` |
| Stream 进入后取消 | interrupted `caller_canceled`；一次 Stream | `TestRunTurnHTTPCancelWinsWithoutSecondStream` |
| 空完成 | `empty_response` | `TestRunTurnHTTPEmptyCompletion` |
| reasoning fixture | completed `Visible`；无泄漏 | `TestRunTurnHTTPReasoningIsolation` |
| 含密钥的 401 正文 | 已脱敏；只持久化展示句 | `TestRunTurnHTTPSecretRedaction` |
| 同一 Request ID 重放 | `FindCommandRequest` found；无第二次 Stream | `TestRunTurnHTTPFindCommandRequestPreventsSecondStream` |

带观察到 latency 的 content-filter 风格失败会持久化 `provider_permanent`，
以及 `finishReason=""` 的 usage
（`TestRunTurnContentFilterPersistsPermanentUsageWithoutFinishReason`）。

## 默认无密钥测试

默认 `go test` 使用 `StaticAPIKey{Value: "test-key"}` 或 `t.Setenv` 与
scripted transport。本里程碑没有无密钥远程模式，也没有
`//go:build liveprovider` 套件。录制 fixture 在
`internal/harness/adapters/openaicompat/testdata/sse/`。

## 正式适配器与可执行证据

- `openaicompat.Model` 是第一个生产 `engine.Model` 实现；
- `testkit.ScriptedModel` 仍是同一端口上的正式 scripted 适配器。`modeltest`
  仍把 nil `Usage` 当作 scripted 默认；
- `MemoryEventStore` 仍是 EventStore v2 参考实现；
- 可复用套件 `eventstoretest.Run`、`modeltest.Run`、`enginescenariotest.Run`
  在 scripted 路径上仍然通过。

仓库根目录本地证据矩阵：

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
```

强制本合同时 focused packages：

```bash
go test ./internal/harness/domain ./internal/harness/engine \
  ./internal/harness/application ./internal/harness/adapters/openaicompat \
  ./internal/harness/architecture -count=1
```

## 明确排除项

本已实现合同不提供：

- tools、Tool Runtime、Policy、审批、MCP，或 Engine `tool_call*` /
  `reasoning_delta` 常量；
- reasoning item 持久化（reasoning 字段被丢弃）；
- 图片、音频、视频或 structured-output 请求；
- prompt-cache 布局或厂商缓存启发式；
- 多厂商路由、fallback、成本优化或 Application 重试；
- SQLite、JSONL、Runtime Host、ACP、TUI 或插件内核；
- EventStore v2 接口变更；
- OAuth、模型发现或厂商 SDK；
- 隐藏 system prompt；
- `RequestIdentity` 为 nil 时 ScriptedModel 路径上的可重建 identity；
- 连网或活密钥 CI。

这些排除项用于保持依赖顺序，不降低已经验证的 `engine.Model` 适配器路径，也
防止把本里程碑误称为 GA harness。
