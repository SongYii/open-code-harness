// Package toolpolicy is the experimental, standard-library-only tool
// authorization contract. Source compatibility is not yet promised; pin a
// tested revision. Policies are trusted, startup-composed Go code, not a
// sandbox. The core guard remains the final authorization authority.
package toolpolicy

import (
	"context"
	"encoding/json"
)

const (
	DefaultID           = "builtin"
	MaxConfigBytes      = 64 * 1024
	MaxRuleIDBytes      = 128
	MaxReasonBytes      = 256
	MaxPathLiteralBytes = 4096
)

type Risk string

const (
	RiskRead  Risk = "read"
	RiskWrite Risk = "write"
	RiskExec  Risk = "exec"
)

type Effect string

const (
	EffectAllow           Effect = "allow"
	EffectDeny            Effect = "deny"
	EffectRequireApproval Effect = "require_approval"
)

// Input is detached metadata. It contains no arguments object, content,
// event, storage, provider, approval, or execution capability.
type Input struct {
	Name        string
	Risk        Risk
	Mutates     bool
	WorkspaceIn bool
	PathLiteral string
}

type Decision struct {
	Effect Effect
	RuleID string
	Reason string
}

// Policy must be deterministic, concurrency-safe, cooperative with context
// cancellation, and perform no I/O or background work. The host cannot stop
// arbitrary in-process Go code.
type Policy interface {
	Decide(context.Context, Input) (Decision, error)
}

// Registration is local to one launcher invocation. Factory validates its
// nonsecret JSON configuration before the host opens durable resources.
type Registration struct {
	ID      string
	Version string
	Factory func(json.RawMessage) (Policy, error)
}
