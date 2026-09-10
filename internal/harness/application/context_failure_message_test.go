package application

import (
	"errors"
	"strings"
	"testing"
)

func TestSafeFailureMessageRetainsOnlyStaticValidationReason(t *testing.T) {
	got := safeFailureMessage(errors.New(CodeContextSummaryInvalid + ": summary does not contain exactly the required headings"))
	if got != "summary output failed validation: summary does not contain exactly the required headings" {
		t.Fatalf("safeFailureMessage() = %q", got)
	}

	raw := "provider returned raw secret text"
	got = safeFailureMessage(errors.New(CodeContextSummaryFailed + ": " + raw))
	if strings.Contains(got, raw) {
		t.Fatalf("safeFailureMessage() leaked provider detail: %q", got)
	}
}
