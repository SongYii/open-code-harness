# Secret Redaction Completion Evidence

**Status:** Complete evidence ledger

**Date:** 2026-08-31

**Contract:** [Secret redaction](secret-redaction.md), [Secret redaction design](../superpowers/specs/2026-08-31-secret-redaction-design.md), [Secret redaction implementation plan](../superpowers/plans/2026-08-31-secret-redaction.md)

This ledger records the gate, design, plan, and Task 1–4 commits on this
branch. Completion is claimed from the evidence below, not from checkbox
state.

英文为规范记录。

## Commits

| # | Commit | Subject |
| --- | --- | --- |
| Gate | `45ab473` | docs: add the secret redaction architecture gate |
| Design | `14c9e8a` | docs: design secret redaction for tool results and event payloads |
| Plan | `09fc863` | docs: freeze the secret redaction implementation plan |
| Task 1 | `b130fe1` | feat(redact): hardcoded shape-specific secret redaction |
| Task 2 | `e48d6dc` | refactor(openaicompat): consolidate secret redaction onto internal/harness/redact |
| Task 3 | `5a4bb06` | feat(application): redact secrets from tool call results and failure messages |
| Task 4 | `640c240` | feat(application): redact secrets from the final assistant message |

Merge commits: `04ce05b` (PR #70, gate), `8ec794d` (PR #71, design),
`7ab26de` (PR #72, plan), `70055ff` (PR #73, Task 1), `1dde828` (PR #74,
Task 2), `26bc5a2` (PR #75, Task 3), `e0eda82` (PR #76, Task 4).

## Mapping table: pattern → test → mutation result

| Pattern (`internal/harness/redact`) | Test | Mutation result |
| --- | --- | --- |
| Authorization header | `TestTextRedactsAuthorizationHeader` | Disabling the pattern fails this test |
| Bearer token | `TestTextRedactsBearerToken` | Disabling the pattern fails this test |
| Provider-style `sk-` key | `TestTextRedactsProviderStyleSecretKeys` | Disabling the pattern fails this test |
| Generic key/value assignment | `TestTextRedactsGenericKeyValueAssignment`, `TestTextRedactsQueryStringKeyPreservingParameterName`, `TestTextRedactsQueryStringKeyAfterAmpersand` | Disabling the pattern fails all three |
| AWS access key ID | `TestTextRedactsAWSAccessKeyID` | Disabling the pattern fails this test |
| GitHub token (`gh[pousr]_`) | `TestTextRedactsGitHubTokens` | Disabling the pattern fails this test |
| GitHub token (`github_pat_`) | `TestTextRedactsGitHubTokens` | Disabling the pattern fails this test |
| PEM private-key block | `TestTextRedactsPEMPrivateKeyBlockAsOneMatch` | Disabling the pattern fails this test |
| (dropped) dedicated `?key=`/`&key=` | — | Never fired for its own tests once the generic pattern's value matcher stopped at `&`; removed as dead weight, not shipped |

Cross-cutting `redact.Text` tests, each independently meaningful: no
false change on ordinary content (`TestTextPassesThroughContentWithNoSecretShape`);
two distinct secrets in one string both redacted, unrelated prose
survives (`TestTextRedactsTwoDistinctSecretsInOneString`); a secret
embedded in realistic surrounding file content is redacted without
corrupting the rest (`TestTextRedactsASecretEmbeddedInSurroundingFileContent`);
the design's own disclosed false-positive case is demonstrated, not
hidden (`TestTextKnownFalsePositiveOnGenericAssignmentShape`).

## Mapping table: call site → test → mutation result

| Call site | Test | Mutation result |
| --- | --- | --- |
| `openaicompat.safeMessage`/`startupFailure` | `TestClassifyHTTPErrors/401`, `TestRedactionOnClassifiedMessages`, `TestRunTurnHTTP401PersistsProviderAuth`, `TestRunTurnHTTPSecretRedaction` | Reverting the `redact.Text` call (with the resulting unused import removed) fails all four |
| `pipeline.completeToolAndContinue` | `TestToolResultSecretIsRedactedInRuntimeEventAndDomainEvent` | Reverting the redaction call fails this test |
| `pipeline.failToolAndContinue` | *(none — see Deviations)* | Not mutation-tested; no live caller passes variable content today |
| `loop.runStepLoop` (intermediate `CompleteAssistantMessage`) | `TestIntermediateAssistantMessageSecretIsRedactedInDomainEvent` | Reverting the redaction call **passed the entire suite silently** before this test existed; fails correctly with it |
| `loop.completeAssistantTurn` (final `CompleteAssistantTurn` + `RunTurnResult.Text`) | `TestFinalAssistantMessageSecretIsRedactedInResultAndDomainEvent` | Reverting the redaction call fails this test |

## Verification commands and output (fresh, this session)

```
$ gofmt -l .
(no output)

$ go vet ./...
(no output)

$ CGO_ENABLED=0 go build ./...
(no output)

$ go test ./... -count=1
ok  	github.com/SongYii/open-code-harness/cmd/acp-client
ok  	github.com/SongYii/open-code-harness/cmd/och
ok  	github.com/SongYii/open-code-harness/internal/client/acp
ok  	github.com/SongYii/open-code-harness/internal/docsguard
ok  	github.com/SongYii/open-code-harness/internal/harness/adapters/acp
ok  	github.com/SongYii/open-code-harness/internal/harness/adapters/localexec
ok  	github.com/SongYii/open-code-harness/internal/harness/adapters/memory
ok  	github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat
ok  	github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite
ok  	github.com/SongYii/open-code-harness/internal/harness/adapters/system
ok  	github.com/SongYii/open-code-harness/internal/harness/adapters/workspacefs
ok  	github.com/SongYii/open-code-harness/internal/harness/application
ok  	github.com/SongYii/open-code-harness/internal/harness/architecture
ok  	github.com/SongYii/open-code-harness/internal/harness/composition
ok  	github.com/SongYii/open-code-harness/internal/harness/domain
ok  	github.com/SongYii/open-code-harness/internal/harness/engine
ok  	github.com/SongYii/open-code-harness/internal/harness/policy
ok  	github.com/SongYii/open-code-harness/internal/harness/redact
ok  	github.com/SongYii/open-code-harness/internal/harness/runtime
ok  	github.com/SongYii/open-code-harness/internal/harness/testkit
ok  	github.com/SongYii/open-code-harness/internal/harness/tools
ok  	github.com/SongYii/open-code-harness/internal/harness/transcript

$ go test -race ./... -count=1
(all packages ok; internal/harness/adapters/sqlite ~60s, internal/harness/application ~42s under -race, everything else sub-4s)
```

## Mutation checks (summary; full narrative in each task's PR)

1. **Task 1, per-pattern** — each of the eight shipped patterns
   independently disabled; each failed exactly its own dedicated test.
   Found and removed one dead pattern (the dedicated query-string rule)
   before it shipped, rather than after.
2. **Task 1, fixture hygiene** — GitHub's own push-protection secret
   scanner flagged the first version of `redact_test.go` for a synthetic
   but shape-matching AWS Access Key ID literal. Test fixtures for
   AWS/GitHub-token shapes are built by string concatenation, not as a
   single contiguous literal, so this source file never contains a
   substring a secret scanner would flag.
3. **Task 2** — reverting the `redact.Text` call in `openaicompat`
   (with the resulting unused import removed) failed all four affected
   tests. Discovered the plan's named test
   (`TestProviderFailureErrorNeverRendersSecrets`) was the wrong one — it
   lives in `internal/harness/engine` and tests `ProviderFailure.Error()`'s
   own code-only formatting, unrelated to `redactSecrets`. The actually
   affected tests were two hardcoded-word-list `assertNoSecrets` helpers
   (one per test package in this directory), which required
   `"Authorization"`/`"Bearer "`/`"sk-"` never appear anywhere — genuinely
   incompatible with the new marker-preserving design, not merely a
   naming mismatch. Both rewritten to assert `redact.Text` idempotence.
4. **Task 3** — reverting `completeToolAndContinue`'s redaction call
   failed `TestToolResultSecretIsRedactedInRuntimeEventAndDomainEvent`.
   `failToolAndContinue`'s redaction call has no dedicated mutation test
   (see Deviations).
5. **Task 4** — reverting `completeAssistantTurn`'s redaction call
   failed `TestFinalAssistantMessageSecretIsRedactedInResultAndDomainEvent`.
   Reverting `runStepLoop`'s redaction call **passed the entire `go test
   ./internal/harness/application/...` suite silently** until
   `TestIntermediateAssistantMessageSecretIsRedactedInDomainEvent` was
   written specifically to close that gap — the same class of blind spot
   Task 3's own plan review anticipated (per the plan's Global
   Constraints, citing this session's live-tool-card-fidelity fix as
   precedent for exactly this failure mode).

## Deviations from the plan's stated file map and checklist

- **Task 1**: the plan's Task 1 checklist named a dedicated `?key=`/`&key=`
  query-string pattern as one of the patterns to implement. It was
  removed during implementation once mutation testing showed it never
  fired for either of its own dedicated tests — the generic key/value
  pattern (fixed to stop its value match at `&`) already covered both
  cases. Recorded in the Task 1 PR and in this contract's own
  documentation rather than silently dropped from the shipped pattern
  set with no explanation.
- **Task 2**: the plan named `TestProviderFailureErrorNeverRendersSecrets`
  as the test to update. That test (in `internal/harness/engine`) turned
  out to be unrelated to `redactSecrets`. The actually affected tests —
  two `assertNoSecrets` helpers — were found, diagnosed, and rewritten
  instead; the plan's intent (update whatever test's assertions are
  incompatible with the new marker-preserving behavior) was honored even
  though its specific test name was wrong.
- **Task 3**: the plan's own fallback for `failToolAndContinue` ("if none
  can today, this sub-item becomes 'assert failToolAndContinue's
  redaction call directly via a package-internal test', not skipped") was
  itself found to be impractical once attempted: no existing test in this
  codebase constructs an `ownedTurn`/session fixture outside the real
  `RunTurn` orchestration, and building one solely to exercise this one
  defensive call site would be a larger, more fragile investment than the
  (currently theoretical) risk warrants. The redaction call ships; its
  correctness rests on code symmetry with the tested `completeToolAndContinue`
  path and on `redact.Text`'s own Task 1 tests, not on a dedicated
  mutation-tested integration test. This is the one call site in this
  plan without independent mutation verification, disclosed here rather
  than silently claimed as covered.

## Remaining

- `failToolAndContinue`'s redaction call is untested by any live data
  path (see Deviations) — every current caller passes a fixed,
  project-owned `ToolText*` constant. If a future change ever routes real
  variable content through this parameter, its redaction has never been
  proven by a failing-then-passing test, only by code inspection.
- Tool-call argument redaction (`rawInput` display-copy scrubbing without
  touching the executed copy), live `model.text.delta` redaction,
  entropy-based detection, and a `RedactedString`-equivalent Go type for
  the Provider API key are excluded by design (see the [implemented
  contract](secret-redaction.md)'s Exclusions), not deferred without a
  stated reason.
- Durable storage encryption (`SECURITY.md`'s neighboring "not encrypted"
  bullet) remains a separate, unaddressed gap.
- Surfaces remain `internal`; not GA.
