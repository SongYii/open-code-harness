# Evaluation Variance and Baseline Policy Design

**Status:** Accepted on 2026-09-05. Proposed on 2026-09-04 and deliberately
left without an authority-table row until acceptance, since adding one would
have recorded an acceptance that had not happened.

Open question 1 is answered: the mechanism is accepted now, with an
explicitly **provisional, uncalibrated** policy document, rather than waiting
for live credentials this environment does not have. That ordering carries
one real risk, stated so it can be watched for: a provisional number is
easily mistaken for a calibrated one. The implementation therefore makes
"uncalibrated" a field of the frozen document itself rather than a comment,
so every Score derived under it carries the disclosure.

Open questions 2 and 3 remain open and do not block the plan. Question 2 (a
nightly repetition budget) does not bind while no checked-in live EvalSet
exceeds `repetitionCount: 1`. Question 3 (whether the deterministic lane gets
a variance policy) is left out, following this document's own reasoning that
folding it in would blur the distinction the design rests on; a fixture lane's
`spread > 0` is a determinism defect rather than noise, and deserves its own
mechanism rather than this one.

**Date:** 2026-09-04

**Authority:** This document specializes the accepted milestone 10 design in
[`2026-09-02-evaluation-design.md`](2026-09-02-evaluation-design.md) — §22
(reports) and §28 (GA blockers) — for the variance and baseline policy those
sections name as outstanding without defining. The parent design wins if these
documents conflict. An implementation must update this document, rather than
silently deviate, if repository facts invalidate a contract below.

**Current baseline:** verified against `main` at `3bbce0c` plus the branch
`fix/context-scheduled-lane-isolation`, by reading the code rather than the
prose that describes it:

- Repetitions are real, not declared-only. `EvalSet.RepetitionCount`
  (`internal/harness/eval/evalset.go:199`) is validated as positive
  (`:288`), `ExpandCellAttempts` loops over it (`matrix.go:136,144`), and
  `RepetitionIndex` is carried through the runner into each Attempt
  (`runner.go:58,142,257`). `matrix_test.go:104` exercises `N = 2` and
  asserts every repetition index appears.
- **No checked-in EvalSet ever uses it.** All 16 files in `eval/sets/`
  declare `"repetitionCount": 1`. The substrate for a variance signal
  exists and has never been exercised end to end.
- `Score` (`score.go:92`) carries `Verdict`, an optional `NumericScore`,
  and per-`Criteria` results, and is append-only per scoring or regrade
  invocation.
- Parity machinery (`parity.go`) compares *semantic facts* between paired
  arms — tool, usage, envelope, workspace — and deliberately not scores.
  It is not a score-comparison mechanism and this design does not make it
  one.
- No document in the repository defines what acceptable variance is. The
  phrase "variance policy" appears as an outstanding GA blocker in six
  documents (`README.md:137`, `docs/README.md:190`,
  `docs/architecture/evaluation.md:579`,
  `docs/architecture/evaluation-evidence.md:406`, and both language copies
  of the milestone 10 design) and is defined in none of them.

## Problem

The evaluation subsystem can produce a live quality Score today, and cannot
say whether that Score means anything. A single judged Attempt is one sample
of a stochastic process. Without a stated policy, three failure modes are all
available and indistinguishable in the artifacts:

1. A real regression is dismissed as noise.
2. Noise is reported as a regression, and the response is to re-run until
   the answer is convenient.
3. A number is quoted as a quality signal when nobody ever established that
   repeating the same run would produce it again.

The parent design already refuses to let this be implicit: live and nightly
quality floors are **advisory** until "sample size, judge meta-eval, provider
breadth, and variance policy have separate accepted evidence"
(§22). This document supplies the fourth of those four.

## Goals

- Define what a variance signal is computed over, in terms of documents that
  already exist.
- Make the spread of repeated Scores a **published, auditable fact** rather
  than a threshold hidden in a report generator.
