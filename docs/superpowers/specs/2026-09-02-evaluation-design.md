# Evaluation System: Real Sessions, Evidence, and Offline Regrading

**Status:** Accepted normative design; not implemented

**Date:** 2026-09-02

**Authority:** This document is normative for the milestone 10 evaluation
subsystem. The synchronized Chinese reading copy is
[2026-09-02-evaluation-design.zh-CN.md](2026-09-02-evaluation-design.zh-CN.md).
If the copies diverge, this English document wins.

**Research basis:**
[Evaluation architecture gate](../../research/architecture-gates/2026-09-01-evaluation.md)

## 1. Scope and decision

This milestone builds a local evaluation system for Open Code Harness (OCH).
It runs real OCH Sessions, freezes experiment identity, preserves bounded
evidence, and scores that evidence without requiring the agent to run again.

The v1 milestone has two concrete OCH executors:

1. an in-process executor that drives the real Composition/Application path;
2. a black-box executor that starts the real `och -acp` binary and drives it
   through the public ACP v1 client protocol.

The persistent model remains executor-neutral, but v1 does not run arbitrary
external agents. A future external Subject requires a real consumer and a new
isolation/conformance decision; this design does not create an unused generic
Agent adapter.

Delivery is staged. The first implementation slice establishes the frozen
models, append-only evidence/regrade path, fixture isolation, and in-process
executor only. It is useful but does not complete the milestone; the ACP
executor and parity contract arrive in the next stage, and live judging
arrives only after deterministic mechanisms are proven.

OpenTelemetry is not part of this design. It remains a separate milestone 10
subsystem with its own architecture gate and design.

## 2. Goals

The implementation must:

- run production OCH behavior rather than an eval-only Engine or Provider path;
- prove both the in-process and public ACP subprocess surfaces;
- isolate every Attempt's fixture, workspace, SQLite database, Session, audit
  replica, process resources, and artifact directory;
- freeze Scenario, Subject, Executor, limits, and scorer identity;
- retain append-only Attempts and append-only Scores;
- save a versioned Evidence Manifest before scoring;
- regrade saved evidence without running the Subject again;
- distinguish execution outcome from quality verdict;
- keep deterministic PR CI separate from explicit live-model evaluation;
- enforce time, token, concurrency, file-count, and artifact-byte bounds;
- fail closed on corrupt, incomplete, missing, or contradictory required
  evidence;
- host Context, tool/workspace, recovery, Provider, ACP, Policy, and future MCP
  suites without making any one suite the runner's package boundary.

## 3. Non-goals

The v1 milestone does not include:

- arbitrary external agents or a Harbor-style agent adapter matrix;
- container/cloud scheduling, distributed workers, or remote artifact storage;
- Terminal-Bench, SWE-bench, or another project's dataset as OCH's native
  Scenario schema;
- arbitrary scenario-authored shell verifiers;
- an MCP evaluation suite before the MCP client adapter exists;
- OpenTelemetry, an eval Web UI, or a second client protocol;
- automatic network lookup of model prices;
- a second trajectory schema parallel to `och.session.transcript`;
- Session events for eval Attempts, manifests, or Scores;
- statistical-significance or GA claims from a small live sample.

## 4. Terms and durable relationship

The durable relationship is:

```text
EvalSet
  └── Cell = Scenario × Subject × Executor
        └── Attempt 1..N
              ├── Outcome
              ├── Evidence Manifest
              └── Score 0..N
```

- **EvalSet** freezes one ordered experiment matrix, repetitions, limits,
  scorer selections, and pairing rules.
- **Scenario** declares the task, fixture, ordered actions, required executor
  capabilities, evidence roles, and scorer/verifier criteria.
- **Subject** is a secret-free semantic identity for one OCH version, model,
  Context/Policy/Tool configuration, and production limits.
- **Executor** is the execution surface: `in_process` or `acp_subprocess`.
- **Attempt** is one execution of one Cell and repetition index. Retry always
  creates another Attempt.
- **Outcome** classifies execution and collection, not behavioral quality.
- **Evidence Manifest** is the only artifact inventory a scorer may read.
- **Score** is one immutable scorer result over one manifest digest.

Scenario, Subject, Executor, Attempt, manifest, and Score identifiers are
opaque lowercase ASCII IDs bounded to 128 bytes. User-provided IDs use
`[a-z0-9][a-z0-9._-]*`; generated Attempt and Score IDs use cryptographically
random 128-bit lowercase hex. Paths are never identities.

