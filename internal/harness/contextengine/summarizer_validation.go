package contextengine

import (
	_ "embed"
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/redact"
)

// SummaryPrompt is the versioned och_context_summary_v1 prompt asset
// (design §11.1, prompt.md), owned by this package rather than an inline
// Application string.
//
//go:embed prompt.md
var SummaryPrompt string

// requiredSummaryHeadings is och_context_summary_v1's exact, ordered
// top-level section list (design §11.1).
var requiredSummaryHeadings = []string{
	"## Objective",
	"## User Constraints",
	"## Established Facts",
	"## Work Completed",
	"## Files and Commands",
	"## Open Work",
	"## Risks and Unknowns",
	"## Continuation",
}

// maxSummaryBytes is design §11.3's 256 KiB absolute cap, independent of
// the token-based SummaryOutputCap.
const maxSummaryBytes = 256 * 1024

// SummaryValidationInput bundles everything ValidateSummary needs (design
// §11.3).
type SummaryValidationInput struct {
	RawOutput          string
	TerminatedNormally bool // false if cut off by output length or a stream error
	ContainsToolCall   bool
	ContainsNonText    bool
	SummaryOutputCap   uint64
	Meter              Meter
	// PrePassRequestTokens is what the complete request would total
	// without this checkpoint.
	PrePassRequestTokens uint64
	// PostPassRequestTokens is what the complete request totals WITH this
	// checkpoint (summary + retained tail + current input) -- computed by
	// the caller (Task 9's materializer) over the real envelope, since
	// only the caller has it.
	PostPassRequestTokens uint64
	HardInput             uint64
	// CoveredSourceTokens is the token cost of the source content this
	// checkpoint replaces; checkpoint framing must come in smaller than
	// what it replaces.
	CoveredSourceTokens uint64
}

// SummaryValidationResult is ValidateSummary's outcome. On success,
// RedactedText is the summary actually validated and usable as evidence —
// redaction runs before the size/shrink checks so what was checked and
// what gets recorded are identical (design §11.3: "Redaction happens
// before the size/shrink checks so recorded evidence equals the summary
// actually used").
type SummaryValidationResult struct {
	Valid         bool
	RedactedText  string
	FailureReason string // non-empty only when !Valid
}

func failSummary(reason string) SummaryValidationResult {
	return SummaryValidationResult{FailureReason: reason}
}

// ValidateSummary implements design §11.3's full checklist. Any failure
// closes the compaction bracket as failed; this function only reports a
// typed result — appending context.compaction.failed is Task 9/10's job.
func ValidateSummary(input SummaryValidationInput) SummaryValidationResult {
	if !input.TerminatedNormally {
		return failSummary("summary generation did not terminate normally")
	}
	if input.ContainsToolCall {
		return failSummary("summary output contains a Tool Call")
	}
	if input.ContainsNonText {
		return failSummary("summary output contains non-text content")
	}
	if !utf8.ValidString(input.RawOutput) || strings.TrimSpace(input.RawOutput) == "" {
		return failSummary("summary output is not valid, non-blank UTF-8")
	}
	if reason := validateHeadingShape(input.RawOutput); reason != "" {
		return failSummary(reason)
	}

	redacted := redact.Text(input.RawOutput)
	if len(redacted) > maxSummaryBytes {
		return failSummary("summary exceeds 256 KiB")
	}
	summaryTokens := input.Meter.EstimateMessages([]domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: redacted}})
	if summaryTokens > input.SummaryOutputCap {
		return failSummary("summary exceeds summaryOutputCap")
	}
	if summaryTokens >= input.CoveredSourceTokens {
		return failSummary("checkpoint framing is not smaller than the source it replaces")
	}
	if input.PostPassRequestTokens > input.HardInput {
		return failSummary("resulting request still exceeds hardInput")
	}
	// At least 10% smaller than the pre-pass request, expressed as
	// integer arithmetic (post*10 <= pre*9) to avoid floating point.
	if input.PostPassRequestTokens*10 > input.PrePassRequestTokens*9 {
		return failSummary("resulting request is not at least 10% smaller than the pre-pass request")
	}

	return SummaryValidationResult{Valid: true, RedactedText: redacted}
}

// validateHeadingShape confirms every requiredSummaryHeadings entry
// appears exactly once, in order, with no unknown top-level ("## ")
// heading interleaved.
func validateHeadingShape(output string) string {
	var found []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(trimmed, "## ") {
			found = append(found, trimmed)
		}
	}
	if len(found) != len(requiredSummaryHeadings) {
		return "summary does not contain exactly the required headings"
	}
	for index, heading := range found {
		if heading != requiredSummaryHeadings[index] {
			return "summary headings are missing, duplicated, unknown, or out of order"
		}
	}
	return ""
}
