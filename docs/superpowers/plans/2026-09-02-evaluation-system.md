# Evaluation System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build milestone 10 as OCH's product-regression platform: execute
frozen Scenarios through real OCH Session paths, isolate every Attempt, publish
append-only evidence, support deterministic offline regrading and paired
reports, then add the ACP-subprocess and explicit live-model lanes without
creating an eval-only shortcut through Application, Context Engine, Provider,
or storage.

**Architecture:** `internal/harness/eval` owns frozen experiment documents,
matrix expansion, attempt orchestration, approval matching, artifact
publication, evidence readers, scoring, and reports. It depends inward on
Composition and the public Application surface, but never on concrete SQLite,
Context Engine, Provider, or tool adapters. `internal/harness/composition` owns
the cold read-only store inspection/export boundary needed for trustworthy eval
evidence. The in-process executor drives `composition.Open` directly; the ACP
executor builds and supervises the real `och` binary and drives it through
`internal/client/acp`. Both compile the same Scenario approval script and
normalize only declared semantic facts. `cmd/och-eval` is orchestration and
presentation, not a second runtime.

**Tech Stack:** Go 1.26; the repository's existing standard-library and
`golang.org/x/*` dependencies; SQLite through the existing adapter; native ACP
JSON-RPC through `internal/client/acp`; OpenAI-compatible fixture/live routes
through the existing Composition root. No container scheduler, external agent
adapter, embedded scripting language, or new Provider abstraction is added in
this milestone.

**Spec:** `docs/superpowers/specs/2026-09-02-evaluation-design.md` (English
normative, Accepted) and its synchronized Chinese reading copy
`docs/superpowers/specs/2026-09-02-evaluation-design.zh-CN.md`. Architecture
gate: `docs/research/architecture-gates/2026-09-02-evaluation-system.md` and
Chinese reading copy. Research inputs and their adopted constraints are
recorded in the design: Pi, Maka, Harbor/Terminal-Bench, Inspect AI,
vitest-evals, Grok Build, and OpenAI Evals.

## Delivery shape

Milestone 10 is delivered in three stages. Stage A is the deterministic,
in-process foundation and must be independently useful before subprocess work
lands. Stage B adds the real ACP subprocess executor and semantic parity. Stage
C adds the initial suites, explicit live quality lane, benchmarks, and operator
documentation. A stage is not a single PR: every task below is a reviewable,
green commit/PR slice.

The pre-existing PR stack is implementation material, not an alternate
contract. Reconcile it in this order before adding new code:

| Existing work | Disposition |
| --- | --- |
| PR #122 | Superseded by accepted design clarification PR #130; do not revive. |
| PR #123, identity models | Reuse after adding stable action IDs, approval-script entries, restart modes, and the eval-owned `EvaluationResult` read DTO. Rebase onto current `main`. |
| PR #124, attempt/outcome store | Reuse after Task 2's fault/publication tests pass; rerun the previously failing Go/race jobs. |
| PR #125, EvalSet/matrix | Reuse after Task 3 verifies every tightened limit and capability contract. |
| PR #126, fixture isolation | Reuse only after replacing the unconditional `syscall.Stat_t` assertion with platform-specific helpers; Windows must cross-build. |
| PR #127, audit snapshot verification | Keep as a low-level verification primitive. It does not satisfy section 14's cold inspection/export APIs by itself. |
| PR #128, shutdown audit flush | Make its dependency explicit by merging/retargeting it after #127; `sqlite.VerifyAuditReplica` cannot compile without #127. Extend tests to prove flush-before-lease-release semantics. |
| PR #129, in-process executor | Rebase only after Tasks 1–5. Replace ignored deferred `Assembly.Close`, wire the shared approval matcher, and add cancellation/clean restart before merging. |
| Uncommitted `internal/harness/eval/evidence.go` | Do not commit as written. It copies the live audit directory and uses `ExportSession`; rewrite it in Task 7 against the new Composition-only cold evidence APIs. |

Do not merge a red PR to preserve stack order. Update an existing owner branch
in place when safe; otherwise supersede it with a clean replacement PR. Never
force-push someone else's active branch without explicit coordination.

## Global Constraints

- **Real product paths only.** In-process execution calls `composition.Open`
  and public Application methods. ACP execution launches the exact `och`
  binary and uses `internal/client/acp`. No eval-only Service, Provider, Context
  Engine, Store, or fake in-memory ACP path may satisfy a conformance test.
- **Eval stays outside the domain event stream.** `Attempt`, `Outcome`,
  `EvidenceManifest`, `Score`, `Report`, and `EvaluationResult` are eval-owned
  documents/read DTOs. No `evaluation.result` Domain event or Session event is
  added.
- **Immutable identity and append-only results.** Scenario, Subject, Executor,
  and EvalSet digests are canonical and frozen. `attempt.json` publishes before
  Subject startup; `outcome.json` publishes at most once; `manifest.json`
  publishes last; every regrade creates a new Score path.
- **Evidence is cold and bounded.** Eval never imports SQLite, opens a live
  Assembly for collection, copies a live audit replica, or reads a path absent
  from the manifest. Composition's eval helpers refuse a live writer lease and
  regenerate transcript/audit evidence from the isolated database into empty
  destinations.
- **Different coordinate systems remain explicit.** Session head sequence and
  global store commit position are stored separately. Inclusion is proven by
  the commit position of the append containing the pinned Session head, never
  by numerically comparing sequence with commit position.
- **One approval contract.** Match `{prompt action ID, zero-based ordinal,
  expected tool name}`. Generated tool-call/request IDs are evidence only.
  Undeclared, exhausted, out-of-order, or mismatched requests fail closed,
  consume no later declaration, and emit `approval_script_violation`.
- **Outcome is not quality.** A script denial/violation can still yield
  `completed`; deterministic Score decides whether observed behavior met the
  Scenario. Missing or contradictory required evidence can never pass.