## 5. Ownership and dependency boundary

The subsystem adds one architecture-guard owner, `ownerEval`, rooted at
`internal/harness/eval`.

```text
cmd/och-eval
    │
    └── internal/harness/eval
          ├── in-process executor ──> composition.Open ──> Application/Session
          └── ACP executor ─────────> internal/client/acp ──> och -acp
```

`internal/harness/eval` owns model validation, matrix expansion, Attempt
orchestration, filesystem publication, evidence collection, scoring, regrade,
and reporting. It may import `application`, `composition`, `transcript` types
where necessary, and `internal/client/acp`; it may use `os` and `os/exec` for
its explicitly-owned isolation and subprocess duties. It must not construct or
import concrete harness adapters. Composition remains the only adapter owner.

`cmd/och-eval` owns flag parsing, signal handling, stable exit codes, and
human/JSON reporting. It contains no Scenario semantics and constructs no
harness adapter.

Application, Domain, Engine, Context Engine, transcript, adapters, and
Composition must not import eval. Eval does not add a second composition root,
change Session authority, or persist eval facts in the Session stream.

## 6. Versioned wire documents

Eval-owned JSON documents use UTF-8, reject duplicate keys and unknown fields,
and include `schema` and `formatVersion`. v1 schemas are:

| Document | `schema` | Publication |
| --- | --- | --- |
| EvalSet | `och.eval.set` | frozen once before expansion |
| Scenario | `och.eval.scenario` | checked in with the suite |
| Subject | `och.eval.subject` | frozen once in the EvalSet |
| Executor | `och.eval.executor` | frozen once in the EvalSet |
| Attempt | `och.eval.attempt` | written once before execution |
| Outcome | `och.eval.outcome` | written once after execution/recovery |
| Evidence Manifest | `och.eval.evidence-manifest` | published last after collection |
| Score | `och.eval.score` | appended once per scoring/regrade invocation |
| Report | `och.eval.report` | derived; never an authority for Attempts |

`formatVersion` is `1`. Canonical identity digests are SHA-256 over the exact
validated, canonical JSON bytes of the referenced frozen document. Credentials,
environment values, absolute machine-local paths, wall-clock timestamps, and
artifact output never enter Scenario/Subject semantic digests.

Decoders fail closed on unsupported format versions. A future reader may skip
an unknown optional artifact role, but may not skip an unknown required role or
an unknown Outcome/verdict value.

## 7. Scenario contract

Scenarios live under a checked-in suite directory:

```text
eval/scenarios/<suite>/<scenario>/
  scenario.json
  fixture/
```

`scenario.json` contains:

- stable Scenario ID and human description;
- fixture digest and bounded copy policy;
- ordered actions;
- required executor capabilities;
- required and optional evidence roles;
- deterministic verifier IDs and live judge criteria IDs;
- per-Scenario limits that may only narrow EvalSet limits;
- pairing tags used by baseline/candidate reports.

v1 actions are `prompt`, `compact`, `cancel`, `restart`, and `collect`.
`prompt` carries bounded UTF-8 text. `compact` carries the public summary/reset
strategy and optional bounded focus. `cancel` names a prior in-flight action.
`restart` requests a production-surface shutdown/crash/reopen sequence supported
by the selected Executor. `collect` requests a declared workspace path or
verifier fact.

The runner rejects an unsupported Scenario/Executor pairing before creating an
Attempt. It never silently skips an action or a required capability.

Scenario files cannot name arbitrary executables. Deterministic verifier IDs
resolve through the concrete verifier catalog compiled into `och-eval`. Adding
a verifier is a reviewed code change with tests, not data-file code execution.

## 8. Fixture isolation

Each Attempt receives a new absolute root with independent `workspace/`,
`database/`, `audit/`, `evidence/`, and process/log directories. No resource is
reused across Attempts, including repeated executions of the same Cell.

Fixture copy walks without following symlinks. It rejects absolute paths,
`..` escape, symlinks, hard-link count greater than one, sockets, devices,
FIFOs, and any unsupported file type. It preserves only regular-file executable
bits and directory structure. It enforces file-count, per-file, and aggregate
fixture limits before the Subject starts.

The resulting workspace is writable by the Subject but cannot escape the
existing workspacefs/localexec jail. Fixture source stays read-only. Evidence
collection repeats path containment and file-type checks; a safe input copy
does not make later Subject-created symlinks trustworthy.

