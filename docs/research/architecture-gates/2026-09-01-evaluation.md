# Evaluation Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-09-01

**Scope:** Milestone 10 in `docs/README.md` still names "scenario evaluation,
benchmarks, and OpenTelemetry" as one undesigned item. The
[2026-09-01 roadmap gate](2026-09-01-context-engine-evaluation-observability-tui.md)
surveyed Evaluation at package-entry depth only and said each of Context
Engine, Evaluation, Observability, and TUI still needed its own
subsystem-specific architecture gate before a normative design. Context
Engine has since had that gate, a design, and an implemented contract.
This is Evaluation's dedicated gate.

This document re-verifies then-current primary sources for agent-quality
evaluation, matches them against what this project already has, records the
constraints those sources actually support, and leaves the remaining product
positioning choices explicit for the later design. It does not design package
layout, scenario syntax, CLI flags, or an implementation plan. OpenTelemetry
is explicitly out of scope: it remains the undesigned remainder of milestone
10 and needs its own gate.

English is normative. The Chinese file is a synchronized reading copy.

## Comparison set and pinned commits

Per Documentation rule 8, repository citations below are from gitignored
`.reference/` checkouts obtained via `./scripts/fetch-reference.sh --list`.
The current OpenAI product documentation is the explicit exception: it was
fetched directly from official `developers.openai.com` Markdown on the observed
date. Per Documentation rule 7 this gate does **not** reuse the 2026-09-01
roadmap gate's Evaluation section: every official comparison-set checkout
except `earendil-works/pi` has moved since that survey, and Evaluation was only
read at README/package-entry depth there.

The official six-project set remains mandatory. Five additional eval-native
repositories were fetched, and current official OpenAI guidance was read,
because the six-project set alone cannot answer the architecture-boundary
question this gate has to settle (product regression of this harness versus a
general runner of arbitrary agents).

| Project | Repository | Commit | Observed | Why fetched |
| --- | --- | --- | --- | --- |
| Pi (agent core source) | `badlogic/pi-mono` | `b8b873b` | 2026-09-01 | `packages/evals` — real `AgentSession` adapter, isolated temp dirs, native session artifacts. `earendil-works/pi` is still `853a80d` (2026-08-28) and is not the eval source of truth; evals live here. |
| Maka | `maka-agent/maka-agent` | `afbcabd` | 2026-09-01 | `packages/eval` — Experiment/Cell/Attempt, Subject/Executor split, append-only store, `subject_failed`/`infra_failed`/`indeterminate`, Maka and external subjects |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `dd6322d` | 2026-09-01 | Re-verify the three-line `BENCHMARK.md` negative |
| Codex | `openai/codex` | `67cc3c3` | 2026-09-01 | Re-verify absence of an agent-quality eval package |
| Kimi Code | `MoonshotAI/kimi-code` | `ab565e0` | 2026-09-01 | Re-verify absence of an agent-quality eval package |
| Grok Build | `xai-org/grok-build` | `bb7f39d` | 2026-09-01 | No standalone offline eval package; online goal evaluator, evidence capture, and adversarial skeptic verification are relevant judge precedents |
| Harbor | `laude-institute/harbor` | `e348ba3` | 2026-09-01 | General agent/benchmark runner: Task + Verifier, artifact manifest, regrade without re-running the agent, installed adapters for many agents |
| Terminal-Bench | `laude-institute/terminal-bench` | `d28711d` | 2026-09-01 | Task dataset shape (instruction + tests + oracle); its own README now points new users at Harbor |
| Inspect AI | `UKGovernmentBEIS/inspect_ai` | `84e512d` | 2026-09-01 | Task/Sample/Scorer layering, recoverable eval set, sample retry, offline `score(log)`, time/token/cost limits |
| OpenAI official Evals and trace grading | `developers.openai.com/api/docs/guides/evals` and `trace-grading` | current docs | 2026-09-01 | Current dataset/testing-criteria/run model, trace grading over end-to-end agent traces, and the published Evals-platform deprecation boundary |
| vitest-evals | `getsentry/vitest-evals` | `aa34b64` | 2026-09-01 | Harness-first contract Pi actually adapts; infra assertions vs judges; configurable CI gate |
| OpenAI Evals (legacy) | `openai/evals` | `8eac7a7` | 2026-09-01 | Dataset versioning, Recorder, model-graded templates — checked as a non-blueprint for a stateful session runner |

