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
| Implemented | Implemented contract | [Provider adapter](architecture/provider-adapter.md) | Current `engine.Model` HTTP adapter, Chat Completions SSE, capability profiles, classified `ProviderFailure`, reconstructable request/usage facts, and exclusions |
| Implemented | Reading copy | [已实现 Provider Adapter 合同](architecture/provider-adapter.zh-CN.md) | 中文阅读版；该副本早于 native-tool send/assemble，以英文 [provider-adapter.md](architecture/provider-adapter.md) 和 [tool-runtime.md](architecture/tool-runtime.md) 为准 |
| Implemented | Implemented contract | [Tool runtime](architecture/tool-runtime.md) | Current Application-owned Step loop, Policy Decide table, four builtin workspace tools, mid-loop ResolveAppend, workspace jail, and exclusions |
| Implemented | Reading copy | [已实现 Tool Runtime 合同](architecture/tool-runtime.zh-CN.md) | 与已实现 Tool Runtime 合同同步的中文语义阅读版 |
| Implemented | Implemented contract | [SQLite canonical EventStore](architecture/sqlite-eventstore.md) | Current pure-Go SQLite adapter: verified open profile, full-shape migrations, append transaction with exact retry, pinned reads, fencing lease primitive, verified backup, OpenReader without taking the runtime lease, and exclusions |
| Implemented | Reading copy | [已实现 SQLite 规范 EventStore 合同](architecture/sqlite-eventstore.zh-CN.md) | 与已实现 SQLite 规范 EventStore 合同同步的中文语义阅读版 |
| Implemented | Implemented contract | [JSONL audit replica](architecture/jsonl-audit-replica.md) | Audit codec v1, chain maintenance in the append transaction, codec-v1 backfill, crash-convergent exporter, consistent export, and eight-step verified import |
| Implemented | Reading copy | [已实现 JSONL 审计副本合同](architecture/jsonl-audit-replica.zh-CN.md) | 与已实现 JSONL 审计副本合同同步的中文语义阅读版 |
| Implemented | Implemented contract | [Runtime Host and crash recovery](architecture/runtime-host.md) | Single host: startup reconciliation with deterministic recovery appends, bounded heartbeat with fencing reaction, matching-pair lease release, exporter ownership |
| Implemented | Reading copy | [已实现 Runtime Host 与崩溃恢复合同](architecture/runtime-host.zh-CN.md) | 与已实现 Runtime Host 与崩溃恢复合同同步的中文语义阅读版 |
| Implemented | Implemented contract | [Composition root](architecture/composition-root.md) | Current single tested composition root, system Clock and IDGenerator, enforced adapter-import owner, lifecycle and shutdown, and exclusions |
| Implemented | Reading copy | [已实现组合根合同](architecture/composition-root.zh-CN.md) | 与已实现组合根合同同步的中文语义阅读版 |
| Implemented | Implemented contract | [ACP v1 adapter](architecture/acp-v1.md) | Current ACP v1 JSON-RPC adapter: initialize, session/new/load/prompt/cancel, live and load tool cards, clip bounds, workspace admission, fail-closed permission slot, keyless in-memory duplex |
| Implemented | Reading copy | [已实现 ACP v1 Adapter 合同](architecture/acp-v1.zh-CN.md) | 与已实现 ACP v1 Adapter 合同同步的中文语义阅读版 |
| Implemented | Implemented contract | [Session transcript](architecture/session-transcript.md) | Current `och.session.transcript` JSONL: snapshot, fact catalog, complete trailer, `WriteSession`, `och export-session`, and exclusions |
| Implemented | Reading copy | [已实现会话转录合同](architecture/session-transcript.zh-CN.md) | 与已实现会话转录合同同步的中文语义阅读版；以英文 [session-transcript.md](architecture/session-transcript.md) 为准 |
| Complete | Evidence ledger | [EventStore v2 completion evidence / EventStore v2 完成证据](architecture/eventstore-v2-evidence.md) | Auditable Task 1–9 commits, verification commands, benchmark sample, and deferred blockers |
| Complete | Evidence ledger | [Engine vertical slice completion evidence / Engine 纵切完成证据](architecture/engine-vertical-slice-evidence.md) | Auditable Task 1–10 commits, architecture gates, verification commands, and deferred GA blockers |
| Complete | Evidence ledger | [Provider adapter completion evidence / Provider Adapter 完成证据](architecture/provider-adapter-evidence.md) | Auditable PR 1–5 commits, keyless verification commands, and deferred GA blockers |
| Complete | Evidence ledger | [Tool runtime completion evidence / Tool Runtime 完成证据](architecture/tool-runtime-evidence.md) | Auditable execute-plan PRs 1–9, keyless verification commands, and deferred GA blockers |
| Complete | Evidence ledger | [SQLite canonical EventStore completion evidence / SQLite 规范 EventStore 完成证据](architecture/sqlite-eventstore-evidence.md) | Auditable five-task commits, dependency inventory, benchmarks, verification evidence, and deferred GA blockers |
| Complete | Evidence ledger | [JSONL audit replica completion evidence / JSONL 审计副本完成证据](architecture/jsonl-audit-replica-evidence.md) | Auditable task commits, publication and import matrices, benchmarks, and deferred GA blockers |
| Complete | Evidence ledger | [Runtime Host completion evidence / Runtime Host 完成证据](architecture/runtime-host-evidence.md) | Auditable task commits, reconciliation and heartbeat matrices, lifecycle evidence, and deferred GA blockers |
| Complete | Evidence ledger | [Composition root completion evidence / 组合根完成证据](architecture/composition-root-evidence.md) | Auditable five-task commits, verification commands, five findings including one open port ambiguity, and deferred GA blockers |
| Complete | Evidence ledger | [ACP v1 adapter completion evidence / ACP v1 Adapter 完成证据](architecture/acp-v1-evidence.md) | Adapter, composition ServeACP, Approver slot, keyless verification, and deferred v2/resume/memory |
| Complete | Evidence ledger | [Conversation and session transcript completion evidence / 对话面与会话转录完成证据](architecture/conversation-and-transcript-evidence.md) | Auditable PRs 1–8, mapping-table tests, golden JSONL hashes, OpenReader vs live lease, and exclusions |
| Complete | Evidence ledger | [Session transcript completion evidence / 会话转录完成证据](architecture/session-transcript-evidence.md) | Stem ledger for the session-transcript contract; combined PRs 1–8 live in the conversation-and-transcript ledger |
| Complete | Evidence ledger | [ACP session lifecycle (Slice B) completion evidence / ACP 会话生命周期（切片 B）完成证据](architecture/acp-session-lifecycle-evidence.md) | Auditable Task 1–7 commits across domain, application, SQLite, ACP, and transcript; mapping-table tests; golden hash; verification commands; and exclusions |
| Accepted | Normative design | [Industrial Engine vertical slice](superpowers/specs/2026-08-12-engine-vertical-slice-design.md) | Application/Engine boundary, formal ports, deterministic adapters, atomic event flow, failure semantics, and acceptance criteria |
| Accepted | Reading copy | [工业级 Engine 最小纵切](superpowers/specs/2026-08-12-engine-vertical-slice-design.zh-CN.md) | Chinese synchronized reading copy of the Engine design |
| Accepted | Normative design | [Production Runtime persistence, recovery, and client boundary](superpowers/specs/2026-08-13-runtime-persistence-recovery-client-design.md) | SQLite canonical events, exact append retry, audit replica, fencing, crash recovery, ACP boundary, resource limits, and staged delivery |
| Accepted | Reading copy | [生产 Runtime 持久化、恢复与客户端边界](superpowers/specs/2026-08-13-runtime-persistence-recovery-client-design.zh-CN.md) | 与生产 Runtime 持久化、恢复和客户端边界规范完整同步的中文阅读版 |
| Accepted | Normative design | [EventStore v2 contract migration](superpowers/specs/2026-08-13-eventstore-v2-contract-design.md) | Focused Slice 1 identities, exact append, pagination, admission, compact aggregate, unknown-outcome, and verification contract |
| Accepted | Reading copy | [EventStore v2 Contract Migration 中文阅读版](superpowers/specs/2026-08-13-eventstore-v2-contract-design.zh-CN.md) | 与 EventStore v2 聚焦规范完整同步的中文阅读版 |
| Accepted | Normative design | [Provider contract and first real adapter](superpowers/specs/2026-08-15-provider-adapter-design.md) | `engine.Model` HTTP adapter, Chat Completions SSE, capability profiles, classified failures, reconstructable request/usage facts |
| Accepted | Reading copy | [Provider 合同与第一个真实 Adapter](superpowers/specs/2026-08-15-provider-adapter-design.zh-CN.md) | 与 Provider 设计同步的中文阅读版：行业对照、是否自研、关键决策与五步 PR Plan |
| Accepted | Normative design | [Tool Runtime, Policy, and minimal workspace tools](superpowers/specs/2026-08-16-tool-runtime-policy-design.md) | Application-owned Step loop, pure Policy Decide, four builtin tools, mid-loop EventStore resolve, no plugin kernel |
| Accepted | Reading copy | [Tool Runtime、Policy 与最小工作区工具](superpowers/specs/2026-08-16-tool-runtime-policy-design.zh-CN.md) | 与 Tool/Policy 设计同步的中文阅读版：关键决策、行业对照、管线与 PR Plan |
| Accepted | Normative design | [SQLite Canonical EventStore (Slice 2)](superpowers/specs/2026-08-16-sqlite-canonical-eventstore-design.md) | Pure-Go SQLite adapter behind the EventStore v2 port: full-shape migrations, append transaction, fencing lease primitive, pinned reads, backup, and fault evidence |
| Accepted | Reading copy | [SQLite 规范 EventStore（Slice 2）中文阅读版](superpowers/specs/2026-08-16-sqlite-canonical-eventstore-design.zh-CN.md) | 与 SQLite 规范 EventStore 聚焦设计完整同步的中文阅读版 |
| Accepted | Normative design | [Exec sandboxing and resource quotas](superpowers/specs/2026-08-30-exec-sandboxing-resource-quotas-design.md) | Bubblewrap + cgroup v2 memory quota on Linux, Seatbelt + RLIMIT_AS on macOS, fail-closed availability check at composition time, structured per-effect enforcement reporting; Windows sandboxing explicitly deferred, accepted as a near-term non-priority |
| Accepted | Reading copy | [Exec 沙箱与资源配额设计（中文摘要）](superpowers/specs/2026-08-30-exec-sandboxing-resource-quotas-design.zh-CN.md) | 与 exec 沙箱与资源配额设计同步的中文摘要阅读版 |
| Accepted | Normative design | [Composition Root and Cross-Adapter Conformance (Slice 5)](superpowers/specs/2026-08-19-composition-root-conformance-design.md) | Single tested composition root, enforced adapter-import owner, scenario suite over the durable store, transport-neutral model contract, and one end-to-end assembly test |
| Accepted | Reading copy | [组合根与跨 Adapter 一致性（Slice 5）中文阅读版](superpowers/specs/2026-08-19-composition-root-conformance-design.zh-CN.md) | 与组合根与跨 Adapter 一致性聚焦设计完整同步的中文阅读版 |
| Accepted | Normative design | [ACP v1 adapter](superpowers/specs/2026-08-22-acp-v1-adapter-design.md) | ACP v1 transport adapter: v1-only, tools.Slot approver seam, three-state stop-reason mapping, owned NDJSON codec, load-as-replay |
| Accepted | Reading copy | [ACP v1 Adapter 中文阅读版](superpowers/specs/2026-08-22-acp-v1-adapter-design.zh-CN.md) | 与 ACP v1 Adapter 聚焦设计同步的中文阅读版 |
| Implemented plan | Plan | [Composition root and conformance implementation plan](superpowers/plans/2026-08-19-composition-root-conformance.md) | Frozen five-task sequence for Slice 5; completion claims require an evidence ledger, not checkbox state |
| Implemented plan | Reading copy | [组合根与跨 Adapter 一致性实施计划中文阅读版](superpowers/plans/2026-08-19-composition-root-conformance.zh-CN.md) | 与组合根与跨 Adapter 一致性实施计划完整同步的中文执行阅读版 |
| Accepted | Normative design | [JSONL Audit Replica and Import (Slice 3)](superpowers/specs/2026-08-16-jsonl-audit-replica-design.md) | Audit codec v1, chain maintenance in the append transaction, codec-v1 backfill, crash-convergent exporter, consistent export, and eight-step verified import |
| Accepted | Reading copy | [JSONL 审计副本与导入（Slice 3）中文阅读版](superpowers/specs/2026-08-16-jsonl-audit-replica-design.zh-CN.md) | 与 JSONL 审计副本聚焦设计完整同步的中文阅读版 |
| Accepted | Normative design | [Runtime Host and Crash Recovery (Slice 4)](superpowers/specs/2026-08-16-runtime-host-recovery-design.md) | Single Runtime Host: startup reconciliation with deterministic recovery appends, bounded heartbeat with fencing reaction, graceful shutdown, exporter ownership |
| Accepted | Reading copy | [Runtime Host 与崩溃恢复（Slice 4）中文阅读版](superpowers/specs/2026-08-16-runtime-host-recovery-design.zh-CN.md) | 与 Runtime Host 聚焦设计完整同步的中文阅读版 |
| Implemented plan | Plan | [EventStore v2 contract migration implementation plan](superpowers/plans/2026-08-13-eventstore-v2-contract.md) | Frozen nine-task sequence; completion claims are backed by the evidence ledger, not checkbox state |
| Implemented plan | Reading copy | [EventStore v2 Contract Migration 实施计划中文阅读版](superpowers/plans/2026-08-13-eventstore-v2-contract.zh-CN.md) | 与 EventStore v2 实施计划完整同步的中文执行阅读版 |
| Implemented plan | Plan | [Engine vertical slice implementation plan](superpowers/plans/2026-08-12-engine-vertical-slice.md) | Frozen ten-task implementation sequence; completion claims are backed by the evidence ledger, not checkbox state |
| Implemented plan | Reading copy | [Engine 纵切实施计划中文阅读版](superpowers/plans/2026-08-12-engine-vertical-slice.zh-CN.md) | Chinese synchronized reading copy of the implemented sequence; see the evidence ledger for completion proof |
| Implemented plan | Plan | [SQLite canonical EventStore implementation plan](superpowers/plans/2026-08-16-sqlite-canonical-eventstore.md) | Frozen five-task sequence for the Slice 2 adapter; completion claims are backed by the evidence ledger, not checkbox state |
| Implemented plan | Reading copy | [SQLite 规范 EventStore 实施计划中文阅读版](superpowers/plans/2026-08-16-sqlite-canonical-eventstore.zh-CN.md) | 与 SQLite 规范 EventStore 实施计划完整同步的中文执行阅读版 |
| Implemented plan | Plan | [JSONL audit replica implementation plan](superpowers/plans/2026-08-16-jsonl-audit-replica.md) | Frozen five-task sequence for the Slice 3 audit chain, exporter, consistent export, and import |
| Implemented plan | Reading copy | [JSONL 审计副本实施计划中文阅读版](superpowers/plans/2026-08-16-jsonl-audit-replica.zh-CN.md) | 与 JSONL 审计副本实施计划完整同步的中文执行阅读版 |
| Implemented plan | Plan | [Runtime Host and recovery implementation plan](superpowers/plans/2026-08-16-runtime-host-recovery.md) | Frozen five-task sequence for the Slice 4 host; Tasks 1–3 are independent of Slice 3 |
| Implemented plan | Reading copy | [Runtime Host 与恢复实施计划中文阅读版](superpowers/plans/2026-08-16-runtime-host-recovery.zh-CN.md) | 与 Runtime Host 与恢复实施计划完整同步的中文执行阅读版 |
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
| Complete | Research evidence | [Client surface reuse and post-Slice-B security sequencing](research/architecture-gates/2026-08-30-client-surface-and-security-sequencing.md) | Reaffirms the 2026-08-15 rejection of a DeepSeek-Harness-style web-primary/ACP-automation-only client; orders exec sandboxing and resource quotas before a minimal ACP-native client |
| Complete | Reading copy | [客户端界面复用与安全加固顺序](research/architecture-gates/2026-08-30-client-surface-and-security-sequencing.zh-CN.md) | 与客户端界面复用及安全加固顺序决策完整同步的中文证据记录 |
| Complete | Research evidence | [Exec sandboxing and resource quotas architecture gate](research/architecture-gates/2026-08-30-exec-sandboxing-and-resource-quotas.md) | Re-verified, pinned-commit comparison of Codex, Kimi Code, Grok Build, Pi, Maka, and DeepSeek Harness sandboxing and resource-quota mechanisms; open questions for the design that follows |
| Complete | Reading copy | [exec 沙箱与资源配额架构调研门](research/architecture-gates/2026-08-30-exec-sandboxing-and-resource-quotas.zh-CN.md) | 与 exec 沙箱与资源配额架构调研门完整同步的中文证据记录 |
| Complete | Research evidence | [SQLite canonical EventStore architecture gate](research/architecture-gates/2026-08-16-sqlite-canonical-eventstore.md) | Slice 2 re-verification of DeepSeek Harness, Codex, Kimi Code, Grok Build, Pi, and Maka establishing row-per-event, WAL, migration, fencing, and fail-closed contracts |
| Complete | Reading copy | [SQLite 规范 EventStore 架构门中文阅读版](research/architecture-gates/2026-08-16-sqlite-canonical-eventstore.zh-CN.md) | 与 SQLite 规范 EventStore 架构门完整同步的中文证据记录 |
| Complete | Research evidence | [Composition root and conformance architecture gate](research/architecture-gates/2026-08-19-composition-root-and-conformance.md) | Verified assembly and contract-test topologies of DeepSeek Harness, Codex, Pi, Kimi Code, Grok Build, and Maka; establishes integration closure before ACP, one enforced composition owner, and the model-contract split |
| Complete | Reading copy | [组合根与跨 Adapter 一致性架构门中文阅读版](research/architecture-gates/2026-08-19-composition-root-and-conformance.zh-CN.md) | 与组合根与跨 Adapter 一致性架构门完整同步的中文证据记录 |
| Complete | Research evidence | [ACP v1 adapter architecture gate](research/architecture-gates/2026-08-22-acp-v1-adapter.md) | Primary-source verification of the ACP specification and the agent-side adapters of Codex, Kimi Code, and DeepSeek Harness; establishes v1 targeting, adapter placement behind existing ports, replay-based session load, turn-ended settlement, and fail-closed permission bridging |
| Complete | Reading copy | [ACP v1 Adapter 架构门中文阅读版](research/architecture-gates/2026-08-22-acp-v1-adapter.zh-CN.md) | 与 ACP v1 Adapter 架构门完整同步的中文证据记录 |
| Complete | Research evidence | [Runtime Host and recovery architecture gate](research/architecture-gates/2026-08-16-runtime-host-recovery.md) | Slice 4 re-verification establishing fenced-lease confirmation (Pi), fencing theory (Kleppmann), cold-only repair, and reconcile-before-command ordering |
| Complete | Reading copy | [Runtime Host 与恢复架构门中文阅读版](research/architecture-gates/2026-08-16-runtime-host-recovery.zh-CN.md) | 与 Runtime Host 与恢复架构门完整同步的中文证据记录 |
| Complete | Research evidence | [JSONL audit replica architecture gate](research/architecture-gates/2026-08-16-jsonl-audit-replica.md) | Slice 3 re-verification establishing transactional-outbox confirmation, verify-then-publish ordering, digest-always framing, and verified-import boundaries |
| Complete | Reading copy | [JSONL 审计副本架构门中文阅读版](research/architecture-gates/2026-08-16-jsonl-audit-replica.zh-CN.md) | 与 JSONL 审计副本架构门完整同步的中文证据记录 |
| Complete | Plan | [Domain implementation plan](superpowers/plans/2026-08-11-domain-events-state-machine.md) | Completed Task 1–8 implementation sequence |
| Complete | Reading copy | [领域实施计划中文阅读版](superpowers/plans/2026-08-11-domain-events-state-machine.zh-CN.md) | Chinese synchronized reading copy of the completed plan |