## 9. EvalSet and matrix expansion

An EvalSet freezes:

- ordered Scenario references and digests;
- ordered Subject snapshots and digests;
- ordered Executor snapshots and digests;
- repetition count and deterministic pairing seed;
- verifier/judge configuration digests;
- global limits and artifact root;
- `fixture` or `live` lane.

Expansion order is Scenario, Subject, Executor, then repetition index. A Cell
that lacks a required executor capability is a validation error for the whole
set, not a skipped row. Expansion above 4,096 Attempts fails before any
Attempt directory or Provider resource is created.

Resume reopens the exact frozen EvalSet. A changed Scenario, Subject, Executor,
scorer, lane, limit, or digest requires a new EvalSet ID. Resume schedules only
Cells without a terminal manifest; it never mutates completed Attempts.

## 10. Subject identity and secret handling

The OCH Subject snapshot includes:

- repository revision and dirty-state marker;
- Provider adapter kind, normalized endpoint identity, model ID, context window,
  and maximum output;
- Context percentages, chunk/recovery/pruning caps, and compaction timeout;
- Policy mode, tool catalog identity, Application limits, and sandbox policy;
- deterministic fixture-provider identity or live-provider identity;
- optional frozen price-table digest for cost reporting.

The snapshot records the credential environment-variable *name*, never its
value. The normalized endpoint excludes userinfo and query strings. Absolute
workspace, database, audit, artifact, binary, and temporary paths are Attempt
facts, not Subject identity.

Before publication, all JSON/log/diagnostic fields pass the existing redaction
policy plus exact-key suppression for environment values and authorization
headers. Eval never captures the complete process environment.

## 11. Executor identity and parity

Executor identity is independent of Subject identity.

`in_process` records the OCH revision, eval build revision, and Composition
contract version. `acp_subprocess` additionally records the exact `och` binary
SHA-256, normalized argv without credential values, ACP protocol version, and
reported agent name/version from `initialize`.

Parity comparisons require equal Scenario and Subject semantic digests and may
compare only declared semantic invariants. Event IDs, command IDs, runtime IDs,
timestamps, temp paths, scheduling order, and raw transcript/audit bytes are
not parity fields. Session/Turn terminal state, tool facts, usage facts,
workspace results, policy decisions, request-envelope properties, and artifact
completeness are valid parity fields.

## 12. Attempt filesystem and atomic publication

The default local artifact root is `.eval/`, which is gitignored. One EvalSet
uses:

```text
.eval/sets/<set-id>/
  set.json
  attempts/<attempt-id>/
    attempt.json
    outcome.json
    evidence/
      manifest.json
      transcript.jsonl
      audit/
      workspace/
      verifier/
      diagnostics.json
      stdout.log
      stderr.log
    scores/<score-id>.json
  reports/<report-id>.json
```

`attempt.json` is atomically published before Subject startup and is immutable.
`outcome.json` is atomically published at most once. Evidence files publish
before `manifest.json`; the manifest is the commit marker for a scoreable
Attempt. A Score publishes into a new path and never replaces another Score.

Atomic publication uses a same-directory temporary file, bounded write, file
sync, close, rename-without-overwrite, and directory sync where the platform
supports it. Any failure leaves either the old state or an uncommitted temp
file. Startup removes only eval-owned temp names after recording diagnostics;
it never guesses that an uncommitted file was complete.

## 13. Outcome taxonomy

Outcome is execution classification, not quality:

| Status | Meaning |
| --- | --- |
| `completed` | executor reached the Scenario boundary and required collection completed |
| `subject_failed` | OCH/provider/tool/protocol behavior failed, but runner authority and evidence classification remain sound |
| `infra_failed` | fixture, spawn, storage, runner, host, or required collection infrastructure failed |
| `indeterminate` | durable evidence cannot prove whether Subject or infrastructure owns the failure |

An expected terminal OCH failure may have `subject_failed` Outcome and still
receive a passing deterministic Score for a negative Scenario. Conversely, a
`completed` Outcome may receive a failing quality Score.

Outcome records stable code, bounded safe message, start/end/duration,
terminal Session/Turn facts when known, limit/truncation facts, collection
status, recovery status, and the exact Attempt identity. Free-form raw provider
or process errors go only to bounded redacted diagnostics.

## 14. Evidence Manifest

