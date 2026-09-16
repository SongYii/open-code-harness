# Provider Protocol Replay — DeepSeek Chat Completions and Messages

Status: implemented internal routes, experimental; not GA. Updated: 2026-09-14.
The [Chinese reading copy](provider-replay.zh-CN.md) describes the same contract.
This supplements [Provider adapter](provider-adapter.md) and
[Startup extensibility](startup-extensibility.md); it does not publish a Provider SDK.

The 2026-09-15 [internal closure](provider-internal-closure-evidence.md) runs
both HTTP adapters through shared chunk-independent semantic tests. Messages
still validates the complete native response before publishing output; typed
state and usage rules are unchanged. Composition now owns model-wide close
after stream drain, including startup rollback, closing only private HTTP pools.
Borrowed transports remain caller-owned. No replay schema, request body or
SDK extension was added by that work.

Local follow-up tests also distinguish concurrent Messages private state,
including rechecking A's retained output after B completes. Real stock-launcher
EOF/SIGTERM cases cover in-flight conversation and automatic summary, followed
by a second real launcher loading/recovering the same durable session. See the
[verification matrix and negative controls](provider-internal-closure-evidence.md).

## Why this slice precedes more extension points

The 2026-09-16 [HTTP2 shutdown repair](provider-http2-shutdown-evidence.md)
applies equally to Messages and Chat: explicit private socket ownership after
stream drain replaces idle-only sweeping. Pending dials and cleanup errors are
accounted for. It changes no replay state, request body or durable event schema.

