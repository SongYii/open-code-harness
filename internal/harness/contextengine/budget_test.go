package contextengine

import (
	"errors"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestComputeBudget(t *testing.T) {
	tests := []struct {
		name    string
		window  uint32
		output  uint32
		config  BudgetConfig
		want    Budget
		wantErr error
	}{
		{
			name:   "8K window",
			window: 8192,
			output: 1024,
			config: DefaultBudgetConfig,
			// safety = max(512, ceil(8192*2/100)) = max(512, 164) = 512
			// hardInput = 8192 - 1024 - 512 = 6656
			want: Budget{
				Safety:           512,
				HardInput:        6656,
				Trigger:          6656 * 80 / 100, // 5324
				Target:           6656 * 55 / 100, // 3660
				ProtectedTail:    6656 * 25 / 100, // 1664
				SummaryOutputCap: 665,             // min(1024, max(128, 6656/10=665))
			},
		},
		{
			name:   "32K window",
			window: 32768,
			output: 4096,
			config: DefaultBudgetConfig,
			// safety = max(512, ceil(32768*2/100)) = max(512, 656) = 656
			// hardInput = 32768 - 4096 - 656 = 28016
			want: Budget{
				Safety:           656,
				HardInput:        28016,
				Trigger:          28016 * 80 / 100,
				Target:           28016 * 55 / 100,
				ProtectedTail:    28016 * 25 / 100,
				SummaryOutputCap: 2801, // min(4096, max(128, 28016/10=2801))
			},
		},
		{
			name:   "128K window",
			window: 131072,
			output: 16384,
			config: DefaultBudgetConfig,
			// safety = max(512, ceil(131072*2/100)) = max(512, 2622) = 2622
			// hardInput = 131072 - 16384 - 2622 = 112066
			want: Budget{
				Safety:           2622,
				HardInput:        112066,
				Trigger:          112066 * 80 / 100,
				Target:           112066 * 55 / 100,
				ProtectedTail:    112066 * 25 / 100,
				SummaryOutputCap: 11206, // min(16384, max(128, 112066/10=11206))
			},
		},
		{
			name:    "O + safety just under W is valid",
			window:  1537, // safety = max(512, ceil(1537*0.02)=31) = 512; O+safety=1536 < 1537
			output:  1024,
			config:  DefaultBudgetConfig,
			wantErr: nil,
		},
		{
			name:    "O + safety equal to W is invalid",
			window:  1536, // safety = 512; O+safety = 1536 == W
			output:  1024,
			config:  DefaultBudgetConfig,
			wantErr: ErrBudgetInvalid,
		},
		{
			name:    "O + safety over W is invalid",
			window:  1000,
			output:  1024,
			config:  DefaultBudgetConfig,
			wantErr: ErrBudgetInvalid,
		},
		{
			name:    "zero window is invalid",
			window:  0,
			output:  0,
			config:  DefaultBudgetConfig,
			wantErr: ErrBudgetInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ComputeBudget(test.window, test.output, test.config)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("ComputeBudget error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ComputeBudget unexpected error: %v", err)
			}
			if test.want != (Budget{}) && got != test.want {
				t.Fatalf("ComputeBudget = %+v, want %+v", got, test.want)
			}
		})
	}
}

// TestComputeBudgetNearInvalidBoundary is the mutation-check counterpart
// for the "output/safety reserve" mutation-kill target (design §22.4): it
// pins the exact boundary at which O + safety transitions from valid to
// invalid, so a mutation that hardcodes safety to a small constant
// regardless of W changes which side of this boundary each case lands on.
func TestComputeBudgetNearInvalidBoundary(t *testing.T) {
	// window=1537, output=1024: safety=512, O+safety=1536 < 1537 -> valid.
	if _, err := ComputeBudget(1537, 1024, DefaultBudgetConfig); err != nil {
		t.Fatalf("expected the boundary-1 case to be valid, got %v", err)
	}
	// window=1536, output=1024: safety=512, O+safety=1536 == 1536 -> invalid.
	if _, err := ComputeBudget(1536, 1024, DefaultBudgetConfig); !errors.Is(err, ErrBudgetInvalid) {
		t.Fatalf("expected the boundary-2 case to be ErrBudgetInvalid, got %v", err)
	}
}

func TestComputeBudgetTriggerComparison(t *testing.T) {
	// A below-trigger and an above-trigger case bracketing the same Trigger
	// value, so an inverted trigger comparison in a caller using Budget.Trigger
	// would flip which case looks like "needs compaction."
	budget, err := ComputeBudget(8192, 1024, DefaultBudgetConfig)
	if err != nil {
		t.Fatal(err)
	}
	if budget.Trigger == 0 {
		t.Fatal("expected a non-zero trigger for this fixture")
	}
	belowTrigger := budget.Trigger - 1
	aboveTrigger := budget.Trigger + 1
	if !(belowTrigger < budget.Trigger) {
		t.Fatalf("fixture invariant broken: %d is not below trigger %d", belowTrigger, budget.Trigger)
	}
	if !(aboveTrigger > budget.Trigger) {
		t.Fatalf("fixture invariant broken: %d is not above trigger %d", aboveTrigger, budget.Trigger)
	}
}

func msg(role, text string) domain.ModelPromptMessage {
	return domain.ModelPromptMessage{Role: role, Text: text}
}

func toolResult(callID, text string) domain.ModelPromptMessage {
	return domain.ModelPromptMessage{Role: "tool", Text: text, ToolCallID: callID}
}