- Define when a quality signal is not trustworthy enough to be read as a
  result at all, and say so in the artifacts instead of returning a verdict
  anyway.
- Define both baseline comparisons the reviewer asked for: within-run paired
  arms, and a pinned historical baseline.
- Keep every rule fail-closed and offline-verifiable from the artifacts.

## Non-goals

- **Replacing the distribution with an aggregate.** Explicitly rejected
  below. A named decision rule published *beside* the distribution is a later
  possibility (§3.5); a reduction published *instead of* it is not.
- **A named reducer catalogue in this slice.** No consumer asks for one
  (§3.5).
- **A determinism check for the deterministic lane.** Different mechanism,
  different document (§3.6).
- Gating ordinary pull requests on any model-judge result. The parent
  design already forbids it (§22) and nothing here relaxes that.
- Choosing calibrated numeric thresholds. No run against a live model has
  ever happened in this repository, so any number written here would be
  invented. §3 defines where the threshold lives and how it is frozen; it
  deliberately supplies no default.
- Statistical inference — confidence intervals, significance tests, power
  analysis. At the sample sizes a nightly lane can afford (single digits),
  those would dress up a small sample as rigor.
- Retrying, re-running, or auto-healing an unstable Cell.
- Provider breadth, judge meta-evaluation breadth, and real-model sample
  size. Those are three separate GA blockers and this document closes
  none of them.

## Decision summary

Three policy choices settled in review on 2026-09-04, recorded here so later
readers see them as decisions rather than as inferred defaults:

| Choice | Decision | Rejected alternatives |
| --- | --- | --- |
| Repetition aggregation | Always publish the distribution. A named **decision rule** may later be applied on top of it — never a reduction that replaces the raw counts — and none is built in this slice. | Mean/median as the published answer (erases the difference between 5/5 and 4/5); all-pass (one stochastic dip turns a lane red and teaches people to re-run); an inspect_ai-style reducer catalogue now (§3.5) |
| Reliability reporting | Two fields, not one boolean: `evaluableEnough` (structural, hard) and `exceedsDeclaredLimits` (threshold, advisory until calibrated) | A single `trustworthy` bool — a consumer branches on the bool and never reads the reason text, which is exactly how "one evaluable repetition of five" and "spread 0.33 over a guessed limit of 0.20" come to share a label |
| Deterministic lane | Takes no variance policy (§3.6) | Folding a determinism check into this mechanism |
| Baseline source | Both: the within-run paired arm and a pinned historical baseline document | Either alone |
| Indeterminate repetitions | Counted separately, excluded from the quality denominator, with raw and filtered views both always shown | Counting as failure (conflates "could not judge" with "judged bad"); voiding the Cell (may never assemble a complete set) |

The first three rows were settled on 2026-09-05, after the research this
design should have had in front of it. The evidence is the
[repetition and variance gate](../../research/architecture-gates/2026-09-05-eval-repetition-and-variance.md)
and the [answers to its four questions](../../research/architecture-gates/2026-09-05-eval-variance-question-answers.md).
Both are research evidence; this document is where they become binding.

## 1. The unit of measurement

A variance signal is computed over one **Cell** — the Scenario × Subject ×
Executor triple the matrix already expands (`matrix.go`) — across that Cell's
`RepetitionCount` Attempts within a single EvalSet run.

Repetitions of one Cell are comparable because every identity input is
already frozen and digested: Scenario digest, Subject digest, Executor
digest, fixture digest, limits, and pairing seed. Two Attempts that differ in
any of those are not repetitions of the same thing and must never be pooled.
The implementation derives the grouping key from those digests, not from
document ids, so a renamed-but-identical document does not split a group and
an edited-but-same-named one does not silently join one.

Repetitions are pooled **within one run only**. Cross-run pooling is what the
pinned baseline in §5.2 is for, and it is a different comparison with
different failure semantics.

## 2. Distribution, not aggregation

For each Cell the report publishes:

