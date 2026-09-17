package policy

import (
	"errors"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type scriptedEngine struct {
	decision Decision
	err      error
	calls    int
	input    Input
}

func (engine *scriptedEngine) Decide(input Input) (Decision, error) {
	engine.calls++
	engine.input = input
	return engine.decision, engine.err
}

func TestNewAcceptsShippedModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []Mode{ModeDefault, ModeReadOnly, ModeAllowWrites, ModeDenyAll} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			engine, err := New(mode)
			if err != nil {
				t.Fatalf("New(%q) error = %v", mode, err)
			}
			if engine == nil {
				t.Fatalf("New(%q) = nil engine", mode)
			}
		})
	}
}

func TestNewRejectsUnknownMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []Mode{"", "allow_all", "bypass", "yolo", "unknown"} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			engine, err := New(mode)
			if err == nil {
				t.Fatalf("New(%q) error = nil, want unknown mode", mode)
			}
			if engine != nil {
				t.Fatalf("New(%q) engine = %#v, want nil", mode, engine)
			}
		})
	}
}

func TestDecideDefaultTable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mode   Mode
		input  Input
		effect Effect
		rule   string
		reason string
	}{
		{
			name:   "default read_file in workspace",
			mode:   ModeDefault,
			input:  Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true},
			effect: EffectAllow, rule: RuleDefaultReadAllow, reason: ReasonInWorkspace,
		},
		{
			name:   "default list_dir in workspace",
			mode:   ModeDefault,
			input:  Input{Name: "list_dir", Risk: domain.RiskRead, WorkspaceIn: true},
			effect: EffectAllow, rule: RuleDefaultReadAllow, reason: ReasonInWorkspace,
		},
		{
			name:   "default write_file in workspace",
			mode:   ModeDefault,
			input:  Input{Name: "write_file", Risk: domain.RiskWrite, Mutates: true, WorkspaceIn: true},
			effect: EffectRequireApproval, rule: RuleDefaultWriteRequiresApproval, reason: ReasonInWorkspace,
		},
		{
			name:   "default exec in workspace",
			mode:   ModeDefault,
			input:  Input{Name: "exec", Risk: domain.RiskExec, Mutates: true, WorkspaceIn: true},
			effect: EffectRequireApproval, rule: RuleDefaultExecRequiresApproval, reason: ReasonInWorkspace,
		},
		{
			name:   "default read out of workspace",
			mode:   ModeDefault,
			input:  Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: false, PathLiteral: "/etc/passwd"},
			effect: EffectDeny, rule: RuleOutOfWorkspace, reason: ReasonOutOfWorkspace,
		},
		{
			name:   "default write out of workspace",
			mode:   ModeDefault,
			input:  Input{Name: "write_file", Risk: domain.RiskWrite, Mutates: true, WorkspaceIn: false},
			effect: EffectDeny, rule: RuleOutOfWorkspace, reason: ReasonOutOfWorkspace,
		},
		{
			name:   "default exec out of workspace",
			mode:   ModeDefault,
			input:  Input{Name: "exec", Risk: domain.RiskExec, Mutates: true, WorkspaceIn: false},
			effect: EffectDeny, rule: RuleOutOfWorkspace, reason: ReasonOutOfWorkspace,
		},
		{
			name:   "read_only write in workspace",
			mode:   ModeReadOnly,
			input:  Input{Name: "write_file", Risk: domain.RiskWrite, Mutates: true, WorkspaceIn: true},
			effect: EffectDeny, rule: RuleReadOnlyWriteDenied, reason: ReasonInWorkspace,
		},
		{
			name:   "read_only exec in workspace",
			mode:   ModeReadOnly,
			input:  Input{Name: "exec", Risk: domain.RiskExec, Mutates: true, WorkspaceIn: true},
			effect: EffectDeny, rule: RuleReadOnlyExecDenied, reason: ReasonInWorkspace,
		},
		{
			name:   "read_only read in workspace",
			mode:   ModeReadOnly,
			input:  Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true},
			effect: EffectAllow, rule: RuleReadOnlyReadAllow, reason: ReasonInWorkspace,
		},
		{
			name:   "deny_all read in workspace",
			mode:   ModeDenyAll,
			input:  Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true},
			effect: EffectDeny, rule: RuleDenyAllDenied, reason: ReasonDenyAll,
		},
		{
			name:   "network risk denies in every mode",
			mode:   ModeAllowWrites,
			input:  Input{Name: "fetch", Risk: domain.RiskNetwork, WorkspaceIn: true},
			effect: EffectDeny, rule: RuleNetworkDenied, reason: ReasonNetworkDenied,
		},
		{
			name:   "network flag denies in-workspace read",
			mode:   ModeDefault,
			input:  Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true, Network: true},
			effect: EffectDeny, rule: RuleNetworkDenied, reason: ReasonNetworkDenied,
		},
		{
			name:   "unknown risk denies",
			mode:   ModeDefault,
			input:  Input{Name: "mystery", Risk: domain.RiskClass("mystery"), WorkspaceIn: true},
			effect: EffectDeny, rule: RuleUnknownRisk, reason: ReasonUnknownRisk,
		},
		{
			name:   "empty name denies",
			mode:   ModeDefault,
			input:  Input{Name: "", Risk: domain.RiskRead, WorkspaceIn: true},
			effect: EffectDeny, rule: RuleEmptyName, reason: ReasonEmptyName,
		},
		{
			name:   "whitespace name denies",
			mode:   ModeAllowWrites,
			input:  Input{Name: "  ", Risk: domain.RiskWrite, Mutates: true, WorkspaceIn: true},
			effect: EffectDeny, rule: RuleEmptyName, reason: ReasonEmptyName,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertDecide(t, mustEngine(t, test.mode), test.input, test.effect, test.rule, test.reason)
		})
	}
}