Each manifest entry contains:

- normalized relative path;
- stable role and media type;
- SHA-256 and byte length;
- `required` boolean;
- `collected`, `missing`, `truncated`, or `rejected` state;
- stable reason code and bounded safe detail when not collected;
- producing step/verifier identity when applicable.

The manifest also records total bytes, file count, collection start/end,
Outcome digest, and collection diagnostics digest. The manifest does not hash
itself; a Score references SHA-256 of the exact published manifest bytes.

Required OCH evidence is:

1. the complete native `och.session.transcript` with valid snapshot and
   complete trailers;
2. a canonical, verified audit-replica snapshot from the Attempt's isolated
   database, covering the same terminal head;
3. frozen Scenario, Subject, Executor, Attempt, and Outcome documents;
4. usage, timing, enforced-limit, truncation, and collection diagnostics;
5. every bounded workspace/verifier artifact required by the Scenario.

The transcript remains the trajectory surface. The audit replica is not a
second transcript: it is existing canonical append evidence and supplies facts
the transcript deliberately omits, including `model.request.recorded` and
`policy.decision.recorded`.

A SQLite backup is optional and only valid when a recovery Scenario requires
it. It is not default evidence. Scorers cannot open the live database or follow
paths absent from the manifest.

Evidence collection requires narrow Composition-owned helpers. Transcript
collection uses the existing `composition.ExportSession`. The implementation
adds a Composition-owned canonical audit snapshot/verification operation so
eval never imports SQLite. Both executors call these helpers only after their
writer has stopped.

## 15. In-process executor

One Attempt constructs one `composition.Config`, calls `composition.Open`,
creates one Session through `Service.CreateSession`, and drives actions through
public Application methods such as `RunTurn` and `CompactSession`. It never
calls Engine, Provider, Context Engine, Store, or an adapter directly.

The executor does not reuse an Assembly or Session between Attempts. It closes
the Assembly on every terminal path with its own bounded shutdown context. A
Scenario request sink may collect bounded live diagnostics, but canonical
scoring evidence comes from transcript/audit/workspace after shutdown.

Fixture lane uses the real OpenAI-compatible adapter against the repository's
loopback fixture server. Live lane uses the frozen real Provider configuration.
There is no eval-only Provider interface or Application branch.

## 16. ACP subprocess executor

The executor starts the exact configured `och` binary with `-acp`, an isolated
workspace/database/audit directory, unique runtime ID, and the Subject's full
production configuration. It uses `internal/client/acp` for `initialize`,
`session/new`, `session/load`, `session/prompt`, and `session/cancel` and captures
bounded ACP diagnostics. It may not replace the process with an in-memory ACP
adapter in conformance tests.

The existing `och` assembly flags do not expose every Subject semantic knob.
Implementation therefore extends the shared `bindAssemblyFlags` surface with:

```text
-max-steps
-max-tool-calls-per-step
-max-assistant-bytes
-approval-timeout
-context-trigger-percent
-context-target-percent
-context-tail-percent
-context-max-summary-chunks
-context-max-overflow-compactions-per-turn
-context-max-pruned-tool-results-per-request
-context-compaction-timeout
```

Both normal `och -acp` and `och compact-session` use the same bindings. The ACP
executor derives argv from the same validated Subject snapshot used by the
in-process executor. Credential values remain in the named environment
variable and never enter argv.

The child receives a minimal allowlisted environment: required OS runtime
variables, the named Provider credential, and explicitly declared fixture
variables. The runner never forwards its complete environment.

The ACP Handler is non-interactive. Scenario/Subject policy freezes approval
responses; any undeclared permission request is denied and recorded. Manual
`compact` stops the ACP writer cleanly, invokes the same binary's public
`compact-session` command against the isolated database, restarts `och -acp`,
and uses `session/load`. It never calls Application in-process.

## 17. Cancellation and subprocess cleanup

In-process cancellation cancels the current action context, waits for its
durable terminal result, then closes the Assembly within the shutdown bound.

ACP cancellation uses this fixed escalation:

```text
session/cancel
  → wait cancellation grace
  → close child stdin
  → wait shutdown grace
  → SIGTERM/process-group terminate
  → wait final grace
  → force-kill process group
  → reap
```

`exec.CommandContext` is not the primary cancellation mechanism because an
immediate kill can discard the Session terminal evidence the scenario measures.
Every escalation stage is timed and recorded. The executor owns one process
group and reaps it on all normal paths.

