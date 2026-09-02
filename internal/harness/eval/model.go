package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"time"
)

// FormatVersion is every eval wire document's v1 format version (design §6).
const FormatVersion = 1

// Schema strings for the document kinds this package defines (design §6's
// table). EvalSet and Report are not yet implemented here; see doc.go.
const (
	SchemaScenario         = "och.eval.scenario"
	SchemaSubject          = "och.eval.subject"
	SchemaExecutor         = "och.eval.executor"
	SchemaAttempt          = "och.eval.attempt"
	SchemaOutcome          = "och.eval.outcome"
	SchemaEvidenceManifest = "och.eval.evidence-manifest"
	SchemaScore            = "och.eval.score"
)

// EvalLane distinguishes the deterministic PR-CI channel from explicit
// live-model evaluation (design §9, §23, §24). It is independent of
// SubjectProviderLane: a live EvalSet always uses a live Subject, but the
// two are recorded on different documents (EvalSet identity versus Subject
// identity) and validated separately.
type EvalLane string

const (
	LaneFixture EvalLane = "fixture"
	LaneLive    EvalLane = "live"
)

var (
	errInvalidDocument          = errors.New("eval: invalid document")
	errUnsupportedSchema        = errors.New("eval: unsupported schema")
	errUnsupportedFormatVersion = errors.New("eval: unsupported format version")
)

// envVarNamePattern matches a POSIX-shell-safe environment variable name,
// the shape design §10 requires for Subject.Provider.CredentialEnvVar (the
// variable's *name*, never its value).
var envVarNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// digestStringPattern matches this package's own Digest string shape
// ("sha256:" + 64 lowercase hex characters), reused wherever a document
// field references a digest computed elsewhere (a fixture tree, a frozen
// price table).
var digestStringPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// sha256HexPattern matches a bare lowercase-hex SHA-256, the shape design
// §11 wants for an ACP subprocess Executor's exact binary hash.
var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ---------------------------------------------------------------------------
// Scenario (design §7)
// ---------------------------------------------------------------------------

// Scenario is the checked-in `och.eval.scenario` document: the task, its
// fixture, ordered actions, required executor capabilities, evidence roles,
// and scorer/verifier criteria.
type Scenario struct {
	FormatVersion int        `json:"formatVersion"`
	Schema        string     `json:"schema"`
	ID            ScenarioID `json:"id"`
	Description   string     `json:"description"`

	FixtureDigest     string            `json:"fixtureDigest"`
	FixtureCopyPolicy FixtureCopyPolicy `json:"fixtureCopyPolicy"`

	Actions []ScenarioAction `json:"actions"`

	// ApprovalScript freezes every scripted approval decision (design §7).
	// Empty means the Scenario expects no permission requests at all.
	ApprovalScript []ApprovalScriptEntry `json:"approvalScript,omitempty"`

	RequiredCapabilities []string `json:"requiredCapabilities"`

	RequiredEvidenceRoles []string `json:"requiredEvidenceRoles"`
	OptionalEvidenceRoles []string `json:"optionalEvidenceRoles,omitempty"`

	DeterministicVerifierIDs []string `json:"deterministicVerifierIds,omitempty"`
	LiveJudgeCriteriaIDs     []string `json:"liveJudgeCriteriaIds,omitempty"`

	Limits ScenarioLimits `json:"limits"`

	PairingTags []string `json:"pairingTags,omitempty"`
}

// DerivedRequiredCapabilities returns the capability names this Scenario
// implicitly requires beyond RequiredCapabilities, derived from its actions.
// A restart action requesting `interrupt` or `kill` requires the matching
// ACP-only capability (design: those two modes are ACP-subprocess-only);
// `clean_shutdown` and every other action type derive nothing, since both
// executors support them. Matrix expansion's capability check (design §9)
// should refuse a Cell missing a capability from either this method or
// RequiredCapabilities.
func (scenario Scenario) DerivedRequiredCapabilities() []string {
	var derived []string
	for _, action := range scenario.Actions {
		if action.Type != ActionRestart || action.Restart == nil {
			continue
		}
		switch action.Restart.Mode {
		case RestartModeInterrupt:
			derived = append(derived, "restart_interrupt")
		case RestartModeKill:
			derived = append(derived, "restart_kill")
		}
	}
	return derived
}

// FixtureCopyPolicy bounds one Scenario's fixture copy (design §7/§8). A
// zero field means "use the EvalSet default"; a Scenario may only narrow
// these, never widen them, which EvalSet-level validation (not yet
// implemented) enforces once EvalSet exists.
type FixtureCopyPolicy struct {
	MaxFiles      int   `json:"maxFiles,omitempty"`
	MaxFileBytes  int64 `json:"maxFileBytes,omitempty"`
	MaxTotalBytes int64 `json:"maxTotalBytes,omitempty"`
}

