# Claude native replay: protocol evidence and integration gates

Date: 2026-09-14. Status: research complete for this bounded investigation;
Claude-specific implementation **in progress**. This is not an implemented
Claude provider contract or approval of a changed compaction policy. The
[Chinese reading copy](2026-09-14-claude-native-replay.zh-CN.md) summarizes the
same findings. Starting repository revision: `3ff5077`.

Follow-up decision: the operator selected DeepSeek's official Anthropic-compatible
endpoint as the first integration target. The implementation approach is now
**SDK reuse first**, subject to local conformance checks; the hand-written
accumulation below is now replaced by SDK-assisted indexed accumulation.
The [pinned SDK conformance probe](../../../experiments/anthropic-sdk/README.md)
now runs against v1.72.0 with an isolated dependency manifest and shared fixtures.
The subsequent experimental `deepseek-messages` route is now implemented in the
main module. Its current authority is the [replay contract](../../architecture/provider-replay.md)
and [evidence ledger](../../architecture/provider-replay-evidence.md). The original
prototype observations below are historical evidence, not a description of the
current production route. Native Claude prefix-binding remains open. After the
initial partial DeepSeek Messages acceptance, an authorized September 15 follow-up
passed the nonempty-retained-tail wire gate and full bounded Composition probe.
The unexplained first-run stream failure and remaining limits are in the evidence ledger.

## Conclusion

Keep the single event history, application-owned tool loop, and host lifecycle.
Do not generalize DeepSeek's `reasoningContent` into a universal thinking string.
Claude requires ordered blocks, and some routes also require preservation of the
request prefix that produced those blocks. This exposes a context-management
constraint, not merely another HTTP endpoint to register.

No Provider SDK is being published. The implemented DeepSeek Messages route is
not native Claude certification, an external consumer, or sufficient evidence
to promote a public SDK.

## Selected target and reuse investigation

