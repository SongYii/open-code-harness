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

- **Aggregating repetitions into one verdict.** Explicitly rejected below.
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
| Repetition aggregation | Report the distribution; flag it untrustworthy when spread exceeds a declared threshold. Never collapse repetitions into a verdict. | Majority/median (erases the difference between 5/5 and 4/5); all-pass (one stochastic dip turns a lane red and teaches people to re-run) |
| Baseline source | Both: the within-run paired arm and a pinned historical baseline document | Either alone |
| Indeterminate repetitions | Counted separately, excluded from the quality denominator, with raw and filtered views both always shown | Counting as failure (conflates "could not judge" with "judged bad"); voiding the Cell (may never assemble a complete set) |

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
- `trustworthy` — a boolean, with `untrustworthyReason` when false.

There is deliberately **no** `cellVerdict` field. A consumer that wants one
must decide its own rule and say so; the artifacts will not pre-decide it.
This is the same discipline the parent design applies to infra failures: show
the reader the real distribution and refuse to hide it behind one number.

## 3. Spread, stability, and the untrustworthy flag

Two spread measures, chosen because they stay honest at N in the single
digits:

- **Numeric spread** = `max(NumericScore) − min(NumericScore)` over
  evaluable repetitions. Range, not standard deviation: at N = 3 a standard
  deviation is a number with no useful sampling behavior, and range is the
  thing a reviewer can check by eye against the published sequence.
- **Verdict stability** = `modalVerdictCount / evaluableCount`, where
  `evaluable` excludes `indeterminate` per §4. `1.0` means unanimous.

A Cell is **untrustworthy** when either declared limit is exceeded:
`numericSpread > maxNumericSpread`, or `verdictStability < minVerdictStability`.
An untrustworthy Cell publishes its full distribution and is **not** reported
as a pass or a fail. It is not a third verdict about the Subject; it is a
statement about the measurement.

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
  `minEvaluableRepetitions` is untrustworthy for that reason, reported
  distinctly from a spread breach. "Mostly unjudgeable" and "judged
  inconsistently" are different problems and must not share a label.

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
- An untrustworthy Cell may never be reported as a pass, in any lane, at any
  maturity level.

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
3. **Does the deterministic lane get a variance policy at all?** Fixture runs
   should be exactly reproducible, so any spread there is a defect rather
   than noise. Treating `spread > 0` on a fixture lane as a hard failure
   would be a genuinely useful determinism check, but it is a different
   mechanism from the one specified here, and folding it in would blur the
   distinction this document rests on.
