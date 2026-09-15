# Provider Replay — Verification Evidence

Updated: 2026-09-15. Scope: current working-tree implementation of the
[DeepSeek replay contract](provider-replay.md), not a committed-release or live
provider certification. The dated live addendum below is bounded acceptance
evidence, not a stable-release claim. No changes to OTel or the localexec
backend were made for this slice.

## Native Messages bounded overflow recovery (2026-09-15)

The preceding boundary-evidence slice is local commit `b504510`. Native HTTP
overflow was unsupported at that checkpoint; this slice intentionally replaces
the blanket unread-error-body rule with the narrow
[HTTP overflow contract](provider-replay.md#http-context-overflow-recognition-2026-09-15).
Only HTTP 400 JSON can be inspected, up to 4 KiB plus a sentinel and an absolute
2s deadline. Strict complete JSON and message matching classify a pre-stream
capacity rejection; vendor body text never survives into a returned failure or
canonical/runtime output. Other statuses still close unread. Classification is
internal to the experimental route and adds no configuration flag or SDK seam.

`TestMessagesOverflowClassification` exercises positive protocol shapes and
malformed/ambiguous variants: mismatched types/codes/parameters, quoted or suffixed
messages, duplicate/escaped duplicate fields, unknown fields, Unicode repair,
incomplete/concatenated JSON, oversized valid prefixes and invalid token arithmetic.
`TestMessagesOverflowBodyLimits` checks the exact byte-read cap and close failure.
`TestMessagesOverflowHTTPDeadlineAndCancellation` uses real local HTTP: the server
sends a complete matching JSON prefix but keeps the body open with a whitespace
drip. Neither the prefix nor progress can bypass EOF/deadline requirements; caller
cancellation and the absolute deadline close the request. Existing unread-body,
single-close/no-SDK-retry tests remain for non-eligible responses.

`TestMessagesNativeOverflowDurableReplay` uses real Composition, HTTP/SSE, Context
Engine and SQLite, with two preceding history turns and a one-recovery cap. It
covers successful recovery, exhausted retry, rejected summary, and an HTTP 200
SSE error following text content. Assertions require exact request/summary counts,
overflow-only compaction, same assistant Item, consecutive attempts/fresh decision,
at least 10% estimated shrink and smaller actual HTTP bodies. Error-body canaries
and failed-stream text must not reach records, errors or runtime output. A cold
Assembly replays the original request after server shutdown without new events
or summary calls, preserving successful or failed canonical outcomes.

The process matrix adds three kills with same-hook release controls: overflow
summary before COMMIT, after COMMIT, and retry response before assistant-completion
COMMIT. The first needs three recovery facts (compaction failure, assistant and
Turn interruption); the other two preserve the committed checkpoint and need
only assistant/Turn interruption. Default-lease natural expiry, dead-provider
request replay, unchanged committed prefixes, third startup and independent
audit verification all use the existing production fixtures. Controls must make
exactly five HTTP requests: two seeds, rejection, summary and retry.

Verification passed: full `go test ./...`, full `go vet ./...`, full Messages
adapter/Composition package race runs, two repeated race runs of the focused
classification/native-retry tests and the three new process boundaries, Windows
amd64 cross-vet, documentation/architecture guards and `git diff --check`.
Three private Go overlays failed as expected: disabling recognition breaks the
native successful-recovery case; loose substring matching admits malformed or
ambiguous bodies and fails the negative matrix; removing the oversize sentinel
admits a truncated valid prefix and fails the byte-limit test. Production files
were not rewritten for these mutations. No remote CI or merge is implied.

All traffic here is synthetic loopback. The numeric DeepSeek shape is grounded
in a first-hand repository issue, not a new paid/live certification; the official
compatibility documentation does not promise a dedicated overflow code. Unknown
future variants remain fail-closed. No real credentials, paid budget, OTel,
schema migration, default-route change or public API expansion is involved.

## Multi-session recovery and native Messages context boundaries (2026-09-15)

The preceding slice was committed locally as `2355f0d`. This follow-up changes
tests and documentation, not production behavior or API stability.

`TestRecoveryProcessKilledBetweenSessions` puts four unfinished sessions and one
idle session in the same SQLite database. The production candidate enumeration
visits three active Turns (assistant, active compaction, tool), then the manual
compaction; the active compaction is present in both candidate sources but must
be reconciled once. Four kills and four identical-hook release controls stop
before/after the second and fourth recovery COMMIT. Read-only WAL inspection
checks exact per-session committed prefixes, pending work and untouched idle
history. The real successor Launch waits for natural 3s lease expiry and completes
all remaining recovery before returning ready; a third Launch changes no records.
No database-wide atomic recovery is claimed: already committed session recovery
survives, while unfinished sessions are rediscovered. The killed child still uses
the production `reconcileAll` seam, not a hook injected into full Launch.

`TestMessagesProcessCrashRecovery` now also kills before and after the checkpoint
COMMIT of a **mid-turn** summary. These two kills and two release controls use
real Composition, Messages HTTP/SSE, two preceding history turns, `read_file`,
Context Engine, SQLite and Runtime Host. The tool has completed and the next
assistant has not started. Before-COMMIT recovery adds a compaction failure and
Turn interruption; after-COMMIT recovery preserves the checkpoint and adds only
Turn interruption. Controls must complete exactly one tool, one mid-turn summary
and five HTTP requests including the seed turns. Natural default-lease expiry,
cold original-request replay against the dead provider, repeated startup and
independent audit verification reuse the existing process matrix. The fixture
uses supported 30% target / 10% tail settings without weakening shrink guards.

At that checkpoint, an important limit was confirmed: **the native Messages route did not
classify HTTP context overflow**. Its adapter intentionally closes rejected HTTP
responses without reading their bodies. Status 400, 413 or 422 alone is not a
model-context diagnosis. `TestMessagesHTTPFailureDurableReplay` sends these
statuses over local HTTP with an explicit overflow-shaped error body and a private
canary. The route commits a permanent failure, makes one request, starts no
compaction/tool and leaks no body to events or runtime output. Cold replay keeps
that terminal without another request or append. The adapter's unreadable-body
test also covers all three statuses, one close and no retry. The earlier scripted
Application overflow tests are **not** native Messages overflow-retry evidence.
Adding such classification requires an explicit, bounded error-recognition
contract and privacy tests; arbitrary 400/413 responses must not be relabeled.

Verification: full `go test ./...` and `go vet ./...`, the expanded process
matrix under race, two repeated race runs of the new multi-session/mid-turn/HTTP
cases, Windows amd64 cross-vet for Runtime/Composition/Messages, documentation
and architecture guards, and `git diff --check` passed. Private Go overlays
provide negative controls without editing production files: skipping recovery
between Items leaves both native mid-turn cases active and fails their recovery
assertions; omitting compaction-only candidates leaves only three recovered
sessions and fails the fourth-commit multi-session control. These are local
checks, not remote CI or a release certification.

Remaining gates at that checkpoint included other full Launch stages, native Messages overflow
classification/retry, exporter file-publication substeps, power loss/corrupt disk
and arbitrary external-tool effects. No remote provider, credential, paid budget,
OTel change, schema migration or public SDK expansion is involved.

## Recovery interrupted again and context retries (2026-09-15)

The preceding slice was committed locally as `035e12d`.
`TestRecoveryProcessKilledAtCommit` adds assistant, tool, manual-compaction and
active-Turn-compaction histories on both sides of recovery COMMIT: eight real
process kills plus eight same-hook release controls. Seeds are validated
canonical fixtures, not another live model run. The child opens SQLite and calls
**the same `reconcileAll` and reconciler used by Launch**, before readiness and
heartbeat construction. It uses existing commit hooks, not a new production
Launch callback. The successor and third host use actual Launch.

The supported 3s lease profile blocks early takeover, then expires naturally;
no row or clock is edited. Pre-COMMIT death leaves no recovery facts, post-COMMIT
death preserves exactly one whole batch. Recovery checks original command/CallID
lineage, deterministic event IDs, stream-derived timestamps, valid replay and no
duplicate facts on repeated startup. The active-compaction seed gives its start
a different CommandID from the Turn, matching production; the failure is in the
Turn's recovery batch. Recovered request results must also reconstruct correctly.
This is transaction-level crash evidence, not every Launch stage or multi-session
candidate iteration.

`TestOverflowDurableRequestReplay` exposed failed reconstruction of both successful
and exhausted overflow retries. The walker now accepts an explicit overflow-retry
preparation with a consecutive attempt index and fresh decision ID, rejecting
duplicates, skipped attempts, reused decisions, wrong triggers, absent prior
requests and retries after usage. A fresh Service returns the durable terminal
result without a new model call or event. Exhausted replay returns the canonical
`context_overflow` error; the live execution API's `model_startup` wrapper remains
unchanged.

Request reconstruction now first reuses bounded Domain replay over the full pinned
history, validating compaction brackets across CommandIDs. The command walker
accepts only `runtime_recovered` immediately before matching process-crash closure;
it does not discard arbitrary context events. The failure remains in returned
evidence. This adds a linear read-only validation pass, not another persistent
history or public extension point.

`TestMidTurnCompactionDurableReplay` uses the real Application loop with in-memory
adapters and a scripted model/summarizer. Calibration measures initial/post-tool
envelopes; a fresh fixture must complete one mid-turn compaction and one tool.
Cold Service reconstruction preserves its result without new model/summary calls
or appends. The initial oversized-tool fixture correctly failed the 10% whole-
request shrink guard. The fixture was bounded to leave actual shrink headroom;
production validation was not weakened.

The recovery kill matrix and focused context/request tests passed two repeated
race runs. Three private Go overlays supplied negative evidence: the previous
walker rejects valid overflow/recovered-compaction cases; removing Domain replay
accepts foreign compaction IDs, failing the adversarial tests; wall-clock recovery
timestamps fail deterministic-record assertions in the kill/control matrix.
Production working-tree files were not edited by these overlays.

Final checks: `go test ./...`, `go vet ./...`, full package race runs for
Application/Runtime/Composition, Windows amd64 cross-vet for Application/Runtime,
documentation/architecture guards and `git diff --check` all passed. The final
recovery matrix and focused request tests also passed their `-race -count=2`
runs. These are local checks; no remote CI or merge is implied.

Remaining gates at that slice: full Launch/multi-candidate crash injection, native HTTP overflow
and mid-turn-compaction crash combinations, exporter publication substeps,
power-loss/corrupt-disk recovery and arbitrary external-tool effects. No schema
change, public SDK expansion, paid request, real credential or OTel modification
is involved. Existing malformed history is not rewritten.

## Process-kill persistence boundaries (2026-09-15)

`TestMessagesProcessCrashRecovery` adds six real process-death boundaries, each
with a control that releases the identical SQLite hook and must finish normally:

| Boundary | Required durable state after death |
| --- | --- |
| Complete response, before assistant completion COMMIT | Request exists; no assistant completion or tool execution |
| Tool start COMMIT, before execution | Offer and tool start exist; no filesystem effect |
| Filesystem effect, before tool-result COMMIT | Real `write_file` effect exists; no tool completion |
| Summary checkpoint before COMMIT | Compaction start exists; no completed checkpoint |
| Summary checkpoint after COMMIT, before acknowledgement | Exactly one completed checkpoint survives |
| Turn COMMIT before audit export | Completed turn survives; replica remains at its verified baseline |

The parent launches the actual test executable as a child. Each child builds
real Composition, Messages HTTP/SSE, Application, workspace tools, Context Engine,
SQLite and Runtime Host. It announces a commit-hook checkpoint and blocks until
the parent releases it or calls `Process.Kill` (SIGKILL on Unix). Neither deferred
Close nor cancellation substitutes for death. Before-publish controls also verify
the very next published batch contains the intended event, excluding an accidental
stop at an unrelated append. Only existing SQLite conformance hooks are used;
there is no new production seam or public API.

After each kill, a read-only WAL reader checks the committed stream and actual
filesystem effect. An early successor is refused while the dead child's lease
is live. Later takeover waits for **natural expiry** of the default 30s lease;
there is no lease-row editing or synthetic clock. Recovery preserves the committed
prefix, closes only unfinished work and leaves no active turn/compaction. Repeating
the original request ID returns its durable terminal result without contacting the
now-dead provider. Interrupted results retain the `process_crash` cancellation
error; completed results remain successful.

For the ambiguous-effect window, file identity and modification time must remain
unchanged after recovery/retry. Recovery does not synthesize a tool success/failure
or execute it again. It records `tool.call.interrupted` with the original CallID,
then `turn.interrupted`, both `process_crash`. This is deliberately **not an
exactly-once guarantee** for arbitrary external effects.

Two production exporter passes and independent cold replica verification must
reproduce every canonical event. The audit-lag fixture leaves periodic export
disabled after a real baseline export; it does not kill within the exporter's
file-publication state machine. A second restart must append no further recovery
facts, and the successor must still commit a fresh session.

The first run exposed production defects:

- Recovery always emitted an assistant interruption, even for an active tool.
  Both killed-tool cases produced histories rejected by Domain replay. Recovery
  now selects the terminal by Item kind and obtains CallID from the matching
  canonical start. Existing assistant recovery bytes, deterministic append IDs
  and timestamps are unchanged.
- Request reconstruction rejected ordinary `context.prepared` evidence and the
  host's `process_crash` reason. It now recognizes preparation at the correct
  assistant boundary, checks matching request decision/attempt metadata and
  accepts the existing recovery reason. Unknown/misplaced events still fail
  closed. No new event type, schema migration or error category was introduced.

The six kill cases, six completion controls and six recovery checks passed with
the race detector. Focused Runtime/Application regressions passed. Replacing each
fixed production file with its pre-fix version through private Go overlays makes
the corresponding tests fail at the intended assertions; production files are
not edited by these negative checks. The context negative fixtures explicitly
validate event codecs first, preventing malformed messages from making lifecycle
checks pass for an unrelated reason. Verification results:

- `go test ./...`: passed, including localexec on this host.
- `go vet ./...`: passed.
- The full kill/control/recovery matrix passed two repeated race runs
  (`-count=2`). After adding exact SIGKILL and completed-request cold replay
  assertions, the final matrix passed with race again.
- `go test -race ./internal/harness/runtime ./internal/harness/application
  ./internal/harness/composition -run 'Test(Reconcile|Reconstruct|MessagesProcessCrashRecovery)' -count=1`:
  passed.
- Documentation/architecture guards and `git diff --check`: passed.

At that checkpoint, killing **during reconciliation**, mid-turn compaction/overflow-retry
request reconstruction, export-publication substeps, power-loss/disk corruption,
non-cooperative drivers and arbitrary external-tool reconciliation remain separate
obligations; the follow-up above records their additional bounded coverage.
This fix does not rewrite already malformed historical recovery
batches or their audit chains. No remote calls, real credentials, paid budget, OTel
changes or stable SDK claims are involved. The earlier uncaptured live failure
remains unexplained.

## In-flight Messages lifecycle fault matrix (2026-09-15)

`TestMessagesInFlightLifecycle` runs eight local Composition scenarios: ordinary
turn and manual summary, each with successful completion, caller cancellation,
assembly Close and actual lease-expiry/heartbeat fencing. It uses real `Open`,
the Messages adapter and HTTP client, Application, Context Engine, SQLite and
Runtime Host. No production model/host replacement, public injection API,
OTel changes, credentials or paid calls are involved.

The loopback server flushes valid thinking/signature/text blocks (and a valid
`list_dir` offer for the turn case), but withholds the terminal frames. Tests
require HTTP cancellation and operation termination **before** releasing those
frames. Close must also finish while the server is still waiting. Only then
does the server attempt the late completion. Assertions cover:

- No partial/late text or hidden canaries enter canonical events or the runtime
  text sink; no assistant completion, tool start or checkpoint may be committed.
- Caller cancellation leaves the host ready and preserves a durable interrupted
  turn. Assembly Close/lease loss close `Done` and reject cached Service/Store
  calls; old facades stay rejected after a successor starts.
- The fixture expires only its temporary database's lease using the existing
  SQLite test seam. The **real heartbeat** detects lost ownership; the test does
  not directly invoke `Abandon` or the fencing reaction.
- A new host recovers the unfinished turn/summary using `process_crash` or
  `runtime_recovered`. Closing the old host reports `writer_fenced`, does not
  release the successor's lease, and the successor can commit a new session.
- The same withheld responses complete successfully in positive controls: one
  real workspace tool execution or one valid summary checkpoint. Exact HTTP
  request counts exclude hidden retries. Interrupted histories pass canonical
  replay, with no active work remaining after cleanup/recovery.

All eight cases passed with `-race`; the initial matrix also passed two repeated
race runs. After strengthening the durable terminal-event assertions, the full
matrix passed with race again. Root `go test ./...`, `go vet ./...`, the separate
example module, documentation/architecture checks and whitespace checks passed.

Two production-source mutations were tested only through private temporary Go
overlays, not by editing production files. Removing `Host.Admit`'s host-cancel
subscription makes the close case fail waiting for HTTP cancellation before
server release. Removing `ReleaseLease`'s runtime/token ownership predicate makes
the lease-loss case fail because stale Close returns success instead of fencing.
Both failures hit the intended assertions, not compilation errors; restored
source passes. This second change adds tests/evidence only; the Messages route
was checkpointed separately as `d4ada6e`.

Limits: the server attempts late frames after client cancellation; this does not
claim canceled clients consumed those frames. These are local stream/lease
integration checks, not process-kill injection at every persistence boundary,
non-cooperative driver shutdown, an HTTP/2 or TLS fault matrix, general leak
certification, or provider reliability measurements. Those remain separate
evidence obligations; the earlier uncaptured live failure is still unexplained.

## Nonempty retained-tail live gate passed (2026-09-15)

After the operator re-supplied the private key and authorized continuation within
the original **CNY 5 total** budget, the corrected Composition probe passed against
the actual `deepseek-flash` model at DeepSeek's official Messages endpoint. The
separate two-request adapter probe was not repeated. This was a new synthetic
session; the previous run's database, audit chain and 12-call ledger were preserved.

`TestLiveMessagesComposition` completed all eight planned HTTP requests in one run:

- One `read_file` invocation, tool-result continuation and nonce recall after
  SQLite close/open; synthetic project color and disposable history added.
- Summary committed through sequence 37, leaving a nonempty completed assistant
  tail. That retained response had one thinking block and one nonempty signature.
- After another close/open, the outgoing retained blocks matched canonical state
  **byte-for-byte**, and the remote continuation returned the nonce and color.
- Seven native assistant completions, one retained state and 54 canonical events;
  display export hid thinking/signatures and independent cold audit verification
  matched all events. The probe reported `LIVE_COMPOSITION=passed`, not fixture
  mode. All eight response captures had accepted terminal reasons: one `tool_use`,
  seven `end_turn`. There were no retries or stream failures in this run.

Production code and validation were unchanged for this follow-up. The sole
assembly overlay still injects the bounded HTTP client; real tools, summarizer,
context engine, SQLite and lifecycle are exercised. This closes the specific
nonempty-retained-wire acceptance gap, **not** a general reliability/quality,
native Claude compatibility, external-consumer or stable-SDK gate. The uncaptured
September 14 stream failure was not reproduced and still has no established
cause; this passing run does not retroactively explain or erase it.

The new ledger records eight calls, 70,174 serialized request bytes and 30,668
aggregate reserved output tokens. Its complete terminal usage reports 9,365
uncached input, 9,472 cached input, zero cache creation and 659 output tokens
(including the summary). At the [official peak prices rechecked on September 15](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/),
this run's usage-based estimate is CNY 0.024381. Conservatively allocating 65,536
input tokens per call and every reserved output token gives CNY 1.293920 for
this run; **combined with the previous run's conservative ceiling, CNY 3.196496**.
These are estimates, not provider-invoice reconciliation or an account-level
monetary lock. The aggregate count is 20 calls across both runs, not a reset of
the CNY 5 budget. No remaining reserved calls were consumed after success.

The exact temporary key file was removed again. Private SQLite, audit and bounded
response captures remain outside Git. Only metadata is reported here. Updated
documentation/architecture checks and `git diff --check` passed; the prior local
rehearsal/race/negative gates remain the deterministic regression evidence.

## Local live-probe rehearsal follow-up (2026-09-15)

No paid requests were made during this rehearsal step; the September 14 live
ledger stayed at 12 and its credential file was absent. Changes were confined to the opt-in experiment,
its overlay preparation and documentation, not production replay/compaction or OTel.

The live scenario now checks the newest Turn for both successful completion and
native assistant blocks **before invoking the summarizer**, including diagnostic
resume. It checks the committed cut again before sending a continuation. This
prevents a failed newest Turn from silently making the nonempty-wire gate vacuous;
the final exact, nonempty comparison remains mandatory.

`TestMessagesCompositionLocalRehearsal` supplies a scripted in-process transport
to the very same Composition scenario. Observed result: eight fixture requests,
one real workspace read, seven completed native assistant states, one retained
signed state replayed byte-for-byte after summary/reopen, and 54 canonical events
matching independent audit verification. The fixture also verifies each request
was reserved in the ledger before transport. There is no real HTTP or credential
access; output is explicitly `LOCAL_REHEARSAL`, never a live pass.

`TestMessagesReplayTailPreflight` uses positive controls plus open, failed,
failed-after-assistant and completed-without-native cases. A callback spy asserts
the same orchestration helper makes zero summary calls in each negative case.
`TestMessagesCompactionMustLeaveReplayTail` rejects no-op and tail-covering cuts
before continuation. These gates and the full local rehearsal passed with race
detection. Numbered synthetic rows replace repeated sentences, without claiming
repetition was the cause of the uncaptured September 14 failure.

A local source-overlay mutation removed the preflight's early return while
leaving the later retained-tail rejection intact. All four negative cases failed
specifically on `summary callbacks=1; want callbacks=0`: a later error cannot
make these tests pass for the wrong reason. Original-source gates pass; the
mutation never changed production files or contacted a provider. Overlaid
`go vet`, documentation/architecture guards and `git diff --check` also pass.

At this point the remote nonempty-retained-tail gate and the uncaptured failure
remained open; the later live follow-up above closes only the former.
Reproduction instructions are in the [probe README](../../experiments/deepseek-messages-live/README.md).

## Bounded live Messages acceptance (2026-09-14)

The operator authorized DeepSeek's official Messages endpoint, a private key
file and a CNY 5 spending ceiling. The actual route was
`https://api.deepseek.com/anthropic/v1/messages`, model `deepseek-flash`, low
effort. The [opt-in probes](../../experiments/deepseek-messages-live/README.md)
use synthetic data and a shared pre-send ledger: 12 total calls, at most 32 KiB
per request and at most 4,096 output tokens, including failures/diagnostics.
Composition's sole overlay change injects that bounded HTTP client into the
real assembly; the model/tool/context/store paths remain production code.

**Result: partial acceptance, not an all-green live gate.**

| Boundary | Actual observation |
| --- | --- |
| Native tool round trip | Requests 1–2 passed: signed thinking + tool use, tool result accepted, terminal text returned. |
| Actual tools and persistence | Requests 3–7 exercised one `read_file` on a synthetic nonce file, tool-result continuation, SQLite close/open, nonce recall and additional synthetic history. |
| Summary rejection | Initial short-history attempt had no coverable prefix and sent no request. Request 8 produced a summary larger than its source; the core refused it. Request 9 failed stream validation; no raw capture was retained, so its exact cause is unresolved. |
| Truncated summary | Request 10 returned HTTP 200 but `stop_reason=max_tokens`, output 1,177. Offline replay confirms rejection before completion; do not count it as a valid summary. |
| Summary and continuation | Request 11 committed a checkpoint through sequence 37; request 12, after close/open, completed with both nonce and color intact. This used a 24,576-token test context, 1,996-token derived summary cap and concise manual focus; initial attempts used a 16,384-token context and 1,177 summary cap. No production validation was relaxed. |
| Post-compaction native retained blocks | **Not covered with a nonempty set.** The retained tail was a failed turn without completed assistant blocks. The probe's positive-coverage assertion failed and remains in place. Successful summary continuation is not evidence of nonempty retained-block wire preservation. Local deterministic tests cover that case. |
| Offline durable evidence | Independent cold audit export/verification matched all 58 canonical events: one tool call, six completed native states, 15 recorded replay-state occurrences matching earlier completions. Display export omitted hidden thinking/signatures. This offline verification does not turn the failed nonempty-wire gate into a pass. |

Live usage exposed a real adapter bug: Messages reports uncached input separately
from cache-read/cache-created input, while Engine requires total input with
cached input a subset. The adapter now performs checked addition before emitting
anything. `TestSDKUsageNormalizesTotalInput` covers all counters, absent caches,
completion/attempt consistency and the live 178 uncached + 256 cached regression;
`TestSDKStreamRejectsBeforeAnyOutput/total_input_overflow` rejects overflow before
text or tools escape. The final live response reported 1,648 uncached + 768 cache
read, and the canonical event correctly recorded total input 2,416, cached 768,
output 84. Early pre-fix events were **not rewritten**.

A no-network source overlay then restored uncached-only accounting. Both the
all-counter and live-regression cases failed on the intended numerical
assertions (12 vs 19 and 178 vs 434), not compilation. The unmodified production
test passed again; the mutation never touched production files or live traffic.

All 12 reserved calls were used; no further paid calls were made in that run. The ledger has
77,776 serialized request bytes and 41,214 aggregate maximum output tokens. At
the [official peak CNY prices checked that day](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/),
allowing 65,536 input tokens for *every* request gives a conservative estimated
ceiling of CNY 1.903. This is not an account-level monetary lock or actual invoice
reconciliation. The exact temporary `/tmp/och-deepseek-key` file was removed;
provider-side key revocation was not performed. Private SQLite/audit/response
captures remain outside Git; their hidden content is sensitive, not public evidence.

After the input-usage fix, root `go test ./...` passed, including localexec;
adapter/Domain/Engine/Context Engine race tests also passed. The next live gate
is a deliberately nonempty completed-assistant tail across compaction, plus
diagnosis of the uncaptured stream failure if it recurs. Additional paid calls
require fresh authorization; this run does not certify reliability, latency,
model quality, account billing, native Claude prefix binding, or a stable SDK.

## Messages route addendum (2026-09-14)

`deepseek-messages` is now an experimental selectable route, using the pinned
Anthropic Go SDK v1.72.0 in the main module. No upstream code was copied. SDK
imports are confined to `adapters/anthropic`; Domain has a closed, SDK-independent
`deepseek_messages_v1` content union and projection validation. This is not a
native Claude claim. The fixture-only stage below predates the bounded live
addendum above.

| Claim | Executable evidence |
| --- | --- |
| Native blocks survive tools and SDK encoding | `TestSDKModelToolContinuationAndExplicitHTTP`: exact ordered mixed blocks, absent signature, large integer and escaped input, grouped tool results, purpose/effort/max_tokens, explicit endpoint/auth and close-once |
| Strict durable variants and projection | `TestMessagesStateStrictCodecAndProjection`, `TestMessagesStateBoundsAndAtomicCommand`: assistant and request codec, unknown/null/duplicate/cross-variant payloads, depth/size, large-integer mismatch, clone isolation and atomic rejection |
| SDK repair cannot silently fabricate completion | `TestSDKStreamRejectsBeforeAnyOutput`: shared hostile fixtures and response-model/redacted/repeated-object cases; `TestSDKRawToolInputOwnsEmptyObjectFragments`: valid fragmented empty object is not reset |
| HTTP failures do not read bodies, retry or redirect | `TestSDKHTTPRejectsWithoutReadingErrorBodiesOrRetrying`: wrong content type, 204/307/401/429/503, zero error-body reads, one transport call and one Close |
| Cancellation/idle/Close cannot publish partial output | `TestSDKStreamCancellationIdleAndEarlyClose`, close-error case in `TestSDKStreamRejectsBeforeAnyOutput`; parent/Next cancellation, idle, repeated early Close, blocked-body release |
| Indexed open blocks and signature absence are preserved | `TestSDKIndexedOpenBlocksAndDeepSeekSignatures` plus the updated indexed-block SDK probe |
| Hidden replay is priced and detached | `TestMessagesReplayMeterAndDetachedMaterialization`; block/signature framing and no double-counted visible text |
| Tool loop, restart, summary and audit work | `TestDeepSeekMessagesToolsRestartAndCompaction`: actual Composition, loopback HTTP, workspace tool, SQLite reopen, rolling summary, second reopen, exact retained-state set and independent event-for-event audit verification |
| Invalid input cannot commit or execute a tool | `TestMessagesInvalidToolInputCannotCommitOrExecute`: SDK-repairable truncated input after visible text, terminal failure, no visible partial, no assistant completion or ToolCallStarted; the lifecycle test above supplies the successful tool control |
| Both execution configurations agree | `TestDeepSeekFlagsAndInProcessProviderParity`, `TestDeepSeekConfigValidation` now exercise both explicit routes; SDK owner guard covers Domain/Engine/Composition rejection |

Four temporary mutations were applied to actual adapter code, run separately,
and restored. All failed through assertions (not compilation/startup errors):

1. Skip raw tool-input validation before SDK refresh: truncated-input test
   reports `invalid stream exposed partial output`.
2. Remove `WithoutEnvironmentDefaults`: the explicit-auth fixture reports an
   unexpected ambient Authorization header.
3. Set `WithMaxRetries(1)`: the 503 fixture fails the single-call/close invariant.
4. Permit standard redirects: the 307 fixture observes follow-ups/error-body
   reads and fails its HTTP boundary assertion. All transport remains injected;
   no live request can escape these mutations.

Local verification completed:

- Root `go test ./...` passed, including localexec in the approved local-test
  environment. The sandbox-only attempt could not create httptest sockets;
  that was an environment restriction, not a protocol result.
- Race tests passed for adapter, Domain, Engine, Application, Context Engine and
  architecture; targeted Composition tool/restart/compaction and invalid-input
  scenarios also passed with `-race`.
- The original overlaid pinned-SDK probe passes with its indexed-block assertion
  updated to require acceptance by the production replacement.
- `go vet ./...`, documentation/architecture guards, `go mod tidy -diff`,
  `go mod verify` and `git diff --check` passed.
- The separate `examples/keep-last-n` module's dependency manifest was tidied
  and `go test -mod=readonly ./...` passed; this is build evidence, not a new
  external consumer.

This local stage did not include a paid/live call. Remote model/quality certification, route-specific
lease-loss fault campaign, encryption, lossy history migration, native Claude
prefix preservation, or public Provider SDK is claimed. Full-turn/summary
semantics are the DeepSeek route's existing local contract, not a generalized
promise about all Anthropic-compatible gateways.

## Executable evidence

| Claim | Tests and observations |
|---|---|
| Durable shape is strict and old bytes are unchanged | `TestProviderStateStrictCodecAndLegacyBytes`; existing domain golden codec tests. Both completed-event and nested request-message state round-trip; null content, missing/unknown/duplicate keys and unknown protocol fail. |
| State is bounded, assistant-only and detached | `TestProviderStateBoundsAndRole`, `TestProviderStateCommandCompletionIsAtomicAndDetached`; role cases first validate a state-free baseline so unrelated role-field errors cannot make them pass. |
| Engine state is completion-only and clears on failure/cancel | `TestRunnerProviderStateIsCompletionOnlyAndDetached`, `TestRunnerCanceledCompletionClearsProviderState`; cancellation checked at Next and Close, and malformed noncompleted events must fail before visible/tool delivery. |
| Both legacy-history and mid-turn projection preserve state | `TestProviderStateSurvivesLegacyProjectionButNotSummaryRendering`; projection and clone checks, plus render isolation. |
| Context projection/materialization and budget preserve the new surface | `TestProviderStateMaterializationAndMeter`; retained state, ownership isolation, exact added estimate and state-only committed assistant coverage. |
| Application rejects foreign, missing or known-secret state | `TestApplicationProviderStateGate`; includes nil legacy identity and a valid protocol state against a different adapter family. |
| Wire replay includes all prior assistants | `TestDeepSeekCompletedStateAndReplayMapping`; empty/nonempty reasoning, prior non-tool replies and tool offers, explicit thinking/effort, no-tools omission. Fragment assembly also exercised end-to-end. |
| Missing/foreign state never reaches HTTP | `TestDeepSeekRejectsInvalidReplayBeforeHTTP`; transport fails the test if invoked. |
| Incomplete/oversized/malformed reasoning does not complete | `TestDeepSeekIncompleteStateNeverCompletes`, `TestDeepSeekLosslessUnicodeAndMultilineBound`; missing/null/type errors, EOF without DONE, length/no finish, known-secret shape, oversized assembled reasoning, malformed Unicode and multiline bounds. |
| Real tool continuation, restart, compaction and audit work | `TestDeepSeekThinkingToolsRestartAndCompaction`; actual composition, HTTP/SSE, workspace tool, SQLite reopen, rolling summary and second reopen; exact retained-state comparison against committed coverage; display export isolation; cold consistent audit export and independently verified event-for-event comparison. |
| Invalid state cannot commit assistant completion or execute tools | `TestDeepSeekFailedStateNeverCommitsOrExecutesTools`; missing, sensitive, truncated and length cases, with a successful two-request tool-roundtrip control using the same fixture path. |
| Explicit route controls and execution paths agree | `TestDeepSeekConfigValidation`, `TestDeepSeekFlagsAndInProcessProviderParity`; route selection changes Subject digest and matches ACP argv/in-process configuration. Baseline launcher parity additionally checks AdapterKind. |

The domain command test proves the emitted atomic batch; SQLite composition
tests exercise its actual persistence. Neither alone is described as crash
injection at every commit boundary. Existing EventStore fault/recovery suites
remain the broader transaction evidence.

## Mutation checks

All four mutations below were actually applied, tested and restored. Each failed
through an assertion, not a compile error. Tests were then rerun on restored code.

| Temporary mutation | Observed failing gate |
|---|---|
| Replace outbound `state.ReasoningContent` with an empty string | `TestDeepSeekCompletedStateAndReplayMapping`: prior assistant reasoning missing, including empty/non-tool reply. |
| Drop the projected completed assistant's state | `TestProviderStateMaterializationAndMeter`: retained state lost or shared. |
| Remove the domain assistant-role check | `TestProviderStateBoundsAndRole`: state accepted outside assistant role. |
| Allow valid state on noncompleted Engine events | `TestRunnerProviderStateIsCompletionOnlyAndDetached/state_on_delta` and `/state_on_tool_call`: output escaped before rejection. |

Review corrected two initially weak test patterns before those checks: non-assistant
messages must otherwise be valid, and a final EOF error is not sufficient evidence
of early stream rejection. These guards test the intended boundary directly.
Adding the implemented contract also triggered the existing bilingual guide
coverage gate; both guide entries were added rather than exempting the new slice.

## Commands and results

Commands used `GOCACHE=/tmp/och-architecture-review-gocache`. Tests needing HTTP
listeners were run with local loopback permission; an initial sandbox-only run
failed at `httptest` listener creation, which is not a product regression.

- `go test ./...`: all packages except `internal/harness/adapters/localexec`
  passed. The two pre-existing environment-dependent failures were
  `TestRunKillsOnResourceLimitSignal` (namespace PID 2 versus registered host PID)
  and `TestEnforcementReportsNoneWithoutAPlatformBackend` (bwrap confinement is
  available, contrary to the test's none-backend assumption). This is explicitly
  **not** a zero-failure full-suite result.
- `go test -race ./internal/harness/domain ./internal/harness/engine
  ./internal/harness/contextengine ./internal/harness/application
  ./internal/harness/adapters/openaicompat ./internal/harness/composition
  ./internal/launcher`: passed.
- Focused `Test.*(ProviderState|DeepSeek)` runs after adding negative/control
  cases: passed, including race. The restored mutation gates passed again.
- `go vet ./...`: passed.
- Documentation/dependency guards and `git diff --check`: passed.

## Limits and next evidence

The original Chat Completions fixture stage did not perform live/paid requests,
model-quality evaluation, remote acceptance after compaction or performance
comparisons. The later Messages live observations are recorded above. The fixture
validates exact harness input/output behavior, not what the remote service may
change to require. Plaintext reasoning remains sensitive canonical evidence;
known-shape rejection is not general secret detection or encryption.

No old-reader migration, field-stripping rollback, Claude native Messages,
Gemini signature replay, public Provider SDK, or stable external-consumer claim
is included. The contract's pre-feature backup/new-reader rollback requirement
still applies. The next architectural gate is a second real protocol, not a
broader registry that merely wraps the same Chat Completions implementation.
