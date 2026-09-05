# Evaluation Variance and Baseline Policy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close one of the Evaluation system's four GA blockers by making the spread of repeated Scores a published, auditable fact, and by defining when a quality signal is not trustworthy enough to be read as a result at all.

**Architecture:** `internal/harness/eval` owns the frozen policy document, the pure distribution computation, the baseline document, and the fail-closed rules. `cmd/och-eval` owns the report block and the baseline regeneration command. Nothing here calls a model, and no task requires a live credential — the mechanism must be provable before the numbers it carries can be earned.

**Tech Stack:** Go 1.26.6; existing `eval`, `domain` packages; standard library only. No new module dependency.

**Spec:** [`docs/superpowers/specs/2026-09-04-evaluation-variance-policy-design.md`](../specs/2026-09-04-evaluation-variance-policy-design.md)

## Global Constraints

- **No default thresholds, ever.** A policy document must declare its limits. No run against a live model has happened in this repository, so a shipped default would be a guess wearing the authority of a specification.
- **Provisional is a field, not a comment.** The accepted ordering ships an uncalibrated policy first, and the risk that a provisional number gets mistaken for a calibrated one is real. `Calibration` is part of the frozen document and reaches every Score derived under it.
- Repetitions are **never** collapsed into a verdict. There is no `cellVerdict` field, and adding one is out of scope. A named decision rule published *beside* the distribution is a later possibility (design §3.5); no reducer enum is built here, because no lane yet asks "did this Cell pass?".
- **This mechanism ships dormant, and the plan does not pretend otherwise.** All sixteen checked-in EvalSets declare `repetitionCount: 1`, and Task 4's own fail-closed rule refuses any set that references a policy at `N = 1`. No task in this plan adds a configuration that would reach the new code, and none should: the first configuration that references a variance policy is the first live quality EvalSet. The mutation tests prove the computation; they are not a consumer, and calling them one would be a relabelling.
- **The deterministic lane takes no variance policy** (design §3.6). A determinism check is a different mechanism in a different document, and `spread` could not be its signal in any case — `NumericScore` is set only on judge paths, so on a fixture lane the spread is `0` by construction.
- A Cell that fails `EvaluableEnough` may never be reported as a pass, in any lane, at any maturity level. `ExceedsDeclaredLimits` carries no such power while its limits are uncalibrated (design §3.2).
- Ordinary PR CI gates on deterministic verifiers only. No quality or variance signal ever gates a pull request.
- Every fail-closed rule is proven by a mutation that turns a test red, recorded in the evidence ledger with its observed result.
- Follow red-green-refactor. Each task is its own commit; tasks may share a PR where the branch already carries a predecessor.
- No task may leave `gofmt`, `go vet` on three platforms, `go test -race ./...`, `go mod tidy -diff`, or `internal/docsguard` failing.

## Sizing expectation

Pure computation over documents that already exist, plus two new frozen document types and a report block. **Order 800–1,200 lines of production Go.** A slice reaching for statistical machinery — confidence intervals, significance tests, power analysis — has left the design, which rejects them explicitly: at the sample sizes a nightly lane can afford, they would dress a small sample up as rigor.

---

### Task 1: The frozen `och.eval.variance-policy` document

**Files:**
- Create: `internal/harness/eval/variance_policy.go`, `variance_policy_test.go`
- Modify: `internal/harness/eval/model.go` (schema constant), `evalset.go` (reference field)

**Interfaces:** `DecodeVariancePolicy([]byte) (VariancePolicy, error)`, `VariancePolicyDigest(VariancePolicy) (Digest, error)`, and the `Calibration` enum (`uncalibrated`, `calibrated`).

Mirrors `judge_config.go` exactly: strict decode rejecting unknown and duplicate keys, a canonical digest over the document's own checked bytes, and no secret-bearing field.

- [ ] **Step 1: Write failing tests**

