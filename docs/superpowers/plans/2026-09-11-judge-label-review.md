# Judge label review implementation plan

1. Add an optional set-level review-policy identity without changing legacy
   corpus validity or digests.
2. Add bounded typed review facts, exact evidence excerpts, and a bounded
   counterfactual to labels selected into the new policy.
3. Fail closed on invented quotes, missing evidence roles, repeated facts,
   unknown policies, and verdicts lacking their required fact kinds.
4. Freeze six new DeepSeek calibration and six new holdout cases with concrete
   target/action/check evidence, plus the identical OpenAI holdout.
5. Prove all bindings, provider equality, no ID reuse across v1-v4, and keyless
   traversal of the production prompt/decoder path.
6. Synchronize architecture, operations, evidence, and authority documents;
   run mutations, race tests, vet, and PR CI.
7. Run v4 DeepSeek calibration and predeclared holdout only after a new explicit
   18 + 18 call authorization; run OpenAI only when available.

## Execution state

Tasks 1–6 are implemented and verified. The separately authorized DeepSeek
part of task 7 ran on 2026-09-12: calibration and the disjoint holdout each
reached 18/18 exact matches, with zero unsafe passes, false fails, unexpected
Indeterminates, or overclaims. Calibration used 17,016 input and 3,589 output
tokens (36,684 microUSD at pinned conservative peak rates); holdout used 17,019
input and 3,541 output tokens (36,496 microUSD). The calibration-frozen policy
passed the untouched holdout. Reports, policy, result, and digest-pinning test
are checked in; the temporary credential was deleted. The equivalent OpenAI
run remains unexecuted because that provider is unavailable.
