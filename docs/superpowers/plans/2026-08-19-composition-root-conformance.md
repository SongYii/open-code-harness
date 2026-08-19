# Composition Root and Cross-Adapter Conformance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Assemble every implemented adapter behind one tested composition root, run the existing scenario suite against the durable store, split the model contract so the real HTTP provider adapter can satisfy it, and prove the whole stack executes one tool-calling turn end to end without network or credentials.

**Architecture:** No port, event, error code, or contract changes. `internal/harness/composition` is the single package permitted to import adapters, enforced by the architecture guard. `cmd/och` is a thin binary over it. Conformance suites are redistributed, not rewritten.

**Tech Stack:** Go 1.26, standard library (`net/http/httptest`, `errors`, `os/signal`), `modernc.org/sqlite` through the existing adapter, `testing`, race and cross-build tooling, GitHub Actions.

## Global Constraints

- Normative specification: `docs/superpowers/specs/2026-08-19-composition-root-conformance-design.md`; sections 4–13 are mandatory. Research evidence: `docs/research/architecture-gates/2026-08-19-composition-root-and-conformance.md`.
- This slice adds no capability. No new port, event type, error code, error category, or domain transition. If a task appears to need one, stop and report it as a finding; do not widen the slice.
- No implemented contract document may be edited. A required edit is a finding recorded in the evidence ledger with its own follow-up slice.
- `enginescenariotest` and `eventstoretest` must not change. `engine/modeltest` is reorganized only by extraction: every case `Run` executes today must still execute against `testkit.ScriptedModel` after the split.
- `internal/harness/composition` is the only package that may import `internal/harness/adapters/...`. `cmd/och` imports `composition` and the standard library only. Every other prohibition in the architecture guard is unchanged, and an unowned directory remains forbidden.
- No plugin kernel, registry, service locator, reflection, or `init()`-time wiring. Construction is explicit calls in a fixed order.
- Verification is keyless and network-free. The provider is driven through `httptest` replaying SSE fixtures. No test may read a real API key, and no default CI lane may contact a provider.
- No test-only branch in production code. Test seams are constructor parameters, not build tags or exported mutable globals.
- `Open` never returns a non-nil `Assembly` with a non-nil error, and never leaks a partially constructed resource.
- No sleep-based synchronization in new tests. Rendezvous uses channels with a generous documented timeout, per the constant introduced for the application package.
- Every behavior is TDD: observe the intended failure before implementation, then run focused and full tests.
- Every task ends with `gofmt`, focused tests, `go test ./... -count=1`, `go test -race ./... -count=1` when the task changes concurrency or assembly, an independent review gate, and one small commit.
- English is normative. The Chinese plan is a complete synchronized reading copy committed together.

## File map

| Path | Responsibility |
| --- | --- |
| `internal/harness/composition/doc.go` | Package scope: the one place adapters are named, and what it may not do |
| `internal/harness/composition/config.go` | `Config`, defaults, and total fail-closed `Validate` |
| `internal/harness/composition/assembly.go` | `Open`, `Assembly`, accessors, ordered `Close` with joined errors |
| `internal/harness/composition/config_test.go` | Validation table: every field rejected for every documented reason |
| `internal/harness/composition/assembly_test.go` | Construction order, no-leak-on-failure, idempotent `Close`, shutdown timeout |
| `internal/harness/composition/end_to_end_test.go` | The assembly test: one tool-calling turn over the real stack |
| `internal/harness/composition/testdata/sse/read_file_turn.sse` | SSE script for the assembly test, if no existing fixture fits |
| `cmd/och/main.go` | Flags, environment, signal handling; no other logic |
| `internal/harness/engine/modeltest/contract.go` | `Contract`, `RunContract`: transport-neutral cases |
| `internal/harness/engine/modeltest/suite.go` | `Run` retained: `RunContract` plus double-accounting cases |
| `internal/harness/adapters/openaicompat/contract_test.go` | `RunContract` over an `httptest` server with `ProviderFailure` matchers |
| `internal/harness/adapters/sqlite/scenario_test.go` | `enginescenariotest.Run` against a real store |
| `internal/harness/architecture/dependencies_test.go` | `ownerComposition`; unowned directories still forbidden |

---

### Task 1 (PR 1): Model contract split

**Intent:** Make the model contract expressible by a transport before any transport consumes it.

- [ ] Add `internal/harness/engine/modeltest/contract.go` with `Contract{Factory, MatchStartupError, MatchStreamError}` and `RunContract`.
- [ ] Move the transport-neutral cases into `RunContract`: exact request delivery with ordered unicode `text_delta`/`tool_call`/`completed`, mid-stream error, a step blocking until cancellation, and concurrent independent streams.
- [ ] When a matcher is nil, `RunContract` requires error identity, preserving today's assertions for in-process doubles.
- [ ] Keep `Run(t, factory)` as the double entry point: it calls `RunContract` with nil matchers, then runs `ReturnNilStream`, `ReturnStreamOnStartupError` precedence, and `Close` accounting.
- [ ] Verify no case was lost: `testkit.ScriptedModel` runs the same set before and after.

**Verification:** `go test ./internal/harness/testkit ./internal/harness/engine/... -count=1`; a deliberate mutation in `RunContract` must fail `scripted_model_test.go`.

**Done when:** the split exists, `testkit` coverage is unchanged, and no adapter consumes `RunContract` yet.

---

### Task 2 (PR 2): `openaicompat` satisfies the transport contract

**Intent:** Prove the real HTTP adapter obeys the same `engine.Model` contract as the double.

