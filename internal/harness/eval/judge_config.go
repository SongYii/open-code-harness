package eval

import (
	"fmt"
	"net/url"
)

// SchemaJudgeConfig is the `och.eval.judge-config` document schema. It is
// a document rather than an in-memory value precisely so a live Score's
// claimed judge identity can be proven offline from an Attempt's own
// immutable evidence: the EvalSet names this document's digest, the
// runner stages the exact canonical bytes into the Attempt's evidence,
// and the manifest hashes them.
const SchemaJudgeConfig = "och.eval.judge-config"

// JudgeAdapterOpenAICompat is the only adapter kind a JudgeConfig may
// name today, matching internal/harness/adapters/openaicompat. A new kind
// is a deliberate schema change, never a free-form string a config file
// can introduce on its own.
const JudgeAdapterOpenAICompat = "openaicompat"

// QualityJudgePromptID is the frozen prompt asset's own stable ID. A
// JudgeConfig must name exactly this prompt: the judge contract is
// defined by prompts/quality_judge_v1.md's own instructions, and a
// config that pointed at different text would produce Scores whose
// meaning could not be reconstructed from this repository.
const QualityJudgePromptID = "och_quality_judge_v1"

// maxJudgeRubricBytes bounds one criterion's own rubric. A rubric is
// trusted, operator-authored text rendered outside the untrusted
// evidence block, so it is bounded to keep a config from crowding the
// evidence budget out of the judge's own context window.
const maxJudgeRubricBytes = 4096

// judgeMaxTokensFields are the request field names an OpenAI-compatible
// endpoint may expect for an output cap. Empty means the adapter's own
// default; the two named values are frozen here so a config cannot ask
// the adapter to emit an arbitrary field.
var judgeMaxTokensFields = map[string]bool{"": true, "max_tokens": true, "max_completion_tokens": true}

// JudgeProvider freezes the judge model's own endpoint identity. Like
// SubjectProvider it carries only a credential variable *name*, never a
// value — nothing in this struct may ever hold a secret, because the
// whole document is serialized verbatim into an Attempt's evidence.
//
// NormalizedEndpoint is validated here only for shape (a credential-free,
// query-free, fragment-free http or https URL). Requiring HTTPS is a
// separate, stricter rule the production CLI applies when it constructs a
// real caller, which is what lets a test drive the same frozen document
// against a loopback httptest server without any production path ever
// accepting a plaintext endpoint.
type JudgeProvider struct {
	AdapterKind        string `json:"adapterKind"`
	NormalizedEndpoint string `json:"normalizedEndpoint"`
	ModelID            string `json:"modelId"`
	CredentialEnvVar   string `json:"credentialEnvVar"`
	ContextWindow      uint32 `json:"contextWindow"`
	MaxOutput          uint32 `json:"maxOutput"`
	IncludeUsage       bool   `json:"includeUsage"`
	MaxTokensField     string `json:"maxTokensField,omitempty"`
}

// JudgePrompt names the frozen prompt asset and pins its exact bytes.
type JudgePrompt struct {
	ID     string `json:"id"`
	Digest Digest `json:"digest"`
}

// JudgeCriterion names one quality dimension a judge run evaluates, the
// rubric it is evaluated against, and which manifest evidence roles it
// may see to evaluate it (the design's own "given only bounded, redacted,
// manifest-declared evidence selected by criteria" — a criterion never
// sees a role it did not itself declare).
type JudgeCriterion struct {
	ID            string   `json:"id"`
	Rubric        string   `json:"rubric"`
	EvidenceRoles []string `json:"evidenceRoles"`
}

