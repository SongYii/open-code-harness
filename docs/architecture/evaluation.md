# Evaluation System: Deterministic Fixtures, ACP Parity, and Consent-Gated Live Judging — Implemented Contract

**Status:** Implemented; not GA (see [Maturity](#maturity-and-ga-blockers))

**Authority:** [Milestone 10 evaluation design](../superpowers/specs/2026-09-02-evaluation-design.md)

**Implemented plan:** [Milestone 10 implementation plan](../superpowers/plans/2026-09-02-evaluation-system.md)

**Completion evidence:** [Evaluation evidence ledger](evaluation-evidence.md)

**Chinese reading copy:** [已实现评估系统合同](evaluation.zh-CN.md)

**Packages:** `internal/harness/eval` (pure evaluation domain — models, digests, runners, verifiers, judge, parity), `cmd/och-eval` (CLI: `run`, `regrade`, `report`, `judge`), `internal/harness/composition`/`internal/harness/adapters/sqlite` (the canonical audit snapshot/verify operation eval reads evidence from)

This document records behavior enforced by the current code and tests. It is
an internal Go contract, not a stable public protocol, and not yet a GA
guarantee.

## Scope

The evaluation system runs a frozen **Scenario** (a scripted sequence of
prompt/compact/cancel/restart/collect actions) against a frozen **Subject**
(a Provider/Policy/Context configuration) through one of two **Executors** —
`in_process` (a real `composition.Assembly` driven directly, no subprocess)
or `acp_subprocess` (a real, independently spawned `och -acp` process driven
over the ACP v1 wire) — and publishes append-only evidence a later, separate
step can score without ever re-running the Subject. It never edits or scores
production Session data; every Attempt gets its own isolated workspace,
SQLite database, and audit directory (design §8).

`internal/harness/eval` is deliberately narrow: it may import
`internal/harness/application`, `internal/harness/composition`, and
`internal/harness/transcript`, but never a concrete harness adapter
(`internal/harness/adapters/*`) and never `internal/harness/testkit` — this
is a live, guarded boundary
(`internal/harness/architecture/dependencies_test.go`'s `TestForbiddenImport`
and `TestClientPackagesAreIsolatedFromInternalHarness`), not a convention. No
other harness package (`application`, `domain`, `engine`, `composition`,
`transcript`, any adapter) may import `eval` either — the dependency runs one
way. `internal/client/acp` is a separately-owned package `eval` never
imports even though both talk the same protocol: each ACP client this
repository builds (`internal/client/acp`, `internal/harness/adapters/acp`,
and `internal/harness/eval`'s own `acp_wire.go`) owns its own independent
copy of NDJSON framing and wire shapes rather than sharing one, an explicit
design choice (see `internal/client/acp`'s own contract), not an oversight.

## Durable documents and directory layout

Every document is UTF-8 JSON with a `schema` string and integer
`formatVersion` field, decoded with duplicate-key and unknown-field rejection
(`internal/harness/eval/model.go`'s `decodeStrict`), and — for the four
identity documents — a canonical SHA-256 digest over its own exact validated
bytes (`ScenarioDigest`, `SubjectDigest`, `ExecutorDigest`, and the derived
`Attempt.ScenarioDigest`/`SubjectDigest`/`ExecutorDigest` fields). A
credential value, an absolute machine-local path, or a wall-clock timestamp
never enters a Scenario or Subject digest — only the credential **environment
variable name** is recorded (design §10).

| Document | Schema | Written | Mutable after publish |
| --- | --- | --- | --- |
| Scenario | `och.eval.scenario` | Checked into `eval/scenarios/<id>/scenario.json` | No — it is a source file, not runtime output |
| Subject | `och.eval.subject` | Checked into `eval/subjects/<id>.json` | No |
| Executor | `och.eval.executor` | Checked into `eval/executors/<id>.json` | No |
| EvalSet | `och.eval.set` | Checked into `eval/sets/<id>.json` | No |
| Attempt | `och.eval.attempt` | Once, atomically, before the Subject starts | Never |
| Outcome | `och.eval.outcome` | At most once, atomically, after execution or crash recovery | Never |
| EvidenceManifest | `och.eval.evidence-manifest` | Once, atomically, after evidence staging completes | Never — the commit marker for a scoreable Attempt |
| Score | `och.eval.score` | Once per scoring/regrade invocation | Never replaces an earlier Score; regrading always appends |

One Attempt's own isolated filesystem root (`NewAttemptRoot`,
`internal/harness/eval/fixture.go`):

```text
<artifactRoot>/<attempt-id>/
  attempt.json
  outcome.json
  workspace/       (the Scenario's own fixture tree, copied and re-digested)
  database/        (this Attempt's own private SQLite file)
  audit/           (this Attempt's own private JSONL audit replica)
  process/
  log/
  evidence/
    manifest.json
    transcript.jsonl
    audit/segments/*.jsonl
    workspace/<collected paths>
    scenario.json, subject.json, executor.json, attempt.json  (staged copies, cross-verified)
```

`AttemptID`/`ScoreID` are 128-bit cryptographically random lowercase hex
(`NewAttemptID`, `crypto/rand`, never `math/rand`); `ScenarioID`/`SubjectID`/
`ExecutorID`/`EvalSetID` are user-provided, `[a-z0-9][a-z0-9._-]*`, ≤128
bytes. A path is never an identity (`AttemptPaths` — design §10/§12).

## Matrix expansion

`EvalSet.ExpandCells()` is a flat Cartesian product of every Scenario ×
Subject × Executor reference the EvalSet declares — there is no per-Cell
selective pairing inside one EvalSet document. `ExpandAttempts` additionally
re-verifies every referenced document's digest still matches what the
EvalSet froze, checks every Cell's Scenario-required capability is present on
its Executor (a missing capability fails the **whole set**, not one skipped
row — design §9), enforces fixture-lane vs. live-lane Subject consistency,
and refuses before returning anything if the total would exceed
`EvalSetLimits.MaxExpandedAttempts` (default 256, hard cap 4096).

Because there is no selective pairing, an EvalSet that needs two different
Subject/Executor pairs for the *same* Scenario (an executor-parity baseline
and candidate, for instance) cannot list both pairs in one document without
cross-multiplying into unwanted Cells — see
[`eval/sets/pr-parity-baseline.json`](../../eval/sets/pr-parity-baseline.json)
and
[`pr-parity-candidate.json`](../../eval/sets/pr-parity-candidate.json), two
separate, minimal-cardinality documents sharing one artifact root, rather
than one combined file.

## Fixture isolation

A Scenario's own `fixtureDigest` is `DigestFixtureTree`'s SHA-256 over a
path-sorted, content-and-executable-bit-bound canonical JSON encoding of its
`fixture/` source tree (timestamps and ownership excluded). `RunEvalSet`
verifies this digest **before** copying the tree into a fresh Attempt
workspace, and again **after** the copy — a changed checked-in fixture or a
corrupted copy is refused before any Subject ever starts, not discovered
after the fact.

A fixture-lane Subject's `provider.normalizedEndpoint` is the symbolic
`fixture://<script-name>` scheme; `cmd/och-eval/fixture.go`'s
`resolveFixtureSubjects` starts one real, in-process `httptest.Server`
per referenced script name and rewrites the endpoint to a real
`http://127.0.0.1:<port>` address **only in memory**, never on disk — the
checked-in Subject document's own bytes and digest never change. This is
`RunnerInputs.ProviderEndpointOverrides`, a purely execution-time fact
(`resolveExecutionSubjects`, `internal/harness/eval/runtime_subject.go`);
`ExpandAttempts`/`Attempt` digests always use the frozen Subject, never the
override.

## Executor lifecycles

### `in_process`

`RunAttempt` (`internal/harness/eval/inprocess.go`) opens a real
`composition.Assembly` directly in this process (`composition.Open`), drives
each Scenario action against it (`Service.RunTurn`/`CompactSession`, a
scripted `ApprovalMatcher` wired as the Assembly's own `tools.Approver`), and
closes it (`assembly.Close()`) on every terminal path, proving
`WriterStopped` before returning. A `restart` action re-opens a fresh
Assembly under a new runtime ID against the *same* database/workspace;
`clean_shutdown` is the only mode this executor accepts — `interrupt`/`kill`
are refused as `infra_failed/unsupported_restart_mode`, since there is no
separate process for either to abruptly end.

### `acp_subprocess`

`RunACPAttempt` (`internal/harness/eval/acp_executor.go`) spawns a real,
independently built `och -acp` binary in its own process group
(`startACPProcess`, `Setpgid`), drives it over a minimal, independently owned
ACP v1 NDJSON client (`acp_wire.go` — deliberately not
`internal/client/acp`, per the isolation boundary above), and supervises its
full lifecycle: bounded stderr capture, an allowlisted child environment
(never `os.Environ()` wholesale — `BuildChildEnvironment`), and binary hash
pinning (`ResolveACPBinary`). Unlike `in_process`, this executor accepts
**all three** restart modes, plus `compact` as a real, lease-safe three-process
transaction (below). Not supported on Windows at all
(`acpProcessSupported = false`, `internal/harness/eval/acp_process_windows.go`)
— design explicitly rejects a parent-only-termination substitute for the
real process-group kill path Windows lacks, rather than approximating it.

`RunEvalSet`/`och-eval run -och-binary` dispatch to whichever executor a
Cell's own `Executor.Kind` names; a Cell naming `acp_subprocess` with no
resolved binary is refused before any Attempt is created.

## Cancellation and restart

`escalateCancel` (`internal/harness/eval/acp_actions.go`) implements design
§16/§17's exact four-rung escalation ladder for one in-flight prompt a later
`cancel` action targets: `session/cancel` → wait `CancelGrace` → close stdin
→ wait `ShutdownGrace` → SIGTERM the owned process group → wait a grace
period → SIGKILL the owned process group → reap. Each rung races the pending
prompt's own resolution against its own grace period and stops at whichever
resolves first; only the mildest rung (`session/cancel` alone resolving it)
leaves the writer running — every rung past that tears it down, and the
Attempt cannot continue past that action (`indeterminate/acp_cancel_escalated`,
or `indeterminate/acp_cancel_reap_unproven` if even SIGKILL's own reap
could not be proven within its grace period). `exec.CommandContext`'s own
ctx-triggered kill is never the primary kill path — it only ever reaches a
process's direct children, not a process group — and no cancellation or
restart path ever signals a PID read back from lease or database state, only
a process handle this package spawned itself. The in-process executor's own
`cancel` action has no subprocess to escalate against at all: it interrupts
the in-flight Go call directly via `context.Cancel`.

Restart (`runACPRestart`) closes the current connection, then dispatches on
mode: `clean_shutdown` closes stdin and waits normally; `interrupt` sends
SIGINT to the owned group; `kill` sends SIGKILL. A successor is launched
under a new, distinct runtime ID and resumes the *same* ACP session via
`session/load` only once the prior writer's reap is **proven** — an
unproven reap is the caller's own indeterminate fact, never silently treated
as success. A real, verified interaction with the runtime's own
single-writer fencing lease (`internal/harness/adapters/sqlite/lease.go`):
an abruptly-terminated writer never releases its lease, so the successor
(a different runtime ID) cannot acquire it until that lease naturally
expires (default 30s) — `relaunchACPSuccessor` retries the spawn+initialize
sequence across a dedicated `RelaunchGrace` bound (default comfortably
exceeds 30s) rather than failing fast on the very first attempt.
`RestartModeInterrupt` against a real, otherwise-idle agent was separately
found not to reliably terminate within any bound at all — `internal/harness/adapters/acp`'s
own `Serve`/`decodeFrames` loop only observes `ctx.Err()` between already-
decoded frames, never while blocked reading the next one — a property of
that package's own Serve loop, not this one; `RunACPAttempt` correctly
reports `infra_failed` rather than a false completion when this happens
(see the evidence ledger for the direct repro).

## Manual compaction as a lease-safe transaction (`acp_subprocess` only)

`runACPActionCompact` (`internal/harness/eval/acp_compact.go`) is a
three-phase transaction, each phase gated on the previous one's own proof:

1. Close the current writer and **prove** its reap
   (`indeterminate/acp_shutdown_unproven` if that proof cannot be obtained,
   `infra_failed/acp_shutdown_failed` if it reaps non-zero) — the compactor
   is never launched without this proof.
2. Launch `och compact-session` under its own distinct runtime ID and wait
   for **its own** proven exit (`infra_failed/acp_compactor_failed` on any
   non-zero exit, timeout, or undecodable stdout) — `runACPCompactor` is a
   separate, one-shot process launcher (not `startACPProcess`'s own
   long-lived NDJSON-server shape), forcibly SIGKILLing an overrun compactor
   so a hung one is never left running.
3. Relaunch a successor writer under a *third* distinct runtime ID and
   resume the same Session via `session/load`. A relaunch failure after a
   *proven-clean* compactor reap is classified separately
   (`infra_failed/runtime_lease_not_released`, by pattern-matching the real,
   verified stderr text `internal/harness/runtime`'s own
   `ErrLeaseHeld.Error()` produces) from any other relaunch failure
   (`indeterminate/acp_compact_relaunch_unproven`).

## Evidence trust model

A scorer or verifier never reads raw files directly — only through
`ArtifactReader` (`internal/harness/eval/artifact_reader.go`), which
re-verifies size and SHA-256 against the published manifest on every read
and refuses a symlink swap, a hard-linked file, or a type change since
collection. `EvidenceManifest` publication is the commit marker for a
scoreable Attempt (design §12); a required role missing from it is
`Indeterminate`, never silently absent. Frozen identity documents
(Scenario/Subject/Executor/Attempt) are staged into the manifest **as
evidence themselves** (`EvidenceDocuments`, `evidence_identity.go`) with
cross-digest validation, so `RegradeAttempt` needs no externally-supplied
Scenario input at all — it reads everything it needs, including which lane
governed the Attempt, from the Attempt's own committed evidence.

## Recovery

`ClassifyAttemptDirectory` (`internal/harness/eval/recovery.go`) is a pure,
four-state read of one Attempt directory's own on-disk documents, checked in
this fixed order:

| State | Condition |
| --- | --- |
| `Uncommitted` | `attempt.json` itself does not exist or does not parse |
| `InspectRequired` | Attempt exists, `outcome.json` does not |
| `ResumeCollectionOnly` | Attempt and Outcome exist, `manifest.json` does not |
| `Terminal` | All three exist |

`ResumeCollection` performs the one recovery step this package automates:
re-staging evidence and publishing the manifest against an Outcome that
*already* exists, without ever republishing or mutating that Outcome —
recovery never reopens a writer to obtain a fresh one.

## Limits

`AttemptExecutionLimits` (`internal/harness/eval/limits.go`) resolves each
EvalSet-declared limit against a documented default and hard maximum (wall
time, per-action time, process startup, cancellation/shutdown grace,
evidence-collection time) — an EvalSet may only **narrow** a Scenario's own
declared limits, never widen them past its own. `CollectionLimits` separately
bounds evidence staging itself (max files, max bytes per artifact, max total
bytes) so a runaway workspace can never make evidence collection itself
unbounded.

## Scoring: deterministic verifiers and the live judge

A `Verifier` (`internal/harness/eval/verifier.go`) is a fixed, versioned,
compiled-in Go function — never data-file code an EvalSet or Scenario could
supply — keyed by ID in `verifierCatalog`; an unregistered ID is invalid
EvalSet input, not a runtime failure to recover from. Every verifier this
milestone ships is proven fail-closed: real, readable evidence that
genuinely carries none of the claimed behavior returns `Fail`, and evidence
that was never collected at all returns `Indeterminate` — neither case ever
returns `Pass`.

The live model judge (`internal/harness/eval/judge.go`, Task 17) is a
different mechanism for a different lane: `RunJudge` builds a bounded,
redacted evidence bundle from only the manifest roles a `JudgeConfig`'s own
`Criteria` declare, sends it to an injectable `JudgeCaller` (so a live model
call and a test double implement the exact same function type — `RunJudge`
itself never opens a network connection), and strictly decodes the response.

`JudgeConfig` is a document, not an in-memory value: schema
`och.eval.judge-config`, validated and digested exactly like
Scenario/Subject/Executor. That is what makes a live Score's judge identity
provable offline. A live `EvalSet` must name a `judgeConfigDigest` and a
fixture set must not; every new Attempt stages its frozen `EvalSet` as
`eval-set.json` (role `eval_set`) and a live Attempt additionally stages
`judge-config.json` (role `judge_config`), so the manifest hashes both and
any later reader can reconstruct which configuration a verdict came from
without trusting the caller that produced it. `readJudgeEvidenceDocuments`
re-verifies that binding on read rather than only on write — a reader
opening an Attempt months later has no expansion step to rely on. An
Attempt collected before those roles existed still regrades
deterministically and can never be live-judged, which is the honest
outcome: nothing in its evidence could prove its entitlement.

Evidence selection is a pure function of the manifest and the config. The
declared roles live in a set, and Go randomizes map iteration, so the
candidate list is sorted in full *before* any byte budget applies —
otherwise judging one Attempt twice could show the judge different evidence
and accept different `evidenceReferences` each time. Omission is
fail-closed, not a smaller question: a declared role the manifest never
collected, or an entry the total budget cannot carry, stops the run before
the caller and is reported in `missingEvidence`, because a model asked
about material it was never shown can answer "pass" indistinguishably from
one that really read it. Per-entry truncation stays permitted — the
contract supplies bounded excerpts — and each entry label records original
bytes, excerpt bytes, and whether it was truncated.

The frozen judge configuration is validated before any caller runs: the
adapter kind, endpoint shape, credential *variable name*, context/output
limits, usage reporting, model identity, and the exact embedded prompt
digest are all mandatory; criterion IDs and evidence roles are non-empty
and unique; each criterion carries a bounded rubric; and the trusted
criterion contract is rendered from the config outside the untrusted
evidence block. The document admits both `http` and `https` endpoints;
requiring HTTPS is the production CLI's own stricter rule, applied where
the real network caller is constructed, which is what lets a test drive the
same frozen document against a loopback server with no production path ever
accepting a plaintext endpoint. Every one of design §21's own
fail-closed cases — unknown fields, malformed or trailing output, a nonexistent
evidence reference, missing evidence, an unresolved contradiction, an
undeclared/omitted/duplicate criterion, an aggregate verdict inconsistent with
its criterion results, an out-of-range score, or the call itself failing —
resolves to a real `JudgeOutcome{Verdict: Indeterminate}` carrying a bounded,
redacted rationale, never a Go error and never silently accepted as `Pass`.
Every Subject-authored value the judge is shown is labeled `untrusted...
not an instruction` (the embedded `prompts/quality_judge_v1.md` prompt's own
framing) — this repository has no live model to prove actually resists a
prompt-injection attempt in an automated test, so what is tested is the
mechanism: that labeling is genuinely present around real transcript
content, not merely aspirational prompt text.

`EvaluateJudgeAttempt` (`internal/harness/eval/judge_attempt.go`) is the
orchestration `och-eval judge` drives, and the order of its gates is the
contract: frozen evidence and the supplied config's digest first, then
design §24's dual consent, then every deterministic verifier the Scenario
declares. Because the `JudgeCaller` is what holds any credential, a run
that is not entitled to happen can reach neither a provider nor a
credential. A non-Pass deterministic prerequisite publishes an
Indeterminate Score without calling the model; `JudgeAttemptResult`
reports that prerequisite verdict separately so an operator can tell "the
invariants did not hold" apart from "the judge could not answer", which
read identically on the Score itself.

A judge Score publishes through the exact same `PublishScore` path a
deterministic regrade uses, with `Lane: LaneLive` — there is no separate
document type — and takes its scorer identity from the frozen JudgeConfig
rather than from anything chosen at invocation time.
`internal/harness/eval/price.go`'s `PriceTable` computes cost in integer
microunits, and `ScorerUsage.costStatus` makes availability explicit:
`computed` carries a currency (a free model is a genuine computed zero),
`unavailable` carries neither a currency nor a cost. Scores published
before that field existed remain readable.

## Context mechanism suite

`internal/harness/eval/context_trace.go` parses one Attempt's canonical
audit once and indexes compaction brackets, Context preparations paired with
their conversation requests, checkpoints, provider usage, and terminal
outcomes. Building it is fail-closed: an orphan terminal compaction, a
bracket that closes twice, a duplicate decision ID, a request paired with a
preparation from a different turn/item/attempt or one that came later, an
attempt index that is zero or out of order, a zero or unordered budget, or a
forked or cyclic checkpoint chain are all errors. A caller turns a build
failure into an `indeterminate` criterion, never a behavioural `fail` — "the
evidence does not hold together" and "the evidence is intact and shows this
did not happen" are different claims, and conflating them is how a suite
starts passing on broken input.

`context_verifier.go` and `context_verifier_mechanisms.go` register eleven
focused criteria rather than one Scenario-aware mega-verifier. Each demands
an observed fact, never a capability: pre-turn summary requires a later
preparation to actually name the new checkpoint, checkpoint reuse refuses a
preparation naming a checkpoint the Attempt never established, pruning
resolves the projected frame's declared `original_bytes` and `sha256` back
to a collected workspace file, overflow requires a strictly smaller retry
estimate, and the usage anchor must be recorded as applied, be supported by
an earlier provider usage record, and lead to a completed compaction.

`cmd/och-eval`'s `context-mechanism` fixture classifies by parsing the
request — summarizer by the versioned prompt marker, then tool continuation,
then the marker on the *latest* user message — and holds no cross-request
state. Rolling depth is carried inside the summary itself, so a chunk count
nothing produced cannot pass. `CriterionResult.Detail` carries a bounded,
evidence-oriented explanation of every verdict.

### What the suite proves today

Five Scenarios run end to end through the production in-process path and
regrade offline: `context-manual-reset`, `context-manual-summary`,
`context-pre-turn-summary`, `context-mid-turn-pruning`,
`context-overflow-retry`, and `context-usage-anchor`.

### What it does not prove yet

- **Multi-chunk summarization has a verifier but no landed Scenario.**
  Forcing at least two summarizer chunks deterministically requires the
  covered source to sit inside `(hardInput - focusTokens, 0.95 x hardInput)`.
  `triggerPercent` has a 60% floor and `maxCompactSessionFocusBytes` caps the
  focus at 4KiB (roughly 1000 tokens), so that band is about 800 tokens wide
  on a 4096-token window — and the summary must still be a net reduction
  inside it. The criterion and its mutation tests are landed and green; the
  end-to-end Scenario is not.
- **ACP paired sets are not landed.** Every Scenario above runs in-process
  only.
- Interrupt-restart recovery remains blocked on the ACP input-cancellation
  prerequisite; `kill` is the suite's abrupt restart mode.

## Parity

`internal/harness/eval/parity.go` implements design §11/§22: `LoadParityArm`
reads an Attempt's own already-collected evidence and projects it to only
design's declared semantic parity facts — terminal Session/Turn state, tool
facts (name/arguments/policy effect/approval decision/result — correlated
internally by each side's own wire IDs, which are then discarded, never
compared across sides), usage facts (excluding latency and provider request
ID), request-envelope properties (excluding message text), and workspace
result (relative path + content digest, never an absolute path).
`ComparePairedArms` diffs two arms field by field. Pairing itself
(`ParityPairKeyForAttempt`) groups by Scenario digest and repetition index
only — Executor Kind is deliberately the *varying* dimension for this report
mode, and every other design-listed pairing field (fixture digest, limits,
pairing seed) is already an EvalSet-level invariant shared by every
co-resident Attempt, needing no separate representation. Verified against
real in-process and ACP subprocess Attempts, not mocked: the same
deterministic Scenario/Subject semantics through both executors yields zero
mismatches; a genuinely different scripted approval decision between the two
arms is caught.

## The four-Cell PR lane

Design §23's own ordinary-PR gate: exactly four Cells — a paired parity
Scenario through both executors (two Cells:
[`pr-parity-baseline.json`](../../eval/sets/pr-parity-baseline.json)/
[`pr-parity-candidate.json`](../../eval/sets/pr-parity-candidate.json)), one
in-process tool/approval/failure Cell, and one in-process Context compaction
Cell (both in
[`pr-tool-and-compaction.json`](../../eval/sets/pr-tool-and-compaction.json)).
`cmd/och-eval/report.go` loads a `ParityArm` for every terminal,
fully-collected Attempt under one shared artifact root, groups by
`ParityPairKey`, and gates the report's own exit code on any non-empty
mismatch list. `TestPRLaneExpandsToExactlyFourFixtureCells` and
`TestPRLaneRunAndReportEndToEnd` (`cmd/och-eval`) are ordinary Go tests, so
this repository's existing CI `go` job (`go test -race ./... -count=1`)
already gates every PR on this lane with no dedicated CI workflow step
needed.

The complete deterministic matrix (both executors, every Scenario) runs only
by explicit command
(`go test ./internal/harness/eval/... -run TestCheckedInDeterministicFullSetProvesToolWorkspaceSuite`
or `och-eval run -set eval/sets/deterministic-full.json`), never in ordinary
PR CI.

## Live lane

`internal/harness/eval/live.go`'s `RequireLiveConsent` is the single source
of truth for design §24's dual-consent gate: an EvalSet's own declared lane
and an explicit `--live` flag must agree exactly, and a live lane
additionally requires `OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND` in the
environment — `RequireLiveConsent` itself never reads the environment or a
credential, so a caller that checks it first and refuses to proceed past a
non-nil error is what makes "before any credential is read" real.
`cmd/och-eval/run.go`'s own `checkLaneConsent` delegates to it rather than
duplicating the rule. A live run always writes an independent artifact root
and this repository never uploads evidence anywhere automatically.

## Platform support

| Platform | `in_process` | `acp_subprocess` |
| --- | --- | --- |
| Linux | Supported | Supported (`acp_process_unix.go`, `//go:build unix`) |
| macOS | Supported | Supported (same file, `unix` covers darwin) |
| Windows | Supported | Not supported — refused before any process is spawned (`acpProcessSupported = false`) |

## Maturity and GA blockers

Evaluation is **implemented, not GA**. Explicitly outstanding before a GA
claim: real-model sample size for live judging — `och-eval judge` is wired
end to end and proven against a fixture SSE stream through the real
adapter, but no run against an actual live model has ever happened in this
repository — judge meta-evaluation against a broader fixture set than this
milestone's own five-case suite (injection, missing-evidence,
contradiction, unsupported-claim, known-pass/fail), provider breadth beyond
the one OpenAI-compatible adapter this repository ships, and an accepted
variance policy for live/quality signals. MCP is a future suite this runner can host, never a runner
prerequisite — its absence does not block anything documented here.
