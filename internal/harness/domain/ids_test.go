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

func TestParseItemID(t *testing.T) {
	t.Parallel()

	got, err := ParseItemID("item-1")
	if err != nil {
		t.Fatalf("ParseItemID() error = %v", err)
	}
	if got != ItemID("item-1") {
		t.Fatalf("ParseItemID() = %q, want %q", got, ItemID("item-1"))
	}
}

func TestParseItemIDRejectsBlankPaddedAndInvalidUTF8(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "   ", " item-1", "item-1 ", "item-\xff"} {
		_, err := ParseItemID(input)
		if !IsCode(err, CodeInvalidID) {
			t.Fatalf("ParseItemID(%q) error = %v, want code %q", input, err, CodeInvalidID)
		}
	}
}

func TestParseAppendAndRunTurnRequestIDs(t *testing.T) {
	for _, test := range []struct {
		name  string
		parse func(string) error
	}{
		{"append", func(v string) error { _, err := ParseAppendID(v); return err }},
		{"request", func(v string) error { _, err := ParseRunTurnRequestID(v); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.parse(test.name + "-1"); err != nil {
				t.Fatal(err)
			}
			if err := test.parse(" " + test.name); !IsCode(err, CodeInvalidID) {
				t.Fatalf("error = %v, want %q", err, CodeInvalidID)
			}
		})
	}
}

func TestAppendAndRunTurnRequestIDParsersRejectInvalidValues(t *testing.T) {
	invalidUTF8 := "identifier-\xff"
	for _, test := range []struct {
		name  string
		parse func(string) error
	}{
		{"append", func(value string) error { _, err := ParseAppendID(value); return err }},
		{"request", func(value string) error { _, err := ParseRunTurnRequestID(value); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, input := range []string{"", "   ", " id", "id ", invalidUTF8} {
				if err := test.parse(input); !IsCode(err, CodeInvalidID) {
					t.Fatalf("parse(%q) error = %v, want code %q", input, err, CodeInvalidID)
				}
			}
		})
	}
}
