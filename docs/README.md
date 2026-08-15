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
| Implemented | Implemented contract | [EventStore v2](architecture/eventstore-v2.md) | Current four-method Store, identity ownership, digest, pagination, admission, compact state, resolution, and exclusions |
| Implemented | Reading copy | [已实现 EventStore v2 合同](architecture/eventstore-v2.zh-CN.md) | 与已实现 EventStore v2 合同同步的中文语义阅读版 |
| Complete | Evidence ledger | [EventStore v2 completion evidence / EventStore v2 完成证据](architecture/eventstore-v2-evidence.md) | Auditable Task 1–9 commits, verification commands, benchmark sample, and deferred blockers |
| Complete | Evidence ledger | [Engine vertical slice completion evidence / Engine 纵切完成证据](architecture/engine-vertical-slice-evidence.md) | Auditable Task 1–10 commits, architecture gates, verification commands, and deferred GA blockers |
| Accepted | Normative design | [Industrial Engine vertical slice](superpowers/specs/2026-08-12-engine-vertical-slice-design.md) | Application/Engine boundary, formal ports, deterministic adapters, atomic event flow, failure semantics, and acceptance criteria |
| Accepted | Reading copy | [工业级 Engine 最小纵切](superpowers/specs/2026-08-12-engine-vertical-slice-design.zh-CN.md) | Chinese synchronized reading copy of the Engine design |
| Accepted | Normative design | [Production Runtime persistence, recovery, and client boundary](superpowers/specs/2026-08-13-runtime-persistence-recovery-client-design.md) | SQLite canonical events, exact append retry, audit replica, fencing, crash recovery, ACP boundary, resource limits, and staged delivery |
| Accepted | Reading copy | [生产 Runtime 持久化、恢复与客户端边界](superpowers/specs/2026-08-13-runtime-persistence-recovery-client-design.zh-CN.md) | 与生产 Runtime 持久化、恢复和客户端边界规范完整同步的中文阅读版 |
| Accepted | Normative design | [EventStore v2 contract migration](superpowers/specs/2026-08-13-eventstore-v2-contract-design.md) | Focused Slice 1 identities, exact append, pagination, admission, compact aggregate, unknown-outcome, and verification contract |
| Accepted | Reading copy | [EventStore v2 Contract Migration 中文阅读版](superpowers/specs/2026-08-13-eventstore-v2-contract-design.zh-CN.md) | 与 EventStore v2 聚焦规范完整同步的中文阅读版 |
| Implemented plan | Plan | [EventStore v2 contract migration implementation plan](superpowers/plans/2026-08-13-eventstore-v2-contract.md) | Frozen nine-task sequence; completion claims are backed by the evidence ledger, not checkbox state |
| Implemented plan | Reading copy | [EventStore v2 Contract Migration 实施计划中文阅读版](superpowers/plans/2026-08-13-eventstore-v2-contract.zh-CN.md) | 与 EventStore v2 实施计划完整同步的中文执行阅读版 |
| Implemented plan | Plan | [Engine vertical slice implementation plan](superpowers/plans/2026-08-12-engine-vertical-slice.md) | Frozen ten-task implementation sequence; completion claims are backed by the evidence ledger, not checkbox state |
| Implemented plan | Reading copy | [Engine 纵切实施计划中文阅读版](superpowers/plans/2026-08-12-engine-vertical-slice.zh-CN.md) | Chinese synchronized reading copy of the implemented sequence; see the evidence ledger for completion proof |
| Complete | Research evidence | [Task 1 Assistant Item architecture gate](research/architecture-gates/2026-08-12-task-1-assistant-item-lifecycle.md) | Official-project comparison and load-bearing amendments required before Task 1 implementation |
| Complete | Research evidence | [Tasks 3–4 Application/EventStore architecture gate](research/architecture-gates/2026-08-12-tasks-3-4-application-eventstore.md) | Agent-project and EventStoreDB comparison establishing exact CAS, atomicity, replay authority, fault, and adapter contracts |
| Complete | Reading copy | [Task 3–4 Application/EventStore 架构门中文阅读版](research/architecture-gates/2026-08-12-tasks-3-4-application-eventstore.zh-CN.md) | 与 Task 3–4 架构门同步的中文决策记录 |
| Complete | Research evidence | [Tasks 5–6 Engine stream/runtime architecture gate](research/architecture-gates/2026-08-12-tasks-5-6-engine-stream-runtime.md) | Official-project comparison establishing stream ownership, cleanup, payload, ordering, byte-boundary, error-tree, adapter, and concurrency contracts |
| Complete | Reading copy | [Task 5–6 Engine stream/runtime 架构门中文阅读版](research/architecture-gates/2026-08-12-tasks-5-6-engine-stream-runtime.zh-CN.md) | 与 Task 5–6 Engine stream/runtime 架构门同步的中文决策记录 |
| Complete | Research evidence | [Tasks 7–9 Application orchestration architecture gate](research/architecture-gates/2026-08-12-tasks-7-9-application-orchestration.md) | Primary-source comparison establishing atomic admission, preflight, append acceptance, cancellation, result algebra, concurrency, and recovery-boundary contracts |
| Complete | Reading copy | [Task 7–9 Application 编排架构门中文阅读版](research/architecture-gates/2026-08-12-tasks-7-9-application-orchestration.zh-CN.md) | 与 Task 7–9 Application 编排架构门同步的中文决策记录 |
| Complete | Research evidence | [Runtime persistence, recovery, and client architecture gate](research/architecture-gates/2026-08-13-runtime-persistence-recovery-client.md) | Primary-source comparison of Codex, Kimi, Maka, Reasonix, Pi, Grok Build, event stores, SQLite, and transactional outbox designs |
| Complete | Reading copy | [Runtime 持久化、恢复与客户端架构门中文阅读版](research/architecture-gates/2026-08-13-runtime-persistence-recovery-client.zh-CN.md) | 与 Runtime 持久化、恢复和客户端边界架构门完整同步的中文证据记录 |
| Complete | Research evidence | [EventStore v2 contract architecture gate](research/architecture-gates/2026-08-13-eventstore-v2-contract.md) | Focused primary-source evidence for exact append, authority, pinned reads, admission, and unknown-outcome semantics |
| Complete | Reading copy | [EventStore v2 Contract 架构门中文阅读版](research/architecture-gates/2026-08-13-eventstore-v2-contract.zh-CN.md) | 与 EventStore v2 Contract 聚焦架构门完整同步的中文证据记录 |
| Complete | Research evidence | [DeepSeek Harness comparison and delivery sequencing](research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.md) | Official DeepSeek Harness adopt/reject boundary, required comparison set, and post-Slice-1 sequencing |
| Complete | Reading copy | [DeepSeek Harness 对照与交付顺序](research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.zh-CN.md) | 与 DeepSeek Harness 对照及交付顺序结论完整同步的中文证据记录 |
| Complete | Plan | [Domain implementation plan](superpowers/plans/2026-08-11-domain-events-state-machine.md) | Completed Task 1–8 implementation sequence |
| Complete | Reading copy | [领域实施计划中文阅读版](superpowers/plans/2026-08-11-domain-events-state-machine.zh-CN.md) | Chinese synchronized reading copy of the completed plan |

