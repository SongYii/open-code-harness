# Live Judge Structured-output Design

**Status:** Accepted normative design  
**Date:** 2026-09-10  
**Research:** [Live Judge structured-output research](../../research/architecture-gates/2026-09-10-live-judge-structured-output.md)

## Scope

Make the existing strict-JSON quality judge use provider-level structured
output without weakening its evidence checks, cost accounting, or append-only
history. This slice changes no Subject route and performs no hidden retry.

## Contract

`JudgeProvider` requires `responseFormat: "json_object"`; `thinkingMode` is an
optional provider extension whose only admitted value is `disabled`. The
checked-in DeepSeek route freezes it. The OpenAI-compatible adapter emits these
as `response_format.type` and, when set, `thinking.type`. Its capability profile
declares structured output required, and both static wire choices are copied into
`RequestIdentity` and `model.request.recorded` when that identity is used by
Application.

Unknown values are rejected before network I/O. Empty, malformed, truncated,
or semantically invalid output remains one Indeterminate result with the
provider-reported usage preserved.

## Retry boundary

`RunJudge` calls its `JudgeCaller` exactly once. Repeating `och-eval judge`
appends a new Score; it does not replace or conceal the previous observation.
This preserves variance, usage, and cost evidence.

## Acceptance

- Exact request-body tests observe both provider fields.
- Adapter and JudgeConfig tests reject unsupported values before a call; a
  provider without the thinking extension may omit it.
- A malformed response proves there is no hidden second call.
- Checked-in JudgeConfigs and EvalSets carry matching canonical digests.
- Existing strict semantic validation and deterministic prerequisites remain
  unchanged.
