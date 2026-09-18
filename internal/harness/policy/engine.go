package policy

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type Effect string

const (
	EffectAllow           Effect = "allow"
	EffectDeny            Effect = "deny"
	EffectRequireApproval Effect = "require_approval"
)

type Mode string

const (
	ModeDefault     Mode = "default"
	ModeReadOnly    Mode = "read_only"
	ModeAllowWrites Mode = "allow_writes"
	ModeDenyAll     Mode = "deny_all"
)

const (
	ReasonInWorkspace     = "in_workspace"
	ReasonOutOfWorkspace  = "out_of_workspace"
	ReasonNetworkDenied   = "network_denied"
	ReasonUnknownRisk     = "unknown_risk"
	ReasonEmptyName       = "empty_name"
	ReasonDenyAll         = "deny_all"
	ReasonAllowAll        = "test_allow_all"
	ReasonInvalidMetadata = "invalid_metadata"
)

const (
	RuleEmptyName                       = "empty_name"
	RuleUnknownRisk                     = "unknown_risk"
	RuleNetworkDenied                   = "network_denied"
	RuleOutOfWorkspace                  = "out_of_workspace"
	RuleDefaultReadAllow                = "default.read_allow"
	RuleDefaultWriteRequiresApproval    = "default.write_requires_approval"
	RuleDefaultExecRequiresApproval     = "default.exec_requires_approval"
	RuleReadOnlyReadAllow               = "read_only.read_allow"
	RuleReadOnlyWriteDenied             = "read_only.write_denied"
	RuleReadOnlyExecDenied              = "read_only.exec_denied"
	RuleAllowWritesReadAllow            = "allow_writes.read_allow"
	RuleAllowWritesWriteAllow           = "allow_writes.write_allow"
	RuleAllowWritesExecRequiresApproval = "allow_writes.exec_requires_approval"
	RuleDenyAllDenied                   = "deny_all.denied"
	RuleAllowAll                        = "allow_all"
	RuleInvalidMetadata                 = "invalid_metadata"
)

const (
	maxRuleIDBytes      = 128
	maxReasonBytes      = 256
	maxPathLiteralBytes = 4096
)

type Input struct {
	Name        string
	Risk        domain.RiskClass
	Mutates     bool // checked against Risk before the strategy runs
	WorkspaceIn bool
	Network     bool
	PathLiteral string // audit-only; not used to re-do I/O
}

type Decision struct {
	Effect Effect
	RuleID string
	Reason string
}

type Engine interface {
	Decide(context.Context, Input) (Decision, error)
}

type tableEngine struct {
	mode Mode
}

func New(mode Mode) (Engine, error) {
	switch mode {
	case ModeDefault, ModeReadOnly, ModeAllowWrites, ModeDenyAll:
		return Guard(tableEngine{mode: mode})
	default:
		return nil, fmt.Errorf("unknown policy mode %q", mode)
	}
}

// Guard turns a decision strategy into the final authorization authority. Core
// denials run before the strategy and strategy output is validated afterwards,
// so a replacement strategy can tighten policy but cannot bypass invariants.
func Guard(strategy Engine) (Engine, error) {
	if isNilEngine(strategy) {
		return nil, fmt.Errorf("policy: strategy is required")
	}
	return guardedEngine{strategy: strategy}, nil
}

