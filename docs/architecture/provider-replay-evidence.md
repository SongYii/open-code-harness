# Provider Replay — Verification Evidence

Date: 2026-09-13. Scope: current working-tree implementation of the
[DeepSeek replay contract](provider-replay.md), not a committed-release or live
provider certification. No changes to the OTel implementation or localexec
backend were made for this slice.

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

No live/paid DeepSeek request, live model-quality evaluation, remote acceptance
test after compaction, or performance comparison was performed. The fixture
validates exact harness input/output behavior, not what the remote service may
change to require. Plaintext reasoning remains sensitive canonical evidence;
known-shape rejection is not general secret detection or encryption.

No old-reader migration, field-stripping rollback, Claude native Messages,
Gemini signature replay, public Provider SDK, or stable external-consumer claim
is included. The contract's pre-feature backup/new-reader rollback requirement
still applies. The next architectural gate is a second real protocol, not a
broader registry that merely wraps the same Chat Completions implementation.
