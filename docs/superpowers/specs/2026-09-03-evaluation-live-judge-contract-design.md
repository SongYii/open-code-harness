# Live Judge Configuration and Evidence Binding Design

**Status:** Accepted on 2026-09-03

**Scope:** Complete Milestone 10 Task 17's deferred live-judge invocation path without weakening offline regrade, evidence immutability, or credential isolation.

## Problem

The repository has a tested evidence-only judge mechanism, but it cannot yet be invoked safely from `och-eval`. The existing `JudgeConfig` is an in-memory value, `EvalSet.JudgeConfigDigest` is not resolved against a concrete document, and an Attempt's evidence does not carry the EvalSet or judge configuration needed to verify that relationship offline.

Adding endpoint/model flags directly to the CLI would produce a Score whose claimed configuration could not be proven from the Attempt's immutable evidence. It would also leave deterministic prerequisites, credential-read ordering, evidence-bundle omissions, and cost availability underspecified.

## Goals

- Define one secret-free, digestible `och.eval.judge-config` document.
- Bind a live Attempt's frozen EvalSet and JudgeConfig into its Evidence Manifest.
- Verify `EvalSet.JudgeConfigDigest` before any judge credential is read.
- Invoke the existing OpenAI-compatible adapter through a real streaming call.
- Run deterministic Scenario verifiers before quality judging.
- Append one immutable live judge Score without rerunning the Subject.
- Make omitted evidence, provider failure, cost availability, and CLI exit behavior explicit.
- Keep existing fixture Attempts and deterministic offline regrade compatible.

## Non-goals

- Supporting arbitrary external agents or benchmark adapters.
- Adding another model-provider protocol.
- Making live quality results an ordinary PR gate.
- Uploading artifacts or evidence automatically.
- Implementing the deferred Context mechanism suite or MCP suite.
- Guaranteeing deterministic model output; the configuration and evidence are reproducible, while live output variance remains measured data.

## Chosen architecture

The implementation uses an evidence-anchored configuration document. A live EvalSet names the SHA-256 digest of a checked-in JudgeConfig. At run time the runner validates that digest and stages exact canonical copies of both EvalSet and JudgeConfig into the Attempt's evidence. The manifest hashes those files, and every Score references the manifest digest.

Attempt v1 is not changed. Its existing `EvalSetID`, Scenario/Subject/Executor digests, the staged EvalSet, and the manifest form the binding chain. The trusted collector is the authority that stages the same validated `RunnerInputs.Set` used to create the Attempt; post-publication substitution is detected by the manifest and ArtifactReader.

The CLI gains a separate `judge` command rather than overloading deterministic `regrade`:

```text
och-eval judge -attempt PATH -config PATH --live [-price-table PATH]
```

There are no endpoint, model, prompt, or credential-value override flags. Those would bypass frozen identity.

## JudgeConfig document

The new schema is `och.eval.judge-config`, format version 1:

```json
{
  "formatVersion": 1,
  "schema": "och.eval.judge-config",
  "id": "context-quality-judge",
  "version": "v1",
  "provider": {
    "adapterKind": "openaicompat",
    "normalizedEndpoint": "https://api.example.com/v1",
    "modelId": "model-id",
    "credentialEnvVar": "OCH_EVAL_LIVE_JUDGE_API_KEY",
    "contextWindow": 128000,
    "maxOutput": 4096,
    "includeUsage": true,
    "maxTokensField": "max_completion_tokens"
  },
  "prompt": {
    "id": "och_quality_judge_v1",
    "digest": "sha256:df5252340da77e38b58c0e5f1faf6048e75c73f46ec90709d7f11e0b19acc567"
  },
  "criteria": [
    {
      "id": "constraint-preservation",
      "rubric": "Determine whether all explicit user constraints remained enforced after compaction.",
      "evidenceRoles": ["scenario", "transcript", "audit", "workspace"]
    }
  ]
}
```

Validation rules:

- `id`, `version`, model ID, prompt ID, rubric, and every criterion ID are non-empty UTF-8 text.
- The only initial adapter kind is `openaicompat`.
- The production CLI accepts only HTTPS endpoints without userinfo, query, or fragment.
- `credentialEnvVar` is a valid environment-variable name; no credential value is serializable.
- Context window and output limit are positive, and output does not exceed context window.
- `includeUsage` must be true for a live judge.
- `maxTokensField` is empty, `max_tokens`, or `max_completion_tokens`.
- Prompt ID is exactly `och_quality_judge_v1`; its digest equals the embedded prompt bytes.
- Criterion IDs are unique. Evidence roles are non-empty and unique within each criterion.
- Each criterion has a bounded rubric and at least one evidence role.
- `priceTableDigest` is optional. When present, `-price-table` is required and its canonical digest must match.

`JudgeConfigDigest` validates the document and computes SHA-256 over its canonical JSON representation.

## EvalSet and evidence binding

Lane rules become:

- A fixture EvalSet must not declare `JudgeConfigDigest`.
- A live EvalSet must declare `JudgeConfigDigest`.
- A fixture Subject belongs only to a fixture EvalSet; a live Subject belongs only to a live EvalSet.

`RunnerInputs` receives the resolved JudgeConfig for a live set. Whole-set validation compares its digest with `EvalSet.JudgeConfigDigest` before creating any Attempt.

`EvidenceDocuments` stages:

- `eval-set.json`, role `eval_set`, required for every new Attempt;
- `judge-config.json`, role `judge_config`, required only for a live Attempt;
- the existing Scenario, Subject, Executor, and Attempt documents.

Before staging, collection verifies:

- staged EvalSet ID equals `Attempt.EvalSetID`;
- EvalSet references contain the Attempt's exact Scenario, Subject, and Executor IDs and digests;
- live lane and Subject provider lane agree;
- the staged JudgeConfig digest equals `EvalSet.JudgeConfigDigest`.

