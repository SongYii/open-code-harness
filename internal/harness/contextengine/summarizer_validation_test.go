package contextengine

import (
	"strings"
	"testing"
)

func validSummaryText() string {
	return `## Objective
Fix the failing test.

## User Constraints
None.

## Established Facts
The bug is in budget.go.

## Work Completed
Read the file.

## Files and Commands
internal/harness/contextengine/budget.go

## Open Work
Write the fix.

## Risks and Unknowns
None.

## Continuation
Continue fixing the bug.`
}

func baseSummaryValidationInput() SummaryValidationInput {
	return SummaryValidationInput{
		RawOutput:             validSummaryText(),
		TerminatedNormally:    true,
		SummaryOutputCap:      10_000,
		Meter:                 WireEstimateMeter{},
		PrePassRequestTokens:  1000,
		PostPassRequestTokens: 800, // 20% smaller, satisfies >= 10%
		HardInput:             1_000_000,
		CoveredSourceTokens:   10_000, // comfortably larger than the summary itself
	}
}

func TestValidateSummaryAccepts(t *testing.T) {
	result := ValidateSummary(baseSummaryValidationInput())
	if !result.Valid {
		t.Fatalf("expected valid, got failure: %s", result.FailureReason)
	}
	if result.RedactedText == "" {
		t.Fatal("expected non-empty RedactedText on success")
	}
}

func TestValidateSummaryRejectsNonNormalTermination(t *testing.T) {
	input := baseSummaryValidationInput()
	input.TerminatedNormally = false
	result := ValidateSummary(input)
	if result.Valid {
		t.Fatal("expected invalid for non-normal termination")
	}
}

func TestValidateSummaryRejectsToolCall(t *testing.T) {
	input := baseSummaryValidationInput()
	input.ContainsToolCall = true
	if ValidateSummary(input).Valid {
		t.Fatal("expected invalid when output contains a Tool Call")
	}
}

func TestValidateSummaryRejectsNonText(t *testing.T) {
	input := baseSummaryValidationInput()
	input.ContainsNonText = true
	if ValidateSummary(input).Valid {
		t.Fatal("expected invalid when output contains non-text content")
	}
}

func TestValidateSummaryRejectsBlank(t *testing.T) {
	input := baseSummaryValidationInput()
	input.RawOutput = "   \n\t  "
	if ValidateSummary(input).Valid {
		t.Fatal("expected invalid for blank output")
	}
}

func TestValidateSummaryRejectsMissingHeading(t *testing.T) {
	input := baseSummaryValidationInput()
	input.RawOutput = strings.Replace(validSummaryText(), "## Continuation\nContinue fixing the bug.", "", 1)
	if ValidateSummary(input).Valid {
		t.Fatal("expected invalid when a required heading is missing")
	}
}

func TestValidateSummaryRejectsDuplicateHeading(t *testing.T) {
	input := baseSummaryValidationInput()
	input.RawOutput = validSummaryText() + "\n\n## Objective\nduplicate section"
	if ValidateSummary(input).Valid {
		t.Fatal("expected invalid when a heading is duplicated")
	}
}

func TestValidateSummaryRejectsUnknownHeading(t *testing.T) {
	input := baseSummaryValidationInput()
	input.RawOutput = validSummaryText() + "\n\n## Extra Unknown Section\nsurprise"
	if ValidateSummary(input).Valid {
		t.Fatal("expected invalid when an unknown heading is present")
	}
}

func TestValidateSummaryRejectsOutOfOrderHeadings(t *testing.T) {
	input := baseSummaryValidationInput()
	// Swap the first two headings.
	swapped := strings.Replace(validSummaryText(), "## Objective", "## PLACEHOLDER", 1)
	swapped = strings.Replace(swapped, "## User Constraints", "## Objective", 1)
	swapped = strings.Replace(swapped, "## PLACEHOLDER", "## User Constraints", 1)
	input.RawOutput = swapped
	if ValidateSummary(input).Valid {
		t.Fatal("expected invalid when headings are out of order")
	}
}

func TestValidateSummaryRedactsSecretBeforeSizeChecks(t *testing.T) {
	input := baseSummaryValidationInput()
	// A secret-shaped string (matching this project's own redact.Text
	// patterns) embedded in an otherwise-valid section.
	input.RawOutput = strings.Replace(validSummaryText(), "None.", "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 1)
	result := ValidateSummary(input)
	if !result.Valid {
		t.Fatalf("expected valid (redaction should strip the secret, not fail the summary), got: %s", result.FailureReason)
	}
	if strings.Contains(result.RedactedText, "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Fatal("expected the secret-shaped string to be redacted out of RedactedText")
	}
}

func TestValidateSummaryRejectsWhenNotSmallerThanCoveredSource(t *testing.T) {
	input := baseSummaryValidationInput()
	input.CoveredSourceTokens = 1 // the summary itself is certainly larger than 1 token
	if ValidateSummary(input).Valid {
		t.Fatal("expected invalid when checkpoint framing is not smaller than the source it replaces")
	}
}

func TestValidateSummaryRejectsWhenExceedingHardInput(t *testing.T) {
	input := baseSummaryValidationInput()
	input.PostPassRequestTokens = 2000
	input.HardInput = 1000
	if ValidateSummary(input).Valid {
		t.Fatal("expected invalid when the resulting request exceeds hardInput")
	}
}

// TestValidateSummaryShrinkMutation is the mutation-check counterpart
// (design §22.4's "summary shrink validator" target, plan Task 6):
// removing the >= 10% shrink requirement must make this test's own
// under-shrink case stop failing -- see the commit message for the actual
// manual mutation run. This test pins the boundary precisely.
func TestValidateSummaryShrinkMutation(t *testing.T) {
	tests := []struct {
		name  string
		post  uint64
		valid bool
	}{
		{name: "exactly 10% smaller is valid", post: 900, valid: true}, // 900*10=9000 <= 1000*9=9000
		{name: "9% smaller is invalid", post: 910, valid: false},       // 910*10=9100 > 9000
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := baseSummaryValidationInput()
			input.PostPassRequestTokens = test.post
			got := ValidateSummary(input).Valid
			if got != test.valid {
				t.Fatalf("PostPassRequestTokens=%d: got valid=%t, want %t", test.post, got, test.valid)
			}
		})
	}
}
