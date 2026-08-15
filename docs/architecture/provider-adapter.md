# Implemented Provider Adapter Contract

- Status: Implemented internal contract
- Stability: `experimental` until v1.0
- Maturity: pre-v0; not a general availability release
- Scope: first real HTTP adapter behind `engine.Model`. One OpenAI-compatible
  Chat Completions SSE client. Not a plugin kernel, vendor SDK, or tool loop.
- Normative design: [Provider contract and first real adapter](../superpowers/specs/2026-08-15-provider-adapter-design.md)
- Completion evidence: [Provider adapter evidence ledger](provider-adapter-evidence.md)
- Chinese reading copy: [已实现 Provider Adapter 合同](provider-adapter.zh-CN.md)

This document records behavior enforced by the current code and tests. It is
an internal Go contract, not a stable public protocol. Pre-v0 changes still
require the design, implementation, tests, and this document to move together.

## Delivered capability

`engine.Model` remains the Engine consumption port. `testkit.ScriptedModel` and
`adapters/openaicompat.Model` both implement it. Application admits a Turn,
calls `engine.TurnRunner`, and persists terminal facts through EventStore v2.
The HTTP adapter owns wire mapping, SSE, keys, and classified
`engine.ProviderFailure`. Application unwraps that cause and does not retry a
model attempt.

Vendor differences enter as a capability profile plus composition-time
identity. Application and Engine have no vendor-name branches. Default `go
test` uses a scripted `http.RoundTripper` and recorded SSE fixtures; it needs
no live key and opens no vendor socket.

Tools, SQLite, ACP, TUI, a plugin kernel, and vendor SDKs are not implemented.

## Package authority and dependency direction

```text
headless caller / composition (tests today)
                    |
                    v
internal/harness/application  -----> internal/harness/engine
  command + durability               Model port, profile, TurnRunner,
                                     ProviderFailure, AttemptStats
                    |
                    v
            internal/harness/domain
             lifecycle + log-only facts

internal/harness/adapters/openaicompat ----implements----> engine.Model
internal/harness/testkit.ScriptedModel ----implements----> engine.Model
internal/harness/adapters/memory      ----implements----> application.EventStore
```

[`dependencies_test.go`](../../internal/harness/architecture/dependencies_test.go)
enforces these directions (`TestProductionDependencyBoundaries`,
`TestForbiddenImport`, `TestClassifyProductionDirectory`):

- `domain` and `engine` cannot import `net/http` or a path segment `provider` /
  `providers`.
- `application` cannot import `adapters/*`, `testkit`, or `net/http`.
- Architecture owner `openaicompat` may import `net/http` and `os`. It must not
  import `os/exec`, `application`, `testkit`, or any other `adapters/*`.
- Production code still cannot mention `ScriptedModel`.
- `go.mod` stays module-stdlib. No vendor SDK.

## Consumption port

`engine.Model` / `engine.ModelStream` method signatures are unchanged. The
stream grammar stays `text_delta* → completed`. This slice adds optional usage
on `completed` and attempt stats on every `Run` exit:

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
    Usage *TokenUsage // nil except optionally on completed
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

`engine.Model` does not grow `Identity()`. Only `*openaicompat.Model` has
`Identity()`. HTTP composition copies that value into
`application.Config.RequestIdentity`. Omitting it is a composition bug, not a
type error. Scripted tests leave identity nil.

`TurnRunner` type-asserts `AttemptObserver` on every exit. Order is Snapshot,
then cancel, then Close (`TestTurnRunnerSnapshotsBeforeCancelOnFailAndCancel`,
`TestTurnRunnerClonesSnapshotUsageBeforeClose`). Fail and cancel return
`RunResult{Stats: snapshot}` with empty `Text`. `FinishReason` is copied from
the observer on success and cleared to `""` on fail/cancel.

`Next` remapping (`internal/harness/engine/runner.go`,
`TestTurnRunnerPreservesClassifiedInvalidStreamFromNext`,
`TestTurnRunnerDoesNotRemapClassifiedTransientAsEOF`):

1. Caller context already canceled → `CodeCanceled`.
2. A non-nil `*engine.Error` with a valid code → keep that `Code` and `Cause`.
   Do not run `isEOF` on that tree.
3. Unclassified `io.EOF` → `CodeInvalidStream` with nil Cause.
4. Else → `CodeModelStream`.

Connection-drop errors from the HTTP adapter wrap `provider_transient` without
`io.EOF` in the unwrap chain (`TestStreamConnectionDropHasNoEOF`).

