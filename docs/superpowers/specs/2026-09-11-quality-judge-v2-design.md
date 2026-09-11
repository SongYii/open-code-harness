# Quality Judge v2: resolved contradictions and authoritative evidence scope

**Status:** Accepted normative design

**Date:** 2026-09-11

## Finding

The v2 DeepSeek holdout exposed two independent sources of unnecessary
Indeterminate verdicts. First, `och_quality_judge_v1` described
`contradictoryEvidence` as any contradiction, while production interpreted any
non-empty value as an *unresolved* contradiction and overrode even a criterion
level Fail. Second, the prompt called records bounded excerpts without saying
that a short, untruncated record is complete for the judgement. The model could
therefore demand imagined underlying files even when every required role was
present.

## Contract

V1 prompt bytes, configuration identities, reports, and decoding remain
unchanged. A new frozen `och_quality_judge_v2` is opt-in through the existing
JudgeConfig prompt ID and digest.

V2 renames the wire field to `unresolvedContradictoryEvidence`. A contradiction
that establishes a rubric violation is resolved evidence: it is cited through
`evidenceReferences` and may produce Fail. Only a conflict that prevents
choosing Pass or Fail belongs in the renamed field, and any non-empty renamed
field remains fail-closed as Indeterminate. The ambiguous v1 field is an
unknown field under v2 and is rejected by strict decoding.

The prompt states that the supplied bundle is the complete authoritative input
for this judgement. `truncated=false` means the selected record is complete as
supplied, however short. `truncated=true` remains usable when visible bytes are
enough to decide. A judge must not change an established Fail into
Indeterminate merely because additional unrelated detail could exist.

## Validation boundary

The twelve v2 cases have been observed and cannot validate this change. V3
therefore freezes six new calibration and six new holdout cases, with no case
ID shared with v1 or v2. The two provider holdouts remain byte-equivalent aside
from set identity and JudgeConfig digest. Live calibration and holdout must
still be authorized separately, and their envelope may not be widened after
seeing the holdout.