```go
func TestDecodeVariancePolicyRoundTripAndDigest(t *testing.T)
func TestVariancePolicyRequiresBothLimits(t *testing.T)
    // maxNumericSpread and minVerdictStability are both mandatory; a document
    // omitting either is refused rather than defaulted.
func TestVariancePolicyRefusesOutOfRangeLimits(t *testing.T)
    // stability outside [0,1]; a negative spread.
func TestVariancePolicyRequiresAnExplicitCalibrationState(t *testing.T)
    // "uncalibrated" must be stated, never inferred from absence — the whole
    // risk of the accepted ordering is a provisional number read as final.
func TestVariancePolicyRequiresMinEvaluableRepetitions(t *testing.T)
func TestVariancePolicyRejectsUnknownFields(t *testing.T)
```

- [ ] **Step 2: Implement** — the document, its validation, and its digest.
- [ ] **Step 3: Mutation check** — supply a default for a missing limit and confirm `TestVariancePolicyRequiresBothLimits` fails; restore.

---

### Task 2: Distribution, spread, and stability

**Files:**
- Create: `internal/harness/eval/variance.go`, `variance_test.go`

**Interfaces:** `ComputeCellDistribution(scores []Score, policy VariancePolicy) (CellDistribution, error)`.

Pure: no I/O, no store access. `CellDistribution` carries `Repetitions`, `Attempts`, `Verdicts` (a count per verdict, never a derived single verdict), `NumericScores` **in repetition-index order**, `NumericSpread`, `VerdictStability`, and — per design §3.1 and §3.2 — the two separate reliability fields `EvaluableEnough` and `ExceedsDeclaredLimits`, each with its own reason. There is no single `Trustworthy` boolean: a consumer branches on a boolean and never reads the reason beside it, so one flag would merge a structural fact with an uncalibrated threshold judgement.

- [ ] **Step 1: Write failing tests**

```go
func TestDistributionPublishesEveryScoreInRepetitionOrder(t *testing.T)
    // The sequence, not a summary of it: a reviewer must be able to see
    // 0.71 0.74 0.69 0.73 0.41 rather than a median.
func TestDistributionNeverDerivesASingleVerdict(t *testing.T)
    // The type must not grow a cellVerdict field; 4-pass/1-fail and 5-pass
    // are different facts and stay different.
func TestNumericSpreadIsTheRangeNotAStandardDeviation(t *testing.T)
func TestVerdictStabilityIsTheModalShareOfEvaluableRepetitions(t *testing.T)
func TestALimitIsAMaximumSoEqualityDoesNotBreach(t *testing.T)
func TestSpreadBreachSetsExceedsDeclaredLimitsNotAFailingVerdict(t *testing.T)
    // A limit breach is a statement about the measurement, never a third
    // verdict about the Subject.
func TestAnUncalibratedLimitBreachDoesNotBlockReportingACellAsAPass(t *testing.T)
    // Design §3.2: while the policy is uncalibrated, ExceedsDeclaredLimits
    // is advisory. Only EvaluableEnough hard-blocks a pass.
func TestTheTwoReliabilityFieldsAreNeverMergedIntoOne(t *testing.T)
    // The type must not grow a Trustworthy bool that ANDs them together.
```

- [ ] **Step 2: Implement.** Range, not standard deviation — at N in single digits a standard deviation has no useful sampling behavior, and range is what a reviewer can check by eye against the published sequence.
- [ ] **Step 3: Mutation check** — swap range for standard deviation and confirm the arithmetic test fails; restore.

---

### Task 3: Indeterminate handling, raw and filtered views

**Files:**
- Modify: `internal/harness/eval/variance.go`, `variance_test.go`

`indeterminate` is an infrastructure or judgeability signal, not a quality one: a judge that could not be reached, refused malformed output, or was denied evidence has said nothing about the Subject.

- [ ] **Step 1: Write failing tests**

