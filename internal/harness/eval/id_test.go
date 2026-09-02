package eval

import (
	"errors"
	"strings"
	"testing"
)

func TestParseScenarioIDValid(t *testing.T) {
	for _, raw := range []string{"a", "a1", "scenario-1", "scenario.v2", "scenario_v2", strings.Repeat("a", 128)} {
		t.Run(raw, func(t *testing.T) {
			id, err := ParseScenarioID(raw)
			if err != nil {
				t.Fatalf("ParseScenarioID(%q) unexpected error: %v", raw, err)
			}
			if string(id) != raw {
				t.Fatalf("ParseScenarioID(%q) = %q, want %q", raw, id, raw)
			}
		})
	}
}

func TestParseScenarioIDInvalid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "too long", raw: strings.Repeat("a", 129)},
		{name: "uppercase", raw: "Scenario"},
		{name: "leading dot", raw: ".scenario"},
		{name: "leading dash", raw: "-scenario"},
		{name: "leading underscore", raw: "_scenario"},
		{name: "space", raw: "scenario one"},
		{name: "slash", raw: "scenario/one"},
		{name: "unicode", raw: "scénario"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseScenarioID(test.raw); err == nil {
				t.Fatalf("ParseScenarioID(%q) succeeded, want error", test.raw)
			} else if !errors.Is(err, errInvalidID) {
				t.Fatalf("ParseScenarioID(%q) error = %v, want wrapping errInvalidID", test.raw, err)
			}
		})
	}
}

func TestParseSubjectIDAndExecutorIDShareValidation(t *testing.T) {
	if _, err := ParseSubjectID(""); err == nil {
		t.Fatal("ParseSubjectID(\"\") succeeded, want error")
	}
	if _, err := ParseExecutorID(""); err == nil {
		t.Fatal("ParseExecutorID(\"\") succeeded, want error")
	}
	subjectID, err := ParseSubjectID("subject-1")
	if err != nil {
		t.Fatalf("ParseSubjectID unexpected error: %v", err)
	}
	if subjectID != "subject-1" {
		t.Fatalf("ParseSubjectID = %q, want %q", subjectID, "subject-1")
	}
	executorID, err := ParseExecutorID("in-process")
	if err != nil {
		t.Fatalf("ParseExecutorID unexpected error: %v", err)
	}
	if executorID != "in-process" {
		t.Fatalf("ParseExecutorID = %q, want %q", executorID, "in-process")
	}
}

func TestNewGeneratedIDShapeAndUniqueness(t *testing.T) {
	first, err := NewGeneratedID()
	if err != nil {
		t.Fatalf("NewGeneratedID: %v", err)
	}
	second, err := NewGeneratedID()
	if err != nil {
		t.Fatalf("NewGeneratedID: %v", err)
	}
	if first == second {
		t.Fatalf("NewGeneratedID produced the same identifier twice: %q", first)
	}
	if len(first) != 32 {
		t.Fatalf("NewGeneratedID length = %d, want 32", len(first))
	}
	if !generatedIDPattern.MatchString(string(first)) {
		t.Fatalf("NewGeneratedID = %q does not match %s", first, generatedIDPattern.String())
	}
}

func TestParseGeneratedID(t *testing.T) {
	generated, err := NewGeneratedID()
	if err != nil {
		t.Fatalf("NewGeneratedID: %v", err)
	}
	parsed, err := ParseGeneratedID(string(generated))
	if err != nil {
		t.Fatalf("ParseGeneratedID(%q): %v", generated, err)
	}
	if parsed != generated {
		t.Fatalf("ParseGeneratedID round trip = %q, want %q", parsed, generated)
	}

	invalid := []string{"", "too-short", strings.ToUpper(string(generated)), string(generated) + "0"}
	for _, raw := range invalid {
		if _, err := ParseGeneratedID(raw); err == nil {
			t.Fatalf("ParseGeneratedID(%q) succeeded, want error", raw)
		}
	}
}