The ACP child's stdin belongs only to the runner. Parent death closes the pipe,
which makes `och -acp` observe EOF and shut down. Recovery never signals a PID
stored by an earlier process because PID reuse can target an unrelated process.

## 18. Crash recovery and resume

Runner startup classifies each Attempt directory:

- valid Outcome plus valid Manifest: immutable terminal Attempt;
- Attempt plus Outcome but no Manifest: resume bounded evidence collection only;
- Attempt with no Outcome: inspect the isolated canonical store without running
  the Subject;
- no valid Attempt document: uncommitted temp directory, never an Attempt.

If canonical evidence proves a terminal Session/Turn, recovery publishes an
Outcome with `recoveryStatus: recovered` and collects evidence. If the Session is
active/running, commit outcome is unknown, the store is corrupt, or sources
contradict each other, recovery publishes `indeterminate` with exact diagnostics.

Recovery never reruns a prompt, retries an append, resumes the Subject, or
changes an existing Outcome. An operator retry appends a new Attempt.

## 19. Resource and privacy limits

All limits are positive after defaults and can only be narrowed by Scenario.

| Limit | Default | Hard maximum |
| --- | ---: | ---: |
| concurrent Attempts | 1 | 8 |
| expanded Attempts per EvalSet | 256 | 4,096 |
| Attempt wall time | 15 min | 2 h |
| Turn/action time | 5 min | 30 min |
| process startup | 30 s | 2 min |
| cancellation grace | 10 s | 1 min |
| shutdown grace | 10 s | 1 min |
| evidence collection | 2 min | 10 min |
| fixture files | 10,000 | 100,000 |
| artifact files | 10,000 | 100,000 |
| one artifact | 16 MiB | 64 MiB |
| total Attempt artifacts | 256 MiB | 1 GiB |
| stdout and stderr, each | 8 MiB | 64 MiB |

An EvalSet must provide a positive per-Attempt token cap. Usage accumulates
after each Turn; once the cap is reached, no next Turn starts. A single in-flight
Turn remains bounded by Provider maximum output and Application limits.

Cost cap is optional. When enabled it requires a frozen user-supplied price
table in integer microunits and is checked after each usage record. Missing or
unknown pricing is `unavailable`, never zero; a configured cost cap with
unavailable pricing fails validation.

All clips and refusals are structured Outcome/manifest facts. Logs alone are
not evidence. Live evidence remains local and gitignored; publication outside
the artifact root is a separate operator action.

## 20. Score and offline regrade

Scoring begins only after a valid manifest commit marker exists. A scorer gets
the manifest bytes and an artifact reader constrained to collected manifest
entries. It receives no Executor, Service, Provider, network client, live Store,
or unrestricted filesystem handle.

A Score records:

- manifest digest and Outcome digest;
- scorer ID, implementation version, config digest, and lane;
- `pass`, `fail`, or `indeterminate` verdict;
- bounded numeric score and per-criterion results;
- evidence references into manifest entries;
- missing/contradictory evidence;
- bounded safe rationale;
- usage/timing/cost of the scorer itself when applicable.

`och-eval regrade` reads one published Attempt, verifies every referenced digest,
and appends a new Score. It never runs or resumes the Subject and never replaces
an earlier Score. Corrupt manifest bytes, missing required artifacts, digest
mismatch, unsupported schema, or unavailable required evidence yields
`indeterminate` or a scoring infrastructure error; never `pass`.

## 21. Deterministic verifiers and model judges

Deterministic verifiers are compiled, versioned implementations. They can check
transcript/audit invariants, Session/Turn terminals, workspace contents, usage,
limits, policy/request facts, and parity. Infrastructure assertions and quality
criteria remain separate fields.

Model judges run only in the live lane. The judge is distinct from the Subject
executor and has its own frozen model/config/prompt digest. It receives only
bounded, redacted evidence selected by declared criteria. Every Subject-authored
string, transcript field, tool result, file, and prior rationale is wrapped as
untrusted evidence and cannot issue judge instructions.

Judge output uses a strict schema with unknown fields denied:

- verdict: `pass`, `fail`, or `indeterminate`;
- numeric score;
- per-criterion score and status;
- manifest evidence references;
- missing or contradictory evidence list;
- one bounded rationale.

