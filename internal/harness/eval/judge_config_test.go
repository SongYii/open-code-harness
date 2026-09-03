package eval

import (
	"reflect"
	"strings"
	"testing"
)

// validJudgeConfig is the canonical `och.eval.judge-config` document from
// the design's own example, which every mutation test below starts from so
// a rejection test is always proving that exactly one field was rejected.
func validJudgeConfig() JudgeConfig {
	return JudgeConfig{
		FormatVersion: FormatVersion,
		Schema:        SchemaJudgeConfig,
		ID:            "context-quality-judge",
		Version:       "v1",
		Provider: JudgeProvider{
			AdapterKind:        JudgeAdapterOpenAICompat,
			NormalizedEndpoint: "https://api.example.com/v1",
			ModelID:            "model-id",
			CredentialEnvVar:   "OCH_EVAL_LIVE_JUDGE_API_KEY",
			ContextWindow:      128000,
			MaxOutput:          4096,
			IncludeUsage:       true,
			MaxTokensField:     "max_completion_tokens",
		},
		Prompt: JudgePrompt{ID: QualityJudgePromptID, Digest: QualityJudgePromptV1Digest()},
		Criteria: []JudgeCriterion{{
			ID:            "constraint-preservation",
			Rubric:        "Determine whether all explicit user constraints remained enforced after compaction.",
			EvidenceRoles: []string{"scenario", "transcript", "audit", "workspace"},
		}},
	}
}

func TestDecodeJudgeConfigRoundTripAndDigest(t *testing.T) {
	want := validJudgeConfig()
	got, err := DecodeJudgeConfig(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeJudgeConfig: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the document:\n got=%+v\nwant=%+v", got, want)
	}
	first, err := JudgeConfigDigest(want)
	if err != nil {
		t.Fatalf("JudgeConfigDigest(want): %v", err)
	}
	second, err := JudgeConfigDigest(got)
	if err != nil {
		t.Fatalf("JudgeConfigDigest(got): %v", err)
	}
	if first != second {
		t.Fatalf("digest %q != %q", first, second)
	}
	if !digestStringPattern.MatchString(string(first)) {
		t.Fatalf("digest %q is not sha256:<64 lowercase hex>", first)
	}
}

// TestJudgeConfigDigestRefusesInvalidDocuments proves the digest is a
// digest *of a validated document*: an unvalidatable config must never
// receive an identity a Score could later cite.
func TestJudgeConfigDigestRefusesInvalidDocuments(t *testing.T) {
	config := validJudgeConfig()
	config.Criteria = nil
	if _, err := JudgeConfigDigest(config); err == nil {
		t.Fatal("JudgeConfigDigest accepted a config with no criteria")
	}
}