`Usage` on a text delta is `CodeInvalidStream`
(`TestTurnRunnerRejectsInvalidEventsAndBoundsBeforeDelivery`).

## Capability profile and composition identity

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

`RequestIdentity.Validate` (`TestRequestIdentityValidateAcceptsCompositionIdentity`,
`TestRequestIdentityValidateRejectsMalformedFields`) requires:

- `AdapterFamily` is a lower-snake token (`openai_compat` for this adapter).
- `ModelID` is non-empty valid UTF-8, trimmed, length ≤ 256.
- `EndpointID` is host[:port][/path-prefix] with no userinfo, query, fragment,
  padding, or trailing slash.
- Every tri-state is `unsupported`, `supported`, or `required`. Empty is
  invalid. Token fields may be 0 (unknown).
- `MaxTokensField` is `""`, `"max_tokens"`, or `"max_completion_tokens"`.

The only shipped preset is `openaicompat.ProfileTextOnly`. There is no
vendor-named helper and no Application/Engine switch on provider names.
`NativeTools=required` is rejected at `New`
(`TestNewRejectsInvalidConfig` / `native tools required`). The first adapter
does not send `tools`, images, `response_format`, or cache-control fields.

## First adapter: `internal/harness/adapters/openaicompat`

```go
type APIKeySource interface {
    APIKey() (string, error)
}

type EnvAPIKey struct{ Name string }     // os.Getenv at Stream time
type StaticAPIKey struct{ Value string } // tests; never logged

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

`New` performs no network I/O. It fail-closes on empty BaseURL/ModelID, nil
API key source, userinfo in the URL, non-loopback `http://` (including
`169.254.169.254` even when the flag is set), required native tools, invalid
profile/hints, or negative bounds (`TestNewRejectsInvalidConfig`). Loopback
`http://` is accepted only with `AllowInsecureLoopback`
(`TestNewAcceptsLoopbackHTTPWhenAllowed`). Injected clients and transports are
cloned; `http.DefaultClient` and `http.DefaultTransport` are never mutated
(`TestNewDoesNotMutateDefaultClientOrTransport`). Redirects are disabled:
`CheckRedirect` returns `http.ErrUseLastResponse`, so a 3xx is classified as
`provider_permanent` and is not followed (`TestCheckRedirectThreeXXIsPermanent`).

`Identity()` copies family `openai_compat`, ModelID, EndpointID derived from
BaseURL, the profile, and both wire hints (`TestIdentityCopiesProfileAndHints`).

`Stream` is safe for concurrent calls; each call owns one HTTP request
(`TestStreamConcurrentCallsOwnRequests`). Defaults: idle 60s, response-header
30s, request JSON 1 MiB, SSE line 256 KiB, `User-Agent: open-code-harness`
with no version.

Test composition helper `MustComposeHTTP` copies `Identity()` into
`Config.RequestIdentity`, sets `AllowInsecureLoopback` only for loopback
fixture hosts, and rejects non-loopback `http://`
(`TestMustComposeHTTPSetsLoopbackForFixtureServer`,
`TestMustComposeHTTPRejectsNonLoopbackHTTP`).

## Request mapping

`POST {BaseURL}/chat/completions` with `stream: true` and messages
`[{role:user, content: Input}]`. No hidden system prompt.

| Field | When sent | Test |
| --- | --- | --- |
| `stream_options.include_usage` | only if `Hints.IncludeUsage` | `TestStreamRequestMapping` / `include usage`, `omit usage` |
| `max_tokens` / `max_completion_tokens` | only if `MaxOutputTokens > 0` and the matching hint is set | `TestStreamRequestMapping` / `max tokens`, `omit max when tokens zero` |
| `Authorization: Bearer <key>` | required | `TestStreamRequestMapping` |
| `Accept: text/event-stream` | always | `TestStreamRequestMapping` |
| `User-Agent: open-code-harness` | default | `TestStreamRequestMapping` |

Missing or blank key is `CodeModelStartup` + `provider_auth` and does not send
HTTP (`TestStreamMissingAPIKeyIsAuth`). `EnvAPIKey` is read at `Stream` time,
not `New` (`TestEnvAPIKeyIsReadAtStreamTime`). Oversize JSON is
`provider_permanent` and is not sent (`TestStreamRejectsOversizeRequest`). A
canceled context does not send (`TestStreamCanceledContextDoesNotSend`).

## SSE mapping

