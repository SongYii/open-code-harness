package domain

import "testing"

func TestParseSessionID(t *testing.T) {
	t.Parallel()

	got, err := ParseSessionID("session-1")
	if err != nil {
		t.Fatalf("ParseSessionID() error = %v", err)
	}
	if got != SessionID("session-1") {
		t.Fatalf("ParseSessionID() = %q", got)
	}
}

func TestParseSessionIDRejectsBlankOrPaddedValues(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "   ", " session-1", "session-1 "} {
		_, err := ParseSessionID(input)
		if !IsCode(err, CodeInvalidID) {
			t.Fatalf("ParseSessionID(%q) error = %v, want code %q", input, err, CodeInvalidID)
		}
	}
}
