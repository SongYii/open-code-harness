package policy

import (
	"fmt"
	"strings"

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
	ReasonInWorkspace    = "in_workspace"
	ReasonOutOfWorkspace = "out_of_workspace"
	ReasonNetworkDenied  = "network_denied"
	ReasonUnknownRisk    = "unknown_risk"
	ReasonEmptyName      = "empty_name"
	ReasonDenyAll        = "deny_all"
	ReasonAllowAll       = "test_allow_all"
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
)

type Input struct {
	Name        string
	Risk        domain.RiskClass
	Mutates     bool // unused; table is Risk × workspace × mode
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
	Decide(Input) (Decision, error)
}

type tableEngine struct {
	mode Mode
}

func New(mode Mode) (Engine, error) {
	switch mode {
	case ModeDefault, ModeReadOnly, ModeAllowWrites, ModeDenyAll:
		return tableEngine{mode: mode}, nil
	default:
		return nil, fmt.Errorf("unknown policy mode %q", mode)
	}
}

func (engine tableEngine) Decide(input Input) (Decision, error) {
	if strings.TrimSpace(input.Name) == "" {
		return Decision{Effect: EffectDeny, RuleID: RuleEmptyName, Reason: ReasonEmptyName}, nil
	}
	if input.Network || input.Risk == domain.RiskNetwork {
		return Decision{Effect: EffectDeny, RuleID: RuleNetworkDenied, Reason: ReasonNetworkDenied}, nil
	}
	if !knownWorkspaceRisk(input.Risk) {
		return Decision{Effect: EffectDeny, RuleID: RuleUnknownRisk, Reason: ReasonUnknownRisk}, nil
	}
	if !input.WorkspaceIn {
		return Decision{Effect: EffectDeny, RuleID: RuleOutOfWorkspace, Reason: ReasonOutOfWorkspace}, nil
	}
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

func (allowAllEngine) Decide(Input) (Decision, error) {
	return Decision{Effect: EffectAllow, RuleID: RuleAllowAll, Reason: ReasonAllowAll}, nil
}