Parse standard SSE `data:` lines. Extra JSON keys in recorded fixtures (`id`,
`object`, `created`, `model`) are ignored. `Content-Type` is accepted
case-insensitively after `mime.ParseMediaType`
(`TestStreamSuccessEmitsDeltasCompletedAndUsage` uses
`TEXT/EVENT-STREAM; charset=UTF-8`).

| Vendor payload | Adapter action | Test |
| --- | --- | --- |
| `delta.content` non-empty string | `text_delta` | `TestStreamSuccessEmitsDeltasCompletedAndUsage` |
| empty / role-only `content` | ignore | success fixture first chunk |
| `reasoning_content` / `reasoning` / `reasoning_details` | ignore; never enter assistant text | `TestStreamIgnoresReasoningContent`, `TestRunTurnHTTPReasoningIsolation` |
| `usage` object | `completed.Usage` / `AttemptStats.Usage` | `TestStreamSuccessEmitsDeltasCompletedAndUsage` |
| `input_tokens` / `output_tokens` / `prompt_cache_hit_tokens` | alternate field map | `TestStreamUsageAlternateFields` |
| fractional usage number | `CodeInvalidStream` + `invalid_stream` | `TestStreamRejectsFractionalUsage` |
| `finish_reason=stop` | `completed`, `FinishReason=stop` | `TestStreamSuccessEmitsDeltasCompletedAndUsage` |
| `finish_reason=content_filter` | `CodeModelStream` + `provider_permanent`; `FinishReason=""` | `TestStreamContentFilterAndToolCallsLeaveFinishReasonEmpty` |
| `finish_reason=tool_calls` or `delta.tool_calls` | `CodeInvalidStream` + `capability_mismatch`; `FinishReason=""` | `TestStreamContentFilterAndToolCallsLeaveFinishReasonEmpty` |
| empty completion | `CodeInvalidStream` + `empty_response` | `TestStreamEmptyCompletion`, `TestRunTurnHTTPEmptyCompletion` |
| non-string `content`, non-JSON `data:`, multiple `choices`, line over bound | `CodeInvalidStream` + `invalid_stream` | `TestStreamRejectsNonStringContentAndOversizeLine` |
| HTTP 200 whose type is not `text/event-stream`; HTTP 201 / 204 | `CodeModelStartup` + `provider_permanent` | `TestStreamNonSSEAndNon200TwoXXFailClosed` |
| idle while `Next` blocked | `CodeModelStream` + `provider_transient` | `TestStreamIdleTimeoutIsTransient` |
| connection drop during `Next` | `CodeModelStream` + `provider_transient`; no `io.EOF` | `TestStreamConnectionDropHasNoEOF` |
| `ctx` canceled | `CodeCanceled`; HTTP request aborted | `TestStreamCancelUnblocksNext`, `TestStreamCancelKeepsLatencyAndUsage` |

Usage field preference, pinned by the success and alternate-field fixtures:
`InputTokens` from `prompt_tokens` then `input_tokens`; `OutputTokens` from
`completion_tokens` then `output_tokens`; `CachedInputTokens` from
`prompt_tokens_details.cached_tokens` then `prompt_cache_hit_tokens`. A
present `x-request-id` is copied onto `AttemptStats.ProviderRequestID`
(`TestStreamSuccessEmitsDeltasCompletedAndUsage`). A present `x-ds-request-id`
on a pre-stream failure is copied onto `ProviderFailure.RequestID`
(`TestClassifyHTTPErrors`). Latency is the adapter HTTP span; `Snapshot` keeps
it after cancel (`TestStreamCancelKeepsLatencyAndUsage`).

Successful `completed` fixtures pin `AttemptStats.FinishReason=stop`. Fail and
cancel paths pin `""`. `content_filter` and `tool_calls` leave `FinishReason`
empty. `length` and `unknown` are codec-legal on `model.usage.recorded`; they
are not pinned by an adapter stream case.

## Errors and classification

The adapter returns `*engine.Error` whose `Code` stays in
`{CodeModelStartup, CodeModelStream, CodeCanceled, CodeInvalidRequest,
CodeInvalidStream}`. Classification lives on `engine.ProviderFailure`:

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

`ProviderFailure.Error()` renders only the durable code
(`TestProviderFailureErrorNeverRendersSecrets`). Application
`durableFailure` does `errors.As(primary.Cause, *ProviderFailure)` and, when
the code is allowed, persists `failure.Code` plus a fixed display sentence
(`TestRunTurnProviderAuthPersistsClassifiedFailureCode`). Scripted errors
without a `ProviderFailure` still persist the Engine code. Application
category stays `CategoryModel` for model Engine codes; it does not retry.

