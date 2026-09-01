# Context Engine, Evaluation, Observability, and TUI Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-09-01

**Scope:** Four milestones in `docs/README.md`'s milestone status remain
undesigned: milestone 7 (TypeScript TUI client — only staged behind a
Go ACP-native terminal client and a browser trajectory UI, the fuller
TUI itself "remains unspecified"), milestone 8 (Context Engine —
persistence and recovery are implemented, but selection, budgeting, and
compression of model-visible context are undesigned), and milestone 10
(scenario evaluation, benchmarks, and OpenTelemetry — "not designed
yet"). Milestone 9 (MCP client adapter) already has its own gate
([2026-08-30-mcp-client-adapter.md](2026-08-30-mcp-client-adapter.md))
and is out of scope here.

This gate reads how the project's six official comparison-set projects
(`docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md`
§12: Codex, Pi, DeepSeek Harness, Kimi Code, Grok Build, Maka) actually
implement — or deliberately do not implement — each of these four areas,
and records the unscoped capability gaps noticed while reading them. It
does not design or implement anything. Per Documentation rule 1, each of
Context Engine, Evaluation, Observability, and TUI still needs its own
subsystem-specific architecture gate re-verifying then-current primary
sources before a normative design — this document establishes
comparative direction and a sequencing recommendation, the way the
[2026-08-30 client surface and security sequencing
decision](2026-08-30-client-surface-and-security-sequencing.md) did for
exec sandboxing and the ACP-native client, not a substitute for those
future gates.

English is normative. The Chinese file is a synchronized reading copy.

## Comparison set and pinned commits

Per Documentation rule 8, these are the same gitignored `.reference/`
checkouts fetched and read directly, obtained via
`./scripts/fetch-reference.sh --list`. Per Documentation rule 7, no
re-fetch was needed: nothing has moved since the
[2026-08-31 web trajectory UI gate](2026-08-31-web-trajectory-ui.md)
last re-verified this same six-project set at this same state.

| Project | Repository | Commit | Observed | Why fetched |
| --- | --- | --- | --- | --- |
| Codex | `openai/codex` | `a9519cb` | 2026-08-31 | Rust; dedicated `otel` crate, `turn_diff_tracker.rs`, subagent-routed approvals, three distinct compaction strategies (`compact.rs`, `compact_token_budget.rs`, `compact_remote_v2.rs`) |
| Pi | `earendil-works/pi` | `853a80d` | 2026-08-28 | TypeScript; identical HEAD to `pi-mono` below (unexplained, immaterial to this gate, previously noted by the MCP gate); real behavioral eval harness, from-scratch terminal toolkit |
| Pi (agent core source) | `badlogic/pi-mono` | `853a80d` | 2026-08-28 | The `packages/agent`/`packages/tui`/`packages/evals`/`packages/telemetry` source actually read for this gate lives here, not in the `earendil-works/pi` mirror |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb5` | 2026-08-30 | TypeScript/Cordis; richest compaction package split of the six, real OTel log export, no terminal TUI, external-only benchmark convention |
| Kimi Code | `MoonshotAI/kimi-code` | `8f2c60b` | 2026-08-31 | TypeScript; vendors Pi's terminal toolkit directly (`packages/pi-tui`), two-tier trigger/block compaction ratios |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-28 | Rust; deepest compaction implementation (intra/inter/code, Basic vs. DivideAndConquer chunking, two-pass prefire), `fastrace`-wrapped OTLP tracing, `ratatui`-based TUI |
| Maka | `maka-agent/maka-agent` | `ef94235` | 2026-08-31 | TypeScript; both a real eval harness (`packages/eval` + Python `harbor/`) and a real long-term-memory layer; separate desktop-React `packages/ui` and `pi-tui`-based terminal `packages/cli` |

## Context Engine

All six projects implement compaction; this project implements none of
it yet (`internal/harness` has no compaction command, event, or budget
concept).

- **Pi** (`pi-mono/packages/agent/src/harness/compaction/compaction.ts`):
  `DEFAULT_COMPACTION_SETTINGS` (lines 158–162) fixes
  `reserveTokens: 16384` and `keepRecentTokens: 20000`;
  `shouldCompact(contextTokens, contextWindow, settings)` (line 247)
  triggers when `contextTokens > contextWindow - settings.reserveTokens`.
  `estimateContextTokens` (line 216) prefers the last real provider
  `usage` block over a character-based estimate when one exists.
  `findValidCutPoints`/`findCutPoint` (lines 312, 374) walk backward from
  the end accumulating `keepRecentTokens`, then snap to the nearest valid
  cut point — a `message` entry whose role is `user`/`assistant`/tool-call
  boundary, never splitting an in-progress tool call/result pair — and
  report whether the cut split an in-progress turn
  (`CutPointResult.isSplitTurn`). Separately,
  `.../session/context.ts`'s `buildSessionContext`/`buildContextEntries`
  (lines 45–100) always collapse history to the entry list starting at the
  most recent `compaction` entry: compaction is a real domain event in the
  session log, and the context sent to the model is a pure projection of
  it, not a second mutable transcript.
- **DeepSeek Harness**
  (`packages/compaction/compaction-basic/src/region.ts`,
  `summarizer.ts`): `selectCompactableRange` (line 100) walks backward
  from the end accumulating a `retainTokens` tail and refuses to select a
  boundary that would split a tool-call/result pair
  (`toolPairingBalancedBefore`/`After`). `compactSurfaceRegion` (line
  154) is a real transaction: it appends `compaction/start` before
  summarization begins, appends `compaction/end` (with an error payload
  on failure) exactly once no matter which stage fails, and rejects a
  summary that turns out no smaller than the token cost of what it
  replaced (`summarizeCompaction`, line 367,
  `framedSummaryTokenCount >= prepared.shadowedRouteTokenCount`).
  `buildSummarizationInput` (line 508) replays the session's own last
  request header (system prompt, tool schemas) as a genuine prefix before
  the compaction instruction specifically so the summarization call reuses
  the provider's prompt cache. `summarizer.ts`'s `COMPACTION_INSTRUCTION`
  (lines 31–66) is a hand-written, structurally fixed Markdown checkpoint
  format ("Primary Request and Intent", "Files and Code", "Pending Jobs",
  etc.), delivered as the *final user message* rather than a separate
  system prompt, for the same cache-reuse reason.
- **Kimi Code**
  (`packages/agent-core-v2/src/agent/fullCompaction/strategy.ts`):
  `DEFAULT_COMPACTION_CONFIG` (lines 18–27) is a genuinely different
  shape from Pi's single reserve-token threshold — two independent
  ratios, `triggerRatio: 0.85` (when to start) and `blockRatio: 0.85`
  (when to force-block further requests), plus
  `reservedContextSize: 50_000`, `maxOverflowCompactionAttempts: 3`, and
  `minOverflowReductionRatio: 0.05` (a compaction pass that doesn't shrink
  the transcript by at least 5% is treated as a failed attempt).
  `fullCompactionService.ts` and `compactionOps.ts` implement the actual
  pass; `compaction-instruction.md` is a dedicated prompt file, not an
  inline string.
- **Grok Build**
  (`crates/common/xai-grok-compaction/src/{intra,inter,code}_compaction/`):
  the deepest implementation of the six. `intra_compaction/trigger.rs`
  (lines 117–160) computes `threshold = context_window *
  trigger_threshold_percent / 100` (default 85%) and a separate
  `target_threshold_percent` (default 50%) the pass compacts *down to*,
  not just up to. `inter_compaction/compact.rs`'s header (lines 1–10)
  documents one shared chunked pipeline for two strategies —
  `Basic` (unbounded chunk budget, exactly one chunk) and
  `DivideAndConquer` (bounded `dnc_chunk_token_limit`, N chunks) — differing
  only in per-chunk budget. `code_compaction/compact.rs`'s header (lines
  1–16) is explicit that this is a *third*, separate strategy:
  "grok-build does not select a tail to keep; it summarizes the whole
  conversation and rebuilds a fresh history from scratch," pipelined as
  `build prompt → sample (retry + classify) → clean → assemble`. A
  distinct two-pass "prefire" flow lives in the product host
  (`crates/codegen/xai-grok-shell/src/session/compaction_two_pass_prefire_helper_tests.rs`),
  confirming the crate's own claim that per-harness triggering and
  transport stay out of the shared crate.
- **Codex** (`codex-rs/core/src/compact*.rs`): three distinct, explicitly
  parallel strategies rather than one algorithm with modes.
  `compact_token_budget.rs`'s `run_manual_compact_task`/
  `run_inline_auto_compact_task` (lines 26–56) doc comments state plainly:
  "Token-budget compaction skips model/server summarization and installs a
  fresh context window instead. It is still modeled as compaction so
  compact hooks and `ContextCompaction` turn items observe the same
  lifecycle as local or remote compaction" — i.e., a no-summarization
  reset is a first-class compaction strategy, not a fallback path bolted
  onto the summarizing ones. `compact_remote_v2.rs` (imports at lines
  1–20) is the model/server-assisted summarization path, wired through
  `compact_model_fallback::should_retry_with_current_model`, and
  `codex_analytics::CompactionTrigger` distinguishes `Manual` from
  automatic triggers explicitly in the type system.
- **Maka** (`packages/runtime/src/history-compact*.ts`,
  `compaction-boundary.ts`): the second-deepest implementation, with
  dedicated checkpoint (771 lines), ledger, summarizer,
  summary-validation, and checkpoint-coordinator files beyond the core
  `history-compaction.ts` (573 lines).
  `compaction-boundary.ts` (lines 24–30) types a
  `CompactionBoundaryKind` taxonomy (`historyCompact` /
  `staleToolResultPrune` / `activeToolResultPrune`) and a
  `CompactionDecisionKind` of `unchanged` / `replaced` / `failedOpen` —
  an explicit fail-*open* semantics for a compaction decision that could
  not complete, distinct from every other project read here, none of
  which name a fail-open path for this specifically.

**Synthesis:** every implementation shares the same three moves — decide
whether to compact from a token estimate against the model's context
window, find a tool-call-safe cut point, and replace the cut region with
a model-generated summary appended as a normal message — but converge on
nothing beyond that. Trigger math ranges from a single reserved-token
margin (Pi) to independent trigger/block ratios (Kimi Code) to a
trigger-plus-target pair (Grok Build). Two projects (Codex, Grok Build)
treat "no-summarization reset" and "summarize-the-whole-thing" as
distinct named strategies beside tail-preserving compaction, not two ends
of one spectrum. Compaction-as-a-logged-transaction (DeepSeek Harness's
`compaction/start`/`compaction/end` bracket, Pi's `compaction` session
entry, Grok Build's `ContextCompaction` turn item) is the closest thing
to a cross-project consensus, and is also the shape this project's own
event-sourced Domain layer is naturally suited to.

## Evaluation

Split precedent: two of six ship a real in-repo scenario/behavioral eval
harness; three have no eval or bench package at all (confirmed, not
merely unsearched); one has an external convention only.

- **Pi** (`packages/evals`): `README.md` (lines 1–4) states the package
  adapts a real `AgentSession` to the third-party `vitest-evals` library
  and runs it "in isolated temporary project and agent directories."
  `src/pi-harness.ts`'s `createPiCodingAgentHarness` (line 246) is the
  harness constructor bound one-per-suite. Each run writes a gitignored
  `.eval/` artifact directory; `runs.jsonl` indexes completed runs
  alongside their native Pi session JSONL under `sessions/` (README
  lines 32–34) — the harness's own real session format is the evaluation
  artifact, not a synthetic transcript format invented for eval alone.
- **Maka** (`packages/eval` + `harbor/`): `README.md` (lines 20–24) states
  the package "owns experiment semantics. It does not execute Maka or
  construct Runtime objects," and diagrams `Experiment → Cells → Attempts
  → Results` with a separate `Runtime Host` executing the actual agent —
  eval orchestration is deliberately decoupled from the harness runtime it
  measures. `packages/eval/harbor/` is a parallel Python layer
  (`eval_framework.py`, `run_trial.py`, `egress_filter.py`, an
  egress-proxy Docker compose file) implementing sandboxed trial execution
  with network egress control. `packages/eval/experiments/` contains real,
  checked-in comparison runs, including
  `terminal-bench-2.1-deepseek-v4-flash-maka-vs-deepseek-harness.json` — a
  published head-to-head against another one of this project's own six
  reference projects.
- **DeepSeek Harness** (checked): `BENCHMARK.md` at the repository root is
  three lines pointing at running the Python SDK's `jsonrpc-agent`
  variant manually with separate workspaces and session IDs — no in-repo
  harness, runner, or scoring package exists.
- **Codex, Kimi Code, Grok Build** (checked negatives): no scenario/quality
  eval package exists in any of the three. What each does have and is not
  this — Codex's `codex-rs/cli/e2e_benches/codex_help.rs` (a 20-line
  `divan` micro-benchmark timing `codex --help` process startup) and
  Kimi Code's `packages/minidb/bench` and Grok Build's several
  `crates/codegen/*/benches` directories — are Rust/JS performance
  micro-benchmarks of specific subsystems, not agent-quality or
  scenario-correctness evaluation. This project's own future eval
  milestone is closer to what Pi and Maka ship than to what these three
  call "bench."

**Synthesis:** Pi's model — a real harness object driving real sessions,
model-backed judging via an external library, artifacts keyed by the
harness's own native session format — is the nearest fit to this
project's own charter (§6.9: "Eval Runner 直接驱动应用层或 headless
Engine，不依赖 TUI"). Maka's explicit Experiment/Runtime-Host separation
is a second usable precedent specifically for keeping eval orchestration
decoupled from `internal/harness/application`, mirroring this project's
own port-based architecture.

## Observability

Genuinely split: two of six wire real OpenTelemetry tracing; the rest
either use OTel for logs only, roll their own typed span schema, do
product analytics rather than tracing, or record cost/usage domain facts
instead.

- **Codex** (`codex-rs/otel/`): a dedicated crate.
  `Cargo.toml:379–383,478` pins `opentelemetry = "0.31.0"`,
  `opentelemetry-otlp`, `opentelemetry_sdk`,
  `opentelemetry-semantic-conventions`, and `tracing-opentelemetry =
  "0.32.0"` as exact versions, the same pinning discipline this project
  already applies to its own dependencies. `otel/README.md` (lines 1–9)
  describes provider wiring for log/trace/metric exporters plus
  "session-scoped business event emission via `codex_otel::SessionTelemetry`."
  `core/src/otel_init.rs` exposes `build_provider` (line 16),
  `record_process_start` (line 97), and `install_sqlite_telemetry` (line
  104) as the composition-time integration points.
- **Grok Build** (`crates/common/xai-tracing/src/fastrace.rs`): wraps the
  lighter `fastrace` crate rather than the `opentelemetry` crate directly.
  `init_fastrace` (line 12) constructs a `fastrace_opentelemetry::OpenTelemetryReporter`
  (line 27) that still exports over real OTLP. `current_trace_id` (line
  38) and `enter_span_with_traceparent` (line 46) encode/decode W3C
  `traceparent` headers, and `TraceparentMiddleware` (line 69) propagates
  them across an HTTP client — real distributed-trace-context propagation,
  not just local spans.
- **DeepSeek Harness**
  (`packages/session/session-telemetry-otel/src/index.ts`): composes the
  OTel JS SDK for **logs only** — `LoggerProvider` +
  `BatchLogRecordProcessor` + an OTLP/HTTP log exporter (module doc
  comment, lines 4–6; imports, lines 30–32) — not traces or spans. A
  `SessionTelemetryMode` enum (lines 45–47) of `FULL` / `FEEDBACK_ONLY` /
  `DISABLED`, defaulting to `DISABLED` (line 51,
  `DEFAULT_TELEMETRY_MODE`), gates what leaves the machine at all; a
  disabled-mode warning string (line 53) states plainly that nothing is
  shared and feedback stays local.
- **Pi** (`packages/telemetry`, `@earendil-works/pi-telemetry`): its own
  typed event/span schema (`src/index.ts`'s `AttributeValue` union, line
  1; `src/memory.ts`'s `RecordedTelemetryEvent`/`RecordedTelemetrySpan`,
  lines 11–20), with an in-memory recorder as the reference
  implementation. This is span-shaped (names, attributes, events) but is
  not OpenTelemetry — no OTLP exporter, no `opentelemetry` crate/package
  dependency found.
- **Kimi Code** (`packages/telemetry/src/client.ts`): a queued
  product-analytics event client, not tracing. `TelemetryClient` (line
  29) carries `deviceId`/`sessionId` (lines 33–34) and dispatches through
  an injected `EventSink` (line 31) — the same shape as a product
  analytics SDK (Amplitude/Segment-style), confirmed as a checked
  negative for real span/trace tracing.
- **Maka** (`packages/runtime/src/provider-request-telemetry.ts`): despite
  the name, this is cost/usage accounting, not tracing.
  `ProviderRequestUsage`/`ProviderRequestAttemptRecord` (lines 44–92) and
  `ResolvedModelCallCost` (lines 125–131) record a `ModelCallAttempt`'s
  usage and resolved USD cost per call — structurally the closest analog
  in this comparison set to this project's own
  `PolicyDecisionRecorded` domain event
  (`internal/harness/domain/apply.go:94`,
  `codec.go:210–297`): a structured, durable fact about one decision or
  attempt, recorded in the same event log as everything else, rather than
  an out-of-band trace.

**Synthesis:** this project's charter already names "Observable" as a
required quality attribute (§3.2: "模型调用、工具调用、审批、压缩、重试和错误均有结构化
trace") and already delivers it today through durable, replayable domain
events (`ModelAttempt`, `ToolExecution`, `PolicyDecisionRecorded`) rather
than an external tracing system — structurally closer to what Maka and
DeepSeek Harness's `session-telemetry-otel` do (domain facts, optionally
exported) than to Codex's or Grok Build's live-span tracing. Whether a
future OTel integration is a genuinely separate need (cross-process trace
correlation, latency percentiles, a standard dashboarding surface) or
substantially already met by the existing audit trail is the open
question a design must answer, not something this gate resolves.

## TUI

- **Pi** (`packages/tui`): built from scratch.
  `package.json:47–54` lists exactly two runtime dependencies —
  `get-east-asian-width` and `marked` — and two dev-only dependencies
  (`@xterm/headless`, `chalk`); no `ink`, `blessed`, or terminal-UI
  framework anywhere in the tree. `src/components/` (17 files: `box.ts`,
  `select-list.ts`, `markdown.ts`, `image.ts`, `editor.ts`,
  `scroll-view.ts`, etc.) is a hand-built widget library; `src/` itself
  adds an editor with an undo stack (`undo-stack.ts`), autocomplete, and
  alternate-screen handling. The approval/confirmation UI lives in
  `packages/coding-agent/src/modes/interactive/interactive-mode.ts`
  (`ui.confirm`/`showExtensionConfirm`, lines 2437–2571) — a large
  (6000+ line) file that is this project's nearest functional analog to
  `cmd/acp-client`'s `PermissionPrompter`, but for a locally-run agent
  loop rather than an ACP client.
- **Kimi Code** (`packages/pi-tui`): a confirmed vendored fork, not an
  independent reimplementation. Its own `AGENTS.md` (line 3) states
  directly: "`packages/pi-tui` is a vendored copy of pi-tui from the
  upstream pi-mono project (baseline: upstream 0.80.2, see commit
  `7859b0af`)... all local fixes are applied directly to the source."
  `package.json` names it `@moonshot-ai/pi-tui`; `CHANGELOG.md` (lines
  21, 27) documents periodic re-baselining against upstream
  `@earendil-works/pi-tui` releases while explicitly preserving a named
  list of local patches (narrow-terminal hardening, paste-burst fallback,
  multi-root `@` completion).
- **Grok Build**: adopts the `ratatui` ecosystem rather than hand-rolling
  or forking. `crates/codegen/xai-ratatui-textarea/Cargo.toml:9–10`
  depends on `ratatui`/`ratatui-core` directly;
  `crates/codegen/xai-grok-pager/Cargo.toml:27,88–89` depends on
  `ratatui` plus this project's own `xai-ratatui-textarea` and
  `xai-ratatui-inline` — a layered pager built on top of the adopted
  library rather than a single monolithic TUI crate.
- **DeepSeek Harness** (checked negative): `apps/cli/src/` is 841 lines
  total across six files; `bin.ts` (50 lines) parses argv and dispatches
  to `profile-boot.ts` or `dump-config.ts` — no render loop, no keypress
  handling, no component library of any kind. This confirms the prior
  finding that DeepSeek Harness's primary interactive surface is
  browser-based, not a terminal TUI.
- **Maka**: two entirely separate UI surfaces, more precise than "present
  as a package split." `packages/ui`'s own `README.md` (line 20) states
  it is "Shared UI layer for the Maka **desktop app**... consumed by
  `apps/desktop`'s renderer" — a React 19 (`package.json:32–33`) GUI
  component library (`stories/` includes `session-list-panel.stories.tsx`,
  `model-picker.stories.tsx`, `sandbox-boundary-prompt.stories.tsx`), not
  a terminal renderer at all. The actual terminal client lives in
  `packages/cli`, and it is built on the *same* upstream toolkit as Kimi
  Code: `packages/cli/package.json:24` depends directly on
  `"@earendil-works/pi-tui": "0.84.2"` (the published package, not a
  vendored copy), consumed through files named `pi-tui-runner.ts`,
  `pi-tui-layout.ts`, `pi-tui-transcript-viewer.ts`, `pi-tui-turn.ts`,
  `pi-tui-pickers.ts`, and `pi-tui-mcp-status.ts`.

**Synthesis:** three of six projects (Pi itself, Kimi Code, Maka) build
their terminal client on one shared toolkit lineage — Pi's own
`@earendil-works/pi-tui` — either as its origin, a maintained fork, or a
direct dependency; this is a real convergence, not three independent
choices. Grok Build is the only project adopting an existing terminal-UI
*framework* (`ratatui`) rather than something in Pi's lineage or a
from-scratch build. DeepSeek Harness has no terminal TUI at all. No
project in this set hand-rolls a terminal UI in Go, this project's own
language, so none of the six offers a same-language reference
implementation regardless of which direction (adopt vs. build) a future
design chooses.

## Unscoped gaps noticed

- **Sub-agent/task delegation** — present in four of six, absent from
  Pi (checked: no `subagent`/`sub-agent` hit anywhere in
  `packages/coding-agent/src`). Codex's is narrower than the name
  suggests: `codex-rs/protocol/src/config_types.rs` (lines 177–200)
  routes *approval decisions* (not general task execution) to an
  `auto_review` reviewer, described as "a carefully prompted subagent,"
  with a legacy `guardian_subagent` alias kept for compatibility — a
  subagent used specifically for policy review, not general work
  delegation. Kimi Code's `packages/protocol/src/task.ts` (line 5) types
  `taskKindSchema` as `z.enum(['subagent', 'bash', 'tool'])` — a task can
  itself be routed to a subagent as one of three execution kinds.
  DeepSeek Harness has by far the largest surface: a `packages/subagent/`
  group of ten packages (`subagent`, `subagent-in-process-driver`,
  `subagent-fork-in-process`, `subagent-spawn-in-process`, `subagent-acp`,
  `subagent-codex`, `subagent-claude-code`, `subagent-dsh-sdk`,
  `tool-subagent`, `tool-subagent-control`, `tool-subagent-report`) —
  distinct drivers for in-process, forked, and spawned delegation, plus
  bridges to running Codex or Claude Code itself as a subagent. Grok
  Build has a dedicated `crates/codegen/xai-grok-subagent-resolution`
  crate (contents not read in depth here).
- **Long-term/session memory beyond event replay** — Maka only.
  `packages/storage/src/sqlite-long-term-memory-store.ts` and
  `long-term-memory-store.ts` implement a persistence layer distinct from
  the session/runtime event log; `packages/core/src/long-term-memory.ts`
  and `packages/runtime/src/session-recap.ts` are the consuming layers
  that surface it back into a session. This is a genuinely separate
  concept from this project's own event-sourced replay: replay
  reconstructs one session's own history; Maka's long-term memory
  persists facts *across* sessions.
- **Structured per-turn diff tracking** — Codex only.
  `codex-rs/core/src/turn_diff_tracker.rs` (module-level constants at
  lines 8–19, e.g. `DIFF_TIMEOUT: Duration = Duration::from_millis(100)`
  with a documented pathological-input fallback) tracks file changes
  against the working tree per turn, structured enough to reconstruct a
  real git-style diff rather than relying on a generic exec/patch tool's
  raw output.

## Non-goals cross-check

Read against `docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md`
§4: none of the findings above collide with a stated non-goal.
Sub-agent/task delegation, as read in the four projects that have it, is
local and in-process (an agent's own loop invoking another instance of
itself or a sibling agent within the same runtime) — not the excluded
"A2A、远程 Agent daemon 和分布式多 Agent 协作" (line 24: A2A, remote agent
daemons, and distributed multi-agent collaboration explicitly deferred
past v0, though line 24 also states the architecture must not block
adding it later as an adapter). Local sub-agent delegation and remote
A2A are different questions; this gate does not conflate them, and
neither should a future design. Long-term memory beyond replay is not
"续 personal-harness" or an Obsidian-plugin/knowledge-base product (§4)
— it is a harness-internal persistence concern, not a product
repositioning. Nothing else surfaced above touches any of the other
non-goals (no v0 cloud control plane/teams/billing claim, no
TUI-behavior-as-sole-Engine-verification claim, no speculative
unconsumed extension point proposed).

## Sequencing recommendation

This is a recommendation for later gates and designs to weigh, not a
commitment.

**Context Engine first.** All six reference projects implement it, more
of them in real depth (Grok Build, Maka, DeepSeek Harness) than any
other area this gate covers, and it is a structural prerequisite for the
other three: Evaluation needs something to regression-test beyond what
already exists, Observability's most interesting spans are compaction
decisions themselves, and a future TUI needs to render compaction/summary
events as part of the trajectory. **Evaluation second**, immediately
after — Pi's model (a real harness object driving real sessions, isolated
fixture directories, artifacts keyed to the harness's own native session
format) is the nearest fit to this project's own charter and existing
`composition`-level fixture-driven-provider precedent
(`README.md`: "runs one tool-calling turn against a real database and a
fixture-driven provider — no network and no credential"), and having it
in place is what lets Context Engine and everything after be
regression-tested rather than eyeballed. **Observability is lower
priority** — only two of six reference projects (Codex, Grok Build) wire
real distributed tracing, three of the other four approximate it with
something structurally closer to this project's own existing audit-event
approach, and this project's charter's "Observable" attribute already has
a working implementation today without OpenTelemetry. **TUI carries the
least urgency to build now** — it is already staged behind two
stepping-stone clients per the 2026-08-30 sequencing decision, and unlike
Context Engine or MCP there is no single converged "adopt this" answer
even among the reference set (three converge on Pi's toolkit lineage, one
adopts `ratatui`, one has none at all), so building it early would spend
effort without a clear reference to build against, still without
unblocking Context Engine, Evaluation, or Observability.

## Open questions a design must resolve

- **Context Engine**: does compaction run as a distinct domain command
  and event the way this project's other decisions already do (matching
  DeepSeek Harness's `compaction/start`/`compaction/end` bracket and
  Pi's `compaction` session entry), or inline within the existing Step
  loop as a side effect of context assembly? What is the token-budget
  source of truth, given `engine.Model`'s capability profile already
  carries `ContextWindow`/`MaxOutput`
  (`internal/harness/composition/config.go:23`,
  `assembly.go:122`)? Does this project adopt one compaction strategy
  (tail-preserving summarization, closest to Pi/DeepSeek
  Harness/Kimi Code/Maka) or, following Codex's and Grok Build's
  precedent, more than one named strategy from the start?
- **Evaluation**: real-model-backed evals (Pi's approach) versus this
  project's own existing fixture-driven-provider precedent from
  composition-root testing — are they complementary layers or does one
  supersede the other for a first slice? Does eval orchestration live
  outside `internal/harness/application` entirely, following Maka's
  explicit Experiment/Runtime-Host separation?
- **Observability**: does this project's existing structured audit event
  set (`ModelAttempt`, `ToolExecution`, `PolicyDecisionRecorded`) already
  satisfy the charter's "Observable" quality attribute, or is OpenTelemetry
  a genuinely separate need (e.g., cross-process trace correlation this
  project does not yet have a use case for, since it has no distributed
  deployment topology today)? If adopted, does it wrap the raw
  `opentelemetry` Go SDK directly (Codex's approach) or a lighter
  intermediate layer (Grok Build's `fastrace`)?
- **TUI**: build from scratch in Go (no reference project offers a
  same-language precedent either way) or adopt an existing Go terminal-UI
  library, given the reference set itself splits between hand-rolling
  (Pi, and by extension Kimi Code and Maka through Pi's own toolkit) and
  adopting a framework (Grok Build's `ratatui`) with no six-project
  consensus either way?

## Evidence limits

- Every citation above traces to the pinned commits in the comparison
  table; no claim is from memory, a project's README marketing language
  alone, or an earlier gate's characterization reused without
  re-verification.
- This gate does not authorize copying any type name, schema shape,
  prompt string, or configuration constant verbatim from any reference
  project — only the mechanisms and architectural choices they represent,
  per the same rule every prior gate in this project states for its own
  comparison set.
- This gate does not audit any of the six projects' Context Engine,
  Evaluation, Observability, or TUI implementations for correctness,
  performance, or security — only for placement, mechanism, and
  precedent value.
- Per-topic depth is uneven by design: Context Engine and TUI read
  multiple files per project with line-level citations; Evaluation and
  Observability read primarily entry points and package-level structure
  once a project's overall shape was clear, consistent with this
  document's roadmap scope rather than a single-subsystem deep dive like
  the MCP client adapter gate.
- Several leads were named but not read in depth and are flagged here
  rather than silently dropped: Grok Build's
  `xai-grok-subagent-resolution` crate contents; DeepSeek Harness's ten
  `packages/subagent/*` packages beyond their names; whether any
  reference project's compaction or MCP-server subprocess handling
  receives OS-level sandboxing.
- "Current state" here means 2026-09-01. A future gate that revisits any
  of these projects, or narrows to one of these four topics for its own
  normative design, must re-fetch and re-read per Documentation rule 7
  rather than reuse this document's characterization.
- This gate does not choose a design for any of the four topics. The next
  step for whichever topic is taken up first is its own normative design,
  informed by — not dictated by — the findings above.