Malformed output, a nonexistent evidence reference, required evidence omission,
or unresolved contradiction is `indeterminate`. v1 does not require a multi-
judge quorum; adding one later appends independent Scores and an aggregate Score
rather than hiding individual verdicts.

## 22. Baseline/candidate pairing and reports

Baseline and candidate arms pair only when Scenario digest, Executor kind,
repetition index, fixture digest, limits, and pairing seed match. Subject digest
must differ in at least one declared semantic field.

Reports retain every raw Attempt and Score reference, then derive counts,
failure taxonomy, pass rate, score distribution, token/cost/latency, and paired
deltas. They never discard infra failures from denominators without showing both
raw and filtered views. Missing pairs are explicit.

PR reports may gate on deterministic verifier failures and configured
deterministic floors. Model-judge results never gate an ordinary PR. Live/nightly
quality floors are advisory milestone signals until sample size, judge meta-eval,
provider breadth, and variance policy have separate accepted evidence.

## 23. PR fixture lane

The fixture lane is the default and fails closed if configured with a non-
loopback Provider endpoint or a live credential requirement. It uses no external
network and no secrets. Loopback fixture HTTP is allowed as an in-process test
transport, not external network access.

Both executors run in PR CI. The ACP path builds the real `och` binary in the
test and launches it; a fake ACP agent is insufficient completion evidence.
Deterministic fixtures use frozen responses and compare semantic facts rather
than timestamps or generated IDs.

## 24. Live lane

Live execution requires the explicit `--live` flag and a `live` EvalSet. Either
condition missing rejects startup before reading a credential. Live runs are
local explicit invocations or scheduled nightly work, write an independent
artifact root, and are not ordinary PR checks.

The runner records Provider/model identity and credential environment-variable
name but never the value. It does not upload evidence. A run interrupted by
budget, operator cancellation, or infrastructure still publishes the strongest
honest Outcome and manifest possible within the evidence-collection bound.

## 25. Initial suites

### 25.1 Executor parity fixture

Runs the same deterministic Scenario and Subject semantics through both
executors. It verifies terminal Session/Turn shape, tool and usage facts,
workspace result, request/policy evidence, manifest completeness, and declared
parity fields. It does not demand identical IDs, timestamps, paths, or bytes.

### 25.2 Tool/workspace deterministic suite

Covers read, write, exec, Policy/approval, expected failure, cancellation,
redaction, and artifact collection. It cross-checks workspace results against
transcript and audit evidence and runs through both executors in PR CI.

### 25.3 Context mechanism fixture suite

Deterministically covers pre-turn/mid-turn triggers, checkpoints, manual
summary/reset, overflow recovery, restart/crash evidence, transcript/audit
projection, and resource bounds. Accepted-but-inert capabilities cannot pass by
configuration presence alone; a Scenario claiming multi-chunk summary or Tool
Result pruning must observe the behavior or fail.

### 25.4 Context real-model quality suite

Runs only live. Criteria cover summary fidelity, preservation of constraints and
decisions, tool-result attribution, long-task continuity, quality, token use,
latency, and stability. Deterministic invariants run before the model judge.

Context is an initial suite because it is a disclosed GA blocker, not because
eval belongs to Context Engine. Recovery, Provider, broader ACP, Policy, and MCP
suites use the same runner. MCP absence does not block the eval system; it only
blocks a truthful MCP suite.

## 26. CLI contract

The initial commands are:

```text
och-eval run     -set PATH -artifacts PATH [--live]
och-eval regrade -attempt PATH -scorer ID
och-eval report  -set PATH [-output PATH]
```

Machine output is one versioned JSON document on stdout. Human diagnostics go
to stderr. Exit codes distinguish validation, deterministic gate failure,
infrastructure failure, indeterminate completion, and internal error. Quality
failure in a non-gating live run is represented in the report and does not
masquerade as infrastructure failure.

`run` refuses an artifact root inside a fixture workspace and refuses a live
set without `--live`. `regrade` has no Executor flags or Provider credential for
the Subject. A model-judge scorer may require its own explicit live judge
configuration and credential.

## 27. Testing and completion evidence

Implementation tests must include:

- strict JSON decoding, canonical digests, duplicate/unknown fields, and schema
  version rejection;
- matrix expansion, capability mismatch, pairing, and 4,096-Attempt refusal;
- fixture and evidence path traversal, symlink/hard-link/device rejection,
  file-count/byte caps, truncation, and digest tampering;
