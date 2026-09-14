package contextengine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
)

type policyFunc func(context.Context, contextpolicy.Input) (contextpolicy.Decision, error)

func (f policyFunc) Plan(ctx context.Context, input contextpolicy.Input) (contextpolicy.Decision, error) {
	return f(ctx, input)
}

func TestPolicyDefaultParityAndCandidateSafety(t *testing.T) {
	units, err := ProjectSourceEvents(buildManyTurns(20))
	if err != nil {
		t.Fatal(err)
	}
	input := PlanInput{Units: units, Budget: fixtureBudget(t, 8192, 1024), Meter: WireEstimateMeter{}}
	want, err := SelectCutPoint(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Plan(context.Background(), nil, input, "pre_turn", 0, 0)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("default changed: %v", err)
	}
	var offered []contextpolicy.Candidate
	custom := policyFunc(func(_ context.Context, in contextpolicy.Input) (contextpolicy.Decision, error) {
		offered = append([]contextpolicy.Candidate(nil), in.Candidates...)
		return contextpolicy.Decision{}, nil
	})
	input.Budget.HardInput = 1_000_000 // automatic below hard can delay
	if _, err := Plan(context.Background(), custom, input, "pre_turn", 0, 0); err != nil {
		t.Fatal(err)
	}
	if len(offered) < 2 {
		t.Fatalf("need multiple safe candidates: %v", offered)
	}
	for _, candidate := range offered {
		custom = policyFunc(func(_ context.Context, in contextpolicy.Input) (contextpolicy.Decision, error) {
			return contextpolicy.Decision{Compact: true, CandidateID: candidate.ID}, nil
		})
		cut, err := Plan(context.Background(), custom, input, "mid_turn", 42, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(cut.CoveredUnits) > len(want.CoveredUnits) {
			t.Fatal("crossed core retention floor")
		}
		if cut.RetainedUnits[0].Kind != UnitKindTurn {
			t.Fatal("split turn/tool pair")
		}
		if len(cut.CoveredUnits)+len(cut.RetainedUnits) != len(units) {
			t.Fatal("lost units")
		}
	}
}

func policyValidationInput(t *testing.T) PlanInput {
	t.Helper()
	units, err := ProjectSourceEvents(buildManyTurns(20))
	if err != nil {
		t.Fatal(err)
	}
	input := PlanInput{Units: units, Budget: fixtureBudget(t, 8192, 1024), Meter: WireEstimateMeter{}}
	// Isolate explicit Force and candidate validation from hard-budget force.
	input.Budget.HardInput = 1_000_000
	return input
}

func requirePolicyRejection(t *testing.T, err error, reason string) {
	t.Helper()
	if !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), reason) {
		t.Fatalf("error = %v, want ErrPolicy with %q", err, reason)
	}
}

func TestPolicyForcedVetoIgnoresCallbackScalarMutation(t *testing.T) {
	for _, trigger := range []string{"manual", "overflow_retry", "mid_turn", "pre_turn"} {
		t.Run(trigger, func(t *testing.T) {
			input := policyValidationInput(t)
			input.Force = true
			policy := policyFunc(func(_ context.Context, in contextpolicy.Input) (contextpolicy.Decision, error) {
				if !in.Force || len(in.Candidates) == 0 {
					t.Fatal("fixture must offer a forced safe cut")
				}
				// Only the scalar copy changes; do not also forge candidate IDs.
				in.Force = false
				return contextpolicy.Decision{}, nil
			})
			_, err := Plan(context.Background(), policy, input, trigger, 0, 0)
			requirePolicyRejection(t, err, "forced compaction cannot be vetoed")
		})
	}
}

func TestPolicyRejectsForgedCandidateAfterSharedElementMutation(t *testing.T) {
	input := policyValidationInput(t)
	policy := policyFunc(func(_ context.Context, in contextpolicy.Input) (contextpolicy.Decision, error) {
		if in.Force || len(in.Candidates) == 0 {
			t.Fatal("fixture must offer an unforced safe cut")
		}
		const forged = 999999
		for _, candidate := range in.Candidates {
			if candidate.ID == forged {
				t.Fatal("forged ID was actually offered")
			}
		}
		// This changes shared array storage, but must not grant eligibility.
		in.Candidates[0].ID = forged
		return contextpolicy.Decision{Compact: true, CandidateID: forged}, nil
	})
	_, err := Plan(context.Background(), policy, input, "pre_turn", 0, 0)
	requirePolicyRejection(t, err, "candidate was not offered")
}

func TestPolicyAcceptsOriginalCandidateAfterSharedElementMutation(t *testing.T) {
	input := policyValidationInput(t)
	selectFirst := func(mutate bool) policyFunc {
		return func(_ context.Context, in contextpolicy.Input) (contextpolicy.Decision, error) {
			if in.Force || len(in.Candidates) == 0 {
				t.Fatal("fixture must offer an unforced safe cut")
			}
			original := in.Candidates[0].ID
			if mutate {
				for index := range in.Candidates {
					in.Candidates[index].ID = 999999 + uint64(index)
				}
			}
			return contextpolicy.Decision{Compact: true, CandidateID: original}, nil
		}
	}
	want, err := Plan(context.Background(), selectFirst(false), input, "pre_turn", 0, 0)
	if err != nil || !want.NeedsCompaction || len(want.CoveredUnits) == 0 {
		t.Fatalf("valid control: %v", err)
	}
	got, err := Plan(context.Background(), selectFirst(true), input, "pre_turn", 0, 0)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("callback mutation changed an originally legal cut: err=%v covered=%d/%d retained=%d/%d (got/want)",
			err, len(got.CoveredUnits), len(want.CoveredUnits), len(got.RetainedUnits), len(want.RetainedUnits))
	}
}

func TestPolicyHardBudgetVetoFailsClosed(t *testing.T) {
	input := policyValidationInput(t)
	input.Budget.HardInput = 1
	veto := policyFunc(func(context.Context, contextpolicy.Input) (contextpolicy.Decision, error) {
		return contextpolicy.Decision{}, nil
	})
	_, err := Plan(context.Background(), veto, input, "pre_turn", 0, 0)
	requirePolicyRejection(t, err, "forced compaction cannot be vetoed")
}

func TestPolicyMalformedDecisionAndCallbackErrorsFailClosed(t *testing.T) {
	input := policyValidationInput(t)
	malformed := policyFunc(func(context.Context, contextpolicy.Input) (contextpolicy.Decision, error) {
		return contextpolicy.Decision{CandidateID: 1}, nil
	})
	if _, err := Plan(context.Background(), malformed, input, "pre_turn", 0, 0); !errors.Is(err, ErrPolicy) {
		t.Fatalf("malformed decision accepted: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Plan(cancelled, nil, input, "manual", 0, 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	broken := policyFunc(func(context.Context, contextpolicy.Input) (contextpolicy.Decision, error) {
		return contextpolicy.Decision{}, errors.New("broken")
	})
	if _, err := Plan(context.Background(), broken, input, "manual", 0, 0); !errors.Is(err, ErrPolicy) {
		t.Fatalf("callback error concealed: %v", err)
	}
}
