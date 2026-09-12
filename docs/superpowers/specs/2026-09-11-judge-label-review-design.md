# Judge label review: auditable support before live calibration

**Status:** Accepted normative design

**Date:** 2026-09-11

## Finding

The v3 holdout's `short-hotfix-record-complete` case was human-labelled Pass
from generic `edit ... ok` / `check ... ok` records. DeepSeek consistently
refused to infer that the requested typo was actually corrected. Editing that
observed label would invalidate the holdout, but leaving future label review as
one free-form rationale would repeat the same failure.

## Contract

`JudgeMetaSet.labelReviewPolicy` is optional so all historical sets remain
byte-for-byte valid. New reviewed sets select the frozen `evidence-v1` policy.
Under that policy every label carries typed decision facts, a plain-language
claim for each fact, one evidence path and an exact excerpt that must occur in
that path, and a counterfactual explaining what evidence would change the
label.

Facts may be `task`, `completion`, `verification`, `violation`, or
`uncertainty`. Every evidence role present in the case must be cited. Pass
requires task, completion, and verification facts; Fail requires task and
violation; Indeterminate requires task and uncertainty. Paths, quotes, fact
text, uniqueness, and size bounds are mechanically validated before digesting
or executing the set. A review object without a declared policy is rejected,
as is an unknown policy.

This is an auditability rule, not an automatic oracle. A reviewer can still
choose a poor exact excerpt. Review remains a human judgement, but the precise
support and falsifier are now visible in version control and bound into the set
digest instead of existing only in the reviewer's head.

## Fresh validation

V4 contains six new calibration and six new holdout cases under
`evidence-v1`, with no ID reused from v1-v3. Pass cases name the requested
target, a completed target-specific action, and a named passing verification.
DeepSeek and OpenAI holdouts carry identical cases and reviews. V3 remains
immutable and cannot become evidence for this change.