- atomic publish fault injection at Attempt, Outcome, manifest, and Score stages;
- restart classification for every partial filesystem state;
- in-process proof through real `composition.Open` and Application methods;
- ACP proof against a freshly built real `och` binary, including initialize,
  new/load/prompt/cancel, manual compact, restart, process-group cleanup, and
  no leaked child;
- exact Subject-to-CLI configuration parity for every Limits/Context field;
- transcript trailer and audit-head agreement, request/policy evidence, and
  missing/corrupt evidence fail-closed behavior;
- offline regrade with Executor/Subject invocation made impossible;
- deterministic verifier versus model-judge separation and strict judge parser;
- fixture lane rejection of external Provider/credential use;
- live dual-consent (`live` set plus `--live`) before credential access;
- time/token/cost/concurrency/artifact limits and cancellation races;
- semantic parity fixtures for both executors;
- initial suite golden fixtures and live-suite dry validation.

Fresh completion evidence includes:

```text
go test ./...
go test -race ./...
go vet ./...
```

plus targeted subprocess interoperability, corruption/fault tests, cancellation
leak checks, deterministic regrade proofs, and benchmarks at 1, 100, 1,000, and
4,096 expanded Attempts without real model calls.

Mutation evidence must independently kill at least: executor shortcut,
Subject/Executor digest omission, manifest-last publication, missing-required-
evidence pass, transcript/audit head mismatch, retry overwrite, live consent,
token cap, artifact path containment, and ACP force-kill/reap behavior.

## 28. Documentation, maturity, and evidence ledger

Implementation publication must add:

- `docs/architecture/evaluation.md` and synchronized Chinese reading copy;
- `docs/architecture/evaluation-evidence.md` with task commits, mapping tables,
  commands and actual output, fault/mutation results, benchmark environment,
  deviations, and open blockers;
- Scenario authoring, live-run privacy/cost, regrade, and operator guidance;
- authority-table, milestone, root README, and CLI help updates.

The subsystem remains not GA until real-model Context quality has repeated
evidence, judge meta-evaluation exists, provider/model coverage is wider, and
variance/baseline policy is documented. Fixture success proves runner mechanism,
not universal agent quality.

## 29. Implementation boundary and likely file map

The implementation plan may refine file names but may not collapse the approved
boundaries:

```text
internal/harness/eval/
  model.go            # versioned frozen documents and validation
  digest.go           # canonical bytes and SHA-256 identity
  scenario.go         # checked-in Scenario loading and capabilities
  matrix.go           # EvalSet expansion, pairing, resume inventory
  store.go            # bounded append-only filesystem publication
  manifest.go         # artifact inventory and constrained readers
  runner.go           # Attempt orchestration and limits
  executor.go         # internal executor contract
  inprocess.go        # real Composition/Application executor
  acp.go              # real och subprocess + internal/client/acp
  recovery.go         # partial Attempt classification, never rerun
  verifier.go         # compiled deterministic verifier catalog
  judge.go            # strict bounded live judge harness
  score.go            # immutable scoring and offline regrade
  report.go           # raw and paired derived reports

cmd/och-eval/
  main.go             # run/regrade/report CLI only

cmd/och/, internal/harness/composition/
  complete Limits/Context flag parity and Composition-owned audit evidence export

eval/scenarios/
  executor-parity/
  tool-workspace/
  context-mechanism/
  context-quality/

internal/harness/architecture/
  ownerEval dependency rules
```

The later implementation plan must deliver independently reviewable slices. It
may stage one executor before the other, but milestone completion requires both;
ACP subprocess support is not an optional follow-up under this accepted design.

## 30. Acceptance summary

The design is satisfied only when:

1. both executors run real OCH surfaces with equivalent frozen Subject semantics;
2. every Attempt is isolated, bounded, append-only, and honestly classified;
3. manifest-published evidence can be verified and regraded offline;
4. transcript and audit evidence jointly cover declared scoring needs without a
   second trajectory format;
5. corrupt or missing required evidence never passes;
6. deterministic PR and explicit live channels cannot be confused;
7. retry/recovery never rewrites an Attempt or reruns unknown work in place;
8. initial suites demonstrate executor neutrality and Context quality coverage;
9. no MCP implementation is required to build the runner, and no MCP evaluation
   is claimed before MCP exists;
10. documentation and the evidence ledger disclose remaining quality and GA
    blockers rather than inferring them from fixture success.
