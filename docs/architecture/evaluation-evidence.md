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
| `edcbab5` | Variance research | Repetition and variance in evaluation frameworks (PR #178) |
| `bfb3399` | Variance research | Answers to the gate's four questions, with three amendments |
| `25ca24a` | Variance design | Accept the variance policy design and plan its implementation (PR #173) |
| `dd1d7c5` | Variance design | Split the reliability gate, defer reducers, say the mechanism ships dormant |
| `0e972aa` | Variance 1 | Frozen `och.eval.variance-policy` document (PR #174) |
| `303dfec` | Variance 2/3 | Cell distribution, spread, stability, and indeterminate handling (PR #175) |
| `18fa46b` | Variance 2 (amended) | Split the reliability gate into a structural fact and a threshold judgement |
| `bd7fb2c` | Variance 4 | Fail-closed rules (PR #177) |
| `6c2d530` | Variance 5 | Pinned, regenerable `och.eval.baseline` document |
| `1e9bd96` | Variance 6 | Paired-arm delta computed between distributions |
| `4287c7b` | Variance 6b | Attempt/Score grouping into Cells by identity digest |
| `a04371f` | Variance 4–6 (amended) | Disclosure replaces refusal where the limit is only a guess |
| `895ee1d` | Variance 7 | Report distribution block, baseline command, derived reliability readings |

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
  budget. That deferral has since been closed: the suite landed with ten
  Scenarios, each paired with the ACP executor. This bullet records the state
  at the time of Task 16, not the current one.
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
- **Idle ACP interrupt.** Recorded in the design's section 12.1 and since
  fixed: ACP's frame read is now released when its Serve context is
  cancelled, so SIGINT reaps an idle agent (25s without reaping, then 1.4s
  complete). `context-checkpoint-interrupt-restart` is part of the suite.

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
- **Multi-chunk** is covered by its criterion and mutation tests rather than
  by an end-to-end Scenario, which is a recorded decision rather than an open
  gap: the feasible band is about 800 tokens wide on a 4096-token window, and
  widening `maxCompactSessionFocusBytes` to open it was rejected because that
  cap is a safety boundary on operator-supplied prompt text. See section 11.1
  of the suite design. Every other mechanism runs end to end on both executor
  surfaces, including checkpoint reuse across `clean_shutdown`, `interrupt`,
  and `kill` restarts.
- Both CI lanes are wired: one representative Context Cell in ordinary PR CI
  (`pr-context.json`, still exactly four Cells total) and every paired set
  plus ACP recovery in the scheduled lane, with the scan regression and its
  benchmarks guarded against removal. As landed in `10190a2` the two lanes
  were not actually separated; see the section below.
- No claim is made about a crash during an open compaction bracket.

## Context suite: the scheduled lane was never isolated

Recorded as its own finding because it is the exact failure mode this
repository's executable-documentation rules exist for, reappearing one layer
down — in CI configuration rather than in prose.

`10190a2` landed the scheduled lane with the stated split "a tiny PR lane, the
complete matrix only by explicit command". Three places said so: the lane's own
comment (`cmd/och-eval/context_scheduled_test.go`), that commit message, and
the Evaluation contract's own [four-Cell PR lane](evaluation.md#the-four-cell-pr-lane)
section. The gate in the code was `testing.Short()`.

No CI job passes `-short`. So the full nine-set matrix — five of whose sets
start real `och -acp` subprocesses — ran on **every pull request**: once in the
`go` job (`go test -race ./... -count=1`) and three more times in
`determinism` (`-count=3`), with `soak` adding ten more nightly. Measured on
the development machine at 39s without the race detector and 64s with it, so
the pull-request path was paying roughly 4.3 minutes per PR for a lane three
documents said it never ran, and the nightly soak was repeating an end-to-end
subprocess matrix ten times as if it were a flakiness sample.

The fix is an opt-in named `OCH_EVAL_SCHEDULED_CONTEXT_MATRIX`, following the
`DOCSGUARD_CHECK_EXTERNAL_LINKS` precedent already in
`internal/docsguard/citations_test.go`: only `"1"` enables the lane, anything
else fails closed. Exactly one CI job sets it — `context-matrix`, gated on
`if: github.event_name == 'schedule'`, running one focused command
(`go test -race ./cmd/och-eval -run '^TestContextScheduledLane' -count=1`).
`-short` was deliberately not used: it would have silently changed which other
tests run.

The guards check executable facts, never comments. Parsing a comment to see
whether it agrees with CI would reproduce the original defect, since the
comment was already correct and the wiring was not.

| Guard | What it asserts |
| --- | --- |
| `TestFullContextMatrixSkipsWithoutTheOptIn` | Re-invokes this test binary (`os.Args[0]`) with the variable stripped from the environment and requires `--- SKIP` from the matrix test. Proves default-off by running it, not by reading it. |
| `TestCIEnablesTheFullContextMatrixOnlyInAScheduledJob` | Parses `.github/workflows/ci.yml`: exactly one job may set the variable, it must carry `if: github.event_name == 'schedule'`, and its single `go test` invocation must be focused on `./cmd/och-eval`, name `^TestContextScheduledLane`, and use `-count=1`. |
| `TestBroadSuiteJobsNeverEnableTheFullContextMatrix` | The same file's whole-suite jobs — `go`, `determinism`, `soak` — must all still exist and none may set the variable. |
| `TestScheduledContextMatrixOptInFailsClosed` | `""`, `"0"`, `"true"`, `"yes"`, `"2"`, `" 1"` all leave the matrix off; only `"1"` enables it. |
| `TestScheduledLaneCoversEveryCheckedInContextSet` | `contextScheduledSets` is maintained by hand, so a tenth set added later would simply never run while the lane still passed. Membership is decided by two independent facts — the set's own declared `fixture` lane and the `context-` id prefix that separates it from the PR lane's `pr-context` — not by a filename convention alone. |
| `TestEveryInProcessContextSetHasAnIdenticalACPArm` | The suite design's pairing claim as a structural fact: every `context-X-inprocess` set has a `context-X-acp` twin carrying the identical Scenario list, the first declaring an `in_process` executor and the second an `acp_subprocess` one. `context-recovery-acp` has no in-process arm by design, since restart recovery is only meaningful against a real subprocess. |

The workflow file is parsed line-wise into job blocks rather than with a YAML
library, because the repository pins its dependency graph (`go mod tidy -diff`,
govulncheck) and a new module is not worth four assertions over a file this
project writes itself.

Five mutations were performed and observed, then restored:

| Mutation | Result |
| --- | --- |
| Delete `requireScheduledContextMatrix(t)` from the matrix test | `TestFullContextMatrixSkipsWithoutTheOptIn` fails in 38.7s — the child process really did start running the nine-set matrix instead of skipping, and the guard reported the missing SKIP. Caught, restored. |
| Add the variable to the `go` job (the pull-request path) | Both CI guards fail: `2 CI jobs set OCH_EVAL_SCHEDULED_CONTEXT_MATRIX ([go context-matrix]); exactly one may`, and `job "go" runs the whole suite and sets ...`. Caught, restored. |
| Change the scheduled job to `-count=3` | `TestCIEnablesTheFullContextMatrixOnlyInAScheduledJob` fails. Caught, restored. |
| Delete `if: github.event_name == 'schedule'` from the scheduled job | Same guard fails. Caught, restored. |
| Widen the scheduled job's command to `go test -race ./... -count=1` | Both CI guards fail. Caught, restored. |
| Drop `context-anchor-acp.json` from `contextScheduledSets` | `TestScheduledLaneCoversEveryCheckedInContextSet` fails, naming the set that would have stopped running. Caught, restored. |
| Delete one of `context-core-acp.json`'s four Scenarios, leaving its in-process twin intact | `TestEveryInProcessContextSetHasAnIdenticalACPArm` fails with both Scenario lists. Caught, restored. The first attempt at this mutation used `context-anchor-acp.json`, which declares a single Scenario, so removing it produced an empty set that an unrelated pre-existing validation rejected first (`at least one scenario is required`) — a red test proving nothing about this guard. Redone against a four-Scenario set. |

One process note, since this ledger records how evidence was obtained and not
only its result: the first attempt at the last three mutations restored the
workflow with `git checkout`, which reverted the not-yet-staged
`context-matrix` job along with the mutation, so those runs were observed
against a file with no such job at all and proved nothing about the intended
mutation. They were redone against a file-copy baseline, with the unmutated
baseline confirmed green first. The results above are from the redone runs.

## Variance: the design was written before the research

This is recorded because it is the same class of failure as the scheduled
lane above — a stated rule and the actual practice diverging — and hiding it
would make this ledger the sort of document the rules exist to prevent.

The charter gained §12.1, "research first, do not reinvent the wheel", on
2026-09-03, from this same sequence of work. The variance design was accepted
on 2026-09-04 and seven implementation tasks were built without that rule
being applied to the variance mechanism itself, while four dedicated
evaluation frameworks sat unread in `.reference/`. The
[repetition and variance gate](../research/architecture-gates/2026-09-05-eval-repetition-and-variance.md)
is that research, written late and saying so, and the
[answers to its four questions](../research/architecture-gates/2026-09-05-eval-variance-question-answers.md)
are what the design was then amended against.

Nothing the research found made the implemented arithmetic wrong. What it
changed was shape, vocabulary, and one claim about who consumes this.

### Contracts the implementation and the late research corrected

- **One `Trustworthy` bool became two fields.** The design specified a single
  boolean with a reason string. Two independent probes killed it. A Cell with
  one evaluable repetition of five reported `trustworthy = true` with perfect
  stability, because a lone survivor is trivially unanimous — the
  "perfect stability, measured never" hazard arriving through run time rather
  than through configuration, where the policy document's own two-repetition
  floor cannot see it. And a Cell with nothing evaluable reported "stability
  0.0000 is below 0.8000", telling an operator it was judged inconsistently
  when it was never judged at all. Both are now separate fields with separate
  reasons, because a consumer branches on a boolean and does not read the
  string beside it.
- **An uncalibrated limit lost the power to change a result.** The design let
  any declared-limit breach make a Cell unreadable. Since the design also
  forbids shipping default limits — no live run has ever happened here —
  that gave a guessed number the authority to rewrite five passes into a
  non-pass, and to decide what a baseline was allowed to record. The rule is
  now split by warrant: the structural half blocks unconditionally, the
  threshold half only once the limits cite the run that produced them.
  Applying it changed real behaviour at both downstream call sites, and the
  change is the point rather than a side effect.
- **"Never collapse" became "publish the distribution, defer the decision
  rule".** inspect_ai expresses this project's mandatory behaviour as one
  named reducer among nine (`collect`), so the shared value is that the
  combination rule is explicit, not that no named answer may exist. The
  design now records the long-term shape and deliberately builds none of it,
  because no lane asks whether a Cell passed and the charter forbids
  pre-building an extension point with no consumer. The vocabulary is
  deliberate: a *decision rule* sits on top of a distribution it may never
  delete, where inspect_ai's *reducer* replaces the per-epoch view.
- **Design open question 3 resolved as "no", on two grounds.** The
  deterministic lane takes no variance policy, following the design's own
  reasoning that folding a determinism check in would blur the distinction it
  rests on. Implementation then found a second, independent reason: a
  proposal to run the fixture lane at `N = 2` with "spread must be exactly 0"
  is *vacuous*. `NumericScore` is assigned only at `judge.go:233`,
  `judge.go:240`, and `judge_attempt.go:159` — all judge paths — so the
  deterministic scorer never produces one, a fixture lane's `numericScores`
  is empty, `spread` is `0` by construction, and the rule would pass
  unconditionally while proving nothing.
- **A floating-point comparison needed a documented tolerance.** A spread
  between the perfectly ordinary scores 0.6 and 0.8 computes as
  0.20000000000000007, which is strictly greater than a declared limit of
  0.20. Without `limitEpsilon` every policy would have been one notch
  stricter than it reads.

### What this work does not close, and did not pretend to

No checked-in EvalSet reaches any of this code. All sixteen declare
`repetitionCount: 1`, and the fail-closed rule refuses a set that references
a policy at one repetition, so no configuration exists that could exercise
the mechanism — a fact recorded in the design's own baseline section and then
built past for seven tasks before the late research named it.

No configuration was invented to fix that. The mutation tests prove the
computation is correct; calling them "this round's consumer" was considered
and rejected as a relabelling, because a mutation test is not the real
consumer the charter means. The mechanism ships **dormant**, the design says
so in those words, and the first configuration that should reference a
variance policy is the first live quality EvalSet.

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
| Uncalibrated limits may not block a result (`CellDistribution.MayBeReadAsAResult`) | `TestAnUncalibratedLimitBreachDoesNotBlockReportingACellAsAPass` | Making the rule consult `ExceedsDeclaredLimits` regardless of calibration makes the test fail — "an uncalibrated limit rewrote a Cell of five passes into a non-pass" — caught, restored (Variance 2 amended). |
| The threshold half stays silent for an unmeasured Cell (`judgeDeclaredLimits`) | `TestNoEvaluableRepetitionsIsNamedAsSuchNotAsInstability` | Letting it speak for a Cell with nothing evaluable makes the test fail — "a Cell with nothing evaluable must not be reported as unstable" — caught, restored (Variance 2 amended). |
| Disclosure over refusal in the baseline and the paired arm (`MayBeReadAsAResult` at both call sites) | `TestBaselineRecordsAWideCellUnderAnUncalibratedLimit`, `TestAWideArmUnderAnUncalibratedLimitIsDisclosedRatherThanRefused` | Making an uncalibrated breach unreadable again makes both tests fail — caught, restored (Variance 4–6 amended). |
| Derived readings need two evaluable repetitions (`reliabilityOf`) | `TestDerivedReadingsAreAbsentBelowTwoEvaluableRepetitions` | Lowering the floor to one makes the test fail, reporting `AtLeastOnePassed:true AllPassed:true` from a single sample — caught, restored (Variance 7). |
| A per-Cell count may not borrow a dataset-level estimator's name (`reportCellReliability` JSON tags) | `TestDerivedReadingsAreNotNamedPassAtK` | Renaming `atLeastOnePassed` to `passAtK` makes the test fail — caught, restored (Variance 7). |

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

### Variance slice, 2026-09-05

Run against the variance stack's own working tree, same Go 1.26.6 host.

```text
$ go build ./...
(clean)

$ go vet ./...
(clean)

$ GOOS=windows GOARCH=amd64 go build ./...
(clean)

$ GOOS=darwin GOARCH=arm64 go build ./...
(clean)

$ go mod tidy -diff
(clean)

$ go test -race ./internal/harness/eval/ ./cmd/och-eval/ ./internal/docsguard/
ok  	github.com/SongYii/open-code-harness/internal/harness/eval	207.567s
ok  	github.com/SongYii/open-code-harness/cmd/och-eval	43.521s
ok  	github.com/SongYii/open-code-harness/internal/docsguard	1.312s

$ go test ./...
(all packages ok)
```

CI on the whole five-PR stack (#178, #173, #174, #175, #177) reported
`cross-build (darwin)`, `cross-build (windows)`, `determinism`, `go`, and
`vulncheck` all passing.

## Judge meta-eval: two of the original five fixtures proved nothing

Found on 2026-09-04 while broadening the meta-eval suite, and recorded here
because the defect is in the *tests*, which is where it is hardest to see: a
green test that is satisfied by the wrong refusal looks exactly like a green
test that works.

`testJudgeConfig` declares two criteria, `quality` and `continuity`.
`TestRunJudgeRejectsNonexistentEvidenceReference` and
`TestRunJudgeUnresolvedContradictionIsAlwaysIndeterminate` each named only
`quality` in the judge output they fed in. `RunJudge` checks for an omitted
required criterion (`judge.go`, the `seenCriteria` loop) *before* it validates
evidence references or applies the contradiction rule, so both fixtures were
refused with `judge output omitted required criterion "continuity"` and never
reached the code they existed to test. Both asserted only
`Verdict == Indeterminate`, which that earlier refusal satisfies.

Proven by mutation rather than by reading:

| Mutation | Before the fix | After the fix |
| --- | --- | --- |
| Delete the `available[ref]` "never shown to it" check entirely | `TestRunJudgeRejectsNonexistentEvidenceReference` **still passed** | Fails, as does the new real-but-unshown fixture |
| Replace the contradiction/missing branch condition with `if false` | `TestRunJudgeUnresolvedContradictionIsAlwaysIndeterminate` **still passed** | Fails |
| Delete the new citation-free check | n/a — the check did not exist | `TestRunJudgeRejectsADeterminateVerdictCitingNoEvidence/pass` fails with `Verdict = "pass", want "indeterminate"` |

The contradiction fixture also returned an empty `ContradictoryEvidence`
list, which is direct evidence the branch never ran; it now asserts the audit
path survives into the outcome.

Both fixtures now declare every required criterion and assert the refusal
**reason**, not merely that some refusal happened. That assertion is the
anti-recurrence measure: a meta-eval fixture that accepts any refusal will
eventually be satisfied by the wrong one.

## Judge meta-eval: a determinate verdict citing nothing was believed

A real production gap found by the same pass, in the same family as the
already-fixed budget-omission defect. Every evidence-reference rule guarded
the references that were present — nonexistent, empty-string, and duplicate
references are all refused — and none required any reference to exist. A
judge returning `pass` with `evidenceReferences: []` was accepted at face
value, so the most economical way for a judge to pass an Attempt was to cite
nothing at all.

`RunJudge` now refuses a determinate verdict that cites no evidence,
contradiction, or missing entry. `indeterminate` is deliberately exempt and
has its own fixture (`TestRunJudgeIndeterminateMayCiteNoEvidence`), so the
guard cannot later be tightened into refusing every citation-free output —
citing nothing is often exactly why an attempt is indeterminate.

The adversarial fixture set is now eight: injection, missing-evidence,
contradiction, unsupported-claim, known-pass/fail, an invented reference, a
real-but-unshown reference (`workspace/output.txt` — a genuine manifest entry
that no declared criterion role puts in the bundle, which is the shape a
reference check written against the manifest instead of the bundle would
wrongly accept), and a determinate verdict citing nothing.

## Known limitations and open blockers (not GA)

See the contract document's own [Maturity and GA blockers](evaluation.md#maturity-and-ga-blockers)
section. Summarized: real-model live-judge sample size, judge
meta-evaluation breadth beyond the eight adversarial fixtures recorded above,
provider breadth beyond one OpenAI-compatible adapter, and an accepted
variance policy for live/quality signals are all explicitly outstanding.
MCP is a future suite this runner can host, never a runner prerequisite.

The variance blocker changed shape on 2026-09-05 without closing. The
mechanism is implemented and verified and this ledger records its evidence;
the policy is not accepted, because no calibrated limits exist and producing
them requires the live run the first blocker in this list says has never
happened. The mechanism is also dormant — no checked-in EvalSet reaches it.
An implemented mechanism counted as an accepted policy would be exactly the
claim the contract's own no-defaults rule exists to prevent.
