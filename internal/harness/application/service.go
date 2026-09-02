package application

import (
	"reflect"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

const (
	DefaultMaxAssistantBytes             = 1 << 20
	DefaultTerminalCommitTimeout         = 5 * time.Second
	DefaultAppendResolutionTimeout       = 5 * time.Second
	DefaultAppendResolutionMaxOperations = 4
	DefaultMaxSteps                      = 8
	DefaultMaxToolCallsPerStep           = 8
	DefaultApprovalTimeout               = 30 * time.Second
	DefaultExecTimeout                   = 30 * time.Second
	MaxExecTimeout                       = 120 * time.Second
	MaxProjectionBytes                   = 4 << 20
	MaxToolResultBytes                   = 64 << 10
	loggedEnvelopeToolSchemaSlack        = 64 << 10
)

type Config struct {
	MaxAssistantBytes             int
	TerminalCommitTimeout         time.Duration
	AppendResolutionTimeout       time.Duration
	AppendResolutionMaxOperations uint32
	RequestIdentity               *engine.RequestIdentity
	MaxSteps                      int
	MaxToolCallsPerStep           int
	ApprovalTimeout               time.Duration
	PolicyMode                    policy.Mode
	Catalog                       *tools.Catalog
	Files                         tools.FileSystem
	Commands                      tools.CommandRunner
	Approver                      tools.Approver
	Context                       ContextConfig
}

// ContextConfig configures the Context Engine (design 2026-09-01). The
// zero value (Enabled: false) is the default: RunTurn keeps its pre-
// Context-Engine admission and dispatch behavior byte-for-byte, so no
// existing caller of this package needs to change anything. The real
// composition root (implementation plan Task 15) is what turns this on by
// default once Tasks 10-14 land; until then it exists for direct Service
// construction to opt into early (this package's own tests, and Task 9
// Step 2's own new tests).
type ContextConfig struct {
	Enabled         bool
	Budget          contextengine.Budget
	Meter           contextengine.Meter
	Summarizer      ContextSummarizer
	CheckpointStore ContextCheckpointStore
	// PageLimit bounds each Scan page; zero uses PrepareContext's own default.
	PageLimit uint32
	// MaxOverflowRecoveriesPerTurn bounds design §15.3/§19's per-Turn
	// Provider overflow recovery count. Zero uses
	// DefaultMaxOverflowRecoveriesPerTurn (2); values above
	// MaxOverflowRecoveriesPerTurnCap (3) are rejected by NewService.
	MaxOverflowRecoveriesPerTurn uint32
	// CompactionTimeout bounds one summarizer call within a compaction
	// bracket (design §21's own config surface; distinct from
	// ContextOrchestratorDeps.CleanupTimeout, which bounds the *cleanup*
	// append issued after a bracket must close). Zero uses
	// DefaultCompactionTimeout (2 minutes). The 5s-10min range design §8's
	// table documents is composition's own responsibility to enforce
	// before any resource is constructed; NewService only rejects a
	// negative value here.
	CompactionTimeout time.Duration
}

// DefaultMaxOverflowRecoveriesPerTurn and MaxOverflowRecoveriesPerTurnCap
// are design §19's own default (2) and maximum (3) for
// ContextConfig.MaxOverflowRecoveriesPerTurn.
const (
	DefaultMaxOverflowRecoveriesPerTurn = 2
	MaxOverflowRecoveriesPerTurnCap     = 3
)

// DefaultCompactionTimeout is design §8's own default for
// ContextConfig.CompactionTimeout.
const DefaultCompactionTimeout = 2 * time.Minute

func DefaultConfig() Config {
	return Config{
		MaxAssistantBytes:             DefaultMaxAssistantBytes,
		TerminalCommitTimeout:         DefaultTerminalCommitTimeout,
		AppendResolutionTimeout:       DefaultAppendResolutionTimeout,
		AppendResolutionMaxOperations: DefaultAppendResolutionMaxOperations,
		MaxSteps:                      DefaultMaxSteps,
		MaxToolCallsPerStep:           DefaultMaxToolCallsPerStep,
		ApprovalTimeout:               DefaultApprovalTimeout,
		PolicyMode:                    policy.ModeDefault,
	}
}

// Service is the single application command authority for Session and Turn
// use cases. Its dependencies and configuration are immutable after creation.
// AuthoritySource is the exception that proves the rule: the field itself
// does not change, but CurrentAuthority is read per append so an
// expired-takeover fencing-token rotation is visible without rebuilding.
type Service struct {
	store      EventStore
	ids        IDGenerator
	clock      Clock
	runner     *engine.TurnRunner
	authority  AuthoritySource
	config     Config
	executions *executionRegistry
	policy     policy.Engine
	catalog    *tools.Catalog
	files      tools.FileSystem
	commands   tools.CommandRunner
	approver   tools.Approver
}

func NewService(store EventStore, ids IDGenerator, clock Clock, runner *engine.TurnRunner, authority AuthoritySource, config Config) (*Service, error) {
	if config.AppendResolutionTimeout == 0 {
		config.AppendResolutionTimeout = DefaultAppendResolutionTimeout
	}
	if config.AppendResolutionMaxOperations == 0 {
		config.AppendResolutionMaxOperations = DefaultAppendResolutionMaxOperations
	}
	if config.MaxSteps == 0 {
		config.MaxSteps = DefaultMaxSteps
	}
	if config.MaxToolCallsPerStep == 0 {
		config.MaxToolCallsPerStep = DefaultMaxToolCallsPerStep
	}
	if config.ApprovalTimeout == 0 {
		config.ApprovalTimeout = DefaultApprovalTimeout
	}
	if config.PolicyMode == "" {
		config.PolicyMode = policy.ModeDefault
	}
	if isNilValue(store) || isNilValue(ids) || isNilValue(clock) || runner == nil || isNilValue(authority) || authority.CurrentAuthority().Validate() != nil || config.MaxAssistantBytes <= 0 || config.TerminalCommitTimeout <= 0 || config.AppendResolutionTimeout <= 0 || config.AppendResolutionMaxOperations == 0 || config.MaxSteps < 1 || config.MaxToolCallsPerStep < 1 {
		return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
	}
	if config.RequestIdentity != nil {
		copied := *config.RequestIdentity
		if err := copied.Validate(); err != nil {
			return nil, applicationError(CategoryValidation, "invalid_configuration", false, err)
		}
		config.RequestIdentity = &copied
	}
	policyEngine, err := policy.New(config.PolicyMode)
	if err != nil {
		return nil, applicationError(CategoryValidation, "invalid_configuration", false, err)
	}
	catalogEnabled := catalogHasSpecs(config.Catalog)
	if err := validateToolComposition(config, catalogEnabled); err != nil {
		return nil, err
	}
	if config.Context.Enabled {
		if isNilValue(config.Context.Summarizer) || isNilValue(config.Context.CheckpointStore) || isNilValue(config.Context.Meter) || config.Context.Budget.HardInput == 0 {
			return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
		}
		if config.Context.MaxOverflowRecoveriesPerTurn == 0 {
			config.Context.MaxOverflowRecoveriesPerTurn = DefaultMaxOverflowRecoveriesPerTurn
		}
		if config.Context.MaxOverflowRecoveriesPerTurn > MaxOverflowRecoveriesPerTurnCap {
			return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
		}
		if config.Context.CompactionTimeout == 0 {
			config.Context.CompactionTimeout = DefaultCompactionTimeout
		}
		if config.Context.CompactionTimeout < 0 {
			return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
		}
	}
	approver := config.Approver
	if isNilValue(approver) {
		approver = tools.DenyApprover{}
	}
	service := &Service{
		store: store, ids: ids, clock: clock, runner: runner, authority: authority,
		config: config, executions: newExecutionRegistry(), policy: policyEngine,
		approver: approver,
	}
	if catalogEnabled {
		service.catalog = config.Catalog
		service.files = config.Files
		service.commands = config.Commands
	}
	return service, nil
}

func catalogHasSpecs(catalog *tools.Catalog) bool {
	return catalog != nil && len(catalog.Specs()) > 0
}

func validateToolComposition(config Config, catalogEnabled bool) error {
	nativeTools := engine.CapabilityUnsupported
	if config.RequestIdentity != nil {
		nativeTools = config.RequestIdentity.Profile.NativeTools
	}
	if nativeTools == engine.CapabilityRequired && !catalogEnabled {
		return applicationError(CategoryPolicy, "invalid_configuration", false, nil)
	}
	if !catalogEnabled {
		return nil
	}
	if config.RequestIdentity == nil {
		return applicationError(CategoryPolicy, "invalid_configuration", false, nil)
	}
	if nativeTools != engine.CapabilitySupported && nativeTools != engine.CapabilityRequired {
		return applicationError(CategoryPolicy, "invalid_configuration", false, nil)
	}
	needsFiles, needsCommands := catalogPortNeeds(config.Catalog.Specs())
	if needsFiles && isNilValue(config.Files) {
		return applicationError(CategoryPolicy, "invalid_configuration", false, nil)
	}
	if needsCommands && isNilValue(config.Commands) {
		return applicationError(CategoryPolicy, "invalid_configuration", false, nil)
	}
	return nil
}

func catalogPortNeeds(specs []domain.ToolSpec) (needsFiles, needsCommands bool) {
	for _, spec := range specs {
		switch spec.Risk {
		case domain.RiskRead, domain.RiskWrite:
			needsFiles = true
		case domain.RiskExec:
			needsFiles = true
			needsCommands = true
		}
	}
	return needsFiles, needsCommands
}

func (service *Service) appendResolutionConfig() AppendResolutionConfig {
	return AppendResolutionConfig{Timeout: service.config.AppendResolutionTimeout, MaxOperations: service.config.AppendResolutionMaxOperations}
}

func (service *Service) contextEnabled() bool {
	return service != nil && service.config.Context.Enabled
}

func (service *Service) maxOverflowRecoveriesPerTurn() uint32 {
	return service.config.Context.MaxOverflowRecoveriesPerTurn
}

func (service *Service) contextOrchestratorDeps() ContextOrchestratorDeps {
	return ContextOrchestratorDeps{
		Store: service.store, IDs: service.ids, Clock: service.clock, Authority: service.authority,
		CheckpointStore: service.config.Context.CheckpointStore, Summarizer: service.config.Context.Summarizer,
		Meter: service.config.Context.Meter, Budget: service.config.Context.Budget, PageLimit: service.config.Context.PageLimit,
		SummarizeTimeout: service.config.Context.CompactionTimeout, Identity: service.config.RequestIdentity,
	}
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
