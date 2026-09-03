# Live Judge Configuration and Evidence Binding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Milestone 10 Task 17 with a frozen, evidence-bound JudgeConfig, consent-gated real OpenAI-compatible invocation, deterministic prerequisites, and append-only live Scores.

**Architecture:** `internal/harness/eval` owns the configuration document, evidence binding, prerequisites, bundle construction, and Score publication. `cmd/och-eval` owns flags/files, the concrete OpenAI-compatible caller, machine output, and exit mapping. New live Attempts stage EvalSet and JudgeConfig evidence; legacy Attempts remain deterministically regradable but cannot be live-judged.

**Tech Stack:** Go 1.26.6; existing eval, engine, domain, and openaicompat packages; standard library beyond existing dependencies.

**Spec:** `docs/superpowers/specs/2026-09-03-evaluation-live-judge-contract-design.md`

## Global Constraints

- Keep Attempt v1 unchanged and never rerun/reopen the Subject while judging.
- Serialize only credential environment-variable names, never values.
- Validate consent and frozen evidence before credential lookup.
- Fixture EvalSets forbid JudgeConfig; live EvalSets require it.
- Deterministic Fail/Indeterminate prevents the provider call.
- Live quality Fail is advisory and exits zero.
- Sort selected evidence paths before applying byte limits.
- Preserve append-only Score publication and legacy deterministic regrade.
- Follow red-green-refactor for every production change.

---

### Task 1: Frozen JudgeConfig and explicit cost status

**Files:**
- Create: `internal/harness/eval/judge_config.go`, `judge_config_test.go`
- Modify: `internal/harness/eval/model.go`, `judge.go`, `score.go`, `score_test.go`, `price.go`, `price_test.go`

**Interfaces:** Produces `DecodeJudgeConfig([]byte)`, `JudgeConfigDigest(JudgeConfig)`, `JudgeProvider`, `JudgePrompt`, rubric-bearing `JudgeCriterion`, and `CostStatusUnavailable`/`CostStatusComputed`.

- [x] **Step 1: Write failing tests**

```go
func TestDecodeJudgeConfigRoundTripAndDigest(t *testing.T) {
	want := validJudgeConfig()
	got, err := DecodeJudgeConfig(marshal(t, want))
	if err != nil || !reflect.DeepEqual(got, want) { t.Fatalf("got=%+v err=%v", got, err) }
	a, _ := JudgeConfigDigest(want)
	b, _ := JudgeConfigDigest(got)
	if a != b { t.Fatalf("digest %q != %q", a, b) }
}

func TestScorerUsageDistinguishesUnavailableFromComputedZero(t *testing.T) {
	for _, usage := range []ScorerUsage{
		{CostStatus: CostStatusUnavailable},
		{CostStatus: CostStatusComputed, CostCurrency: "USD", CostMicrounits: 0},
	} {
		score := validScore(t, mustAttemptID(t)); score.ScorerUsage = &usage
		if err := score.Validate(); err != nil { t.Fatal(err) }
	}
}
```

- [x] **Step 2: Verify RED**

Run: `go test ./internal/harness/eval -run 'JudgeConfig|ScorerUsage|Price' -count=1`

Expected: FAIL because the document API and cost-status fields do not exist.

- [x] **Step 3: Implement the exact spec schema and validation**

```go
const SchemaJudgeConfig = "och.eval.judge-config"
type JudgeConfig struct {
	FormatVersion int `json:"formatVersion"`; Schema string `json:"schema"`
	ID string `json:"id"`; Version string `json:"version"`
	Provider JudgeProvider `json:"provider"`; Prompt JudgePrompt `json:"prompt"`
	Criteria []JudgeCriterion `json:"criteria"`
	PriceTableDigest Digest `json:"priceTableDigest,omitempty"`
}
type CostStatus string
const ( CostStatusUnavailable CostStatus = "unavailable"; CostStatusComputed CostStatus = "computed" )
```

Validate all spec fields, a 4096-byte rubric cap, unique IDs/roles, exact embedded prompt digest, and cost-field combinations. Legacy empty cost status remains readable; Task 4 requires a status for newly published judge Scores.

- [x] **Step 4: Verify GREEN**

Run: `go test ./internal/harness/eval -run 'JudgeConfig|Judge|ScorerUsage|Score|Price' -count=1`

- [x] **Step 5: Commit**

```bash
git add internal/harness/eval
git commit -m "feat(eval): freeze live judge configuration"
```

### Task 2: EvalSet lane rules and frozen evidence binding

