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

func TestIDParsersRejectInvalidUTF8(t *testing.T) {
	t.Parallel()

	invalid := "identifier-\xff"
	tests := []struct {
		name  string
		parse func(string) error
	}{
		{name: "session", parse: func(value string) error { _, err := ParseSessionID(value); return err }},
		{name: "turn", parse: func(value string) error { _, err := ParseTurnID(value); return err }},
		{name: "command", parse: func(value string) error { _, err := ParseCommandID(value); return err }},
		{name: "event", parse: func(value string) error { _, err := ParseEventID(value); return err }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.parse(invalid); !IsCode(err, CodeInvalidID) {
				t.Fatalf("parse invalid UTF-8 error = %v, want code %q", err, CodeInvalidID)
			}
		})
	}
}