- `repetitions` — the declared `RepetitionCount`;
- `attempts` — Attempts that actually reached a terminal, fully-collected
  state;
- `verdicts` — the count of each `ScoreVerdict` (`pass`, `fail`,
  `indeterminate`), never a single derived verdict;
- `numericScores` — every `NumericScore` in repetition-index order, so the
  reader sees the actual sequence rather than a summary of it;
- `spread` — §3;
- `stability` — §3;
- `evaluableEnough` and `exceedsDeclaredLimits` — §3.1 and §3.2, two fields
  that are never merged into one;
- the derived reliability readings of §3.4.

There is deliberately **no** `cellVerdict` field, and no reduction of the
repetitions into a single score. A consumer that wants a yes/no must decide
its own rule and say so; the artifacts will not pre-decide it. A future
**decision rule** (§3.5) may name such a rule and publish its answer *beside*
the distribution — it may never publish it *instead of* the distribution.
This is the same discipline the parent design applies to infra failures: show
the reader the real distribution and refuse to hide it behind one number.

## 3. Spread, stability, and the two reliability fields

Two spread measures, chosen because they stay honest at N in the single
digits:

- **Numeric spread** = `max(NumericScore) − min(NumericScore)` over
  evaluable repetitions. Range, not standard deviation: at N = 3 a standard
  deviation is a number with no useful sampling behavior, and range is the
  thing a reviewer can check by eye against the published sequence.
- **Verdict stability** = `modalVerdictCount / evaluableCount`, where
  `evaluable` excludes `indeterminate` per §4. `1.0` means unanimous.

Two different statements can be made about a Cell's reliability, and they
have different warrants. They therefore get **two fields, never one boolean**.
A consumer branches on a boolean and does not read the reason string beside
it, so a single `trustworthy` flag with a reason attached collapses back into
one undifferentiated claim the moment anything consumes it.

### 3.1 `evaluableEnough` — a structural fact

`evaluableCount < minEvaluableRepetitions`, or no evaluable repetition at
all. This is arithmetic on counts. It needs no calibration to be certain, and
it is certain: one survivor of five repetitions is not a measurement,
whatever the survivor said.

A Cell that is not `evaluableEnough` publishes its full distribution and
**must not be reported as a pass**. This is a hard reporting rule.

It is also not a novelty. The industrial analogues are ordinary: insufficient
`n`, an A/B test reported inconclusive, inspect_ai returning `NaN` when every
sample is filtered out. What *is* deliberate is being stricter than
inspect_ai, which returns `stderr = 0` from a single sample — perfect
precision reported where nothing was measured. That refusal stays.

### 3.2 `exceedsDeclaredLimits` — a threshold judgement

`numericSpread > maxNumericSpread`, or
`verdictStability < minVerdictStability`.

This field is only ever as good as the limits it compares against, and no
limit in this repository has been calibrated — §3's own rule is that no
default may be supplied, because no live run has ever happened here. The
field therefore carries the policy's calibration state with it, and while
that state is `uncalibrated`:

- it is **advisory metadata**;
- it **must not** rewrite a Cell from a pass into "not a pass".

Only calibrated limits, citing the run that produced them, may harden this
into a reporting rule the way §3.1 already is.

No framework in the comparison set — inspect_ai, terminal-bench, evals,
vitest-evals, Maka — has a gate of this kind. That fact belongs next to this
field and not next to §3.1: the structural half is common practice, the
threshold half is this project's own invention, and an invention that has
never been calibrated does not get to change a verdict.

### 3.3 Why the split is load-bearing

Before the split, a Cell with one evaluable repetition out of five reported
`trustworthy = true` with perfect stability, because a single surviving
repetition is trivially unanimous. Two statements — "we could barely measure
this" and "what we measured agreed with itself" — shared one label, and the
label showed the reassuring one.