func isNilEngine(engine Engine) bool {
	if engine == nil {
		return true
	}
	value := reflect.ValueOf(engine)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

type guardedEngine struct {
	strategy Engine
}

func (engine guardedEngine) Decide(ctx context.Context, input Input) (Decision, error) {
	if decision, denied := coreDeny(input); denied {
		return decision, nil
	}
	decision, err := engine.strategy.Decide(ctx, input)
	if err != nil {
		return Decision{}, fmt.Errorf("policy: strategy decision: %w", err)
	}
	if !validDecision(decision) {
		return Decision{}, fmt.Errorf("policy: strategy returned an invalid decision")
	}
	return decision, nil
}

func coreDeny(input Input) (Decision, bool) {
	if strings.TrimSpace(input.Name) == "" {
		return Decision{Effect: EffectDeny, RuleID: RuleEmptyName, Reason: ReasonEmptyName}, true
	}
	if len(input.PathLiteral) > maxPathLiteralBytes || !utf8.ValidString(input.PathLiteral) {
		return Decision{Effect: EffectDeny, RuleID: RuleInvalidMetadata, Reason: ReasonInvalidMetadata}, true
	}
	if input.Network || input.Risk == domain.RiskNetwork {
		return Decision{Effect: EffectDeny, RuleID: RuleNetworkDenied, Reason: ReasonNetworkDenied}, true
	}
	if !knownWorkspaceRisk(input.Risk) {
		return Decision{Effect: EffectDeny, RuleID: RuleUnknownRisk, Reason: ReasonUnknownRisk}, true
	}
	if input.Mutates != (input.Risk == domain.RiskWrite || input.Risk == domain.RiskExec) {
		return Decision{Effect: EffectDeny, RuleID: RuleUnknownRisk, Reason: ReasonUnknownRisk}, true
	}
	if !input.WorkspaceIn {
		return Decision{Effect: EffectDeny, RuleID: RuleOutOfWorkspace, Reason: ReasonOutOfWorkspace}, true
	}
	return Decision{}, false
}

func validDecision(decision Decision) bool {
	switch decision.Effect {
	case EffectAllow, EffectDeny, EffectRequireApproval:
	default:
		return false
	}
	return strings.TrimSpace(decision.RuleID) != "" && strings.TrimSpace(decision.Reason) != "" &&
		utf8.ValidString(decision.RuleID) && utf8.ValidString(decision.Reason) &&
		len(decision.RuleID) <= maxRuleIDBytes && len(decision.Reason) <= maxReasonBytes
}

func (engine tableEngine) Decide(_ context.Context, input Input) (Decision, error) {
	return engine.decideInWorkspace(input.Risk), nil
}

func knownWorkspaceRisk(risk domain.RiskClass) bool {
	switch risk {
	case domain.RiskRead, domain.RiskWrite, domain.RiskExec:
		return true
	default:
		return false
	}
}

func (engine tableEngine) decideInWorkspace(risk domain.RiskClass) Decision {
	switch engine.mode {
	case ModeDenyAll:
		return Decision{Effect: EffectDeny, RuleID: RuleDenyAllDenied, Reason: ReasonDenyAll}
	case ModeDefault:
		switch risk {
		case domain.RiskRead:
			return Decision{Effect: EffectAllow, RuleID: RuleDefaultReadAllow, Reason: ReasonInWorkspace}
		case domain.RiskWrite:
			return Decision{Effect: EffectRequireApproval, RuleID: RuleDefaultWriteRequiresApproval, Reason: ReasonInWorkspace}
		case domain.RiskExec:
			return Decision{Effect: EffectRequireApproval, RuleID: RuleDefaultExecRequiresApproval, Reason: ReasonInWorkspace}
		}
	case ModeReadOnly:
		switch risk {
		case domain.RiskRead:
			return Decision{Effect: EffectAllow, RuleID: RuleReadOnlyReadAllow, Reason: ReasonInWorkspace}
		case domain.RiskWrite:
			return Decision{Effect: EffectDeny, RuleID: RuleReadOnlyWriteDenied, Reason: ReasonInWorkspace}
		case domain.RiskExec:
			return Decision{Effect: EffectDeny, RuleID: RuleReadOnlyExecDenied, Reason: ReasonInWorkspace}
		}
	case ModeAllowWrites:
		switch risk {
		case domain.RiskRead:
			return Decision{Effect: EffectAllow, RuleID: RuleAllowWritesReadAllow, Reason: ReasonInWorkspace}
		case domain.RiskWrite:
			return Decision{Effect: EffectAllow, RuleID: RuleAllowWritesWriteAllow, Reason: ReasonInWorkspace}
		case domain.RiskExec:
			return Decision{Effect: EffectRequireApproval, RuleID: RuleAllowWritesExecRequiresApproval, Reason: ReasonInWorkspace}
		}
	}
	return Decision{Effect: EffectDeny, RuleID: RuleUnknownRisk, Reason: ReasonUnknownRisk}
}

// AllowAll is a test constructor. Production composition must not call it.
func AllowAll() Engine {
	return allowAllEngine{}
}

type allowAllEngine struct{}

func (allowAllEngine) Decide(context.Context, Input) (Decision, error) {
	return Decision{Effect: EffectAllow, RuleID: RuleAllowAll, Reason: ReasonAllowAll}, nil
}