**Files:**
- Modify: `internal/harness/eval/evalset.go`, `evalset_test.go`, `runner.go`, `runner_test.go`
- Modify: `internal/harness/eval/evidence.go`, `evidence_test.go`, `evidence_identity.go`, `evidence_identity_test.go`, `benchmark_test.go`

**Interfaces:** Adds `RunnerInputs.JudgeConfig *JudgeConfig`, `EvidenceDocuments.EvalSet EvalSet`, `EvidenceDocuments.JudgeConfig *JudgeConfig`, and `readJudgeEvidenceDocuments` while retaining the legacy reader.

- [x] **Step 1: Write failing tests**

```go
func TestEvalSetJudgeDigestMatchesLane(t *testing.T) {
	fixture := validEvalSet(t); fixture.JudgeConfigDigest = mustDigest(t, 41)
	if fixture.Validate() == nil { t.Fatal("fixture accepted judge config") }
	live := validEvalSet(t); live.Lane = LaneLive
	if live.Validate() == nil { t.Fatal("live accepted no judge config") }
}

func TestRunEvalSetRejectsJudgeDigestBeforeAttemptCreation(t *testing.T) {
	inputs := validRunnerInputs(t); inputs.Set.Lane = LaneLive
	config := validJudgeConfig(); inputs.JudgeConfig = &config
	inputs.Set.JudgeConfigDigest = mustDigest(t, 42)
	if _, err := RunEvalSet(context.Background(), inputs); err == nil { t.Fatal("accepted mismatch") }
	assertArtifactRootEmpty(t, inputs.ArtifactRootOverride)
}
```

- [x] **Step 2: Verify RED**

Run: `go test ./internal/harness/eval -run 'EvalSetJudge|JudgeDigest|LiveEvidence|Legacy.*Regrade' -count=1`

- [x] **Step 3: Implement binding**

Stage required `eval-set.json`/role `eval_set` on every new Attempt and `judge-config.json`/role `judge_config` only on live Attempts. Verify set ID, reference IDs/digests, lane parity, and JudgeConfig digest before Attempt creation and before staging. Keep deterministic regrade compatible with old four-document evidence; require new roles only in `readJudgeEvidenceDocuments`.

- [x] **Step 4: Verify GREEN**

Run: `go test ./internal/harness/eval -run 'EvalSet|Runner|Evidence|Regrade' -count=1`

- [x] **Step 5: Commit**

```bash
git add internal/harness/eval
git commit -m "feat(eval): bind judge config into attempt evidence"
```

### Task 3: Deterministic and conservative evidence bundles

**Files:** Modify `internal/harness/eval/judge.go`, `judge_test.go`.

**Interfaces:** Introduces internal `judgeEvidenceBundle{Text, AvailablePaths, MissingPaths}`; `RunJudge` skips `JudgeCaller` when MissingPaths is non-empty.

- [x] **Step 1: Write failing tests**

```go
func TestJudgeBundleIsStableBeforeLimits(t *testing.T) {
	reader := judgeReaderWithEntriesInManifestOrder(t, "z.txt", "a.txt", "m.txt")
	first, _ := buildJudgeEvidenceBundle(reader, testJudgeConfig())
	for i := 0; i < 50; i++ { next, _ := buildJudgeEvidenceBundle(reader, testJudgeConfig()); if next.Text != first.Text { t.Fatalf("changed at %d", i) } }
}

func TestRunJudgeSkipsModelWhenSelectedEvidenceIsOmitted(t *testing.T) {
	called := false
	outcome, err := RunJudge(context.Background(), judgeReaderExceedingTotalLimit(t), testJudgeConfig(), func(context.Context, string, string) (string, ScorerUsage, error) { called = true; return "", ScorerUsage{}, nil })
	if err != nil || called || outcome.Verdict != ScoreIndeterminate || len(outcome.MissingEvidence) == 0 { t.Fatalf("called=%v outcome=%+v err=%v", called, outcome, err) }
}
```

- [x] **Step 2: Verify RED**

Run: `go test ./internal/harness/eval -run 'JudgeBundleIsStable|SelectedEvidenceIsOmitted' -count=1`

- [x] **Step 3: Implement sorted selection**

Gather matching manifest entries, normalize/deduplicate, sort by path, then read/redact/bound. Record non-collected and total-limit-omitted paths as missing. Labels include original bytes, excerpt bytes, and truncation. Reject empty/duplicate response references.

- [x] **Step 4: Verify GREEN twice**