Both limits live in a new frozen `och.eval.variance-policy` document,
following the `och.eval.judge-config` precedent exactly (`judge_config.go`):
a digestible, secret-free document, referenced by an EvalSet, and bound into
the run's evidence so a report's trustworthiness rule is provable offline
from the artifacts alone rather than from whatever the report generator
happened to be compiled with.

**No default limits are supplied.** A policy document must declare both. The
parent design's own honesty rule applies: this repository has never run a
judge against a live model, so a shipped default would be a guess wearing the
authority of a specification. The first accepted values must cite the run
that produced them.

**Fail-closed rules:**

- An EvalSet that references a variance policy but declares
  `repetitionCount: 1` is refused at load time, before any work starts.
  One sample cannot exhibit spread, and silently reporting
  `spread = 0` would be the most dangerous possible output — perfect
  stability, measured never.
- A variance policy whose digest does not match the one bound into the
  Attempt's evidence is refused; the report does not fall back to
  "no policy".
- A Cell whose Attempts disagree on any identity digest is a hard error,
  not a merge.

### 3.4 Derived reliability readings

These are arithmetic on counts the distribution already publishes. They are
**not** a gate, **not** a reduction, and they need no calibrated threshold —
which is the reason they are worth having in a policy that admits it is
uncalibrated.

Per Cell, at `evaluableCount ≥ 2`:

- `c/n` — evaluable passes over evaluable repetitions, the raw counts;
- **at least one passed** — the optimistic envelope;
- **all passed** — the pessimistic reading, and the one this project actually
  cares about. A harness asking whether it can be trusted unattended wants
  the number that *falls* as repetitions grow, not the one that rises. This
  is τ-bench's pass^k rather than a leaderboard's pass@k.

Naming, because getting it wrong would make the first public comparison
dishonest: a Cell-level "at least one of k attempts passed" is `at_least(1)`.
It is **not** `pass@k`. Chen et al.'s `pass@k` is an unbiased dataset-level
estimator, and inspect_ai's `pass_at(k)` is that estimator. At the sample
sizes a nightly lane can afford here — single digits, often two — the
estimator and the raw `c/n` are nearly the same fact, so the estimator is not
implemented and the counts are published instead. A suite-level `pass@k`
curve is terminal-bench's shape and waits for a suite large enough for the
curve to mean anything.

### 3.5 Named decision rules are deferred, not rejected

The long-term shape is: the distribution is always published, and a consumer
that needs a yes/no gets a **named decision rule** applied on top of it. That
is a better default than inspect_ai's, whose `mean` reducer erases 4/5 unless
the caller also asks for `collect` — this design inverts which one you have
to ask for.

The vocabulary matters. inspect_ai's *reducer* **replaces** the per-epoch
view that metrics then see. A decision rule here is applied **on top of** the
published distribution and never deletes the raw counts. Calling it a reducer
would, over time, license exactly the replacement this document refuses.

No such rule is built in this slice. No lane currently asks "did this Cell
pass?", and the charter forbids pre-implementing an extension point with no
real consumer (architecture design §92). `mean` and `median` stay rejected on
their own merits. `at_least(k)` waits for the first consumer that needs a
yes/no.

### 3.6 The deterministic lane takes no variance policy

This resolves the third open question below, and the answer is no.

