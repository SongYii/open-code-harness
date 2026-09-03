# Authoring Evaluation Scenarios, Subjects, and Executors

**Audience:** anyone adding or changing a checked-in evaluation fixture under `eval/`

**See also:** [Evaluation System — Implemented Contract](../architecture/evaluation.md) for the underlying mechanism this guide assumes; [Evaluation Operations](evaluation-operations.md) for running what you author here.

This guide is about authoring `eval/scenarios/**`, `eval/subjects/*.json`,
`eval/executors/*.json`, and `eval/sets/*.json` — not about the Go code that
executes them. It assumes you have read the contract document above.

## The document kinds you author

| You write | Where | What it means |
| --- | --- | --- |
| Scenario | `eval/scenarios/<id>/scenario.json` + `eval/scenarios/<id>/fixture/` | A scripted sequence of actions and the workspace content it starts from |
| Subject | `eval/subjects/<id>.json` | A Provider/Policy/Context configuration |
| Executor | `eval/executors/<id>.json` | `in_process` or `acp_subprocess` identity |
| EvalSet | `eval/sets/<id>.json` | A named Scenario × Subject × Executor product, plus limits/lane |
| JudgeConfig | `eval/judges/<id>.json` | A live quality judge's model/prompt/criteria identity — **live-lane sets only** |

A Scenario, Subject, or Executor's own `.json` file is never hand-edited
after computing its digest without also recomputing that digest — every
field you change moves the SHA-256 the EvalSet(s) that reference it must
also be updated to match, or `ExpandAttempts` refuses the whole EvalSet with
a stale-digest error. There is no tool in this repository that recomputes a
digest for you outside a Go test; the fastest way is a short throwaway test
in `internal/harness/eval` calling `DecodeScenario`/`ScenarioDigest` (or the
Subject/Executor equivalents) against your file and printing the result via
`t.Logf`, then deleting the test once you have copied the digest into every
EvalSet that references it.

## Writing a Scenario

A Scenario's `actions` array is ordered and immutable once frozen. The five
v1 action types:

- `prompt` — one user turn's text. If a later `cancel` action names this
  prompt's own `id` as its `targetActionId`, the executor runs it
  asynchronously so the loop can reach that cancel action without blocking;
  otherwise it runs synchronously and must complete before the next action.
- `compact` — `strategy: "summary"` or `"reset"`, optional `focus` text.
  Supported by both executors.
- `cancel` — `targetActionId` must name an earlier `prompt` action.
- `restart` — `mode: "clean_shutdown"` (both executors), `"interrupt"` or
  `"kill"` (`acp_subprocess` only — the in-process executor refuses both as
  `unsupported_restart_mode`, since it has no separate process to abruptly
  end). Tag a Scenario using `interrupt`/`kill` with
  `requiredCapabilities: ["prompt", "restart_interrupt"]` (or `restart_kill`)
  so it is never expanded against an Executor that cannot honor it.
- `collect` — exactly one of `workspacePath` (a file to stage as evidence
  from the Attempt's own workspace) or `verifierFact` (a marker consumed by
  a specific verifier, not a real file). Declaring `collect` does not by
  itself require anything — pair it with a `deterministicVerifierIds` entry
  that actually checks for the collected content, or the collection is
  inert.

`fixtureDigest` must exactly match `DigestFixtureTree`'s own SHA-256 of your
`fixture/` directory's real content. An empty fixture directory (just a
`.gitkeep` placeholder, the common case for a Scenario whose actions never
read a pre-existing file) always digests to the same known value,
`sha256:f5043b2c09fb8ecfc394548860794027a0f22602668cee36597f41b9f349cefd` —
copy it directly rather than recomputing it for yet another empty fixture.
The moment your Scenario needs a real starting file (`tool-read-success`'s
own `input.txt` is the checked-in example), you must recompute.

`approvalScript` entries are matched by `{promptActionId, ordinal}` — the
ordinal is the zero-based index of the Nth tool call *within that one prompt
action's own Turn*, not a global counter across the whole Scenario. An
undeclared, out-of-order, or tool-name-mismatched request is denied and
recorded as a violation, never silently allowed (design's own fail-closed
approval contract) — if your Scenario expects the model to call two
different tools in one Turn, you need two script entries at ordinals 0 and
1, in that exact order.

`requiredEvidenceRoles`/`optionalEvidenceRoles` name manifest roles
(`transcript`, `audit`, `workspace`) your Scenario's own verifiers actually
read — declaring `audit` required when nothing your verifiers check needs it
just makes an infra hiccup that skips audit collection look like a bigger
failure than it is. `deterministicVerifierIds` must name IDs already
registered in `internal/harness/eval/verifier.go`'s compiled-in catalog —
there is no way to supply a scenario-local verifier; a new check needs a new
Go function and a PR.

## Extending the shared fixture provider script

