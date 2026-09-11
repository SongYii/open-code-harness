# Judge Semantic Meta-evaluation Design

**Status:** Accepted normative design

**Date:** 2026-09-11

**Research:** [Judge semantic meta-evaluation research](../../research/architecture-gates/2026-09-11-judge-semantic-meta-evaluation.md)

## Scope

Add a labelled corpus and runner that measure the existing live quality Judge.
This slice does not execute a Subject, change the frozen judge prompt, introduce
a new provider adapter, or declare a GA quality threshold.

## Frozen meta-set

`och.eval.judge-meta-set`, format version 1, contains:

- stable `id` and `version`;
- the exact `judgeConfigDigest` it is valid for;
- `repetitionCount` in `[1, 20]`;
- ordered cases with unique IDs, a human-reviewed expected verdict and label
  rationale, unique tags, and inline evidence entries (`path`, `role`, `text`).

Case paths are normalized contained relative paths. Roles must be declared by
the bound JudgeConfig, every declared role must be present, paths and roles are
non-empty, and per-entry/whole-case byte limits are the same as the production
judge bundle. Expected verdict may be Pass, Fail, or Indeterminate. The label
rationale is trusted metadata and is never sent to the Judge.

The canonical digest covers every field, including evidence bytes, label, case
order, and repetition count.

## Production-path reuse

Each case is rendered with the same trusted `<criteria>` and untrusted
`<evidence>` framing used for an Attempt. It then enters the same strict output
decoder and semantic checks as `RunJudge`. There is no second parser and no
meta-eval-only weakening of evidence-reference rules.

The runner processes cases in document order and repetitions from zero upward.
One caller invocation produces one observation. Provider failure or malformed
output is an Indeterminate observation with its usage; it is not retried or
discarded. Context cancellation stops before the next call and returns an
explicitly incomplete prefix report, preserving every already-paid observation,
its usage, the planned/completed call counts, and a bounded stop reason.

## Report and metrics

`och.eval.judge-meta-report`, format version 1, binds:

- meta-set ID/version/digest;
- JudgeConfig ID/version/digest and model ID;
- all observations in deterministic case/repetition order, including expected
  and observed verdict, numeric score, every per-criterion result the Judge
  produced (none when a call/decoder failure precluded them),
  evidence/missing/contradictory references, rationale, and scorer usage/cost
  status;
- planned/completed call counts, completion state, and an incomplete run's stop
  reason;
- all nine expected-by-observed confusion cells, including zeros;
- raw totals: observations, exact matches, unsafe passes, false fails,
  unexpected indeterminates, and overclaims.

Definitions are deliberately asymmetric:

- `unsafePasses`: expected Fail or Indeterminate, observed Pass;
- `falseFails`: expected Pass, observed Fail;
- `unexpectedIndeterminates`: expected Pass or Fail, observed Indeterminate;
- `overclaims`: expected Indeterminate, observed Pass or Fail.

The report carries counts, not a pass/fail gate. A later calibrated policy may
decide how many of which error are acceptable; this slice does not guess.

## Live safety

`och-eval judge-meta` requires:

- `-set` and the exactly bound `-judge-config`;
- `-live` and `OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND`;
- `-max-calls` equal to `case count × repetitionCount`.

All document, digest, consent, and call-budget checks happen before the caller
can read a credential. The command emits exactly one report on stdout. It is an
explicit live operation and is absent from ordinary PR CI.

## First corpus and acceptance

The checked-in seed corpus has six reviewed cases: clear pass, explicit fail,
unsupported success claim, direct verdict injection, harmless quoted injection,
and unresolved contradiction. A fixture caller drives all cases through the
real render/decode/validate path without a key.

Acceptance requires strict document tests, digest sensitivity, binding and
call-budget refusal before calls, exact confusion counts, observation ordering,
one-call-per-repetition proof, CLI dual-consent tests, checked-in digest guards,
and mutation tests for the unsafe-pass and unexpected-indeterminate counters.