## What this project already has

There is no evaluation runner. `internal/harness` has no `eval` package;
`EvaluationResult` is named in the charter
(`docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md:140`)
and is not a domain type, event, or projection in code. Milestone 10 in
`docs/README.md` is still "not designed yet." The architecture-guard
ownership table (`internal/harness/architecture/dependencies_test.go`) has
no eval owner.

The project is not starting from a blank execution path.

- **The charter already says what an Eval Runner is.** §6.9
  (`2026-08-11-open-code-harness-architecture-design.md:213-225`): the
  runner "直接驱动应用层或 headless Engine，不依赖 TUI"; ACP tests are a
  separate black-box conformance layer; the taxonomy already includes
  domain unit tests, Provider contracts, Tool/Policy security, Context
  fixtures, deterministic replay, ACP/MCP conformance, interrupt/recovery
  tests, **real-repository scenario eval**, and
  quality/cost/latency/stability baselines. Most implemented portions already
  have `go test` coverage, but MCP remains designed and unimplemented, so the
  ACP/MCP category is not complete. Real-repository scenario evaluation and
  regression baselines do not exist yet.
- **§10.2 names `Evaluator` as a community extension surface**, alongside
  Provider, MCP server config, Policy, and Observer
  (`2026-08-11-open-code-harness-architecture-design.md:289`). The charter
  does not define that extension's grain, so this line alone cannot decide
  between an OCH-only evaluator and an external-subject boundary. §4 does
  forbid extension points without a real consumer
  (`2026-08-11-open-code-harness-architecture-design.md:92`).
- **The real Application/Session path already runs end to end without a
  network.** `internal/harness/composition/end_to_end_test.go:24-34`
  (`TestAssemblyRunsAToolCallingTurnEndToEnd`) opens `composition.Open`
  against a real SQLite file, the OpenAI-compatible adapter talking to a
  loopback fixture server, workspace fs, and the real Policy Decide table.
  `CreateSession` + `RunTurn` is the headless loop §6.9 named. A future
  eval executor that invents a second loop around Engine or Provider would
  be an eval-only shortcut this charter already rejected.
- **Native session evidence already exists.**
  `docs/architecture/session-transcript.md:15-19`: experimental
  `och.session.transcript` JSONL is a projection of one EventStore
  session, not a replica, not a commit point, and not writable back.
  `och export-session` is the export command. Eval must not invent a
  second transcript format for OCH runs.
- **Composition already freezes the knobs a Subject would name.**
  `internal/harness/composition/config.go` carries Provider
  (`BaseURL`/`ModelID`/`ContextWindow`/`MaxOutput`), Context budget
  percents, Policy mode, Limits, workspace root, and sandbox-related
  flags. There is no versioned "subject identity" object wrapping them.
- **Context Engine quality is a disclosed GA blocker, not a substitute
  for the eval system.** `docs/architecture/context-engine.md:374-376`:
  the milestone stays not GA until real-model quality evaluation of
  rolling summaries exists; current tests use scripted/fixture
  summarizers. The same ledger also says "No MCP, TUI, OpenTelemetry, or
  milestone 10 evaluation-runner surface." Context Engine is one future
  suite, not the eval system's package boundary.
- **The repository already distinguishes PR CI from a nightly lane.**
  `docs/README.md:204-210`: pull-request `go test` stays keyless and
  network-free; `determinism` and live citation checks run nightly. Live
  model calls are not a PR gate today (`composition-root` design
  non-goal 4: "Live provider calls in the default verification path").

## Official comparison set

### Pi — real AgentSession, isolated dirs, native artifacts, live-only

`packages/evals/README.md:1-4` states the package adapts a real
`AgentSession` to `vitest-evals`, runs it "in isolated temporary project
and agent directories," and attaches native Pi session artifacts. It
measures end-to-end behavior and compares "prompts, tools, skills, models,
or other harness configurations" — Pi configurations, not foreign agents.

