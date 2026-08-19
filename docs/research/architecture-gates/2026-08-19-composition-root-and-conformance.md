# Composition Root and Cross-Adapter Conformance Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-19

**Scope:** Slice 5 (composition root and cross-adapter conformance)
primary-source verification. Records the then-public assembly and
contract-test topologies of the required comparison set, establishes whether
an integration-closure slice must precede the ACP adapter, and fixes the
adopt/reject boundary for a composition root inside a strictly layered Go
module.

This document is research evidence. It does not change any implemented
contract and does not authorize copying reference-project types, package
layouts, or runtime.

English is the normative research record. The Chinese file is a synchronized
reading copy.

## Questions

1. After Slices 2–4 landed (SQLite canonical EventStore, JSONL audit replica,
   Runtime Host), is an integration-closure slice the correct next work, or
   should milestone 6 (ACP v1) begin directly?
2. Do the re-verified projects assemble their subsystems at a single named
   composition root, and is that root a documented artifact or an incidental
   `main`?
3. Do they run one adapter-neutral contract suite against more than one
   implementation of the same port, including a durable one?
4. Does a contract suite written against an in-process double remain valid
   when applied to a real transport adapter, or must it be split?
5. Which assembly shapes conflict with the charter's dependency rules and must
   be rejected?

## Verified primary sources

All observed from official repositories on 2026-08-19 by resolving each
default branch to a commit and reading that commit's tree and files. Commits
are the observed state, not endorsements.