- **Restart semantics are executor-specific.** `clean_shutdown` belongs to
  both executors. `interrupt` and `kill` are ACP-only; in-process Cells asking
  for them fail validation before Attempt publication. An in-process executor
  never simulates a crash by abandoning an Assembly.
- **ACP ownership is in-memory and current.** Signal only a currently owned,
  not-yet-reaped process group. Never signal a PID recovered from disk. A new
  writer or compactor starts only after reap and receives a distinct runtime
  ID.
- **ACP runtime is Unix-only in v1.** Linux/macOS use process groups. Windows
  cross-builds and tests fail-closed validation; it does not claim subprocess
  support until a separate Job Object design is accepted.
- **Fixture lane is offline.** It accepts loopback fixture HTTP only, no live
  credential requirement and no external network. Live requires both a live
  EvalSet and `--live` before reading a credential.
- **All bounds are enforced facts.** Wall/action/startup/cancel/shutdown/
  collection time, concurrency, tokens, optional cost, fixture and artifact
  sizes, stdout/stderr, and the 4,096 expansion ceiling must fail or truncate
  with structured facts, never only logs.
- **No secret-shaped data in identity or diagnostics.** Capture the credential
  environment-variable name only. Never capture the full process environment.
  Redact structured documents and logs before publication.
- **No sleep-based lifecycle tests.** Use channels, contexts, controlled child
  fixtures, and bounded test deadlines. Process-leak tests must reap and prove
  cleanup.
- **Red-green-refactor per task.** Add a failing test first, run it to observe
  the intended failure, implement the smallest contract, rerun targeted and
  package tests, then commit. `gofmt`, `go vet ./...`, and
  `CGO_ENABLED=0 go build ./...` remain clean after every task.