`src/pi-harness.ts` is the adapter. `createPiCodingAgentHarness` (lines
246-256) binds one harness per suite. `runPiCodingAgent` (lines 122-151)
creates `mkdtemp(.../pi-eval-)`, then `workspace/`, `agent/`, and a
`SessionManager` under `sessions/`, then `createAgentSessionFromServices`
and `session.prompt` — the product session object, not an eval double.
Before deleting the temp tree it snapshots the native session file as an
artifact (lines 213-218). README lines 32-34: each invocation prints a
gitignored `.eval/` directory; `runs.jsonl` indexes completed runs and
their native Pi session JSONL under `sessions/`.

Live models are required. README lines 8-23: `PI_PROVIDER` and `PI_MODEL`
must be set together; authentication is Pi's normal `ModelRuntime`. There
is no fixture-provider CI lane inside `packages/evals`. Comparative
suites (`README.md:104-138`) run the same inputs against multiple Pi
harnesses that differ by prompt, tools, skills, or model;
`judgeThreshold: null` keeps a low score as an observation rather than a
Vitest failure. "Use hard assertions only for suite invariants and
infrastructure contracts."

Pi is the nearest execution-path precedent for this project: drive the
real harness object, isolate each run, keep the native session as
evidence. It is not a general agent framework, and it does not solve
this project's PR-CI channel.

### Maka — experiment semantics owned outside Runtime; Subject ≠ Executor

`packages/eval/README.md:20-28`:

> `@maka/eval` owns experiment semantics. It does not execute Maka or
> construct Runtime objects.
>
> `Experiment → Cells → Attempts → Results`
> Runtime Host executes Maka subjects

An Experiment freezes one benchmark, one executor, all subjects, all
tasks, a repetition count, one budget, one verifier
(`README.md:40-41`; `src/experiment.ts:46-70`). Cells are the Cartesian
product `task × repetition × subject` (`experiment.ts:84-101`). The
experiment directory holds the frozen `experiment.json` and append-only
attempt records; "there is no second mutable results file"
(`README.md:42-43`).

Subject and Executor are separate ports (`src/runner.ts:74-130`).
`SubjectAdapter.execute` invokes one cell's agent;
`ExperimentExecutor.runAttempt` decides how the attempt is isolated and
verified. Built-in subjects are `kind: 'maka' | 'external'`
(`experiment.ts:56-61`). `createMakaSubjectAdapter`
(`src/maka-subject.ts:34-77`) asks Runtime Host to run one owned
execution in a dedicated Host root — Session/Turn stay inside Runtime
Host. `createExternalSubjectAdapter` (`src/external-subject.ts:39-43`)
runs a declared command, used for the checked-in DeepSeek Harness arm.

Attempts are append-only (`src/attempt-store.ts:26-80`,
`FileAttemptStore.append` requires `sequence == last+1`). Result status
is `'completed' | 'subject_failed' | 'infra_failed' | 'indeterminate'`
(`src/result.ts:31-41`). `isReplaceableAttempt` (lines 87-89) allows
retry only of `infra_failed` and `indeterminate`. `selectCellResult`
(lines 91-95) always takes the earliest valid attempt. README line 80:
`--cell` replaces one failed or indeterminate cell; "result selection
always uses the earliest valid attempt."

The result kernel is score, normalized usage, attributable cost,
duration, status, and artifacts (`README.md:49`; `result.ts:33-41`).
Specs carry every semantic setting; environment variables are reserved
for credentials and machine-local paths.

Maka today can run an external harness arm — the checked-in
`experiments/terminal-bench-2.1-deepseek-v4-flash-maka-vs-deepseek-harness.json`
pairs Maka and DeepSeek Harness in one task group, on Harbor, with
Docker and an egress proxy. That is Maka's *current* completeness, not
the shape this project's first slice should copy. The load-bearing
precedent for OCH is the split itself: eval owns experiment semantics;
the product runtime executes product subjects; attempts are append-only;
subject failure is not infrastructure failure.

### Codex, Kimi Code, DeepSeek Harness — checked negatives

Re-verified at the commits in the table, not copied from the roadmap
gate.

- **DeepSeek Harness** `BENCHMARK.md` is still three lines: install the
  Python SDK, run the `jsonrpc-agent` variant, use separate workspaces
  and session IDs. No in-repo runner, scorer, or experiment store.
  Maka's eval README (lines 127-128) says the same thing in its own
  words while carrying a Harbor profile *of* DeepSeek Harness.
- **Codex**: no agent-quality eval package. The only `eval` hit in
  `codex-rs/Cargo.toml` is the clippy lint `unnecessary_lazy_evaluations`.