// JudgeConfig freezes one judge run's own model/config/prompt identity,
// independent of the Subject's own identity — judge usage and cost are
// kept separate from Subject usage and cost by construction: RunJudge
// never touches Outcome or the Subject's own evidence beyond reading it,
// and its own ScorerUsage is carried on the Score it produces, never
// folded into Outcome.
type JudgeConfig struct {
	FormatVersion int    `json:"formatVersion"`
	Schema        string `json:"schema"`
	ID            string `json:"id"`
	Version       string `json:"version"`

	Provider JudgeProvider    `json:"provider"`
	Prompt   JudgePrompt      `json:"prompt"`
	Criteria []JudgeCriterion `json:"criteria"`

	PriceTableDigest Digest `json:"priceTableDigest,omitempty"`
}

// DecodeJudgeConfig strictly decodes and validates one
// `och.eval.judge-config` document. The decode rejects unknown fields, so
// a credential value cannot ride along inside an unmodeled key and reach
// an Attempt's evidence.
func DecodeJudgeConfig(data []byte) (JudgeConfig, error) {
	var config JudgeConfig
	if err := decodeStrict(data, &config); err != nil {
		return JudgeConfig{}, fmt.Errorf("eval: judge config: %w", err)
	}
	if config.Schema != SchemaJudgeConfig {
		return JudgeConfig{}, fmt.Errorf("eval: judge config: %w: %q", errUnsupportedSchema, config.Schema)
	}
	if config.FormatVersion != FormatVersion {
		return JudgeConfig{}, fmt.Errorf("eval: judge config: %w: %d", errUnsupportedFormatVersion, config.FormatVersion)
	}
	if err := config.Validate(); err != nil {
		return JudgeConfig{}, err
	}
	return config, nil
}

// JudgeConfigDigest is this package's canonical identity digest of a
// validated JudgeConfig: SHA-256 over the exact canonical JSON bytes of
// the whole document, the same convention ScenarioDigest/SubjectDigest
// already use. Validating first is what makes the digest an identity a
// Score may cite — an unvalidatable config never receives one.
func JudgeConfigDigest(config JudgeConfig) (Digest, error) {
	if err := config.Validate(); err != nil {
		return "", fmt.Errorf("eval: judge config digest: %w", err)
	}
	return canonicalDigest(config)
}