- **Ordinary PR eval stays tiny.** Its checked-in EvalSet expands to exactly
  four Cells: one paired parity Scenario through both executors, one
  in-process tool/approval/failure Scenario, and one in-process Context
  compaction Scenario. Full deterministic matrices are explicit/scheduled;
  live/model-judge Cells never run in ordinary PR CI.

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/harness/eval/model.go`, `id.go`, `digest.go` | Frozen Scenario/Subject/Executor schemas, stable action coordinates, approval and restart contracts, canonical identity digests |
| `internal/harness/eval/{attempt,outcome,manifest,score,result}.go` | Append-only execution/scoring documents and the eval-owned `EvaluationResult` read DTO |
| `internal/harness/eval/{store,evalset,matrix}.go` | Atomic document publication, frozen EvalSet, bounded deterministic expansion and resume classification |
| `internal/harness/eval/{fixture,fixture_stat_unix,fixture_stat_windows}.go` | Per-Attempt roots and cross-platform safe fixture copying |
| `internal/harness/eval/approval.go` | Shared stateful approval matcher and evidence observations; adapters compile from this matcher |
| `internal/harness/eval/inprocess.go` | Real Composition/Application executor with prompt, compact, collect, cancel, and clean restart |
| `internal/harness/eval/evidence.go` | Post-shutdown bounded evidence collection through Composition APIs only |
| `internal/harness/eval/runner.go`, `recovery.go`, `limits.go` | Attempt orchestration, scheduling, resume, classification, and resource accounting |
| `internal/harness/eval/{artifact_reader,verifier,scorer,regrade}.go` | Manifest-constrained reads, deterministic verification, append-only offline regrade |
| `internal/harness/eval/{report,parity}.go` | Pairing, semantic normalization, parity comparison, derived reports |
| `internal/harness/composition/evaluation.go` | `InspectEvaluationStore` and `ExportEvaluationEvidence` public read-only boundary |
| `internal/harness/adapters/sqlite/evaluation.go` | Composition-private cold reader implementation; no migration, lease acquisition, or mutation |
| `internal/client/acp/permission.go` | Complete decoding of this agent's `session/request_permission` identity fields for scripted handling |
| `internal/harness/eval/acp_*.go` | ACP argv mapping, subprocess lifecycle, Unix process groups, action execution, and platform rejection |
| `cmd/och/main.go` | Complete shared Composition flags for `och -acp` and `compact-session` |
| `cmd/och-eval/main.go` | `run`, `regrade`, and `report` CLI contracts |
| `eval/scenarios/**`, `eval/sets/**` | Frozen deterministic and live suite data/fixtures |
| `docs/architecture/evaluation.md`, `.zh-CN.md` | Implemented architecture and operator contract |
| `docs/architecture/evaluation-evidence.md` | Commit, test, mutation, benchmark, deviation, and blocker ledger |

---

## Stage A — deterministic in-process foundation

### Task 1: Reconcile frozen schemas and stable action contracts

**Files:**

- Modify/rebase: `internal/harness/eval/model.go`, `model_test.go`
- Modify/rebase: `internal/harness/eval/id.go`, `id_test.go`
- Modify/rebase: `internal/harness/eval/digest.go`, `digest_test.go`
- Create: `internal/harness/eval/result.go`, `result_test.go`
- Modify: `internal/harness/eval/doc.go`

- [ ] Add a required stable `id` to every `ScenarioAction`; validate uniqueness
  within the Scenario and bound its UTF-8 byte length. Change `CancelAction`
  from slice index to `targetActionId`, require it to name an earlier prompt
  action, and reject self/forward/unknown targets.
- [ ] Add `RestartMode` values `clean_shutdown`, `interrupt`, and `kill`; require
  a mode on every restart action. Add capability derivation so abrupt modes
  require ACP-specific capabilities before an Attempt can exist.
- [ ] Add `ApprovalScriptEntry` with `promptActionId`, non-negative `ordinal`,
  `toolName`, and `answer` (`allow`/`deny`). Validate prompt references, unique
  coordinates, contiguous zero-based ordinals per prompt, non-empty bounded
  tool names, and no entries for non-prompt actions.
- [ ] Add an eval-owned `EvaluationResult` read DTO containing one immutable
  Outcome plus zero or more committed Score references. Assemble it from those
  authoritative documents on read; give it no independent wire schema, file,
  identity, or publication path, and never serialize it into a Session event.
- [ ] Keep strict JSON behavior: duplicate keys, unknown fields, unknown enum
  values, unsupported schemas/versions, non-canonical URLs, secret-shaped
  identity fields, and invalid UTF-8 all fail closed.
- [ ] Golden-test canonical JSON/digests. Prove generated IDs, timestamps,
  absolute paths, credentials, and artifacts do not affect Scenario/Subject/
  Executor semantic digests, while approval entries and restart modes do.
- [ ] Mutation check: omit approval script or restart mode from canonical
  encoding and confirm digest golden tests fail; restore.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'Scenario|Subject|Executor|Digest|EvaluationResult' -count=1 -v
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): freeze scenario actions, approvals, and result contract`.

### Task 2: Finish append-only attempt documents and atomic publication

**Files:**

- Modify/rebase: `internal/harness/eval/attempt.go`, `attempt_test.go`
- Modify/rebase: `internal/harness/eval/outcome.go`, `outcome_test.go`
- Modify/rebase: `internal/harness/eval/manifest.go`, `manifest_test.go`
- Modify/rebase: `internal/harness/eval/score.go`, `score_test.go`
- Modify/rebase: `internal/harness/eval/store.go`, `store_test.go`
- Create: `internal/harness/eval/publish_fault_test.go`

- [ ] Make Attempt carry all resolved identity digests, repetition/pairing
  coordinates, isolated absolute paths, start facts, and executor launch facts;
  validate that secret values and full environments cannot be represented.
- [ ] Complete Outcome's four statuses and structured fields: stable code,
  bounded redacted message, start/end/duration, terminal Session/Turn facts,
  limits/truncation, collection status, recovery status, and Attempt identity.
- [ ] Complete manifest entries and Score fields exactly as design sections 14
  and 20 require, including reason/detail, producer, manifest/outcome digest,
  criterion results, evidence references, contradiction/missing lists, and
  scorer resource use.
- [ ] Implement same-directory temp publication: bounded write, file sync,
  close, no-overwrite rename, and best-effort directory sync. Publish Attempt
  before execution, Outcome at most once, evidence files before Manifest, and
  each Score under a fresh ID.
- [ ] Inject faults after temp create, partial write, file sync, close, rename,
  and directory sync. Assert every failure leaves either the prior immutable
  document or an eval-owned uncommitted temp, never a partially accepted JSON
  document or overwritten result.
- [ ] Add startup temp cleanup that recognizes only the exact eval temp naming
  scheme and records a bounded diagnostic before removal; unrelated files are
  never touched.
- [ ] Mutation check: publish `manifest.json` before one required artifact and
  confirm the store/scoreability test fails; restore.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'Attempt|Outcome|Manifest|Score|Publish|Fault' -count=1 -v
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): publish immutable attempts and append-only results`.

### Task 3: Freeze EvalSets, expand bounded matrices, and isolate fixtures portably

**Files:**

- Modify/rebase: `internal/harness/eval/evalset.go`, `evalset_test.go`
- Modify/rebase: `internal/harness/eval/matrix.go`, `matrix_test.go`
- Modify/rebase: `internal/harness/eval/fixture.go`, `fixture_test.go`
- Create: `internal/harness/eval/fixture_stat_unix.go`
- Create: `internal/harness/eval/fixture_stat_windows.go`
- Create: `internal/harness/eval/platform_test.go`
- Modify: `.gitignore`

- [ ] Implement deterministic expansion in Scenario → Subject → Executor →
  repetition order; reject capability mismatches and more than 4,096 Cells
  before creating a directory or Provider resource.
- [ ] Resolve defaults and Scenario narrowing for every section 19 limit.
  Reject zero/negative resolved values, widening, concurrency above 8, wall or
  action time above hard maxima, missing token caps, and a cost cap whose frozen
  price table cannot price every selected Subject.
- [ ] Validate lane consistency: fixture sets accept loopback fixture endpoints
  and no live credential requirement; live sets require live Subjects. Freeze
  verifier/judge config digests, pairing seed, artifact root, and ordered
  references into the EvalSet digest.
- [ ] Preserve `NewAttemptRoot`'s create-new semantics and add `.eval/` to the
  root gitignore. Refuse an artifact root inside any fixture/workspace root.
- [ ] Split hard-link detection behind build-tagged helpers: Unix reads
  `syscall.Stat_t.Nlink`; Windows opens the file with `golang.org/x/sys/windows`
  and reads `ByHandleFileInformation.NumberOfLinks`. Reject link count greater
  than one on both platforms. Keep the common file free of platform-specific
  stat types so `GOOS=windows go test` compiles.
- [ ] Test normalized containment, `..`, absolute paths, symlinks, hard links,
  sockets/FIFOs/devices where supported, executable-bit-only preservation,
  file/per-file/aggregate bounds, non-empty destination, and source immutability.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'EvalSet|Matrix|Fixture|AttemptRoot|Platform' -count=1 -v
GOOS=windows GOARCH=amd64 go test -c -o /tmp/och-eval-windows.test.exe ./internal/harness/eval
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): freeze bounded matrices and portable fixtures`.

### Task 4: Add cold, verified Composition evidence APIs

**Files:**

- Create: `internal/harness/composition/evaluation.go`, `evaluation_test.go`
- Create: `internal/harness/adapters/sqlite/evaluation.go`, `evaluation_test.go`
- Modify/rebase: `internal/harness/adapters/sqlite/audit_verify.go`,
  `audit_verify_test.go`
- Modify/rebase: `internal/harness/composition/audit_snapshot.go`,
  `audit_snapshot_test.go`
- Modify: `internal/harness/transcript/export.go`, `export_test.go` to expose
  the existing bounded transcript writer to the Composition helper
- Modify: `internal/harness/architecture/dependencies_test.go`

- [ ] Define Composition-owned requests/results. `InspectEvaluationStore`
  accepts an isolated database path and Session ID, opens cold/read-only with no
  migration or writer lease, verifies schema/canonical event/context chains,
  pins database identity, Session head sequence, Session-head append commit
  position, and store head commit position, and returns bounded Session/Turn/
  compaction terminal facts.
- [ ] Return a typed live-lease refusal carrying only safe lease facts. Never
  wait, take over, release, heartbeat, or signal. Prove no destination is
  created on refusal.
- [ ] `ExportEvaluationEvidence` accepts the inspection identity plus empty
  caller-owned transcript and audit staging destinations. Reopen cold, verify
  the pinned identity/heads are unchanged, export a complete native transcript,
  regenerate canonical audit JSONL from database append records, and verify the
  generated snapshot before returning.
- [ ] Return transcript digest, audit head digest, pinned Session sequence,
  store commit position, Session-head append commit position, and an inclusion
  proof that the generated audit reaches that append. Do not compare sequence
  and commit-position numbers.
- [ ] Refactor PR #127's `VerifyAuditSnapshot` as the verification primitive;
  do not treat verification of an already-copied directory as export. The cold
  path must never copy `Config.AuditDirectory` or update exporter checkpoints.
- [ ] Snapshot table state before/after inspection/export and assert no rows or
  metadata change. Test live lease, corrupt event chain, corrupt context chain,
  missing Session, changed database between inspect/export, non-empty
  destinations, incomplete trailers, request/policy inclusion, and transcript/
  audit head mismatch.
- [ ] Add an architecture guard that `internal/harness/eval` cannot import
  `internal/harness/adapters/sqlite` and that the new Composition API remains
  read-only by type surface.
- [ ] Mutation check: remove the Session-head inclusion check and confirm the
  deliberately short audit fixture is rejected by the test; restore.
- [ ] Run:

```bash
go test ./internal/harness/composition/... ./internal/harness/adapters/sqlite/... -run 'Evaluation|Audit|Transcript|Lease|Inclusion' -count=1 -v
go test ./internal/harness/architecture/... -count=1
go test -race ./internal/harness/composition/... ./internal/harness/adapters/sqlite/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(composition): export cold verified evaluation evidence`.

### Task 5: Guarantee shutdown flush and implement the shared approval matcher

**Files:**

- Modify/rebase: `internal/harness/runtime/host.go`, `host_test.go`
- Modify/rebase: `internal/harness/runtime/host_shutdown_export_test.go`
- Create: `internal/harness/eval/approval.go`, `approval_test.go`
- Modify: `internal/client/acp/permission.go`, `permission_test.go`

- [ ] Make `Host.Shutdown` stop admission/background work, flush the audit
  exporter through the final committed store position, then release the writer
  lease and close storage within its caller's bound. Preserve idempotence and
  surface flush failure instead of reporting a clean shutdown.
- [ ] Merge/retarget PR #128 after PR #127 so `sqlite.VerifyAuditReplica` is a
  real dependency, not an undefined symbol. Test a blocked flush, corrupt live
  replica, timeout, double shutdown, and lease release ordering.
- [ ] Implement a stateful `ApprovalMatcher` from frozen script entries. Before
  each prompt call `BeginPrompt(actionID)`; each request matches current action,
  next ordinal, and exact tool name. A mismatch/undeclared/exhausted request is
  denied, records a bounded `approval_script_violation`, and does not advance
  the declaration cursor.
- [ ] Implement an in-process adapter satisfying `tools.Approver`. Implement a
  separate ACP adapter over the same matcher, not a second matching algorithm.
  Extend `internal/client/acp`'s private permission decode to retain
  `sessionId` and `toolCall.toolCallId` as evidence while `title` is the tool
  name; generated IDs never participate in the match.
- [ ] For ACP, select the offered `allow_once` or `reject_once` option by option
  kind/ID and fail closed when the expected option is absent or params are
  malformed. Never fall back to interactive prompting in eval.
- [ ] Test declared allow/deny, repeated tools, ordinal reset on the next prompt,
  out-of-order/tool mismatch/exhaustion, cancellation during permission,
  malformed ACP JSON, missing options, and parity of observations from both
  adapters. Assert violations do not automatically set Outcome status.
- [ ] Run:

```bash
go test ./internal/harness/runtime/... -run 'Shutdown|Audit|Lease' -count=1 -v
go test ./internal/harness/eval/... ./internal/client/acp/... -run 'Approval|Permission' -count=1 -v
go test -race ./internal/harness/runtime/... ./internal/harness/eval/... ./internal/client/acp/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): share fail-closed approval scripts across executors`.

### Task 6: Complete the real in-process executor

**Files:**

- Modify/rebase: `internal/harness/eval/inprocess.go`, `inprocess_test.go`
- Create: `internal/harness/eval/inprocess_cancel_test.go`
- Create: `internal/harness/eval/inprocess_restart_test.go`

- [ ] Map the validated Subject and resolved Cell limits into one
  `composition.Config`, including the scripted `Approver`, distinct Attempt
  runtime identity, fixture endpoint, all Limits/Context values, and bounded
  shutdown timeout.
- [ ] Execute prompt/compact/collect through public Application/Composition
  methods only. Track action IDs and live action contexts; cancellation cancels
  the named in-flight prompt, waits for its durable terminal result, then
  continues or terminates according to the Scenario boundary.
- [ ] Implement `clean_shutdown` by explicitly closing the Assembly, checking
  its error, reopening a fresh Assembly with a new runtime ID, and loading the
  same Session. Reject `interrupt`/`kill` before Attempt creation through
  matrix capability validation; no executor branch drops an Assembly.
- [ ] Remove `defer assembly.Close()` where its result is ignored. Funnel every
  terminal path through one bounded close function; classify unproven/failed
  closure honestly and return a stopped-writer proof needed by collection.
- [ ] Separate executor transport errors from durable Outcome classification.
  Once `attempt.json` exists, all terminal paths return the strongest Outcome
  facts; a Go error must not prevent the runner from publishing them.
- [ ] End-to-end tests use the real loopback OpenAI-compatible fixture server,
  `composition.Open`, SQLite Runtime Host, tool catalog, approvals, manual
  compaction, cancellation, and clean restart/load. Assert no reused Assembly,
  Session, directory, or runtime ID across Attempts.
- [ ] Mutation check: replace the Composition executor with a direct Service/
  fake Store construction and confirm the architecture/conformance test fails;
  restore.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'InProcess|RunAttempt|Cancel|Restart|Composition' -count=1 -v
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): execute scenarios through real in-process sessions`.

### Task 7: Collect bounded evidence only after writer shutdown

**Files:**

- Replace: `internal/harness/eval/evidence.go`
- Create: `internal/harness/eval/evidence_test.go`
- Create: `internal/harness/eval/artifact_copy.go`, `artifact_copy_test.go`
- Modify: `internal/harness/eval/manifest.go`, tests

- [ ] Delete the uncommitted implementation's live audit-directory copy and
  `composition.ExportSession` path. Call only `InspectEvaluationStore`, then
  `ExportEvaluationEvidence`, after Task 6 returns a stopped-writer proof.
- [ ] Stage transcript, regenerated audit, frozen identity documents,
  diagnostics, logs, and requested workspace/verifier artifacts under a fresh
  collection directory. Recheck containment and file type for every
  Subject-created workspace entry; reject symlinks, hard links, devices,
  sockets, FIFOs, escapes, and undeclared paths.
- [ ] Enforce file-count, one-file, total-byte, stdout/stderr, and collection
  time bounds while streaming and hashing. Record `collected`, `missing`,
  `truncated`, or `rejected` with stable reason codes; required truncation or
  rejection prevents a scoreable pass.
- [ ] Cross-check transcript trailers, audit verification/head inclusion,
  Outcome digest, request/policy evidence, terminal facts, usage, and approval
  observations before publishing the manifest last.
- [ ] Treat collection as two-phase: stage and verify every evidence file except
  the Outcome copy, finalize and atomically publish Outcome with the resulting
  collection status, stage that exact Outcome document, then publish Manifest
  last. On failure, publish the strongest honest non-completed Outcome and a
  manifest of collected/missing artifacts when possible; never replace Outcome.
- [ ] Test a live lease, malicious post-run symlink swap, hard link, oversized
  file/log, collection timeout, missing required evidence, corrupt transcript,
  corrupt audit, digest tampering, and crash immediately before manifest rename.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'Evidence|Artifact|Collection|Manifest' -count=1 -v
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): publish bounded post-shutdown evidence manifests`.

### Task 8: Orchestrate Attempts, enforce limits, and resume without rerunning Subjects

**Files:**

- Create: `internal/harness/eval/limits.go`, `limits_test.go`
- Create: `internal/harness/eval/runner.go`, `runner_test.go`
- Create: `internal/harness/eval/recovery.go`, `recovery_test.go`
- Modify: `internal/harness/eval/store.go`, tests

- [ ] Implement Runner order: validate/freeze EvalSet → expand all Cells →
  classify existing Attempt roots → allocate concurrency → create/copy fixture
  → atomically publish Attempt → run executor → explicitly stop writer →
  stage/verify cold evidence → atomically publish the finalized Outcome → add
  its exact bytes to evidence → publish Manifest last. Do not start work for
  any Cell until whole-set validation succeeds.
- [ ] Enforce wall/action/startup/cancel/shutdown/collection contexts,
  concurrency, token budget after each Turn, optional frozen-price cost budget,
  and artifact/log bounds. Once token/cost cap is reached, never start another
  Turn. Unknown pricing with a configured cap fails set validation.
- [ ] Recovery classification: valid Outcome+Manifest is immutable terminal;
  Outcome without Manifest resumes collection only; Attempt without Outcome
  calls `composition.InspectEvaluationStore`; no valid Attempt is uncommitted
  temp state only.
- [ ] Publish recovered Outcome only when cold evidence proves a terminal
  Session/Turn. Active/running state, unknown commit, corruption, contradictory
  sources, or an unexpired orphan lease becomes exact `indeterminate` facts.
  Never reopen a writer, signal a persisted PID, rerun a prompt, retry an
  append, or mutate an existing Outcome.
- [ ] Expose dependency seams for clocks/IDs/executor launch and use controlled
  channels for races. Test cancellation at every publication boundary and all
  partial filesystem states.
- [ ] Mutation checks: allow resume to rerun a prompt and confirm the invocation
  counter fails; allow retry to overwrite Outcome and confirm immutability test
  fails; disable token cap and confirm no-next-turn test fails; restore each.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'Runner|Recovery|Resume|Limit|Token|Cost|Concurrent' -count=1 -v
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): orchestrate bounded resumable attempts`.

### Task 9: Add manifest-constrained deterministic scoring and offline regrade

**Files:**

- Create: `internal/harness/eval/artifact_reader.go`, `artifact_reader_test.go`
- Create: `internal/harness/eval/verifier.go`, `verifier_test.go`
- Create: `internal/harness/eval/scorer.go`, `scorer_test.go`
- Create: `internal/harness/eval/regrade.go`, `regrade_test.go`
- Modify: `internal/harness/eval/result.go`, tests

- [ ] Implement `ArtifactReader` from exact committed manifest bytes. It opens
  only normalized collected entries beneath the evidence root, rechecks size
  and SHA-256, rejects symlink/escape/type changes, and cannot expose the live
  database, workspace, network, Executor, Service, Provider, or unrestricted
  filesystem.
- [ ] Implement a compiled verifier catalog keyed by versioned IDs. Separate
  infrastructure invariants from quality criteria in Score fields; unknown
  verifier IDs fail EvalSet validation rather than executing data-file code.
- [ ] Implement deterministic checks for manifest completeness, transcript/
  audit terminals and inclusion, request/policy/usage facts, approval script,
  workspace artifacts, expected negative Outcomes, and declared truncation/
  limit behavior.
- [ ] Implement `RegradeAttempt`: verify immutable documents/digests, require a
  committed manifest, run the selected scorer through `ArtifactReader`, and
  publish a new Score ID without replacing any earlier Score or invoking the
  Subject.
- [ ] Missing/corrupt/contradictory required evidence, unknown schema/verdict,
  nonexistent evidence references, or digest mismatch yields
  `indeterminate`/scoring-infrastructure failure, never `pass`.
- [ ] Assemble `EvaluationResult` from the immutable artifacts and ordered
  Scores. Test that Domain event encoding has no evaluation event and regrade
  has no executor/provider construction capability.
- [ ] Mutation check: allow missing required evidence to pass and confirm a
  dedicated test fails; restore.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'ArtifactReader|Verifier|Scorer|Regrade|EvaluationResult' -count=1 -v
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): regrade immutable evidence offline`.

### Task 10: Ship the Stage A CLI and deterministic in-process smoke set

**Files:**

- Create: `cmd/och-eval/main.go`, `main_test.go`
- Create: `eval/sets/pr-inprocess.json`
- Create: `eval/scenarios/tool-approval-failure/**`
- Create: `eval/scenarios/context-compaction/**`
- Modify: `Makefile` and/or existing CI workflow files only where the repository
  already defines command entry points

- [ ] Implement `och-eval run -set PATH -artifacts PATH [--live]`, `regrade
  -attempt PATH -scorer ID`, and `report -set PATH [-output PATH]` parsing with
  one versioned JSON document on stdout and human diagnostics on stderr.
- [ ] Define stable exit classes for validation, deterministic gate failure,
  infrastructure failure, indeterminate completion, and internal error. A
  non-gating quality failure is report data, not infrastructure failure.
- [ ] Stage A `run` accepts only in-process executors; it reports unsupported
  ACP Cells before Attempt creation until Stage B registers that executor.
  Enforce live dual consent before reading any credential.
- [ ] Check in a fixture-only in-process smoke set and real loopback model
  scripts that exercise tool approval/failure and actual Context compaction.
  Assert claimed multi-chunk/pruning behavior only when observed evidence proves
  it; configuration presence alone is insufficient.
- [ ] CLI tests capture stdout/stderr separately, validate exit codes, refuse
  artifact roots within fixtures, and prove `regrade` does not expose Subject
  execution flags.
- [ ] Run:

```bash
go test ./cmd/och-eval/... ./internal/harness/eval/... -count=1
go run ./cmd/och-eval run -set eval/sets/pr-inprocess.json -artifacts "$(mktemp -d)"
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): ship deterministic in-process evaluation CLI`.

---

## Stage B — real ACP subprocess and parity

### Task 11: Make Subject-to-CLI configuration exact

**Files:**

- Modify: `cmd/och/main.go`, `main_test.go`
- Create: `internal/harness/eval/acp_argv.go`, `acp_argv_test.go`

- [ ] Extend `bindAssemblyFlags` with every design section 16 Limits/Context
  flag: max steps, tool calls/step, assistant bytes, approval timeout, trigger/
  target/tail percentages, summary chunks, overflow compactions/turn, pruned
  Tool Results/request, and compaction timeout.
- [ ] Apply the same binding and validation to normal `och -acp` and
  `compact-session`; preserve existing defaults when flags are absent.
- [ ] Derive normalized argv from the same validated Subject used by
  `BuildConfig`. Include no credential value, temp path in Subject identity, or
  unrelated environment value.
- [ ] Table-test every field with a non-default sentinel and assert parsed
  `composition.Config` equality between in-process and CLI routes. Fail if a
  future Subject semantic field lacks an argv mapping.
- [ ] Run:

```bash
go test ./cmd/och/... ./internal/harness/eval/... -run 'AssemblyFlags|ACPArgv|ConfigParity' -count=1 -v
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(och): expose complete subject configuration to ACP`.

### Task 12: Supervise the real ACP child on supported Unix hosts

**Files:**

- Create: `internal/harness/eval/acp_executor.go`, `acp_executor_test.go`
- Create: `internal/harness/eval/acp_process_unix.go`, `acp_process_unix_test.go`
- Create: `internal/harness/eval/acp_process_windows.go`, `acp_process_windows_test.go`
- Create: `internal/harness/eval/testdata/acpchild/main.go` for controlled
  lifecycle fault modes; conformance tests still launch the real `och` binary

- [ ] Build/resolve the exact `och` binary once per run, hash its bytes, and
  freeze hash, normalized argv, ACP version, and initialized agent name/version
  into Executor/Attempt facts. Conformance tests must launch that real binary.
- [ ] Construct a minimal allowlisted child environment from required OS
  runtime variables, the named credential only, and explicitly declared
  fixture variables. Never forward `os.Environ()` wholesale.
- [ ] On Unix start each writer in a new process group, retain the live
  `os.Process` and group identity until `Wait` returns, own stdin exclusively,
  capture bounded stdout/stderr, and use `internal/client/acp` for initialize,
  new/load/prompt/cancel.
- [ ] Implement normal shutdown as stdin close → bounded exit → `Wait`/reap.
  Record every stage and reject a new launch until reap is proven.
- [ ] On Windows, compile the binary and schemas but reject any
  `acp_subprocess` Cell before Attempt publication with a stable capability
  error. Do not implement parent-only termination.
- [ ] Test allowlisted environment, binary hash mismatch, startup timeout,
  malformed frames, stdout/stderr bounds, EOF shutdown, non-zero exit, reap,
  no leaked child, and Windows cross-build/rejection.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'ACPProcess|Subprocess|Environment|Reap|Platform' -count=1 -v
GOOS=windows GOARCH=amd64 go test -c -o /tmp/och-eval-windows.test.exe ./internal/harness/eval
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): supervise real ACP subprocess attempts`.

### Task 13: Drive ACP approvals, cancellation, and abrupt restart modes

**Files:**

- Create: `internal/harness/eval/acp_actions.go`, `acp_actions_test.go`
- Create: `internal/harness/eval/acp_cancel_unix.go`, `acp_cancel_unix_test.go`
- Modify: `internal/harness/eval/approval.go`, tests
- Modify: `internal/client/acp/connection.go`, tests only for missing public
  lifecycle seams required by the real client

- [ ] Bind the current prompt action/ordinal into the non-interactive ACP
  approval handler and record `sessionId`, `toolCallId`, tool name, offered
  options, decision, and violation code as bounded evidence.
- [ ] Implement cancellation escalation exactly: `session/cancel` → wait cancel
  grace → close stdin → wait shutdown grace → SIGTERM current owned process
  group → wait final grace → SIGKILL current owned process group → reap.
- [ ] Implement restart `interrupt` as SIGINT to the current owned group and
  `kill` as SIGKILL, followed by bounded `Wait`/reap. Start/load a successor
  only after reap. Loss of ownership/reap proof is `indeterminate`, not success.
- [ ] Never use `exec.CommandContext` as the primary kill path and never signal
  persisted PIDs. Clear handles after reap so reuse cannot target an unrelated
  process.
- [ ] Test each escalation winner with controlled channel-driven children,
  SIGINT/SIGTERM ignore paths, already-exited races, cancellation during
  approval, no process leak, and evidence ordering. No fixed sleeps.
- [ ] Mutation check: skip final reap after SIGKILL and confirm the leak/launch
  guard test fails; restore.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'ACPApproval|ACPCancel|Interrupt|Kill|Escalation|Reap' -count=1 -v
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): enforce ACP approval and process-group lifecycle`.

### Task 14: Implement ACP manual compaction as a lease-safe transaction

**Files:**

- Create: `internal/harness/eval/acp_compact.go`, `acp_compact_test.go`
- Modify: `internal/harness/eval/acp_executor.go`, tests
- Modify: `cmd/och/main_test.go`

- [ ] Assign monotonic launch ordinals and unique runtime IDs for writer,
  compactor, and restarted writer; record them only as Attempt/evidence facts.
- [ ] For `compact`: close writer stdin, require clean exit and reap, invoke the
  same binary's public `compact-session` with identical Subject bindings and a
  distinct compactor ID, reap it, launch a third-ID ACP writer, then
  `session/load` the existing Session.
- [ ] If writer reap is unproven, return
  `indeterminate/acp_shutdown_unproven` and never launch the compactor. If clean
  child reaps non-zero, return `infra_failed/acp_shutdown_failed`. If a known
  clean reap is followed by `ErrLeaseHeld`, return
  `infra_failed/runtime_lease_not_released`; uncertain ownership remains
  `indeterminate`.
- [ ] Do not require in-process and ACP compaction lifecycle events to match.
  Parity observes only declared semantic checkpoint/session/request facts.
- [ ] Test runtime-ID distinctness, call order, live-lease prevention, compactor
  non-zero/timeout, failed load, no compaction beside live writer, and no
  signaling a lease's persisted PID.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'ACPCompact|RuntimeID|Lease|SessionLoad' -count=1 -v
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): transact ACP manual compaction across writer leases`.

### Task 15: Normalize semantic parity and install the four-Cell PR lane

**Files:**

- Create: `internal/harness/eval/parity.go`, `parity_test.go`
- Create/modify: `internal/harness/eval/report.go`, `report_test.go`
- Create: `eval/sets/pr.json`
- Create: `eval/scenarios/executor-parity/**`
- Modify: repository CI workflow files

- [ ] Normalize only declared semantic facts: terminal Session/Turn state,
  tools, usage, workspace result, policy decisions, request-envelope
  properties, and artifact completeness. Explicitly reject IDs, timestamps,
  paths, scheduling/raw bytes, process/host/lease/shutdown/reload/recovery facts
  as parity fields.
- [ ] Pair only equal Scenario digest, Executor kind where required by report
  mode, repetition, fixture digest, limits, and seed; baseline/candidate Subject
  digests must differ in a declared semantic field. Show missing pairs and raw
  plus filtered failure denominators.
- [ ] Check in `eval/sets/pr.json` expanding to exactly four Cells: a paired
  parity Scenario through both executors, one in-process tool/approval/failure
  Cell, and one in-process Context compaction Cell.
- [ ] Add a guard test that fails if ordinary PR expansion is not exactly four
  or includes a live/model-judge Cell. Full deterministic cross-products run
  only through an explicit command and scheduled workflow.
- [ ] Build a fresh real `och` binary for the ACP parity Cell. Test that adding
  a lifecycle-only difference does not fail parity, while a tool, usage,
  request, policy, terminal, workspace, or completeness difference does.
- [ ] Run:

```bash
go test ./internal/harness/eval/... ./cmd/och-eval/... -run 'Parity|Pair|Report|PRSet' -count=1 -v
go run ./cmd/och-eval run -set eval/sets/pr.json -artifacts "$(mktemp -d)"
go test -race ./internal/harness/eval/... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(eval): gate four deterministic cells with ACP parity`.

---

## Stage C — suites, live quality, and completion evidence

### Task 16: Add deterministic tool/workspace and Context mechanism suites

**Files:**

- Create: `eval/sets/deterministic-full.json`
- Create: `eval/scenarios/tool-workspace/**`
- Create: `eval/scenarios/context-mechanism/**`
- Extend: `internal/harness/eval/verifier.go`, tests

- [ ] Add frozen scenarios for read/write/exec, policy/approval, expected tool
  failure, cancellation, redaction, containment, and artifact collection;
  cross-check workspace artifacts with transcript and audit facts.
- [ ] Add Context scenarios for pre-turn/mid-turn compaction, manual summary and
  reset, overflow recovery, clean restart, ACP interrupt/kill recovery,
  transcript/audit projection, resource bounds, multi-chunk summary, and Tool
  Result pruning.
- [ ] Require behavioral evidence for multi-chunk/pruning. A configured nonzero
  cap with no observed multi-chunk or prune event fails the relevant scenario;
  this prevents milestone 8's previously inert configuration from looking
  implemented.
- [ ] Run both executors in the explicit/scheduled full matrix while keeping the
  ordinary PR set unchanged. Mark abrupt restart scenarios ACP-only, not parity.
- [ ] Add golden fixtures for known failure taxonomies and run every verifier
  once against tampered/missing evidence to prove fail-closed behavior.
- [ ] Run:

```bash
go test ./internal/harness/eval/... -run 'ToolWorkspace|ContextMechanism|Redaction|Pruning|SummaryChunk' -count=1 -v
go run ./cmd/och-eval run -set eval/sets/deterministic-full.json -artifacts "$(mktemp -d)"
```

- [ ] Commit: `test(eval): cover tools, recovery, and context mechanisms`.

### Task 17: Add the explicit live lane and strict evidence-only model judge

**Files:**

- Create: `internal/harness/eval/judge.go`, `judge_test.go`
- Create: `internal/harness/eval/price.go`, `price_test.go`
- Create: `internal/harness/eval/live.go`, `live_test.go`
- Create: `internal/harness/eval/prompts/quality_judge_v1.md`
- Create: `eval/sets/context-quality-live.example.json`
- Create: `eval/scenarios/context-quality/**`

- [ ] Enforce live dual consent (`lane=live` and `--live`) before resolving or
  reading any credential. Use an independent artifact root and never upload
  evidence automatically.
- [ ] Freeze judge model/config/prompt and optional integer-microunit price
  table digests. Keep judge usage/cost separate from Subject usage/cost;
  unavailable price is explicit, never zero.
- [ ] Give judges only bounded, redacted, manifest-declared evidence selected by
  criteria. Wrap every Subject-authored value as untrusted evidence and state
  that it cannot issue instructions.
- [ ] Strictly decode judge output with verdict, bounded score, per-criterion
  results, manifest evidence references, missing/contradiction list, and bounded
  rationale. Unknown fields, malformed output, nonexistent references, omitted
  required evidence, or unresolved contradiction is `indeterminate`.
- [ ] Run deterministic invariants before quality judging. Implement context
  fidelity/constraints/decisions/tool attribution/continuity/quality/token/
  latency/stability criteria and append judge Scores without replacing
  deterministic Scores.
- [ ] Meta-evaluate the judge with injection-bearing, missing-evidence,
  contradiction, unsupported-claim, and known-pass/fail fixtures. Live results
  remain advisory and never gate an ordinary PR.
- [ ] Mutation check: bypass either live-consent half and confirm credential
  access probes fail; restore.
- [ ] Run:

```bash
go test ./internal/harness/eval/... ./cmd/och-eval/... -run 'Live|Judge|Injection|Price|Consent' -count=1 -v
go run ./cmd/och-eval run -set eval/sets/context-quality-live.example.json -artifacts "$(mktemp -d)"  # must refuse without --live before credential access
```

- [ ] Commit: `feat(eval): add consent-gated live quality evaluation`.

### Task 18: Benchmark, document, and publish the milestone evidence ledger

**Files:**

- Create: `internal/harness/eval/benchmark_test.go`
- Create: `docs/architecture/evaluation.md`
- Create: `docs/architecture/evaluation.zh-CN.md`
- Create: `docs/architecture/evaluation-evidence.md`
- Create: `docs/guides/evaluation-scenarios.md`
- Create: `docs/guides/evaluation-operations.md`
- Modify: `README.md`, `docs/README.md`, relevant roadmap/status documents

- [ ] Benchmark expansion/reporting/recovery at 1, 100, 1,000, and 4,096 Cells
  without model calls; record CPU, allocations, wall time, platform, Go version,
  and exact commit. Add subprocess startup/cleanup and evidence export numbers
  separately so orchestration and model latency are not conflated.
- [ ] Write implemented architecture in English and a synchronized Chinese
  reading copy: ownership boundaries, durable documents, directory layout,
  executor lifecycles, approval/restart contracts, evidence trust, recovery,
  limits, scoring, parity, live consent, and platform support.
- [ ] Write author guidance for fixtures/actions/approvals/verifiers and operator
  guidance for run/resume/regrade/report, privacy, credentials, costs, artifact
  retention, diagnosing indeterminate attempts, and safely handling orphan
  leases without PID signaling.
- [ ] Populate an evidence ledger mapping every design acceptance item and plan
  task to commit/PR, exact commands/output, fault/mutation results, benchmark
  data, deviations, and open blockers. Never cite a commit that is absent from
  repository history.
- [ ] Update authority tables, CLI indexes, milestone status, root README, and
  docs README. State explicitly that evaluation is not GA until real-model
  sample size, judge meta-eval, provider breadth, and variance policy have
  accepted evidence; MCP is a future suite, not a runner prerequisite.
- [ ] Run fresh completion verification from a clean tree:

```bash
go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build ./...
GOOS=windows GOARCH=amd64 go build ./internal/harness/eval ./cmd/och-eval
git diff --check origin/main...HEAD
git status --short
```

- [ ] Run the checked-in four-Cell PR EvalSet and the explicit deterministic
  matrix into fresh artifact roots; run an offline regrade after making Subject
  execution credentials unavailable; verify no child process or writer lease
  remains.
- [ ] Commit: `docs(eval): publish architecture and completion evidence`.

## Merge and completion gate

- [ ] Stage A is complete only when Tasks 1–10 are merged, the in-process
  fixture set runs through real Composition, evidence can be regraded offline,
  and restart recovery never reruns a Subject.
- [ ] Stage B is complete only when Tasks 11–15 are merged, the real ACP binary
  passes paired semantic parity on Unix, Windows rejects before Attempt
  creation, and subprocess/lease leak tests are green.
- [ ] Stage C is complete only when Tasks 16–18 are merged, the four-Cell PR
  lane and explicit full deterministic lane pass, live dual consent and judge
  meta-eval are proven, and the evidence ledger contains fresh reproducible
  output.
- [ ] Milestone 10 may be marked implemented only after all three stages. It
  remains explicitly not GA under the blockers documented in Task 18.