Existing manifests remain readable. Deterministic `regrade` does not require the new roles. `judge` refuses legacy Attempts that lack either frozen live document and does not call a provider or publish a misleading Score.

## Judge execution flow

`EvaluateJudgeAttempt` owns the sequence:

1. Open the committed Outcome and Evidence Manifest through `ArtifactReader`.
2. Read and validate the frozen Scenario, Subject, Attempt, EvalSet, and JudgeConfig evidence.
3. Verify the operator-supplied JudgeConfig bytes have the same canonical digest as the frozen evidence and `EvalSet.JudgeConfigDigest`.
4. Enforce live consent using EvalSet lane, `--live`, and `OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND`.
5. Run every `Scenario.DeterministicVerifierIDs` verifier against committed evidence.
6. If any verifier fails or is indeterminate, do not construct or call the provider. Append an Indeterminate judge Score whose rationale names the prerequisite results. Return gate-failure exit class for a deterministic Fail and indeterminate exit class otherwise.
7. Construct the OpenAI-compatible model using `EnvAPIKey`. Credential lookup occurs inside `Stream`, after every preceding check.
8. Send two messages: the embedded frozen prompt as `system`, and the trusted criteria plus bounded evidence bundle as `user`. Set request purpose to a new `quality_judge` attribution value.
9. Consume the stream to completion, collecting text, token usage, duration, and provider request diagnostics without publishing credential-bearing data.
10. Strictly decode and validate the response through `RunJudge`.
11. Build and atomically append a `LaneLive` Score.

The Score uses JudgeConfig `id` and `version`, records its canonical config digest, and references the current manifest and Outcome digests. Judge usage never mutates or contributes to Subject Outcome usage.

## Evidence bundle rules

Evidence selection must be deterministic before applying byte limits:

1. Union the roles declared by all criteria.
2. Collect matching manifest entries.
3. Sort by normalized manifest path.
4. Read, redact, and bound entries in that order.

Each included entry label records its manifest path, original byte length, excerpt byte length, and whether the excerpt was truncated. Subject-authored bytes remain inside the untrusted evidence block. Criteria IDs and rubrics are rendered from trusted JudgeConfig outside that block.

If a selected role has no collected entry, an entry is missing/rejected, or the total bundle limit would omit an entire selected file, `RunJudge` returns Indeterminate without calling the model. Per-entry truncation is permitted because the contract intentionally supplies bounded excerpts, but it must be explicit in the request.

Evidence references in the response may name only paths actually included. Empty, duplicate, or nonexistent references are rejected. Missing or contradictory evidence always overrides a claimed Pass or Fail to Indeterminate.

## Cost representation

The existing integer-microunit price calculation remains authoritative, but a Score must distinguish unavailable pricing from a computed zero. `ScorerUsage` gains explicit cost metadata:

```json
{
  "costStatus": "unavailable" | "computed",
  "costMicrounits": 0,
  "costCurrency": "USD"
}
```

For `computed`, currency is non-empty and the Score's config chain identifies the matching price-table digest. For `unavailable`, currency is empty and microunits is zero. Legacy Scores without `costStatus` remain readable; newly published live judge Scores must set it.

## CLI output and exit classes

`och-eval judge` prints exactly one `och.eval.score` JSON document on stdout when it publishes a Score. Diagnostics go to stderr.

- `0`: judge completed, including advisory quality Pass or Fail.
- `1`: internal encoding/publication defect.
- `2`: flags, JudgeConfig, price table, lane, frozen binding, or legacy Attempt validation failure before scoring.
- `3`: deterministic prerequisite verifier failed; an Indeterminate judge Score is still published.
- `4`: reserved for evaluator infrastructure failures outside the model call.
- `5`: deterministic prerequisite was indeterminate, model call failed, judge output was invalid, or evidence was insufficient; an Indeterminate Score is published whenever the committed evidence remains safe to reference.

Quality Fail never gates ordinary PR CI. The command is unavailable for fixture-lane Attempts.

## Security and lifecycle

- Consent and all frozen-document checks occur before credential lookup.
- Judge configuration contains only the credential environment-variable name.
- The adapter never logs the credential and follows existing redirect and HTTPS policies.
- Evidence is read only through ArtifactReader; no live database, workspace, or unrestricted path is exposed.
- The Subject is never reopened or rerun during judging.
- Scores are append-only and never replace deterministic or earlier live Scores.
- Evidence is never uploaded except as the explicit bounded request to the configured judge endpoint.

## Testing and acceptance

The implementation is accepted only with tests proving:

- strict JudgeConfig decode, validation, canonical digest, and secret-free serialization;
- EvalSet lane/digest rules and whole-set refusal before Attempt creation;
- staged EvalSet/JudgeConfig identity cross-checks and tamper rejection;
- legacy deterministic regrade remains functional while legacy live judge is refused;
- consent failure and digest failure occur before a credential-access probe;
- deterministic Fail/Indeterminate prevents any provider call and publishes an Indeterminate judge Score with the correct exit class;
- a real fixture SSE response flows through the existing OpenAI-compatible adapter into an appended Score;
- model failure, malformed output, criteria mismatch, missing evidence, and publication failure retain their declared classifications;
- evidence selection is stable across repeated runs and total-limit omission prevents a model call;
- cost status distinguishes unavailable from computed zero;
- quality Fail exits zero and remains advisory;
- full Go tests, race tests, vet, CGO-disabled build, and Windows evaluation build pass.

The live example EvalSet, scenario, subject, JudgeConfig, operator guide, architecture docs, Chinese reading copy, and evidence ledger must be updated together. No real credential or live model result is required in ordinary CI.
