# Context Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Application's two divergent, unbounded history paths
with one deterministic Context Engine: a pure `internal/harness/contextengine`
package that meters the complete provider-neutral envelope against the
routed model's declared capacity, selects a tool-pair-safe history prefix,
projects oversized Tool Results into bounded excerpts, and replaces the
covered prefix with a durable rolling-summary or deterministic-reset
checkpoint — all before a Provider may see the request — exactly as
`docs/superpowers/specs/2026-09-01-context-engine-design.md` (Accepted)
specifies.

**Architecture:** `internal/harness/contextengine` (CE-01) owns pure
projection, metering, planning, checkpoint validation, and materialization;
it imports Domain value types and nothing else. `internal/harness/application`
gains a Context Orchestrator (CE-02) owning the compaction lifecycle,
Provider dispatch, CAS appends, and overflow retry, replacing today's
unconditional `projectPriorTurns(records, ...)` fold
(`internal/harness/application/loop.go:66`, invoked from `runAfterAdmission`
at `loop.go:187-191`) and today's two divergent request paths — bare-`Input`
`runSingleAttempt` (`loop.go:195`) when no Tool Catalog is configured versus
the byte-capped (`ensureProjectionUnderCap`, `loop.go:365-374`,
`MaxProjectionBytes = 4 << 20`, `service.go:23`) `runStepLoop` when one is —
with one deterministic, token-metered path for both. `internal/harness/domain`
gains four new commands/events and one bounded optional field on
`domain.Session` (§13 of the design). `internal/harness/adapters/memory` and
`internal/harness/adapters/sqlite` gain a `ContextCheckpointStore`
implementation; SQLite gets migration 5 (current `latestMigrationVersion = 4`,
`internal/harness/adapters/sqlite/migrations.go:9`). `internal/harness/runtime`
gains one more startup-reconciliation case. `internal/harness/composition`
wires the new config, summarizer, and construction-order step.
`cmd/och` gains `compact-session`.

**Tech Stack:** Go 1.26, standard library plus this repository's existing
`crypto/sha256` (checkpoint digest chain, §7.2) and `golang.org/x/*`
policy — no new module dependency. `CGO_ENABLED=0 go build ./...` and
`go test -race ./...` stay clean after every task, matching this project's
standing constraint.

**Spec:** `docs/superpowers/specs/2026-09-01-context-engine-design.md`
(English normative, Accepted); synchronized Chinese summary at
`docs/superpowers/specs/2026-09-01-context-engine-design.zh-CN.md`.
Research: `docs/research/architecture-gates/2026-09-01-context-engine.md`
(dedicated subsystem gate) and
`docs/research/architecture-gates/2026-09-01-context-engine-evaluation-observability-tui.md`
(roadmap gate). No Chinese reading copy for this plan, matching the three
most recent prior plans' precedent (exec CPU quota, secret redaction, web
trajectory UI).

## Why sixteen tasks

This design (26 sections, 1261 lines) is larger than any prior accepted
design in this repository — it touches a new pure package (ten files per
§24), the Domain layer, `engine.ModelRequest`, the OpenAI-compatible
Adapter, Application orchestration across four distinct triggers, two
storage adapters plus a new migration, Runtime Host recovery, composition,
a new CLI command, and three existing projection surfaces (ACP, JSONL
audit, transcript). Compressing unrelated concerns to hit a smaller task
count (the way the six-task exec-sandboxing plan or the eight-task web-
trajectory-ui plan could, for smaller designs) would violate this plan's
own Global Constraints below. The six pure-package tasks (1–6) split along
the design's own §24 file boundaries, grouped only where two files are one
inseparable concern (budget+meter; source+projector); Task 12 is a
dedicated concurrency task rather than diffusing race coverage across
Tasks 9–11, because §17 names five distinct winner-table scenarios that
deserve their own focused red-green-refactor pass, matching how the
exec-sandboxing plan's own Task 3 gave the cgroup monitor a dedicated
integration test rather than folding it into Task 2's bwrap wiring.

## Global Constraints