Pre-stream statuses the cited tests hit (`TestClassifyHTTPErrors`,
`TestCheckRedirectThreeXXIsPermanent`, `TestHTMLErrorBodyUsesStatusFallback`):

| HTTP status | Durable code | Class | Retryable |
| ---: | --- | --- | --- |
| 302 | `provider_permanent` | permanent | false |
| 401, 403 | `provider_auth` | auth | false |
| 429 (default) | `provider_rate_limit` | rate_limit | true |
| 429 + quota token/code | `provider_quota` | quota | false |
| 400/413 + overflow token/code | `context_overflow` | permanent | false |
| 400 without overflow | `provider_permanent` | permanent | false |
| 500, 502 | `provider_transient` | transient | true |

Quota override matches the closed substring/code lists in those 429 cases.
`Retry-After` is integer seconds or RFC1123, capped at 1 hour
(`TestRetryAfterRFC1123AndCap`). Dial/EOF with no status is
`provider_transient` and does not unwrap to `io.EOF`
(`TestDialFailureIsTransientStartup`). An HTML 502 body becomes `http_502`
(`TestHTMLErrorBodyUsesStatusFallback`).

Display sentences persisted on failed Item/Turn by the named Application
tests:

| Durable code | Message | Test |
| --- | --- | --- |
| `provider_auth` | `provider rejected credentials` | `TestRunTurnProviderAuthPersistsClassifiedFailureCode`, `TestRunTurnHTTP401PersistsProviderAuth` |
| `provider_quota` | `provider quota exhausted` | `TestRunTurnHTTP429QuotaVersusRateLimit` |
| `provider_rate_limit` | `provider rate limited` | `TestRunTurnHTTP429QuotaVersusRateLimit` |
| `provider_permanent` | `provider rejected the request` | `TestRunTurnContentFilterPersistsPermanentUsageWithoutFinishReason` |
| `empty_response` | `provider returned an empty completion` | `TestRunTurnHTTPEmptyCompletion` |

`Retryable` is advisory. `Service.RunTurn` does not loop. A 429 quota or
rate-limit turn performs one `Stream` (`TestRunTurnHTTP429QuotaVersusRateLimit`).

Secrets: `Authorization`, `Bearer `, and `sk-` must not appear in
`Error()`, `SafeMessage`, or marshaled events (`TestRedactionOnClassifiedMessages`,
`TestRunTurnHTTPSecretRedaction`).

## Log-only request and usage facts

New schemaVersion-1 events. Compact `Session` Apply is version-only
(`TestApplyModelFactsAreVersionOnly`, `TestApplyCompactModelFactsAreVersionOnly`,
`TestCompactDecisionsMatchFullStateForModelFacts`). EventStore v2 methods are
unchanged.

When `StartAssistantTurn.Request != nil`, one `Decide` emits:

```text
turn.started
assistant.message.started
model.request.recorded
```

(`TestDecideStartAssistantTurnWithRequestReturnsThreeEvents`). `Messages` must
be exactly `[{role:user, text:Input}]`; any other shape is
`CodeInvalidCommand` (`TestDecideStartAssistantTurnRejectsRequestMessages`).
`Request == nil` keeps the two-event scripted admission.

`RecordModelUsage` is a version-only command against the running item
(`TestDecideRecordModelUsageIsVersionOnly`). Application optionally
`Decide`s usage against the **same** running state, then `Decide`s the
terminal command, and concatenates one append
(`TestRunTurnPrependsObservedUsageBeforeTerminal`). Usage is prepended only
when `RequestIdentity != nil` and any of `Usage`, `FinishReason`,
`ProviderRequestID`, or `LatencyMs` is observed. Observed stats without
identity do not persist usage (`TestRunTurnObservedStatsWithoutIdentityDoNotPersistUsage`).

`model.request.recorded` and `model.usage.recorded` codecs are strict objects
with `DisallowUnknownFields` and the required key lists
(`TestModelFactEventJSONRejectsNonStrictPayloads`,
`TestRecordedEventJSONUsesCanonicalEncodingForAllPayloads`).
`finishReason` is `stop`, `length`, `unknown`, or `""`.

`DigestRunTurnRequestV1` still covers Session ID and exact UTF-8 input only
(`TestDigestRunTurnRequestV1FramesSessionAndInput`). Identity is not folded
into the digest.

