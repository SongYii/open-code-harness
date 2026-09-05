# Repetition and Variance in Evaluation Frameworks

**Status:** Research evidence. Records comparison inputs for the evaluation
variance and baseline policy. It informs that design; nothing here becomes a
requirement without being adopted into it.

**Date:** 2026-09-05

**Why this exists, stated plainly:** it should have been written before the
[variance policy design](../../superpowers/specs/2026-09-04-evaluation-variance-policy-design.md),
not after seven implementation tasks. Charter §12.1 — added by this same
sequence of work two days earlier — requires researching existing practice
before building a basic component. That rule was not applied to the variance
mechanism itself. Four dedicated evaluation frameworks were sitting unread in
`.reference/` the whole time. This document is the research, and the design
decisions it questions are listed at the end.

## Pinned sources

| Source | Revision | Date |
| --- | --- | --- |
| `inspect_ai` (UK AISI) | `84e512d` | 2026-09-01 |
| `terminal-bench` | `d28711d` | 2026-07-10 |
| `evals` (OpenAI) | `8eac7a7` | 2026-04-14 |
| `vitest-evals` | `aa34b64` | 2026-08-07 |
| Maka | `afbcabdc7` | 2026-09-01 |

## The short version

| Project | Repetition concept | Default | How repeats are combined | Variance surfaced as |
| --- | --- | --- | --- | --- |
| **inspect_ai** | `Epochs(epochs, reducer)` | **1** | **Nine named reducers**, caller picks; default `mean` | `stderr`, `std`, **clustered** stderr — across *samples*, not epochs |
| **terminal-bench** | `n_attempts` | **1** | `pass@k` curves at powers of two, plus 5 and 10 | The pass@k curve itself |
| **evals** | `n_samples_per_task`, per-eval not framework-level | varies | eval-specific | — |
| **vitest-evals** | none found | — | — | — |
| **Maka** | `repetitions` → `ExperimentCell.repetition` | — | **Nothing.** No aggregation anywhere in its eval package | — |

**No framework has a "this measurement is untrustworthy" gate.** A search for
`untrustworthy`, `unreliable`, `too_variable`, `insufficient_samples` across
all five returns three hits, all unrelated: one is a tracing helper for
long-running actions, two are Maka's ledger-chain and MIME-type comments.

## inspect_ai — repetition and reduction are one decision

`_eval/task/epochs.py:4-22`:

```python
class Epochs:
    """Number of epochs to repeat samples over and optionally one or more
    reducers used to combine scores from samples across epochs. If not
    specified the "mean" score reducer is used."""
    def __init__(self, epochs: int, reducer: ScoreReducers | None = None)
```

The count and the combination rule are **the same object**. A caller cannot
ask for repetitions without the framework having an answer for what to do with
them, and the reducer may be a *list*, so mean and median and "keep
everything" can be reported side by side.

The nine reducers (`scorer/_reducer/reducer.py`): `mode`, `mean`, `median`,
`at_least(k, value)`, `pass_at(k)`, `pass_k`, `max`, and — significant for
this project — **`collect`**, whose docstring is "Collect each score's value
into a list, **preserving every value**."

That last one matters more than any other line in this document. This
project's variance design treats "never collapse repetitions into a verdict"
as its central refusal. In inspect_ai the same behavior exists as **one named
option among nine**, chosen by the caller. The underlying value is shared —
the combination rule is explicit rather than hidden — but inspect_ai
expresses it as a choice while this project expresses it as a prohibition.

**Variance is reported at a different altitude.** `stderr`
(`scorer/_metrics/std.py:118-165`) computes the standard error of the mean
over `list[SampleScore]` — across the *dataset*, not across epochs of one
sample. There is also **clustered** standard error, which exists precisely
because repeated measurements of one sample are correlated and naive stderr
would understate uncertainty.

One behavior is worth naming as a contrast rather than a model. With fewer
than two values, `stderr` returns **0** (`std.py:151-152`, and three more
sites). A caller reading that sees perfect precision where there was no
measurement. This project refuses that case instead — and the refusal is one
of the few places where this design is stricter than the most mature
framework in the comparison set, deliberately.

