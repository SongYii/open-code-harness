# Judge meta-evaluation v2 architecture gate

**Status:** Complete research evidence

**Date:** 2026-09-11

## Questions and evidence

This gate rechecked three facts before extending the six-case seed.

1. **Can the existing price type represent current rates?** No. It stores an
   integer number of currency microunits per token. DeepSeek V4 Pro's official
   peak input rate is USD 1.32 per million tokens and its output rate is USD
   3.96 per million; those are 1.32 and 3.96 microUSD per token. Truncation or
   rounding at the token rate would materially misstate a run. The official
   table also has peak/off-peak and cache-hit/cache-miss columns, so the chosen
   basis must be frozen with the numbers.
2. **Which independent provider/model is compatible with the existing wire
   adapter?** The official OpenAI model page identifies
   `gpt-5.4-mini-2026-03-17`, a 400k-context model supporting Chat Completions,
   structured outputs, and reasoning effort `none`. The official standard
   synchronous price table lists GPT-5.4 mini at USD 0.75/M input, 0.075/M
   cached input, and 4.50/M output. This is a second provider service and model,
   but not a second adapter protocol; the GA wording must preserve that limit.
3. **Can calibration validate itself?** No. A policy learned from one report
   must bind a different, predeclared validation-set digest. Merely rejecting
   the calibration digest still permits choosing an easier set after seeing
   the result.

Sources retrieved from the providers on 2026-09-11:

- DeepSeek official pricing: <https://api-docs.deepseek.com/quick_start/pricing>
- OpenAI official pricing: <https://developers.openai.com/api/docs/pricing>
- OpenAI GPT-5.4 mini: <https://developers.openai.com/api/docs/models/gpt-5.4-mini>

## Decision

- Preserve v1 identities and its 18-call report unchanged.
- Add mutually exclusive scaled price rates (`rateUnitTokens` plus integer
  microunits per rate unit), sum with arbitrary precision, then round the final
  call total upward once to a currency microunit. Invalid/overflowing rates are
  unavailable, never wrapped or silently zero.
- Bind price provenance (source URL, observation date, and rate basis) into the
  price-table digest. DeepSeek uses the peak table as a conservative upper
  bound because reports do not record its time band.
- Add six calibration and six disjoint holdout cases. Three repetitions make
  each set exactly 18 calls. The OpenAI holdout must be byte-equivalent in
  labels/evidence to the DeepSeek holdout.
- A calibrated policy copies the calibration report's empirical envelope and
  binds both its calibration and predeclared validation set. This is a
  scenario-specific regression gate, not a population accuracy guarantee.