- **Canonical events are never rewritten, deleted, or squashed.** The
  `EventStore` interface stays exactly `ReadStream`, `Append`,
  `ResolveAppend`, `FindCommandRequest`
  (`docs/architecture/eventstore-v2.md:31-36`) through every task. A
  checkpoint is always a new appended event; no task adds a delete/update
  path to any Store adapter (CE-09, design §25 "Rewrite or delete
  historical events").
- **No task may split a Tool Call from its terminal result.** Every
  boundary-selection, projection, and materialization path must treat a
  `ToolCallStarted` and its matching `ToolCallCompleted`/`Failed`/
  `Interrupted` as one inseparable unit (§9.1, §9.2 priority 3/5). A test
  proving this must exist in Task 2 (projection balance) and Task 3
  (boundary selection) independently — one covering the invariant is not
  enough.
- **No Provider request may exceed `hardInput` or the existing 4 MiB
  `MaxProjectionBytes` cap.** `MaxProjectionBytes` is not widenable by any
  Context config (§8's resource table) — no task raises, parameterizes, or
  bypasses the `service.go:23` constant.
- **No task reintroduces a second Provider, credential, or transport for
  summarization.** CE-12 and design §25 ("Separate summary Provider in this
  milestone") explicitly deferred this; `ContextSummarizer` is implemented
  over the already-constructed active conversation `engine.Model` only, for
  every task in this plan. If a future milestone adds one, it is a separate
  design, not a mid-plan addition here.
- **A provider-usage anchor may only raise `budgetEstimate`, never lower
  it below `wireEstimate`.** `budgetEstimate = max(wireEstimate,
  anchoredEstimate?)` (§8) is exact; no task may compute an estimate that
  can fall below the deterministic `och_wire_estimate_v1` value under any
  input, including a malformed or adversarial anchor candidate.
- **No summary is used before its `context.compaction.completed` append
  actually commits**, and no Provider dispatch happens before its
  `context.prepared` + `model.request.recorded` pair is durably appended
  (§26 review checklist, first and fifth bullets). Every orchestration task
  (9–11) must have a test proving no Provider call occurs before the
  corresponding append commits.
- **Cancellation never silently becomes a reset or a completion** (§26).
  Task 12's race matrix must include a case proving a cancelled compaction
  closes as `failed`, never as a synthesized `completed` or `reset`.
- **No live heap scales with Session lifetime.** The two-pass bounded scan
  (§9.3) retains at most the protected-tail deque, the current open unit,
  and one below-trigger envelope during Pass 1; no task materializes a
  complete historical stream into memory at once except the explicitly
  paged, heap-bounded cold-rebuild path (§22.4 benchmark acceptance).
- **`gofmt`, `go vet ./...`, and `CGO_ENABLED=0 go build ./...` stay clean
  after every task**, not just at the end.
- **Every task follows red-green-refactor**, and every task listed below as
  introducing security- or correctness-load-bearing logic performs its
  stated mutation check (disable the new logic, confirm the right test
  fails, restore) as part of that task, not deferred to the Final
  Completion Gate.
- **No sleep-based concurrency tests.** Task 12 and every other
  concurrency-relevant test uses channels, contexts, and this project's
  existing bounded-timeout precedent.

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/harness/contextengine/budget.go` | `och_wire_estimate_v1` meter, budget formula (`safety`/`hardInput`/`trigger`/`target`/`protectedTail`/`summaryOutputCap`), non-lowering usage-anchor eligibility and `anchoredEstimate` |
| `internal/harness/contextengine/source.go` | Source event filter, extendable SHA-256 digest chain (§7.2) |
| `internal/harness/contextengine/projector.go` | Projection grammar (§9.1): canonical events → `ContextUnit`s, tool-pair balance |
| `internal/harness/contextengine/planner.go` | Cut selection (§9.2) and the two-pass bounded scan (§9.3) |
| `internal/harness/contextengine/tool_result.go` | Tool Result projection (§10): head/tail excerpting, digest, marker escaping |
| `internal/harness/contextengine/checkpoint.go` | `ContextCheckpoint` types, deterministic `source_tail_reset_v1` (§12), checkpoint replay validation (§14.3) |
| `internal/harness/contextengine/prompt.md` | Versioned `och_context_summary_v1` prompt asset (§11.1) |
| `internal/harness/contextengine/summarizer_validation.go` | Summary output validation (§11.3): shape, redaction, shrink proof |
| `internal/harness/contextengine/materialize.go` | `Materializer`: combines checkpoint/tail/input into `PreparedContext` (§7.4) |
| `internal/harness/domain/{commands,events,ids,codec,apply,decide,state}.go` | New commands/events/IDs, `ContextCompaction` bounded aggregate field, codec/apply/decide rules (§13) |
| `internal/harness/engine/model.go` | `ModelRequest.Purpose`/`MaxOutputTokens` (§6.3) |
| `internal/harness/adapters/openaicompat/*.go` | Output-cap/purpose mapping, `InputTokens`/`CachedInputTokens` normalization (§20.1) |
| `internal/harness/application/ports.go` | `ContextSummarizer`, `ContextCheckpointStore` port declarations |
| `internal/harness/application/context_orchestrator.go` (new) | Pre-turn/mid-turn automatic preparation, replacing `projectPriorTurns`'s direct use in `runAfterAdmission` |
| `internal/harness/application/context_overflow.go` (new) | Provider overflow recovery (§15.3) |
| `internal/harness/application/context_manual.go` (new) | `Service.CompactSession` (§15.4) |
| `internal/harness/application/errors.go` | New `context_*` codes (§16) |
| `internal/harness/adapters/memory/event_store.go` | In-memory `ContextCheckpointStore` |
| `internal/harness/adapters/sqlite/migration5.go` (new), `migrations.go`, `migrations_sql.go` | `context_checkpoint_heads` table (§14.1) |
| `internal/harness/adapters/sqlite/context_checkpoint.go` (new) | SQLite `ContextCheckpointStore`, hash-chain verification before trusting the row |
| `internal/harness/adapters/sqlite/rebuild.go` | `RebuildAndVerifyContextCheckpointHeads` (§14.2) |
| `internal/harness/runtime/reconcile.go` | Unmatched `ContextCompactionStarted` recovery (§14.4) |
| `internal/harness/composition/{config,assembly}.go` | `composition.Config.Context`, construction order (§21) |
| `internal/harness/adapters/acp/project.go` | Confirm `session/load` stays canonical-only; no fabricated updates (§20.4) |
| `internal/harness/transcript/{codec,export}.go` | Bounded compaction/prepared transcript facts (§20.3) |
| `cmd/och/main.go` | `compact-session` subcommand |
| `docs/architecture/context-engine.md`, `.zh-CN.md` | New implemented contract |
| `docs/architecture/context-engine-evidence.md` | New evidence ledger |
| `docs/getting-started.md` | New section: automatic and manual compaction |
| `docs/README.md`, `README.md` | Authority-table rows, milestone 8 prose, current-status bullet |

---

### Task 1: Pure package — budget contract, meter, and usage anchor

**Files:**

- Create: `internal/harness/contextengine/budget.go`, `budget_test.go`
- Create: `internal/harness/contextengine/meter.go`, `meter_test.go`
- Create: `internal/harness/contextengine/doc.go` (package boundary statement: imports Domain value types only)

- [ ] `doc.go`: package comment stating CE-01's import boundary explicitly
  (no Application, Adapter, SQLite, ACP, filesystem, clock, randomness,
  logger, or Provider SDK import), enforced later by
  `internal/harness/architecture`'s existing dependency-boundary test
  pattern (Task 7 adds the concrete case).
- [ ] `budget.go`: `Budget` (or similarly named) type computing `safety =
  max(512, ceil(W*0.02))`, `hardInput = W - O - safety`, `trigger =
  floor(hardInput*TriggerPercent/100)`, `target =
  floor(hardInput*TargetPercent/100)`, `protectedTail =
  floor(hardInput*TailPercent/100)`, `summaryOutputCap = min(O,
  max(128, floor(hardInput*0.10)))`, given `W`, `O`, and the
  `TriggerPercent`/`TargetPercent`/`TailPercent` config (defaults 80/55/25
  per §8's table). Returns `context_budget_invalid` when `O + safety >= W`.
- [ ] `meter.go`: `Meter` implementing `och_wire_estimate_v1` — `ceil(UTF-8
  bytes/3)` per text/JSON payload, 8 tokens fixed per message, 16
  additional per Tool Call/Result, 16 plus `ceil(canonical JSON bytes/3)`
  per Tool Schema — over an `Envelope` value (messages, tools, checkpoint
  framing, prune markers all measured as ordinary text).
- [ ] Usage-anchor eligibility and `anchoredEstimate = max(0,
  observedInputTokens + signedSurfaceDelta)` per §8's exact eligibility
  list (newest prior completed attempt with non-zero `InputTokens`;
  Session/Turn/Item/AttemptIndex match; adapter family/endpoint/model/
  purpose/meter identity/Tool Schemas all match; message surface derivable
  from the recorded request by ordered appends or checkpoint/rewrite
  replacements the same meter can price). `signedSurfaceDelta` prices
  content added/removed/replaced since the sampled request; raw
  `OutputTokens` is never added (may include unpersisted reasoning);
  `CachedInputTokens` is a subset of `InputTokens`, never additive. Final
  dispatch value: `budgetEstimate = max(wireEstimate, anchoredEstimate?)`.
- [ ] Unit tests: table tests for budget math at 8K/32K/128K windows and a
  near-invalid route (`O + safety` just under and at `W`); golden meter
  fixtures for ASCII, Chinese (multi-byte UTF-8), code, JSON, a Tool Call,
  a Tool Result, a Tool Schema, checkpoint framing text, and a prune
  marker; usage-anchor matrices covering an exact match, an
  append-only delta, a checkpoint-replacement delta, every listed identity
  mismatch (adapter/endpoint/model/purpose/meter/tools), zero/missing/
  malformed `InputTokens`, and the non-lowering rule (a deliberately
  understated anchor never reduces `budgetEstimate` below `wireEstimate`).
- [ ] Mutation check (trigger comparison, §22.4's mutation-kill list):
  invert `trigger`'s comparison operator, confirm the below-trigger and
  above-trigger table tests fail for the right reason, restore.
- [ ] Mutation check (output/safety reserve): set `safety` to a fixed small
  constant regardless of `W`, confirm the near-invalid-route test fails
  (it stops rejecting `O + safety >= W` at the boundary the real formula
  rejects), restore.
- [ ] Run:

```bash
go test ./internal/harness/contextengine/... -run 'Budget|Meter|Anchor' -count=1 -v
go test -race ./internal/harness/contextengine/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(contextengine): budget contract, wire estimate meter, and usage anchor`.

### Task 2: Pure package — event projection grammar and source digest

**Files:**

- Create: `internal/harness/contextengine/source.go`, `source_test.go`
- Create: `internal/harness/contextengine/projector.go`, `projector_test.go`

- [ ] `source.go`: the versioned source filter (excludes Session, model
  request/usage, policy, approval, context, and terminal Turn events from
  `coveredEventCount`/`sourceDigest`, per §7.2) and the extendable SHA-256
  chain: `D0 = SHA256("och-context-source-v1\n")`, `Di =
  SHA256("och-context-source-step-v1\n" || Di-1 ||
  uint64-big-endian(len) || canonical-event-encoding)` using
  `domain.MarshalRecordedEvent` after the filter. A rolling-successor mode
  continues from a prior validated `Dn`; a cold-rebuild mode recomputes
  from `D0`.
- [ ] `projector.go`: the projection grammar (§9.1) — `TurnStarted` → user
  message, `AssistantMessageCompleted` → assistant message (+ Tool
  Calls), `ToolCallStarted` → Step-membership bookkeeping,
  `ToolCallCompleted`/`Failed`/`Interrupted` → tool result. Groups
  multiple calls from one assistant message and their terminal results
  into one Step unit even when completion events interleave; a boundary
  is balanced only once every offered Call ID has exactly one terminal
  result. Duplicate, unknown, or missing Call IDs fail closed with
  `context_projection_invalid`, matching the "incomplete historical tool
  pair is a store/domain contract violation" rule (§7.1).
- [ ] Unit tests: fixtures covering complete, failed, and interrupted
  Turns; interleaved multi-call Tool Results (two open calls, terminal
  results arriving out of call order) still resolve to one balanced Step;
  a duplicate Call ID and a terminal result naming an unknown Call ID both
  fail closed with `context_projection_invalid`, never panic or silently
  drop; a source-digest fixture proves the same event sequence always
  produces the same `Dn`, and a rolling continuation from a prior `Dn`
  matches a cold rebuild over the full sequence.
- [ ] Mutation check (tool-pair boundary): remove the "every Call ID has
  exactly one terminal result" balance check, confirm the interleaved-
  multi-call test fails for the right reason (an unbalanced Step is
  accepted), restore.
- [ ] Mutation check (source filter/digest): stop excluding operational
  events (e.g. include a `PolicyDecisionRecorded` in the digest input),
  confirm the rolling-vs-cold-rebuild digest-equality test fails, restore.
- [ ] Run:

```bash
go test ./internal/harness/contextengine/... -run 'Source|Digest|Projector|Grammar' -count=1 -v
go test -race ./internal/harness/contextengine/... -count=1
```

- [ ] Commit: `feat(contextengine): event projection grammar and source digest chain`.

### Task 3: Pure package — boundary planner and two-pass bounded scan

**Files:**

- Create: `internal/harness/contextengine/planner.go`, `planner_test.go`

- [ ] Cut selection (§9.2): walk a bounded deque backward from the head
  until `protectedTail` is met, then snap older content to the nearest
  safe boundary, in priority order — retain complete recent Turns; if one
  retained historical Turn alone exceeds the tail budget, retain its
  newest closed Steps; during mid-turn pressure an earlier closed Step of
  the active Turn may be covered only after all its Tool Calls are
  terminal; never cover current input without covering the complete
  earlier portion of its Turn; never cover the currently open assistant
  item. The selected prefix must bring the estimated request to or below
  `target` unless `MaxSummaryChunks` limits a below-hard opportunistic
  pass; a partial advancement is acceptable only when it shrinks the total
  request by at least 10% and lands at or below `hardInput`.
- [ ] Two-pass bounded scan (§9.3): Pass 1 pins the first `ReadStream`
  head (the same `ReadStreamRequest`/`StreamPage` port this repository's
  Application layer already uses, `internal/harness/application/store.go:64`
  — Task 9 wires the real `EventStore` call; this task's own tests use an
  in-memory fake page source) and checks every subsequent page against
  that pin; it folds source events into unit metadata, increments the
  digest/counts (Task 2), meters messages/schemas (Task 1), and retains
  at most the protected-tail deque, the current open unit, and one
  below-trigger envelope — never a complete historical stream. If no
  compaction is needed, Pass 1's bounded messages materialize the request
  directly. If compaction is needed, Pass 2 rereads only the selected
  sequence ranges at the same pinned head. A head mismatch between passes
  is a store contract violation (`context_projection_invalid`); an append
  landing after the pin is simply absent from this immutable view — no
  path in this package calls a whole-stream read equivalent to
  `ReadWholeStreamPinned` (`internal/harness/application/loop.go:187`).
- [ ] Unit tests: boundary matrices for between-Turn, within-Turn, active-
  Step, no-safe-prefix, prior-checkpoint-present, and model-switch (a
  smaller `W` than the checkpoint was built against) cases; fuzz
  properties — no orphaned Tool Result, no missing retained Call result,
  coverage is always a strict prefix, the cut point only ever advances
  (never regresses) across repeated planning calls on growing input, and
  the materialized estimate never exceeds `hardInput`; a Pass-1-only path
  (below trigger) proven to read at most one page beyond the protected
  tail, never the complete fixture stream.
- [ ] Mutation check: relax "never cover the currently open assistant
  item," confirm the active-Step boundary test fails (a still-open item
  gets covered), restore.
- [ ] Run:

```bash
go test ./internal/harness/contextengine/... -run 'Planner|Cut|Scan|Fuzz' -count=1 -v
go test -race ./internal/harness/contextengine/... -count=1
```

- [ ] Commit: `feat(contextengine): boundary planner and two-pass bounded scan`.

### Task 4: Pure package — Tool Result projection

**Files:**

- Create: `internal/harness/contextengine/tool_result.go`, `tool_result_test.go`

- [ ] `maxProjectedToolResultTokens = min(2048, max(256, protectedTail/2))`
  (depends on Task 1's `Budget`); results at or below remain
  byte-identical. Larger results become the fixed marker format (§10):
  `[tool result projected by Open Code Harness]` header with `event_id`,
  `original_bytes`, `sha256`, then `content_head`/`content_tail` excerpts
  — 75% of the content budget to the head, 25% to the tail, rounded
  toward the head, UTF-8 cut only at rune boundaries, shrinking further if
  the marker plus metadata plus excerpts would otherwise exceed the cap.
  Role, Tool Call ID, and tool name stay unchanged; marker delimiters
  inside the excerpted content are escaped so excerpted tool output can
  never be mistaken for marker syntax.
- [ ] A single protected message or projected result that still exceeds
  `hardInput` returns `context_unit_too_large` rather than being silently
  truncated further or dropped.
- [ ] Unit tests: byte-identical passthrough at/under the cap; over-cap
  head/tail split at the exact 75/25 ratio with a rune-boundary-respecting
  cut on multi-byte UTF-8 content; a marker-delimiter string embedded in
  the original content is escaped and round-trips distinguishably in the
  projected output; a pathologically large single result triggers
  `context_unit_too_large`.
- [ ] Mutation check: stop escaping marker delimiters in excerpted
  content, confirm the delimiter-embedding test fails (the projected
  output becomes ambiguous with real marker syntax), restore.
- [ ] Run:

```bash
go test ./internal/harness/contextengine/... -run 'ToolResult|Projection' -count=1 -v
```

- [ ] Commit: `feat(contextengine): bounded Tool Result projection`.

### Task 5: Pure package — checkpoint types, deterministic reset, and replay validation

**Files:**

- Create: `internal/harness/contextengine/checkpoint.go`, `checkpoint_test.go`

- [ ] `ContextCheckpoint` type (§7.3): ID, Session, `kind`
  (`rolling_summary_v1` | `source_tail_reset_v1`), `sourceSchema`,
  `summaryFormat`/`promptVersion`, coverage, `previousCheckpointID`,
  optional summary, limitations, before/after/tail/estimated token
  counts, optional summarizer route/usage, chunk count, pruned-result
  count. A successor must strictly advance coverage, or be an explicit
  same-coverage rewrite whose `previousCheckpointID` names the current
  checkpoint with an identical source digest — never backward, never
  skipping a source unit.
- [ ] `source_tail_reset_v1` (§12): the fixed, versioned user-role marker
  (no LLM text) stating an earlier prefix was omitted for capacity, that
  the marker makes no historical claim, that coverage identifiers are
  diagnostic only, and that the model must continue from the retained raw
  tail and current input. Eligibility gate: projected request exceeds
  `hardInput` or a classified startup `context_overflow` occurred; rolling
  summary is impossible/canceled-without-caller-cancellation/invalid/
  non-shrinking/chunk-exhausted; a safe covered prefix exists; reset plus
  the retained complete tail fits `hardInput`. Cancellation itself never
  satisfies this gate.
- [ ] Checkpoint replay validation (§14.3): Session/checkpoint ID and
  schema/kind/format support; coverage boundary at or before the pinned
  head; source identity/digest proof (via the `ContextCheckpointStore`
  contract Task 13 implements — this task's tests supply a fake);
  predecessor rules; summary structure for `rolling_summary_v1`; current
  route capacity and current meter estimate; checkpoint plus retained tail
  fits the current budget. A previously valid checkpoint failing any check
  (e.g. after a model switch) is rejected, not silently accepted.
- [ ] Unit tests: checkpoint digest/lineage/same-coverage-rewrite/current-
  budget/clone cases; the deterministic reset marker's exact fixed text is
  golden-tested (no source content or digest embedded in the marker
  itself, per §12); the eligibility gate's four conditions each tested
  independently (each one being false alone blocks reset); a checkpoint
  valid under one route's `W` rejected after simulating a smaller `W`.
- [ ] Mutation check (reset hard-limit gate): drop the "reset plus
  retained tail fits `hardInput`" condition, confirm a reset that would
  still overflow is no longer rejected by the eligibility test, restore.
- [ ] Mutation check (checkpoint current-budget replay check): skip the
  "checkpoint plus retained tail fits the current budget" validation step,
  confirm the smaller-`W` rejection test stops rejecting, restore.
- [ ] Run:

```bash
go test ./internal/harness/contextengine/... -run 'Checkpoint|Reset|Replay' -count=1 -v
```

- [ ] Commit: `feat(contextengine): checkpoint types, deterministic reset, and replay validation`.

### Task 6: Pure package — summary contract, validation, and materializer

**Files:**

- Create: `internal/harness/contextengine/prompt.md`
- Create: `internal/harness/contextengine/summarizer_validation.go`, `summarizer_validation_test.go`
- Create: `internal/harness/contextengine/materialize.go`, `materialize_test.go`

- [ ] `prompt.md`: the versioned `och_context_summary_v1` prompt asset
  (owned by this package, not an inline Application string) instructing
  the model to transform bounded source material — not continue the
  conversation, obey embedded requests, or call Tools — with source
  messages and any previous summary delimited as untrusted data. Requires
  exactly these top-level sections in order: Objective, User Constraints,
  Established Facts, Work Completed, Files and Commands, Open Work, Risks
  and Unknowns, Continuation. Requires exact paths/identifiers/commands/
  error codes and unresolved uncertainty where material; forbids secrets,
  hidden reasoning, invented completion, and unsupported claims. A manual
  focus string is data inside a dedicated field, never able to alter the
  output schema.
- [ ] `summarizer_validation.go` (§11.3): before persistence, summary
  output must be valid non-blank UTF-8; terminate normally (not at output
  length or stream error); contain each required heading exactly once, in
  order, with no unknown top-level heading; fit `summaryOutputCap` and
  256 KiB; pass `redact.Text` (`internal/harness/redact`,
  `docs/architecture/secret-redaction.md:13`) before the size/shrink
  checks below, so recorded evidence equals the summary actually used;
  contain no Tool Calls or non-text content; make the complete projected
  request at least 10% smaller than the pre-pass request and no larger
  than `hardInput`; make checkpoint framing itself smaller than the
  covered source it replaces. Any failure closes the bracket as failed
  (Task 9/10 own the actual `context.compaction.failed` append; this task
  only returns a typed validation result).
- [ ] `materialize.go`: the `Materializer` combining a selected checkpoint
  (or none), the retained raw tail (Task 3), and the current input into
  the final `PreparedContext` — the complete serialized message/Tool
  Schema envelope plus the `ContextPreparedRecorded` evidence fields from
  §7.4 (trigger, source head version, checkpoint ID/kind or none, raw
  tail range, budget values, deterministic/anchored estimates, bounded
  Tool Result rewrite facts, final envelope bytes, meter implementation
  ID).
- [ ] Unit tests: summary shape goldens (valid, duplicate heading, missing
  heading, unknown heading, non-terminal finish, over-cap, under-shrink,
  a secret-shaped string that redaction must strip before the shrink
  check runs); a rolling successor's chunked input containing only the
  previous validated summary plus newly covered units, fitted below `W -
  summaryOutputCap - safety`; materializer goldens for checkpoint+tail+
  input combining correctly and the resulting envelope never exceeding
  `hardInput`.
- [ ] Mutation check (summary shrink validator): remove the "at least 10%
  smaller" check, confirm the under-shrink golden stops failing, restore.
- [ ] Run:

```bash
go test ./internal/harness/contextengine/... -run 'Summary|Validation|Materialize' -count=1 -v
go test -race ./internal/harness/contextengine/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(contextengine): summary contract, validation, and materializer`.

### Task 7: Domain — commands, events, bounded aggregate state, request-event changes

**Files:**

- Modify: `internal/harness/domain/{commands,events,ids,codec,apply,decide,state,errors}.go`
- Modify: `internal/harness/domain/*_test.go` (existing suites covering `Decide`/`Apply`/codec round-trips)
- Modify: `internal/harness/domain/compact_test.go` only if the field-count assertion below requires it

- [ ] New IDs (`ids.go`): `ContextCompactionID`, `ContextDecisionID`, with
  `Parse*`/validation functions matching the existing pattern
  (`SessionID`/`TurnID`/etc., `ids.go:8-15`).
- [ ] New commands (`commands.go`, §13.1): `StartContextCompaction`,
  `CompleteContextCompaction`, `FailContextCompaction`,
  `RecordContextPreparation`, wired into `Decide`
  (`internal/harness/domain/decide.go:202`) alongside the existing command
  switch.
- [ ] New events (`events.go`, §13.2): `ContextCompactionStarted` (ID,
  trigger, strategy, base source head, prior checkpoint ID, prompt/
  source/meter versions, non-secret planned route identity),
  `ContextCompactionCompleted` (embeds the validated `ContextCheckpoint`
  from Task 5), `ContextCompactionFailed` (closed stable code, safe
  message — never partial model output), `ContextPreparedRecorded` (§7.4
  fields). Wired into `Apply` (`internal/harness/domain/apply.go:11`).
- [ ] `ModelRequestRecorded` (`events.go:167-182`) gains `Purpose`,
  `AttemptIndex`, `ContextDecisionID`; conversation requests store the
  complete final `Messages`/`Tools`. `ModelUsageRecorded`
  (`events.go:188-197`) gains `AttemptIndex` so an overflow attempt and
  its retry are never conflated.
- [ ] `domain.Session` (`state.go:96-102`, currently 5 fields: ID, Status,
  Version, WorkspaceRoot, ActiveTurn) gains one optional
  `ContextCompaction *ContextCompaction` field (ID, Trigger, Strategy,
  BaseVersion, StartedAt — no summary, source events, messages, or
  checkpoint list). Confirm this fits
  `compact_test.go:17-20`'s existing `NumField() > 6` growth guard (5 + 1
  = 6, at the guard's current headroom, not over it) before touching that
  test; if the actual current field count differs from 5 by the time this
  task runs, update the guard's threshold explicitly and say so in the
  commit message rather than silently loosening it.
- [ ] Eligibility rules in `Decide`: manual/pre-turn start requires an
  active Session and no active Turn; mid-turn/overflow start requires the
  caller-owned active Turn at a pre-Provider boundary; a new Turn,
  Session close/delete, or a second compaction start all reject while one
  is active; terminal compaction timestamps cannot precede start. At most
  one `ContextCompaction` active at a time; start sets it, complete/fail
  clears it; completed checkpoints never enter the bounded aggregate.
- [ ] Codec (`codec.go`): round-trip encode/decode for all four new event
  types and the two new request-event fields, rejecting unknown/extra
  fields exactly as every existing event type does.
- [ ] Unit tests: `Decide`/`Apply` table tests for every eligibility rule
  above (each independently, each as the sole reason a command is
  rejected); a full command→event→state round trip through `Replay`; codec
  golden round-trips for all four new event types; confirm
  `TestReplayCompactDiscardsTerminalTranscript` and
  `TestApplyCompactRetainsOnlyActiveTurnAndItem` still pass unmodified in
  intent (only the field-count literal may change, per above).
- [ ] Run:

```bash
go test ./internal/harness/domain/... -count=1 -v
go test -race ./internal/harness/domain/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(domain): context compaction commands, events, and bounded aggregate state`.

### Task 8: Engine request purpose/output cap and OpenAI-compatible Adapter mapping

**Files:**

- Modify: `internal/harness/engine/model.go` (currently 7 fields: SessionID, TurnID, ItemID, Input, Messages, Tools — `model.go:11-18`)
- Modify: `internal/harness/adapters/openaicompat/*.go`
- Modify: corresponding `*_test.go` files

- [ ] `engine.ModelRequest` gains `Purpose ModelRequestPurpose`
  (`conversation` | `compaction`) and `MaxOutputTokens uint32` (positive,
  `<=` route maximum) per §6.3.
- [ ] The OpenAI-compatible Adapter maps `MaxOutputTokens` to its
  configured `max_tokens`/`max_completion_tokens` field per request (not
  the route's static configured maximum) and may use `Purpose` only for
  non-secret attribution headers — `Purpose` never changes model-visible
  request semantics inside the generic adapter.
- [ ] `InputTokens` normalization (§20.1): treat it as total prompt
  occupancy including cached input; treat `CachedInputTokens` as a
  strict subset; reject (classify, do not silently clamp) a response
  reporting `CachedInputTokens > InputTokens`.
- [ ] Unit tests: a `compaction`-purpose request produces an
  attribution-only difference in the outbound HTTP request, never a
  different message/tool shape than an equivalent `conversation`-purpose
  request; `MaxOutputTokens` overrides the per-request cap correctly at
  the wire level; a fixture response with `CachedInputTokens >
  InputTokens` is classified as a provider anomaly, not accepted.
- [ ] Run:

```bash
go test ./internal/harness/engine/... ./internal/harness/adapters/openaicompat/... -run 'Purpose|MaxOutputTokens|CachedInput' -count=1 -v
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(engine,openaicompat): request purpose, output cap, and cached-input normalization`.

### Task 9: Application — pre-turn automatic preparation, replacing `projectPriorTurns`'s direct use

**Files:**

- Create: `internal/harness/application/context_orchestrator.go`, `context_orchestrator_test.go`
- Modify: `internal/harness/application/ports.go` (add `ContextSummarizer`, `ContextCheckpointStore` port declarations)
- Modify: `internal/harness/application/loop.go` (`runAfterAdmission`)
- Modify: `internal/harness/application/service.go` (new `Context` config fields, defaults)

- [ ] `ContextSummarizer` port: one method calling `engine.Model` directly
  with `Purpose: compaction` through a shared bounded stream collector (no
  Tools sent, text-only, output capped, using the same closed
  `engine.ProviderFailure` taxonomy conversation attempts already use) —
  it does not enter `RunTurn`, emit assistant deltas, or recursively
  invoke the Context Engine.
- [ ] `context_orchestrator.go`: the pre-turn flow (§15.1) — validate
  request/idempotency, acquire execution ownership (existing registry),
  load bounded Session state, allocate Turn/Item/Command/decision IDs,
  read the latest verified checkpoint (Task 13's port, faked in this
  task's own tests), run Task 3's Pass 1 pinned plan including the
  incoming input and Tool Schemas, run the compaction bracket (Task 5/6's
  planner/summarizer/checkpoint pipeline) only if pressure is detected,
  materialize checkpoint+tail+input (Task 6), append the admission batch
  (`turn.started` + `assistant.message.started` + `context.prepared` +
  `model.request.recorded`, §13.4), then dispatch the Provider attempt.
  If compaction changed the Session version, admission uses the
  post-compaction version.
- [ ] Replace `runAfterAdmission`'s current unconditional
  `owned.projection = newTurnProjectionWithPrefix(projectPriorTurns(records,
  result.TurnID), request.Input)` (`loop.go:187-191`) and the
  `catalogEnabled()`-branched `runSingleAttempt` bare-`Input` path
  (`loop.go:195-211`) with calls into this new orchestrator for **both**
  the model-only and tool-enabled paths — closing the design's own
  defect #1 ("history behavior changes merely because a Tool Catalog is
  configured").
- [ ] Unit tests: history behaves identically (same projected messages)
  whether or not a Tool Catalog is configured, given equivalent Session
  state; the first request and every subsequent Step record the complete
  envelope actually sent (`ModelRequestRecorded.Messages`/`Tools` equal
  what `engine.Model` received, byte for byte); no Provider call occurs
  before its `context.prepared` + `model.request.recorded` append pair
  commits (Global Constraints); a duplicate `RunTurnRequestID` arriving
  while pre-turn compaction is in flight joins behind the existing
  execution registry rather than racing it; append success/failure/
  unknown-outcome/version-conflict at each phase of the admission batch.
- [ ] Mutation check (append-before-use): reorder the orchestrator to
  dispatch the Provider attempt before the `context.prepared`/
  `model.request.recorded` append commits, confirm the "no Provider call
  before append" test fails for the right reason, restore.
- [ ] Run:

```bash
go test ./internal/harness/application/... -run 'ContextOrchestrator|PreTurn|Projection' -count=1 -v
go test -race ./internal/harness/application/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(application): pre-turn Context Engine orchestration`.

### Task 10: Application — mid-turn preparation, overflow recovery, and failure algebra

**Files:**

- Modify: `internal/harness/application/context_orchestrator.go`
- Create: `internal/harness/application/context_overflow.go`, `context_overflow_test.go`
- Modify: `internal/harness/application/errors.go` (new `context_*` codes, §16)
- Modify: `internal/harness/application/pipeline.go` or `loop.go` (Step-loop integration point after all Tool Result events for a Step commit)

- [ ] Mid-turn preparation (§15.2): after every Tool Result event for a
  Step commits and before the next assistant item dispatches, allocate
  the next Item/decision IDs, plan at a pinned post-tool head, compact
  only if required while the Turn owner is between Steps, append
  `assistant.message.started + context.prepared + model.request.recorded`,
  dispatch. Closed early Steps of the active Turn may enter coverage; the
  new open item may not (reuses Task 3's planner priority rules directly).
- [ ] Provider overflow recovery (§15.3): intercept only an
  `engine.ProviderFailure` whose durable `Code == "context_overflow"`
  (`internal/harness/adapters/openaicompat/classify.go:103`) produced
  before any stream delta/tool call was emitted — today this code is
  handled purely as a terminal display-only case
  (`internal/harness/application/turn.go:480`, `displayFailureSentence`).
  Close the failed stream, snapshot attempt stats, check caller
  cancellation and the per-Turn recovery counter, force an overflow plan
  against the latest committed source, require at least 10% estimated
  reduction, commit a summary or reset checkpoint, append a new
  `context.prepared + model.request.recorded` pair with the next attempt
  index, retry the Provider once per recovery (default 2 recoveries per
  Turn, maximum 3, §19). If no safe prefix exists, reduction is
  insufficient, the cap is exhausted, or the retry still overflows, fall
  through to the existing `context_overflow` terminal failure path
  unchanged. No other Provider failure enters this flow.
- [ ] New `errors.go` codes (§16), matching the existing
  `ErrorCategory`/`Code*`/`ToolText*` triple pattern (`errors.go:12-49`):
  `context_budget_invalid`, `context_projection_invalid`,
  `context_unit_too_large`, `context_compaction_busy`,
  `context_nothing_to_compact`, `context_summary_failed`,
  `context_summary_invalid`, `context_checkpoint_invalid`,
  `context_compaction_limit`, each with the retryability and category
  from §16's table.
- [ ] Failure policy (§16, exact semantics, tested independently): before
  a compaction-start append, no compaction event is written; after start,
  exactly one completed-or-failed terminal is ever attempted; a summary
  failure below hard budget lets the source-derived request proceed after
  logging the terminal failure; a summary failure at hard budget lets
  automatic/overflow attempt deterministic reset (manual summary instead
  returns its failure directly); a completion-append unknown outcome is
  resolved before any further summarization is attempted; a completion
  version conflict closes/reconciles the old attempt and replans once
  from a new head under the same deadline; a context-prepared/request
  append failure means no Provider dispatch occurred; runtime delivery
  failure never changes whether a checkpoint or model request actually
  committed.
- [ ] Unit tests: mid-turn history identical with/without a Tool Catalog;
  overflow retry only follows measured reduction (a forced replan that
  doesn't reduce by 10% does not retry); mid-stream Provider errors never
  enter the overflow-retry path (only a pre-delta `context_overflow`
  does); each failure-policy bullet above as an independent test case;
  the overflow-recovery-count cap (default 2, max 3) exhausting correctly
  falls through to the existing terminal failure.
- [ ] Mutation check (overflow attempt cap): remove the per-Turn recovery
  counter check, confirm the cap-exhaustion test no longer falls through
  to the terminal failure (it retries forever instead), restore.
- [ ] Run:

```bash
go test ./internal/harness/application/... -run 'MidTurn|Overflow|ContextFailure' -count=1 -v
go test -race ./internal/harness/application/... -count=1
```

- [ ] Commit: `feat(application): mid-turn preparation, overflow recovery, and context failure algebra`.

### Task 11: Application — manual compaction and `och compact-session`

**Files:**

- Create: `internal/harness/application/context_manual.go`, `context_manual_test.go`
- Modify: `cmd/och/main.go`, `cmd/och/main_test.go`

- [ ] `Service.CompactSession(ctx, request)` (§15.4): accepts Session ID,
  strategy (`summary` default or explicit `reset`), and an optional focus
  string bounded to 4 KiB UTF-8. Acquires Session compaction ownership,
  requires an active idle Session (no active Turn — reuses Task 7's
  eligibility rules), runs the same planner/bracket/checkpoint-append
  pipeline as the automatic paths, returns checkpoint identity plus token
  evidence. Below trigger, manual summary is still allowed if a safe
  prefix exists; a no-op (not an error) when nothing can be covered
  (`context_nothing_to_compact`).
- [ ] Manual cancellation closes a started bracket as `failed` within the
  configured cleanup timeout; it never falls through to reset unless the
  request explicitly selected `reset` (matching the Global Constraints'
  "cancellation never silently becomes a reset" rule).
- [ ] `och compact-session` (CE-14): opens the normal composition root
  (must acquire the Runtime lease; fails rather than operating beside
  another live writer, matching `och export-session`'s own
  `sqlite.OpenReader`-vs-lease precedent but on the write side). Output is
  one stable JSON object on stdout; logs on stderr, matching
  `och export-session`'s existing stdout/stderr discipline
  (`cmd/och/main.go:118-122,158-160`).
- [ ] Unit tests: manual summary/reset both succeed on a safe Session;
  manual compaction on an active Turn is rejected
  (`context_compaction_busy` or the Turn-active eligibility rule from
  Task 7); a nothing-to-compact Session returns the no-op result, not an
  error; manual cancellation mid-bracket closes failed, never reset,
  even when the request's own strategy field was left at the `summary`
  default; `och compact-session` end-to-end against a real composition
  root and a fixture provider, asserting the printed JSON shape and that
  a concurrent live writer causes it to fail rather than double-write.
- [ ] Run:

```bash
go test ./internal/harness/application/... -run 'CompactSession|Manual' -count=1 -v
go test ./cmd/och/... -run 'CompactSession' -count=1 -v
go test -race ./internal/harness/application/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(application,och): manual compact-session command`.

### Task 12: Concurrency and cancellation race matrix

**Files:**

- Create: `internal/harness/application/context_concurrency_test.go`

- [ ] `go test -race` coverage for exactly the winner tables §17 names:
  concurrent manual/manual compaction on one Session (one wins, the other
  observes `context_compaction_busy` deterministically, never both
  succeeding or corrupting state); manual compaction concurrent with
  `RunTurn` on the same Session (Domain state plus Store CAS reject the
  loser, matching "unrelated clients cannot append tool/assistant
  transitions during a manual compaction," §13.3); `RunTurn` concurrent
  with Session close/delete (mutually exclusive through Domain state,
  §17); an overflow recovery concurrent with caller cancellation (the
  Global Constraints' "cancellation never becomes reset or completion"
  case, proven under real goroutine contention, not just Task 10's
  single-threaded failure-policy tests); an append landing in the unknown-
  outcome state concurrent with a cancellation signal (resolver rules win
  over the cancellation, matching this project's existing
  `commit_outcome_unknown` resolution precedent).
- [ ] Confirm no background task retains a pointer to mutable projection
  state after its owning call returns (a `go vet`-visible or race-
  detector-visible check, not merely a code-review claim) and that
  cleanup after a committed compaction-start uses
  `context.WithoutCancel` plus the existing bounded terminal-commit
  pattern, matching §17's stated cleanup discipline.
- [ ] Run:

```bash
go test -race ./internal/harness/application/... -run 'Concurrency|Winner|Race' -count=5 -v
go test -race ./internal/harness/domain/... -count=3
```

- [ ] Commit: `test(application): context compaction concurrency and cancellation matrix`.

### Task 13: SQLite migration 5 and `ContextCheckpointStore`

**Files:**

- Create: `internal/harness/adapters/sqlite/migration5.go`
- Modify: `internal/harness/adapters/sqlite/{migrations.go,migrations_sql.go}` (`latestMigrationVersion` 4 → 5)
- Create: `internal/harness/adapters/sqlite/context_checkpoint.go`, `context_checkpoint_test.go`
- Modify: `internal/harness/adapters/memory/event_store.go` (memory `ContextCheckpointStore`)
- Modify: `internal/harness/adapters/sqlite/{fault,validate}.go` (fault-injection and corruption coverage)

- [ ] `migration5.go` (matching `migration4.go`'s own dedicated-file
  precedent): adds `context_checkpoint_heads` (§14.1) — `session_id`
  primary key referencing `event_streams`, `checkpoint_event_sequence`,
  `checkpoint_event_id` unique referencing `events`, `checkpoint_id`
  unique, `covered_through_sequence`, `source_digest BLOB(32)`,
  `updated_at_commit_position` referencing `event_appends`. Confirm
  `latestMigrationVersion = 4` (`migrations.go:9`) is still current
  immediately before this task lands; if a concurrent change already
  claimed migration 5, this task becomes migration 6 and says so in its
  commit message.
- [ ] SQLite `ContextCheckpointStore.LoadLatestContextCheckpoint`: joins
  the canonical `ContextCompactionCompleted` event for its payload,
  verifies row/event agreement. Before accepting a new completed
  checkpoint into the row, independently re-verifies its coverage
  boundary and hash chain against canonical events (Task 2's digest
  logic) inside the same append transaction — an initial checkpoint scans
  from `D0`; a successor starts from the indexed predecessor digest and
  scans only the newly covered range; a same-coverage rewrite requires an
  identical digest. Any verification failure rolls back the event and the
  row projection together. The read port returns `none`, `found`, or a
  classified `store_corrupt`/store error — it never fabricates a
  checkpoint.
- [ ] Memory adapter: the same bounded row semantics over copied Go
  values, sharing the same conformance test suite as SQLite (matching
  this project's existing memory-and-SQLite-share-one-suite precedent,
  `docs/architecture/eventstore-v2.md`).
- [ ] Unit tests: migration applies cleanly on a fresh database and is
  idempotent on reopen (matching `TestUserVersionTracksLatestMigration`'s
  own pattern, `migrations_test.go:89`); an accepted completion updates
  the row only after hash-chain verification passes; a completion whose
  claimed digest does not match canonical events is rejected and neither
  the event nor the row commits; SQLite fault injection during the row
  update rolls back the whole transaction (event included); memory and
  SQLite pass the identical conformance suite.
- [ ] Mutation check (source filter/digest, storage-layer half): skip the
  independent re-verification and trust the completion event's claimed
  digest directly, confirm the mismatched-digest rejection test stops
  rejecting, restore.
- [ ] Run:

```bash
go test ./internal/harness/adapters/sqlite/... -run 'Migration5|ContextCheckpoint|Digest' -count=1 -v
go test ./internal/harness/adapters/memory/... -run 'ContextCheckpoint' -count=1 -v
go test -race ./internal/harness/adapters/sqlite/... -count=1
```

- [ ] Commit: `feat(sqlite,memory): context checkpoint store, migration 5, and hash-chain verification`.

### Task 14: Projection recovery and Runtime Host crash reconciliation

**Files:**

- Modify: `internal/harness/adapters/sqlite/rebuild.go`
- Modify: `internal/harness/runtime/reconcile.go`
- Modify: `internal/harness/adapters/sqlite/{auditimport,backfill}.go` (JSONL import interaction)

- [ ] `RebuildAndVerifyContextCheckpointHeads` (§14.2): scans canonical
  events, validates checkpoint shape and successor rules, chooses the
  furthest valid coverage, rebuilds the row. Same-coverage rewrites follow
  valid predecessor lineage; remaining ties break on canonical sequence,
  never wall-clock alone. JSONL import rebuilds the projection only after
  every existing audit/replay verification layer passes (reuses the
  existing import ordering, does not reorder it).
- [ ] Runtime Host reconciliation (§14.4): extend `reconcile.go`'s
  existing compact replay scan so an unmatched `ContextCompactionStarted`
  becomes `ContextCompactionFailed{Code: "runtime_recovered"}` under
  current fencing authority; a matched completion/failure needs no
  action; no summary or reset is ever synthesized during recovery; an
  unknown recovery-append outcome uses the existing stable recovery
  Append ID and resolver pattern; reconciliation closes an active
  compaction before it terminalizes an enclosing active Turn, preserving
  Domain eligibility ordering.
- [ ] Unit tests: missing derived state (row deleted or never created) is
  repaired by the rebuild path from canonical events alone; a projection
  that disagrees with its canonical event is reported `store_corrupt`,
  never silently trusted; a simulated crash mid-compaction (an
  unmatched `ContextCompactionStarted` in the log) reconciles to `failed`
  on the next startup, with the enclosing Turn's own terminalization
  correctly ordered after it; JSONL import round-trips a Session
  containing compaction events and rebuilds a matching checkpoint-heads
  row afterward.
- [ ] Run:

```bash
go test ./internal/harness/adapters/sqlite/... -run 'Rebuild|Import' -count=1 -v
go test ./internal/harness/runtime/... -run 'Reconcile|Recover' -count=1 -v
go test -race ./internal/harness/runtime/... -count=1
```

- [ ] Commit: `feat(sqlite,runtime): context checkpoint recovery and crash reconciliation`.

### Task 15: Composition wiring, config, and adapter/protocol projection effects

**Files:**

- Modify: `internal/harness/composition/{config,assembly}.go`, `*_test.go`
- Modify: `internal/harness/adapters/acp/project.go`, `*_test.go`
- Modify: `internal/harness/transcript/{codec,export}.go`, `*_test.go`

- [ ] `composition.Config` gains `Context` (`TriggerPercent`,
  `TargetPercent`, `TailPercent`, `MaxSummaryChunks`,
  `MaxOverflowCompactionsPerTurn`, `MaxPrunedToolResultsPerRequest`,
  `CompactionTimeout`, §21). Zero scalar values receive §8's defaults;
  invalid relationships (e.g. `TailPercent >= TargetPercent`) fail before
  any resource construction — no partial assembly on invalid config.
- [ ] Construction order (§21): Runtime Host/store → conversation Provider
  + runner → Context meter/engine + summarizer (wired over the
  already-constructed conversation Provider, per Task 9's
  `ContextSummarizer`, no second Provider) → workspace tools/catalog →
  Application service → ACP adapter. Every resource constructed after
  Host launch participates in the existing release-on-failure path.
- [ ] ACP (`adapters/acp/project.go`, §20.4): confirm `session/load`
  continues projecting canonical user/assistant/Tool facts only — a
  checkpoint never replaces visible conversation history in the ACP
  projection, and no context event is mapped to a fabricated
  `session/update` or plan update. Add the explicit non-projection test
  the design calls for, not just an absence of a positive test.
- [ ] Transcript (`transcript/{codec,export}.go`, §20.3): the strict event
  codec accepts all four new context event types and rejects unknown/
  extra fields exactly as every existing event type does; transcript
  projection adds bounded facts for compaction start/completed/failed and
  context preparation — completed includes checkpoint metadata and the
  summary itself, since the transcript is an explicit content-bearing
  export (distinct from ACP, which stays canonical-only). Golden fixtures
  and hashes change intentionally; update them with a stated reason in
  the commit, not silently.
- [ ] Unit tests: `composition.Open` rejects invalid Context config before
  constructing any resource; a full assembly with Context configured
  passes one real tool-calling turn through composition's existing
  fixture-driven-provider test pattern
  (`README.md`: "runs one tool-calling turn against a real database and
  a fixture-driven provider"); `session/load` non-projection test;
  transcript export/golden-hash update for a Session containing a
  compaction.
- [ ] Run:

```bash
go test ./internal/harness/composition/... -run 'Context|Assembly' -count=1 -v
go test ./internal/harness/adapters/acp/... -run 'Load|Context' -count=1 -v
go test ./internal/harness/transcript/... -count=1 -v
go test -race ./internal/harness/composition/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(composition,acp,transcript): wire Context Engine into assembly and projection surfaces`.

### Task 16: Evidence, benchmarks, and documentation

**Files:**

- Create: `docs/architecture/context-engine.md`, `.zh-CN.md`
- Create: `docs/architecture/context-engine-evidence.md`
- Create: `internal/harness/contextengine/bench_test.go` (or equivalent benchmark location)
- Modify: `docs/getting-started.md`
- Modify: `docs/README.md`, `README.md`

- [ ] Fill any remaining gaps in the fixture/scenario/store/recovery test
  suites named across §22.1–22.3 that Tasks 1–15 did not already cover
  standalone (cross-check against §22's own bullet list explicitly; do
  not assume coverage without checking).
- [ ] Confirm every mutation check named in Tasks 1, 2, 5, 6, 9, 10, and
  13 above actually ran and is recorded — this task's own job is to
  verify the full §22.4 mutation-kill list (trigger comparison, output/
  safety reserve, tool-pair boundary, source filter/digest,
  append-before-use, summary shrink validator, reset hard-limit gate,
  overflow attempt cap, checkpoint current-budget replay check) is
  covered end to end, not to add new mutation checks itself.
- [ ] Benchmarks over equivalent 100-Turn, 1,000-Turn, and 10,000-Turn
  streams (§22.4): below-trigger and checkpoint-replay live heap bounded
  by configured token budget, not Turn count; normal checkpoint lookup
  bounded, not a full-stream scan; cold projection rebuild may be
  `O(history)` but stays paged and heap-bounded; no request exceeds
  `hardInput` or 4 MiB before dispatch; every accepted compaction
  demonstrates measured shrink; no GA latency claim from a single-machine
  sample.
- [ ] Write `docs/architecture/context-engine.md` (full English normative
  implemented contract — a new subsystem, not an extension of an existing
  one) and its Chinese reading copy: scope, the budget/meter contract, the
  projection/planning/checkpoint pipeline, the four triggers, the failure
  algebra, resource bounds, and every §4 non-goal restated as a stated
  exclusion, not silently absent.
- [ ] Write `docs/architecture/context-engine-evidence.md`: a commit
  table for the gate/design/plan/Tasks 1–16, mapping tables (mechanism →
  test → mutation result) per pure-package concern and per Application
  trigger, real verification command output for every command block in
  Tasks 1–15, the benchmark run's actual output, deviations from this
  plan's file map if any arose, and remaining blockers — explicitly
  naming that the milestone stays "not GA" until real-model quality
  evaluation and wider provider coverage exist (§23), matching every
  prior evidence ledger's own honesty convention.
- [ ] Extend `docs/getting-started.md` (read it first — it already
  documents `och`'s other flags and `export-session` in the same style)
  with a new section on automatic compaction (what an operator observes
  when it triggers) and manual compaction (`och compact-session` usage,
  flags, output shape).
- [ ] Update `docs/README.md`'s milestone 8 entry to distinguish already-
  implemented persistence/recovery from the newly implemented Context
  Engine (matching how milestone 6's prose was updated after ACP session
  lifecycle landed), add authority-table rows for the new implemented
  contract/evidence/reading-copy, and update this plan's own row from
  "Implemented plan" phrasing if the table distinguishes plan-accepted
  from plan-executed status elsewhere. Update root `README.md`'s "Current
  status" bullets in the same style as the exec CPU quota and secret
  redaction entries there.
- [ ] Run:

```bash
go test ./... -count=1
go test -race ./... -count=1
go test ./internal/harness/contextengine/... -bench . -benchmem -run '^$'
go vet ./...
CGO_ENABLED=0 go build ./...
go test ./internal/docsguard/... -v
git diff --check
```

- [ ] Commit: `docs: publish the Context Engine contract, evidence, and getting-started guide`.

## Final Completion Gate

- [ ] Run `gofmt -w` on changed Go files and verify `gofmt -l` prints
  nothing for them.
- [ ] Run `go vet ./...`.
- [ ] Run `CGO_ENABLED=0 go build ./...`.
- [ ] Run `go test ./... -count=1` and `go test -race ./... -count=1`.
- [ ] Run `go test -race -count=5 ./internal/harness/application/... ./internal/harness/domain/...` to exercise the compaction lifecycle, orchestration, and race matrix repeatedly.
- [ ] Confirm every §22.4 mutation-kill target was actually killed and recorded in the evidence ledger, not merely planned (Task 16's own job, re-verified here as the final gate).
- [ ] Confirm no task reintroduced a second Provider/credential/transport for summarization (Global Constraints) — a final read of `composition/{config,assembly}.go`'s diff, not just a test-passing check.
- [ ] Confirm no task widened `MaxProjectionBytes` or otherwise let a Provider request exceed `hardInput`/4 MiB in any code path, including the overflow-retry path.
- [ ] Confirm `internal/harness/contextengine` still imports nothing but Domain value types (CE-01) — a dependency-boundary check, not an assumption.
- [ ] Confirm `och compact-session` and automatic compaction both actually ran end to end at least once during this plan's execution (Task 11, Task 16), with real output captured in the evidence ledger — a design requiring real triggers is not satisfied by tests that only ever exercise the pure package in isolation.
- [ ] Confirm `docs/README.md`'s milestone 8 prose change is honest about scope: real-model quality evaluation and wider provider coverage remain open, and the entry says so explicitly rather than implying GA.
- [ ] Request code review, address findings with focused regression tests, then create a final implementation/evidence commit if review changes are needed.
