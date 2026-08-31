# Secret Redaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close `SECURITY.md`'s "no secret redaction" gap for tool call
results/failure messages and the final assistant message text, by
consolidating this project's own existing narrow `redactSecrets`
(`openaicompat`) into a new, shared, more broadly applicable
`internal/harness/redact` package, and calling it once, upstream, at
every point in `internal/harness/application` where a model-visible
string is about to become a domain command.

**Architecture:** A new dependency-free leaf package,
`internal/harness/redact`, importable by any layer (`application`,
adapters) without creating a dependency cycle. `application`'s existing
tool-result and assistant-message-completion construction sites call it
before constructing the corresponding `domain` command, so the domain
event, the JSONL audit replica, and both the live and replayed ACP
`session/update` projections all receive the already-redacted value from
one choke point. No existing contract's wire/DB shape changes — only the
bytes inside three already-existing string fields do.

**Tech Stack:** Go 1.26, standard library only (`regexp`, `strings`) for
`internal/harness/redact`. No new module dependency.

**Spec:** `docs/superpowers/specs/2026-08-31-secret-redaction-design.md`
(English normative, Accepted); synchronized Chinese summary at
`docs/superpowers/specs/2026-08-31-secret-redaction-design.zh-CN.md`.
Research: `docs/research/architecture-gates/2026-08-30-secret-redaction.md`.
No Chinese reading copy for this plan, matching the two most recent prior
plans' precedent.

## Global Constraints

- `internal/harness/redact` imports nothing from this repository's own
  module — standard library only — so it can be safely imported from
  `application`, `openaicompat`, or any future adapter without creating a
  new dependency-boundary rule beyond adding it to
  `internal/harness/architecture/dependencies_test.go`'s existing owner
  table (a leaf package like `policy`/`tools`, not requiring a new
  forbidden-import row of its own since nothing needs to be forbidden
  from importing it).
- Every redaction call site replaces the *entire* string used to
  construct the corresponding `domain` command — never a truncated or
  partially-substituted copy — and does so *before* that command is
  constructed, never after, so this project's own existing truncation
  logic (`toolTextContent`'s 16 KiB clip, `MaxToolResultBytes`) always
  runs downstream of redaction, per the design's §1.5.
- Tool call arguments and live `model.text.delta` streaming chunks are
  never touched by any task in this plan — the design's own §2 non-goals,
  not something a task should "helpfully" extend to.
- Every task follows red-green-refactor: write the focused test, observe
  it fail for the right reason, then implement, then run the focused
  package tests green before moving on.
- Every task that changes a call site producing a redacted value adds a
  mutation check to its own verification: revert the redaction call,
  confirm the corresponding test fails for the right reason, restore it.
  This project's own history (this session's live-tool-card-fidelity fix)
  found a real case where reverting an emission-path change passed the
  whole suite silently — every task here must not repeat that.
