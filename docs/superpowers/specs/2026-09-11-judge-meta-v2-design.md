# Judge meta-evaluation v2: precise pricing, holdout calibration, provider comparison

**Status:** Accepted normative design

**Date:** 2026-09-11

**Research:** [architecture gate](../../research/architecture-gates/2026-09-11-judge-meta-v2-pricing-calibration-provider.md)

## Contract

V1 files and reports are immutable. V2 adds twelve reviewed cases split before
execution into equal calibration and validation sets. Each set has two Pass,
three Fail, and one Indeterminate label and runs three repetitions. Provider
variants of a holdout must have identical ordered cases; only set identity and
the exact JudgeConfig binding differ.

Price entries support either legacy integer microunits per token or a scaled
integer rate over `rateUnitTokens`, never both. Scaled computation sums all
input/output/cache terms before one ceiling division, so a non-zero sub-
microcurrency call cannot become a computed zero. Digests reject missing
currency, empty/duplicate models, negative/mixed/incomplete rates, partial
provenance, and arithmetic outside signed 64-bit report storage.

`och.eval.judge-meta-policy` is calibrated from one complete report and records
that report digest, its set digest, the exact JudgeConfig digest, the exact
required sample count, and the observed minimum exact matches / maximum four
directional-error counts. It also binds a different validation-set digest
provided before validation. `judge-meta-check` refuses incomplete, differently
configured, differently sized, calibration-set, or substituted-set reports.
Passing means the holdout stays inside the measured envelope; it is not a GA
or statistical-generalization claim.

Two offline commands publish the policy and its check result. Live execution
continues to require the existing dual consent and exact call count. DeepSeek
calibration and holdout are 18 calls each; the OpenAI holdout is another 18.
No command reads credentials during calibration or checking.

## Provider and pricing boundary

The DeepSeek configuration binds the official peak V4 Pro rates as an upper
bound. The OpenAI configuration binds official standard synchronous rates and
the dated GPT-5.4 mini model. Both travel through the existing
`openaicompat` adapter. Therefore this closes model/provider-service comparison,
not adapter-protocol breadth.
