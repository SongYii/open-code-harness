package application

import (
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestDigestAppendRequestIsStableAndCoversImmutableFacts(t *testing.T) {
	request := validAppendRequest()
	got, err := DigestAppendRequest(request)
	if err != nil {
		t.Fatalf("DigestAppendRequest() error = %v", err)
	}
	if text := got.String(); text != "e7f068f5f36b1b2c50cb19aff13bb16bb038c53ef272d54d2e7a6d4c38e4005c" {
		t.Fatalf("DigestAppendRequest() = %q, want fixed canonical digest", text)
	}
	repeated, err := DigestAppendRequest(request)
	if err != nil || repeated != got {
		t.Fatalf("DigestAppendRequest() repeated = (%x, %v), want (%x, nil)", repeated, err, got)
	}

	changes := []struct {
		name   string
		mutate func(*AppendRequestV2)
	}{
		{"session", func(r *AppendRequestV2) { r.SessionID = "session-2" }},
		{"expected version", func(r *AppendRequestV2) { r.ExpectedVersion++ }},
		{"command", func(r *AppendRequestV2) { r.CommandID = "command-2" }},
		{"admission request ID", func(r *AppendRequestV2) { r.Admission.RunTurnRequestID = "request-2" }},
		{"admission digest", func(r *AppendRequestV2) { r.Admission.RequestDigest[0]++ }},
		{"admission turn", func(r *AppendRequestV2) { r.Admission.TurnID = "turn-2" }},
		{"admission item", func(r *AppendRequestV2) { r.Admission.ItemID = "item-2" }},
		{"event ID", func(r *AppendRequestV2) { r.Events[0].ID = "event-2" }},
		{"event time", func(r *AppendRequestV2) { r.Events[0].OccurredAt = r.Events[0].OccurredAt.Add(time.Nanosecond) }},
		{"event type", func(r *AppendRequestV2) {
			r.Events[0].Event = domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}
		}},
		{"event payload", func(r *AppendRequestV2) {
			r.Events[0].Event = domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "changed"}
		}},
	}
	withoutAdmission := validAppendRequest()
	withoutAdmission.Admission = nil
	withoutAdmissionDigest, err := DigestAppendRequest(withoutAdmission)
	if err != nil || withoutAdmissionDigest == got {
		t.Fatalf("admission presence digest = (%x, %v), want different valid digest", withoutAdmissionDigest, err)
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			changed := validAppendRequest()
			change.mutate(&changed)
			digest, err := DigestAppendRequest(changed)
			if err == nil && digest == got {
				t.Fatal("covered immutable fact did not change digest")
			}
		})
	}
}

func TestDigestAppendRequestExcludesReceiptAndAuthorityButValidatesThem(t *testing.T) {
	base, err := DigestAppendRequest(validAppendRequest())
	if err != nil {
		t.Fatal(err)
	}
	changed := validAppendRequest()
	changed.AppendID = "append-2"
	changed.Authority = WriterAuthority{RuntimeID: "runtime-2", FencingToken: 2}
	got, err := DigestAppendRequest(changed)
	if err != nil || got != base {
		t.Fatalf("excluded facts digest = (%x, %v), want (%x, nil)", got, err, base)
	}

	for _, mutate := range []func(*AppendRequestV2){
		func(r *AppendRequestV2) { r.AppendID = " invalid" },
		func(r *AppendRequestV2) { r.Authority = WriterAuthority{} },
	} {
		invalid := validAppendRequest()
		mutate(&invalid)
		if _, err := DigestAppendRequest(invalid); err == nil {
			t.Fatal("DigestAppendRequest() error = nil for invalid excluded fact")
		}
	}
}

func TestDigestAppendRequestFramingSeparatesFactsAndOrder(t *testing.T) {
	left, right := validAppendRequest(), validAppendRequest()
	left.SessionID, left.CommandID = "ab", "c"
	right.SessionID, right.CommandID = "a", "bc"
	leftDigest, leftErr := DigestAppendRequest(left)
	rightDigest, rightErr := DigestAppendRequest(right)
	if leftErr != nil || rightErr != nil {
		t.Fatalf("digest errors = %v, %v", leftErr, rightErr)
	}
	if leftDigest == rightDigest {
		t.Fatal("length framing did not separate adjacent fields")
	}

	left, right = validAppendRequest(), validAppendRequest()
	left.Admission.TurnID, left.Admission.ItemID = "a\x00b", "c"
	right.Admission.TurnID, right.Admission.ItemID = "a", "\x00bc"
	if string(left.Admission.TurnID)+string(left.Admission.ItemID) != string(right.Admission.TurnID)+string(right.Admission.ItemID) {
		t.Fatal("test setup must collide under unframed admission ID concatenation")
	}
	leftDigest, leftErr = DigestAppendRequest(left)
	rightDigest, rightErr = DigestAppendRequest(right)
	if leftErr != nil || rightErr != nil {
		t.Fatalf("embedded NUL digest errors = %v, %v", leftErr, rightErr)
	}
	if leftDigest == rightDigest {
		t.Fatal("length framing did not separate embedded-NUL admission IDs")
	}

	left, right = validAppendRequest(), validAppendRequest()
	left.Events = append(left.Events, domainEvent("event-2", "second"))
	right.Events = append(right.Events, domainEvent("event-2", "second"))
	right.Events[0], right.Events[1] = right.Events[1], right.Events[0]
	leftDigest, leftErr = DigestAppendRequest(left)
	rightDigest, rightErr = DigestAppendRequest(right)
	if leftErr != nil || rightErr != nil || leftDigest == rightDigest {
		t.Fatalf("event ordering digests = (%x, %v), (%x, %v)", leftDigest, leftErr, rightDigest, rightErr)
	}
}