## Milestone status

1. **Harness domain, events, Session/Turn state machine** — implemented.
2. **Industrial Go Engine executable vertical slice** — implemented and verified.
3. **EventStore v2 contract migration** — implemented and verified; required before a SQLite adapter, not a replacement for Provider/Tool.
4. **Provider contract and first real provider** — designed and implemented; not GA. First adapter is a thin OpenAI-compatible Chat Completions SSE client behind `engine.Model`, not a vendor SDK or plugin kernel.
5. **Tool Runtime, Policy, and minimal workspace tools** — designed, implemented, and verified; not GA. Application-owned Step loop, pure Policy Decide, and four builtin workspace tools behind ports; not a plugin kernel.
6. **ACP v1 adapter and conformance** — designed, implemented, and verified; not GA. Transport adapter over the Slice 5 assembly: initialize, session/new/load/prompt/cancel, live and load tool cards, workspace admission, fail-closed permission slot, keyless in-memory NDJSON. Session lifecycle (Slice B) adds capability-gated session/list/resume/close/delete, a duplex wire-state machine, and the logical `session.deleted` domain fact with a matching SQLite session head catalog (migration 4, keyset `ListSessionHeads`) and transcript fact. Session transcript JSONL (`och export-session`) is implemented as a separate EventStore projection. Catalog-backed `RunTurn` prefixes prior turns from the event log (`projectPriorTurns`). No v2, no independent Context Engine or token-aware compaction.
7. **TypeScript TUI client** — cross-cutting boundary accepted; focused implementation specification not written yet. Any client, TUI or otherwise, is an ACP client; see the [client surface and security sequencing decision](research/architecture-gates/2026-08-30-client-surface-and-security-sequencing.md), which orders exec sandboxing and resource quotas (`SECURITY.md`'s "Not enforced" list) before a minimal ACP-native client, and both before broader UI investment.
8. **Context Engine, checkpoint, and recovery** — production persistence and crash-recovery boundary accepted; the persistence track's Slices 2–4 (SQLite canonical EventStore, JSONL audit replica with export/import, and Runtime Host with crash recovery) are designed, implemented, and verified, not GA; Slice 5 (composition root and cross-adapter conformance) is designed, implemented, and verified, not GA; ACP, TUI, and the Context Engine itself remain undesigned.
9. **MCP client adapter** — not designed yet.
10. **Scenario evaluation, benchmarks, and OpenTelemetry** — not designed yet.
11. **Open-source release, governance, and ecosystem documentation** — not designed yet.

## Executable documentation rules

Several rules below were prose, and prose drifted: the root README described
three landed slices as unimplemented because nothing required it to agree with
this file. The rules that can be checked without judgement are now tests in
`internal/docsguard`, run by `go test ./...` like any other gate:

| Gate | Rule it enforces |
| --- | --- |
| `TestRelativeLinksResolve` | Every relative Markdown link in `README.md`, `SECURITY.md`, and `docs/` resolves to a file |
| `TestAuthorityTableTargetsExist` | Every document named in the authority table above exists |
| `TestImplementedContractsAppearInRootReadme` | Every implemented contract listed above is referenced from the root README |
| `TestReadingCopiesHaveANormativeSource` | Every `*.zh-CN.md` has an English source beside it |
| `TestReadingCopiesNameTheirNormativeSource` | Every reading copy names the document that wins when the copies diverge |
| `TestEveryImplementedContractHasEvidence` | Every implemented contract has an evidence ledger, with exemptions named in the test rather than left implicit |

Two further gates run outside the pull-request path, in the nightly lane:
`determinism` repeats the whole race suite, because a single green run only
samples it and two flaky tests reached `main` that way;
`TestExternalCitationsResolve` follows every cited URL, because citations rot
— `badlogic/pi-mono`, cited by nine documents, now redirects elsewhere. Dead
citations are recorded in the test with what was found and when, never
deleted.

A rule that cannot be checked without judgement stays prose and stays below.
Synchronization of translated *content* is one of those: the gates prove a
reading copy exists and declares its authority, not that it is current.

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
8. A gate cites a reference project by repository and commit, and states the
   date it was observed. `scripts/fetch-reference.sh <owner/repo> <sha>`
   fetches a shallow, pinned, gitignored checkout under `.reference/` so the
   citation can be re-derived and the subsystem can be read rather than
   guessed. The checkout is disposable by design: rule 7 requires each later
   gate to re-verify at the then-current state, so no reference is kept
   tracked or long-lived. Nothing under `.reference/` may be copied into this
   repository.
