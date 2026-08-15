package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type contractStore struct{}

func (*contractStore) ReadStream(context.Context, ReadStreamRequest) (StreamPage, error) {
	return StreamPage{}, nil
}

func (*contractStore) Append(context.Context, AppendRequestV2) (CommitReceipt, error) {
	return CommitReceipt{}, nil
}

func (*contractStore) ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error) {
	return AppendResolution{}, nil
}

func (*contractStore) FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error) {
	return CommandRequestLookup{}, nil
}

var _ EventStoreV2 = (*contractStore)(nil)

func TestParseRuntimeIDRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{"", "   ", " runtime-1", "runtime-1 ", "runtime-\xff"} {
		if _, err := ParseRuntimeID(input); err == nil {
			t.Fatalf("ParseRuntimeID(%q) error = nil", input)
		}
	}
	got, err := ParseRuntimeID("runtime-1")
	if err != nil || got != RuntimeID("runtime-1") {
		t.Fatalf("ParseRuntimeID() = %q, %v", got, err)
	}
}

func TestWriterAuthorityRequiresNonZeroFencingToken(t *testing.T) {
	for _, authority := range []WriterAuthority{
		{RuntimeID: "runtime-1"},
		{RuntimeID: " runtime-1", FencingToken: 1},
		{RuntimeID: "runtime-1", FencingToken: 1},
	} {
		err := authority.Validate()
		if authority.RuntimeID == "runtime-1" && authority.FencingToken == 1 {
			if err != nil {
				t.Fatalf("Validate(%#v) = %v", authority, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("Validate(%#v) error = nil", authority)
		}
	}
}

func TestDigestTextIsLowerCaseHexAndComparable(t *testing.T) {
	digest := Digest{0xAB, 0xCD, 0xEF}
	if got, want := digest.String(), "abcdef"+strings.Repeat("00", 29); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	got, err := digest.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error = %v", err)
	}
	if want := []byte(digest.String()); string(got) != string(want) {
		t.Fatalf("MarshalText() = %q, want %q", got, want)
	}
	seen := map[Digest]string{digest: "present"}
	if got := seen[Digest{0xAB, 0xCD, 0xEF}]; got != "present" {
		t.Fatalf("Digest is not comparable, lookup = %q", got)
	}
}

func TestV2EnumsHaveStableStrings(t *testing.T) {
	for _, test := range []struct {
		got  string
		want string
	}{
		{string(AppendResolutionCommitted), "committed"},
		{string(AppendResolutionNotFound), "not_found"},
		{string(AppendResolutionIdentityMismatch), "identity_mismatch"},
		{string(CommandRequestLookupFound), "found"},
		{string(CommandRequestLookupNotFound), "not_found"},
		{string(CommandRequestLookupIdentityMismatch), "identity_mismatch"},
	} {
		if test.got != test.want {
			t.Errorf("enum = %q, want %q", test.got, test.want)
		}
	}
}

func TestV2ResultsRequireReceiptsAndRecordsOnlyForFoundKinds(t *testing.T) {
	receipt := &CommitReceipt{AppendID: "append-1"}
	record := &CommandRequestRecord{RunTurnRequestID: "request-1"}
	for _, test := range []struct {
		name  string
		valid bool
		err   error
	}{
		{"committed append with receipt", true, (AppendResolution{Kind: AppendResolutionCommitted, Receipt: receipt}).Validate()},
		{"committed append without receipt", false, (AppendResolution{Kind: AppendResolutionCommitted}).Validate()},
		{"not found append with receipt", false, (AppendResolution{Kind: AppendResolutionNotFound, Receipt: receipt}).Validate()},
		{"not found append without receipt", true, (AppendResolution{Kind: AppendResolutionNotFound}).Validate()},
		{"mismatch append with receipt", false, (AppendResolution{Kind: AppendResolutionIdentityMismatch, Receipt: receipt}).Validate()},
		{"mismatch append without receipt", true, (AppendResolution{Kind: AppendResolutionIdentityMismatch}).Validate()},
		{"found request with record", true, (CommandRequestLookup{Kind: CommandRequestLookupFound, Record: record}).Validate()},
		{"found request without record", false, (CommandRequestLookup{Kind: CommandRequestLookupFound}).Validate()},
		{"not found request with record", false, (CommandRequestLookup{Kind: CommandRequestLookupNotFound, Record: record}).Validate()},
		{"not found request without record", true, (CommandRequestLookup{Kind: CommandRequestLookupNotFound}).Validate()},
		{"mismatch request with record", false, (CommandRequestLookup{Kind: CommandRequestLookupIdentityMismatch, Record: record}).Validate()},
		{"mismatch request without record", true, (CommandRequestLookup{Kind: CommandRequestLookupIdentityMismatch}).Validate()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if (test.err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %t", test.err, test.valid)
			}
		})
	}
}

