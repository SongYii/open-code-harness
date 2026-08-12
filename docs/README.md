# Open Code Harness Documentation

This directory is the authority map for project documentation. Open Code
Harness targets industrial use, while remaining honest about maturity: the
repository is pre-v0 and not yet a general availability release.

Small milestones are production-quality slices, not demos. A milestone may
defer capabilities that have their own future specification, but it may not use
temporary architecture, unbounded resources, silent partial writes, or
test-only production branches for the capabilities it does deliver.

## Authority levels

- **Normative** — defines required architecture or behavior. Changes require a
  reviewed specification, ADR, or implementation plan.
- **Implemented contract** — documents behavior already enforced by code and
  tests. Code and contract must change together.
- **Reading copy** — a synchronized translation of a normative document. The
  named normative source wins if the copies diverge.
- **Plan** — an executable implementation sequence for an approved design. A
  plan is not a substitute for the design contract.
- **Research evidence** — records comparison or evaluation inputs. It informs
  decisions but does not become a requirement without adoption in a spec.

## Current authoritative documents

| Status | Authority | Document | Purpose |
| --- | --- | --- | --- |
| Accepted | Normative charter | [Foundational architecture](superpowers/specs/2026-08-11-open-code-harness-architecture-design.md) | Product positioning, system boundaries, protocol choices, quality attributes, and milestone order |
| Implemented | Implemented contract | [Domain events and state machine](architecture/domain-events.md) | Current Session/Turn commands, events, errors, invariants, codec, and replay rules |
| Implemented | Implemented contract | [Engine vertical slice](architecture/engine-vertical-slice.md) | Current Application/Engine ports, bounded stream execution, atomic durability, cancellation, errors, adapters, evidence, and exclusions |
| Implemented | Reading copy | [已实现 Engine 纵切](architecture/engine-vertical-slice.zh-CN.md) | 与已实现 Engine 合同同步的中文语义阅读版 |
| Accepted | Normative design | [Industrial Engine vertical slice](superpowers/specs/2026-08-12-engine-vertical-slice-design.md) | Application/Engine boundary, formal ports, deterministic adapters, atomic event flow, failure semantics, and acceptance criteria |
| Accepted | Reading copy | [工业级 Engine 最小纵切](superpowers/specs/2026-08-12-engine-vertical-slice-design.zh-CN.md) | Chinese synchronized reading copy of the Engine design |
| Complete | Plan | [Engine vertical slice implementation plan](superpowers/plans/2026-08-12-engine-vertical-slice.md) | Completed ten-task sequence for domain Items, ports, adapters, Engine, application orchestration, concurrency, and evidence |
| Complete | Reading copy | [Engine 纵切实施计划中文阅读版](superpowers/plans/2026-08-12-engine-vertical-slice.zh-CN.md) | Chinese synchronized execution guide for the completed Engine plan |
| Complete | Research evidence | [Task 1 Assistant Item architecture gate](research/architecture-gates/2026-08-12-task-1-assistant-item-lifecycle.md) | Official-project comparison and load-bearing amendments required before Task 1 implementation |
| Complete | Research evidence | [Tasks 3–4 Application/EventStore architecture gate](research/architecture-gates/2026-08-12-tasks-3-4-application-eventstore.md) | Agent-project and EventStoreDB comparison establishing exact CAS, atomicity, replay authority, fault, and adapter contracts |
| Complete | Reading copy | [Task 3–4 Application/EventStore 架构门中文阅读版](research/architecture-gates/2026-08-12-tasks-3-4-application-eventstore.zh-CN.md) | 与 Task 3–4 架构门同步的中文决策记录 |
| Complete | Research evidence | [Tasks 5–6 Engine stream/runtime architecture gate](research/architecture-gates/2026-08-12-tasks-5-6-engine-stream-runtime.md) | Official-project comparison establishing stream ownership, cleanup, payload, ordering, byte-boundary, error-tree, adapter, and concurrency contracts |
| Complete | Reading copy | [Task 5–6 Engine stream/runtime 架构门中文阅读版](research/architecture-gates/2026-08-12-tasks-5-6-engine-stream-runtime.zh-CN.md) | 与 Task 5–6 Engine stream/runtime 架构门同步的中文决策记录 |
| Complete | Research evidence | [Tasks 7–9 Application orchestration architecture gate](research/architecture-gates/2026-08-12-tasks-7-9-application-orchestration.md) | Primary-source comparison establishing atomic admission, preflight, append acceptance, cancellation, result algebra, concurrency, and recovery-boundary contracts |
| Complete | Reading copy | [Task 7–9 Application 编排架构门中文阅读版](research/architecture-gates/2026-08-12-tasks-7-9-application-orchestration.zh-CN.md) | 与 Task 7–9 Application 编排架构门同步的中文决策记录 |
| Complete | Plan | [Domain implementation plan](superpowers/plans/2026-08-11-domain-events-state-machine.md) | Completed Task 1–8 implementation sequence |
| Complete | Reading copy | [领域实施计划中文阅读版](superpowers/plans/2026-08-11-domain-events-state-machine.zh-CN.md) | Chinese synchronized reading copy of the completed plan |

## Milestone status

1. **Harness domain, events, Session/Turn state machine** — implemented.
2. **Industrial Go Engine executable vertical slice** — implemented and verified.
3. **Provider contract and first real provider** — not designed yet.
4. **Tool Runtime, Policy, and minimal workspace tools** — not designed yet.
5. **ACP v1 adapter and conformance** — not designed yet.
6. **TypeScript TUI client** — not designed yet.
7. **Context Engine, checkpoint, and recovery** — not designed yet.
8. **MCP client adapter** — not designed yet.
9. **Scenario evaluation, benchmarks, and OpenTelemetry** — not designed yet.
10. **Open-source release, governance, and ecosystem documentation** — not designed yet.

## Documentation rules

1. Every subsystem receives an approved design before an implementation plan.
2. English normative specs receive a synchronized Chinese reading copy unless
   a document explicitly declares another authority arrangement.
3. Public interfaces identify their stability level: `stable`, `experimental`,
   or `internal`.
4. A design names its exclusions, failure semantics, resource bounds,
   evaluation method, and evidence required for completion.
5. Historical drafts move under an archive location and must not compete with
   current authority.
6. Research projects are cited by official repositories or primary technical
   documents; unavailable implementation details are recorded as unknown, not
   inferred from product marketing.