Run: `go test ./internal/harness/eval -run 'Judge|EvidenceBundle' -count=2`

- [x] **Step 5: Commit**

```bash
git add internal/harness/eval/judge.go internal/harness/eval/judge_test.go
git commit -m "fix(eval): make judge evidence selection deterministic"
```

### Task 4: Prerequisite-gated judge orchestration and Score publication

**Files:**
- Create: `internal/harness/eval/judge_attempt.go`, `judge_attempt_test.go`
- Modify: `internal/harness/engine/model.go`, `internal/harness/adapters/openaicompat/purpose_test.go`

**Interfaces:** Adds `ModelRequestPurposeQualityJudge`; produces `EvaluateJudgeAttempt(ctx context.Context, directories AttemptRootDirectories, suppliedConfig JudgeConfig, consent LiveConsent, caller JudgeCaller, priceTable *PriceTable) (JudgeAttemptResult, error)`.

- [x] **Step 1: Write failing tests**

```go
func TestEvaluateJudgeAttemptStopsBeforeCallerOnPrerequisiteFailure(t *testing.T) {
	dirs, config := collectedLiveJudgeAttempt(t, ScoreFail); called := false
	result, err := EvaluateJudgeAttempt(context.Background(), dirs, config, LiveConsent{Flag:true, Environment:LiveConfirmValue}, func(context.Context,string,string)(string,ScorerUsage,error){ called=true; return "",ScorerUsage{},nil }, nil)
	if err != nil || called || result.PrerequisiteVerdict != ScoreFail || result.Score.Verdict != ScoreIndeterminate { t.Fatalf("called=%v result=%+v err=%v", called,result,err) }
}

func TestEvaluateJudgeAttemptChecksConsentBeforeCaller(t *testing.T) {
	dirs, config := collectedLiveJudgeAttempt(t, ScorePass); called := false
	_, err := EvaluateJudgeAttempt(context.Background(), dirs, config, LiveConsent{}, func(context.Context,string,string)(string,ScorerUsage,error){ called=true; return "",ScorerUsage{},nil }, nil)
	if err == nil || called { t.Fatalf("called=%v err=%v", called,err) }
}
```

- [x] **Step 2: Verify RED**

Run: `go test ./internal/harness/eval ./internal/harness/adapters/openaicompat -run 'EvaluateJudgeAttempt|QualityJudgePurpose' -count=1`

- [x] **Step 3: Implement orchestration**

```go
type LiveConsent struct { Flag bool; Environment string }
type JudgeAttemptResult struct { Score Score; PrerequisiteVerdict ScoreVerdict }
```

Validate frozen documents/config/consent, run every Scenario verifier, skip caller on non-Pass, otherwise invoke RunJudge. Build a LaneLive Score with config/manifest/outcome digests and explicit cost status, then append through PublishScore.

- [x] **Step 4: Verify GREEN**

Run: `go test ./internal/harness/eval/... ./internal/harness/adapters/openaicompat/... -run 'JudgeAttempt|Judge|QualityJudgePurpose' -count=1`

- [x] **Step 5: Commit**

```bash
git add internal/harness/eval/judge_attempt.go internal/harness/eval/judge_attempt_test.go internal/harness/engine/model.go internal/harness/adapters/openaicompat/purpose_test.go
git commit -m "feat(eval): publish prerequisite-gated live judge scores"
```

### Task 5: Real OpenAI-compatible caller and `och-eval judge`

**Files:**
- Create: `cmd/och-eval/judge.go`, `judge_test.go`
- Modify: `cmd/och-eval/main.go`, `main_test.go`, `loader.go`, `loader_test.go`, `exit.go`

**Interfaces:** Adds `judgeCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int`; adds `newOpenAICompatibleJudgeCaller(config eval.JudgeConfig, client *http.Client, allowInsecureLoopback bool) (eval.JudgeCaller, error)` where production passes `nil,false` and tests use an `httptest` client with `true`.

- [x] **Step 1: Write failing real-stream and CLI tests**

```go
func TestJudgeCallerConsumesFixtureSSE(t *testing.T) {
	server := newJudgeSSEServer(t, validJudgeJSON("pass")); config := validCLIJudgeConfig(server.URL)
	caller, err := newOpenAICompatibleJudgeCaller(config, server.Client(), true); if err != nil { t.Fatal(err) }
	raw, usage, err := caller(context.Background(), eval.QualityJudgePromptV1, `<criteria>[]</criteria><evidence></evidence>`)
	if err != nil || !strings.Contains(raw, `"verdict":"pass"`) || usage.OutputTokens == 0 { t.Fatalf("raw=%q usage=%+v err=%v",raw,usage,err) }
}

func TestJudgeCLIQualityFailIsAdvisory(t *testing.T) {
	result := runFixtureJudgeCLI(t, "fail")
	if result.ExitCode != exitOK || result.Score.Verdict != eval.ScoreFail { t.Fatalf("%+v",result) }
}
```

