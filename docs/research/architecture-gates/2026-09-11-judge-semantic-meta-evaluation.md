# Judge Semantic Meta-evaluation Research

**Status:** Complete research evidence

**Date:** 2026-09-11

## Question

The repository already rejects malformed or unauditable judge output. What is
the smallest useful next slice for measuring whether a judge's *meaning* agrees
with a human-reviewed answer?

This is narrower than another general evaluation runner. It evaluates the
existing frozen quality judge itself.

## Re-verified precedents

| Project | Pinned source | Useful precedent | Boundary retained here |
| --- | --- | --- | --- |
| OpenAI Evals (legacy) | `openai/evals@8eac7a7`, `docs/build-eval.md`, `evals/elsuite/modelgraded/classify.py`, `evals/metrics.py` | Model-graded contributions are recommended to carry human `choice` labels; meta-eval compares the grader's choice with that label, reports metascore, and has confusion-matrix utilities. | Accuracy alone hides which error occurred. OCH must separately count unsafe passes, false fails, indeterminate results, and overclaims. |
| Inspect AI | Existing 2026-09-01 evaluation gate, pinned `84e512d` | Sample and scorer are separate, recoverable artifacts; offline scoring does not rerun the subject. | A meta-case is frozen input plus a label, not an executable Subject or a second Session runner. |
| Grok Build | `xai-org/grok-build@bb7f39d`, re-verified in the 2026-09-01 gate | Evidence is bounded and untrusted; strict decisions and skeptic findings remain evidence. | The production `och_quality_judge_v1` prompt, evidence framing, strict parser, and one-call policy must be reused rather than approximated. |
| vitest-evals | `getsentry/vitest-evals@aa34b64` | Full judge score metadata is retained in JSON reports; thresholds are optional. | The first OCH corpus publishes observations and counts but invents no GA threshold. |

The current official OpenAI documentation search was attempted through the
official-domain documentation route on 2026-09-11 but returned no retrievable
page content in this environment. No claim below depends on an unfetched search
snippet; the directly inspected, pinned repository sources above are the
evidence used.

## Decision

Build a frozen, digestible `och.eval.judge-meta-set` containing human-reviewed
expected verdicts and small synthetic evidence records. Bind it to one exact
JudgeConfig digest. Run the real prompt, evidence framing, strict response
validation, and provider caller repeatedly over those cases, then publish a
versioned report with every observation and a three-by-three confusion matrix.

The first corpus deliberately contains clear controls and adversarial pairs:
clean pass, explicit fail, unsupported self-claim, direct verdict injection,
quoted injection text that should not itself cause a fail, and contradictory
evidence. Git review and blame are label provenance; an inline rationale makes
each expected verdict inspectable.

## Rejected alternatives

- **More parser fixtures:** useful for protocol defenses, but already shown not
  to measure semantic accuracy.
- **One aggregate metascore only:** too lossy; one unsafe false pass and one
  harmless false fail cancel numerically.
- **A default pass threshold:** no calibration evidence exists yet.
- **Running Subject Sessions again:** unnecessary and confounds Judge quality
  with Subject variance. Meta-cases are already-labelled evidence.
- **Normal PR live calls:** credentials, cost, and provider variance do not
  belong in the keyless PR lane.

## Implementation consequence

The keyless suite validates documents, binding, aggregation, and the exact
production Judge path using fixture callers. The live CLI requires the existing
dual consent plus an exact call-budget acknowledgement. It prints one JSON
report and never hides retries: each repetition is one separately recorded
observation.