| Source | Observed state | Assembly and conformance entry points |
| --- | --- | --- |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | `99f6f02`, TypeScript/Cordis, 2026-08-17 | `apps/cli/composition.md`, `examples/headless-agent/composition.md`, `examples/acp-agent/composition.md`, `packages/boot/app-boot`, `packages/boot/cmdline`, 30+ `tests/loader-composition.spec.ts` |
| [OpenAI Codex](https://github.com/openai/codex) | `d35e549`, Rust, 2026-08-19 | `codex-rs/cli/src/main.rs`, `codex-rs/app-server/src/main.rs`, `codex-rs/code-mode-runtime/src/service_contract_tests.rs`, `scripts/mcp_conformance/` |
| [Pi](https://github.com/earendil-works/pi) | `59a71b2`, TypeScript, 2026-08-18 | `packages/agent/src/harness/session/testing/conformance.ts`, `packages/session-backends/sqlite-node/test/conformance.test.ts`, `packages/telemetry/src/testing/conformance.ts` |
| [Kimi Code](https://github.com/MoonshotAI/kimi-code) | `cdaa80b`, TypeScript, 2026-08-18 | `apps/kimi-code/src/main.ts`, `packages/acp-adapter/test/e2e-happy-path.test.ts`, `packages/acp-server/test/e2e-turn.test.ts` |
| [Grok Build](https://github.com/xai-org/grok-build) | `d92c5b0`, Rust, 2026-08-19 | `crates/codegen/xai-grok-pager/tests/leader_pty_e2e/`, `crates/codegen/xai-crash-handler/tests/integration.rs` |
| [Maka](https://github.com/maka-agent/maka-agent) | `8ea593a`, TypeScript, 2026-08-19 | `packages/runtime-host/src/__tests__/bootstrap-runtime-policy.test.ts`, `apps/desktop/src/main/__tests__/bootstrap-selection-lease.test.ts` |

`badlogic/pi-mono`, cited by earlier gates, now redirects (HTTP 301) to
`earendil-works/pi`. Earlier gates' Pi citations should be read against the
new location; this is a repository move, not a fork.

[DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) remains
community, non-authoritative context.

## Ecosystem convergence

Every verified project, across three languages and three architectures,
converges on the same two properties. Neither is currently satisfied by this
repository.

1. **There is exactly one named place where the concrete implementations are
   assembled, and it is an artifact rather than an accident.** DeepSeek
   Harness goes furthest: each deployable has a `composition.md` carrying a
   generated dependency graph, emitted by `scripts/gen-doc-graphs.ts` and
   marked `do not edit by hand`. Codex names its roots as binaries
   (`codex-rs/cli/src/main.rs`, `codex-rs/app-server/src/main.rs`). Kimi Code
   uses `apps/kimi-code/src/main.ts`. Maka tests its root directly in
   `bootstrap-runtime-policy.test.ts`.
2. **A contract suite is exported from the core and consumed by each
   implementation of the port, durable ones included.** Pi is the closest
   analogue to this repository's design: `packages/agent/src/harness/session/testing/conformance.ts`
   exports `createSessionBackendConformance(fixtureFactory)`, and
   `packages/session-backends/sqlite-node/test/conformance.test.ts` calls it
   with a real SQLite repository over a temporary directory. Pi applies the
   same shape to telemetry (`telemetry/src/testing/conformance.ts` consumed by
   `telemetry/test/conformance.test.ts`).

DeepSeek Harness adds a third property this gate records but does not adopt
wholesale: a per-package `tests/loader-composition.spec.ts` convention in
which a package is exercised *inside a real assembly of its neighbours*. The
observed `packages/llm/llm-retry/tests/loader-composition.spec.ts` constructs
a real `Context` with `AgentRegistry`, `AgentLoop`, `LlmRuntime`,
`SessionStore`, `SystemPrompt`, and `ToolRuntime` before exercising retry
behavior — the package is never verified only against mocks.

## Observed contracts and boundary

### C1. The composition root is a unit under test, not only a program

Maka's `bootstrap-runtime-policy.test.ts` and `bootstrap-selection-lease.test.ts`,
and DeepSeek's `loader-composition.spec.ts` family, show assembly logic being
asserted directly. A root reachable only by launching a process cannot be
covered by the repository's existing race, scenario, and dependency gates.

### C2. Durable backends run the same suite as in-memory ones

Pi's SQLite session backend runs the exported conformance suite unchanged.
This repository already does this for `eventstoretest` — `adapters/sqlite/conformance_test.go`
calls `eventstoretest.Run` with a real `Open`ed store — but not for
`enginescenariotest`, which today runs only against `adapters/memory` from
`application/scenario_test.go`.

### C3. Transport adapters need a contract suite that transports can express

Codex separates `code-mode-runtime/src/service_contract_tests.rs` (in-tree
contract) from `scripts/mcp_conformance/` (external-specification conformance
driven through `codex_conformance_adapter.py` against
`regression-baseline-v1.json`). The two are not the same suite, because the
observable surface differs.

This is directly load-bearing here. `engine/modeltest.Config` exposes
`ReturnNilStream`, `ReturnStreamOnStartupError`, and `CloseError`. Those knobs
describe how an in-process Go value returns from `Stream` and `Close`; an HTTP
adapter cannot be asked to return a nil stream. Of the seven `modeltest.Run`
subtests, four are transport-neutral (ordered event delivery, mid-stream
error, cancellation blocking, concurrent independent streams), one is
expressible with a changed error identity (startup error), and two are
double-only (startup stream/nil pairing, close accounting).

### C4. End-to-end coverage exists at every scale, and is separate from unit tiers

Counting paths matching `e2e`/`integration`/`conformance` at the observed
commits: Grok Build 240, DeepSeek Harness 153, Kimi Code 99, Maka 49, Codex
25, Pi 11. The ratio varies enormously with architecture, but no project has
zero. This repository currently has zero tests that assemble Application,
a durable store, a transport provider adapter, the workspace tools, and the
Runtime Host together.

## Rejected shapes

### R1. Plugin kernel or dependency-injection container

DeepSeek Harness composes through Cordis plugins and a loader; Maka and Kimi
Code use package-level DI. The charter rejects a plugin kernel, and every
prior slice restates that rejection. A composition root that reads
configuration and calls constructors in a fixed order is adopted; a registry,
service locator, reflection-based container, or plugin loader is rejected.

### R2. Generated composition documentation, for now

DeepSeek's generated `composition.md` is the strongest observed answer to
assembly documentation drifting from assembly code. It is rejected for this
slice only on sequencing grounds: generating a graph requires a stable
assembly to generate it from. It is recorded as a candidate once the root
exists, and is the recommended remedy for the drift class this gate observed
in this repository's own README.

### R3. Relaxing the dependency guard to let Application import adapters

The strict rule that no production package imports an adapter is the property
that makes the ports real. The root must be the single exception, and the
exception must be enforced by the existing architecture guard rather than
merely documented, or the guard silently weakens to "anything may import
anything once one package does".

### R4. Live provider calls in the verification path

Kimi Code keeps `real-llm-smoke.e2e.test.ts` and DeepSeek keeps `e2e.yml`
and `pi-ai-provider-e2e.yml` as separate lanes rather than default gates.
Keyless verification is already this repository's rule for the Provider
adapter and is retained: the assembly test drives `openaicompat` over a local
`httptest` server replaying the existing SSE fixtures, exercising the real
HTTP and SSE code path with no network and no credential.

## Findings

### F1. Integration closure is the correct next slice, before ACP

Six slices are individually verified and jointly unproven. Milestone 6 (ACP
v1) must expose a working assembly over a protocol; writing that adapter
against a stack never once assembled would put the first end-to-end
integration inside a protocol conformance effort, where failures are hardest
to attribute. Every verified project has an assembled root beneath its
protocol surface — Codex's `app-server`, Kimi's `acp-server`, DeepSeek's
`examples/acp-agent`. Adopt: close integration first.

### F2. `enginescenariotest` must run against the SQLite adapter

Per C2, and by direct analogy to Pi's SQLite backend running the exported
session conformance suite. The suite's `Harness` already accepts any
`application.EventStore`, so this is a new factory in the adapter's tests, not
a suite change. Any behavior that only holds for the memory adapter is a
defect this reveals.

### F3. `modeltest` must be split before `openaicompat` can consume it

Per C3. A single suite cannot serve both an in-process double and an HTTP
adapter without either weakening the double's accounting checks or forcing the
HTTP adapter to fake conditions it cannot produce. Adopt Codex's separation:
a transport-neutral contract every `engine.Model` satisfies, and a
double-accounting suite that only in-process implementations run.

### F4. The composition root is a library with a thin binary, and it is tested

Per C1 and R1. A `main` package alone is untestable by the existing gates. The
root is a normal package that returns an assembled, closeable value; `cmd/`
only reads configuration and calls it. Assembly is asserted by tests, not by
running a process.

### F5. The dependency guard must gain, not lose, precision

Per R3. The architecture test currently forbids adapter imports from every
owned package. It must be extended with an explicit composition owner that is
permitted to import every adapter, and every other package must remain
forbidden. Absence of an owner must keep meaning "forbidden", so a new package
cannot silently inherit the exception.

### F6. Adopt summary

1. Sequence integration closure before ACP.
2. One named composition root package; a thin `cmd/` binary over it.
3. `enginescenariotest` runs against SQLite as well as memory.
4. `modeltest` splits into transport-neutral and double-only suites;
   `openaicompat` runs the transport-neutral one.
5. One assembly test covering Application, SQLite, `openaicompat` over
   `httptest`, `workspacefs`, `localexec`, and `runtime.Host`.
6. Keyless, network-free verification.

### F7. Reject summary

1. No plugin kernel, container, service locator, or reflection-based wiring.
2. No generated composition graph in this slice.
3. No relaxation of the dependency guard beyond one enforced owner.
4. No live provider calls in the default verification path.
5. No new ports, events, or contract changes: this slice adds an assembly and
   redistributes existing suites. If it requires a contract change, that is a
   finding to report, not a change to make inside this slice.

## Evidence limits

- Repository trees and file contents were read at the commits listed above.
  Behavior was inferred from source and file layout, not from executing any
  reference project.
- Path counts in C4 are matches against path names, not a semantic census of
  test tiers; they establish presence and rough scale only.
- Pi's conformance suite was read for shape (`createSessionBackendConformance`
  and its SQLite consumer). Its individual assertions were not audited.
- DeepSeek Harness's `composition.md` was read as a generated artifact; the
  generator `scripts/gen-doc-graphs.ts` was not read.
- No claim is made about any project's private or unreleased implementation.
  Where this gate says a project does something, it means the observed commit
  contains the cited path.
