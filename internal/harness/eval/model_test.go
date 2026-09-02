package eval

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validScenario() Scenario {
	return Scenario{
		FormatVersion:     FormatVersion,
		Schema:            SchemaScenario,
		ID:                "scenario-1",
		Description:       "a scenario",
		FixtureDigest:     "sha256:" + strings.Repeat("a", 64),
		FixtureCopyPolicy: FixtureCopyPolicy{},
		Actions: []ScenarioAction{
			{Type: ActionPrompt, Prompt: &PromptAction{Text: "hello"}},
			{Type: ActionCompact, Compact: &CompactAction{Strategy: "summary"}},
			{Type: ActionCancel, Cancel: &CancelAction{TargetActionIndex: 0}},
			{Type: ActionRestart, Restart: &RestartAction{}},
			{Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "output.txt"}},
		},
		RequiredCapabilities:     []string{"prompt"},
		RequiredEvidenceRoles:    []string{"transcript"},
		OptionalEvidenceRoles:    []string{"workspace"},
		DeterministicVerifierIDs: []string{"verifier-1"},
		LiveJudgeCriteriaIDs:     []string{"criteria-1"},
		Limits:                   ScenarioLimits{},
		PairingTags:              []string{"baseline"},
	}
}

func validSubject() Subject {
	return Subject{
		FormatVersion:      FormatVersion,
		Schema:             SchemaSubject,
		ID:                 "subject-1",
		RepositoryRevision: "abc123",
		RepositoryDirty:    false,
		Provider: SubjectProvider{
			AdapterKind:        "openaicompat",
			NormalizedEndpoint: "https://api.example.com/v1",
			ModelID:            "test-model",
			ContextWindow:      128000,
			MaxOutput:          4096,
			CredentialEnvVar:   "OCH_API_KEY",
			Lane:               ProviderLaneFixture,
		},
		Context: SubjectContext{
			TriggerPercent:                 80,
			TargetPercent:                  55,
			TailPercent:                    25,
			MaxSummaryChunks:               8,
			MaxOverflowCompactionsPerTurn:  2,
			MaxPrunedToolResultsPerRequest: 64,
			CompactionTimeout:              2 * time.Minute,
		},
		Policy: SubjectPolicy{
			Mode:                "default",
			ToolCatalogIdentity: "catalog-v1",
			Limits:              SubjectLimits{},
			SandboxPolicy:       SandboxPolicySandboxed,
		},
	}
}

func validExecutorInProcess() Executor {
	return Executor{
		FormatVersion:              FormatVersion,
		Schema:                     SchemaExecutor,
		ID:                         "in-process",
		Kind:                       ExecutorInProcess,
		OCHRevision:                "abc123",
		EvalBuildRevision:          "def456",
		CompositionContractVersion: "v1",
		Capabilities:               []string{"prompt", "compact"},
	}
}

func validExecutorACPSubprocess() Executor {
	executor := validExecutorInProcess()
	executor.ID = "acp-subprocess"
	executor.Kind = ExecutorACPSubprocess
	executor.ACPSubprocess = &ACPSubprocessIdentity{
		BinarySHA256:    strings.Repeat("b", 64),
		NormalizedArgv:  []string{"och", "-acp"},
		ProtocolVersion: "1",
		AgentName:       "och",
		AgentVersion:    "0.1.0",
	}
	return executor
}

func marshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}

// --- Scenario ---