func TestDecodeJudgeConfigRejectsInvalidDocuments(t *testing.T) {
	longRubric := strings.Repeat("r", maxJudgeRubricBytes+1)
	cases := []struct {
		name   string
		mutate func(*JudgeConfig)
	}{
		{"unsupported schema", func(config *JudgeConfig) { config.Schema = "och.eval.other" }},
		{"unsupported format version", func(config *JudgeConfig) { config.FormatVersion = 2 }},
		{"missing id", func(config *JudgeConfig) { config.ID = " " }},
		{"missing version", func(config *JudgeConfig) { config.Version = "" }},
		{"unknown adapter kind", func(config *JudgeConfig) { config.Provider.AdapterKind = "anthropic" }},
		{"endpoint with userinfo", func(config *JudgeConfig) {
			config.Provider.NormalizedEndpoint = "https://user:pass@api.example.com/v1"
		}},
		{"endpoint with query", func(config *JudgeConfig) {
			config.Provider.NormalizedEndpoint = "https://api.example.com/v1?key=secret"
		}},
		{"endpoint with fragment", func(config *JudgeConfig) {
			config.Provider.NormalizedEndpoint = "https://api.example.com/v1#frag"
		}},
		{"endpoint with unsupported scheme", func(config *JudgeConfig) {
			config.Provider.NormalizedEndpoint = "ftp://api.example.com/v1"
		}},
		{"missing model id", func(config *JudgeConfig) { config.Provider.ModelID = "" }},
		{"credential env var is not a name", func(config *JudgeConfig) { config.Provider.CredentialEnvVar = "not a name" }},
		{"zero context window", func(config *JudgeConfig) { config.Provider.ContextWindow = 0 }},
		{"zero max output", func(config *JudgeConfig) { config.Provider.MaxOutput = 0 }},
		{"max output exceeds context window", func(config *JudgeConfig) {
			config.Provider.ContextWindow = 1024
			config.Provider.MaxOutput = 2048
		}},
		{"usage reporting disabled", func(config *JudgeConfig) { config.Provider.IncludeUsage = false }},
		{"unknown max tokens field", func(config *JudgeConfig) { config.Provider.MaxTokensField = "maxTokens" }},
		{"unknown prompt id", func(config *JudgeConfig) { config.Prompt.ID = "och_quality_judge_v2" }},
		{"prompt digest disagrees with embedded prompt", func(config *JudgeConfig) {
			config.Prompt.Digest = mustDigest(t, 0x41)
		}},
		{"no criteria", func(config *JudgeConfig) { config.Criteria = nil }},
		{"criterion without id", func(config *JudgeConfig) { config.Criteria[0].ID = "" }},
		{"criterion without rubric", func(config *JudgeConfig) { config.Criteria[0].Rubric = "  " }},
		{"criterion rubric exceeds the cap", func(config *JudgeConfig) { config.Criteria[0].Rubric = longRubric }},
		{"criterion without evidence roles", func(config *JudgeConfig) { config.Criteria[0].EvidenceRoles = nil }},
		{"criterion with an empty evidence role", func(config *JudgeConfig) {
			config.Criteria[0].EvidenceRoles = []string{"transcript", ""}
		}},
		{"criterion repeating an evidence role", func(config *JudgeConfig) {
			config.Criteria[0].EvidenceRoles = []string{"transcript", "transcript"}
		}},
		{"repeated criterion id", func(config *JudgeConfig) {
			config.Criteria = append(config.Criteria, config.Criteria[0])
		}},
		{"malformed price table digest", func(config *JudgeConfig) { config.PriceTableDigest = "sha256:short" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := validJudgeConfig()
			testCase.mutate(&config)
			if _, err := DecodeJudgeConfig(marshal(t, config)); err == nil {
				t.Fatalf("DecodeJudgeConfig accepted %s", testCase.name)
			}
		})
	}
}

// TestDecodeJudgeConfigRejectsUnknownFields proves the decode is strict,
// so a credential value smuggled into an unmodeled field can never ride
// along inside a document this package claims to have frozen.
func TestDecodeJudgeConfigRejectsUnknownFields(t *testing.T) {
	data := marshal(t, validJudgeConfig())
	withExtra := strings.Replace(string(data), `{"formatVersion"`, `{"apiKey":"sk-secret","formatVersion"`, 1)
	if _, err := DecodeJudgeConfig([]byte(withExtra)); err == nil {
		t.Fatal("DecodeJudgeConfig accepted an unknown field")
	}
}

// TestJudgeConfigSerializationCarriesNoCredentialValue proves only the
// credential's *name* is serializable, matching Subject's own rule.
func TestJudgeConfigSerializationCarriesNoCredentialValue(t *testing.T) {
	encoded := string(marshal(t, validJudgeConfig()))
	if !strings.Contains(encoded, "OCH_EVAL_LIVE_JUDGE_API_KEY") {
		t.Fatal("credential env var name is not serialized")
	}
	for _, forbidden := range []string{"apiKey", "credential\":", "secret", "sk-"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("serialized JudgeConfig carried %q: %s", forbidden, encoded)
		}
	}
}