func TestDecideAllowWritesEveryCell(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t, ModeAllowWrites)
	tests := []struct {
		name   string
		input  Input
		effect Effect
		rule   string
		reason string
	}{
		{
			name:   "read in workspace",
			input:  Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true},
			effect: EffectAllow, rule: RuleAllowWritesReadAllow, reason: ReasonInWorkspace,
		},
		{
			name:   "list_dir in workspace",
			input:  Input{Name: "list_dir", Risk: domain.RiskRead, WorkspaceIn: true},
			effect: EffectAllow, rule: RuleAllowWritesReadAllow, reason: ReasonInWorkspace,
		},
		{
			name:   "read out of workspace",
			input:  Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: false, PathLiteral: "../etc/passwd"},
			effect: EffectDeny, rule: RuleOutOfWorkspace, reason: ReasonOutOfWorkspace,
		},
		{
			name:   "write in workspace",
			input:  Input{Name: "write_file", Risk: domain.RiskWrite, Mutates: true, WorkspaceIn: true},
			effect: EffectAllow, rule: RuleAllowWritesWriteAllow, reason: ReasonInWorkspace,
		},
		{
			name:   "write out of workspace",
			input:  Input{Name: "write_file", Risk: domain.RiskWrite, Mutates: true, WorkspaceIn: false},
			effect: EffectDeny, rule: RuleOutOfWorkspace, reason: ReasonOutOfWorkspace,
		},
		{
			name:   "exec in workspace still asks",
			input:  Input{Name: "exec", Risk: domain.RiskExec, Mutates: true, WorkspaceIn: true},
			effect: EffectRequireApproval, rule: RuleAllowWritesExecRequiresApproval, reason: ReasonInWorkspace,
		},
		{
			name:   "exec out of workspace",
			input:  Input{Name: "exec", Risk: domain.RiskExec, Mutates: true, WorkspaceIn: false},
			effect: EffectDeny, rule: RuleOutOfWorkspace, reason: ReasonOutOfWorkspace,
		},
		{
			name:   "network in workspace",
			input:  Input{Name: "fetch", Risk: domain.RiskNetwork, WorkspaceIn: true},
			effect: EffectDeny, rule: RuleNetworkDenied, reason: ReasonNetworkDenied,
		},
		{
			name:   "network out of workspace",
			input:  Input{Name: "fetch", Risk: domain.RiskNetwork, WorkspaceIn: false},
			effect: EffectDeny, rule: RuleNetworkDenied, reason: ReasonNetworkDenied,
		},
		{
			name:   "network flag in workspace",
			input:  Input{Name: "exec", Risk: domain.RiskExec, Mutates: true, WorkspaceIn: true, Network: true},
			effect: EffectDeny, rule: RuleNetworkDenied, reason: ReasonNetworkDenied,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertDecide(t, engine, test.input, test.effect, test.rule, test.reason)
		})
	}
}

func TestDecideDoesNotInterpretPathLiteral(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t, ModeDefault)
	inside := Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true, PathLiteral: "/etc/passwd"}
	assertDecide(t, engine, inside, EffectAllow, RuleDefaultReadAllow, ReasonInWorkspace)
}

func TestAllowAllIsUnconditional(t *testing.T) {
	t.Parallel()
	engine := AllowAll()
	inputs := []Input{
		{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true},
		{Name: "write_file", Risk: domain.RiskWrite, Mutates: true, WorkspaceIn: true},
		{Name: "exec", Risk: domain.RiskExec, Mutates: true, WorkspaceIn: false},
		{Name: "fetch", Risk: domain.RiskNetwork, Network: true},
		{Name: "", Risk: domain.RiskClass("mystery")},
	}
	for _, input := range inputs {
		assertDecide(t, engine, input, EffectAllow, RuleAllowAll, ReasonAllowAll)
	}
}

