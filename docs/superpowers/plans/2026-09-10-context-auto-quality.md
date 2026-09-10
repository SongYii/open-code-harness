# Automatic Context Quality Evaluation Implementation Plan

**Design:** [Automatic Context Quality Evaluation Design](../specs/2026-09-10-context-auto-quality-design.md)

1. Add a frozen live Subject with a safe automatic-compaction budget.
2. Add the no-focus Scenario, live EvalSet, and matching canonical digests.
3. Add an end-to-end fixture test that observes automatic compaction, source
   constraint inclusion, focus absence, checkpoint use, and file absence.
4. Add docsguard wiring and operator documentation.
5. Run frontend, focused, full race, vet, and diff gates; publish a PR.
6. Run the live Subject and Judge only after a fresh credential is supplied;
   record the immutable Attempt/Score evidence separately.

