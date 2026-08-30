# Client Surface Reuse and Post-Slice-B Sequencing Decision

**Status:** Complete research evidence

**Date:** 2026-08-30

**Scope:** Record the decision on reusing an external product's web UI as
this project's ACP client, and the delivery sequencing accepted after ACP
session lifecycle (Slice B), following a discussion prompted by DeepSeek
Harness's web UI and its agent-trajectory presentation.

This document is a sequencing decision reached in conversation, not a fresh
primary-source comparison. It cites only the existing 2026-08-15 DeepSeek
Harness gate; no external repository was fetched or re-verified to produce
it. It does not implement anything, does not authorize copying DeepSeek
Harness UI code, and is not itself the architecture gate required before
implementing exec sandboxing, resource quotas, or a TUI/web ACP client —
each of those remains an accepted-but-undesigned boundary that needs its own
gate re-verifying then-current primary sources before an implementation
plan, per Documentation rule 7 in `docs/README.md`.

English is normative. The Chinese file is a synchronized reading copy.

## Question

Should Open Code Harness reuse DeepSeek Harness's web UI as its client
surface, to avoid building one, given DeepSeek Harness's agent-trajectory
presentation is considered strong?

## Finding: this repeats an already-rejected shape

The 2026-08-15 DeepSeek Harness gate
([`2026-08-15-deepseek-harness-and-roadmap.md`](2026-08-15-deepseek-harness-and-roadmap.md))
already evaluated this and rejected it explicitly, as Rejected shape 3:

> Web UI as the primary product surface, with ACP reduced to
> automation-only. ACP v1 remains the public client boundary; the TUI is an
> ACP client.

DeepSeek Harness's own architecture, as observed at that gate, treats its
web UI as the primary surface and ACP as a secondary, automation-only
interface — the inverse of this project's charter, which fixes ACP as the
sole public client boundary specifically to keep the harness model-neutral
and UI-neutral (see Milestone 6 in `docs/README.md` and the ACP v1 design).
Reusing DeepSeek Harness's web frontend as-is would require either:

1. the harness speaking DeepSeek Harness's own protocol behind that UI,
   reinstating the rejected shape; or
2. forking and rewiring DeepSeek Harness's frontend to consume ACP
   JSON-RPC instead of its native data layer — real, ongoing integration
   work against a project DeepSeek Harness itself labels a "developer
   preview" with no compatibility guarantee, not an effort savings.

Neither option is "using their UI instead of building one" in the sense
the question intended; both are new integration surfaces to build and
maintain.

## Decision

1. Do not integrate or fork DeepSeek Harness's web UI. Any client this
   project builds is an ACP client, full stop — this reaffirms the
   2026-08-15 decision rather than reopening it.
2. The properties that make DeepSeek Harness's trajectory view compelling —
   log reconstructability, step/turn layering, an explicit tool pipeline —
   were already adopted in principle at the 2026-08-15 gate (its Adopt
   column) and are already reflected in this project's own transcript and
   live-update design: the `session-transcript` JSONL projection, ACP
   `session/update` notifications, and `policy.decision.recorded` audit
   facts. A future client renders trajectory from those existing surfaces,
   not from a foreign data model.
3. Priority after Slice B is **usable and safe**. That names urgency, not
   build order: harden the currently-unenforced security boundaries before
   investing in a client that would widen the audience touching them.
   Concretely, exec sandboxing and resource quotas — both listed under
   "Not enforced" in `SECURITY.md` — come before a minimal ACP client; the
   client comes before broader UI polish or a second client surface.
4. Both the security-hardening subsystem and the minimal ACP client remain
   **accepted but undesigned** boundaries as of this decision (see
   Milestones 6–7 in `docs/README.md`). Each requires its own architecture
   gate before an implementation plan: for sandboxing, re-verify how
   current reference projects (Codex, Grok Build, Kimi Code, and others
   named in prior gates) bound tool execution today, not their 2026-08
   snapshots; for the client, identify whether a genuinely ACP-native
   reference client already exists worth studying, since ACP is a
   published protocol with its own ecosystem, not DeepSeek Harness's
   web app. This document does not substitute for either gate.

## Sequencing

1. Architecture gate, design, and implementation: exec sandboxing and
   resource quotas. Extends `internal/harness/adapters/localexec`;
   `SECURITY.md`'s "Not enforced" list is the acceptance criteria this
   work must close or explicitly narrow.
2. Architecture gate, design, and implementation: a minimal ACP-native
   client sufficient to send a prompt and render a trajectory view from
   `session/update` notifications and `och export-session` output.
3. Broader UI investment, the MCP client adapter, evaluation/benchmarks,
   and every other milestone still marked "not designed" in
   `docs/README.md` stay ordered after steps 1–2, unless a later gate
   shows one of them blocks work already in flight.

## Evidence limits

- This is a sequencing decision recorded from a conversation, not a
  primary-source comparison; it does not authorize starting
  implementation of exec sandboxing or the ACP client on its own.
- "Trajectory" here means the step/turn/tool-call timeline this project's
  own transcript and live-update design already project from the
  canonical event log — not any DeepSeek Harness UI component.
- DeepSeek Harness remains a developer preview per the 2026-08-15 gate; a
  future gate that revisits it must re-read its then-current state rather
  than reuse this document's characterization.