## Reconstruction shapes

`ReconstructRequestResult` collects records by CommandID and accepts only
these shapes (`TestReconstructRequestResultAcceptsExactRequestShapes`):

```text
running scripted:   turn.started, assistant.message.started
running HTTP:       turn.started, assistant.message.started, model.request.recorded
terminal scripted:  admission pair + itemTerminal + turnTerminal
terminal HTTP, no usage:
                    admission pair + model.request.recorded + itemTerminal + turnTerminal
terminal HTTP, usage:
                    admission pair + model.request.recorded + model.usage.recorded
                    + itemTerminal + turnTerminal
```

Those are the 2 / 3 / 4 / 5 / 6-event shapes. Fail closed on usage after the
terminal pair, extra request, unknown same-CommandID type, mismatched
turn/item ids, or usage on a still-running request
(`TestReconstructRequestResultRejectsMisplacedCompanions`). Terminals are
found **by type**, not by index
(`TestDurableRequestTerminalErrorPreservesFailureAndInterruptionCode` /
`provider auth after usage`).

HTTP `RunTurn` through fixtures
(`internal/harness/adapters/openaicompat/runturn_test.go`):

| Case | Durable outcome | Shape |
| --- | --- | --- |
| success fixture | completed text `Hello world`; usage stop / request-id / latency | 6 events; replay reconstructs | `TestRunTurnHTTPSuccessRecordsRequestUsageAndReplay` |
| HTTP 401 | `provider_auth` | 5 events (no usage) | `TestRunTurnHTTP401PersistsProviderAuth` |
| HTTP 429 quota vs rate-limit | `provider_quota` / `provider_rate_limit`; one Stream | failed | `TestRunTurnHTTP429QuotaVersusRateLimit` |
| cancel after Stream entered | interrupted `caller_canceled`; one Stream | `TestRunTurnHTTPCancelWinsWithoutSecondStream` |
| empty completion | `empty_response` | `TestRunTurnHTTPEmptyCompletion` |
| reasoning fixture | completed `Visible`; no leaked reasoning | `TestRunTurnHTTPReasoningIsolation` |
| secret 401 body | redacted; persisted sentence only | `TestRunTurnHTTPSecretRedaction` |
| replay same Request ID | `FindCommandRequest` found; no second Stream | `TestRunTurnHTTPFindCommandRequestPreventsSecondStream` |

A content-filter style fail with observed latency persists
`provider_permanent` plus usage with `finishReason=""`
(`TestRunTurnContentFilterPersistsPermanentUsageWithoutFinishReason`).

## Keyless default tests

Default `go test` uses `StaticAPIKey{Value: "test-key"}` or `t.Setenv` and a
scripted transport. There is no keyless remote mode and no
`//go:build liveprovider` suite in this milestone. Recorded fixtures live
under `internal/harness/adapters/openaicompat/testdata/sse/`.

## Formal adapters and executable evidence

- `openaicompat.Model` is the first production `engine.Model` implementation.
- `testkit.ScriptedModel` remains the formal scripted adapter on the same
  port. `modeltest` still treats nil `Usage` as the scripted default.
- `MemoryEventStore` remains the EventStore v2 reference.
- Reusable suites `eventstoretest.Run`, `modeltest.Run`, and
  `enginescenariotest.Run` still pass on the scripted path.

Run the local evidence matrix from the repository root:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
```

Focused packages that enforce this contract:

```bash
go test ./internal/harness/domain ./internal/harness/engine \
  ./internal/harness/application ./internal/harness/adapters/openaicompat \
  ./internal/harness/architecture -count=1
```

## Explicit exclusions

This implemented contract does not provide:

- tools, Tool Runtime, Policy, approvals, MCP, or Engine `tool_call*` /
  `reasoning_delta` constants;
- reasoning-item persistence (reasoning fields are dropped);
- images, audio, video, or structured-output requests;
- prompt-cache layout or vendor cache heuristics;
- multi-provider routing, fallback, cost optimization, or Application retry;
- SQLite, JSONL, Runtime Host, ACP, TUI, or a plugin kernel;
- EventStore v2 interface changes;
- OAuth, model discovery, or vendor SDKs;
- hidden system prompts;
- reconstructable identity on the ScriptedModel path when
  `RequestIdentity` is nil;
- live-network or live-key CI.

These exclusions preserve dependency order. They do not weaken the verified
`engine.Model` adapter path, and they prevent this milestone from being
presented as a GA harness.