## terminal-bench — the pass@k curve is the variance report

`harness/models.py:29` declares `n_attempts: int = 1`. When attempts exceed
one, `pass_at_k` (`models.py:90-112`) computes the fraction of tasks resolved
by at least one of k attempts, for k at powers of two up to the minimum
attempt count, plus 5 and 10 where available.

This is a genuinely different answer to the same question. Rather than asking
"is this measurement stable enough to read", it publishes **how the result
changes with more attempts** and lets the reader see the shape. A task solved
at pass@8 but not pass@1 is not flagged as untrustworthy; it is reported as
exactly what it is.

## Maka — repetitions with no aggregation at all

Maka's vocabulary is the closest to this project's: `ExperimentSpec.repetitions`
(`packages/eval/src/experiment.ts:67`) expands into `ExperimentCell` records
each carrying a `repetition` index (`experiment.ts:79`), and the expansion is
a flat product over tasks × repetitions × subjects (`expandExperiment`).

And then **nothing aggregates them**. The only use of `repetition` outside the
model is `groupTaskCells` (`runner.ts:243`), which keys concurrency groups on
`task.id + repetition`. Searching Maka's whole eval package for `mean`,
`median`, `aggregate`, `passAt`, `variance`, `stddev`, or `spread` returns no
implementation.

Maka's position is therefore: run the repetitions, persist every attempt, and
leave every aggregation question to whoever reads the artifacts. That is a
defensible design for a system whose artifacts are the product — and it is
close to what this project already does *without* the variance mechanism.

## What this changes about the accepted design

Four questions the design settled without this evidence. None of them
invalidates the implemented mechanism; each is a place where the reasoning was
thinner than it appeared.

**1. "Never collapse" versus "name the reduction."** The design's decision
table rejects majority/median because it "erases the difference between 5/5
and 4/5". True — but inspect_ai's answer is to let the caller name the
reduction *and* offer `collect` for exactly the case this project made
mandatory. A design offering `distribution` (today's behavior) alongside named
reducers would keep the honesty and lose the rigidity. **This is a real
alternative that was never considered, because the research was not done.**

**2. Range versus standard deviation.** The design chose range on the grounds
that a standard deviation "has no useful sampling behavior" at single-digit
N. inspect_ai does use standard deviation and standard error — but across
*samples*, where N is the dataset size, and it reaches for **clustered**
standard errors precisely when measurements are correlated the way epochs of
one sample are. The choice of range for within-Cell spread survives this
comparison; what does not survive is the implication that these frameworks
avoid standard deviation. They use it at a different altitude.

**3. The trustworthiness gate is unique to this project.** No framework in the
comparison set has one. That is not automatically wrong — this project's
charter is more conservative than a benchmark harness's, and refusing to
report `stderr = 0` from one sample is defensible where inspect_ai's silent 0
is not. But "nobody else does this" deserves to be a stated fact next to the
decision rather than an unexamined novelty.

**4. Nothing in this repository can exercise any of it.** All sixteen
checked-in EvalSets declare `repetitionCount: 1`. This was recorded in the
design's own baseline section and then built past for seven tasks. inspect_ai
and terminal-bench both default to 1 as well, so the default is not the
anomaly — the anomaly is shipping a mechanism with no configuration that
reaches it, and no task in the plan to add one.

## Questions for the design revision

1. Should the mechanism offer **named reductions** (inspect_ai's shape) with
   `distribution` as one of them, rather than prohibiting reduction outright?
2. Should `pass@k` be reported when repetitions allow it (terminal-bench's
   shape), given it answers "how does this change with more attempts" without
   needing a calibrated threshold at all?
3. Is the trustworthiness gate worth keeping now that it is known to be
   unique — and if so, should it be advisory metadata rather than a
   `Trustworthy bool`?
4. What checked-in configuration will exercise this, and does adding one
   belong to this work or to the first live run?
