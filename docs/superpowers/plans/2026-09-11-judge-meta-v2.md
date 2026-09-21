# Judge meta-evaluation v2 implementation plan

1. Preserve and mechanically bind the v1 live report.
2. Add scaled, provenance-bound pricing with strict decoding, overflow safety,
   and regression tests.
3. Add calibrated policy generation/checking with a predeclared disjoint
   validation set and stable CLI exits.
4. Check in six calibration and six holdout cases, provider-specific frozen
   configs, official price tables, and no-drift contract tests.
5. Run DeepSeek calibration, generate its policy, run the DeepSeek holdout, and
   check the policy (18 + 18 explicitly authorized calls).
6. Run the same holdout against dated OpenAI GPT-5.4 mini (18 separately
   authorized calls) and publish a bound comparison report.
7. Synchronize architecture, operations, evidence, and authority docs; run
   focused mutations, full race/static verification, and submit a separate PR.

## Execution state (2026-09-11)

Steps 1–5 are implemented. The DeepSeek calibration completed at 16/18 exact;
the disjoint holdout completed at 15/18 exact and correctly failed the frozen
calibration envelope. The failure is checked in without post-hoc retuning.
Step 6 remains unrun because GPT/OpenAI credentials are temporarily
unavailable. Step 7 is complete for implementation and DeepSeek evidence; the
OpenAI comparison remains an explicitly deferred live-evidence addition.