- **Kimi Code**: no `eval` package in workspace `package.json` files.

These three cannot be the blueprint. Their session logs, micro-benchmarks,
or an external Harbor profile are not an eval architecture.

### Grok Build — no offline runner, but a relevant evidence-first judge

Grok Build has no standalone offline eval crate, and Harbor's installed Grok
Build adapter is Harbor measuring it rather than Grok Build shipping a general
quality-eval subsystem. Treating that package-level absence as the whole
finding would still discard relevant primary evidence.

`xai-grok-shell/src/session/goal_evaluator.rs:7-17` defines a hidden,
tool-free completion evaluator. It sees a bounded recent transcript plus the
objective and optional plan, treats transcript content as untrusted, and
requires one strict schema-constrained decision: `continue`,
`candidate_complete`, or `blocked`, with concrete evidence, one next step, and
a stable blocker key. The parser denies unknown fields and malformed semantic
combinations (`goal_evaluator.rs:19-113`); the transcript has both per-item and
overall byte caps (`goal_evaluator.rs:115-157`).

Candidate completion then enters harness-owned adversarial verification in
`goal_classifier.rs`. The stage captures one shared changes artifact and a
complete changed-file list, sanitizes agent-authored final/plan text, and
threads previous gaps into later attempts (`goal_classifier.rs:1717-1813`).
Skeptic zero can produce a decisive high-confidence refutation; otherwise the
remaining cold skeptics fan out and approval requires the aggregate quorum
(`goal_classifier.rs:1815-1897`). Per-skeptic details and an aggregate outcome
remain evidence files/events rather than an uncited prose verdict.

This is online self-verification, not an offline scenario benchmark, so it is
not the runner blueprint. It is a direct precedent for evidence-first judge
inputs, strict structured verdicts, untrusted-evidence fencing, bounded judge
context, independent verification, and conservative treatment of missing or
contradictory evidence.

## Additional eval-native sources

### Harbor / Terminal-Bench — Task + Verifier, regrade, general agents

Harbor's README (lines 10-16) states the product goal: "Evaluate
arbitrary agents like Claude Code, OpenHands, Codex CLI, and more,"
build benchmarks, and run thousands of environments in parallel through
Daytona, Modal, and similar providers. `src/harbor/agents/installed/`
contains adapters for Codex, Pi, Grok Build, Kimi Code, Claude Code,
and many others. `BaseAgent` (`src/harbor/agents/base.py:25-59`) is the
agent port; `capabilities` include resume and native-trajectory load.

A Harbor `Task` (`src/harbor/models/task/task.py:35-50`) is a directory:
`instruction.md`, `task.toml`, `environment/`, `solution/`, `tests/`.
The Verifier (`src/harbor/verifier/verifier.py`) scores the environment
the task was left in. `ArtifactManifest`
(`src/harbor/models/trial/artifact_manifest.py:6-16`) records what was
collected. `src/harbor/trial/regrade.py:1-12` is explicit: a regrade
replaces the agent phase with "restore recorded outputs", copies
`agent/` and `artifacts/` into a fresh trial directory, and runs the
verifier against seeded artifacts. "The source trial is never modified."
Regradability is defined by the record: the new verifier's declared
inputs must be present in the source trial's artifact manifest.

Terminal-Bench (`README.md:21-28, 55-63`) is the dataset: instruction,
test script, oracle solution. Its own README now tells new users to use
Harbor to run Terminal-Bench 2.0.

Harbor is the existence proof that a general agent-eval platform is a
different product: many agent adapters, many environment backends,
container/cloud scheduling. The mechanism this project should absorb is
narrower: **Task and Verifier are separate; evidence is a manifest;
scoring does not require re-running the agent.** The scheduling platform
is out of first-slice scope.

### Inspect AI — Task/Sample/Scorer, recoverable set, offline score, limits

Inspect's public entry (`README.md:3-5`) is a framework for LLM
evaluations with prompt engineering, tools, multi-turn dialog, and
model-graded scoring. The layering in code is `eval()`
(`src/inspect_ai/_eval/eval.py:118`) over `Task`
(`src/inspect_ai/_eval/task/task.py:76-80`) over dataset `Sample`
(`src/inspect_ai/dataset/_dataset.py:29-53`) with a `Scorer` protocol
(`src/inspect_ai/scorer/_scorer.py:35-40`).