## Milestone status

1. **Harness domain, events, Session/Turn state machine** — implemented.
2. **Industrial Go Engine executable vertical slice** — implemented and verified.
3. **EventStore v2 contract migration** — implemented and verified; required before a SQLite adapter, not a replacement for Provider/Tool.
4. **Provider contract and first real provider** — not designed yet; next product design after EventStore v2 Slice 1.
5. **Tool Runtime, Policy, and minimal workspace tools** — not designed yet; next product design after EventStore v2 Slice 1.
6. **ACP v1 adapter and conformance** — cross-cutting boundary accepted; focused implementation specification not written yet.
7. **TypeScript TUI client** — cross-cutting boundary accepted; focused implementation specification not written yet.
8. **Context Engine, checkpoint, and recovery** — production persistence and crash-recovery boundary accepted; Context Engine and focused implementation specifications not written yet.
9. **MCP client adapter** — not designed yet.
10. **Scenario evaluation, benchmarks, and OpenTelemetry** — not designed yet.
11. **Open-source release, governance, and ecosystem documentation** — not designed yet.

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
7. Later subsystem architecture gates re-verify the then-public official
   sources that are directly relevant to the slice: Pi, Kimi Code, Grok Build,
   Codex, Maka, and DeepSeek Harness. Community projects such as
   DeepSeek-Reasonix are non-authoritative context only.