- [x] **Step 2: Verify RED**

Run: `go test ./cmd/och-eval -run 'JudgeCaller|JudgeCLI' -count=1`

- [x] **Step 3: Implement caller and command**

Use `openaicompat.Model` with text-only profile, EnvAPIKey, usage enabled, and frozen token field. Send system/user messages with `quality_judge` purpose, consume and close the stream, copy text/usage/duration, call `EvaluateJudgeAttempt`, print one Score JSON, and map exit classes exactly as the spec. Do not expose endpoint/model override flags.

- [x] **Step 4: Verify GREEN**

Run: `go test ./cmd/och-eval ./internal/harness/eval ./internal/harness/adapters/openaicompat -run 'Judge|Consent|Live|Exit' -count=1`

- [x] **Step 5: Commit**

```bash
git add cmd/och-eval
git commit -m "feat(eval): wire consent-gated live judge CLI"
```

### Task 6: Freeze examples, documentation, and completion evidence

**Files:**
- Create: `eval/judges/context-quality-judge.example.json`
- Modify: `eval/sets/context-quality-live.example.json`, `eval/scenarios/context-quality/scenario.json`
- Modify: `internal/docsguard/docs_test.go`
- Modify: `docs/architecture/evaluation.md`, `evaluation.zh-CN.md`, `evaluation-evidence.md`
- Modify: `docs/guides/evaluation-operations.md`, `evaluation-scenarios.md`, `README.md`, `docs/README.md`

**Interfaces:** Pins the canonical JudgeConfig and Scenario digests; removes only the Task 17 CLI deferral while retaining real-model/provider/variance blockers.

- [ ] **Step 1: Add examples and a failing docs/digest guard**

```go
func TestLiveJudgeExampleDigestsAndGuide(t *testing.T) {
	root := repoRoot(t)
	set, err := eval.DecodeEvalSet([]byte(read(t,filepath.Join(root,"eval/sets/context-quality-live.example.json"))))
	if err != nil { t.Fatal(err) }
	config, err := eval.DecodeJudgeConfig([]byte(read(t,filepath.Join(root,"eval/judges/context-quality-judge.example.json"))))
	if err != nil { t.Fatal(err) }
	digest, err := eval.JudgeConfigDigest(config)
	if err != nil || set.JudgeConfigDigest != digest { t.Fatalf("set=%q config=%q err=%v",set.JudgeConfigDigest,digest,err) }
	guide := read(t, filepath.Join(root,"docs/guides/evaluation-operations.md"))
	for _, value := range []string{"och-eval judge","OCH_EVAL_LIVE_CONFIRM","OCH_EVAL_LIVE_JUDGE_API_KEY"} { if !strings.Contains(guide,value) { t.Fatalf("missing %q",value) } }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/docsguard ./cmd/och-eval -run 'LiveJudgeExample|Documentation|Digest' -count=1`

- [ ] **Step 3: Compute canonical digests and update all docs**

Use temporary Go tests calling `JudgeConfigDigest` and `ScenarioDigest`, copy their output into references, then delete the temporary tests. Document command, consent, retention, advisory semantics, price availability, legacy refusal, commits, and remaining blockers in English and Chinese.

- [ ] **Step 4: Run completion verification**

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
GOOS=windows GOARCH=amd64 go build ./internal/harness/eval ./cmd/och-eval
git diff --check origin/main...HEAD
git status --short
```

- [ ] **Step 5: Commit**

```bash
git add eval docs README.md
git commit -m "docs(eval): publish live judge completion evidence"
```

## Completion gate

- [ ] All six task commits are present in the evidence ledger.
- [ ] Consent/binding failure occurs before credential access.
- [ ] A fixture SSE stream reaches an appended Score through the real adapter.
- [ ] Deterministic non-Pass prevents provider invocation.
- [ ] Quality Fail returns exit 0; insufficient evidence cannot pass.
- [ ] Bundle selection is deterministic and legacy deterministic regrade remains green.
- [ ] Task 17's CLI deferral is removed without changing Context, MCP, or GA blockers.
