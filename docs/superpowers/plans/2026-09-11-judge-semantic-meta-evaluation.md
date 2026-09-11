# Judge Semantic Meta-evaluation Implementation Plan

**Design:** [Judge Semantic Meta-evaluation Design](../specs/2026-09-11-judge-semantic-meta-evaluation-design.md)

1. Define and strictly validate/digest the frozen meta-set document.
2. Refactor Judge execution so Attempt evidence and labelled inline evidence
   share one renderer and one response-validation path.
3. Implement the pure repeated runner, observations, confusion matrix, and
   asymmetric error counters with focused mutation tests.
4. Add `och-eval judge-meta` with config binding, dual consent, exact call-budget
   acknowledgement, optional frozen pricing, and one JSON report.
5. Check in the six-case seed corpus and exact digest/fixture-run guards.
6. Synchronize operator guide, English/Chinese architecture contracts, authority
   table, milestone status, and evidence ledger.
7. Run targeted tests, mutations, full race tests, vet, formatting, and diff
   checks; open a PR without making a paid call.
