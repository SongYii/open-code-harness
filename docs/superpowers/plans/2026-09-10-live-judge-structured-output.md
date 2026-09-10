# Live Judge Structured-output Implementation Plan

**Design:** [Live Judge Structured-output Design](../specs/2026-09-10-live-judge-structured-output-design.md)

1. Freeze the two provider wire choices in JudgeConfig and checked-in examples.
2. Map and validate them in the OpenAI-compatible adapter and request identity.
3. Preserve the choices in Application/domain request evidence.
4. Add exact-body, rejection, and no-hidden-retry tests.
5. Update operator/architecture docs and canonical example digests.
6. Run targeted tests, full race tests, vet, formatting, and diff checks; then
   open a stacked PR without spending another live-model call.