// ScenarioLimits bounds one Scenario (design §7); a zero field means "use
// the EvalSet default". Mirrors composition.Limits's own zero-means-default
// convention.
type ScenarioLimits struct {
	MaxSteps            int           `json:"maxSteps,omitempty"`
	MaxToolCallsPerStep int           `json:"maxToolCallsPerStep,omitempty"`
	MaxAssistantBytes   int           `json:"maxAssistantBytes,omitempty"`
	ApprovalTimeout     time.Duration `json:"approvalTimeout,omitempty"`
}

// ScenarioActionType names one of design §7's v1 actions.
type ScenarioActionType string

const (
	ActionPrompt  ScenarioActionType = "prompt"
	ActionCompact ScenarioActionType = "compact"
	ActionCancel  ScenarioActionType = "cancel"
	ActionRestart ScenarioActionType = "restart"
	ActionCollect ScenarioActionType = "collect"
)

// ScenarioAction is one ordered Scenario action (design §7). ID is a stable
// coordinate other structures reference (CancelAction.TargetActionID,
// ApprovalScriptEntry.PromptActionID) instead of a fragile slice index.
// Exactly the field matching Type is populated; the runner (a later PR)
// rejects an unsupported Scenario/Executor pairing before creating an
// Attempt.
type ScenarioAction struct {
	ID   ActionID           `json:"id"`
	Type ScenarioActionType `json:"type"`

	Prompt  *PromptAction  `json:"prompt,omitempty"`
	Compact *CompactAction `json:"compact,omitempty"`
	Cancel  *CancelAction  `json:"cancel,omitempty"`
	Restart *RestartAction `json:"restart,omitempty"`
	Collect *CollectAction `json:"collect,omitempty"`
}

// PromptAction carries bounded UTF-8 text (design §7). The concrete byte
// bound is an EvalSet/Application limit, not a fixed constant here.
type PromptAction struct {
	Text string `json:"text"`
}

// CompactAction carries the public compact-session summary/reset strategy
// and an optional bounded focus (design §7).
type CompactAction struct {
	Strategy string `json:"strategy"`
	Focus    string `json:"focus,omitempty"`
}

// CancelAction names a prior prompt action by its stable ID (design §7:
// "cancel names a prior in-flight action"). The target must be an earlier
// action of type prompt; a self, forward, or unknown target is invalid.
type CancelAction struct {
	TargetActionID ActionID `json:"targetActionId"`
}

// RestartMode is one of design's three restart request shapes.
// `RestartModeCleanShutdown` is supported by every executor this package
// defines; `RestartModeInterrupt` and `RestartModeKill` are ACP-subprocess-
// only and require the matching derived capability
// (Scenario.DerivedRequiredCapabilities) before a Cell may be paired with
// any executor.
type RestartMode string

const (
	RestartModeCleanShutdown RestartMode = "clean_shutdown"
	RestartModeInterrupt     RestartMode = "interrupt"
	RestartModeKill          RestartMode = "kill"
)

// RestartAction requests a production-surface shutdown/reopen sequence
// (design §7). Mode is required.
type RestartAction struct {
	Mode RestartMode `json:"mode"`
}

// CollectAction requests a declared workspace path or verifier fact (design
// §7). Exactly one of WorkspacePath or VerifierFact must be set.
type CollectAction struct {
	WorkspacePath string `json:"workspacePath,omitempty"`
	VerifierFact  string `json:"verifierFact,omitempty"`
}

// ApprovalAnswer is one scripted permission-request decision (design §7).
type ApprovalAnswer string

const (
	ApprovalAllow ApprovalAnswer = "allow"
	ApprovalDeny  ApprovalAnswer = "deny"
)

// ApprovalScriptEntry binds one prompt action's zero-based approval ordinal
// and expected tool name to a frozen allow/deny decision (design §7). The
// in-process and ACP executors compile the same script; a request matching
// none of {current prompt action, next ordinal, expected tool name} is
// denied and recorded as an approval_script_violation rather than consuming
// a later declaration.
type ApprovalScriptEntry struct {
	PromptActionID ActionID       `json:"promptActionId"`
	Ordinal        int            `json:"ordinal"`
	ToolName       string         `json:"toolName"`
	Answer         ApprovalAnswer `json:"answer"`
}