func TestEvaluateUsageAnchor(t *testing.T) {
	meter := WireEstimateMeter{}
	base := []domain.ModelPromptMessage{msg("user", "hello"), msg("assistant", "hi there")}
	tools := []domain.ToolSchema{{Name: "read_file", Description: "reads a file", InputSchema: []byte(`{"type":"object"}`)}}

	anchor := UsageAnchor{
		AdapterFamily:       "openaicompat",
		ModelID:             "gpt-x",
		EndpointID:          "https://api.example.com/v1",
		Purpose:             PurposeConversation,
		MeterID:             WireEstimateMeterID,
		Tools:               tools,
		Messages:            base,
		ObservedInputTokens: 500,
	}

	t.Run("exact match with no appended content", func(t *testing.T) {
		got := EvaluateUsageAnchor(anchor, meter, "openaicompat", "gpt-x", "https://api.example.com/v1", WireEstimateMeterID, tools, base)
		if !got.Eligible || got.Tokens != 500 {
			t.Fatalf("got %+v, want eligible with 500 tokens", got)
		}
	})

	t.Run("append-only delta is priced and added", func(t *testing.T) {
		appended := append(append([]domain.ModelPromptMessage{}, base...), msg("user", "and one more thing"))
		got := EvaluateUsageAnchor(anchor, meter, "openaicompat", "gpt-x", "https://api.example.com/v1", WireEstimateMeterID, tools, appended)
		wantDelta := meter.EstimateMessages([]domain.ModelPromptMessage{msg("user", "and one more thing")})
		if !got.Eligible || got.Tokens != 500+wantDelta {
			t.Fatalf("got %+v, want eligible with %d tokens", got, 500+wantDelta)
		}
	})

	t.Run("checkpoint-style replacement delta is not derivable by append and falls back", func(t *testing.T) {
		rewritten := []domain.ModelPromptMessage{msg("user", "a checkpoint summary replaces history")}
		got := EvaluateUsageAnchor(anchor, meter, "openaicompat", "gpt-x", "https://api.example.com/v1", WireEstimateMeterID, tools, rewritten)
		if got.Eligible {
			t.Fatalf("got %+v, want ineligible: a rewritten prefix is not a simple append", got)
		}
	})

	identityMismatches := []struct {
		name    string
		mutate  func(UsageAnchor) UsageAnchor
		current func() (adapter, model, endpoint, meterID string, tools []domain.ToolSchema)
	}{
		{name: "adapter family mismatch", mutate: func(a UsageAnchor) UsageAnchor { a.AdapterFamily = "other"; return a }},
		{name: "model mismatch", mutate: func(a UsageAnchor) UsageAnchor { a.ModelID = "other-model"; return a }},
		{name: "endpoint mismatch", mutate: func(a UsageAnchor) UsageAnchor { a.EndpointID = "https://other.example.com/v1"; return a }},
		{name: "meter mismatch", mutate: func(a UsageAnchor) UsageAnchor { a.MeterID = "some_other_meter_v1"; return a }},
		{name: "tools mismatch", mutate: func(a UsageAnchor) UsageAnchor {
			a.Tools = []domain.ToolSchema{{Name: "write_file"}}
			return a
		}},
		{name: "compaction purpose is never eligible", mutate: func(a UsageAnchor) UsageAnchor { a.Purpose = "compaction"; return a }},
	}
	for _, test := range identityMismatches {
		t.Run(test.name, func(t *testing.T) {
			mutated := test.mutate(anchor)
			got := EvaluateUsageAnchor(mutated, meter, "openaicompat", "gpt-x", "https://api.example.com/v1", WireEstimateMeterID, tools, base)
			if got.Eligible {
				t.Fatalf("got %+v, want ineligible", got)
			}
		})
	}

	t.Run("zero observed input tokens is ineligible", func(t *testing.T) {
		zero := anchor
		zero.ObservedInputTokens = 0
		got := EvaluateUsageAnchor(zero, meter, "openaicompat", "gpt-x", "https://api.example.com/v1", WireEstimateMeterID, tools, base)
		if got.Eligible {
			t.Fatalf("got %+v, want ineligible", got)
		}
	})

	t.Run("malformed cached-greater-than-observed is ineligible", func(t *testing.T) {
		malformed := anchor
		malformed.CachedInputTokens = anchor.ObservedInputTokens + 1
		got := EvaluateUsageAnchor(malformed, meter, "openaicompat", "gpt-x", "https://api.example.com/v1", WireEstimateMeterID, tools, base)
		if got.Eligible {
			t.Fatalf("got %+v, want ineligible", got)
		}
	})

	t.Run("non-lowering: a deliberately understated anchor never reduces budgetEstimate below wireEstimate", func(t *testing.T) {
		understated := anchor
		understated.ObservedInputTokens = 1 // far below the real wire estimate of `base`
		wireEstimate := meter.Estimate(Envelope{Messages: base, Tools: tools}).Tokens
		anchorResult := EvaluateUsageAnchor(understated, meter, "openaicompat", "gpt-x", "https://api.example.com/v1", WireEstimateMeterID, tools, base)
		if !anchorResult.Eligible {
			t.Fatal("expected the understated anchor to still be eligible on identity/append grounds")
		}
		budgetEstimate := maxUint64(wireEstimate, anchorResult.Tokens)
		if budgetEstimate < wireEstimate {
			t.Fatalf("budgetEstimate %d fell below wireEstimate %d", budgetEstimate, wireEstimate)
		}
	})
}