Use `https://api.deepseek.com/anthropic` as the base URL. The current official
example uses `deepseek-flash`; use the actual DeepSeek model name, not a Claude
alias that the server remaps. DeepSeek documents streaming, thinking, and client
tools, but does not support `redacted_thinking`; it ignores `budget_tokens` and
tool-result `is_error`. These differences belong in a named compatibility profile,
not in a claim of full Claude compatibility.
[DeepSeek's compatibility table, checked 2026-09-14](https://api-docs.deepseek.com/guides/anthropic_api/).

The document does not establish signature presence/non-emptiness or Claude-style
prefix binding for DeepSeek. Verify those on this route before requiring them.
Do not fabricate a signature to satisfy our prototype, or impose Claude's
model/account constraints on DeepSeek without evidence. Live calls still need
separate permission and a budget; selecting an endpoint is not spending consent.

Existing implementations inspected:

| Implementation | Concrete reusable precedent | Choice for this repository |
| --- | --- | --- |
| [Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go/blob/main/messageutil.go) | `Message.Accumulate`, ordered/indexed blocks, signature accumulation, `ToParam` replay conversion | Preferred transport/types/assembly candidate; retain our admission checks |
| [Fantasy Anthropic adapter](https://github.com/charmbracelet/fantasy/blob/main/providers/anthropic/anthropic.go) | Uses the official Go SDK, preserves reasoning metadata and groups tool results into user messages | Reference native request mapping; do not import its whole agent loop |
| [Vercel AI SDK Anthropic converter](https://github.com/vercel/ai/blob/main/packages/anthropic/src/convert-to-anthropic-prompt.ts) | Maps reasoning metadata back into thinking/signature and redacted blocks | Independent TypeScript reference for mapping/tests, not a runtime dependency |

Fantasy has a real application consumer: [Crush's dependency manifest](https://github.com/charmbracelet/crush/blob/main/go.mod)
includes it. These are implementation precedents, not evidence that an external
consumer has adopted **our** SDK. The inspected licenses are
[MIT for the Anthropic SDK](https://github.com/anthropics/anthropic-sdk-go/blob/main/LICENSE),
[Apache-2.0 for Fantasy](https://github.com/charmbracelet/fantasy/blob/main/LICENSE),
and [Apache-2.0 for Vercel AI SDK](https://github.com/vercel/ai/blob/main/LICENSE).
No upstream code was copied and no production dependency was added. The subsequent
conformance probe pins its test dependencies in a separate alternate manifest.
Any later reuse must preserve the applicable notices and record its pinned version.

The [latest release page inspected](https://github.com/anthropics/anthropic-sdk-go/releases/tag/v1.72.0)
identifies SDK v1.72.0 (2026-09-10). Initial source inspection used moving `main`
URLs. The subsequent probe downloaded the checksummed release at commit
`03d7ef5861e6db02581610bf60bf0abe57bcb52a`, inspected its local sources and compiled
and tested it. This is now pinned local evidence, not merely a source comparison.
Its [request options](https://github.com/anthropics/anthropic-sdk-go/blob/v1.72.0/option/requestoption.go)
include explicit base URL, HTTP client, and retry controls. Disable SDK retries
so the application's attempt accounting stays authoritative; supply our bounded,
redirect-safe transport rather than inheriting ambient configuration.

### A concrete correction to the prototype

The upstream accumulator accepts deltas/stops addressed to multiple open blocks;
our prototype allows only one active block. A temporary local test started blocks
0 and 1, then delivered their deltas/stops by index. It failed at the intended
decode assertion (`TestUpstreamIndexedOpenBlocksProbe`), not at compilation. The
probe was removed after this diagnostic. This was a synthetic upstream-shaped
stream, **not** observed DeepSeek traffic. The current tests prove only the
prototype's sequential-open subset; they do not prove full native grammar coverage.

Conversely, the SDK's accumulator is not our integrity gate: the inspected
implementation can repair a truncated tool-input value to `{}`. SDK transport
errors can also retain provider bodies and HTTP request/response objects.
[Accumulator source](https://github.com/anthropics/anthropic-sdk-go/blob/main/messageutil.go),
[SSE/error source](https://github.com/anthropics/anthropic-sdk-go/blob/main/packages/ssestream/ssestream.go).

The pinned-SDK conformance spike is now implemented and has exercised the existing
normal/hostile fixtures through SDK streaming and a controlled HTTP transport.
It proves indexed-open-block assembly and a two-request tool continuation, but
also confirms the need for pre-decode checks. In the production replacement,
preserve explicit completion,
bounded raw-input validation before lossy decoding/repair, safe error projection,
and atomic admission in our layer. Carry over indexed-open-block coverage. Replace
the prototype's redundant assembly/framing where those checks remain enforceable;
do not retain two production parsers solely as mutual oracles. Reuse or reject
each SDK facility based on that proof, not its label.

After that, implement the repository-specific durable state and request mapping,
restart/compaction/tool-loop evidence, and production wiring. The provider library
must not execute tools, own a second transcript, or write the EventStore.

## Verified protocol differences

1. Native SSE uses named message/block events and indexed content, ending in
   `message_stop`. Tool input arrives as JSON fragments; usage updates are
   cumulative. A signature can arrive without any thinking-text deltas. The
   implementation must distinguish protocol completion from transport EOF.
   [Official streaming contract](https://platform.claude.com/docs/en/build-with-claude/streaming).
2. Adaptive thinking may produce no thinking block at all. Omitted thinking has
   empty text but retains a signature. Mode/effort support is model-specific;
   DeepSeek's `enabled` setting is not a portable Claude default.
   [Official thinking guide](https://platform.claude.com/docs/en/build-with-claude/thinking).
3. A tool continuation must preserve the assistant's complete block sequence,
   including `redacted_thinking`; thinking configuration stays fixed throughout
   that assistant's tool loop.
   [Official multi-turn workflow](https://platform.claude.com/docs/en/build-with-claude/thinking-tool-workflows).
4. Tool results belong in the immediately following **user** message, before
   ordinary text, not in messages with OpenAI's `tool` role. Parallel results
   must be grouped correctly.
   [Official tool-result format](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls).
5. For Fable 5.1 accounts created on/after 2026-08-31, the documented binding
   includes the system prompt, tools, and earlier messages. Editing that prefix
   can invalidate retained thinking. The documented client-side escape is a
   complete history replacement with summary/new input and no old thinking;
   retaining an unchanged tail after rewriting its prefix is insufficient.
   This is a model/account-specific fact, **not** a claim that every Claude
   model currently enforces it.
   [Official append-only guidance](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-fable-5-1#keep-the-conversation-history-append-only).

## What the code now proves

[`adapters/anthropic`](../../../internal/harness/adapters/anthropic/doc.go) contains
an unexported, synchronous native response decoder. It deliberately has no HTTP
client, `engine.Model`, startup switch, persistent state, or independent history.
Its reader owner must eventually provide cancellation, idle deadlines and Close.

- Preserve ordered `text`, `thinking`, `redacted_thinking`, and `tool_use` blocks;
  concatenate fragments within a block without combining distinct blocks.
- Replay the same decoded field values and array order, including empty thinking
  and integers above float64's exact range. This is **not** a guarantee of original
  JSON whitespace/key ordering, nor cryptographic verification of signatures.
- Publish only after all blocks close and a matching `end_turn`/`tool_use` finish
  reaches `message_stop`. Incomplete input, unsupported content, provider errors,
  duplicate keys, invalid Unicode, and invalid tool arguments return one fixed
  error and a zero message, not partially usable state.
- Visible text excludes thinking, signatures, encrypted data, and tool input.
  Existing secret-shaped text is rejected, including across visible text blocks;
  it is never silently redacted into a different replay payload. Opaque signatures
  are not interpreted as display text. This is not general secret detection.
- Bounds: 256 blocks, 1 MiB assembled content, 256 KiB combined hidden content,
  32 KiB per tool input, 256 KiB scanner line, 1 MiB/1,024 data lines per event,
  8 MiB stream read budget, and 64 nested JSON containers. Builders avoid
  quadratic fragment concatenation. Limits reject rather than truncate.

The accepted subset intentionally rejects server tools, images, citations,
fallback blocks, input transformations, unknown content/event shapes, refusal,
length/stop-sequence/pause termination, and non-empty tool-input start snapshots.
It does not silently drop unsupported replay material. Known usage counters are
retained separately; unknown usage metadata is ignored, never treated as tokens.
Future HTTP integration must classify unsupported/incomplete/server failures
without echoing provider bodies; this codec does not yet provide that taxonomy.

## Integration plan and acceptance gates

| Stage | Required work and proof | Current state |
| --- | --- | --- |
| Native response grammar | Ordered blocks, fragmented arguments, explicit completion, hostile-input bounds | SDK-assisted indexed open blocks plus bounded admission implemented; DeepSeek profile separates absent/empty signatures |
| SDK reuse | Pin release, check mapping/transport behavior, retain strict admission without duplicate full parsers | v1.72.0 adopted in main module; raw framing and tool-input validation remain ours |
| Durable replay | Versioned payload, strict variant codec, clones, legacy bytes, visible/tool consistency, atomic admission | `deepseek_messages_v1` implemented; native Claude-specific contract still separate |
| Native request/HTTP adapter | System mapping, grouped results, binding, auth/redirect/errors, cancellation/deadlines | DeepSeek implementation and local tests complete; Claude prefix validation not implemented |
| Context semantics | Wire accounting, pruning/instruction/tool changes and compaction | DeepSeek existing full-turn/summary path fixture-verified; Claude prefix-preserving transforms still open |
| Production wiring | Composition/CLI/eval identity, parity, tools, SQLite, audit and teardown | DeepSeek experimental route; tools/restart/compaction/audit plus in-flight turn/manual-summary cancellation, close and real lease-expiry fencing matrix pass; process-kill/commit-boundary campaign remains open |
| Live validation | Named endpoint/model, controlled budget, explicit permission, privately supplied credentials | DeepSeek Messages follow-up passed: eight new bounded calls, nonempty signed tail across summary/reopen, exact wire replay and audit; 20 calls total, first-run uncaptured failure still unexplained |
| Public Provider SDK | Two complete real protocols, actual external consumer, reviewed stability level | Deferred |

For durable replay, bind the block sequence to the visible assistant text and
tool calls. Do not allow an opaque payload to become an alternative writable
transcript. Keep state out of runtime text, summary prompts and OTel. New schema
work must preserve existing nil/DeepSeek bytes and explicitly document old-reader
rejection and verified-backup rollback; never strip fields from an audit chain.

For request binding, hash the exact deterministic native prefix (system, tool
schemas and messages) associated with each completed assistant, not the abstract
`ModelPromptMessage` list alone. Check after native request mapping and before
HTTP. A digest detects local edits; it does not validate a provider signature or
prove that a remote gateway accepts replay. The serving model in the response
must be checked against an explicit route/alias policy, never inferred from URL.

### Context decision that must precede route activation

Current [`materialize.go`](../../../internal/harness/contextengine/materialize.go)
can replace a covered prefix and prune retained tool results. Both can change the
prefix of retained Claude blocks. Current DeepSeek recovery tests do not prove
Claude compatibility. The proposed safe choices are:

- A declared append-only mode: reject a mismatched prefix before HTTP, with a
  clear supported-workflow limit; do not spend on repeated doomed retries.
- A separately specified full-history checkpoint at a completed-turn boundary:
  summary plus new user input, no retained old blocks, and explicit semantic-loss
  evidence. This changes protected-tail policy and must not be silently enabled.
- A provider-native context-management mode: separate future work, because its
  transformation/compaction blocks need durable semantics and evidence of their
  own. A beta that silently drops thinking is not a lossless-replay fix.

Do not reset a live tool chain or summarize away unanswered tool calls. If it
cannot fit, fail the turn safely. A model with verified weaker binding may permit
the existing rolling-summary behavior, but must have an explicit tested profile;
"all Claude" is not such a profile. The first endpoint is now DeepSeek official
Messages, with `deepseek-flash` as the documented initial model candidate. The
Claude-specific context alternatives above remain future-route design questions,
not reasons to silently change DeepSeek's context policy. No credential is needed
to finish deterministic work.

## Local verification

The codec tests use synthetic signatures and local in-memory SSE, not recorded
customer conversations or a live model. They cover positive boundary controls as
well as rejection, exact block-array assertions, multiple cumulative usage
updates, read failure and completion before transport EOF. The new owner appears
in the executable architecture matrix with only `redact` permitted internally;
it cannot import sibling adapters, Application or the store.

Run from the repository root:

```sh
GOCACHE=/tmp/och-architecture-review-gocache go test -race ./internal/harness/adapters/anthropic ./internal/harness/architecture ./internal/docsguard -count=1
GOCACHE=/tmp/och-architecture-review-gocache go test ./internal/harness/adapters/anthropic -run '^$' -fuzz '^FuzzDecodeMessage$' -fuzztime=20s -parallel=2
GOCACHE=/tmp/och-architecture-review-gocache go vet ./...
git diff --check
```

Observed results: the targeted race suite, Domain/Context Engine regression
tests, repository-wide `go vet`, and diff checks passed. A 20-second fuzz smoke
run passed (144,037 executions); this is bounded smoke coverage, not an exhaustive
protocol proof. No whole-repository test-suite pass is claimed for this slice.

Three temporary production-code mutations were run separately and restored:

| Mutation | Test that failed at its intended assertion |
| --- | --- |
| Remove the required-signature condition | `TestDecodeFailsClosedWithoutPublishingPartialMessage/missing_signature` accepted an invalid completion |
| Disable duplicate-key rejection | `TestDecodeFailsClosedWithoutPublishingPartialMessage/duplicate_fields` accepted an ambiguous payload |
| Add cumulative usage instead of replacing it | `TestDecodePreservesOrderedBlocksAndSignatures` observed 18 output tokens instead of 17 |

None failed due to compilation or a fixture startup error. Boundary tests also
include accepted controls at the content, hidden-state, block-count and tool-input
limits so an unrelated smaller guard cannot mask the intended check.

No live Claude call, credential access, or paid execution is part of this change.
Existing runtime routes and durable event schemas are unchanged.