- `CGO_ENABLED=0 go build ./...` and `gofmt -l` stay clean after every
  task.

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/harness/redact/redact.go` (+`_test.go`) | `Text(string) string`: the hardcoded, shape-specific pattern set from the design's §4 |
| `internal/harness/adapters/openaicompat/classify.go` (+`_test.go`) | Delete the private `redactSecrets`/pattern vars; call `redact.Text` instead |
| `internal/harness/application/pipeline.go` | `completeToolAndContinue`/`failToolAndContinue` redact `content`/`message` before constructing `domain.CompleteToolCall`/`FailToolCall` and the `engine.RuntimePayload` emit |
| `internal/harness/application/loop.go` | `runResult.Text` redacted before `domain.CompleteAssistantMessage`/`CompleteAssistantTurn` construction |
| `internal/harness/application/loop_test.go` | New tests proving redaction reaches the domain event, the `RuntimeEvent`, and (via `adapters/acp`) both live and replayed ACP projections |
| `docs/architecture/secret-redaction.md`, `.zh-CN.md` | New implemented-contract doc |
| `docs/architecture/secret-redaction-evidence.md` | New evidence ledger |
| `docs/README.md`, `README.md` | Authority-table rows, milestone/summary prose |

---

### Task 1: `internal/harness/redact` package

**Files:**

- Add: `internal/harness/redact/redact.go`
- Add: `internal/harness/redact/redact_test.go`

- [ ] Define `func Text(s string) string` applying, in order, the pattern
  set from the design's §4 table: `Authorization: ...`, `Bearer ...`,
  provider-style secret keys (`sk-`, `sk-ant-`, `sk-proj-` prefixes),
  `?key=`/`&key=` query parameters (preserving the parameter name), a
  generic case-insensitive `(key|token|secret|password|credential)\s*[:=]\s*\S+`
  assignment shape, AWS access key IDs (`AKIA`/`ASIA` + 16 alphanumerics),
  GitHub tokens (`gh[pousr]_...`, `github_pat_...`), and PEM private-key
  blocks (`-----BEGIN ... PRIVATE KEY-----` through the matching `END`
  line, spanning newlines).
- [ ] Every match's *value* is replaced with the literal marker
  `[redacted]`; where a pattern captures a key name or prefix (the query
  parameter, the generic assignment), that captured text is preserved and
  only the value portion becomes `[redacted]` — matching the design's §5
  decision to standardize on a marker instead of `redactSecrets`'s old
  empty-string replacement.
- [ ] Add unit tests, one per pattern in isolation, plus: a string with no
  secret-shaped content passes through byte-for-byte unchanged; two
  distinct secrets in one string are both redacted; a secret embedded in
  otherwise-ordinary surrounding text (simulating a `.env`-shaped line
  inside a larger file read) is redacted without corrupting the
  surrounding text; a multi-line PEM block is redacted as one match, not
  line-by-line; the known false-positive case from the design's §9 (a
  comment containing `secret = soon`) is demonstrated with an explicit
  test asserting it *is* redacted — proving the design's accepted trade
  is real and tested, not merely described in prose.
- [ ] Mutation check: comment out each pattern from the set one at a time,
  confirm that pattern's own dedicated test fails for the right reason
  (the input containing that secret shape is no longer redacted), then
  restore it — proving each pattern is actually load-bearing for its own
  test, not merely present.
- [ ] Run:

```bash
go test ./internal/harness/redact/... -count=1 -v
go test -race ./internal/harness/redact/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(redact): hardcoded shape-specific secret redaction`.

### Task 2: Consolidate `openaicompat`'s narrow precedent onto the shared package

**Files:**

- Modify: `internal/harness/adapters/openaicompat/classify.go`
- Modify: `internal/harness/adapters/openaicompat/classify_test.go` (or
  wherever `TestProviderFailureErrorNeverRendersSecrets` currently lives)

- [ ] Delete `classify.go`'s private `redactSecrets` function and its
  four pattern variables (`reAuthorization`, `reBearer`, `reSecretKey`,
  `reQueryKey`); `safeMessage` and `startupFailure` call
  `redact.Text` directly instead.
- [ ] Update `TestProviderFailureErrorNeverRendersSecrets` to assert the
  new `[redacted]` marker output (Task 1's behavior) rather than the old
  empty-string replacement — a disclosed, intentional behavior change per
  the design's §5, in the same commit as the migration, not a silent
  side effect discovered later.
- [ ] Confirm no other test in `openaicompat` asserted the old
  empty-string behavior incidentally; update any that did with the same
  disclosure.
- [ ] Run:

```bash
go test ./internal/harness/adapters/openaicompat/... -count=1 -v
go test -race ./internal/harness/adapters/openaicompat/... -count=1
```

- [ ] Commit: `refactor(openaicompat): consolidate secret redaction onto internal/harness/redact`.

### Task 3: Redact tool call results and failure messages

**Files:**

- Modify: `internal/harness/application/pipeline.go`
- Modify: `internal/harness/application/loop_test.go` (new tests
  alongside this session's existing `TestRuntimeToolExecutionCompletedCarriesResultContent`
  and `TestRuntimeToolExecutionFailedCarriesFailureMessageAsContent`)

- [ ] `completeToolAndContinue`: redact `content` once, at the top of the
  function, before both the `domain.CompleteToolCall{Content: ...}`
  construction and the `engine.RuntimePayload{..., Content: ...}` emit
  that follows in the same function — one redacted value, both
  consumers.
- [ ] `failToolAndContinue`: redact `message` once, symmetrically, before
  both `domain.FailToolCall{Message: ...}` and the
  `engine.RuntimePayload{..., Content: ...}` emit.
- [ ] Add an `application`-level test (extending this session's existing
  `TestTwoStepReadFileSuccess`/`runtimeEventOfType` pattern — a real
  `RunTurn`, not a synthetic `RuntimeEvent` literal): a `read_file` result
  containing a recognizable secret shape (e.g. an `AKIA...` string, or a
  `.env`-shaped `API_KEY=...` line) is redacted in
  `sink.Delivered()`'s `RuntimeToolExecutionCompleted.Content`.
- [ ] Add a second such test for the failure path: a tool failure whose
  message would otherwise carry secret-shaped content is redacted in
  `RuntimeToolExecutionFailed.Content` (construct this via whatever
  existing failure path in `internal/harness/application` can be made to
  carry attacker-influenced text — if none can today, this sub-item
  becomes "assert `failToolAndContinue`'s redaction call directly via a
  package-internal test", not skipped).
- [ ] Add an `adapters/acp`-level test proving the redacted value reaches
  both the live and replayed `session/update` `content` field — extending
  `TestProjectRuntimeEvent`'s existing cases (this session's own
  `tool.execution.completed carries result content` case) with a secret-
  shaped `Content` value, asserting the projected `toolCallContent` text
  is the redacted form, not the original.
- [ ] Mutation check: revert each of the two `pipeline.go` redaction
  calls independently, confirm the corresponding new `application`-level
  test fails for the right reason (the secret shape survives unredacted
  in the captured `RuntimeEvent`), then restore.
- [ ] Run:

```bash
go test ./internal/harness/application/... -run 'Redact|Secret' -count=1 -v
go test ./internal/harness/adapters/acp/... -run 'ProjectRuntimeEvent' -count=1 -v
go test -race ./internal/harness/application/... ./internal/harness/adapters/acp/... -count=1
```

- [ ] Commit: `feat(application): redact secrets from tool call results and failure messages`.

### Task 4: Redact the final assistant message text

**Files:**

- Modify: `internal/harness/application/loop.go`
- Modify: `internal/harness/application/turn_success_test.go` (or
  `loop_test.go`, matching wherever assistant-completion tests already
  live)

- [ ] Redact `runResult.Text` once, immediately before each of the two
  construction sites named in the design's §3: `domain.CompleteAssistantMessage{...,
  Text: ...}` and `completeAssistantTurn`'s `domain.CompleteAssistantTurn{...,
  Text: ...}`.
- [ ] Confirm (do not assume) that `FailAssistantTurn`/`InterruptAssistantTurn`'s
  `Message` fields are never redaction targets: re-verify directly that
  every call site constructing either command passes a value derived from
  `displayFailureSentence`'s fixed, code-keyed sentence table
  (`turn.go:466`), never raw model or provider text, before leaving them
  untouched — the design's own claim, checked again here rather than
  taken on faith from the design document.
- [ ] Add an `application`-level test: a scripted model response whose
  final text contains a recognizable secret shape is redacted in the
  resulting `RunTurnResult.Text` and in the persisted
  `domain.AssistantMessageCompleted.Text` (read back via
  `ReadWholeStreamPinned`, matching this project's own existing
  durable-read-back test pattern).
- [ ] Mutation check: revert each of the two `loop.go` redaction calls
  independently, confirm the corresponding test fails for the right
  reason, then restore.
- [ ] Run:

```bash
go test ./internal/harness/application/... -run 'Redact|Secret|AssistantMessage' -count=1 -v
go test -race ./internal/harness/application/... -count=1
```

- [ ] Commit: `feat(application): redact secrets from the final assistant message`.

### Task 5: Publish the implemented-contract documentation and evidence

**Files:**

- Add: `docs/architecture/secret-redaction.md`, `.zh-CN.md`
- Add: `docs/architecture/secret-redaction-evidence.md`
- Modify: `docs/README.md`, `README.md`

- [ ] Write `docs/architecture/secret-redaction.md` following this
  project's established implemented-contract format (status/stability/
  maturity header, scope, the exact pattern set with its `[redacted]`
  marker behavior, the four call sites and why each is or is not a
  redaction target, explicit exclusions for tool arguments and live text
  deltas). Full English/Chinese parity (not a condensed summary), matching
  the implemented-contract convention this project uses for shipped code
  (as distinct from the Draft/Accepted design's own condensed Chinese
  summary).
- [ ] Add `docs/architecture/secret-redaction-evidence.md`: a commit table
  for Tasks 1–5, a mapping table of tests per pattern and per call site,
  the actual verification command output, every mutation check's result,
  a "Deviations from this plan's file map" section if any arose (matching
  this project's own established precedent of disclosing such things
  rather than silently absorbing them — for example if Task 3's failure-
  path test required a different construction than planned), and a
  "Remaining" section naming tool-call-argument redaction, live
  `model.text.delta` redaction, entropy-based detection, and the Provider
  API key's compile-time redaction type as excluded by design, not
  deferred without a stated reason.
- [ ] Add authority-table rows to `docs/README.md` for the new
  implemented contract, its reading copy, and the evidence ledger. Update
  `README.md`'s summary/Security section to mention this closes part of
  `SECURITY.md`'s "no secret redaction" gap, matching how other slices'
  summaries were updated when they shipped.
- [ ] Update `SECURITY.md` itself: move the closed portion of "no secret
  redaction" out of "Not enforced" and into "Enforced," stating precisely
  what is and is not covered (tool results and assistant text; not
  arguments, not live deltas, not an exhaustive secret scanner) — the
  same precision this project's existing "Enforced"/"Not enforced" split
  already requires for `exec` sandboxing.
- [ ] Run:

```bash
go test ./internal/docsguard/... -v
git diff --check
```

- [ ] Commit: `docs: publish the secret redaction contract and evidence`.

## Final Completion Gate

- [ ] Run `gofmt -w` on changed Go files and verify `gofmt -l` prints
  nothing for them.
- [ ] Run `go vet ./...`.
- [ ] Run `CGO_ENABLED=0 go build ./...`.
- [ ] Run `go test ./... -count=1`.
- [ ] Run `go test -race ./... -count=1`.
- [ ] Confirm every mutation check across Tasks 1, 3, and 4 was actually
  performed and its result recorded in the evidence ledger, not merely
  planned — matching this project's own established rigor bar (a
  mutation test that "should" catch a regression is not evidence until
  it is actually run and observed failing for the right reason).
- [ ] Confirm `SECURITY.md`'s updated "Enforced" bullet precisely states
  the scope of what this plan actually redacts, with no broader claim
  than Tasks 1–4 actually deliver.
- [ ] Request code review, address findings with focused regression
  tests, then create a final implementation/evidence commit if review
  changes are needed.
