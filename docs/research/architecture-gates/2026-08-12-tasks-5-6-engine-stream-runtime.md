# Tasks 5–6 Architecture Gate: Engine Stream and Runtime Boundary

- Date: 2026-08-12
- Scope: Engine plan Tasks 5–6; load-bearing implications for Tasks 7–10
- Initial verdict: **READY_WITH_AMENDMENTS**
- Final verdict after incorporation: **READY**
- Implementation state at review: not started

## Evidence

| Project | Primary evidence | Relevant behavior | Decision for Open Code Harness |
| --- | --- | --- | --- |
| OpenAI Codex | [`app-server/README.md`](https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md), [`common.rs`](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/common.rs), [`responses_websocket.rs`](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/responses_websocket.rs), [`compact.rs`](https://github.com/openai/codex/blob/main/codex-rs/core/src/compact.rs) | An item has `started -> zero or more deltas -> completed`; completed is authoritative. The typed response stream reports an error if transport ends before `response.completed`, and consumers stop on explicit completion. Codex also uses internal channels, transport tasks, retries, and fallback. | Adopt explicit completion and premature-EOF failure. Do not infer that Engine should expose push callbacks, channels, detached work, or hidden retries. |
| Kimi Code | [`AGENTS.md`](https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md), [`packages/transcript/AGENTS.md`](https://github.com/MoonshotAI/kimi-code/blob/main/packages/transcript/AGENTS.md), [wire mode](https://moonshotai.github.io/kimi-cli/en/customization/wire-mode.html) | Transcript owns its contract and cold rebuild source; recorded operations retain order and scoped sequence. Wire replay is read-only and ordered. On interruption `TurnEnd` may be absent, and retry can supersede partial output. | Adopt contract ownership, scoped monotonic order, and transcript/runtime separation. Reject its permissive interrupted ending and retry semantics for the strict ModelStream grammar. |
| Maka | [`ARCHITECTURE.md`](https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md) | Runtime Host is the execution authority; the Runtime Event Log is canonical for messages, tool results, and termination facts, while UI, recovery, and context are projections. | Adopt one execution authority and durable facts versus transient delivery signals. The source does not define pull streaming, Close, UTF-8, or byte limits. |
| Pi | [`agent-loop.ts`](https://github.com/badlogic/pi-mono/blob/main/packages/agent/src/agent-loop.ts), [`types.ts`](https://github.com/badlogic/pi-mono/blob/main/packages/agent/src/types.ts), [`agent-session.ts`](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/src/core/agent-session.ts) | The loop injects its stream function, propagates `AbortSignal`, consumes an async iterable, and awaits internal lifecycle delivery. The public wrapper starts detached async work; AgentSession also owns retries, compaction, and listeners with different awaiting rules. | Adopt dependency injection, explicit cancellation, ordered lifecycle, and awaited delivery. Reject the detached push wrapper and broad session policy from the synchronous Engine boundary. |
| MiniMax Mini-Agent | [`MiniMax-AI/Mini-Agent`](https://github.com/MiniMax-AI/Mini-Agent), [`agent.py`](https://github.com/MiniMax-AI/Mini-Agent/blob/main/mini_agent/agent.py) | The official MiniMax demo injects an LLM client and runs a bounded step loop, but uses provider-specific unary `generate`; cancellation is checked at step boundaries. | Keep only the small, injectable, bounded-loop lesson. It supplies no ModelStream, Close, UTF-8, or delivery contract. |
| DeepSeek-Reasonix | [`docs/ARCHITECTURE.md`](https://github.com/esengine/DeepSeek-Reasonix/blob/main/docs/ARCHITECTURE.md) | The architecture separates immutable prefix, ordered append-only history, and volatile scratch, and adds provider-specific cache repair, retries, and model escalation. | Adopt only ordered history versus transient scratch as an analogy. Reject cache/repair/retry behavior from Engine. This is an `esengine` community project, not an official DeepSeek repository. |
| Go standard library | [`io`](https://pkg.go.dev/io), [`errors`](https://pkg.go.dev/errors), [`context`](https://pkg.go.dev/context), [`unicode/utf8`](https://pkg.go.dev/unicode/utf8) | EOF is a successful end only when the protocol permits it; `errors.Is` traverses single and joined unwrap trees; Context carries cancellation across API boundaries; `utf8.ValidString` validates complete strings. | Use explicit completion for the structured stream, full-tree error matching, propagated cancellation, and per-delta UTF-8 validation. Define Close ownership ourselves because `io.Closer` leaves behavior after the first call unspecified. |

The compared systems do **not** publicly establish all of our required guarantees: exactly-once Close and its error precedence, pre-delivery byte bounds, no detached Engine work, typed-nil-safe code lookup, or failing-sink recording semantics. Those are local contracts and must not be attributed to the references.

## Findings and required amendments

### 1. Emitter owns correlation and attempt order

Callers submit a `RuntimePayload`, not a fully stamped `RuntimeEvent`. The Emitter
validates its immutable Session/Turn/Item/Command correlation at construction,
then stamps every sink call with that correlation and the next ordinal. Caller
supplied correlation or ordinal is rejected; it is never trusted or silently
mixed with Emitter state.

Ordinals are one-based, local to one command attempt, and allocated immediately
before the sink attempt. A failed sink call consumes its ordinal; a later
attempt is `N+1`. An invalid payload or cancellation detected before the attempt
consumes none. An Emitter is single-run, non-copyable after use, and not safe for
concurrent calls. Its ordinal is a delivery-attempt order, not a durable stream
sequence or global clock.

### 2. Centralize RuntimePayload and RuntimeEvent validation

Validation occurs before stamping and before the sink is called:

- `started`, `completed`, and `append.completed` require empty Text and Code;
- `text_delta` requires non-empty valid UTF-8 Text and empty Code;
- `failed` and `interrupted` require empty Text and a stable Code of 1–64 ASCII
  bytes: first `[a-z]`, then only `[a-z0-9_]`;
- `diagnostic` is deferred unless Tasks 5–6 define a real consumer; if retained,
  it requires a non-empty stable Code and valid UTF-8 Text;
- an unknown runtime type is a caller contract error, not provider
  `invalid_stream`.

The RuntimeSink sees only a fully validated, fully stamped RuntimeEvent.

### 3. Empty model deltas are invalid

`StreamEvent{Type: text_delta, Text: ""}` returns `invalid_stream`. It is not
delivered, accumulated, or assigned a runtime ordinal. Provider keepalives and
empty transport chunks are filtered by the adapter. An empty final assistant
answer remains valid and is represented by an immediate `completed` event.

The model grammar is exactly `text_delta* -> completed`. EOF before completed,
unknown event types, or non-empty Text on completed are `invalid_stream`. The
Runner stops calling Next immediately after completed; it cannot probe for or
claim to detect latent events after completion, so adapters must contractually
emit none.

### 4. Close every acquired stream exactly once and preserve error precedence

Every non-nil stream returned by Model.Stream is owned by the Runner and closed
exactly once, including `(stream, err)`, started-delivery failure, Next failure,
EOF, invalid event, cancellation, output-limit failure, and success. A nil
stream is never closed. On a failure path the derived context is canceled before
Close; after explicit completion the Runner closes first, then cancels, so it
does not manufacture a cleanup failure.

If explicit completed is otherwise successful and Close alone fails, the run
fails with `model_stream`; success is not committed yet. If a primary failure
already exists, Close never replaces its stable code. Preserve both causes
under one outer `engine.Error` whose Code is the primary code and whose Cause is
`errors.Join(primaryCause, closeCause)`. Do not join separately coded Engine
errors, which would make `IsCode` ambiguous.

`Close() error` has no context, so the adapter contract must require prompt,
synchronous teardown and joining of any adapter-owned transport work. Engine
itself starts no channel or goroutine and leaves no work alive after Close.

### 5. Define every Model.Stream and ModelStream.Next value/error pair

| Call result | Required outcome |
| --- | --- |
| `Stream(non-nil, nil)` | Acquisition succeeds; install exactly-once Close ownership immediately. |
| `Stream(nil, nil)` | `invalid_stream`; no Close. |
| `Stream(nil, err)` | `model_startup`, unless caller context is canceled, then `canceled`. |
| `Stream(non-nil, err)` | The event source is unusable, but the Runner owns and closes it once; primary is `model_startup` or `canceled`. |
| `Next(event, nil)` | Validate and process the event according to the grammar. |
| `Next(any value, err)` | Ignore the value completely; never deliver or accumulate it. Context cancellation wins, EOF maps to `invalid_stream`, and other errors map to `model_stream`. |

A poisoned `text_delta + error` and `completed + error` must therefore produce
no output and no completion. Check cancellation before Stream, each Next, event
processing, and sink delivery; when a dependency returns an error and the
caller context is already canceled, `canceled` is the primary code.

### 6. Enforce the UTF-8 byte boundary before side effects

For each model delta, perform this order: non-empty check, `utf8.ValidString`,
remaining-byte-limit check, RuntimeSink delivery, exact builder append. A delta
that would exceed the byte limit is neither delivered nor accumulated; exactly
the limit is valid. Accepted strings are concatenated byte-for-byte without
trimming, normalization, replacement, or rechunking.

Per-delta validation deliberately means an adapter may not split one UTF-8 code
point across StreamEvents. The size check must avoid overflow, for example
`len(delta) > limit-builder.Len()`, after proving the current length is within
the configured limit.

### 7. Make IsCode full-tree and typed-nil safe

`IsCode` must find a matching Engine error in every branch and depth of an
`errors.Join` tree. A single `errors.As` is insufficient because an earlier
Engine error with a different code can hide a matching sibling. Prefer
`errors.Is(err, &Error{Code: wanted})` with a code-aware `Error.Is` method and
nil-receiver-safe `Is`, `Unwrap`, and `Error` methods, or an equivalent explicit
tree walk.

Tests cover the match in the second/middle sibling, nested joins, the first
Engine error carrying the wrong code, a direct typed-nil `*Error`, a typed nil
inside `errors.Join`, ordinary nil, and an invalid requested code. No path may
invoke a method that dereferences a typed-nil receiver.

### 8. RecordingSink separates Attempts from Delivered

Under one mutex, RecordingSink first records the fully stamped event in
`Attempts`, then evaluates deterministic failure injection. A failing call is
present in Attempts but absent from Delivered; a successful call appears in
both. Both snapshots are defensive copies.

`FailOrdinal` means a one-shot failure on the first matching attempt and is
consumed under the same mutex. A sink with nonzero `FailOrdinal` is scoped to
one Emitter because ordinal 1 can legitimately exist in multiple command
attempts; with failure injection disabled it may be shared. This preserves
evidence of what the Engine tried without falsely claiming failed delivery.

### 9. State the concurrency boundary and prove the exercised cases

A shared Model may receive concurrent Stream calls. Each returned ModelStream
has one consumer: no concurrent Next/Close, no reuse across turns, and no Next
after completed. ModelRequest and recorded fixture data use defensive copies.
A shared production RuntimeSink must be safe for calls from different Emitters;
otherwise the adapter must expose an explicit per-run factory. Inline Emit waits
for the sink and provides backpressure.

Contract tests must cover concurrent independent Stream acquisitions, exact
request capture, cancellation barriers, single-consumer streams, Close counts,
Emitter ordering, defensive snapshots, and sink failure. Run those cases with
the Go race detector, while recognizing that it detects races only on executed
paths and does not replace the written ownership contract.

## Adopt, reject, defer

| Decision | Contract |
| --- | --- |
| Adopt | Consumer-owned Model/ModelStream/RuntimeSink ports; synchronous pull; inline sink backpressure; explicit completed; premature EOF failure; Emitter-owned correlation and ordinal; exact valid UTF-8 bytes; pre-delivery bounds; exactly-once cleanup; durable terminal facts separated from transient runtime delivery; formal adapter contract suites. |
| Reject | Push callback/channel as the Engine API; Engine-owned detached goroutines; caller-stamped identity/order; empty delta; EOF as success; completed payload; delivery or accumulation before validation/bounds; ignored or repeated Close; Close replacing an existing primary code; persisted token deltas; global runtime ordinal; hidden retry/cache/repair/fallback; concurrent consumption of one stream. |
| Defer | Production provider adapters and their bounded internal transport queues; retry/attempt policy; prefix cache and repair; tool/reasoning/usage events; persisted runtime log, catch-up cursor, and global sequence; `Close(ctx)` or cleanup timeouts; diagnostic events without a current consumer. |

## Final Tasks 5–6 contract

Tasks 5–6 may begin only after the accepted design and English/Chinese plans
carry these amendments:

1. Replace caller-populated RuntimeEvent input with validated RuntimePayload and
   Emitter-owned correlation/ordinal semantics.
2. Make empty delta and all event-field combinations explicit in the grammar.
3. Define exactly-once Close ownership and primary-versus-cleanup precedence.
4. Specify every Stream/Next value-plus-error combination.
5. Apply valid UTF-8 and byte limits before delivery and accumulation.
6. Make IsCode traverse full joined trees safely in the presence of typed nils.
7. Split RecordingSink Attempts from Delivered and define one-shot failure.
8. State Model, ModelStream, Emitter, and RuntimeSink concurrency ownership and
   cover exercised paths with race-enabled contract tests.
9. Keep runtime deltas transient and all retry/cache/repair policy outside this
   Engine milestone.

With these amendments incorporated, the gate is **READY**.