`score()` (`src/inspect_ai/_eval/score.py:81-94`) scores an existing
`EvalLog` — offline re-scoring without re-running solvers. `eval_set()`
(`src/inspect_ai/_eval/evalset.py:226`) is a recoverable set of evals.
Sample-level `retry_on_error`, `fail_on_error`, `token_limit`,
`time_limit`, `working_limit`, and `cost_limit` are first-class
parameters (`eval.py:251-276`; `evalset.py:380-400`).
`design/recover.md` documents how a crashed `.eval` ZIP plus the sample
buffer SQLite can reconstruct completed-but-unflushed samples.

Inspect is the existence proof for **recoverable eval sets, failed-sample
retry, offline re-score, and per-sample resource limits**. It is not a
code-agent harness, and it does not drive OCH sessions.

### vitest-evals — harness-first, infra vs judge, CI floors

`docs/architecture.md:5-14`: the primary contract is one explicit
`harness` per suite, named tests calling `run(input)`, one normalized
`HarnessRun`, optional judges, and optional Vitest assertions. A judge
harness is a separate object from the application harness under test
(`packages/vitest-evals/src/judges/judgeHarness.ts:45-60`).

The GitHub reporter gate (`packages/github-reporter/src/gate.ts:10-26,
52-61`) applies optional `minPassRate` / `minScoreAverage` /
`failOnFailures` floors to a combined report. Pi's eval README (lines
136-138) uses that split in practice: hard assertions for
infrastructure; judges for quality; comparative suites record scores
instead of failing the invocation.

vitest-evals is a library Pi embeds. This project should not take a
Vitest/TypeScript runtime dependency. The mechanism to absorb is the
split: **infrastructure contracts fail the run; quality scores are
observations behind a configurable threshold; the application harness
and the judge harness are not the same object.**

### OpenAI Evals (legacy) — datasets and model graders, not a session runner

`README.md:5-11` is a registry of LLM evals plus custom YAML/JSONL
datasets. `evals/record.py:1-8` defines Recorder classes that log to
local JSON or Snowflake. `docs/eval-templates.md:22-40` is the
model-graded `classify` template (wrap a completion in an evaluation
prompt, parse a choice). Completion-function protocol exists for prompt
chains; there is no Agent Session, no workspace isolation, no append-only
attempt log, no subject/infra failure split.

Useful as a reminder that datasets should be versioned and that
model-graded scoring is a known pattern. Not a blueprint for a stateful
code-agent session runner. The repository itself now points readers at
the OpenAI Dashboard evals product.

### Current OpenAI guidance — dataset criteria and trace grading, not a local runner

