# Provider Protocol Replay — DeepSeek First Slice

Status: implemented internal slice, experimental; not GA. Date: 2026-09-13.
The [Chinese reading copy](provider-replay.zh-CN.md) describes the same contract.
This supplements [Provider adapter](provider-adapter.md) and
[Startup extensibility](startup-extensibility.md); it does not publish a Provider SDK.

## Why this slice precedes more extension points

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

`domain.ProviderState` contains exactly `protocol`, `modelID`, `endpointID`, and
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
[the evidence ledger](provider-replay-evidence.md). No paid/live DeepSeek call has
been made. Local fixtures validate harness behavior, not the remote service's
current acceptance, tokenizer, latency, or output quality.

Next: implement Claude native Messages as the second real protocol, preserving
its own block/signature associations and model-specific history constraints.
Do not route it through `reasoningContent` merely because both are called
thinking. Exact original thinking blocks and their signatures are a separate
contract. [Source: Claude extended thinking](https://platform.claude.com/docs/en/about-claude/models/extended-thinking-models).
Only after two real implementations should we settle a shared replay envelope
and consider a public Provider SDK. Stable promotion still needs an actual
external consumer; a repository-owned example does not satisfy that gate.