func TestDecodeScenarioRoundTrip(t *testing.T) {
	want := validScenario()
	got, err := DecodeScenario(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeScenario: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("DecodeScenario round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestDecodeScenarioRejectsUnknownField(t *testing.T) {
	data := marshal(t, validScenario())
	withExtra := strings.Replace(string(data), `"schema":`, `"unknownField":true,"schema":`, 1)
	if _, err := DecodeScenario([]byte(withExtra)); err == nil {
		t.Fatal("DecodeScenario accepted an unknown top-level field")
	}
}

func TestDecodeScenarioRejectsDuplicateKey(t *testing.T) {
	data := string(marshal(t, validScenario()))
	withDuplicate := strings.Replace(data, `"schema":"och.eval.scenario"`, `"schema":"och.eval.scenario","schema":"och.eval.scenario"`, 1)
	if _, err := DecodeScenario([]byte(withDuplicate)); err == nil {
		t.Fatal("DecodeScenario accepted a duplicate top-level key")
	}
}

func TestDecodeScenarioRejectsDuplicateNestedKey(t *testing.T) {
	data := string(marshal(t, validScenario()))
	withDuplicate := strings.Replace(data, `"prompt":{"text":"hello"}`, `"prompt":{"text":"hello","text":"hello"}`, 1)
	if withDuplicate == data {
		t.Fatal("test fixture did not contain the expected nested prompt payload")
	}
	if _, err := DecodeScenario([]byte(withDuplicate)); err == nil {
		t.Fatal("DecodeScenario accepted a duplicate nested key")
	}
}

func TestDecodeScenarioRejectsWrongSchema(t *testing.T) {
	scenario := validScenario()
	scenario.Schema = "och.eval.subject"
	if _, err := DecodeScenario(marshal(t, scenario)); !errors.Is(err, errUnsupportedSchema) {
		t.Fatalf("DecodeScenario error = %v, want wrapping errUnsupportedSchema", err)
	}
}

func TestDecodeScenarioRejectsWrongFormatVersion(t *testing.T) {
	scenario := validScenario()
	scenario.FormatVersion = 2
	if _, err := DecodeScenario(marshal(t, scenario)); !errors.Is(err, errUnsupportedFormatVersion) {
		t.Fatalf("DecodeScenario error = %v, want wrapping errUnsupportedFormatVersion", err)
	}
}

func TestScenarioValidateRejectsInvalidID(t *testing.T) {
	scenario := validScenario()
	scenario.ID = "Not Valid"
	if err := scenario.Validate(); !errors.Is(err, errInvalidID) {
		t.Fatalf("Validate() error = %v, want wrapping errInvalidID", err)
	}
}

func TestScenarioValidateRejectsEmptyActions(t *testing.T) {
	scenario := validScenario()
	scenario.Actions = nil
	if err := scenario.Validate(); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("Validate() error = %v, want wrapping errInvalidDocument", err)
	}
}

func TestScenarioValidateRejectsCancelTargetingFutureAction(t *testing.T) {
	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		{Type: ActionCancel, Cancel: &CancelAction{TargetActionIndex: 1}},
		{Type: ActionPrompt, Prompt: &PromptAction{Text: "hi"}},
	}
	if err := scenario.Validate(); err == nil {
		t.Fatal("Validate() accepted a cancel action targeting a later action")
	}
}

func TestScenarioValidateRejectsCollectWithBothOrNeitherTarget(t *testing.T) {
	for _, collect := range []*CollectAction{
		{},
		{WorkspacePath: "a", VerifierFact: "b"},
	} {
		scenario := validScenario()
		scenario.Actions = []ScenarioAction{{Type: ActionCollect, Collect: collect}}
		if err := scenario.Validate(); err == nil {
			t.Fatalf("Validate() accepted collect action %+v", collect)
		}
	}
}

func TestScenarioValidateRejectsMismatchedActionPayload(t *testing.T) {
	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		{Type: ActionPrompt, Prompt: &PromptAction{Text: "hi"}, Compact: &CompactAction{Strategy: "summary"}},
	}
	if err := scenario.Validate(); err == nil {
		t.Fatal("Validate() accepted an action with two populated payloads")
	}
}

func TestScenarioValidateRejectsOverlappingEvidenceRoles(t *testing.T) {
	scenario := validScenario()
	scenario.RequiredEvidenceRoles = []string{"transcript"}
	scenario.OptionalEvidenceRoles = []string{"transcript"}
	if err := scenario.Validate(); err == nil {
		t.Fatal("Validate() accepted a role that is both required and optional")
	}
}

func TestScenarioValidateRejectsBadFixtureDigest(t *testing.T) {
	scenario := validScenario()
	scenario.FixtureDigest = "not-a-digest"
	if err := scenario.Validate(); err == nil {
		t.Fatal("Validate() accepted a malformed fixtureDigest")
	}
}

// --- Subject ---

