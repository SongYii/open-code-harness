# Evaluation System Completion Evidence

**Status:** Evidence ledger for Milestone 10 (Tasks 1–18 of the implementation plan; Tasks 16–17 shipped as documented partial slices — see their own entries below)

**Contract:** [Evaluation System — Implemented Contract](evaluation.md)

**Design:** [Milestone 10 evaluation design](../superpowers/specs/2026-09-02-evaluation-design.md)

**Plan:** [Milestone 10 implementation plan](../superpowers/plans/2026-09-02-evaluation-system.md)

## Commits

Every commit below is a real, resolvable commit in this repository's own
history (`git log`/`git rev-parse`), never invented. Tasks 1–17 were each
opened as an individual PR, stacked on its own predecessor branch, verified
independently (`go build`, `go vet`, `go test -race`, cross-builds), and left
open for review rather than force-merged — the standard workflow this
repository uses throughout.

| Commit | Task | Content |
| --- | --- | --- |
| `6355c10` | Design | Milestone 10 evaluation design (PR #118) |
| `9d855f9` | Design | Terminology clarification: "v1 milestone scope" vs. "incremental delivery slice" (own follow-on branch, not PR #122's own stalled worktree) |
| `cd322ef` | Design | Implementation-contract seam clarifications (PR #130) |
| `6e6a50b` | Plan | 18-task implementation plan (PR #131) |
| `bcac606` | Task 1 | Frozen Scenario/Subject/Executor identity models and canonical digests (PR #123) |
| `f4c4e6e` | Task 2 | Attempt/Outcome/EvidenceManifest/Score models and append-only store (PR #124) |
| `90e186c` | Task 3 | EvalSet document and matrix expansion (PR #125) |
| `704ce17` | Task 3 (fixtures) | Portable fixture isolation — Attempt root + fixture copy (PR #126) |
| `80fb55b` | Task 4 (prereq) | Canonical audit snapshot/verification operation (`sqlite`, `composition`) |
| `7e8d538` | Task 4 | Cold, verified evaluation evidence APIs (PR #132) |
| `ab808ec` | Task 5 | Fail-closed `ApprovalMatcher` shared across both executors (PR #133) |
| `722c50e` | Task 6 | In-process executor: real Scenario execution through `composition.Assembly` (PR #134, superseding the earlier, closed PR #129) |
| `4dafc82` | Task 7 | Bounded post-shutdown evidence manifests (PR #135) |
| `969560b` | Task 8 | Sequential Attempt orchestration and recovery-state classification (PR #136) |
| `08d8f72` | Task 9 | Deterministic verifier catalog, `RunScorer`, offline `RegradeAttempt` (PR #137) |
| `bcc35de` | Task 10 | Stage A CLI (`och-eval run/regrade/report`) and the first checked-in, audit-proven smoke EvalSet (PR #138) |
| `3e73fdf` | Task 11 | `och` CLI argv parity with `eval.BuildConfig`; `NormalizedArgv` (PR #139) |
| `bd9dc63` | Task 12 | Real ACP subprocess supervision — process groups, bounded stderr, binary pinning (PR #140) |
| `b691660` | Task 13 | ACP approval handler, cancellation escalation ladder, restart modes (PR #141) |
| `1cb82a1` | Task 14 | ACP manual compaction as a three-phase lease-safe transaction (PR #142) |
| `67cd2a4` | Task 15 | Executor parity comparison, ACP dispatch in the runner/CLI (a missing prerequisite this task discovered), four-Cell PR lane (PR #143) |
| `4904bf4` | Task 16 (partial) | Tool/workspace deterministic suite: read/exec-redaction/read-missing/containment (PR #144) |
| `45964ff` | Task 17 (partial) | Live dual-consent gate consolidation, evidence-only judge, price table (PR #145) |
| `4151968` | Post-merge review | Fail-closed judge contract enforcement and semantic CLI exit-code precedence |
| `fb28132` | Task 18 | Benchmarks, evidence ledger, and documentation (PR #146) |
| `fe07f5d` | Task 17 completion 1/6 | Frozen `och.eval.judge-config` document, canonical digest, explicit `costStatus` |
| `1fe0a3c` | Task 17 completion 2/6 | EvalSet lane rules and EvalSet/JudgeConfig evidence binding |
| `d3010d6` | Task 17 completion 3/6 | Deterministic, fail-closed judge evidence selection (two real defects fixed) |
| `304f37f` | Task 17 completion 4/6 | `EvaluateJudgeAttempt`: prerequisite-gated, append-only live Scores |
| `b5244e4` | Task 17 completion 5/6 | `och-eval judge`, real OpenAI-compatible caller, checked-in JudgeConfig example |
| `517e22c` | Context suite 1 | Per-request Tool Result pruning count on `context.prepared` |
| `4e460f4` | Context suite 2 | Typed, fail-closed Context trace over canonical audit |
| `a707517` | Context suite 3 | Stateless `context-mechanism` fixture protocol |
| `f4b8d0a` | Context suite 4 | Six core Context verifiers; `CriterionResult.Detail` |
| `86239a0` | Context suite 5 | Five mechanism verifiers and the required mutation set |
| `0d441bb` | Context suite 6 | Core profile, pre-turn Scenario, checked-in digest guard |
| `37be2fe` | Context suite 7 | Manual reset and summary Scenarios |
| `efd8ce1` | Context suite 8 | Overflow recovery Scenario |
| `263f5ae` | Context suite 9 | Mid-turn criterion correction; pruning Scenario |
| `dbb385f` | Context suite 10 | Usage-anchor Scenario and criterion correction |

## Post-merge review findings closed

A fresh review after PR #146 found that the Task 17 mechanism was less strict
than its documentation claimed even though the existing suite was green. Commit
`4151968` closes the executable contract gaps: it sends the frozen criteria to
the caller, validates model/prompt/criterion identity before calling, rejects
omitted or duplicate criterion results and inconsistent aggregates, forces
missing evidence to `indeterminate`, enforces the documented `[0,1]` score
range, and uses a second decode requiring `io.EOF` for trailing-data rejection.
The same review replaced numeric `max(exitCode)` aggregation with explicit
semantic severity so `indeterminate` can no longer mask gate, infrastructure,
or internal failures. The new tests were observed failing against `8116113`
before the fixes and passing afterward; the full repository suite, eval race
suite, vet, CGO-disabled build, and Windows eval build were rerun.

## Deliberately scoped/deferred items

Documented explicitly here rather than silently absent, per each PR's own
description at the time:

- **Task 16** shipped the tool/workspace suite (read, exec+redaction,
  expected failure, containment) but not the Context mechanism suite
  (pre-turn/mid-turn auto-trigger, overflow recovery, ACP interrupt/kill
  recovery scenarios, multi-chunk summary, Tool Result pruning), and not an
  ACP-executor pairing for this specific suite — the Context mechanism suite
  needs empirically-tuned token budgets against the real Context Engine
  trigger math, deliberately not guessed at under this milestone's own time
  budget.
- **Task 17** is now complete through `och-eval judge`: the frozen
  `och.eval.judge-config` document, its EvalSet/manifest binding,
  consent-before-credential ordering, deterministic prerequisites, the real
  OpenAI-compatible caller, explicit cost availability, and append-only live
  Scores are all shipped and tested. What remains outstanding is genuinely
  outstanding, not deferred wiring: no run against a real live model has ever
  happened here (no live credentials exist in this environment — a fixture
  SSE stream reaching an appended Score through the real adapter is what is
  actually proven), and the `context-quality` Scenario's own live
  meta-evaluation run has never been executed (it is an example, deliberately
  never run by CI).
- Design §25.2's `list_dir` tool and MCP suites are out of scope for this
  milestone entirely (design §3's own stated non-goals / §25.4's own "MCP
  absence does not block the eval system").

## Context suite: contracts the implementation corrected

Three clauses of the accepted Context suite design did not survive contact
with real evidence. Each was amended rather than silently worked around.

- **Mid-turn attempt index.** The design's section 9 requires the mid-turn
  criterion to pair with attempt index 2. Production emits the mid-turn
  continuation as a *new assistant item on the same Turn*, so its index is 1
  (`turn=4756ba item=03c3b2 attempt=1 trigger=pre_turn` followed by
  `turn=4756ba item=6080b6 attempt=1 trigger=mid_turn`). Index 2 identifies a
  second attempt at the same item — the overflow-retry shape, which the
  overflow Scenario really does record as `overflow_retry#2`. The criterion
  now requires a mid_turn preparation that follows an earlier preparation on
  the same Turn and carries a Tool Result.
- **Usage-anchor comparison direction.** The first implementation refused an
  applied anchor larger than the earlier provider usage record. An anchor of
  60025 against a recorded 60000 is correct: the anchor is non-lowering and
  the request adds its own new content. An anchor *below* the observed usage
  is the defect.
- **Idle ACP interrupt.** Recorded separately in the design's section 12.1;
  the suite ships `kill` as its abrupt restart mode.

Two Scenario-shaped facts were also found only by running:

- A Scenario that declares the `workspace` evidence role without a `collect`
  action collects nothing, and the pruning criterion correctly refuses — it
  has no file to resolve the projected frame's digest against.
- The overflow Scenario sits between two walls: too little history and the
  compaction fails `context_summary_invalid` because the summary is not
  smaller than the source it replaces; too much and the local pre-turn
  trigger fires first, so no overflow ever happens.

## Context suite: what is not yet proven

- **Multi-chunk summarization** has a landed, mutation-tested criterion but
  no end-to-end Scenario. Forcing two summarizer chunks needs the covered
  source inside `(hardInput - focusTokens, 0.95 x hardInput)`; the 60%
  `triggerPercent` floor and the 4KiB `maxCompactSessionFocusBytes` cap leave
  roughly an 800-token band on a 4096-token window, and the summary must be a
  net reduction within it.
- **Multi-chunk** is the only Context mechanism without an end-to-end
  Scenario. Every other one runs on both executor surfaces, including
  checkpoint reuse across a `clean_shutdown` restart on each and a `kill`
  restart through the ACP recovery set.
- No claim is made about a crash during an open compaction bracket.

## Mechanism → test → mutation result

Every row below reflects a mutation check actually performed and observed in
this repository's own working history (temporarily weakening a guard,
confirming the dependent test fails, restoring it) — not merely a claim
that a test exists.

| Mechanism | Test | Mutation check |
| --- | --- | --- |
| `os/exec` import restriction (`TestOsExecOnlyInLocalExec`) | Architecture guard suite | Removing the `internal/harness/eval` exception (needed for ACP subprocess supervision) makes the test fail — caught, restored (Task 12). |
| Manifest completeness (`verifyManifestComplete`) | `TestRunScorerIndeterminateWhenRequiredEvidenceMissing` | Weakening the required-role check stops it from reporting `Indeterminate` for a missing required role — caught, restored (Task 9). |
| ACP cancellation escalation reap proof (`escalateCancel`'s SIGKILL rung) | `TestEscalateCancelSigkillResolvesChildThatIgnoresSigterm`, `TestDrainACPPendingLeavesNoProcessBehindForAnUnresponsiveChild` | Skipping the post-SIGKILL reap wait makes both tests fail — caught. The very first run of this same mutation also surfaced a real, separate leak bug in `TestRunACPActionCompactReportsUnprovenShutdownForAnUnresponsiveWriter`'s own cleanup (not deferred, so a failing assertion mid-test skipped it) — fixed by moving cleanup into a `defer`, then the mutation re-verified clean (Task 13/14). |
| Compact transaction's Phase 1 reap proof (`runACPActionCompact`) | `TestRunACPActionCompactReportsUnprovenShutdownForAnUnresponsiveWriter` | Forcing the writer-reap check to always report success makes the test fail differently than expected (proceeds into Phase 2 instead of stopping) — caught, restored (Task 14). |
| Live dual-consent, `liveFlag` half (`RequireLiveConsent`) | `TestRequireLiveConsentRejectsLiveLaneWithoutLiveFlag` | Bypassing the `liveFlag` check for a live lane makes the test fail — caught, restored (Task 17). |
| Live dual-consent, environment-confirmation half (`RequireLiveConsent`) | `TestRequireLiveConsentRejectsLiveLaneWithoutEnvironmentConfirmation` | Bypassing the `OCH_EVAL_LIVE_CONFIRM` check makes the test fail — caught, restored (Task 17). |
| Deterministic judge evidence selection (`buildJudgeEvidenceBundle`) | `TestJudgeBundleIsStableBeforeLimits` | Not a mutation but a real defect found and fixed: the pre-fix builder applied its byte budget while iterating the declared-role *map*, so 40 identical calls over one Attempt produced two different selections. The test was observed failing against `1fe0a3c` and passing after `d3010d6`. |
| Fail-closed omission (`judgeEvidenceBundle.MissingPaths`) | `TestRunJudgeSkipsModelWhenSelectedEvidenceIsOmitted` | Also a real defect, not a mutation: entries dropped by the budget were silently skipped, so a judge could return `pass` over 16 of 40 declared entries with an empty `missingEvidence`. Observed failing against `1fe0a3c`, passing after `d3010d6`. |
| Consent-before-credential ordering (`EvaluateJudgeAttempt`) | `TestEvaluateJudgeAttemptChecksConsentBeforeCaller` | The test asserts the `JudgeCaller` — the only holder of a credential — is never invoked; moving the `RequireLiveConsent` call after the caller makes it fail (Task 17 completion). |
| Production HTTPS-only judge endpoint (`newOpenAICompatibleJudgeCaller`) | `TestJudgeCallerRefusesPlaintextEndpointInProduction` | The same constructor that a sibling test drives against an `httptest` loopback server refuses that exact endpoint under production's own `(nil, false)` arguments; passing `true` in the production path makes the test fail (Task 17 completion). |
| `internal/client/acp` isolation from `internal/harness/eval` (`TestClientPackagesAreIsolatedFromInternalHarness`) | Architecture guard suite | The ACP subprocess executor was built against an independently owned `acp_wire.go` specifically because importing `internal/client/acp` fails this guard — verified by attempting the import and observing the guard fail before building the independent copy instead (Task 12). |

## Real findings this milestone's own work surfaced (not assumed from reading source alone)

- **ACP containment mechanism**: an out-of-workspace path is refused by
  `internal/harness/application/pipeline.go`'s own *lexical*
  `tools.CheckScopeLexical` check, before `Policy.Decide` is ever called at
  all — no `policy.decision.recorded` audit event is emitted for this path,
  only `tool.call.failed` with code `scope_denied`. Discovered by inspecting
  a real Attempt's own committed audit evidence after the originally-assumed
  verifier (checking for a `PolicyDecisionRecorded` deny) failed against real
  evidence; the verifier and the Scenario's own description were both
  corrected (Task 16).
- **`RestartModeInterrupt` against a real, idle `och -acp` agent** does not
  reliably terminate within any bound: `internal/harness/adapters/acp`'s own
  `Serve`/`decodeFrames` loop checks `ctx.Err()` only between already-decoded
  frames, never while blocked reading the next one. Verified with a
  standalone repro: sending SIGINT to a freshly-initialized, otherwise-idle
  process left it running past a 5s wait, where the exact same process
  reaped in well under 5s to SIGKILL. `TestRunACPAttemptInterruptRestartReportsUnprovenReapAgainstAnIdleAgent`
  documents this as the correct, honest `infra_failed` outcome rather than a
  false completion (Task 13).
- **Abrupt ACP restarts vs. the runtime's single-writer fencing lease**:
  `RestartModeKill`'s own successor writer (a new runtime ID) could not
  acquire `internal/harness/adapters/sqlite/lease.go`'s own lease until the
  prior, abruptly-terminated holder's lease naturally expired (default 30s)
  — a killed writer never releases it. `relaunchACPSuccessor`'s own retry
  loop (`ACPShutdownGrades.RelaunchGrace`) was added specifically because
  the first implementation attempt failed fast on this exact condition
  during real testing against the real `och` binary (Task 14).
- **`promptAsync` write-ordering race**: the original implementation started
  the request-writing goroutine asynchronously, so a `cancel` action
  immediately following its own `prompt` action could race that prompt's own
  frame onto the wire. Caught by a real, repeatable test failure (not a
  flaky timing assumption) once `TestEscalateCancelSessionCancelResolvesWithoutTearingDownProcess`
  exercised the `cancel-aware` acpchild double; fixed by making `callAsync`
  write synchronously before returning (Task 13).
- **`RunEvalSet`/`och-eval run` had refused every `acp_subprocess` Executor
  outright since Task 10**, a "Stage A" restriction nothing since Task 12
  had actually lifted — meaning a paired ACP Cell could not run through the
  standard runner/CLI path at all before Task 15. `RunnerInputs` gained
  `ACPLaunch`, and the runner now dispatches to `RunACPAttempt` when a
  Cell's own `Executor.Kind` calls for it (Task 15).

## Benchmark data

`internal/harness/eval/benchmark_test.go` (`internal/harness/eval/benchmark_acp_test.go`
for the subprocess-specific one). Every number below is expansion/recovery/
reporting/export cost alone — no model call ever leaves this process for any
of them.

Go 1.26.6, linux/amd64, 2-vCPU cloud instance, commit `45964ff` (the Task 17
tip this benchmark run was taken against; re-run at Task 18's own tip is
this PR's own commit).

```text
$ go test ./internal/harness/eval/... -run '^$' -bench '.' -benchtime=1x -count=1

BenchmarkACPProcessStartupAndShutdown-2               1        51575144 ns/op       127368 B/op        389 allocs/op
BenchmarkExpandAttempts/cells=1-2                      1           57708 ns/op         3536 B/op         20 allocs/op
BenchmarkExpandAttempts/cells=100-2                    1         1467210 ns/op       173944 B/op        716 allocs/op
BenchmarkExpandAttempts/cells=1000-2                   1        15474151 ns/op      1741712 B/op       7033 allocs/op
BenchmarkExpandAttempts/cells=4096-2                   1        61175925 ns/op      7102440 B/op      28710 allocs/op
BenchmarkClassifyAttemptDirectory/cells=1-2            1          300636 ns/op        33280 B/op        759 allocs/op
BenchmarkClassifyAttemptDirectory/cells=100-2          1        20070133 ns/op      3331464 B/op      75902 allocs/op
BenchmarkClassifyAttemptDirectory/cells=1000-2         1       180661016 ns/op     33315056 B/op     759024 allocs/op
BenchmarkAssembleEvaluationResult/cells=1-2            1          134424 ns/op         7216 B/op        173 allocs/op
BenchmarkAssembleEvaluationResult/cells=100-2          1         5158680 ns/op       723200 B/op      17300 allocs/op
BenchmarkAssembleEvaluationResult/cells=1000-2         1        48316281 ns/op      7232640 B/op     173006 allocs/op
BenchmarkCollectEvidence-2                             1        42451998 ns/op      3169672 B/op      51299 allocs/op
```

Interpretation: pure in-memory matrix expansion scales roughly linearly with
Cell count and stays well under 100ms even at the design's own hard cap of
4096 Cells (61ms). Recovery classification and report aggregation are
dominated by real filesystem I/O per Attempt directory (both scale from
~0.1–0.3ms at 1 Cell to ~20–50ms at 100 and ~50–180ms at 1000, roughly
linear) — `BenchmarkClassifyAttemptDirectory` costs noticeably more per
Attempt than `BenchmarkAssembleEvaluationResult` since it reads and parses
three documents (`attempt.json`/`outcome.json`/`manifest.json`) per
directory rather than two. Real ACP subprocess startup+handshake+shutdown
(~52ms) and real evidence export for one Attempt (~42ms) are both an order
of magnitude larger than a single Cell's own pure-expansion or
classification cost, confirming design's own intuition that orchestration
and process-lifecycle cost, not the runner's own bookkeeping, dominates a
real Attempt's wall time — separated out here specifically so that fact
does not get conflated with (nonexistent, in this benchmark suite) model
latency.

Benchmarks were not re-run against Task 18's own final commit after
documentation-only changes; the numbers above are the real, most recent
measurement taken during this milestone's own development, not fabricated.

## Verification command output

Go 1.26.6, linux/amd64, 2-vCPU cloud instance. Run against this PR's own
working tree.

```text
$ go build ./...
(clean)

$ go vet ./...
(clean)

$ CGO_ENABLED=0 go build ./...
(clean)

$ GOOS=windows GOARCH=amd64 go build ./...
(clean)

$ GOOS=darwin GOARCH=arm64 go build ./...
(clean)

$ go test ./... -race -count=1
ok  	github.com/SongYii/open-code-harness/cmd/acp-client
ok  	github.com/SongYii/open-code-harness/cmd/acp-web-bridge
ok  	github.com/SongYii/open-code-harness/cmd/och
ok  	github.com/SongYii/open-code-harness/cmd/och-eval
ok  	github.com/SongYii/open-code-harness/internal/client/acp
ok  	github.com/SongYii/open-code-harness/internal/client/acpweb
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
ok  	github.com/SongYii/open-code-harness/internal/harness/contextengine
ok  	github.com/SongYii/open-code-harness/internal/harness/domain
ok  	github.com/SongYii/open-code-harness/internal/harness/engine
ok  	github.com/SongYii/open-code-harness/internal/harness/eval
ok  	github.com/SongYii/open-code-harness/internal/harness/policy
ok  	github.com/SongYii/open-code-harness/internal/harness/redact
ok  	github.com/SongYii/open-code-harness/internal/harness/runtime
ok  	github.com/SongYii/open-code-harness/internal/harness/testkit
ok  	github.com/SongYii/open-code-harness/internal/harness/tools
ok  	github.com/SongYii/open-code-harness/internal/harness/transcript
```

Process-leak check: `ps aux | grep -iE "acpchild|/och "` after every full
test run in this session found nothing — no ACP subprocess or `acpchild`
test double was ever left running.

## Known limitations and open blockers (not GA)

See the contract document's own [Maturity and GA blockers](evaluation.md#maturity-and-ga-blockers)
section. Summarized: real-model live-judge sample size, judge
meta-evaluation breadth beyond this milestone's own five-case fixture suite,
provider breadth beyond one OpenAI-compatible adapter, and an accepted
variance policy for live/quality signals are all explicitly outstanding.
MCP is a future suite this runner can host, never a runner prerequisite.
