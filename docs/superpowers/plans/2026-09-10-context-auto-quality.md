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

## Follow-up: independent reasoning effort

7. Add one provider-neutral `ReasoningEffort` vocabulary and an explicit
   per-request override; never derive request semantics from `Purpose`.
8. Expose normal response effort through Provider config and summary effort
   through Context config, including CLI, in-process, ACP, Subject, and Judge
   paths.
9. Preserve legacy `thinkingMode` compatibility but reject mixed controls and
   unknown values before network I/O.
10. Prove exact HTTP bodies, summary override, executor parity, strict event
    replay, and the automatic-context end-to-end contract.