func TestDecodeSubjectRoundTrip(t *testing.T) {
	want := validSubject()
	got, err := DecodeSubject(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeSubject: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("DecodeSubject round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestDecodeSubjectRejectsUnknownField(t *testing.T) {
	data := string(marshal(t, validSubject()))
	withExtra := strings.Replace(data, `"schema":`, `"unknownField":true,"schema":`, 1)
	if _, err := DecodeSubject([]byte(withExtra)); err == nil {
		t.Fatal("DecodeSubject accepted an unknown top-level field")
	}
}

func TestDecodeSubjectRejectsDuplicateKey(t *testing.T) {
	data := string(marshal(t, validSubject()))
	withDuplicate := strings.Replace(data, `"mode":"default"`, `"mode":"default","mode":"default"`, 1)
	if _, err := DecodeSubject([]byte(withDuplicate)); err == nil {
		t.Fatal("DecodeSubject accepted a duplicate nested key")
	}
}

func TestDecodeSubjectRejectsWrongSchema(t *testing.T) {
	subject := validSubject()
	subject.Schema = SchemaScenario
	if _, err := DecodeSubject(marshal(t, subject)); !errors.Is(err, errUnsupportedSchema) {
		t.Fatalf("DecodeSubject error = %v, want wrapping errUnsupportedSchema", err)
	}
}

func TestDecodeSubjectRejectsWrongFormatVersion(t *testing.T) {
	subject := validSubject()
	subject.FormatVersion = 0
	if _, err := DecodeSubject(marshal(t, subject)); !errors.Is(err, errUnsupportedFormatVersion) {
		t.Fatalf("DecodeSubject error = %v, want wrapping errUnsupportedFormatVersion", err)
	}
}

func TestSubjectValidateRejectsEndpointWithUserinfo(t *testing.T) {
	subject := validSubject()
	subject.Provider.NormalizedEndpoint = "https://user:pass@api.example.com/v1"
	if err := subject.Validate(); err == nil {
		t.Fatal("Validate() accepted an endpoint carrying userinfo")
	}
}

func TestSubjectValidateRejectsEndpointWithQuery(t *testing.T) {
	subject := validSubject()
	subject.Provider.NormalizedEndpoint = "https://api.example.com/v1?key=abc"
	if err := subject.Validate(); err == nil {
		t.Fatal("Validate() accepted an endpoint carrying a query string")
	}
}

func TestSubjectValidateRejectsLowercaseCredentialEnvVar(t *testing.T) {
	subject := validSubject()
	subject.Provider.CredentialEnvVar = "och_api_key"
	if err := subject.Validate(); err == nil {
		t.Fatal("Validate() accepted a lowercase credential env var name")
	}
}

func TestSubjectValidateRejectsUnknownProviderLane(t *testing.T) {
	subject := validSubject()
	subject.Provider.Lane = "staging"
	if err := subject.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown provider lane")
	}
}

func TestSubjectValidateRejectsInvertedContextPercentages(t *testing.T) {
	subject := validSubject()
	subject.Context.TargetPercent = subject.Context.TriggerPercent
	if err := subject.Validate(); err == nil {
		t.Fatal("Validate() accepted targetPercent >= triggerPercent")
	}
}

func TestSubjectValidateRejectsUnknownSandboxPolicy(t *testing.T) {
	subject := validSubject()
	subject.Policy.SandboxPolicy = "maybe"
	if err := subject.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown sandbox policy")
	}
}

// --- Executor ---

func TestDecodeExecutorRoundTripInProcess(t *testing.T) {
	want := validExecutorInProcess()
	got, err := DecodeExecutor(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeExecutor: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("DecodeExecutor round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestDecodeExecutorRoundTripACPSubprocess(t *testing.T) {
	want := validExecutorACPSubprocess()
	got, err := DecodeExecutor(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeExecutor: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("DecodeExecutor round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestExecutorValidateRejectsInProcessWithACPIdentity(t *testing.T) {
	executor := validExecutorInProcess()
	executor.ACPSubprocess = &ACPSubprocessIdentity{
		BinarySHA256:    strings.Repeat("c", 64),
		NormalizedArgv:  []string{"och"},
		ProtocolVersion: "1",
		AgentName:       "och",
		AgentVersion:    "0.1.0",
	}
	if err := executor.Validate(); err == nil {
		t.Fatal("Validate() accepted an in_process executor carrying an ACP identity")
	}
}

func TestExecutorValidateRejectsACPSubprocessWithoutIdentity(t *testing.T) {
	executor := validExecutorInProcess()
	executor.Kind = ExecutorACPSubprocess
	if err := executor.Validate(); err == nil {
		t.Fatal("Validate() accepted an acp_subprocess executor without an ACP identity")
	}
}

func TestExecutorValidateRejectsBadBinaryDigest(t *testing.T) {
	executor := validExecutorACPSubprocess()
	executor.ACPSubprocess.BinarySHA256 = "not-hex"
	if err := executor.Validate(); err == nil {
		t.Fatal("Validate() accepted a malformed binarySha256")
	}
}

func TestExecutorValidateRejectsDuplicateCapabilities(t *testing.T) {
	executor := validExecutorInProcess()
	executor.Capabilities = []string{"prompt", "prompt"}
	if err := executor.Validate(); err == nil {
		t.Fatal("Validate() accepted duplicate capabilities")
	}
}

func TestExecutorValidateRejectsUnknownKind(t *testing.T) {
	executor := validExecutorInProcess()
	executor.Kind = "sandboxed"
	if err := executor.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown executor kind")
	}
}