var allStoreErrorCodes = []StoreErrorCode{
	StoreCodeInvalidRead,
	StoreCodeInvalidAppend,
	StoreCodeVersionConflict,
	StoreCodeAppendIdentityMismatch,
	StoreCodeCommandRequestConflict,
	StoreCodeCommandIdentityMismatch,
	StoreCodeDomainIdentityConflict,
	StoreCodeWriterFenced,
	StoreCodeUnavailable,
	StoreCodeCommitOutcomeUnknown,
	StoreCodeCorrupt,
}

func TestStoreErrorCodesHaveStableStrings(t *testing.T) {
	for _, test := range []struct {
		code StoreErrorCode
		want string
	}{
		{StoreCodeInvalidRead, "invalid_read"},
		{StoreCodeInvalidAppend, "invalid_append"},
		{StoreCodeVersionConflict, "version_conflict"},
		{StoreCodeAppendIdentityMismatch, "append_identity_mismatch"},
		{StoreCodeCommandRequestConflict, "command_request_conflict"},
		{StoreCodeCommandIdentityMismatch, "command_identity_mismatch"},
		{StoreCodeDomainIdentityConflict, "domain_identity_conflict"},
		{StoreCodeWriterFenced, "writer_fenced"},
		{StoreCodeUnavailable, "store_unavailable"},
		{StoreCodeCommitOutcomeUnknown, "commit_outcome_unknown"},
		{StoreCodeCorrupt, "store_corrupt"},
	} {
		if got := string(test.code); got != test.want {
			t.Errorf("code = %q, want %q", got, test.want)
		}
	}
}

func TestStoreErrorCommitKnowledge(t *testing.T) {
	for _, code := range allStoreErrorCodes {
		err := &StoreError{Code: code, MayHaveCommitted: code == StoreCodeCommitOutcomeUnknown}
		if got := err.MayHaveCommitted; got != (code == StoreCodeCommitOutcomeUnknown) {
			t.Fatalf("code %q may_have_committed = %t", code, got)
		}
		if !IsStoreCode(fmt.Errorf("wrapped: %w", err), code) {
			t.Fatalf("code %q not found", code)
		}
	}
}

func TestNewStoreErrorRejectsUnsupportedCommitKnowledge(t *testing.T) {
	if _, err := NewStoreError(StoreError{Code: StoreCodeVersionConflict, MayHaveCommitted: true}); err == nil {
		t.Fatal("NewStoreError() accepted definite-absence code with may-have-committed")
	}
	if _, err := NewStoreError(StoreError{Code: StoreCodeCommitOutcomeUnknown, MayHaveCommitted: true}); err != nil {
		t.Fatalf("NewStoreError() = %v, want unknown outcome accepted", err)
	}
}

func TestStoreErrorRendersOnlySafeMetadata(t *testing.T) {
	cause := errors.New("secret payload from another session")
	err, buildErr := NewStoreError(StoreError{
		Code: StoreCodeVersionConflict, SessionID: "session-1", ExpectedVersion: 3, ActualVersion: 5,
		IdentityKind: "turn", Cause: cause,
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	text := err.Error()
	for _, forbidden := range []string{cause.Error(), "secret", "another session"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Error() leaked %q: %q", forbidden, text)
		}
	}
	if !strings.Contains(text, "version_conflict") || !strings.Contains(text, "session-1") || !strings.Contains(text, "expected=3") || !strings.Contains(text, "actual=5") {
		t.Fatalf("Error() = %q, want safe stable metadata", text)
	}
	if !errors.Is(err, cause) {
		t.Fatal("StoreError did not preserve cause for programmatic inspection")
	}
}

func TestV2RequestCarriesImmutableAppendFacts(t *testing.T) {
	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	request := AppendRequestV2{
		AppendID: "append-1", SessionID: "session-1", ExpectedVersion: 7, CommandID: "command-1",
		Authority: WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1},
		Admission: &CommandAdmission{RunTurnRequestID: "request-1", TurnID: "turn-1", ItemID: "item-1"},
		Events:    []ProposedEvent{{ID: "event-1", SchemaVersion: 1, OccurredAt: now, Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}},
	}
	if request.AppendID != "append-1" || request.Admission.RunTurnRequestID != "request-1" || request.Events[0].OccurredAt != now {
		t.Fatalf("AppendRequestV2 lost immutable facts: %#v", request)
	}
}