A fixture lane's run-to-run difference is a determinism defect, not noise, so
the useful check there is Bazel's `--runs_per_test` shape — run it twice, the
outcomes must match, report FLAKY — which needs none of `maxNumericSpread`,
`minVerdictStability`, a policy document, a baseline, or a paired delta.
Routing it through this mechanism would blur the distinction the document
rests on, and would double the cost of a scheduled matrix that was moved off
the pull-request path (#157) precisely because it was already too expensive
there.

Spread could not serve as that check's signal in any case. `NumericScore` is
assigned in exactly three places — `internal/harness/eval/judge.go:233`,
`judge.go:240`, and `judge_attempt.go:159` — and all three are judge paths.
The deterministic scorer never produces a numeric score, so on a fixture lane
`numericScores` is empty, `spread` is `0` by construction, and a rule reading
"spread must be exactly 0" would pass unconditionally while proving nothing.
A determinism check has to compare the two Outcomes and Scores itemwise.

If such a check is wanted, it is a separate mechanism in a separate document,
on the cheapest smoke set only.

### 3.7 This mechanism ships dormant

All sixteen checked-in EvalSets declare `repetitionCount: 1`, and the
fail-closed rule above refuses any set that references a policy at `N = 1`.
Nothing in this repository reaches this code, and this slice does not invent
a configuration so that something would.

That is stated here rather than papered over. The mutation tests prove the
computation is correct; they are not a consumer in the sense §92 means, and
calling them one would be a relabelling. The mechanism is merged as a tested
library whose first configuration has not arrived. The first configuration
that should reference a variance policy is the first live quality EvalSet.

## 4. Indeterminate repetitions

`indeterminate` is an infrastructure or judgeability signal, not a quality
signal. A judge that could not be reached, refused malformed output, or was
denied evidence has said nothing about the Subject.

- Indeterminate repetitions are counted and named individually, with the
  reason carried from the Score.
- They are **excluded** from `evaluableCount`, and therefore from verdict
  stability and numeric spread.
- Every report presents both views, always, never one:
  **raw** (denominator = all terminal Attempts) and **filtered**
  (denominator = evaluable Attempts). This extends the parent design's
  existing §22 rule — "never discard infra failures from denominators
  without showing both raw and filtered views" — to quality signals, rather
  than inventing a new convention beside it.
- A Cell in which `evaluableCount` falls below a declared
  `minEvaluableRepetitions` fails `evaluableEnough` (§3.1). "Mostly
  unjudgeable" and "judged inconsistently" are different problems, they carry
  different warrants, and §3 gives them separate fields rather than separate
  reason strings on one shared label.

## 5. Baselines

Both comparisons are required. They answer different questions and fail
differently.

### 5.1 Within-run paired arm

The existing pairing rule stands unchanged: baseline and candidate arms pair
only when Scenario digest, Executor kind, repetition index, fixture digest,
limits, and pairing seed match, and the Subject digest differs in at least
one declared semantic field (parent design §22).

For variance, the paired delta is computed **between distributions, not
between single Scores**: the report publishes each arm's distribution per §2
and the delta of their evaluable medians, alongside both spreads. A delta
smaller than the wider arm's own spread is published as
`withinNoise: true` — the honest statement that this run cannot distinguish
the arms, which is different from "no difference exists".

This comparison is immune to environment drift because both arms ran in the
same process, on the same machine, against the same fixtures, minutes apart.
That is its whole value, and its limit: it cannot see a regression that moved
both arms together.

### 5.2 Pinned historical baseline

A new `och.eval.baseline` document records, per Cell, a previous run's
published distribution together with the identity digests that make the Cell
what it is, the run that produced it, and the environment facts that bound
its interpretation.

- A baseline is compared **only** to a Cell whose identity digests match
  exactly. A mismatch is reported as `baseline-unmatched` and is never
  silently treated as either a pass or an absent baseline — an unmatched
  baseline is a fact the reviewer needs, since the usual cause is that the
  Scenario or Subject was edited.
- A baseline is regenerable from checked-in artifacts by an explicit
  command, and records the Attempt ids it was derived from. A baseline
  nobody can reproduce is an assertion, not evidence.
- Baselines are append-only and versioned like every other document here.
  Updating one is a reviewed commit, never an automatic write-back from a
  run — a lane that rewrites its own baseline when it drifts measures
  nothing.
- A baseline older than a declared staleness bound is reported as stale and
  still shown. Staleness is disclosed, not silently ignored.

## 6. What may gate

Unchanged from the parent design, restated because this is where readers will
look for it:

- Ordinary PR CI: deterministic verifier results and deterministic floors
  only. No quality signal, no variance signal, ever.
- Live/nightly: quality and variance signals remain **advisory** until all
  four GA blockers — real-model sample size, judge meta-evaluation breadth,
  provider breadth, and this policy — have separate accepted evidence. This
  document closes exactly one of the four. Implementing it does not make the
  other three closed, and no report may present a variance-checked signal as
  a GA-grade quality claim while any of them stands open.
- A Cell that fails `evaluableEnough` (§3.1) may never be reported as a pass,
  in any lane, at any maturity level. `exceedsDeclaredLimits` (§3.2) carries
  no such power while the limits it compares against are uncalibrated.

## 7. Testing and acceptance

Deterministic and offline. No live credential is required to accept this
policy, which is the point: the mechanism must be provable before the numbers
it carries can be earned.

- Distribution computation over hand-built Score sets, including the
  asymmetric cases: 4-pass/1-fail, 3-pass/2-indeterminate,
  all-indeterminate, and a single evaluable repetition among many
  indeterminate ones.
- Spread and stability arithmetic, including the boundary where a value
  exactly equals a declared limit (the limit is a maximum, so equality does
  not breach).
- Fail-closed: `repetitionCount: 1` with a referenced policy is refused
  before work starts; a mismatched policy digest is refused; identity-digest
  disagreement within a Cell is a hard error.
- Both views: every report path emits raw and filtered denominators, proven
  by a test that fails if either is absent.
- Baseline matching, mismatch, staleness, and regeneration determinism —
  the same document must be produced twice from the same artifacts.
- Paired-arm `withinNoise` including the case where the delta exceeds one
  arm's spread but not the other's.
- Mutation checks recorded in the evidence ledger, per this repository's
  established discipline: weakening each fail-closed rule must turn a test
  red.

## 8. File map

Indicative, not binding on the implementation plan:

- `internal/harness/eval/variance.go` — pure distribution, spread, and
  stability computation over Scores. No I/O.
- `internal/harness/eval/variance_policy.go` — the frozen
  `och.eval.variance-policy` document, decode/validate/digest, mirroring
  `judge_config.go`.
- `internal/harness/eval/baseline.go` — the `och.eval.baseline` document,
  matching rules, and staleness.
- `cmd/och-eval/report.go` — publishes the per-Cell distribution block;
  `cmd/och-eval` gains a baseline regeneration command.
- `eval/policies/` — checked-in policy documents, once values are earned.

The pure computation stays in `internal/harness/eval` with no store
instrumentation and no new dependency, consistent with the architecture
guards that already constrain this package.

## 9. Documentation updates on acceptance

- An authority-table row in `docs/README.md` (`Accepted | Normative design`),
  added when this document is accepted and not before.
- `docs/architecture/evaluation.md` and its Chinese reading copy, once
  implemented.
- `docs/architecture/evaluation-evidence.md` with commits, commands, actual
  output, and mutation results.
- The GA-blocker lists in `README.md:137`, `docs/README.md:190`,
  `docs/architecture/evaluation.md:579`, and
  `docs/architecture/evaluation-evidence.md:406` — each names "variance
  policy" as outstanding and each must change together, or the repository
  will again hold a claim in one place that another place contradicts.

## Open questions for the reviewer

1. **Where do the first numbers come from?** §3 refuses to invent defaults,
   so the first accepted policy document depends on a real live run that has
   never happened here. Is the intended order (a) obtain credentials, run,
   then accept calibrated values, or (b) accept the mechanism now with an
   explicitly provisional policy document marked as uncalibrated?
2. **Nightly repetition budget.** Every checked-in set uses
   `repetitionCount: 1`. A live Cell at N = 5 multiplies both cost and wall
   time by five. Is there a cost ceiling this policy must respect?
3. ~~**Does the deterministic lane get a variance policy at all?**~~
   **Resolved 2026-09-05: no.** See §3.6, which also records that `spread`
   could not have been that check's signal even if the answer had been yes.