// DecodeScenario strictly decodes and validates one `och.eval.scenario`
// document (design §6).
func DecodeScenario(data []byte) (Scenario, error) {
	var scenario Scenario
	if err := decodeStrict(data, &scenario); err != nil {
		return Scenario{}, fmt.Errorf("eval: scenario: %w", err)
	}
	if scenario.Schema != SchemaScenario {
		return Scenario{}, fmt.Errorf("eval: scenario: %w: %q", errUnsupportedSchema, scenario.Schema)
	}
	if scenario.FormatVersion != FormatVersion {
		return Scenario{}, fmt.Errorf("eval: scenario: %w: %d", errUnsupportedFormatVersion, scenario.FormatVersion)
	}
	if err := scenario.Validate(); err != nil {
		return Scenario{}, err
	}
	return scenario, nil
}

// Validate checks every field design §7 requires. It does not check
// EvalSet-level narrowing (EvalSet does not exist yet) or verifier/judge
// catalog membership (the verifier catalog does not exist yet).
func (scenario Scenario) Validate() error {
	if _, err := ParseScenarioID(string(scenario.ID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !hasText(scenario.Description) {
		return fmt.Errorf("%w: description is required", errInvalidDocument)
	}
	if !digestStringPattern.MatchString(scenario.FixtureDigest) {
		return fmt.Errorf("%w: fixtureDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if err := scenario.FixtureCopyPolicy.validate(); err != nil {
		return err
	}
	if len(scenario.Actions) == 0 {
		return fmt.Errorf("%w: at least one action is required", errInvalidDocument)
	}
	actionIndex := make(map[ActionID]int, len(scenario.Actions))
	for index, action := range scenario.Actions {
		actionID, err := ParseActionID(string(action.ID))
		if err != nil {
			return fmt.Errorf("%w: action %d: %w", errInvalidDocument, index, err)
		}
		if _, exists := actionIndex[actionID]; exists {
			return fmt.Errorf("%w: action %d: duplicate action id %q", errInvalidDocument, index, actionID)
		}
		actionIndex[actionID] = index
		if err := action.validate(index); err != nil {
			return err
		}
	}
	for index, action := range scenario.Actions {
		if action.Type != ActionCancel {
			continue
		}
		targetIndex, exists := actionIndex[action.Cancel.TargetActionID]
		if !exists {
			return fmt.Errorf("%w: action %d: cancel.targetActionId %q does not name a known action", errInvalidDocument, index, action.Cancel.TargetActionID)
		}
		if targetIndex >= index {
			return fmt.Errorf("%w: action %d: cancel.targetActionId must name an earlier action", errInvalidDocument, index)
		}
		if scenario.Actions[targetIndex].Type != ActionPrompt {
			return fmt.Errorf("%w: action %d: cancel.targetActionId must name a prompt action", errInvalidDocument, index)
		}
	}
	if err := scenario.validateApprovalScript(actionIndex); err != nil {
		return err
	}
	if err := requireNonEmptyEntries("requiredCapabilities", scenario.RequiredCapabilities); err != nil {
		return err
	}
	if err := requireNonEmptyEntries("requiredEvidenceRoles", scenario.RequiredEvidenceRoles); err != nil {
		return err
	}
	if len(scenario.RequiredEvidenceRoles) == 0 {
		return fmt.Errorf("%w: at least one required evidence role is required", errInvalidDocument)
	}
	if err := requireNonEmptyEntries("optionalEvidenceRoles", scenario.OptionalEvidenceRoles); err != nil {
		return err
	}
	if overlap := stringSetOverlap(scenario.RequiredEvidenceRoles, scenario.OptionalEvidenceRoles); overlap != "" {
		return fmt.Errorf("%w: evidence role %q is both required and optional", errInvalidDocument, overlap)
	}
	if err := requireNonEmptyEntries("deterministicVerifierIds", scenario.DeterministicVerifierIDs); err != nil {
		return err
	}
	if err := requireNonEmptyEntries("liveJudgeCriteriaIds", scenario.LiveJudgeCriteriaIDs); err != nil {
		return err
	}
	if err := scenario.Limits.validate(); err != nil {
		return err
	}
	if err := requireNonEmptyEntries("pairingTags", scenario.PairingTags); err != nil {
		return err
	}
	return nil
}

func (policy FixtureCopyPolicy) validate() error {
	if policy.MaxFiles < 0 || policy.MaxFileBytes < 0 || policy.MaxTotalBytes < 0 {
		return fmt.Errorf("%w: fixtureCopyPolicy must not be negative", errInvalidDocument)
	}
	return nil
}

func (limits ScenarioLimits) validate() error {
	if limits.MaxSteps < 0 || limits.MaxToolCallsPerStep < 0 || limits.MaxAssistantBytes < 0 {
		return fmt.Errorf("%w: limits must not be negative", errInvalidDocument)
	}
	if limits.ApprovalTimeout < 0 {
		return fmt.Errorf("%w: limits.approvalTimeout must not be negative", errInvalidDocument)
	}
	return nil
}

// approvalCoordinate is one (prompt action, ordinal) pair an approval script
// entry declares. Coordinates, not generated wire/domain IDs, are what the
// runner matches a live permission request against (design §7).
type approvalCoordinate struct {
	promptActionID ActionID
	ordinal        int
}

// validateApprovalScript checks every entry references a known prompt
// action, no two entries share a coordinate, ordinals are contiguous from
// zero per prompt, tool names are non-empty and bounded, and every answer is
// allow or deny. actionIndex is the same action-ID-to-position map Validate
// already built while walking Actions.
func (scenario Scenario) validateApprovalScript(actionIndex map[ActionID]int) error {
	seen := make(map[approvalCoordinate]struct{}, len(scenario.ApprovalScript))
	ordinalsByPrompt := make(map[ActionID][]int)
	for index, entry := range scenario.ApprovalScript {
		promptActionID, err := ParseActionID(string(entry.PromptActionID))
		if err != nil {
			return fmt.Errorf("%w: approvalScript %d: %w", errInvalidDocument, index, err)
		}
		targetIndex, exists := actionIndex[promptActionID]
		if !exists {
			return fmt.Errorf("%w: approvalScript %d: promptActionId %q does not name a known action", errInvalidDocument, index, promptActionID)
		}
		if scenario.Actions[targetIndex].Type != ActionPrompt {
			return fmt.Errorf("%w: approvalScript %d: promptActionId %q must name a prompt action", errInvalidDocument, index, promptActionID)
		}
		if entry.Ordinal < 0 {
			return fmt.Errorf("%w: approvalScript %d: ordinal must not be negative", errInvalidDocument, index)
		}
		if !hasText(entry.ToolName) {
			return fmt.Errorf("%w: approvalScript %d: toolName is required", errInvalidDocument, index)
		}
		if len(entry.ToolName) > maxIDBytes {
			return fmt.Errorf("%w: approvalScript %d: toolName must not exceed %d bytes", errInvalidDocument, index, maxIDBytes)
		}
		switch entry.Answer {
		case ApprovalAllow, ApprovalDeny:
		default:
			return fmt.Errorf("%w: approvalScript %d: answer must be %q or %q", errInvalidDocument, index, ApprovalAllow, ApprovalDeny)
		}
		coordinate := approvalCoordinate{promptActionID: promptActionID, ordinal: entry.Ordinal}
		if _, exists := seen[coordinate]; exists {
			return fmt.Errorf("%w: approvalScript %d: duplicate coordinate (promptActionId=%q, ordinal=%d)", errInvalidDocument, index, promptActionID, entry.Ordinal)
		}
		seen[coordinate] = struct{}{}
		ordinalsByPrompt[promptActionID] = append(ordinalsByPrompt[promptActionID], entry.Ordinal)
	}
	for promptActionID, ordinals := range ordinalsByPrompt {
		sort.Ints(ordinals)
		for want, got := range ordinals {
			if got != want {
				return fmt.Errorf("%w: approvalScript: prompt %q ordinals must be contiguous from 0, got %v", errInvalidDocument, promptActionID, ordinals)
			}
		}
	}
	return nil
}

func (action ScenarioAction) validate(index int) error {
	switch action.Type {
	case ActionPrompt:
		if action.Prompt == nil {
			return fmt.Errorf("%w: action %d: type prompt requires a prompt payload", errInvalidDocument, index)
		}
		if !hasText(action.Prompt.Text) {
			return fmt.Errorf("%w: action %d: prompt.text is required", errInvalidDocument, index)
		}
	case ActionCompact:
		if action.Compact == nil {
			return fmt.Errorf("%w: action %d: type compact requires a compact payload", errInvalidDocument, index)
		}
		if !hasText(action.Compact.Strategy) {
			return fmt.Errorf("%w: action %d: compact.strategy is required", errInvalidDocument, index)
		}
	case ActionCancel:
		if action.Cancel == nil {
			return fmt.Errorf("%w: action %d: type cancel requires a cancel payload", errInvalidDocument, index)
		}
		if _, err := ParseActionID(string(action.Cancel.TargetActionID)); err != nil {
			return fmt.Errorf("%w: action %d: cancel.targetActionId: %w", errInvalidDocument, index, err)
		}
		// Whether the target exists, precedes this action, and names a
		// prompt is checked by Scenario.Validate's second pass, once every
		// action ID is known.
	case ActionRestart:
		if action.Restart == nil {
			return fmt.Errorf("%w: action %d: type restart requires a restart payload", errInvalidDocument, index)
		}
		switch action.Restart.Mode {
		case RestartModeCleanShutdown, RestartModeInterrupt, RestartModeKill:
		default:
			return fmt.Errorf("%w: action %d: restart.mode must be %q, %q, or %q", errInvalidDocument, index,
				RestartModeCleanShutdown, RestartModeInterrupt, RestartModeKill)
		}
	case ActionCollect:
		if action.Collect == nil {
			return fmt.Errorf("%w: action %d: type collect requires a collect payload", errInvalidDocument, index)
		}
		hasWorkspace := action.Collect.WorkspacePath != ""
		hasVerifier := action.Collect.VerifierFact != ""
		if hasWorkspace == hasVerifier {
			return fmt.Errorf("%w: action %d: collect requires exactly one of workspacePath or verifierFact", errInvalidDocument, index)
		}
	default:
		return fmt.Errorf("%w: action %d: unknown action type %q", errInvalidDocument, index, action.Type)
	}
	if action.otherPayloadsPopulated() {
		return fmt.Errorf("%w: action %d: only the payload matching type %q may be set", errInvalidDocument, index, action.Type)
	}
	return nil
}

func (action ScenarioAction) otherPayloadsPopulated() bool {
	count := 0
	if action.Prompt != nil {
		count++
	}
	if action.Compact != nil {
		count++
	}
	if action.Cancel != nil {
		count++
	}
	if action.Restart != nil {
		count++
	}
	if action.Collect != nil {
		count++
	}
	return count > 1
}

// ---------------------------------------------------------------------------
// Subject (design §10)
// ---------------------------------------------------------------------------

// SubjectProviderLane distinguishes a deterministic fixture-provider
// identity from a live-provider identity (design §10).
type SubjectProviderLane string

const (
	ProviderLaneFixture SubjectProviderLane = "fixture"
	ProviderLaneLive    SubjectProviderLane = "live"
)

// Subject is the frozen `och.eval.subject` document: a secret-free semantic
// identity for one OCH version, model, Context/Policy/Tool configuration,
// and production limits (design §4/§10). It never carries a credential
// value or an absolute machine-local path; those are Attempt facts.
type Subject struct {
	FormatVersion int       `json:"formatVersion"`
	Schema        string    `json:"schema"`
	ID            SubjectID `json:"id"`

	RepositoryRevision string `json:"repositoryRevision"`
	RepositoryDirty    bool   `json:"repositoryDirty"`

	Provider SubjectProvider `json:"provider"`
	Context  SubjectContext  `json:"context"`
	Policy   SubjectPolicy   `json:"policy"`

	// PriceTableDigest is an optional frozen price-table digest used for
	// cost reporting (design §10, §19). Empty means cost reporting is
	// unavailable for this Subject, never zero cost.
	PriceTableDigest string `json:"priceTableDigest,omitempty"`
}

// SubjectProvider identifies the model endpoint without any credential
// value (design §10).
type SubjectProvider struct {
	AdapterKind        string              `json:"adapterKind"`
	NormalizedEndpoint string              `json:"normalizedEndpoint"`
	ModelID            string              `json:"modelId"`
	ContextWindow      uint32              `json:"contextWindow"`
	MaxOutput          uint32              `json:"maxOutput"`
	CredentialEnvVar   string              `json:"credentialEnvVar"`
	Lane               SubjectProviderLane `json:"lane"`
}

// SubjectContext freezes the Context Engine knobs composition.Context
// exposes (design §10; internal/harness/composition/config.go's Context
// type is the runtime counterpart this snapshot's fields mirror).
type SubjectContext struct {
	TriggerPercent                 uint32        `json:"triggerPercent"`
	TargetPercent                  uint32        `json:"targetPercent"`
	TailPercent                    uint32        `json:"tailPercent"`
	MaxSummaryChunks               uint32        `json:"maxSummaryChunks"`
	MaxOverflowCompactionsPerTurn  uint32        `json:"maxOverflowCompactionsPerTurn"`
	MaxPrunedToolResultsPerRequest uint32        `json:"maxPrunedToolResultsPerRequest"`
	CompactionTimeout              time.Duration `json:"compactionTimeout"`
}

// SubjectSandboxPolicy names whether this Subject snapshot ran with OS-level
// exec confinement, mirroring composition.Config.AllowUnsandboxedExec's two
// possible identity states (design §10).
type SubjectSandboxPolicy string

const (
	SandboxPolicySandboxed          SubjectSandboxPolicy = "sandboxed"
	SandboxPolicyUnsandboxedAllowed SubjectSandboxPolicy = "unsandboxed_allowed"
)

// SubjectPolicy freezes Policy mode, tool catalog identity, Application
// limits, and sandbox policy (design §10).
type SubjectPolicy struct {
	Mode                string               `json:"mode"`
	ToolCatalogIdentity string               `json:"toolCatalogIdentity"`
	Limits              SubjectLimits        `json:"limits"`
	SandboxPolicy       SubjectSandboxPolicy `json:"sandboxPolicy"`
}

// SubjectLimits mirrors composition.Limits's zero-means-default convention.
type SubjectLimits struct {
	MaxSteps            int           `json:"maxSteps,omitempty"`
	MaxToolCallsPerStep int           `json:"maxToolCallsPerStep,omitempty"`
	MaxAssistantBytes   int           `json:"maxAssistantBytes,omitempty"`
	ApprovalTimeout     time.Duration `json:"approvalTimeout,omitempty"`
}

// DecodeSubject strictly decodes and validates one `och.eval.subject`
// document (design §6).
func DecodeSubject(data []byte) (Subject, error) {
	var subject Subject
	if err := decodeStrict(data, &subject); err != nil {
		return Subject{}, fmt.Errorf("eval: subject: %w", err)
	}
	if subject.Schema != SchemaSubject {
		return Subject{}, fmt.Errorf("eval: subject: %w: %q", errUnsupportedSchema, subject.Schema)
	}
	if subject.FormatVersion != FormatVersion {
		return Subject{}, fmt.Errorf("eval: subject: %w: %d", errUnsupportedFormatVersion, subject.FormatVersion)
	}
	if err := subject.Validate(); err != nil {
		return Subject{}, err
	}
	return subject, nil
}

// Validate checks every field design §10 requires, including the two
// secret-handling guarantees: the credential field is an environment
// variable name (never a value, and there is no field shaped like a value
// on this type at all), and the normalized endpoint excludes userinfo and
// query strings.
func (subject Subject) Validate() error {
	if _, err := ParseSubjectID(string(subject.ID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !hasText(subject.RepositoryRevision) {
		return fmt.Errorf("%w: repositoryRevision is required", errInvalidDocument)
	}
	if err := subject.Provider.validate(); err != nil {
		return err
	}
	if err := subject.Context.validate(); err != nil {
		return err
	}
	if err := subject.Policy.validate(); err != nil {
		return err
	}
	if subject.PriceTableDigest != "" && !digestStringPattern.MatchString(subject.PriceTableDigest) {
		return fmt.Errorf("%w: priceTableDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	return nil
}

func (provider SubjectProvider) validate() error {
	if !hasText(provider.AdapterKind) {
		return fmt.Errorf("%w: provider.adapterKind is required", errInvalidDocument)
	}
	if err := validateNormalizedEndpoint(provider.NormalizedEndpoint); err != nil {
		return err
	}
	if !hasText(provider.ModelID) {
		return fmt.Errorf("%w: provider.modelId is required", errInvalidDocument)
	}
	if provider.ContextWindow == 0 || provider.MaxOutput == 0 {
		return fmt.Errorf("%w: provider.contextWindow and provider.maxOutput must be greater than zero", errInvalidDocument)
	}
	if !envVarNamePattern.MatchString(provider.CredentialEnvVar) {
		return fmt.Errorf("%w: provider.credentialEnvVar must be a valid environment variable name", errInvalidDocument)
	}
	switch provider.Lane {
	case ProviderLaneFixture, ProviderLaneLive:
	default:
		return fmt.Errorf("%w: provider.lane must be %q or %q", errInvalidDocument, ProviderLaneFixture, ProviderLaneLive)
	}
	return nil
}

// validateNormalizedEndpoint enforces design §10's "normalized endpoint
// excludes userinfo and query strings."
func validateNormalizedEndpoint(endpoint string) error {
	if !hasText(endpoint) {
		return fmt.Errorf("%w: provider.normalizedEndpoint is required", errInvalidDocument)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%w: provider.normalizedEndpoint is not a valid URL: %w", errInvalidDocument, err)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: provider.normalizedEndpoint must not carry userinfo", errInvalidDocument)
	}
	if parsed.RawQuery != "" {
		return fmt.Errorf("%w: provider.normalizedEndpoint must not carry a query string", errInvalidDocument)
	}
	return nil
}

func (context SubjectContext) validate() error {
	if context.TriggerPercent == 0 || context.TriggerPercent > 99 {
		return fmt.Errorf("%w: context.triggerPercent must be 1-99", errInvalidDocument)
	}
	if context.TargetPercent == 0 || context.TargetPercent >= context.TriggerPercent {
		return fmt.Errorf("%w: context.targetPercent must be positive and less than triggerPercent", errInvalidDocument)
	}
	if context.TailPercent == 0 || context.TailPercent >= context.TargetPercent {
		return fmt.Errorf("%w: context.tailPercent must be positive and less than targetPercent", errInvalidDocument)
	}
	if context.MaxSummaryChunks == 0 {
		return fmt.Errorf("%w: context.maxSummaryChunks must be greater than zero", errInvalidDocument)
	}
	if context.MaxOverflowCompactionsPerTurn == 0 {
		return fmt.Errorf("%w: context.maxOverflowCompactionsPerTurn must be greater than zero", errInvalidDocument)
	}
	if context.MaxPrunedToolResultsPerRequest == 0 {
		return fmt.Errorf("%w: context.maxPrunedToolResultsPerRequest must be greater than zero", errInvalidDocument)
	}
	if context.CompactionTimeout <= 0 {
		return fmt.Errorf("%w: context.compactionTimeout must be greater than zero", errInvalidDocument)
	}
	return nil
}

func (policy SubjectPolicy) validate() error {
	if !hasText(policy.Mode) {
		return fmt.Errorf("%w: policy.mode is required", errInvalidDocument)
	}
	if !hasText(policy.ToolCatalogIdentity) {
		return fmt.Errorf("%w: policy.toolCatalogIdentity is required", errInvalidDocument)
	}
	if err := policy.Limits.validate(); err != nil {
		return err
	}
	switch policy.SandboxPolicy {
	case SandboxPolicySandboxed, SandboxPolicyUnsandboxedAllowed:
	default:
		return fmt.Errorf("%w: policy.sandboxPolicy must be %q or %q", errInvalidDocument, SandboxPolicySandboxed, SandboxPolicyUnsandboxedAllowed)
	}
	return nil
}

func (limits SubjectLimits) validate() error {
	if limits.MaxSteps < 0 || limits.MaxToolCallsPerStep < 0 || limits.MaxAssistantBytes < 0 {
		return fmt.Errorf("%w: limits must not be negative", errInvalidDocument)
	}
	if limits.ApprovalTimeout < 0 {
		return fmt.Errorf("%w: limits.approvalTimeout must not be negative", errInvalidDocument)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Executor (design §11)
// ---------------------------------------------------------------------------

// ExecutorKind is design §4's execution surface: `in_process` or
// `acp_subprocess`.
type ExecutorKind string

const (
	ExecutorInProcess     ExecutorKind = "in_process"
	ExecutorACPSubprocess ExecutorKind = "acp_subprocess"
)

// Executor is the frozen `och.eval.executor` document (design §11).
// Executor identity is independent of Subject identity.
type Executor struct {
	FormatVersion int          `json:"formatVersion"`
	Schema        string       `json:"schema"`
	ID            ExecutorID   `json:"id"`
	Kind          ExecutorKind `json:"kind"`

	OCHRevision                string `json:"ochRevision"`
	EvalBuildRevision          string `json:"evalBuildRevision"`
	CompositionContractVersion string `json:"compositionContractVersion"`

	// ACPSubprocess is populated only when Kind is ExecutorACPSubprocess.
	ACPSubprocess *ACPSubprocessIdentity `json:"acpSubprocess,omitempty"`

	// Capabilities are the Scenario-declared capability names this
	// Executor snapshot supports. Matrix expansion (design §9, not yet
	// implemented) rejects a Cell whose Scenario requires a capability
	// absent here.
	Capabilities []string `json:"capabilities,omitempty"`
}

// ACPSubprocessIdentity is the additional identity an `acp_subprocess`
// Executor records (design §11): the exact binary hash, credential-free
// normalized argv, protocol version, and the agent name/version reported by
// `initialize`.
type ACPSubprocessIdentity struct {
	BinarySHA256    string   `json:"binarySha256"`
	NormalizedArgv  []string `json:"normalizedArgv"`
	ProtocolVersion string   `json:"protocolVersion"`
	AgentName       string   `json:"agentName"`
	AgentVersion    string   `json:"agentVersion"`
}

// DecodeExecutor strictly decodes and validates one `och.eval.executor`
// document (design §6).
func DecodeExecutor(data []byte) (Executor, error) {
	var executor Executor
	if err := decodeStrict(data, &executor); err != nil {
		return Executor{}, fmt.Errorf("eval: executor: %w", err)
	}
	if executor.Schema != SchemaExecutor {
		return Executor{}, fmt.Errorf("eval: executor: %w: %q", errUnsupportedSchema, executor.Schema)
	}
	if executor.FormatVersion != FormatVersion {
		return Executor{}, fmt.Errorf("eval: executor: %w: %d", errUnsupportedFormatVersion, executor.FormatVersion)
	}
	if err := executor.Validate(); err != nil {
		return Executor{}, err
	}
	return executor, nil
}

// Validate checks every field design §11 requires.
func (executor Executor) Validate() error {
	if _, err := ParseExecutorID(string(executor.ID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !hasText(executor.OCHRevision) {
		return fmt.Errorf("%w: ochRevision is required", errInvalidDocument)
	}
	if !hasText(executor.EvalBuildRevision) {
		return fmt.Errorf("%w: evalBuildRevision is required", errInvalidDocument)
	}
	if !hasText(executor.CompositionContractVersion) {
		return fmt.Errorf("%w: compositionContractVersion is required", errInvalidDocument)
	}
	switch executor.Kind {
	case ExecutorInProcess:
		if executor.ACPSubprocess != nil {
			return fmt.Errorf("%w: kind %q must not carry an acpSubprocess identity", errInvalidDocument, executor.Kind)
		}
	case ExecutorACPSubprocess:
		if executor.ACPSubprocess == nil {
			return fmt.Errorf("%w: kind %q requires an acpSubprocess identity", errInvalidDocument, executor.Kind)
		}
		if err := executor.ACPSubprocess.validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unknown executor kind %q", errInvalidDocument, executor.Kind)
	}
	if err := requireNonEmptyEntries("capabilities", executor.Capabilities); err != nil {
		return err
	}
	if dup := firstDuplicate(executor.Capabilities); dup != "" {
		return fmt.Errorf("%w: capabilities contains duplicate %q", errInvalidDocument, dup)
	}
	return nil
}

func (identity ACPSubprocessIdentity) validate() error {
	if !sha256HexPattern.MatchString(identity.BinarySHA256) {
		return fmt.Errorf("%w: acpSubprocess.binarySha256 must be 64 lowercase hex characters", errInvalidDocument)
	}
	if err := requireNonEmptyEntries("acpSubprocess.normalizedArgv", identity.NormalizedArgv); err != nil {
		return err
	}
	if len(identity.NormalizedArgv) == 0 {
		return fmt.Errorf("%w: acpSubprocess.normalizedArgv must not be empty", errInvalidDocument)
	}
	if !hasText(identity.ProtocolVersion) {
		return fmt.Errorf("%w: acpSubprocess.protocolVersion is required", errInvalidDocument)
	}
	if !hasText(identity.AgentName) {
		return fmt.Errorf("%w: acpSubprocess.agentName is required", errInvalidDocument)
	}
	if !hasText(identity.AgentVersion) {
		return fmt.Errorf("%w: acpSubprocess.agentVersion is required", errInvalidDocument)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared decode/validation helpers
// ---------------------------------------------------------------------------

// decodeStrict rejects a duplicate JSON object key at any depth, then
// decodes with unknown top-level and nested fields rejected, then rejects
// trailing data after the value (design §6: "reject duplicate keys and
// unknown fields").
func decodeStrict(data []byte, target any) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if decoder.More() {
		return errors.New("decode: trailing data after JSON value")
	}
	return nil
}

// rejectDuplicateJSONKeys walks data recursively and fails if any JSON
// object in it, at any depth, repeats a key. encoding/json accepts a
// duplicate key silently (last value wins); design §6 requires eval's own
// documents to reject it instead.
func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := rejectDuplicateJSONKeysValue(decoder); err != nil {
		return err
	}
	return nil
}

func rejectDuplicateJSONKeysValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("invalid JSON object key: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid JSON object key type")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("JSON object contains a duplicate key %q", key)
			}
			seen[key] = struct{}{}
			if err := rejectDuplicateJSONKeysValue(decoder); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil { // consume '}'
			return fmt.Errorf("invalid JSON object: %w", err)
		}
	case json.Delim('['):
		for decoder.More() {
			if err := rejectDuplicateJSONKeysValue(decoder); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil { // consume ']'
			return fmt.Errorf("invalid JSON array: %w", err)
		}
	}
	return nil
}

func hasText(value string) bool {
	return len(bytes.TrimSpace([]byte(value))) > 0
}

func requireNonEmptyEntries(field string, entries []string) error {
	for _, entry := range entries {
		if !hasText(entry) {
			return fmt.Errorf("%w: %s entries must not be blank", errInvalidDocument, field)
		}
	}
	return nil
}

func stringSetOverlap(first, second []string) string {
	seen := make(map[string]struct{}, len(first))
	for _, entry := range first {
		seen[entry] = struct{}{}
	}
	for _, entry := range second {
		if _, ok := seen[entry]; ok {
			return entry
		}
	}
	return ""
}

func firstDuplicate(entries []string) string {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, ok := seen[entry]; ok {
			return entry
		}
		seen[entry] = struct{}{}
	}
	return ""
}
