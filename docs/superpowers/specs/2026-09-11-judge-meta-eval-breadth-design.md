# Judge Meta-evaluation Breadth Design

**Status:** Accepted normative design

**Date:** 2026-09-11

## Problem

The existing adversarial fixtures prove strict JSON decoding, criterion-set
integrity, aggregate consistency, citation existence, contradiction handling,
and the requirement to cite something. They still accept two structurally valid
but unauditable determinate answers:

- two criteria can declare different evidence roles while the judge cites only
  one role; and
- `pass` or `fail` can carry an empty rationale.

The first defect is present in the old known-fail fixture itself: it judges audit
continuity while citing only the transcript. The second satisfies the JSON shape
but not the prompt's requirement for a bounded, specific explanation.

## Contract

For a determinate (`pass` or `fail`) result, `RunJudge` must additionally prove:

1. `evidenceReferences` covers every evidence role declared by the frozen
   criteria, with at least one reference to a shown manifest entry in each role;
2. `rationale` contains non-whitespace text after decoding.

Failure of either check produces a real `Indeterminate` outcome with a bounded,
redacted diagnostic and preserves the caller's usage. It is not a Go error and
does not cause a retry.

An `indeterminate` response remains exempt. It may legitimately have no usable
citation or explanation. Missing and contradictory evidence retain priority and
their existing structured outcome fields.

## Compatibility and boundary

The frozen `och_quality_judge_v1` prompt and JudgeConfig schema do not change.
Its existing global `evidenceReferences` field already claims every manifest
path the judge relied on, and every declared role is already mandatory when the
bundle is built. The implementation records the role membership of each path
actually shown and validates the response against that map.

This proves role coverage, not sentence-level entailment or a per-criterion
path assignment. Adding per-criterion citations would change the frozen output
protocol and requires a future prompt/schema version. Semantic correctness
against real model outputs remains a calibration concern rather than something
a parser fixture can prove.

## Acceptance

- The previous known-fail fixture cites both transcript and audit and remains a
  determinate fail.
- A correct-shaped fail that omits the audit citation becomes Indeterminate for
  the role-coverage reason.
- A correct-shaped pass with whitespace-only rationale becomes Indeterminate
  for the rationale reason.
- Existing missing/contradictory/indeterminate behavior and one-call policy stay
  unchanged.
- Targeted mutation checks remove each new guard separately; its named fixture
  must then fail for the intended reason.