// Validate checks every field this document requires.
func (config JudgeConfig) Validate() error {
	if !hasText(config.ID) {
		return fmt.Errorf("%w: judge config id is required", errInvalidDocument)
	}
	if !hasText(config.Version) {
		return fmt.Errorf("%w: judge config version is required", errInvalidDocument)
	}
	if err := config.Provider.validate(); err != nil {
		return err
	}
	if err := config.Prompt.validate(); err != nil {
		return err
	}
	if len(config.Criteria) == 0 {
		return fmt.Errorf("%w: judge config has no criteria", errInvalidDocument)
	}
	seenCriteria := make(map[string]bool, len(config.Criteria))
	for index, criterion := range config.Criteria {
		if err := criterion.validate(index); err != nil {
			return err
		}
		if seenCriteria[criterion.ID] {
			return fmt.Errorf("%w: criteria %d: repeated criterion id %q", errInvalidDocument, index, criterion.ID)
		}
		seenCriteria[criterion.ID] = true
	}
	if config.PriceTableDigest != "" && !digestStringPattern.MatchString(string(config.PriceTableDigest)) {
		return fmt.Errorf("%w: priceTableDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	return nil
}

func (provider JudgeProvider) validate() error {
	if provider.AdapterKind != JudgeAdapterOpenAICompat {
		return fmt.Errorf("%w: provider.adapterKind must be %q", errInvalidDocument, JudgeAdapterOpenAICompat)
	}
	if err := validateJudgeEndpoint(provider.NormalizedEndpoint); err != nil {
		return err
	}
	if !hasText(provider.ModelID) {
		return fmt.Errorf("%w: provider.modelId is required", errInvalidDocument)
	}
	if !envVarNamePattern.MatchString(provider.CredentialEnvVar) {
		return fmt.Errorf("%w: provider.credentialEnvVar must be a valid environment variable name", errInvalidDocument)
	}
	if provider.ContextWindow == 0 || provider.MaxOutput == 0 {
		return fmt.Errorf("%w: provider.contextWindow and provider.maxOutput must be greater than zero", errInvalidDocument)
	}
	if provider.MaxOutput > provider.ContextWindow {
		return fmt.Errorf("%w: provider.maxOutput %d exceeds provider.contextWindow %d",
			errInvalidDocument, provider.MaxOutput, provider.ContextWindow)
	}
	if !provider.IncludeUsage {
		// A live judge Score must be able to report real token usage; a
		// provider configured to withhold it could only ever publish an
		// unavailable cost, which the contract treats as a defect rather
		// than an acceptable default.
		return fmt.Errorf("%w: provider.includeUsage must be true for a live judge", errInvalidDocument)
	}
	if !judgeMaxTokensFields[provider.MaxTokensField] {
		return fmt.Errorf("%w: provider.maxTokensField must be empty, %q, or %q",
			errInvalidDocument, "max_tokens", "max_completion_tokens")
	}
	return nil
}

// validateJudgeEndpoint enforces the credential-free, query-free,
// fragment-free endpoint shape. It deliberately admits both http and
// https: HTTPS-only is the production CLI's own stricter rule, applied
// where a real network caller is constructed.
func validateJudgeEndpoint(endpoint string) error {
	if !hasText(endpoint) {
		return fmt.Errorf("%w: provider.normalizedEndpoint is required", errInvalidDocument)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%w: provider.normalizedEndpoint is not a valid URL: %w", errInvalidDocument, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: provider.normalizedEndpoint must use http or https, not %q", errInvalidDocument, parsed.Scheme)
	}
	if parsed.Opaque != "" || parsed.Host == "" {
		return fmt.Errorf("%w: provider.normalizedEndpoint must name a host", errInvalidDocument)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: provider.normalizedEndpoint must not carry userinfo", errInvalidDocument)
	}
	if parsed.RawQuery != "" {
		return fmt.Errorf("%w: provider.normalizedEndpoint must not carry a query string", errInvalidDocument)
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("%w: provider.normalizedEndpoint must not carry a fragment", errInvalidDocument)
	}
	return nil
}

func (prompt JudgePrompt) validate() error {
	if prompt.ID != QualityJudgePromptID {
		return fmt.Errorf("%w: prompt.id must be %q, not %q", errInvalidDocument, QualityJudgePromptID, prompt.ID)
	}
	if expected := QualityJudgePromptV1Digest(); prompt.Digest != expected {
		return fmt.Errorf("%w: prompt.digest %q does not identify %s", errInvalidDocument, prompt.Digest, QualityJudgePromptID)
	}
	return nil
}

func (criterion JudgeCriterion) validate(index int) error {
	if !hasText(criterion.ID) {
		return fmt.Errorf("%w: criteria %d: id is required", errInvalidDocument, index)
	}
	if !hasText(criterion.Rubric) {
		return fmt.Errorf("%w: criteria %d: rubric is required", errInvalidDocument, index)
	}
	if len(criterion.Rubric) > maxJudgeRubricBytes {
		return fmt.Errorf("%w: criteria %d: rubric is %d bytes, over the %d-byte cap",
			errInvalidDocument, index, len(criterion.Rubric), maxJudgeRubricBytes)
	}
	if len(criterion.EvidenceRoles) == 0 {
		return fmt.Errorf("%w: criteria %d: at least one evidence role is required", errInvalidDocument, index)
	}
	seenRoles := make(map[string]bool, len(criterion.EvidenceRoles))
	for _, role := range criterion.EvidenceRoles {
		if !hasText(role) {
			return fmt.Errorf("%w: criteria %d: evidence role must not be empty", errInvalidDocument, index)
		}
		if seenRoles[role] {
			return fmt.Errorf("%w: criteria %d: repeated evidence role %q", errInvalidDocument, index, role)
		}
		seenRoles[role] = true
	}
	return nil
}
