# Plain-Language Implementation Guide Plan

- Status: Implemented
- Date: 2026-09-10
- Design: [Plain-language implementation guide and documentation standard](../specs/2026-09-10-plain-language-implementation-guide-design.md)

## Task 1: repair the authoritative module inventory

- Register Evaluation's implemented contract, reading copy, and evidence.
- Use `Implemented contract` rows as the one module inventory.

## Task 2: publish bilingual five-question guides

- Add `docs/architecture/how-it-works.md` and its Chinese reading copy.
- Cover every implemented contract with the same five questions.
- Link real code, the detailed contract, and evidence.
- Prefer concrete failures and outcomes over interface catalogs.

## Task 3: label historical snapshots

- Preserve dated evidence text unchanged.
- Add current-status notices to early ledgers whose “not implemented” lists
  have been superseded.

## Task 4: make coverage executable

- Parse guide entries using stable contract markers.
- Require one entry per implemented contract in both guides.
- Require all five headings within every entry.
- Add the gate to the documented executable rules.

## Task 5: verify and publish evidence

- Run a mutation that removes a guide entry and require docsguard to fail.
- Run docsguard, architecture tests, vet, full race, and diff checks.
- Record commits, findings, mutation output, and limitations.