Every checked-in fixture-lane Scenario's prompt text is a **trigger
marker**, not literal content the model is expected to produce creatively —
`cmd/och-eval/fixture.go`'s `smartFixtureScript` inspects each request's
latest user message for one of its own known markers
(`toolApprovalTriggerMarker`, `readFileTriggerMarker`, and so on) and
answers with a fixed, deterministic tool call or plain response. If your new
Scenario needs a behavior none of the existing markers produce, add a new
`const ...Marker = "TRIGGER_..."` and a new `case bytes.Contains(body,
[]byte(yourMarker)):` branch to `smartFixtureScript` — **never chain two
markers in one Scenario's own conversation**: a later request in a
multi-turn Scenario still carries every earlier message (and so every
earlier marker) in its own request body, so the dispatcher would match
whichever marker's `case` comes first, not necessarily the one you intended
for that turn. One marker, one Scenario, exactly like every marker already
checked in.

The fixture server is real, running Go code answering real HTTP requests on
a real loopback socket — never static response data a Scenario document
itself carries. This matters for review: a reviewer can read
`smartFixtureScript` once and know exactly what every fixture-lane Scenario
in the repository can possibly cause a model to "say," rather than trusting
each Scenario's own claim about what its fixture does.

## Writing a Subject

Two Subjects intended to compare across executors for parity (an
`in_process` baseline and an `acp_subprocess` candidate) must differ in at
least one declared semantic field, or `ComparePairedArms` treats their
`SubjectDigest` values as reflecting the same identity and the pairing
mechanism (which requires a differing Subject digest as part of what makes
a valid pair) never engages the way you expect. `smoke-subject-acp.json`'s
own difference from `smoke-subject.json` is `policy.limits.approvalTimeout`
— chosen specifically because it never affects deterministic-fixture
behavior (a fixture's approval decisions are effectively instantaneous
regardless of the timeout value) while still being a real, meaningful
declared field, not an arbitrary cosmetic one.

`provider.credentialEnvVar` names an environment variable, never a secret
value — this field's own presence in a Subject document is precisely why a
credential value itself must never appear anywhere in one. For the fixture
lane, `provider.normalizedEndpoint` is always `fixture://<script-name>`,
resolved to a real loopback address only at runtime (never written back to
the checked-in file). For the live lane, it is a real `https://` endpoint —
see the live-lane section of the operations guide before ever pointing a
Subject at one.

## Writing an Executor

`kind: "in_process"` needs no further identity fields.
`kind: "acp_subprocess"` requires an `acpSubprocess` block
(`binarySha256`, `normalizedArgv`, `protocolVersion`, `agentName`,
`agentVersion`) — none of these fields are cross-checked against the
*actually launched* binary at runtime today (they are declarative identity
facts, matching how `ochRevision: "dev"` is also a placeholder rather than a
build-specific value), so a checked-in Executor document's own
`binarySha256` may legitimately be a fixed placeholder like this
repository's own checked-in `smoke-executor-acp.json` uses, not a
per-build-real hash you would need to keep updating.

## Composing an EvalSet

`EvalSet.ExpandCells()` is a flat Cartesian product of every listed
Scenario × Subject × Executor — **there is no per-Cell selective pairing
inside one EvalSet document.** If you need Scenario A to run only against
Subject/Executor pair 1 and Scenario B to run only against pair 2, listing
both pairs in one EvalSet produces every cross-combination too (A × pair 2,
B × pair 1), not just the two you wanted. Use separate, minimal-cardinality
EvalSet documents sharing one artifact root instead — see
`eval/sets/pr-parity-baseline.json`/`pr-parity-candidate.json` for the
checked-in example of exactly this pattern, and the contract document's own
[Matrix expansion](../architecture/evaluation.md#matrix-expansion) section
for why.

`limits.maxExpandedAttempts` defaults to 256 and caps at 4096
(`MaxMaxExpandedAttempts`) — an EvalSet expanding past either bound without
explicitly raising this field is refused before any Attempt is created.
`lane` must be `"fixture"` or `"live"` and must match every referenced
Subject's own `provider.lane` — mixing lanes in one EvalSet is refused by
`ExpandAttempts` (a live EvalSet must never silently reach a fixture-lane
double, and a fixture EvalSet must never silently reach a live credential).

`lane` also decides `judgeConfigDigest`: a **live** set must declare it and
a **fixture** set must not. A fixture set naming a judge configuration
would be claiming an identity the deterministic lane can never exercise; a
live set without one could publish a quality Score whose configuration no
reader could reconstruct from the Attempt's own evidence. Write the
JudgeConfig under `eval/judges/<id>.json`, compute its digest the same way
you compute any other (`eval.JudgeConfigDigest`), and pin it in the set —
`internal/docsguard`'s own `TestLiveJudgeExampleDigestsAndGuide` recomputes
the checked-in pair on every run, so a drifted digest fails the build
rather than surfacing at judge time. `eval/judges/context-quality-judge.example.json`
is the worked example. The runner verifies the digest during whole-set
validation, before any Attempt directory exists, and stages the exact
document into every live Attempt's evidence.

Adding a new Scenario/Subject/Executor/EvalSet to the **ordinary PR lane**
specifically (as opposed to the explicit/scheduled deterministic-full or
example live sets) changes what runs on every pull request in this
repository — keep that lane at exactly four Cells per design §23, and treat
adding a fifth as a change that needs its own explicit justification, not a
routine addition.