```go
func TestIndeterminateRepetitionsAreCountedAndNamedIndividually(t *testing.T)
func TestIndeterminateIsExcludedFromEvaluableCount(t *testing.T)
    // ... and therefore from spread and stability.
func TestEveryDistributionCarriesBothRawAndFilteredDenominators(t *testing.T)
    // Fails if either is absent. This extends the parent design's existing
    // rule for infra failures rather than inventing a second convention.
func TestTooFewEvaluableRepetitionsFailsEvaluableEnoughNotTheLimitsField(t *testing.T)
    // "mostly unjudgeable" and "judged inconsistently" are different
    // problems with different warrants, so design §3 gives them separate
    // fields rather than separate reason strings on one shared label.
```

- [ ] **Step 2: Implement.**
- [ ] **Step 3: Mutation check** — count indeterminate as a failure and confirm the exclusion test fails; restore.

---

### Task 4: Fail-closed rules

**Files:**
- Modify: `internal/harness/eval/evalset.go`, `variance.go`, and their tests

- [ ] **Step 1: Write failing tests**

```go
func TestEvalSetReferencingAPolicyWithOneRepetitionIsRefusedBeforeAnyWork(t *testing.T)
    // The most dangerous possible output is spread=0 from a single sample:
    // perfect stability, measured never. Refused at load, before an Attempt
    // root exists.
func TestAPolicyDigestMismatchIsRefusedRatherThanFallingBackToNoPolicy(t *testing.T)
func TestScoresDisagreeingOnAnyIdentityDigestAreAHardError(t *testing.T)
    // Two Attempts that differ in Scenario, Subject, Executor, fixture,
    // limits, or pairing seed are not repetitions of the same thing and are
    // never pooled.
```

- [ ] **Step 2: Implement.**
- [ ] **Step 3: Mutation check** — allow `repetitionCount: 1` with a policy and confirm the refusal test fails; restore. This is the plan's single most important mutation.

---

### Task 5: The pinned `och.eval.baseline` document

**Files:**
- Create: `internal/harness/eval/baseline.go`, `baseline_test.go`

**Interfaces:** `DecodeBaseline`, `BaselineDigest`, `MatchBaseline(baseline, distribution) (BaselineComparison, error)`, `BuildBaseline(attempts, scores) (Baseline, error)`.

- [ ] **Step 1: Write failing tests**

```go
func TestBaselineComparesOnlyToMatchingIdentityDigests(t *testing.T)
func TestABaselineMismatchIsReportedNotSilentlyTreatedAsAbsentOrPassing(t *testing.T)
    // The usual cause is that the Scenario or Subject was edited, which is
    // exactly the fact a reviewer needs.
func TestBaselineRegenerationIsDeterministic(t *testing.T)
    // The same document twice from the same artifacts. A baseline nobody can
    // reproduce is an assertion, not evidence.
func TestBaselineRecordsTheAttemptIdsItWasDerivedFrom(t *testing.T)
func TestAStaleBaselineIsDisclosedAndStillShown(t *testing.T)
func TestBuildBaselineIsNeverCalledFromARunPath(t *testing.T)
    // A lane that rewrites its own baseline when it drifts measures nothing;
    // regeneration is an explicit command and a reviewed commit.
```

- [ ] **Step 2: Implement.**
- [ ] **Step 3: Mutation check** — let a run path write a baseline back and confirm the last test fails; restore.

---

### Task 6: Within-run paired-arm delta

**Files:**
- Modify: `internal/harness/eval/parity.go` or a new `variance_pairing.go`, plus tests

The existing pairing rule is unchanged. What is new is that the delta is computed **between distributions, not between single Scores**.

- [ ] **Step 1: Write failing tests**

