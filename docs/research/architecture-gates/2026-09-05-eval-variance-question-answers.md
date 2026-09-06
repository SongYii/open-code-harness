# Proposed answers to the four variance-design questions

**Status:** Research evidence. Proposed answers to the four questions in the
[repetition and variance gate](2026-09-05-eval-repetition-and-variance.md).
Nothing here amends the
[variance policy design](../../superpowers/specs/2026-09-04-evaluation-variance-policy-design.md)
by existing. Adopting these answers is a design revision; until that happens
they are a reviewer position, not a contract.

**Date:** 2026-09-05

**Why this exists:** the variance work stopped itself. Four implementation
PRs (#173, #174, #175, #177) are open and unmerged, Task 7 is complete but
uncommitted, and #178 records the research that should have preceded the
design. The research asked four questions. A later note supplied options,
costs, and a recommendation for each. This document answers those questions
against a wider industry comparison, says where that recommendation is
accepted, and says where it is not.

The synchronized Chinese reading copy is
[2026-09-05-eval-variance-question-answers.zh-CN.md](2026-09-05-eval-variance-question-answers.zh-CN.md).
If the copies diverge, this English document wins.

## What this is not

This is not a finding that the implemented mechanism is unsound. The
research already said that, and it still holds: distribution, range,
fail-closed refusal of `repetitionCount: 1` under a policy, identity-digest
grouping, a pinned baseline a run path cannot write back, and a paired
delta computed between distributions are all keepers. What may change is
shape and who consumes it, not whether the arithmetic is trustworthy.

This is also not a stall in the implementation. The four PRs have mutation
evidence. The stop is a design freeze after building first and researching
second, then discovering that no checked-in EvalSet can reach the new
code.

## The short version

The research diagnosis is right. The recommended landing scope is too
large.

Long-term shape: always publish the distribution; a named decision rule
may sit on top of it later; never let a reduction replace the raw
counts. That is a better inversion of inspect_ai's default than a
prohibition on giving any named answer at all.

This round: do not build a reducer catalogue; do not put the variance
policy on the deterministic lane; do not keep a single `Trustworthy`
bool that mixes a structural fact with an uncalibrated threshold. Split
the gate, add cheap derived reliability fields to the report, say out
loud that the mechanism ships dormant with no consumer, and amend the
four PRs in place instead of reshaping them.

| Question | CC's recommendation | This document's recommendation |
| --- | --- | --- |
| 1. Named reducers vs. a prohibition | Always publish the distribution, and add three named reducers now (`distribution`, `at_least(k)`, `pass_at(k)`); no mean/median | Accept the long-term shape; do not add a reducer catalogue this round. Call a future named answer a **decision rule** applied on top of the distribution, not a reducer that replaces it |
| 2. `pass@k` | Add as a reported metric, not a gate; optional; ~30 lines | Add as derived report fields, not a gate and not a reducer. Report both the optimistic envelope and the reliability number. Do not name Cell-level "at least one pass" as `pass_at(k)` |
| 3. Trustworthiness gate | Keep the bool; split structural reasons from threshold reasons; write "nobody else has this" next to the decision | Split into two fields. Structural insufficiency is a hard reporting rule. An uncalibrated threshold breach is advisory metadata and must not rewrite a Cell as "not a pass" until calibrated. A single bool undoes the split |
| 4. First consumer | Repeat the deterministic lane with "spread must be exactly 0" | The mechanism ships **dormant** — no consumer this round, and the design says so. A determinism check is a separate, later, cheap mechanism (smoke only, not the scheduled Context matrix) and is not a variance policy; spread cannot even be its signal. The first real variance consumer is the first live EvalSet |

CC's overall recommendation was "do 1, 3, and 4, and treat 2 as optional,"
then revise the design and reshape the four PRs. The position here is:
adopt the long-term shape in the design, change almost none of the
implementation shape, and keep the four PRs.

## Industry practice the four questions sit on

The comparison set in the 2026-09-05 gate (inspect_ai, terminal-bench,
evals, vitest-evals, Maka) is the right set for *this repository's*
`.reference/` checkouts. The four questions also have a wider industry
shape that the gate did not need to settle, and that the later
recommendation under-weighted.

### Persistence is universal; reduction is a later, named step

- inspect_ai binds count and combination in one `Epochs(epochs, reducer)`
  object. Per-epoch scores stay in the log. The default reducer is
  `mean`. `collect` preserves every value. Several reducers may be named
  at once, and a `headline_metric` records which reduced number is the
  headline ([Scoring Metrics](https://inspect.aisi.org.uk/metrics.html)).
- terminal-bench persists every trial and, at summarize time, publishes
  a `pass@k` curve at powers of two.
- Maka expands `repetitions` into cells and aggregates nothing. That is
  the closest cousin of this project *without* the variance mechanism.
- HumanEval / HuggingFace `code_eval` keep all `n` samples and estimate
  `pass@k` afterwards (Chen et al., 2021, [arXiv:2107.03374](https://arxiv.org/abs/2107.03374)).

No mature framework makes "keep the distribution" a prohibition on
named answers. They make the combination rule explicit. inspect_ai's
`collect` is this project's mandatory behaviour expressed as one named
choice among nine. The shared value is "the rule is not hidden." The
expression differs: they offer a choice, this design wrote a ban.

### Stochastic agent evals report a curve, not a calibrated gate

- Chen 2021's `pass@k` is the probability that at least one of `k`
  attempts succeeds, estimated without bias as
  \(1 - C(n-c,k)/C(n,k)\) given `n ≥ k` samples of which `c` passed.
- terminal-bench's answer to "is this measurement stable?" is the
  `pass@k` curve itself. No threshold is required to read it.
- τ-bench (Yao et al., 2024, [arXiv:2406.12045](https://arxiv.org/abs/2406.12045))
  reports **pass^k**: the probability that *all* `k` independent trials
  succeed. That number falls as `k` grows. It is the reliability metric,
  and it is closer to this project's charter than `pass@k`.
- inspect_ai ships both: `pass_at(k)` is Chen's estimator; `pass_k` /
  `at_least(k)` are "all k succeed" / "at least k succeed."
- *On Randomness in Agentic Evals* (2026) finds 2–6 percentage point
  swings in single-run `pass@1` even at temperature 0, and recommends
  reporting both the optimistic envelope and the pessimistic one.

A Cell-level "did at least one of these k attempts pass?" is
`at_least(1)`, not Chen's `pass@k`. `pass@k` is ordinarily a
dataset-level fraction of tasks. At the sample sizes a nightly lane can
afford here (single digits, often 2), the unbiased estimator and the
raw `c/n` counts are almost the same fact. The interesting suite-level
curve can wait until there is a suite it would describe.

### Uncertainty is disclosed; an uncalibrated threshold is not a gate

inspect_ai, EleutherAI `lm-eval-harness`, and HELM report `mean ±
stderr` or a bootstrap interval. Correlated measurements use
**clustered** standard errors, which is why inspect_ai reaches for them
when samples are not IID. Miller (2024) states the same rule. None of
these frameworks refuse to publish a score because a within-cell spread
exceeded a number nobody has measured.

The gate's search for `untrustworthy` / `unreliable` / `too_variable` /
`insufficient_samples` across five frameworks returning nothing
on-point is still true. The closest industrial analogues are different
mechanisms:

- statistics: `n` insufficient, an A/B test reported as inconclusive;
- inspect_ai: `NaN` when every sample is filtered, which is more honest
  than the four sites that return `stderr = 0` from one sample;
- CI: a flaky-test quarantine.

Refusing to report `stderr = 0` from one sample is a place this design
is deliberately stricter than inspect_ai, and that refusal should stay.
Folding an uncalibrated numeric limit into the same boolean is not the
same refusal.

### Deterministic reruns are flake detection, not a variance policy

Bazel `--runs_per_test` plus `--runs_per_test_detects_flakes` yields a
**FLAKY** status, not a quality-signal judgement
([Command-line reference](https://bazel.build/reference/command-line-reference)).
Go `-count=N` is the same idea. Uber's pre-land flake detection uses
`runs_per_test` the same way. The rule is "run it twice, the outcomes
must match." It does not need `maxNumericSpread`,
`minVerdictStability`, a baseline document, or a paired delta.

The variance design's own open question 3 already said this: a fixture
lane's `spread > 0` is a determinism defect, and folding it into the
variance policy would blur the distinction the document rests on. #173
accepted the design with that question left open and out of scope.

## Question 1 — prohibition vs. named reducers

The later recommendation's hybrid — always publish the distribution,
and allow one named reduction beside it — is the right long-term
shape. It is cleaner than inspect_ai: inspect_ai's default `mean`
erases 4/5 unless the caller also asks for `collect`. This project
should invert that default.

It is the wrong thing to implement this round.

| | Keep the prohibition | inspect_ai-style catalogue now | Distribution always, named decision rule later |
| --- | --- | --- | --- |
| Keeps | Output shape unique and easy to test | The combination rule enters a frozen, digestible document | Raw counts cannot be hidden; a consumer that needs yes/no gets a named rule |
| Costs | Every consumer invents a rule off to the side, which is less auditable than naming one | Someone will ask for `mean` next; no consumer needs a catalogue today | One extra field, which can stay unused |

**Decision:** record the long-term shape in the design. Do not add
`distribution` / `at_least(k)` / `pass_at(k)` as reducers in this
slice.

Reasons, in the order they bind:

1. The charter wants the rule explicit. It does not want the framework
   to refuse to give an answer. Those are different sentences.
2. No lane currently asks "did this Cell pass?". The parent evaluation
   design keeps live quality signals advisory until four GA blockers
   have separate accepted evidence, and forbids gating ordinary PR CI
   on a quality signal. A named decision rule without a consumer is
   the same unused mechanism the research just caught, under a new
   name.
3. `mean` and `median` stay rejected. That rejection is about not
   erasing 4/5, and it survives. `at_least(k)` and `pass_at(k)` wait
   for the first consumer that needs a yes/no.
4. Do not call it a reducer. inspect_ai's reducer **replaces** the
   per-epoch view that metrics then see. This project's named answer
   should be a **decision rule**: applied on top of the published
   distribution, never deleting the raw counts. That is how "always
   publish the distribution" stays true after the first named answer
   lands.

## Question 2 — `pass@k`

Add it as a reported metric, not a gate. That part of the later
recommendation is right. Two corrections.

First, a Cell-level "k attempts, at least one passed" is
`at_least(1)`, not `pass_at(k)`. inspect_ai's `pass_at(k)` is Chen's
estimator. Mixing the names will make the first public comparison
dishonest.

Second, for a harness judging its own reliability, **pass^k** (all `k`
succeeded) is the more load-bearing number. `pass@k` is the capability
envelope; pass^k is whether the thing can be trusted unattended.
τ-bench introduced pass^k for exactly that reason. This project's
charter is closer to τ-bench than to a leaderboard.

**Decision:** derived fields on the existing distribution block, not a
reducer and not a gate.

1. At Cell level, publish what the counts already say: evaluable,
   pass, fail, indeterminate. At `N = 2`, "one passed" and "both
   passed" *are* pass@1 and pass^2. Chen's estimator is not worth
   a separate implementation until `N` is large enough that it
   disagrees with `c/n`.
2. A suite-level `pass@k` curve is terminal-bench's shape. The
   checked-in suites are too small for that curve to mean anything
   yet. Do not draw it this round.
3. These fields belong on Task 7's report block. They do not depend
   on a reducer concept.

This is not optional in the sense of "leave it for later if busy." It
is the only signal in the proposal that does not need a calibrated
threshold, which is the whole point of a first policy that admits it
is uncalibrated. It is optional in the sense of "it is not a gate and
it is not a reason to reshape the four PRs."

## Question 3 — the trustworthiness gate

Splitting structural facts from threshold judgements is right. Keeping
one `Trustworthy bool` after the split is not. A consumer will read
the bool and ignore the reason text. That is how "one survivor of five
was reported perfectly trustworthy" (#175) happens: two different
statements share a label.

"Nobody else has this gate" should be written next to the decision. It
changes the threshold half, not the structural half.

| Kind | Example | Industry analogue | This round |
| --- | --- | --- | --- |
| Structural fact | 1 of 5 evaluable; all indeterminate | `n` insufficient; inspect_ai `NaN`; A/B inconclusive | Hard reporting rule: the Cell must not be reported as a pass. This is the refusal of `stderr = 0`, and it is not a novelty |
| Threshold judgement | spread 0.33 exceeds a declared 0.20 | none; the number is uncalibrated | Advisory metadata, inheriting the policy's own `uncalibrated` mark. Until a cited run produces the limits, this must not rewrite a Cell from pass to "not a pass" |

**Decision:** two fields, not one bool.

```text
evaluableEnough        # structural. false ⇒ must not be reported as a pass
exceedsDeclaredLimits  # threshold comparison, carrying the calibration state
```

The unique-among-frameworks claim belongs next to
`exceedsDeclaredLimits`, with the charter's conservatism named as the
reason it exists at all. It does not belong next to
`evaluableEnough`.

## Question 4 — the first consumer

This is the real product question, and the later recommendation's
third path solves the wrong problem.

The two-way bind is real: repeating the deterministic lane is cheap
and does not prove the mechanism has value; waiting for a live model
run leaves the mechanism idle in an environment with no credentials.
"Repeat the deterministic lane, but as a determinism check whose
threshold is exactly 0" is a good *determinism* check and a bad first
consumer of the *variance policy*.

| Path | Proves | Costs |
| --- | --- | --- |
| A. Deterministic lane at `N = 2` as a variance consumer | The pipe is wired | Spread is 0 by definition, so thresholds, 4/5, and the uncalibrated disclosure are never hit. Open question 3 forbade this. The scheduled Context matrix was just isolated from the PR path (#157) because it was already too expensive to run there |
| B. Wait for the first live run | Variance as a live-lane concept | Idle until credentials exist |
| C. Deterministic lane at `N = 2` with "spread must be 0," using the variance policy | Harness non-determinism is a bug | Uses the wrong tool. That check does not need a variance-policy document, a baseline, a paired delta, or a trustworthy bool |

**Decision:** the mechanism has no consumer this round and ships
dormant. A determinism check is a separate later task, on the cheapest
smoke set only. The first variance-policy consumer is the first live
EvalSet.

1. All sixteen checked-in EvalSets declare `repetitionCount: 1` and
   none of them claims a variance signal. The fail-closed rule in #177
   already refuses a set that references a policy at `N = 1`. That is
   working as designed, not a missing configuration.
2. Calling the mutation tests "this round's consumer" would be a
   relabelling rather than an answer. A mutation test proves the
   computation is correct; it is not the real consumer the charter
   asks for when it forbids pre-building extension points that have
   none (§92). The honest statement is that the mechanism ships
   **dormant**: tested, merged as a library, and reached by no
   checked-in configuration until the first live EvalSet exists. The
   design has to say that sentence out loud. Shipping a tested library
   whose first configuration has not arrived is ordinary; claiming it
   already has a consumer is not.
3. If a determinism check is wanted, it is Bazel `runs_per_test`, not
   `och.eval.variance-policy`. Candidate: `repetitionCount: 2` on the
   cheapest smoke set (not the scheduled Context matrix), with the
   rule "the two Outcomes and Scores must match itemwise, else fail."
   That is a later task, and a different document.
4. The first configuration that should reference a variance policy is
   the first live quality EvalSet. Until then, do not invent a nightly
   `N = 5`.

**Why spread cannot be that determinism check's signal.**
`NumericScore` is assigned in exactly three places —
`internal/harness/eval/judge.go:233`, `judge.go:240`, and
`judge_attempt.go:159` — and all three are judge paths. The
deterministic scorer never produces a numeric score. On a fixture lane
`NumericScores` is therefore empty, `NumericSpread` is `0` by
construction, and a rule reading "spread must be exactly 0" passes
unconditionally while proving nothing. This retires path C in the table
above on a second, independent ground: not merely the wrong tool, but a
vacuous check. It also retires the amended form that was offered after
that table — "verdict stability must be exactly 1" — which fixes the
signal while keeping the variance policy as the vehicle. A determinism
check has to compare the two Outcomes and Scores itemwise, which is
what the candidate in point 3 already says.

## What to change in the open work

#178's research document should merge. It is the evidence for these
answers, not an authorisation to rebuild the implementation.

The four implementation PRs stay. The edits are amendments, not a
reshape:

| PR | Change |
| --- | --- |
| #173 | Accept the design with these four answers written in. Open question 3 stays out of the variance policy. Record that the trustworthiness gate's threshold half is unique, and that the structural half is not |
| #174 | No shape change. Calibration as a mandatory field, both limits mandatory, `MinEvaluableRepetitions ≥ 2` all stay |
| #175 | Replace `Trustworthy bool` with the two fields above. Keep the "one survivor" and "nothing evaluable" mutations; they are the reason the split exists |
| #177 | Fail-closed rules stay. Do not add a checked-in EvalSet that references a variance policy |
| Task 7 (unstashed) | Distribution block plus `c/n` and pass^k / at-least derived fields, plus the calibration disclosure. No reducer enum |

Do not: introduce a reducer catalogue, double the scheduled Context
matrix, treat spread-must-be-zero as a variance-policy mode, or gate
any Cell on an uncalibrated numeric limit.

## Consensus checklist

Each line is a yes/no. "Yes" on all six is enough to amend the design
and land the PRs. A no on any line is a different revision, and should
say which line and what instead.

1. Long-term shape is "distribution always published; a named decision
   rule may be added later on top of it; mean/median remain rejected."
2. This round does not add a named-reducer catalogue.
3. The report grows cheap derived reliability fields (`c/n`, and at
   `N ≥ 2` the Cell-level "at least one passed" / "all passed"
   readings). They are not a gate. Cell-level "at least one passed"
   is not named `pass_at(k)`.
4. `Trustworthy bool` splits into `evaluableEnough` (hard) and
   `exceedsDeclaredLimits` (advisory until calibrated). "Nobody else
   has this" is written next to the threshold half.
5. The deterministic lane does not take a variance policy. A future
   determinism check, if any, is a separate mechanism on smoke only.
6. #173–#175, #177, and Task 7 are amended in place; they are not
   reshaped around inspect_ai's `Epochs` object.

## Sources beyond the 2026-09-05 gate

The gate's pinned checkouts remain the authority for what inspect_ai,
terminal-bench, evals, vitest-evals, and Maka do in this repository's
`.reference/` tree. The additional sources below were read as public
documents on 2026-09-05 and are named so a later reader can see what
sat outside that tree.

| Source | What it contributes here |
| --- | --- |
| inspect_ai scoring metrics, [inspect.aisi.org.uk/metrics.html](https://inspect.aisi.org.uk/metrics.html) | `collect`; multiple reducers; `headline_metric`; clustered stderr; `NaN` on an empty aggregate |
| Chen et al. 2021, [arXiv:2107.03374](https://arxiv.org/abs/2107.03374) | Unbiased `pass@k` estimator; `pass@k` is a dataset-level functional-correctness metric |
| Yao et al. 2024, τ-bench, [arXiv:2406.12045](https://arxiv.org/abs/2406.12045) | pass^k as a reliability metric; the number that falls as `k` grows |
| *On Randomness in Agentic Evals*, 2026, [arXiv:2602.07150](https://arxiv.org/abs/2602.07150) — **unverified**, see below | Single-run `pass@1` moves several points even at temperature 0; report both envelopes |
| Miller 2024, *Adding Error Bars to Evals* | stderr / clustered stderr as the published uncertainty; not a within-cell gate |
| Bazel `--runs_per_test` / `--runs_per_test_detects_flakes` | Deterministic reruns produce FLAKY, which is a different status from a quality judgement |

**Verification status.** A pinned checkout can be re-read at its
commit; a web citation cannot be pinned the same way, so this table
carries a weaker warrant than the 2026-09-05 gate's. One row is weaker
still: *On Randomness in Agentic Evals* could not be confirmed to
exist at the identifier given. It is marked unverified and **nothing in
this document rests on it alone** — the claim it supports, that a
single run's number moves and both envelopes deserve reporting, is
carried independently by τ-bench's pass^k and by terminal-bench's
pass@k curve. Either confirm the reference or delete the row before
this document is cited as evidence elsewhere; do not let an
unverifiable citation ride into the design on the strength of the rows
around it.