func TestScorerUsageDistinguishesUnavailableFromComputedZero(t *testing.T) {
	for _, usage := range []ScorerUsage{
		{CostStatus: CostStatusUnavailable},
		{CostStatus: CostStatusComputed, CostCurrency: "USD", CostMicrounits: 0},
		{CostStatus: CostStatusComputed, CostCurrency: "USD", CostMicrounits: 1250},
	} {
		score := validScore(t, mustAttemptID(t))
		score.ScorerUsage = &usage
		if err := score.Validate(); err != nil {
			t.Fatalf("Validate(%+v): %v", usage, err)
		}
	}
}

// TestScorerUsageLegacyScoresRemainReadable pins the spec's own
// compatibility rule: a Score published before costStatus existed must
// still decode and validate.
func TestScorerUsageLegacyScoresRemainReadable(t *testing.T) {
	score := validScore(t, mustAttemptID(t))
	score.ScorerUsage = &ScorerUsage{InputTokens: 10, OutputTokens: 3, DurationMillis: 250}
	if err := score.Validate(); err != nil {
		t.Fatalf("legacy ScorerUsage rejected: %v", err)
	}
	if _, err := DecodeScore(marshal(t, score)); err != nil {
		t.Fatalf("DecodeScore(legacy): %v", err)
	}
}

func TestScorerUsageRejectsInconsistentCost(t *testing.T) {
	cases := []struct {
		name  string
		usage ScorerUsage
	}{
		{"unknown status", ScorerUsage{CostStatus: "estimated"}},
		{"unavailable with a currency", ScorerUsage{CostStatus: CostStatusUnavailable, CostCurrency: "USD"}},
		{"unavailable with a cost", ScorerUsage{CostStatus: CostStatusUnavailable, CostMicrounits: 5}},
		{"computed without a currency", ScorerUsage{CostStatus: CostStatusComputed, CostMicrounits: 5}},
		{"negative cost", ScorerUsage{CostStatus: CostStatusComputed, CostCurrency: "USD", CostMicrounits: -1}},
		{"legacy status with a currency", ScorerUsage{CostCurrency: "USD"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			score := validScore(t, mustAttemptID(t))
			usage := testCase.usage
			score.ScorerUsage = &usage
			if err := score.Validate(); err == nil {
				t.Fatalf("Validate accepted %s", testCase.name)
			}
		})
	}
}

// TestResolveScorerCostReportsUnavailableRatherThanZero pins price.go's
// own "unavailable price is explicit, never zero" rule onto the new cost
// status, including the real computed-zero case a free model produces.
func TestResolveScorerCostReportsUnavailableRatherThanZero(t *testing.T) {
	table := PriceTable{Currency: "USD", Entries: []PriceEntry{
		{ModelID: "priced-model", InputMicrounitsPerToken: 3, OutputMicrounitsPerToken: 15},
		{ModelID: "free-model"},
	}}

	status, microunits, currency := ResolveScorerCost(&table, "priced-model", 100, 10, 0)
	if status != CostStatusComputed || microunits != 450 || currency != "USD" {
		t.Fatalf("priced model: status=%q microunits=%d currency=%q", status, microunits, currency)
	}

	status, microunits, currency = ResolveScorerCost(&table, "free-model", 100, 10, 0)
	if status != CostStatusComputed || microunits != 0 || currency != "USD" {
		t.Fatalf("free model must be a computed zero: status=%q microunits=%d currency=%q", status, microunits, currency)
	}

	for _, unavailable := range []struct {
		name    string
		table   *PriceTable
		modelID string
	}{
		{"no price table at all", nil, "priced-model"},
		{"model absent from the table", &table, "unpriced-model"},
		{"table without a currency", &PriceTable{Entries: table.Entries}, "priced-model"},
	} {
		status, microunits, currency = ResolveScorerCost(unavailable.table, unavailable.modelID, 100, 10, 0)
		if status != CostStatusUnavailable || microunits != 0 || currency != "" {
			t.Fatalf("%s: status=%q microunits=%d currency=%q", unavailable.name, status, microunits, currency)
		}
	}
}