```go
func TestPairedDeltaIsComputedBetweenDistributionsNotSingleScores(t *testing.T)
func TestADeltaSmallerThanTheWiderArmsSpreadIsPublishedAsWithinNoise(t *testing.T)
    // "This run cannot distinguish the arms" is a different claim from "no
    // difference exists", and only the first is honest here.
func TestWithinNoiseUsesTheWiderArmNotAnAverage(t *testing.T)
func TestPairingItselfIsUnchanged(t *testing.T)
```

- [ ] **Step 2: Implement.**
- [ ] **Step 3: Mutation check** — compare against the narrower arm's spread and confirm the wider-arm test fails; restore.

---

### Task 7: Report block and the regeneration command

**Files:**
- Modify: `cmd/och-eval/report.go`, `main.go`, and their tests

- [ ] **Step 1: Write failing tests**

```go
func TestReportPublishesThePerCellDistributionBlock(t *testing.T)
func TestReportNeverGatesAPullRequestOnAVarianceSignal(t *testing.T)
func TestReportMarksAnUncalibratedPolicyOnEveryCellItGoverns(t *testing.T)
    // The accepted ordering's own risk, closed at the point a reader sees a
    // number.
func TestReportPublishesDerivedReliabilityReadings(t *testing.T)
    // Design §3.4: c/n, at-least-one-passed, and all-passed. Arithmetic on
    // counts the block already carries, needing no calibrated threshold —
    // which is why they are the one part of this that is trustworthy today.
func TestDerivedReadingsAreNotNamedPassAtK(t *testing.T)
    // A Cell-level "at least one of k passed" is at_least(1). Chen's pass@k
    // is a dataset-level estimator; borrowing the name would make the first
    // public comparison dishonest.
func TestReportCarriesBothReliabilityFieldsSeparately(t *testing.T)
    // Fails if the block renders one merged verdict about reliability.
func TestRegenerateBaselineWritesADeterministicDocument(t *testing.T)
```

- [ ] **Step 2: Implement** — `och-eval report` gains the block; `och-eval baseline` regenerates. No reducer enum: design §3.5 defers named decision rules until a consumer exists.
- [ ] **Step 3: Mutation check** — drop the uncalibrated marker and confirm the marking test fails; restore.

---

### Task 8: Contract, evidence ledger, and documentation sync

**Files:**
- Modify: `docs/architecture/evaluation.md` and its Chinese reading copy, `docs/architecture/evaluation-evidence.md`, `docs/README.md`, root `README.md`

- [ ] **Step 1** — extend the Evaluation contract with the variance and baseline rules, in both languages.
- [ ] **Step 2** — record commits, verification output, every mutation and its observed result, and any design clause the implementation had to correct.
- [ ] **Step 3** — update the four GA-blocker lists (`README.md`, `docs/README.md`, `docs/architecture/evaluation.md`, `docs/architecture/evaluation-evidence.md`) **together**. Each names "variance policy" as outstanding; leaving one behind would reproduce exactly the drift this repository's executable documentation rules exist to prevent.
- [ ] **Step 4: Verify** — `internal/docsguard`, full race suite, three-platform vet, cross-builds.

---

## What this plan does not close

Three of the Evaluation system's four GA blockers remain untouched, and this
work must not be read as reducing them:

- **Real-model sample size.** No run against a live model has happened in
  this repository. The mechanism will be provable; the numbers it carries
  will not be earned until one has.
- **Judge meta-evaluation breadth.** Eight adversarial fixtures today.
- **Provider breadth.** One OpenAI-compatible adapter.

## Open questions this plan does not settle

1. **A nightly repetition budget** (design open question 2). It does not bind
   while no checked-in live EvalSet exceeds `repetitionCount: 1`, so the plan
   proceeds without it — but the first live set that raises N will need an
   answer, because N multiplies both cost and wall time.
2. **Whether the deterministic lane gets a variance policy at all** (design
   open question 3). Left out, following the design's own reasoning. A
   fixture lane's `spread > 0` is a determinism defect rather than noise and
   deserves its own mechanism; folding it in here would blur the distinction
   the design rests on.