The first missing capability was not another factory registry. The existing
message representation discarded state that a real provider requires to continue
a tool-using conversation. DeepSeek's current documented Chat Completions
contract requires previous assistant `reasoning_content` when tools are supplied,
including earlier replies that did not call a tool. Without tools the server
ignores that history field. The first implementation follows that concrete
contract, not a universal reasoning-block abstraction.
[Source: DeepSeek thinking mode, checked 2026-09-13](https://api-docs.deepseek.com/guides/thinking_mode/).

The architectural advantage remains one durable history, one application tool
loop, and one lifecycle owner. Protocol adapters translate that history; they
do not retain an independent transcript or gain write access to the EventStore.
One protocol implementation is not evidence of a general plugin architecture.

## Activation and identity

Opt in with `-provider-adapter deepseek`, using the existing endpoint, model,
credential-environment and budget flags. `composition.Provider.AdapterKind` and
eval `provider.adapterKind` accept `deepseek`. Empty/`openaicompat` retain the
legacy route. No URL or model-name heuristics select a protocol.

```sh
go run ./cmd/och -provider-adapter deepseek \
  -provider-url https://api.deepseek.com -model YOUR_MODEL \
  -api-key-env DEEPSEEK_API_KEY -context-window YOUR_CONTEXT_LIMIT \
  -max-output YOUR_OUTPUT_LIMIT
```

This schematic command requires the ordinary workspace/database configuration
and real, model-specific limits; it is not a live-API test or consent to spend.
Do not place credentials in argv. Use a new session and a verified backup.

This route explicitly sends `thinking.type=enabled`, records adapter family
`deepseek_thinking_v1`, and advertises supported reasoning fields. The optional
`-provider-thinking-mode enabled` is equivalent. Native effort controls accepted
in this slice are empty (provider default), `low`, `high`, and `max`. Conversation
and summary effort may differ; neither `none` nor silently disabling thinking
mid-session is supported. No portable-effort alias mapping is inferred.
Eval digest binds the adapter selection; ACP flags and in-process configuration
map it identically. Existing default-route argv and durable event bytes remain
unchanged when no new state is present.

## Ownership and completion

| Boundary | Contract |
|---|---|
| HTTP/SSE adapter | Assemble reasoning deltas separately from text and tool calls. No provider-owned history. |
| Engine | Only `completed` may carry `ProviderState`; validate and detach it. Failure, cancellation and Close failure clear the result state. No reasoning runtime event. |
| Application | Match state against configured protocol/model/endpoint; apply the secret-shape rejection gate; commit it atomically with `AssistantMessageCompleted`, before executing offered tools. |
| Domain/EventStore | Typed optional field on completed assistant and recorded request messages, strict nested codec, detached clones, existing transaction/audit machinery. |
| Context Engine | Retain state on retained assistant messages, count its wire content in estimates, and cover it together with its source event. Summary rendering never reads the field. |
| Outgoing adapter | Replay every retained assistant's reasoning when tools are present. Missing, unknown or foreign state fails before HTTP; never invent empty reasoning or silently strip state to switch routes. |

For the Chat Completions route, `domain.ProviderState` contains exactly `protocol`, `modelID`, `endpointID`, and
`reasoningContent`. There is no `map[string]any`, arbitrary header bag, event
pointer, executable capability, or new public SDK type. The route binding is
validated, not sent to the model. Empty reasoning explicitly returned by the
provider is representable; an absent field is not equivalent.

One assistant's reasoning is bounded to 256 KiB of UTF-8, independently of the
visible assistant byte limit. Exceeding it fails rather than truncating. Existing
request/projection caps still apply. The opt-in SSE path also bounds one
multiline event to 1 MiB and 1,024 data lines, in addition to the scanner's line
limit. Unpaired JSON Unicode surrogates are rejected rather than replaced during
decoding. This slice requires reasoning, a `stop`/`tool_calls` finish and `[DONE]`;
length termination, missing terminal markers and message-snapshot/delta mixtures
cannot publish replay state. This stricter completion rule does not alter the
legacy SSE route.

## Compaction, recovery, and sensitive data

State is recovered from committed events, including across SQLite close/open.
The existing planner retains complete turns, so an active tool chain is not
split. Rolling summary/reset replaces only the covered prefix; retained
assistant messages keep their original state. A summary/reset marker is a user
message, not a synthetic assistant missing provider state. Source digests include
the full canonical completed event, hence include protocol state automatically.
Neither compression nor checkpoint validation rewrites old canonical events.

`och_wire_estimate_v1` retains its old arithmetic for old message shapes. The
new shape adds eight framing tokens plus ceil(reasoning UTF-8 bytes / 3).
The replay binding metadata is not model input. The bare-message estimator is
conservative even without tools; it is not a provider tokenizer. Provider usage
remains the actual usage evidence. Summary prompts render ordinary message text
and existing framing only, not protocol state.

Reasoning is sensitive even though it is not displayed. Canonical SQLite events,
recorded requests, backups and canonical audit replicas contain it as plaintext
under the existing storage protection model. This slice adds no encryption or
general secret detector. It must not be copied into ACP output, runtime text,
display transcripts, metrics or trace attributes. Display transcript export is
therefore not a replay backup. No OTel implementation is changed by this slice.

The existing `redact.Text` shape scanner runs as a rejection check: if it would
change reasoning, the completion fails instead of persisting a modified protocol
payload. This is enforced in the adapter and at the application completion gate;
it does not promise detection of arbitrary credentials or sensitive content.
Visible-text/tool-result redaction behavior is unchanged.

## Compatibility and rollback

The current event envelope schema remains version 1, with a strictly validated,
optional `providerState` field. Nil omits it entirely; existing golden codec tests
continue to pin legacy bytes. New readers accept both shapes. Old strict readers
reject events containing the new field. Protocol identity is independently
versioned as `deepseek_thinking_v1`; this is not a promise that old executables can
read new opt-in histories. Experimental status does not waive audit integrity.

Before first use, make and verify a backup that includes the required canonical
store/audit recovery material. Rollback to an old reader must use that pre-feature
backup (losing subsequent work), or use a reader that understands the new format.
Merely switching the adapter flag back does not migrate stored history. Never
strip fields, rewrite audit chains, or delete protocol state to make an old reader
accept the database. Existing assistant history without required state cannot be
losslessly upgraded; use a new session. Changing model, endpoint or protocol is
also not an implicit migration. No lossy migration workflow is implemented.

## Evidence and next gate

The Messages route below extends this evidence; descriptions above of
`reasoning_content`, `[DONE]`, and its four-field state apply to Chat Completions.

Deterministic tests cover strict nested codec/legacy omission, null and duplicate
fields, unknown versions, byte/role bounds, clone isolation, completion-only engine
state, secret rejection, missing/foreign history rejection before HTTP, fragmented
and empty reasoning, non-tool assistant replay, and incomplete streams.
`TestDeepSeekThinkingToolsRestartAndCompaction` uses actual composition, SQLite,
HTTP/SSE, a workspace tool, rolling summary, and two close/open cycles. It compares
the exact replayed state set after compaction with committed history beyond the
checkpoint coverage, and asserts summary/display export isolation.
Cold consistent audit export is independently verified and compared event for
event with the canonical SQLite history, including protocol-bearing requests.

Verification commands and the observed final results belong in
[the evidence ledger](provider-replay-evidence.md). The 2026-09-14 Messages run
gave partial acceptance; the authorized 2026-09-15 follow-up passed the nonempty
native assistant-tail gate across compaction/reopen, including exact outgoing
blocks, remote continuation and independent audit verification. This is bounded
acceptance, not a reliability, tokenizer or quality certification. An uncaptured
first-run stream failure remains unexplained. Chat Completions evidence here
remains fixture-based.

Next: separately review native Claude's model-specific request-prefix constraints
and investigate the earlier stream failure if it recurs. Neither fixtures nor
this bounded live run certify general service reliability. A public Provider SDK remains deferred;
stable promotion still requires an actual external consumer, not our own example.

The Messages route also has an eight-case local in-flight lifecycle matrix for
turns/manual summaries: success, caller cancellation, Close and real heartbeat
fencing after SQLite lease expiry. It checks late-output isolation, canceled
requests, durable termination/recovery and successor ownership. This does not
replace process-kill tests at persistence boundaries; see the evidence ledger.

Six real process-kill boundaries and matching completion controls now cover
pre-commit assistant completion, tool execution before/after its side effect,
summary checkpoint COMMIT before/after, and canonical COMMIT before audit export.
Natural-expiry takeover, durable request retry, repeated restart and cold audit
comparison passed with race. The tests exposed and fixed recovery emitting an
assistant interruption for a tool Item, and request reconstruction rejecting
`context.prepared` / `process_crash`. An ambiguous tool result is interrupted,
never automatically re-executed; external exactly-once effects are not promised.
Follow-up evidence adds kills during the recovery transaction and scripted
Application mid-turn compaction/overflow-retry reconstruction. Further coverage
adds mixed multi-session recovery kills and native Messages mid-turn checkpoint
kills. Native Messages now recognizes a narrowly defined HTTP 400 context overflow
and delegates bounded recovery to the existing core, as specified below. Other
statuses and ambiguous bodies remain permanent rejections where applicable.
Three further kills cover overflow checkpoint COMMIT before/after and retry
completion before COMMIT. Other startup stages, live overflow certification and
export-publication substeps remain separate gates. See the
[evidence ledger](provider-replay-evidence.md) for scope and negative controls.

## DeepSeek Messages route (2026-09-14)

Select `-provider-adapter deepseek-messages` and
`-provider-url https://api.deepseek.com/anthropic`, with the actual DeepSeek model
name, credential environment variable, and explicit model limits. This appends
`/v1/messages`. Use a new session and a verified backup; changing routes cannot
migrate existing replay history. Eval and ACP/in-process configuration support
the same explicit selection. The default route is unchanged.

The route uses `anthropic-sdk-go v1.72.0` (MIT) for typed requests, HTTP execution
and indexed content accumulation. No SDK types, environment credential loader,
automatic retry, redirect, SDK tool runner, or SDK-owned history enter the core.
Only HTTP 200 with `text/event-stream` is accepted as a model stream. Rejected
bodies are closed without reading, except bounded inspection of HTTP 400 JSON
for the overflow contract below. SDK errors containing request/response bodies are replaced
with safe classified errors. Private HTTP transport has 30-second header and
10-second TLS timeouts, a 64 KiB response-header limit, and a 60-second body-read
idle timeout. Cancellation closes the response; Close is once-only, and its
failure prevents completion. There is no decoder goroutine.

### HTTP context overflow recognition (2026-09-15)

This is an internal, experimental route behavior, not a new plugin or SDK API.
Only HTTP 400 with parsed `application/json` is inspected, before any SSE stream
is admitted. The adapter reads at most 4 KiB plus one oversize sentinel byte and
requires EOF within an absolute two-second deadline, not a resettable idle timer.
Cancellation/deadline closes the body exactly once; read/close failure, timeout,
oversize, truncation, duplicate JSON keys or invalid Unicode declines recognition.
Injected transports must unblock Read on Close, as for streaming. No error-body
text or request ID is retained in errors, events, runtime output or telemetry;
the adapter returns only a stable code, HTTP status and fixed safe message.

Recognition requires a closed JSON error object with `type=invalid_request_error`:
optional `code` is null or that same type; optional `param` is null. Root `type`,
if present, must be `error`; optional `request_id` must be a string and is ignored.
Unknown fields decline recognition. A message must be an exact supported form:
the short Messages prompt-too-long diagnostic, its numeric tokens/maximum form,
or the full numeric DeepSeek maximum-context diagnostic. Numeric forms validate
positive uint64 counts and actual overflow; DeepSeek totals must equal input plus
output, with output alone below capacity. Quoted fragments, arbitrary suffixes,
generic `max_tokens` text and invented codes do not match. 413 is a byte-size
rejection, not a token-limit diagnosis; neither 413 nor 422 enters this parser.

Evidence is deliberately separated: [DeepSeek's compatibility guide](https://api-docs.deepseek.com/guides/anthropic_api/)
documents the Messages route, while [Anthropic's error reference](https://platform.claude.com/docs/en/api/errors)
defines the JSON envelope and 413 semantics, and its [context-window guide](https://platform.claude.com/docs/en/build-with-claude/context-windows)
documents the 400 prompt-too-long diagnostic. The numeric DeepSeek variant comes
from a [first-hand report in DeepSeek's repository](https://github.com/deepseek-ai/DeepSeek-V3/issues/1102),
not an official guarantee or a live probe performed here. Unknown future variants
fail closed; this support does not certify every current DeepSeek error response.

Recognized rejection becomes `CodeModelStartup` / `context_overflow`, with adapter
`Retryable=false` and SDK retries disabled. Only Application may force compaction,
require at least 10% estimated request reduction, persist a fresh decision and
consecutive attempt, and retry within `MaxOverflowCompactionsPerTurn`. Summary
failure/exhaustion is terminal; an HTTP 200 SSE error is a stream failure and
never promoted to pre-delta overflow. Recovery does not lower model limits, edit
old events or introduce a provider-owned retry/tool loop.

### Completion and replay integrity

The SDK does not supply our integrity checks: raw SSE framing remains bounded
(256 KiB line, 1 MiB/1,024 data lines per event, 8 MiB total). Duplicate keys,
unpaired Unicode surrogates, unsupported event/block fields, bad block lifecycles,
and malformed tool input fail before SDK repair. SDK owns text/thinking/signature
accumulation; only raw tool fragments remain independently buffered because its
`{}` sentinel and refresh logic can otherwise reset or repair arguments. Multiple
open blocks may receive interleaved deltas/stops by index. Completion requires
every block closed, a supported `end_turn` or `tool_use`, and `message_stop`.
Nothing is emitted to Engine before those checks and response Close succeed.
Attempt stats normalize these reasons to the existing `stop`/`tool_calls` values.

Durable protocol `deepseek_messages_v1` uses a closed `messagesContent` union:
ordered `text`, `thinking`, and `tool_use` blocks. Thinking preserves genuinely
absent versus empty signatures; no signature is fabricated. `redacted_thinking`
is rejected for this route. This follows the documented DeepSeek compatibility
profile, not Claude signature or request-prefix requirements.
[DeepSeek compatibility reference](https://api-docs.deepseek.com/guides/anthropic_api/).
The legacy `reasoningContent` field remains present but must be empty; older
Chat Completions bytes remain unchanged and cannot contain `messagesContent`.

Limits are 256 blocks, 1 MiB assembled content, 256 KiB combined thinking and
signatures, 32 KiB per tool input, JSON depth 64, and a 5 MiB request. Reject,
never truncate. Domain validates active variants, exact visible-text projection,
and ordered tool ID/name/JSON-value projection (numbers are not rounded through
float64), on commands, events, Apply and recorded requests. Clones detach arrays,
raw arguments and optional signature pointers. Engine also checks projection at
completion. The existing application transaction still precedes tool execution.

Requests use top-level system blocks, ordered native assistant blocks, and
grouped tool results in the following user message. Missing/foreign state,
projection mismatches, unmatched tool results, and response-model aliases fail
closed. No automatic model alias mapping is assumed. Thinking is explicitly
enabled; only empty/low/high/max effort is supported, via `output_config.effort`.
DeepSeek ignores `budget_tokens`, so it is omitted. `max_tokens` is always sent;
Chat Completions `include_usage` and `max_completion_tokens` hints are rejected.

The existing full-turn retention and rolling-summary/reset mechanisms apply.
Hidden thinking/signatures and block framing are estimated in addition to the
existing visible text/tool estimate, without counting visible content twice;
this is not a native tokenizer. Summary and display export omit replay blocks.
Canonical SQLite/request/audit records contain sensitive blocks in plaintext
under the existing storage protections. Secret-shape checks reject rather than
rewrite thinking/visible text; opaque signatures are never interpreted or
redacted. This does not promise general secret detection or encryption.

Rollback restrictions above apply again: readers predating this protocol cannot
read it. Do not strip blocks or rewrite audit chains. Native Claude compatibility,
Claude prefix-preserving context transforms, general live reliability/quality,
and public Provider SDK stability are **not** established by this implementation.

Messages usage normalizes uncached + cache-read + cache-created input to the
Engine's total-input counter; cache-read remains a subset. Overflow fails before
any output is admitted. This corrects the undercount found during bounded live
acceptance; historical audit events are not retroactively rewritten.