// TestGuardRejectsCoreDenialsBeforeCallingStrategy protects the boundary that
// makes an authorization strategy advisory rather than authoritative. Removing
// any core branch must both call the allow strategy and change the decision.
func TestGuardRejectsCoreDenialsBeforeCallingStrategy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  Input
		rule   string
		reason string
	}{
		{name: "empty name", input: Input{Risk: domain.RiskRead, WorkspaceIn: true}, rule: RuleEmptyName, reason: ReasonEmptyName},
		{name: "network flag", input: Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true, Network: true}, rule: RuleNetworkDenied, reason: ReasonNetworkDenied},
		{name: "network risk", input: Input{Name: "fetch", Risk: domain.RiskNetwork, WorkspaceIn: true}, rule: RuleNetworkDenied, reason: ReasonNetworkDenied},
		{name: "unknown risk", input: Input{Name: "mystery", Risk: domain.RiskClass("invented"), WorkspaceIn: true}, rule: RuleUnknownRisk, reason: ReasonUnknownRisk},
		{name: "read marked mutating", input: Input{Name: "read_file", Risk: domain.RiskRead, Mutates: true, WorkspaceIn: true}, rule: RuleUnknownRisk, reason: ReasonUnknownRisk},
		{name: "write marked non-mutating", input: Input{Name: "write_file", Risk: domain.RiskWrite, WorkspaceIn: true}, rule: RuleUnknownRisk, reason: ReasonUnknownRisk},
		{name: "outside workspace", input: Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: false}, rule: RuleOutOfWorkspace, reason: ReasonOutOfWorkspace},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			strategy := &scriptedEngine{decision: Decision{Effect: EffectAllow, RuleID: "malicious.allow", Reason: "bypass"}}
			guarded, err := Guard(strategy)
			if err != nil {
				t.Fatalf("Guard: %v", err)
			}
			decision, err := guarded.Decide(test.input)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if decision != (Decision{Effect: EffectDeny, RuleID: test.rule, Reason: test.reason}) {
				t.Fatalf("decision = %#v, want core deny rule=%q reason=%q", decision, test.rule, test.reason)
			}
			if strategy.calls != 0 {
				t.Fatalf("unsafe input reached strategy %d times, want 0", strategy.calls)
			}
		})
	}
}

func TestGuardDelegatesValidDetachedInput(t *testing.T) {
	t.Parallel()
	wantInput := Input{Name: "write_file", Risk: domain.RiskWrite, Mutates: true, WorkspaceIn: true, PathLiteral: "out.txt"}
	wantDecision := Decision{Effect: EffectRequireApproval, RuleID: "custom.write", Reason: "operator_review"}
	strategy := &scriptedEngine{decision: wantDecision}
	guarded, err := Guard(strategy)
	if err != nil {
		t.Fatalf("Guard: %v", err)
	}
	got, err := guarded.Decide(wantInput)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got != wantDecision {
		t.Fatalf("decision = %#v, want %#v", got, wantDecision)
	}
	if strategy.calls != 1 || strategy.input != wantInput {
		t.Fatalf("strategy observed calls=%d input=%#v, want one detached input %#v", strategy.calls, strategy.input, wantInput)
	}
}

func TestGuardFailsClosedOnStrategyErrorsAndInvalidDecisions(t *testing.T) {
	t.Parallel()
	strategyFailure := errors.New("strategy failed")
	tests := []struct {
		name     string
		decision Decision
		err      error
	}{
		{name: "strategy error", err: strategyFailure},
		{name: "unknown effect", decision: Decision{Effect: Effect("invented"), RuleID: "custom.rule", Reason: "custom_reason"}},
		{name: "empty rule", decision: Decision{Effect: EffectAllow, Reason: "custom_reason"}},
		{name: "blank reason", decision: Decision{Effect: EffectAllow, RuleID: "custom.rule", Reason: "  "}},
		{name: "invalid UTF-8 rule", decision: Decision{Effect: EffectAllow, RuleID: string([]byte{0xff}), Reason: "custom_reason"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			guarded, err := Guard(&scriptedEngine{decision: test.decision, err: test.err})
			if err != nil {
				t.Fatalf("Guard: %v", err)
			}
			decision, err := guarded.Decide(Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true})
			if err == nil {
				t.Fatalf("Decide returned nil error and decision %#v", decision)
			}
			if decision != (Decision{}) {
				t.Fatalf("decision = %#v, want zero decision on failure", decision)
			}
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want wrapped strategy error %v", err, test.err)
			}
		})
	}
}

func TestGuardRejectsNilStrategies(t *testing.T) {
	t.Parallel()
	var typedNil *scriptedEngine
	for name, strategy := range map[string]Engine{"nil interface": nil, "typed nil": typedNil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			guarded, err := Guard(strategy)
			if err == nil || guarded != nil {
				t.Fatalf("Guard(nil) = (%#v, %v), want nil engine and error", guarded, err)
			}
		})
	}
}

func mustEngine(t *testing.T, mode Mode) Engine {
	t.Helper()
	engine, err := New(mode)
	if err != nil {
		t.Fatalf("New(%q) error = %v", mode, err)
	}
	return engine
}

func assertDecide(t *testing.T, engine Engine, input Input, effect Effect, rule, reason string) {
	t.Helper()
	decision, err := engine.Decide(input)
	if err != nil {
		t.Fatalf("Decide(%#v) error = %v, want nil", input, err)
	}
	if decision.Effect != effect || decision.RuleID != rule || decision.Reason != reason {
		t.Fatalf("Decide(%#v) = %#v, want effect=%q rule=%q reason=%q", input, decision, effect, rule, reason)
	}
}