func TestDigestAppendRequestRejectsInvalidAndOversizedFacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AppendRequestV2)
	}{
		{"empty events", func(r *AppendRequestV2) { r.Events = nil }},
		{"invalid event ID", func(r *AppendRequestV2) { r.Events[0].ID = " invalid" }},
		{"non UTC time", func(r *AppendRequestV2) {
			r.Events[0].OccurredAt = r.Events[0].OccurredAt.In(time.FixedZone("offset", 3600))
		}},
		{"zero time", func(r *AppendRequestV2) { r.Events[0].OccurredAt = time.Time{} }},
		{"unsupported schema", func(r *AppendRequestV2) { r.Events[0].SchemaVersion = 2 }},
		{"invalid payload", func(r *AppendRequestV2) {
			r.Events[0].Event = domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: string([]byte{0xff})}
		}},
		{"more than 64 events", func(r *AppendRequestV2) {
			for n := 2; n <= 65; n++ {
				r.Events = append(r.Events, domainEvent(domain.EventID("event-"+strings.Repeat("x", n)), "x"))
			}
		}},
		{"payload over 8 MiB", func(r *AppendRequestV2) {
			r.Events[0].Event = domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: strings.Repeat("x", 8*1024*1024)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validAppendRequest()
			test.mutate(&request)
			if _, err := DigestAppendRequest(request); err == nil {
				t.Fatal("DigestAppendRequest() error = nil")
			}
		})
	}
}

func TestDigestAppendRequestAcceptsExactResourceLimits(t *testing.T) {
	request := validAppendRequest()
	request.Events[0].Event = domain.AssistantMessageCompleted{
		TurnID: "turn-1", ItemID: "item-1",
		Text: strings.Repeat("x", 8*1024*1024-47),
	}
	if _, err := DigestAppendRequest(request); err != nil {
		t.Fatalf("DigestAppendRequest() exact 8 MiB payload error = %v", err)
	}

	request = validAppendRequest()
	request.Events = []ProposedEvent{
		appendSizedEvent("event-a", 8*1024*1024-47),
		appendSizedEvent("event-b", 8*1024*1024-47),
	}
	// The complete v1 frame is 16 MiB exactly: two 8 MiB payloads plus the
	// fixed fields, offset by reducing the second payload by the fixed framing.
	request.Events[1] = appendSizedEvent("event-b", 8*1024*1024-47-296)
	if _, err := DigestAppendRequest(request); err != nil {
		t.Fatalf("DigestAppendRequest() exact 16 MiB frame error = %v", err)
	}
	request.Events[1] = appendSizedEvent("event-b", 8*1024*1024-47-295)
	if _, err := DigestAppendRequest(request); err == nil {
		t.Fatal("DigestAppendRequest() accepted 16 MiB + 1 complete frame")
	}
}

func appendSizedEvent(id domain.EventID, textSize int) ProposedEvent {
	return ProposedEvent{
		ID:            id,
		SchemaVersion: 1,
		OccurredAt:    time.Date(2026, 8, 13, 1, 2, 3, 4, time.UTC),
		Event: domain.AssistantMessageCompleted{
			TurnID: "turn-1", ItemID: "item-1", Text: strings.Repeat("x", textSize),
		},
	}
}

func TestDigestRunTurnRequestV1FramesSessionAndInput(t *testing.T) {
	left, err := DigestRunTurnRequestV1("ab", "c")
	if err != nil {
		t.Fatal(err)
	}
	right, err := DigestRunTurnRequestV1("a", "bc")
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("session and input framing collided")
	}
	if _, err := DigestRunTurnRequestV1(" invalid", "input"); err == nil {
		t.Fatal("DigestRunTurnRequestV1() accepted invalid session ID")
	}
	if _, err := DigestRunTurnRequestV1("session-1", string([]byte{0xff})); err == nil {
		t.Fatal("DigestRunTurnRequestV1() accepted invalid UTF-8 input")
	}
}

func TestDigestTextEncodingIsStrictLowercaseHex(t *testing.T) {
	digest := Digest{0xab, 0xcd}
	text, err := digest.MarshalText()
	if err != nil || string(text) != "abcd000000000000000000000000000000000000000000000000000000000000" {
		t.Fatalf("MarshalText() = %q, %v", text, err)
	}
	if parsed, err := ParseDigest(string(text)); err != nil || parsed != digest {
		t.Fatalf("ParseDigest() = %x, %v", parsed, err)
	}
	for _, invalid := range []string{"ABCD000000000000000000000000000000000000000000000000000000000000", "abc", strings.Repeat("g", 64)} {
		if _, err := ParseDigest(invalid); err == nil {
			t.Fatalf("ParseDigest(%q) error = nil", invalid)
		}
	}
}

func validAppendRequest() AppendRequestV2 {
	return AppendRequestV2{
		AppendID:        "append-1",
		SessionID:       "session-1",
		ExpectedVersion: 7,
		CommandID:       "command-1",
		Authority:       WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1},
		Admission: &CommandAdmission{
			RunTurnRequestID: "request-1",
			RequestDigest:    Digest{1},
			TurnID:           "turn-1",
			ItemID:           "item-1",
		},
		Events: []ProposedEvent{domainEvent("event-1", "hello")},
	}
}

func domainEvent(id domain.EventID, text string) ProposedEvent {
	return ProposedEvent{
		ID:            id,
		SchemaVersion: 1,
		OccurredAt:    time.Date(2026, 8, 13, 1, 2, 3, 4, time.UTC),
		Event:         domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: text},
	}
}