The official [Working with evals](https://developers.openai.com/api/docs/guides/evals)
guide describes an eval as a data-source schema plus testing criteria/graders,
then asynchronous runs over test inputs with per-criterion results and model
usage. As observed on 2026-09-01, that same page says the Evals platform is
being deprecated: read-only on 2026-10-31 and shutdown scheduled for
2026-11-30, with new users directed to Datasets. The cloud product therefore
cannot be a stable local runtime dependency or OCH's persistence authority.

The official [Trace grading](https://developers.openai.com/api/docs/guides/trace-grading)
guide defines a trace as the end-to-end log of an agent's decisions, tool calls,
and reasoning steps, then assigns structured scores or labels to that trace.
It explicitly distinguishes trace evals, which explain where behavior failed,
from black-box output evaluation. This independently supports first-class
trajectory evidence and pluggable graders. It does not define local session
isolation, append-only attempts, crash recovery, or OCH's runner boundary.

## Synthesis

Two of the six official projects ship a standalone in-repo quality-eval
harness (Pi, Maka). Grok Build instead ships an online evidence-first judge;
the other three do not expose a comparable subsystem. The eval-native extras
divide the same way the architecture-boundary question divides:

| Source | What it evaluates | Execution | What to absorb | What not to copy in v1 |
| --- | --- | --- | --- | --- |
| Pi | Pi configurations | Real `AgentSession` | Real product path, isolated dirs, native transcript | Live-only; Vitest runtime |
| Maka | Maka subjects first; external arms exist | Runtime Host / Harbor | Subject/Executor split, append-only attempt, subject vs infra vs indeterminate | Harbor/Docker/egress platform; an unused adapter matrix |
| Harbor / Terminal-Bench | Arbitrary agents | Installed agent + container/cloud env | Task/Verifier split, artifact manifest, regrade | Agent adapter matrix, env backends, parallel cloud |
| Inspect AI | Models and LLM tasks | Inspect solvers/sandboxes | Recoverable set, retry, offline score, limits | Inspect Task/Sample as OCH's domain |
| vitest-evals | Whatever harness you bind | Caller-supplied harness | Infra assert vs quality judge, CI floors | TypeScript/Vitest embedding |
| Grok Build | Completion claims inside Grok Build | Hidden evaluator + skeptic panel | Evidence-first strict judge, untrusted-input fencing, independent verification | Treating online self-verification as an offline runner |
| OpenAI legacy/current guidance | Outputs and agent traces | Completion functions or hosted runs/graders | Dataset versioning, structured criteria, trace grading | Completion-function runner or hosted platform dependency |
| Codex / Kimi / DSH | (no comparable in-repo quality eval) | — | — | Do not infer architecture from benches or session logs |

The nearest combined fit for this project is **Pi's execution path plus
Maka's experiment semantics**, with Harbor's regrade/manifest and
Inspect's offline score as scoring-side constraints, and vitest-evals'
infra-vs-judge split as the CI rule. Grok Build adds judge-side constraints:
evidence is untrusted, bounded, structured, and independently checked. This
combination does not by itself decide whether the first product surface is
OCH-only or includes an external subject adapter.

## Adopted findings

These are evidence-backed architecture constraints. Product choices that the
sources do not settle remain in the open-question section and must be decided
in the later design rather than silently promoted here.

1. **The first-slice product boundary remains a design choice.** Evidence
   supports an OCH product-regression runner whose subjects are frozen
   combinations of OCH model, composition configuration, and version.
   Maka and Harbor also prove that an external-subject adapter is viable, but
   it brings a different isolation, evidence, and conformance obligation. The
   later design must choose between OCH-only first and one deliberately bounded
   external-subject slice; this gate does not make that product decision.
2. **Every OCH subject must drive the real Application/Session path.** Its
   executor uses `composition.Open` → `CreateSession` → `RunTurn` (and the
   already-public `CompactSession` / recovery paths the scenario needs).
   No eval-only Engine or Provider shortcut. Charter §6.9 already
   required this; Pi independently confirms it.
3. **Eval orchestration lives outside `internal/harness/application`.**
   Maka's "owns experiment semantics; does not execute Maka or construct
   Runtime objects" is the placement rule. Application remains the
   authority for Session/Turn. The eval package is a consumer of
   `composition`, the way `cmd/och` is, not a second composition root
   and not a child of `contextengine`.
4. **Keep Subject, Executor, Attempt, Evidence, and Score separate.** The
   durable shape retains `Scenario → Subject → Attempt → Evidence → Score`
   (Maka's Experiment/Cell/Attempt/Result, Harbor's
   Task/Trial/Verifier, Inspect's Task/Sample/Score). Subject is a named
   frozen identity, not a Go alias of `composition.Config`. Whether the first
   slice has one OCH executor or an additional external executor follows the
   product-boundary choice above. Do not add an unused general `Agent` port;
   add an external-subject port only with a real first-slice consumer.
5. **Save evidence, then score independently.** Harbor regrade and
   Inspect `score(log)` are the same constraint: a recorded attempt can
   be re-scored without re-running the agent. Evidence is an artifact manifest,
   not a hard-coded pair of files. For OCH it can include the native
   `och.session.transcript`, workspace/verifier outputs, frozen subject/config
   identity, usage and timing, EventStore/audit or request-envelope evidence,
   and collection diagnostics. The transcript remains the trajectory surface;
   do not invent a second trajectory schema, but do not pretend it contains
   `model.request.recorded` or `policy.decision.recorded`, which it omits.
6. **Dual channel is mandatory and already has a repository precedent.**
   - **PR CI:** no network, no secrets, deterministic fixtures, the
     existing composition/fixture-provider path. Deterministic verifiers may
     gate ordinary PRs; model-based quality judges do not.
   - **Live:** explicit local or nightly invocation, real models,
     independent artifact directory, not an ordinary PR gate.
7. **Distinguish subject failure, infrastructure failure, and
   indeterminate outcomes.** Maka's status taxonomy and replaceable-
   attempt rule are the precedent. A timeout that still produced a
   verifier reward is not the same event as a missing Docker daemon.
   Retry appends a new attempt; it does not rewrite the last one.
8. **Hard infrastructure assertions and quality judges are different
   steps.** vitest-evals / Pi: invariants fail the run; quality scores
   are observations behind a configurable floor. Live evals may use a model
   judge; that judge is not the subject harness. Grok Build further requires
   bounded, sanitized evidence and strict structured verdicts; missing judge
   evidence must not silently become a passing score.
9. **The runner is suite-neutral; the first suites remain a design choice.**
   Context Engine summary quality is a disclosed GA blocker and therefore a
   strong candidate, not a research-proven first suite. The runner must also
   be able to host tool/workspace, recovery, Provider, ACP, and policy suites.
   Putting it inside `internal/harness/contextengine` would make those suites
   patches.
10. **Do not copy Harbor's cloud control plane by default.** Pi-style isolated
    fixture/workspace/session directories are sufficient for an in-process OCH
    executor. A chosen external subject may require a subprocess or container
    boundary, which the design must state explicitly; it still does not imply
    Daytona/Modal or a second cloud scheduler.
11. **OpenTelemetry is not part of this eval contract.** Milestone 10
    still contains it as a separate undesigned item. Domain events
    already implement the charter's Observable attribute. An OTel gate
    comes later.

## Rejected shapes

1. **Claiming general external-agent support without a real subject and
   isolation contract.** A bounded external-subject first slice remains a
   design option; speculative adapters, copied third-party schemas, or a
   Harbor-scale abstraction with no first consumer do not. If the design
   chooses OCH-only, it must record external subjects as deferred rather than
   pretending the research proved them undesirable.
2. **An eval-only execution shortcut** that talks to Engine, Provider, or
   Context Engine without a Session. Contradicts §6.9 and would make
   scenario scores incomparable to production.
3. **Treating Context Engine fixture tests as milestone 10.** Those
   tests prove mechanism. They do not prove summary quality, and they
   are not a scenario runner.
4. **OpenAI Evals or its hosted product as the session-runner blueprint.**
   Datasets, criteria, and trace graders are useful mechanisms; completion
   functions and a deprecating hosted control plane are the wrong authority
   for a local tool-using Session.
5. **Inferring architecture from Codex / Kimi benches or DeepSeek Harness
   `BENCHMARK.md`.** Those are confirmed absences. Grok Build is not placed in
   this negative: its online evaluator informs judge design, not runner design.
6. **Baking `application.Service` or domain event structs into the Score
   schema.** That closes the door on ACP-subprocess subjects (still OCH)
   and on any later executor. Score reads a versioned evidence manifest.
7. **Live model calls as an ordinary PR gate.** Conflicts with the
   existing keyless composition-root verification contract.
8. **Copying any reference type name, JSON schema, prompt, or status
   string verbatim** into this repository. Mechanisms only.

An ACP-subprocess subject that still launches this project's `och`
binary is **not** an external agent. It is a second OCH executor
(in-process composition versus the public protocol surface). First slice
may start with in-process only; the design must not define Subject so
narrowly that an ACP executor later has to fight the schema. Comparing
two OCH git versions on one frozen scenario is likewise still product
regression.

## Open questions a design must resolve

- **Product boundary.** Is v1 an OCH product-regression platform only, or does
  it include one deliberately bounded external Subject with a named consumer?
  The latter requires a concrete launch, isolation, evidence, and cancellation
  contract; an abstract adapter interface without such a consumer is not
  sufficient.
- **Package placement.** `internal/harness/eval` (new architecture-guard
  owner that may import `composition` but not adapters) versus a `cmd/`
  binary that stays outside the harness ownership table. Either way
  Application does not grow eval orchestration, and `contextengine`
  does not host it.
- **OCH executor surface.** In-process `composition.Open` only, or also
  an ACP-subprocess subject in the same slice? In-process is enough to
  prove the model; ACP is the public protocol and is the stronger
  product-regression claim.
- **First suites.** The runner must be suite-neutral. Context Engine
  rolling-summary quality is a strong candidate because it is a current GA
  blocker, but the design must explicitly choose the first suite or suites
  rather than bake Context concepts into the runner. A minimal tool/workspace
  scenario is a useful proof of neutrality; recovery, Provider, ACP, and policy
  remain candidates for later suites.
- **Scenario encoding.** Checked-in Go fixtures, a frozen JSON/JSONL
  experiment file (Maka), or a Harbor-like directory of
  `instruction.md` + tests. The format is OCH-owned in v1; Terminal-Bench
  YAML is not the native schema.
- **Subject identity.** What is hashed: git SHA, `composition.Config`
  digest, provider endpoint, Context percents, policy, sandbox flags,
  tool catalog? Machine-local paths must not enter the identity (Maka:
  they select artifacts, they do not alter experiment semantics).
- **Attempt layout and retention.** Where live artifacts live, what is
  gitignored, whether PR-CI fixture runs persist anything beyond the
  test process, and how append-only attempts are stored (files like
  Maka's `NNNNNN.json`, or SQLite).
- **Evidence manifest completeness.** Which artifacts are required versus
  optional, how hashes and collection diagnostics are recorded, and what an
  offline regrade may do when transcript, workspace/verifier output, frozen
  config identity, usage/timing, or EventStore/audit/request-envelope evidence
  is absent.
- **Scoring mix for v1 live.** Deterministic verifiers only (workspace
  tests, transcript invariants), a model judge, or both. If a judge
  exists, it is a separate harness (vitest-evals), consumes bounded and
  sanitized evidence (Grok Build), emits strict structured output, and treats
  missing or contradictory evidence conservatively.
- **Limits.** Per-attempt wall clock, token, step, and cost caps
  (Inspect). How they interact with Application's existing `Limits` and
  Context overflow caps.
- **Domain `EvaluationResult`.** Charter-named, unimplemented. Is it a
  domain event, an eval-package DTO, or a projection of attempt files?
  First slice should not force eval facts into the Session event log
  unless a Session query actually needs them.
- **Version-to-version comparison.** Maka freezes experiments and
  compares arms. Is "this SHA versus last night's SHA" a v1 reporter
  feature or a later suite?
- **Resource and privacy bounds** required by Documentation rule 4:
  max concurrent attempts, max artifact bytes, secret redaction of
  stored transcripts (the existing `redact.Text` path already runs
  before persistence — confirm eval export cannot bypass it), and a
  fail-closed refusal to start a live run without an explicit flag.

## Evidence limits

- Repository citations trace to the pinned commits in the comparison table,
  opened in this session; current OpenAI guidance was fetched from the two
  official Markdown pages named above. The 2026-09-01 roadmap gate's Evaluation
  section was treated as a lead, not as evidence.
- This gate does not authorize copying any type name, schema, prompt,
  status string, or configuration constant from any reference project.
- This gate does not audit any project's eval implementation for
  correctness, statistical validity, or security — only placement,
  mechanism, and precedent.
- Depth is uneven by disclosure: Pi `packages/evals` and Maka
  `packages/eval` were read at README plus the runner/subject/attempt/
  result files; Harbor was read at README, Task, Verifier, artifact
  manifest, regrade, and BaseAgent, not every installed adapter or
  environment backend; Inspect was read at `eval`/`eval_set`/`score`/
  Task/Sample/Scorer and `design/recover.md`, not the full control
  channel; vitest-evals at architecture, gate, and judge-harness; legacy
  OpenAI Evals at README, Recorder, and eval-templates; and Grok Build at
  the goal evaluator and classifier paths cited above. Current OpenAI Evals
  and trace-grading guidance was read from the official pages named in the
  comparison table. Codex, Kimi Code, and DeepSeek Harness were checked as
  negatives.
- Harbor's cloud environment backends (Daytona, Modal, GKE, …) were
  listed from the tree, not executed.
- Maka's egress proxy and Harbor's Docker/cloud paths show that an external
  Subject needs an explicit isolation and scheduling contract. They do not
  prove every bounded external adapter needs Harbor's platform; the contract
  may be satisfied by a smaller subprocess or local-container executor.
- "Current state" means 2026-09-01. A later gate that revisits these
  projects must re-fetch and re-read per Documentation rule 7.
- This gate does not choose a design. The next step is a normative
  evaluation design under `docs/superpowers/specs/`, informed by — not
  dictated by — the findings above. Product positioning and first-suite scope
  remain design decisions. OpenTelemetry stays a separate undesigned item.