- [ ] Add `adapters/openaicompat/contract_test.go` with a factory translating `modeltest.Config` into SSE bytes served by `httptest.Server`.
- [ ] Map `Steps` to SSE chunks: `text_delta` to a content delta, `tool_call` to a `tool_calls` delta, `completed` to a finish chunk and `[DONE]`.
- [ ] Express a startup failure as a non-2xx response and a mid-stream failure as a truncated or malformed event; supply matchers asserting the documented `ProviderFailure` classification rather than error identity.
- [ ] Express `WaitForCancel` with a handler that blocks until the request context is done.
- [ ] Reuse `testdata/sse` fixtures where one already expresses the case; add a fixture only where none does.
- [ ] Record in the test file which contract cases are transport-inexpressible and why, referencing spec section 9.

**Verification:** `go test -race ./internal/harness/adapters/openaicompat -count=1`; the suite must fail if the SSE grammar is reordered.

**Done when:** `RunContract` is green against `openaicompat` with no production change to the adapter. A production change required here is a finding: report it before making it.

---

### Task 3 (PR 3): Scenario suite over the durable store

**Intent:** Stop the Engine scenario contract from being a memory-adapter-only guarantee.

- [ ] Add `adapters/sqlite/scenario_test.go` calling `enginescenariotest.Run` with a `Harness` whose `Store` is a real store opened over `t.TempDir()`, mirroring `conformance_test.go`.
- [ ] Do not modify `enginescenariotest`.
- [ ] For every scenario that fails, determine whether the defect is in the SQLite adapter or in an assumption the memory adapter happens to satisfy, fix the adapter, and record the finding.
- [ ] If a scenario proves to encode a memory-only assumption, report it and correct the suite only if every behavior the memory adapter asserts is preserved.

**Verification:** `go test -race ./internal/harness/adapters/sqlite ./internal/harness/application -count=1`, and `-count=5` on the new test to expose ordering sensitivity.

**Done when:** the same scenario suite is green against both adapters and every divergence found is recorded.

---

### Task 4 (PR 4): Composition root and dependency guard

**Intent:** Create the one place adapters are named, and prove nothing else may name them.

- [ ] Add `composition/doc.go`, `config.go`, and `assembly.go` per spec sections 4–6: fixed construction order, reverse-order `Close`, joined errors, bounded shutdown timeout, idempotent `Close`.
- [ ] `Config.Validate` is total and fail-closed; every field is checked before any resource is constructed.
- [ ] `Open` releases everything already constructed before returning an error, and never returns a non-nil `Assembly` with a non-nil error.
- [ ] Extend `architecture/dependencies_test.go` with `ownerComposition`, permitting adapter imports there and nowhere else; assert an unowned directory is still forbidden and that `application` still cannot import an adapter.
- [ ] Add `config_test.go` and `assembly_test.go`: validation table, no-leak-on-failure including no database file created, double `Close`, and shutdown timeout.

**Verification:** `go test -race ./... -count=1`; `go test ./internal/harness/architecture -count=1` must fail if the composition exception is widened to a second package.

**Done when:** the root assembles and tears down cleanly and the guard is strictly more precise than before.

---

### Task 5 (PR 5): Assembly test, binary, and evidence

**Intent:** Prove the stack runs a real turn, and record auditable completion.

- [ ] Add `composition/end_to_end_test.go` per spec section 10: temporary workspace seeded with a file, real SQLite database, `openaicompat` over `httptest` scripting a `read_file` call then a final answer, `policy.ModeDefault`, host started and closed through `Assembly.Close`.
- [ ] Assert turn completion, that the durable stream replays to the same state, that it contains `tool.call.started`, `policy.decision.recorded`, and `tool.call.completed`, and that the final text reflects the file contents.
- [ ] Add `cmd/och/main.go`: flags, environment, `composition.Open`, signal wait, `Close`, non-zero exit on error, and nothing else.
- [ ] Add `cmd/och` to the cross-build matrix expectations; confirm `GOOS=windows` and `GOOS=darwin` build.
- [ ] Write `docs/architecture/composition-root.md` and its Chinese reading copy as the implemented contract, and `docs/architecture/composition-root-evidence.md` as the bilingual evidence ledger recording every task commit, verification commands, contract findings, and deferred GA blockers.
- [ ] Update `docs/README.md` authority table and milestone status, and the root `README.md` current status.

**Verification:** `gofmt -l .`; `go vet ./...`; `go test -race ./... -count=1`; `GOOS=windows go build ./...`; `GOOS=darwin go build ./...`; `go run ./cmd/och -help`.

**Done when:** one command builds the binary, one test proves the stack executes a tool-calling turn durably, and the ledger records it.

---

## Final completion gate

- [ ] Spec sections 4–13 are satisfied, or every deviation is recorded in the ledger with a reason.
- [ ] `enginescenariotest.Run` green against memory and SQLite.
- [ ] `modeltest.RunContract` green against `testkit.ScriptedModel` and `openaicompat`; `modeltest.Run` green against `testkit.ScriptedModel` with no lost case.
- [ ] Adapter imports permitted in `composition` only; unowned directories still forbidden.
- [ ] The assembly test passes with no network, no credential, and no sleep-based synchronization.
- [ ] `cmd/och` builds on every cross-build platform.
- [ ] No implemented contract document was edited, or every edit is justified in the ledger as a recorded finding.
- [ ] `go test -race ./... -count=1` green three consecutive times, to catch order and timing sensitivity before it reaches CI.
